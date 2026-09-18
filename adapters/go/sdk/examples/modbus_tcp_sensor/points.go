package main

import (
	"fmt"
	"strconv"

	"github.com/e7217/edg/adapters/go/sdk"
)

// ProtocolModbusTCP is the point-list protocol this adapter reads.
const ProtocolModbusTCP = "modbus-tcp"

// registersFromPoints turns a declared point list into a register map. The
// point's address is the register number and its encoding carries what a
// mapping.yaml register carries: function, type, word_order and scale.
//
// One bad point rejects the whole list rather than being skipped: a register
// map with a hole in it polls successfully and reports nothing about the hole.
func registersFromPoints(pl *sdk.PointList) ([]RegisterSpec, error) {
	if pl.Protocol != "" && pl.Protocol != ProtocolModbusTCP {
		return nil, fmt.Errorf("point list protocol is %q; this adapter reads %q", pl.Protocol, ProtocolModbusTCP)
	}
	points := pl.EnabledPoints()
	regs := make([]RegisterSpec, 0, len(points))
	for i, p := range points {
		addr, err := strconv.ParseUint(p.Address, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("point %q: address %q is not a register number (0-65535)", p.Name, p.Address)
		}
		r := RegisterSpec{
			Name:      p.Name,
			Address:   uint16(addr),
			Unit:      p.Unit,
			Function:  stringField(p.Encoding, "function", "holding"),
			Type:      stringField(p.Encoding, "type", ""),
			WordOrder: stringField(p.Encoding, "word_order", ""),
		}
		if r.Type == "" {
			return nil, fmt.Errorf("point %q: encoding.type is required (uint16, int16, uint32, int32, float32)", p.Name)
		}
		switch v := p.Encoding["scale"].(type) {
		case nil:
		case float64:
			r.Scale = v
		default:
			return nil, fmt.Errorf("point %q: encoding.scale must be a number, got %T", p.Name, v)
		}
		if err := validateAndDefault(&r, i); err != nil {
			return nil, fmt.Errorf("point %q: %w", p.Name, err)
		}
		regs = append(regs, r)
	}
	return regs, nil
}

func stringField(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}
