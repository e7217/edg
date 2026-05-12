package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "mapping.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestLoadConfigMinimal(t *testing.T) {
	path := writeYAML(t, `
version: 1
host: 10.0.0.1
port: 5020
unit_id: 7
poll_interval: 2.0
timeout: 0.5
registers:
  - name: temperature
    function: holding
    address: 100
    type: int16
    scale: 0.1
    unit: "°C"
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Host != "10.0.0.1" || cfg.Port != 5020 || cfg.UnitID != 7 {
		t.Errorf("host/port/unit_id mismatch: %+v", cfg)
	}
	if cfg.PollInterval != 2.0 || cfg.Timeout != 0.5 {
		t.Errorf("interval/timeout mismatch: %+v", cfg)
	}
	if len(cfg.Registers) != 1 {
		t.Fatalf("want 1 register, got %d", len(cfg.Registers))
	}
	r := cfg.Registers[0]
	if r.Name != "temperature" || r.Function != "holding" || r.Address != 100 || r.Type != "int16" {
		t.Errorf("register mismatch: %+v", r)
	}
	if r.Scale != 0.1 || r.Unit != "°C" {
		t.Errorf("scale/unit mismatch: %+v", r)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	path := writeYAML(t, `
version: 1
host: 127.0.0.1
registers:
  - name: counter
    function: input
    address: 0
    type: uint16
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Port != 502 {
		t.Errorf("default port: got %d, want 502", cfg.Port)
	}
	if cfg.UnitID != 1 {
		t.Errorf("default unit_id: got %d, want 1", cfg.UnitID)
	}
	if cfg.PollInterval != 1.0 || cfg.Timeout != 1.0 {
		t.Errorf("default poll/timeout: %+v", cfg)
	}
	r := cfg.Registers[0]
	if r.WordOrder != "ABCD" {
		t.Errorf("default word_order: %q", r.WordOrder)
	}
	if r.Scale != 1.0 {
		t.Errorf("default scale: %v", r.Scale)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing name",
			body: `
version: 1
host: 127.0.0.1
registers:
  - function: holding
    address: 0
    type: uint16
`,
			want: "name",
		},
		{
			name: "unknown function",
			body: `
version: 1
host: 127.0.0.1
registers:
  - name: bad
    function: coil
    address: 0
    type: uint16
`,
			want: "function",
		},
		{
			name: "unknown type",
			body: `
version: 1
host: 127.0.0.1
registers:
  - name: bad
    function: holding
    address: 0
    type: float64
`,
			want: "type",
		},
		{
			name: "empty registers",
			body: `
version: 1
host: 127.0.0.1
registers: []
`,
			want: "at least one",
		},
		{
			name: "unknown version",
			body: `
version: 99
host: 127.0.0.1
registers:
  - name: r
    function: holding
    address: 0
    type: uint16
`,
			want: "version",
		},
		{
			name: "missing host",
			body: `
version: 1
registers:
  - name: r
    function: holding
    address: 0
    type: uint16
`,
			want: "host",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := writeYAML(t, tt.body)
			_, err := LoadConfig(p)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}
