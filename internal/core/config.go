package core

import (
	"fmt"
	"log"
	"os"
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
}

type NATSConfig struct {
	Port     int    `yaml:"port"`
	HTTPPort int    `yaml:"http_port"`
	LogLevel string `yaml:"log_level"`
	StoreDir string `yaml:"store_dir"`
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
			Port:     4222,
			HTTPPort: 8222,
			LogLevel: "info",
			StoreDir: "./data/jetstream",
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

	if c.NATS.Port == 0 {
		c.NATS.Port = defaults.NATS.Port
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
