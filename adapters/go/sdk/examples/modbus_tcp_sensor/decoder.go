package main

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/e7217/edg/adapters/go/sdk"
)

// RegisterSpec mirrors one row of the mapping YAML and the Python
// RegisterSpec dataclass. Field tags map to the YAML schema.
type RegisterSpec struct {
	Name      string  `yaml:"name"`
	Function  string  `yaml:"function"`  // "holding" | "input"
	Address   uint16  `yaml:"address"`
	Type      string  `yaml:"type"`      // uint16 | int16 | uint32 | int32 | float32
	WordOrder string  `yaml:"word_order"`
	Scale     float64 `yaml:"scale"`
	Unit      string  `yaml:"unit"`
}

// WordCount returns how many 16-bit registers this spec consumes.
func (r RegisterSpec) WordCount() (uint16, error) {
	switch r.Type {
	case "uint16", "int16":
		return 1, nil
	case "uint32", "int32", "float32":
		return 2, nil
	default:
		return 0, fmt.Errorf("unsupported type: %s", r.Type)
	}
}

// DecodeRegister converts raw 16-bit register words into a TagValue.
// The Quality is always GOOD; the adapter is responsible for downgrading
// quality on read failure.
func DecodeRegister(words []uint16, spec RegisterSpec) (sdk.TagValue, error) {
	scale := spec.Scale
	if scale == 0 {
		scale = 1.0
	}

	var value float64
	switch spec.Type {
	case "uint16":
		if len(words) != 1 {
			return sdk.TagValue{}, fmt.Errorf("uint16 requires 1 word, got %d", len(words))
		}
		value = float64(words[0])

	case "int16":
		if len(words) != 1 {
			return sdk.TagValue{}, fmt.Errorf("int16 requires 1 word, got %d", len(words))
		}
		value = float64(int16(words[0]))

	case "uint32", "int32", "float32":
		if len(words) != 2 {
			return sdk.TagValue{}, fmt.Errorf("%s requires 2 words, got %d", spec.Type, len(words))
		}
		buf, err := toBigEndianBytes(words, spec.WordOrder)
		if err != nil {
			return sdk.TagValue{}, err
		}
		switch spec.Type {
		case "uint32":
			value = float64(binary.BigEndian.Uint32(buf))
		case "int32":
			value = float64(int32(binary.BigEndian.Uint32(buf)))
		case "float32":
			value = float64(math.Float32frombits(binary.BigEndian.Uint32(buf)))
		}

	default:
		return sdk.TagValue{}, fmt.Errorf("unsupported type: %s", spec.Type)
	}

	n := value * scale
	return sdk.TagValue{
		Name:    spec.Name,
		Quality: sdk.QualityGood,
		Number:  &n,
		Unit:    spec.Unit,
	}, nil
}

// toBigEndianBytes reassembles two raw register words into a canonical
// big-endian 4-byte buffer, undoing any vendor-specific byte/word swap.
//
// Word-order convention (vendor manual style):
//
//	ABCD: bytes [A B C D] -> word0=AB, word1=CD (big-endian, normal)
//	CDAB: word swap -> word0=CD, word1=AB
//	BADC: byte swap inside each word -> word0=BA, word1=DC
//	DCBA: both swapped -> word0=DC, word1=BA
func toBigEndianBytes(words []uint16, order string) ([]byte, error) {
	hi, lo := words[0], words[1]
	a, b := byte(hi>>8), byte(hi&0xFF)
	c, d := byte(lo>>8), byte(lo&0xFF)

	switch order {
	case "ABCD", "":
		return []byte{a, b, c, d}, nil
	case "CDAB":
		return []byte{c, d, a, b}, nil
	case "BADC":
		return []byte{b, a, d, c}, nil
	case "DCBA":
		return []byte{d, c, b, a}, nil
	default:
		return nil, fmt.Errorf("unsupported word_order: %s (expected one of ABCD, CDAB, BADC, DCBA)", order)
	}
}
