package natsauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables that override the credentials file. When all three are
// set the file is neither created nor read, so secrets can be injected by a
// container orchestrator without ever landing on disk. The naming mirrors the
// existing EDG_SINK_URL / EDG_HTTP_TOKEN / EDG_CORE_CONFIG convention.
const (
	EnvOperatorPassword = "EDG_NATS_OPERATOR_PASSWORD"
	EnvAdapterPassword  = "EDG_NATS_ADAPTER_PASSWORD"
	EnvFanoutPassword   = "EDG_NATS_FANOUT_PASSWORD"
	EnvCredentialsFile  = "EDG_NATS_CREDENTIALS_FILE"
)

// credentialsFileMode and credentialsDirMode keep secrets off other local
// accounts. LoadOrCreate warns (but does not fail) when an existing file is
// more permissive.
const (
	credentialsFileMode = 0o600
	credentialsDirMode  = 0o700
)

// ErrIncompleteEnv is returned when some, but not all, password environment
// variables are set. Partially configured credentials are a deployment mistake
// that must fail loudly rather than silently fall back to a generated file.
var ErrIncompleteEnv = errors.New("incomplete NATS credential environment")

// Credentials holds the on-disk (or env-provided) secrets for the roles that
// external clients authenticate as. RoleCore is deliberately absent: it is
// ephemeral (see Ephemeral). RoleLegacy is absent because compat-mode
// anonymous connections present no credential at all.
type Credentials struct {
	Operator string `json:"operator"`
	Adapter  string `json:"adapter"`
	Fanout   string `json:"fanout"`
}

// Secret returns the password for a role, or "" when the role has none.
func (c Credentials) Secret(role string) string {
	switch role {
	case RoleOperator:
		return c.Operator
	case RoleAdapter:
		return c.Adapter
	case RoleFanout:
		return c.Fanout
	default:
		return ""
	}
}

// Complete reports whether every external role has a secret.
func (c Credentials) Complete() bool {
	return c.Operator != "" && c.Adapter != "" && c.Fanout != ""
}

// Source describes where a Credentials value came from, for operator-facing
// logging.
type Source string

const (
	SourceEnv     Source = "environment"
	SourceFile    Source = "file"
	SourceCreated Source = "created"
)

// LoadOrCreate resolves the credentials for the external roles.
//
// Resolution order:
//  1. All three password environment variables set -> use them, touch no file.
//  2. Some but not all set -> ErrIncompleteEnv.
//  3. path exists -> read it.
//  4. otherwise -> generate, write atomically with mode 0600, return SourceCreated.
func LoadOrCreate(path string) (Credentials, Source, error) {
	creds, n, missing := fromEnv()
	switch {
	case n == 3:
		return creds, SourceEnv, nil
	case n > 0:
		return Credentials{}, "", fmt.Errorf("%w: %s must also be set", ErrIncompleteEnv, strings.Join(missing, ", "))
	}

	if path == "" {
		return Credentials{}, "", fmt.Errorf("natsauth: empty credentials path")
	}

	switch loaded, err := readFile(path); {
	case err == nil:
		return loaded, SourceFile, nil
	case !errors.Is(err, os.ErrNotExist):
		return Credentials{}, "", err
	}

	return create(path)
}

// InsecureMode reports whether an existing credentials file is readable beyond
// its owner. Callers warn instead of failing: refusing to boot over a file mode
// would be a worse outage than the exposure it prevents.
func InsecureMode(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o077 != 0
}

// fromEnv reads the password environment variables, returning how many were set
// and the names of those that were not.
func fromEnv() (creds Credentials, set int, missing []string) {
	for _, e := range []struct {
		name string
		dst  *string
	}{
		{EnvOperatorPassword, &creds.Operator},
		{EnvAdapterPassword, &creds.Adapter},
		{EnvFanoutPassword, &creds.Fanout},
	} {
		if v := os.Getenv(e.name); v != "" {
			*e.dst = v
			set++
		} else {
			missing = append(missing, e.name)
		}
	}
	return creds, set, missing
}

func readFile(path string) (Credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var creds Credentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return Credentials{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if !creds.Complete() {
		return Credentials{}, fmt.Errorf("%s is missing a secret for one of: operator, adapter, fanout", path)
	}
	return creds, nil
}

// create generates fresh secrets and writes them atomically. The temp file is
// created with the final mode so the secret is never briefly world-readable.
func create(path string) (Credentials, Source, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, credentialsDirMode); err != nil {
			return Credentials{}, "", fmt.Errorf("create credentials directory: %w", err)
		}
	}

	var creds Credentials
	for _, dst := range []*string{&creds.Operator, &creds.Adapter, &creds.Fanout} {
		secret, err := newSecret()
		if err != nil {
			return Credentials{}, "", fmt.Errorf("generate credential: %w", err)
		}
		*dst = secret
	}

	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return Credentials{}, "", err
	}
	raw = append(raw, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, credentialsFileMode); err != nil {
		return Credentials{}, "", fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return Credentials{}, "", fmt.Errorf("install credentials: %w", err)
	}
	return creds, SourceCreated, nil
}
