package natsauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func credsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nats-credentials.json")
}

func TestLoadOrCreateGeneratesOnFirstBoot(t *testing.T) {
	path := credsPath(t)

	creds, src, err := LoadOrCreate(path)
	require.NoError(t, err)
	assert.Equal(t, SourceCreated, src)
	assert.True(t, creds.Complete(), "all external roles must get a secret")

	// Secrets must be distinct and URL-safe so they can ride in nats://u:p@host.
	seen := map[string]bool{}
	for _, role := range []string{RoleOperator, RoleAdapter, RoleFanout} {
		s := creds.Secret(role)
		require.NotEmpty(t, s, "role %s", role)
		assert.False(t, seen[s], "secrets must differ across roles")
		seen[s] = true
		assert.NotContains(t, s, "/", "secret must be URL-safe")
		assert.NotContains(t, s, "+", "secret must be URL-safe")
		assert.NotContains(t, s, "=", "secret must be URL-safe")
		assert.NotContains(t, s, ":", "secret must not break user:password parsing")
		assert.GreaterOrEqual(t, len(s), 40, "secret must carry ~256 bits")
	}

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(credentialsFileMode), info.Mode().Perm(), "credentials must not be world-readable")
}

func TestLoadOrCreateIsIdempotent(t *testing.T) {
	path := credsPath(t)

	first, src, err := LoadOrCreate(path)
	require.NoError(t, err)
	require.Equal(t, SourceCreated, src)

	second, src, err := LoadOrCreate(path)
	require.NoError(t, err)
	assert.Equal(t, SourceFile, src, "a second boot must read, not regenerate")
	assert.Equal(t, first, second, "regenerating would lock out every deployed adapter")
}

func TestLoadOrCreateCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "nats-credentials.json")

	_, src, err := LoadOrCreate(path)
	require.NoError(t, err)
	assert.Equal(t, SourceCreated, src)

	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(credentialsDirMode), info.Mode().Perm())
}

func TestEnvOverridesFile(t *testing.T) {
	path := credsPath(t)
	t.Setenv(EnvOperatorPassword, "env-op")
	t.Setenv(EnvAdapterPassword, "env-ad")
	t.Setenv(EnvFanoutPassword, "env-fo")

	creds, src, err := LoadOrCreate(path)
	require.NoError(t, err)
	assert.Equal(t, SourceEnv, src)
	assert.Equal(t, Credentials{Operator: "env-op", Adapter: "env-ad", Fanout: "env-fo"}, creds)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "env-provided secrets must never touch disk")
}

func TestPartialEnvIsAnError(t *testing.T) {
	path := credsPath(t)
	t.Setenv(EnvOperatorPassword, "env-op")
	t.Setenv(EnvAdapterPassword, "env-ad")
	// EnvFanoutPassword deliberately unset.

	_, _, err := LoadOrCreate(path)
	require.ErrorIs(t, err, ErrIncompleteEnv,
		"a half-configured deployment must fail loudly, not silently fall back to a file")
	assert.Contains(t, err.Error(), EnvFanoutPassword, "the error must name the missing variable")
}

func TestLoadRejectsIncompleteFile(t *testing.T) {
	path := credsPath(t)
	require.NoError(t, os.WriteFile(path, []byte(`{"operator":"a","adapter":"b"}`), credentialsFileMode))

	_, _, err := LoadOrCreate(path)
	require.Error(t, err, "a truncated credentials file must not boot a half-authorized server")
	assert.Contains(t, err.Error(), "fanout")
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	path := credsPath(t)
	require.NoError(t, os.WriteFile(path, []byte(`{not json`), credentialsFileMode))

	_, _, err := LoadOrCreate(path)
	require.Error(t, err)
}

func TestCreatedFileIsValidJSONWithoutCoreSecret(t *testing.T) {
	path := credsPath(t)
	_, _, err := LoadOrCreate(path)
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.ElementsMatch(t, []string{"operator", "adapter", "fanout"}, keysOf(decoded),
		"the core credential is ephemeral and must never be persisted")
}

func TestLooseFilePermissionsAreReported(t *testing.T) {
	path := credsPath(t)
	_, _, err := LoadOrCreate(path)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o644))

	creds, src, err := LoadOrCreate(path)
	require.NoError(t, err, "a loose mode must not block boot")
	assert.Equal(t, SourceFile, src)
	assert.True(t, creds.Complete())
	assert.True(t, InsecureMode(path), "callers need a way to warn about world-readable secrets")
}

func TestEphemeralCoreIsUniquePerCall(t *testing.T) {
	a, err := NewEphemeralCore()
	require.NoError(t, err)
	b, err := NewEphemeralCore()
	require.NoError(t, err)

	assert.Equal(t, RoleCore, a.Username)
	assert.NotEqual(t, a.Secret, b.Secret, "each boot must mint a fresh core credential")
	assert.GreaterOrEqual(t, len(a.Secret), 40)
}

func TestSecretUnknownRoleIsEmpty(t *testing.T) {
	c := Credentials{Operator: "o", Adapter: "a", Fanout: "f"}
	assert.Empty(t, c.Secret(RoleCore), "core is not stored in Credentials")
	assert.Empty(t, c.Secret(RoleLegacy), "legacy authenticates anonymously")
	assert.Empty(t, c.Secret("nope"))
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// guard against accidentally logging secrets in the file format
func TestFileDoesNotContainCoreRoleName(t *testing.T) {
	path := credsPath(t)
	_, _, err := LoadOrCreate(path)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(raw), `"core"`))
}
