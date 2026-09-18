package main

import (
	"fmt"
	"math"

	"github.com/e7217/edg/adapters/go/sdk"
)

// PointSpec is one reading: a device, how to decode it, and what to call it.
type PointSpec struct {
	Name   string
	Device Device
	// Type is int16, uint16, int32, uint32, float32 for word devices, or bit
	// for a bit device (M100) and for one bit of a word (D100.3).
	Type  string
	Bit   int // bit index within the word for "bit" on a word device
	Scale float64
	Unit  string
}

// Words returns how many words the point occupies.
func (p PointSpec) Words() uint16 {
	switch p.Type {
	case "int32", "uint32", "float32":
		return 2
	default:
		return 1
	}
}

// decode turns the words read at the point's device into a value. MELSEC
// stores a 32-bit value low word first (the D100/D101 pair of a DINT or REAL).
func decode(p PointSpec, w []uint16) (sdk.TagValue, error) {
	tv := sdk.TagValue{Name: p.Name, Quality: sdk.QualityGood}
	if int(p.Words()) > len(w) {
		return tv, fmt.Errorf("%s: need %d words, have %d", p.Name, p.Words(), len(w))
	}
	var f float64
	switch p.Type {
	case "bit":
		b := w[0]>>uint(p.Bit)&1 == 1
		tv.Flag = &b
		return tv, nil
	case "int16":
		f = float64(int16(w[0]))
	case "uint16":
		f = float64(w[0])
	case "int32":
		f = float64(int32(uint32(w[1])<<16 | uint32(w[0])))
	case "uint32":
		f = float64(uint32(w[1])<<16 | uint32(w[0]))
	case "float32":
		f = float64(math.Float32frombits(uint32(w[1])<<16 | uint32(w[0])))
	default:
		return tv, fmt.Errorf("%s: type %q not supported", p.Name, p.Type)
	}
	if p.Scale != 0 {
		f *= p.Scale
	}
	tv.Number = &f
	tv.Unit = p.Unit
	return tv, nil
}
