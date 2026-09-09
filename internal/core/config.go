package core

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"gopkg.in/yaml.v3"
)

const (
	DefaultValidatedDataSubject = "platform.data.validated"
	DefaultDeadLetterSubject    = "platform.data.deadletter"
)

const (
	UnknownAssetPolicyPassThrough = "pass_through"
	UnknownAssetPolicyDeadLetter  = "dead_letter"
)

// NATS authorization modes. These mirror internal/natsauth's Mode* constants;
// internal/core does not import internal/natsauth so that natsauth can import
// core subjects in its tests without a cycle.
const (
	NATSAuthModeCompat = "compat"
	NATSAuthModeStrict = "strict"
	NATSAuthModeOff    = "off"
)

// EnvNATSCredentialsFile overrides nats.auth.credentials_file, matching the
// EDG_SINK_URL / EDG_HTTP_TOKEN convention of naming the variable in config
// rather than storing the value there.
const EnvNATSCredentialsFile = "EDG_NATS_CREDENTIALS_FILE"

// defaultCredentialsFileName is created under storage.data_dir when
// nats.auth.credentials_file is empty.
const defaultCredentialsFileName = "nats-credentials.json"

// CoreConfig contains runtime settings for the embedded core process.
type CoreConfig struct {
	NATS               NATSConfig        `yaml:"nats"`
	Storage            StorageConfig     `yaml:"storage"`
	Templates          TemplateConfig    `yaml:"templates"`
	Logging            LoggingConfig     `yaml:"logging"`
	JetStream          JetStreamConfig   `yaml:"jetstream"`
	UnknownAssetPolicy string            `yaml:"unknown_asset_policy"`
	Alarm              AlarmConfig       `yaml:"alarm"`
	Constraints        ConstraintsConfig `yaml:"constraints"`
	HTTP               HTTPConfig        `yaml:"http"`
	Sink               SinkConfig        `yaml:"sink"`
	Adapters           AdaptersConfig    `yaml:"adapters"`
}

// AdaptersConfig configures the adapter runtime-status registry (ADR 0008).
type AdaptersConfig struct {
	// Enabled runs the registry and its subscriptions.
	Enabled bool `yaml:"enabled"`
	// StaleAfterFloor is the minimum deadline, so a 1s poller is not declared
	// stale by a momentary hiccup. The effective deadline is
	// max(3 x announced_interval, this).
	StaleAfterFloor time.Duration `yaml:"stale_after_floor"`
	// MinInterval and MaxInterval clamp the interval an adapter announces, so
	// a misconfigured adapter cannot pin the deadline at either extreme.
	MinInterval time.Duration `yaml:"min_interval"`
	MaxInterval time.Duration `yaml:"max_interval"`
	// ForgetAfter drops an adapter that has been stale this long.
	ForgetAfter time.Duration `yaml:"forget_after"`
	// ProbeOnMiss actively pings an adapter whose deadline passed, turning a
	// missed heartbeat into a confirmed verdict instead of a guess.
	ProbeOnMiss bool `yaml:"probe_on_miss"`
	// ProbeTimeout bounds each probe.
	ProbeTimeout time.Duration `yaml:"probe_timeout"`
	// MaxConcurrentProbes stops a fleet-wide partition from becoming a probe
	// storm.
	MaxConcurrentProbes int `yaml:"max_concurrent_probes"`
}

type NATSConfig struct {
	// Host is the client-port bind address. Defaults to 0.0.0.0: an edge
	// gateway exists to serve remote adapters and sibling containers.
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// HTTPHost is the monitoring-port bind address. Defaults to 127.0.0.1
	// because nats-server serves /varz, /connz and /debug/vars there with no
	// authentication mechanism of any kind.
	HTTPHost string         `yaml:"http_host"`
	HTTPPort int            `yaml:"http_port"`
	LogLevel string         `yaml:"log_level"`
	StoreDir string         `yaml:"store_dir"`
	Auth     NATSAuthConfig `yaml:"auth"`
}

// NATSAuthConfig selects the authorization posture of the embedded server.
// See ADR 0007 and internal/natsauth.
type NATSAuthConfig struct {
	// Mode is one of NATSAuthModeCompat, NATSAuthModeStrict, NATSAuthModeOff.
	Mode string `yaml:"mode"`
	// CredentialsFile overrides the default <storage.data_dir>/nats-credentials.json.
	// Resolve it with CoreConfig.NATSCredentialsFile, which also honours
	// EDG_NATS_CREDENTIALS_FILE.
	CredentialsFile string `yaml:"credentials_file"`
}

type StorageConfig struct {
	MetadataDB    string `yaml:"metadata_db"`
	DataDir       string `yaml:"data_dir"`
	MigrationsDir string `yaml:"migrations_dir"`
	AutoMigrate   bool   `yaml:"auto_migrate"`
}

type TemplateConfig struct {
	// Dir is the seed/import directory for templates. Templates are stored in
	// SQLite; this directory is imported into an empty DB on first boot and is
	// the default target for --import-templates / --export-templates.
	Dir string `yaml:"dir"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type JetStreamConfig struct {
	Stream            JetStreamStreamConfig `yaml:"stream"`
	ValidatedSubject  string                `yaml:"validated_subject"`
	DeadLetterSubject string                `yaml:"dead_letter_subject"`
}

type AlarmConfig struct {
	WindowSeconds     int `yaml:"window_seconds"`
	MaxTraversalDepth int `yaml:"max_traversal_depth"`
}

type ConstraintsConfig struct {
	Enforcement string `yaml:"enforcement"`
}

type HTTPConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Address            string   `yaml:"address"`
	TokenEnv           string   `yaml:"token_env"`
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
	WebUIEnabled       bool     `yaml:"webui_enabled"`
}

// SinkConfig configures the built-in VictoriaMetrics sink. When enabled, core
// runs a durable JetStream pull consumer that writes validated data to a
// VictoriaMetrics-compatible endpoint, replacing the external Telegraf bridge.
type SinkConfig struct {
	Enabled        bool          `yaml:"enabled"`
	URL            string        `yaml:"url"`
	ConsumerName   string        `yaml:"consumer_name"`
	Measurement    string        `yaml:"measurement"`
	BatchMaxSize   int           `yaml:"batch_max_size"`
	FlushInterval  time.Duration `yaml:"flush_interval"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

type JetStreamStreamConfig struct {
	Name      string        `yaml:"name"`
	Subjects  []string      `yaml:"subjects"`
	Storage   string        `yaml:"storage"`
	MaxAge    time.Duration `yaml:"max_age"`
	MaxBytes  int64         `yaml:"max_bytes"`
	Replicas  int           `yaml:"replicas"`
	Retention string        `yaml:"retention"`
	Discard   string        `yaml:"discard"`
}

// DefaultCoreConfig returns the ADR-backed reliability defaults.
func DefaultCoreConfig() CoreConfig {
	return CoreConfig{
		NATS: NATSConfig{
			Host:     "0.0.0.0",
			Port:     4222,
			HTTPHost: "127.0.0.1",
			HTTPPort: 8222,
			LogLevel: "info",
			StoreDir: "./data/jetstream",
			Auth:     NATSAuthConfig{Mode: NATSAuthModeCompat},
		},
		Storage: StorageConfig{
			MetadataDB:    "./data/metadata.db",
			DataDir:       "./data",
			MigrationsDir: "embedded",
			AutoMigrate:   true,
		},
		Templates: TemplateConfig{
			Dir: "./templates",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		JetStream: JetStreamConfig{
			ValidatedSubject:  DefaultValidatedDataSubject,
			DeadLetterSubject: DefaultDeadLetterSubject,
			Stream: JetStreamStreamConfig{
				Name:      "PLATFORM_DATA",
				Subjects:  []string{"platform.data.>"},
				Storage:   "file",
				MaxAge:    7 * 24 * time.Hour,
				MaxBytes:  1024 * 1024 * 1024,
				Replicas:  1,
				Retention: "limits",
				Discard:   "old",
			},
		},
		UnknownAssetPolicy: UnknownAssetPolicyPassThrough,
		Alarm: AlarmConfig{
			WindowSeconds:     int(DefaultAlarmWindow / time.Second),
			MaxTraversalDepth: DefaultTraversalMaxDepth,
		},
		Constraints: ConstraintsConfig{
			Enforcement: ConstraintsEnforcementWarn,
		},
		HTTP: HTTPConfig{
			Enabled:  false,
			Address:  "127.0.0.1:8080",
			TokenEnv: "EDG_HTTP_TOKEN",
		},
		Adapters: AdaptersConfig{
			Enabled:             true,
			StaleAfterFloor:     DefaultAdapterStaleFloor,
			MinInterval:         DefaultAdapterMinInterval,
			MaxInterval:         DefaultAdapterMaxInterval,
			ForgetAfter:         DefaultAdapterForgetAfter,
			ProbeOnMiss:         true,
			ProbeTimeout:        2 * time.Second,
			MaxConcurrentProbes: DefaultAdapterMaxProbes,
		},
		Sink: SinkConfig{
			Enabled:        true,
			URL:            "http://localhost:8428",
			ConsumerName:   "edg-core-vm-sink",
			Measurement:    "edg_data",
			BatchMaxSize:   500,
			FlushInterval:  time.Second,
			RequestTimeout: 5 * time.Second,
		},
	}
}

func LoadCoreConfig(path string) (CoreConfig, error) {
	cfg := DefaultCoreConfig()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read core config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse core config: %w", err)
	}
	warnLegacyConfigKeys(data)
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// warnLegacyConfigKeys logs a one-time warning when removed config keys are still
// present in a user's YAML (yaml.Unmarshal silently ignores unknown keys).
func warnLegacyConfigKeys(data []byte) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	if _, ok := raw["asset_registration"]; ok {
		log.Printf("[Config] 'asset_registration' is removed; use 'unknown_asset_policy' (pass_through|dead_letter)")
	}
}

func (c *CoreConfig) applyDefaults() {
	defaults := DefaultCoreConfig()

	if c.NATS.Host == "" {
		c.NATS.Host = defaults.NATS.Host
	}
	if c.NATS.Port == 0 {
		c.NATS.Port = defaults.NATS.Port
	}
	if c.NATS.HTTPHost == "" {
		c.NATS.HTTPHost = defaults.NATS.HTTPHost
	}
	if c.NATS.Auth.Mode == "" {
		c.NATS.Auth.Mode = defaults.NATS.Auth.Mode
	}
	if c.NATS.HTTPPort == 0 {
		c.NATS.HTTPPort = defaults.NATS.HTTPPort
	}
	if c.NATS.StoreDir == "" {
		c.NATS.StoreDir = defaults.NATS.StoreDir
	}
	if c.Storage.MetadataDB == "" {
		c.Storage.MetadataDB = defaults.Storage.MetadataDB
	}
	if c.Storage.DataDir == "" {
		c.Storage.DataDir = defaults.Storage.DataDir
	}
	if c.Storage.MigrationsDir == "" {
		c.Storage.MigrationsDir = defaults.Storage.MigrationsDir
	}
	if c.Templates.Dir == "" {
		c.Templates.Dir = defaults.Templates.Dir
	}
	if c.JetStream.ValidatedSubject == "" {
		c.JetStream.ValidatedSubject = defaults.JetStream.ValidatedSubject
	}
	if c.JetStream.DeadLetterSubject == "" {
		c.JetStream.DeadLetterSubject = defaults.JetStream.DeadLetterSubject
	}
	if c.UnknownAssetPolicy == "" {
		c.UnknownAssetPolicy = defaults.UnknownAssetPolicy
	}
	if c.Alarm.WindowSeconds == 0 {
		c.Alarm.WindowSeconds = defaults.Alarm.WindowSeconds
	}
	if c.Alarm.MaxTraversalDepth == 0 {
		c.Alarm.MaxTraversalDepth = defaults.Alarm.MaxTraversalDepth
	}
	if c.Constraints.Enforcement == "" {
		c.Constraints.Enforcement = defaults.Constraints.Enforcement
	}
	if c.HTTP.Address == "" {
		c.HTTP.Address = defaults.HTTP.Address
	}
	if c.HTTP.TokenEnv == "" {
		c.HTTP.TokenEnv = defaults.HTTP.TokenEnv
	}
	c.Sink.applyDefaults(defaults.Sink)
	c.Adapters.applyDefaults(defaults.Adapters)
	c.JetStream.Stream.applyDefaults(defaults.JetStream.Stream)
}

func (c *SinkConfig) applyDefaults(defaults SinkConfig) {
	if c.URL == "" {
		c.URL = defaults.URL
	}
	if c.ConsumerName == "" {
		c.ConsumerName = defaults.ConsumerName
	}
	if c.Measurement == "" {
		c.Measurement = defaults.Measurement
	}
	if c.BatchMaxSize == 0 {
		c.BatchMaxSize = defaults.BatchMaxSize
	}
	if c.FlushInterval == 0 {
		c.FlushInterval = defaults.FlushInterval
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = defaults.RequestTimeout
	}
}

func (c CoreConfig) validate() error {
	switch c.UnknownAssetPolicy {
	case UnknownAssetPolicyPassThrough, UnknownAssetPolicyDeadLetter:
	default:
		return fmt.Errorf("invalid unknown_asset_policy: %q (allowed: pass_through, dead_letter)", c.UnknownAssetPolicy)
	}
	switch c.NATS.Auth.Mode {
	case NATSAuthModeCompat, NATSAuthModeStrict:
	case NATSAuthModeOff:
		// Disabling authorization is only defensible when nothing off-box can
		// reach the port. Refusing this combination is the point of #104.
		if !isLoopbackHost(c.NATS.Host) {
			return fmt.Errorf(
				"invalid nats.auth.mode: %q requires a loopback nats.host (got %q); "+
					"an unauthenticated NATS port accepts master-data deletes from anyone who can reach it",
				c.NATS.Auth.Mode, c.NATS.Host)
		}
	default:
		return fmt.Errorf("invalid nats.auth.mode: %q (allowed: compat, strict, off)", c.NATS.Auth.Mode)
	}
	if c.Alarm.WindowSeconds <= 0 {
		return fmt.Errorf("invalid alarm.window_seconds: %d (must be > 0)", c.Alarm.WindowSeconds)
	}
	if c.Alarm.MaxTraversalDepth <= 0 {
		return fmt.Errorf("invalid alarm.max_traversal_depth: %d (must be > 0)", c.Alarm.MaxTraversalDepth)
	}
	switch c.Constraints.Enforcement {
	case ConstraintsEnforcementWarn, ConstraintsEnforcementEnforce, ConstraintsEnforcementDisabled:
	default:
		return fmt.Errorf("invalid constraints.enforcement: %q (allowed: warn, enforce, disabled)", c.Constraints.Enforcement)
	}
	if c.HTTP.Enabled && c.HTTP.Address == "" {
		return fmt.Errorf("http.address is required when http.enabled is true")
	}
	if c.Adapters.Enabled {
		if c.Adapters.MinInterval <= 0 {
			return fmt.Errorf("invalid adapters.min_interval: %s (must be > 0)", c.Adapters.MinInterval)
		}
		if c.Adapters.MaxInterval < c.Adapters.MinInterval {
			return fmt.Errorf("invalid adapters.max_interval: %s (must be >= adapters.min_interval %s)",
				c.Adapters.MaxInterval, c.Adapters.MinInterval)
		}
		if c.Adapters.MaxConcurrentProbes <= 0 {
			return fmt.Errorf("invalid adapters.max_concurrent_probes: %d (must be > 0)", c.Adapters.MaxConcurrentProbes)
		}
	}
	if c.Sink.Enabled {
		if c.Sink.URL == "" {
			return fmt.Errorf("sink.url is required when sink.enabled is true")
		}
		if c.Sink.BatchMaxSize <= 0 {
			return fmt.Errorf("invalid sink.batch_max_size: %d (must be > 0)", c.Sink.BatchMaxSize)
		}
		if c.Sink.FlushInterval <= 0 {
			return fmt.Errorf("invalid sink.flush_interval: %s (must be > 0)", c.Sink.FlushInterval)
		}
		if c.Sink.RequestTimeout <= 0 {
			return fmt.Errorf("invalid sink.request_timeout: %s (must be > 0)", c.Sink.RequestTimeout)
		}
	}
	return nil
}

func (c *JetStreamStreamConfig) applyDefaults(defaults JetStreamStreamConfig) {
	if c.Name == "" {
		c.Name = defaults.Name
	}
	if len(c.Subjects) == 0 {
		c.Subjects = defaults.Subjects
	}
	if c.Storage == "" {
		c.Storage = defaults.Storage
	}
	if c.MaxAge == 0 {
		c.MaxAge = defaults.MaxAge
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = defaults.MaxBytes
	}
	if c.Replicas == 0 {
		c.Replicas = defaults.Replicas
	}
	if c.Retention == "" {
		c.Retention = defaults.Retention
	}
	if c.Discard == "" {
		c.Discard = defaults.Discard
	}
}

func (c JetStreamStreamConfig) NATSConfig() (*nats.StreamConfig, error) {
	storage, err := parseStoragePolicy(c.Storage)
	if err != nil {
		return nil, err
	}
	retention, err := parseRetentionPolicy(c.Retention)
	if err != nil {
		return nil, err
	}
	discard, err := parseDiscardPolicy(c.Discard)
	if err != nil {
		return nil, err
	}

	return &nats.StreamConfig{
		Name:      c.Name,
		Subjects:  c.Subjects,
		Storage:   storage,
		MaxAge:    c.MaxAge,
		MaxBytes:  c.MaxBytes,
		Replicas:  c.Replicas,
		Retention: retention,
		Discard:   discard,
	}, nil
}

func (c *JetStreamStreamConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Name      string   `yaml:"name"`
		Subjects  []string `yaml:"subjects"`
		Storage   string   `yaml:"storage"`
		MaxAge    string   `yaml:"max_age"`
		MaxBytes  int64    `yaml:"max_bytes"`
		Replicas  int      `yaml:"replicas"`
		Retention string   `yaml:"retention"`
		Discard   string   `yaml:"discard"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	*c = JetStreamStreamConfig{
		Name:      raw.Name,
		Subjects:  raw.Subjects,
		Storage:   raw.Storage,
		MaxBytes:  raw.MaxBytes,
		Replicas:  raw.Replicas,
		Retention: raw.Retention,
		Discard:   raw.Discard,
	}
	if raw.MaxAge != "" {
		duration, err := time.ParseDuration(raw.MaxAge)
		if err != nil {
			return fmt.Errorf("invalid jetstream.stream.max_age: %w", err)
		}
		c.MaxAge = duration
	}
	return nil
}

func (c *SinkConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled        *bool  `yaml:"enabled"`
		URL            string `yaml:"url"`
		ConsumerName   string `yaml:"consumer_name"`
		Measurement    string `yaml:"measurement"`
		BatchMaxSize   int    `yaml:"batch_max_size"`
		FlushInterval  string `yaml:"flush_interval"`
		RequestTimeout string `yaml:"request_timeout"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	*c = SinkConfig{
		URL:          raw.URL,
		ConsumerName: raw.ConsumerName,
		Measurement:  raw.Measurement,
		BatchMaxSize: raw.BatchMaxSize,
	}
	// Default Enabled to true when the key is omitted, matching DefaultCoreConfig.
	c.Enabled = raw.Enabled == nil || *raw.Enabled
	if raw.FlushInterval != "" {
		duration, err := time.ParseDuration(raw.FlushInterval)
		if err != nil {
			return fmt.Errorf("invalid sink.flush_interval: %w", err)
		}
		c.FlushInterval = duration
	}
	if raw.RequestTimeout != "" {
		duration, err := time.ParseDuration(raw.RequestTimeout)
		if err != nil {
			return fmt.Errorf("invalid sink.request_timeout: %w", err)
		}
		c.RequestTimeout = duration
	}
	return nil
}

func parseStoragePolicy(value string) (nats.StorageType, error) {
	switch value {
	case "", "file":
		return nats.FileStorage, nil
	case "memory":
		return nats.MemoryStorage, nil
	default:
		return nats.FileStorage, fmt.Errorf("unsupported JetStream storage policy: %s", value)
	}
}

func parseRetentionPolicy(value string) (nats.RetentionPolicy, error) {
	switch value {
	case "", "limits":
		return nats.LimitsPolicy, nil
	case "interest":
		return nats.InterestPolicy, nil
	case "workqueue":
		return nats.WorkQueuePolicy, nil
	default:
		return nats.LimitsPolicy, fmt.Errorf("unsupported JetStream retention policy: %s", value)
	}
}

func parseDiscardPolicy(value string) (nats.DiscardPolicy, error) {
	switch value {
	case "", "old":
		return nats.DiscardOld, nil
	case "new":
		return nats.DiscardNew, nil
	default:
		return nats.DiscardOld, fmt.Errorf("unsupported JetStream discard policy: %s", value)
	}
}

// NATSCredentialsFile resolves where the role credentials live.
// Precedence: EDG_NATS_CREDENTIALS_FILE > nats.auth.credentials_file >
// <storage.data_dir>/nats-credentials.json.
func (c CoreConfig) NATSCredentialsFile() string {
	if v := os.Getenv(EnvNATSCredentialsFile); v != "" {
		return v
	}
	if c.NATS.Auth.CredentialsFile != "" {
		return c.NATS.Auth.CredentialsFile
	}
	dir := c.Storage.DataDir
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, defaultCredentialsFileName)
}

// isLoopbackHost reports whether a bind address is unreachable from off-box.
// An empty host is not loopback: nats-server treats it as 0.0.0.0
// (server/opts.go setBaselineOptions, server/const.go DEFAULT_HOST).
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "::1", "[::1]":
		return true
	case "":
		return false
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (c *AdaptersConfig) applyDefaults(defaults AdaptersConfig) {
	if c.StaleAfterFloor == 0 {
		c.StaleAfterFloor = defaults.StaleAfterFloor
	}
	if c.MinInterval == 0 {
		c.MinInterval = defaults.MinInterval
	}
	if c.MaxInterval == 0 {
		c.MaxInterval = defaults.MaxInterval
	}
	if c.ForgetAfter == 0 {
		c.ForgetAfter = defaults.ForgetAfter
	}
	if c.ProbeTimeout == 0 {
		c.ProbeTimeout = defaults.ProbeTimeout
	}
	if c.MaxConcurrentProbes == 0 {
		c.MaxConcurrentProbes = defaults.MaxConcurrentProbes
	}
}

// UnmarshalYAML mirrors SinkConfig's: durations arrive as strings, and the
// boolean keys default to true when omitted rather than to Go's zero value.
func (c *AdaptersConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled             *bool  `yaml:"enabled"`
		StaleAfterFloor     string `yaml:"stale_after_floor"`
		MinInterval         string `yaml:"min_interval"`
		MaxInterval         string `yaml:"max_interval"`
		ForgetAfter         string `yaml:"forget_after"`
		ProbeOnMiss         *bool  `yaml:"probe_on_miss"`
		ProbeTimeout        string `yaml:"probe_timeout"`
		MaxConcurrentProbes int    `yaml:"max_concurrent_probes"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	*c = AdaptersConfig{MaxConcurrentProbes: raw.MaxConcurrentProbes}
	c.Enabled = raw.Enabled == nil || *raw.Enabled
	c.ProbeOnMiss = raw.ProbeOnMiss == nil || *raw.ProbeOnMiss

	for _, f := range []struct {
		key string
		raw string
		dst *time.Duration
	}{
		{"stale_after_floor", raw.StaleAfterFloor, &c.StaleAfterFloor},
		{"min_interval", raw.MinInterval, &c.MinInterval},
		{"max_interval", raw.MaxInterval, &c.MaxInterval},
		{"forget_after", raw.ForgetAfter, &c.ForgetAfter},
		{"probe_timeout", raw.ProbeTimeout, &c.ProbeTimeout},
	} {
		if f.raw == "" {
			continue
		}
		d, err := time.ParseDuration(f.raw)
		if err != nil {
			return fmt.Errorf("invalid adapters.%s: %w", f.key, err)
		}
		*f.dst = d
	}
	return nil
}
