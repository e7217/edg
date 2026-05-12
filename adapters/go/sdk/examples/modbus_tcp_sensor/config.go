package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ModbusConfig is the top-level mapping YAML schema.
type ModbusConfig struct {
	Version      int            `yaml:"version"`
	Host         string         `yaml:"host"`
	Port         int            `yaml:"port"`
	UnitID       byte           `yaml:"unit_id"`
	PollInterval float64        `yaml:"poll_interval"`
	Timeout      float64        `yaml:"timeout"`
	Registers    []RegisterSpec `yaml:"registers"`
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
	if cfg.Host == "" {
		return nil, fmt.Errorf("'host' is required")
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
	if len(cfg.Registers) == 0 {
		return nil, fmt.Errorf("'registers' must list at least one entry")
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
