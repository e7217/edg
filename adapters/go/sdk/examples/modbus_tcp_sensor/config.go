package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ModbusConfig is the top-level mapping YAML schema.
type ModbusConfig struct {
	Version int `yaml:"version"`
	// Transport is "tcp" (default) or "rtu". RTU reads the same registers
	// over a serial line; Host and Port are then unused and Serial applies.
	Transport    string         `yaml:"transport"`
	Serial       SerialConfig   `yaml:"serial"`
	Host         string         `yaml:"host"`
	Port         int            `yaml:"port"`
	UnitID       byte           `yaml:"unit_id"`
	PollInterval float64        `yaml:"poll_interval"`
	Timeout      float64        `yaml:"timeout"`
	Registers    []RegisterSpec `yaml:"registers"`

	// AssetID, when set without registers, makes the register map come from
	// EDG master data: the adapter polls the point list declared for this
	// asset and follows changes to it (ADR 0011). The device connection above
	// stays local, because it is a fact about this box's network.
	AssetID string `yaml:"asset_id"`
	// NATSURL is EDG Core's NATS address, credentials included. Empty uses
	// nats://localhost:4222; EDG_NATS_URL overrides it.
	NATSURL string `yaml:"nats_url"`
}

// Transports.
const (
	TransportTCP = "tcp"
	TransportRTU = "rtu"
)

// SerialConfig is the serial line of a Modbus RTU transport.
type SerialConfig struct {
	Port     string `yaml:"port"`      // /dev/ttyUSB0, COM3
	BaudRate int    `yaml:"baud_rate"` // default 9600
	DataBits int    `yaml:"data_bits"` // default 8
	// Parity is N, E or O. The Modbus specification's default is E; many
	// devices ship configured N, which the specification pairs with two stop
	// bits. Set both to what the device's panel says.
	Parity   string `yaml:"parity"`
	StopBits int    `yaml:"stop_bits"` // default 1
}

// Protocol is the point-list protocol this configuration reads.
func (c *ModbusConfig) Protocol() string {
	if c.Transport == TransportRTU {
		return ProtocolModbusRTU
	}
	return ProtocolModbusTCP
}

// Provisioned reports whether registers come from master data.
func (c *ModbusConfig) Provisioned() bool {
	return c.AssetID != "" && len(c.Registers) == 0
}

var (
	supportedVersions   = map[int]struct{}{1: {}}
	supportedFunctions  = map[string]struct{}{"holding": {}, "input": {}}
	supportedTypes      = map[string]struct{}{"uint16": {}, "int16": {}, "uint32": {}, "int32": {}, "float32": {}}
	supportedWordOrders = map[string]struct{}{"ABCD": {}, "CDAB": {}, "BADC": {}, "DCBA": {}}
)

// LoadConfig reads and validates a mapping YAML.
func LoadConfig(path string) (*ModbusConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ModbusConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if _, ok := supportedVersions[cfg.Version]; !ok {
		return nil, fmt.Errorf("unsupported config version: %d", cfg.Version)
	}
	switch cfg.Transport {
	case "", TransportTCP:
		cfg.Transport = TransportTCP
		if cfg.Host == "" {
			return nil, fmt.Errorf("'host' is required")
		}
	case TransportRTU:
		if err := cfg.Serial.applyDefaults(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("transport %q not supported (tcp, rtu)", cfg.Transport)
	}
	if cfg.Port == 0 {
		cfg.Port = 502
	}
	if cfg.UnitID == 0 {
		cfg.UnitID = 1
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1.0
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 1.0
	}
	if len(cfg.Registers) == 0 && cfg.AssetID == "" {
		return nil, fmt.Errorf("'registers' must list at least one entry, or 'asset_id' must name an asset whose point list to poll")
	}

	for i := range cfg.Registers {
		if err := validateAndDefault(&cfg.Registers[i], i); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func validateAndDefault(r *RegisterSpec, index int) error {
	if r.Name == "" {
		return fmt.Errorf("registers[%d]: missing required field 'name'", index)
	}
	if _, ok := supportedFunctions[r.Function]; !ok {
		return fmt.Errorf("registers[%d]: function %q not supported", index, r.Function)
	}
	if _, ok := supportedTypes[r.Type]; !ok {
		return fmt.Errorf("registers[%d]: type %q not supported", index, r.Type)
	}
	if r.WordOrder == "" {
		r.WordOrder = "ABCD"
	}
	if _, ok := supportedWordOrders[r.WordOrder]; !ok {
		return fmt.Errorf("registers[%d]: word_order %q not supported", index, r.WordOrder)
	}
	if r.Scale == 0 {
		r.Scale = 1.0
	}
	return nil
}

func (s *SerialConfig) applyDefaults() error {
	if s.Port == "" {
		return fmt.Errorf("'serial.port' is required for transport rtu, e.g. /dev/ttyUSB0")
	}
	if s.BaudRate == 0 {
		s.BaudRate = 9600
	}
	if s.DataBits == 0 {
		s.DataBits = 8
	}
	if s.Parity == "" {
		s.Parity = "E"
	}
	switch s.Parity {
	case "N", "E", "O":
	default:
		return fmt.Errorf("serial.parity %q not supported (N, E, O)", s.Parity)
	}
	if s.StopBits == 0 {
		s.StopBits = 1
	}
	return nil
}
