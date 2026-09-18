package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gopcua/opcua/ua"
	"gopkg.in/yaml.v3"
)

// Config is the adapter's local configuration.
//
// The server connection is local: where the OPC UA server is, as this box sees
// it, and how to log in. What to read comes either from Nodes or -- with
// AssetID set and Nodes empty -- from the point list EDG declares for that
// asset (ADR 0011), with each point's address as its NodeId.
type Config struct {
	Endpoint     string  `yaml:"endpoint"`
	Username     string  `yaml:"username"`
	Password     string  `yaml:"password"`
	PollInterval float64 `yaml:"poll_interval"`
	Timeout      float64 `yaml:"timeout"`
	Nodes        []Node  `yaml:"nodes"`

	AssetID string `yaml:"asset_id"`
	NATSURL string `yaml:"nats_url"`
}

// Node is one variable to read.
type Node struct {
	Name   string `yaml:"name"`
	NodeID string `yaml:"node_id"`
	Unit   string `yaml:"unit"`

	id *ua.NodeID
}

// Provisioned reports whether nodes come from master data.
func (c *Config) Provisioned() bool { return c.AssetID != "" && len(c.Nodes) == 0 }

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
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("'endpoint' is required, e.g. opc.tcp://192.168.10.30:4840")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5
	}
	if len(cfg.Nodes) == 0 && cfg.AssetID == "" {
		return nil, fmt.Errorf("'nodes' must list at least one entry, or 'asset_id' must name an asset whose point list to read")
	}
	if err := parseNodes(cfg.Nodes); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// parseNodes resolves every NodeId. One bad id rejects the list: a read with a
// hole in it succeeds and says nothing about the hole.
func parseNodes(nodes []Node) error {
	seen := map[string]bool{}
	for i := range nodes {
		n := &nodes[i]
		if n.Name == "" {
			return fmt.Errorf("nodes[%d]: 'name' is required", i)
		}
		if seen[n.Name] {
			return fmt.Errorf("nodes[%d]: name %q is used twice", i, n.Name)
		}
		seen[n.Name] = true
		// A bare number parses as ns=0;i=<n>, which is almost always a Modbus
		// register pasted into the wrong list. The explicit form is required.
		if !strings.Contains(n.NodeID, "=") {
			return fmt.Errorf("node %q: node_id %q is not an OPC UA NodeId; write it in full, e.g. ns=2;s=Temperature or i=2258", n.Name, n.NodeID)
		}
		id, err := ua.ParseNodeID(n.NodeID)
		if err != nil {
			return fmt.Errorf("node %q: node_id %q is not an OPC UA NodeId (e.g. ns=2;s=Temperature): %v", n.Name, n.NodeID, err)
		}
		n.id = id
	}
	return nil
}
