package core

import "time"

// Asset represents a registered asset (sensor, equipment, etc.)
type Asset struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	TemplateName string    `json:"template_name,omitempty"`
	Labels       []string  `json:"labels,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// AssetTemplate defines an asset type loaded from YAML
type AssetTemplate struct {
	Name      string          `yaml:"name" json:"name"`
	Resources []AssetResource `yaml:"resources" json:"resources"`
}

// AssetResource defines a data point provided by an asset
type AssetResource struct {
	Name      string `yaml:"name" json:"name"`           // maps to TagValue.Name
	ValueType string `yaml:"valueType" json:"valueType"` // NUMBER, TEXT, FLAG
	Unit      string `yaml:"unit,omitempty" json:"unit,omitempty"`
}

// ValueType constants
const (
	ValueTypeNumber = "NUMBER"
	ValueTypeText   = "TEXT"
	ValueTypeFlag   = "FLAG"
)
