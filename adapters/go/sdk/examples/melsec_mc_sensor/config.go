package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/e7217/edg/adapters/go/sdk"
)

// ProtocolMELSEC is the point-list protocol this adapter reads.
const ProtocolMELSEC = "melsec-mc"

// Config is the adapter's local configuration. The PLC connection is local;
// what to read is either Points, or -- with AssetID set and no Points -- the
// point list EDG declares for that asset (ADR 0011).
type Config struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	PollInterval float64       `yaml:"poll_interval"`
	Timeout      float64       `yaml:"timeout"`
	Points       []PointConfig `yaml:"points"`

	AssetID string `yaml:"asset_id"`
	NATSURL string `yaml:"nats_url"`

	specs []PointSpec
}

// PointConfig is one point in the local file, in the same shape as a
// declared point's address and encoding.
type PointConfig struct {
	Name    string  `yaml:"name"`
	Address string  `yaml:"address"` // D100, M8000, X1F, D100.3
	Type    string  `yaml:"type"`    // int16 (default for words), uint16, int32, uint32, float32, bit
	Scale   float64 `yaml:"scale"`
	Unit    string  `yaml:"unit"`
}

// Provisioned reports whether points come from master data.
func (c *Config) Provisioned() bool { return c.AssetID != "" && len(c.Points) == 0 }

func (c *Config) timeout() time.Duration { return time.Duration(c.Timeout * float64(time.Second)) }

// LoadConfig reads and validates the YAML at path.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("'host' is required")
	}
	if cfg.Port == 0 {
		return nil, fmt.Errorf("'port' is required: the MC protocol port set in the PLC's Ethernet settings (often 5007, 1025 or 5000)")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 3
	}
	if len(cfg.Points) == 0 && cfg.AssetID == "" {
		return nil, fmt.Errorf("'points' must list at least one entry, or 'asset_id' must name an asset whose point list to read")
	}
	cfg.specs, err = specsFrom(cfg.Points)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// specsFrom validates points. One bad point rejects the list: a read with a
// hole in it succeeds and says nothing about the hole.
func specsFrom(points []PointConfig) ([]PointSpec, error) {
	specs := make([]PointSpec, 0, len(points))
	seen := map[string]bool{}
	for _, p := range points {
		if p.Name == "" {
			return nil, fmt.Errorf("point with address %q has no name", p.Address)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("point %q is declared twice", p.Name)
		}
		seen[p.Name] = true
		spec, err := specFrom(p)
		if err != nil {
			return nil, fmt.Errorf("point %q: %w", p.Name, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func specFrom(p PointConfig) (PointSpec, error) {
	addr, bit := p.Address, -1
	if i := strings.IndexByte(addr, '.'); i >= 0 {
		b, err := strconv.Atoi(addr[i+1:])
		if err != nil || b < 0 || b > 15 {
			return PointSpec{}, fmt.Errorf("address %q: bit index after '.' must be 0-15", p.Address)
		}
		addr, bit = addr[:i], b
	}
	dev, err := ParseDevice(addr)
	if err != nil {
		return PointSpec{}, err
	}
	spec := PointSpec{Name: p.Name, Device: dev, Type: p.Type, Scale: p.Scale, Unit: p.Unit}
	switch {
	case dev.Bit:
		if bit >= 0 {
			return PointSpec{}, fmt.Errorf("address %q: %s is already a bit device", p.Address, addr)
		}
		if spec.Type != "" && spec.Type != "bit" {
			return PointSpec{}, fmt.Errorf("address %q: a bit device reads as type bit, not %s", p.Address, spec.Type)
		}
		spec.Type = "bit"
	case bit >= 0:
		if spec.Type != "" && spec.Type != "bit" {
			return PointSpec{}, fmt.Errorf("address %q: a bit of a word reads as type bit, not %s", p.Address, spec.Type)
		}
		spec.Type, spec.Bit = "bit", bit
	default:
		if spec.Type == "" {
			spec.Type = "int16"
		}
		switch spec.Type {
		case "int16", "uint16", "int32", "uint32", "float32":
		default:
			return PointSpec{}, fmt.Errorf("type %q not supported (int16, uint16, int32, uint32, float32, bit)", spec.Type)
		}
	}
	return spec, nil
}

// specsFromPointList converts a declared point list: the point's address is
// the device, and encoding carries type and scale.
func specsFromPointList(pl *sdk.PointList) ([]PointSpec, error) {
	if pl.Protocol != "" && pl.Protocol != ProtocolMELSEC {
		return nil, fmt.Errorf("point list protocol is %q; this adapter reads %q", pl.Protocol, ProtocolMELSEC)
	}
	points := make([]PointConfig, 0, len(pl.Points))
	for _, p := range pl.EnabledPoints() {
		pc := PointConfig{Name: p.Name, Address: p.Address, Unit: p.Unit}
		if t, ok := p.Encoding["type"].(string); ok {
			pc.Type = t
		}
		switch s := p.Encoding["scale"].(type) {
		case nil:
		case float64:
			pc.Scale = s
		default:
			return nil, fmt.Errorf("point %q: encoding.scale must be a number", p.Name)
		}
		points = append(points, pc)
	}
	return specsFrom(points)
}
