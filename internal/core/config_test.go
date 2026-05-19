package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCoreConfig_JetStreamPolicy(t *testing.T) {
	cfg := DefaultCoreConfig()

	assert.Equal(t, "PLATFORM_DATA", cfg.JetStream.Stream.Name)
	assert.Equal(t, []string{"platform.data.>"}, cfg.JetStream.Stream.Subjects)
	assert.Equal(t, "file", cfg.JetStream.Stream.Storage)
	assert.Equal(t, 7*24*time.Hour, cfg.JetStream.Stream.MaxAge)
	assert.Equal(t, int64(1024*1024*1024), cfg.JetStream.Stream.MaxBytes)
	assert.Equal(t, 1, cfg.JetStream.Stream.Replicas)
	assert.Equal(t, "limits", cfg.JetStream.Stream.Retention)
	assert.Equal(t, "old", cfg.JetStream.Stream.Discard)
	assert.Equal(t, "embedded", cfg.Storage.MigrationsDir)
	assert.True(t, cfg.Storage.AutoMigrate)
}

func TestDefaultCoreConfig_AssetRegistrationMode(t *testing.T) {
	cfg := DefaultCoreConfig()

	assert.Equal(t, RegistrationModeAuto, cfg.AssetRegistration.Mode)
}

func TestDefaultCoreConfig_Alarm(t *testing.T) {
	cfg := DefaultCoreConfig()

	assert.Equal(t, 5, cfg.Alarm.WindowSeconds)
	assert.Equal(t, DefaultTraversalMaxDepth, cfg.Alarm.MaxTraversalDepth)
}

func TestDefaultCoreConfig_Constraints(t *testing.T) {
	cfg := DefaultCoreConfig()

	assert.Equal(t, ConstraintsEnforcementWarn, cfg.Constraints.Enforcement)
}

func TestDefaultCoreConfig_HTTP(t *testing.T) {
	cfg := DefaultCoreConfig()

	assert.False(t, cfg.HTTP.Enabled)
	assert.Equal(t, "127.0.0.1:8080", cfg.HTTP.Address)
	assert.Equal(t, "EDG_HTTP_TOKEN", cfg.HTTP.TokenEnv)
}

func TestLoadCoreConfig_JetStreamPolicyFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
nats:
  port: 4333
  http_port: 8333
  store_dir: /tmp/jetstream
storage:
  metadata_db: /tmp/metadata.db
  data_dir: /tmp/data
  migrations_dir: embedded
  auto_migrate: false
templates:
  dir: /tmp/templates
jetstream:
  stream:
    name: TEST_DATA
    subjects:
      - custom.data.>
    storage: memory
    max_age: 1h
    max_bytes: 1048576
    replicas: 1
    retention: limits
    discard: new
  dead_letter_subject: custom.data.deadletter
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadCoreConfig(path)
	require.NoError(t, err)

	assert.Equal(t, 4333, cfg.NATS.Port)
	assert.Equal(t, 8333, cfg.NATS.HTTPPort)
	assert.Equal(t, "/tmp/jetstream", cfg.NATS.StoreDir)
	assert.Equal(t, "/tmp/metadata.db", cfg.Storage.MetadataDB)
	assert.Equal(t, "embedded", cfg.Storage.MigrationsDir)
	assert.False(t, cfg.Storage.AutoMigrate)
	assert.Equal(t, "/tmp/templates", cfg.Templates.Dir)
	assert.Equal(t, "TEST_DATA", cfg.JetStream.Stream.Name)
	assert.Equal(t, []string{"custom.data.>"}, cfg.JetStream.Stream.Subjects)
	assert.Equal(t, time.Hour, cfg.JetStream.Stream.MaxAge)
	assert.Equal(t, int64(1048576), cfg.JetStream.Stream.MaxBytes)
	assert.Equal(t, "custom.data.deadletter", cfg.JetStream.DeadLetterSubject)

	streamConfig, err := cfg.JetStream.Stream.NATSConfig()
	require.NoError(t, err)
	assert.Equal(t, nats.MemoryStorage, streamConfig.Storage)
	assert.Equal(t, nats.DiscardNew, streamConfig.Discard)
	assert.Equal(t, nats.LimitsPolicy, streamConfig.Retention)
}

func TestLoadCoreConfig_AssetRegistrationModeFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
asset_registration:
  mode: manual
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadCoreConfig(path)
	require.NoError(t, err)

	assert.Equal(t, RegistrationModeManual, cfg.AssetRegistration.Mode)
}

func TestLoadCoreConfig_AlarmFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
alarm:
  window_seconds: 2
  max_traversal_depth: 4
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadCoreConfig(path)
	require.NoError(t, err)

	assert.Equal(t, 2, cfg.Alarm.WindowSeconds)
	assert.Equal(t, 4, cfg.Alarm.MaxTraversalDepth)
}

func TestLoadCoreConfig_ConstraintsFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
constraints:
  enforcement: enforce
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadCoreConfig(path)
	require.NoError(t, err)

	assert.Equal(t, ConstraintsEnforcementEnforce, cfg.Constraints.Enforcement)
}

func TestLoadCoreConfig_HTTPFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
http:
  enabled: true
  address: 127.0.0.1:9090
  token_env: CUSTOM_HTTP_TOKEN
  cors_allowed_origins:
    - http://localhost:3000
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadCoreConfig(path)
	require.NoError(t, err)

	assert.True(t, cfg.HTTP.Enabled)
	assert.Equal(t, "127.0.0.1:9090", cfg.HTTP.Address)
	assert.Equal(t, "CUSTOM_HTTP_TOKEN", cfg.HTTP.TokenEnv)
	assert.Equal(t, []string{"http://localhost:3000"}, cfg.HTTP.CORSAllowedOrigins)
}

func TestLoadCoreConfig_RejectsInvalidAssetRegistrationMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
asset_registration:
  mode: disabled
`), 0644)
	require.NoError(t, err)

	_, err = LoadCoreConfig(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid asset_registration.mode: "disabled"`)
}

func TestLoadCoreConfig_RejectsInvalidAlarmConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
alarm:
  window_seconds: -1
`), 0644)
	require.NoError(t, err)

	_, err = LoadCoreConfig(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid alarm.window_seconds")
}

func TestLoadCoreConfig_RejectsInvalidConstraintsEnforcement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
constraints:
  enforcement: audit
`), 0644)
	require.NoError(t, err)

	_, err = LoadCoreConfig(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid constraints.enforcement")
}

func TestJetStreamStreamConfig_NATSConfigRejectsInvalidPolicies(t *testing.T) {
	tests := []struct {
		name   string
		config JetStreamStreamConfig
	}{
		{
			name: "storage",
			config: JetStreamStreamConfig{
				Name:      "TEST",
				Subjects:  []string{"test.>"},
				Storage:   "disk",
				MaxAge:    time.Hour,
				MaxBytes:  1024,
				Replicas:  1,
				Retention: "limits",
				Discard:   "old",
			},
		},
		{
			name: "retention",
			config: JetStreamStreamConfig{
				Name:      "TEST",
				Subjects:  []string{"test.>"},
				Storage:   "file",
				MaxAge:    time.Hour,
				MaxBytes:  1024,
				Replicas:  1,
				Retention: "forever",
				Discard:   "old",
			},
		},
		{
			name: "discard",
			config: JetStreamStreamConfig{
				Name:      "TEST",
				Subjects:  []string{"test.>"},
				Storage:   "file",
				MaxAge:    time.Hour,
				MaxBytes:  1024,
				Replicas:  1,
				Retention: "limits",
				Discard:   "middle",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.config.NATSConfig()
			require.Error(t, err)
		})
	}
}
