package core

// AssetData represents data collected from an asset
type AssetData struct {
	AssetID   string            `json:"asset_id"`
	Timestamp int64             `json:"timestamp"`
	Values    []TagValue        `json:"values"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// TagValue represents an individual tag value
type TagValue struct {
	Name    string   `json:"name"`
	Number  *float64 `json:"number,omitempty"`
	Text    *string  `json:"text,omitempty"`
	Flag    *bool    `json:"flag,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Quality string   `json:"quality"`
}
