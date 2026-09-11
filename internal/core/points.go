package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// Point is one declared reading on an asset: which address on the field device
// it lives at, what it is called on the data plane, and how to decode it.
//
// Name is the join to telemetry: a reported TagValue.Name matches a declared
// point's Name, which is why a point is resolvable from (asset_id, name) alone
// and why no field has to be added to TagValue.
type Point struct {
	Name      string `json:"name" yaml:"name"`
	ValueType string `json:"value_type" yaml:"value_type"`
	Unit      string `json:"unit,omitempty" yaml:"unit,omitempty"`
	// Address is opaque to the core: "0" for a Modbus register, "ns=2;s=Temp"
	// for OPC-UA. Only the adapter that speaks the protocol interprets it.
	Address string `json:"address" yaml:"address"`
	// Encoding carries the protocol-specific decoding rules -- function, type,
	// word_order and scale for Modbus. It is an arbitrary JSON object so a
	// numeric scale stays numeric rather than being flattened to a string, and
	// so adding a protocol needs no core change.
	Encoding map[string]any `json:"encoding,omitempty" yaml:"encoding,omitempty"`
	// Enabled false keeps a declaration for the record without polling it,
	// which is what an operator wants for a decommissioned sensor.
	Enabled   bool      `json:"enabled" yaml:"enabled"`
	CreatedAt time.Time `json:"created_at,omitempty" yaml:"-"`
	UpdatedAt time.Time `json:"updated_at,omitempty" yaml:"-"`
}

// PointList is every point declared for one asset.
type PointList struct {
	AssetID string `json:"asset_id" yaml:"asset_id"`
	// Protocol is opaque to the core and names the adapter that can read this
	// list: "modbus-tcp", "opcua".
	Protocol string `json:"protocol" yaml:"protocol"`
	// PollIntervalMS zero leaves the adapter's own default in place.
	PollIntervalMS int `json:"poll_interval_ms,omitempty" yaml:"poll_interval_ms,omitempty"`
	// Version is bumped by the store on every write. It is the convergence
	// signal an adapter reports back as config_version once distribution lands.
	Version   int       `json:"version" yaml:"-"`
	Points    []Point   `json:"points" yaml:"points"`
	CreatedAt time.Time `json:"created_at,omitempty" yaml:"-"`
	UpdatedAt time.Time `json:"updated_at,omitempty" yaml:"-"`
}

// UpsertPointListRequest replaces an asset's point list wholesale.
//
// Wholesale replacement rather than per-point patching, because the operator's
// artifact is a spreadsheet or a mapping file: "here is the list" is the
// operation they actually perform, and a partial apply is what leaves a plant
// half-provisioned.
type UpsertPointListRequest struct {
	AssetID        string  `json:"asset_id"`
	Protocol       string  `json:"protocol"`
	PollIntervalMS int     `json:"poll_interval_ms,omitempty"`
	Points         []Point `json:"points"`
}

// DeletePointListRequest removes an asset's declarations entirely.
type DeletePointListRequest struct {
	AssetID string `json:"asset_id"`
}

// maxPointsPerAsset bounds one list. It is generous -- a large Modbus device
// exposes a few hundred registers -- and exists so that a malformed bulk import
// fails with a clear message rather than by exhausting memory.
const maxPointsPerAsset = 5000

// pointNameMaxLen matches the practical limit of a tag name on the data plane.
const pointNameMaxLen = 200

// Validate checks a point in isolation. Cross-point rules (unique names) belong
// to the list.
func (p *Point) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("point name is required")
	}
	if len(p.Name) > pointNameMaxLen {
		return fmt.Errorf("point name %q is longer than %d characters", p.Name, pointNameMaxLen)
	}
	switch p.ValueType {
	case ValueTypeNumber, ValueTypeText, ValueTypeFlag:
	default:
		return fmt.Errorf("point %q has invalid value_type %q (allowed: %s, %s, %s)",
			p.Name, p.ValueType, ValueTypeNumber, ValueTypeText, ValueTypeFlag)
	}
	if p.Address == "" {
		return fmt.Errorf("point %q requires an address", p.Name)
	}
	return nil
}

// encodingJSON renders Encoding for storage. A nil map stores as an empty
// object so the column is never NULL and readers need no nil handling.
func (p *Point) encodingJSON() (string, error) {
	if len(p.Encoding) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(p.Encoding)
	if err != nil {
		return "", fmt.Errorf("point %q has an unencodable encoding: %w", p.Name, err)
	}
	return string(b), nil
}
