package main

import (
	"os"
	"strings"
	"testing"

	"github.com/e7217/edg/adapters/go/sdk"
)

func TestRegistersFromPoints(t *testing.T) {
	pl := &sdk.PointList{AssetID: "pump-a", Protocol: ProtocolModbusTCP, Version: 3, Points: []sdk.Point{
		{Name: "temperature", Address: "0", Unit: "°C", Enabled: true,
			Encoding: map[string]any{"function": "holding", "type": "int16", "scale": 0.1}},
		{Name: "flow", Address: "100", Enabled: true,
			Encoding: map[string]any{"function": "input", "type": "float32", "word_order": "CDAB"}},
		{Name: "retired", Address: "7", Enabled: false, Encoding: map[string]any{"type": "uint16"}},
	}}
	regs, err := registersFromPoints(pl)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 2 {
		t.Fatalf("got %d registers, want 2 (a disabled point is not polled)", len(regs))
	}
	want0 := RegisterSpec{Name: "temperature", Function: "holding", Address: 0, Type: "int16",
		WordOrder: "ABCD", Scale: 0.1, Unit: "°C"}
	if regs[0] != want0 {
		t.Errorf("regs[0] = %+v, want %+v", regs[0], want0)
	}
	if regs[1].Function != "input" || regs[1].Address != 100 || regs[1].WordOrder != "CDAB" || regs[1].Scale != 1 {
		t.Errorf("regs[1] = %+v", regs[1])
	}
}

func TestRegistersFromPointsRejectsTheWholeList(t *testing.T) {
	cases := map[string]sdk.Point{
		"not a register number":     {Name: "p", Address: "ns=2;s=Temp", Enabled: true, Encoding: map[string]any{"type": "int16"}},
		"encoding.type is required": {Name: "p", Address: "1", Enabled: true},
		"must be a number":          {Name: "p", Address: "1", Enabled: true, Encoding: map[string]any{"type": "int16", "scale": "0.1"}},
		"not supported":             {Name: "p", Address: "1", Enabled: true, Encoding: map[string]any{"type": "int64"}},
	}
	for want, p := range cases {
		good := sdk.Point{Name: "ok", Address: "0", Enabled: true, Encoding: map[string]any{"type": "uint16"}}
		_, err := registersFromPoints(&sdk.PointList{Points: []sdk.Point{good, p}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestRegistersFromPointsRefusesAnotherProtocol(t *testing.T) {
	_, err := registersFromPoints(&sdk.PointList{Protocol: "opcua"})
	if err == nil || !strings.Contains(err.Error(), "opcua") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadConfigProvisioned(t *testing.T) {
	path := t.TempDir() + "/m.yaml"
	if err := writeFile(path, "host: 10.0.0.5\nasset_id: pump-a\nnats_url: nats://core:4222\n"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Provisioned() || cfg.AssetID != "pump-a" || cfg.NATSURL != "nats://core:4222" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
