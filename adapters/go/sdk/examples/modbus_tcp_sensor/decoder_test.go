package main

import (
	"math"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

type matrixCase struct {
	Name           string   `yaml:"name"`
	Words          []uint16 `yaml:"words"`
	Type           string   `yaml:"type"`
	WordOrder      string   `yaml:"word_order"`
	Scale          *float64 `yaml:"scale"`
	Expected       *float64 `yaml:"expected"`
	ExpectedApprox *float64 `yaml:"expected_approx"`
}

type matrixFile struct {
	Cases []matrixCase `yaml:"cases"`
}

func loadMatrix(t *testing.T) []matrixCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/decoder_matrix.yaml")
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	var m matrixFile
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal matrix: %v", err)
	}
	return m.Cases
}

func TestDecodeRegisterMatrix(t *testing.T) {
	for _, c := range loadMatrix(t) {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			spec := RegisterSpec{
				Name:      c.Name,
				Function:  "holding",
				Address:   0,
				Type:      c.Type,
				WordOrder: c.WordOrder,
				Scale:     1.0,
				Unit:      "",
			}
			if spec.WordOrder == "" {
				spec.WordOrder = "ABCD"
			}
			if c.Scale != nil {
				spec.Scale = *c.Scale
			}

			tv, err := DecodeRegister(c.Words, spec)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if tv.Number == nil {
				t.Fatalf("expected number, got nil")
			}
			got := *tv.Number

			switch {
			case c.ExpectedApprox != nil:
				if math.Abs(got-*c.ExpectedApprox) > math.Abs(*c.ExpectedApprox)*1e-4 {
					t.Errorf("got %v, want ≈ %v", got, *c.ExpectedApprox)
				}
			case c.Expected != nil:
				if got != *c.Expected {
					t.Errorf("got %v, want %v", got, *c.Expected)
				}
			default:
				t.Fatal("matrix case has neither expected nor expected_approx")
			}

			if tv.Quality != "GOOD" {
				t.Errorf("quality: got %q, want GOOD", tv.Quality)
			}
			if tv.Name != c.Name {
				t.Errorf("name: got %q, want %q", tv.Name, c.Name)
			}
		})
	}
}

func TestDecodeRegisterErrors(t *testing.T) {
	tests := []struct {
		name    string
		spec    RegisterSpec
		words   []uint16
		wantMsg string
	}{
		{
			name:    "uint32 needs 2 words",
			spec:    RegisterSpec{Type: "uint32", WordOrder: "ABCD"},
			words:   []uint16{0x1234},
			wantMsg: "2 words",
		},
		{
			name:    "uint16 needs 1 word",
			spec:    RegisterSpec{Type: "uint16"},
			words:   []uint16{0x1234, 0x5678},
			wantMsg: "1 word",
		},
		{
			name:    "unknown type",
			spec:    RegisterSpec{Type: "float64"},
			words:   []uint16{0x0000, 0x0000},
			wantMsg: "unsupported type",
		},
		{
			name:    "unknown word_order",
			spec:    RegisterSpec{Type: "uint32", WordOrder: "XXXX"},
			words:   []uint16{0x0000, 0x0000},
			wantMsg: "unsupported word_order",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeRegister(tt.words, tt.spec)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
