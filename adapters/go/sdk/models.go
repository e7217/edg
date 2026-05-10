package sdk

import (
	"encoding/json"
	"fmt"
	"time"
)

// TagValue is a single tag reading. Mirrors core's wire type. Pointer fields
// represent absence — Python SDK's optional fields with None.
type TagValue struct {
	Name    string   `json:"name"`
	Quality string   `json:"quality"`
	Number  *float64 `json:"number,omitempty"`
	Text    *string  `json:"text,omitempty"`
	Flag    *bool    `json:"flag,omitempty"`
	Unit    string   `json:"unit,omitempty"`
}

// AssetData is the payload published to SubjectAssetData. If Timestamp is
// zero, Client.PublishAssetData fills it with the current epoch milliseconds.
type AssetData struct {
	AssetID   string            `json:"asset_id"`
	Timestamp int64             `json:"timestamp"`
	Values    []TagValue        `json:"values"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// RelationType enumerates supported asset relationships. Matches
// internal/core/relations.go.
type RelationType string

const (
	RelationPartOf      RelationType = "partOf"
	RelationConnectedTo RelationType = "connectedTo"
	RelationLocatedIn   RelationType = "locatedIn"
)

// Asset matches internal/core/metadata.go's wire form.
type Asset struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	TemplateName string            `json:"template_name,omitempty"`
	Labels       []string          `json:"labels,omitempty"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
	Source       string            `json:"source"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// AssetRelation matches internal/core/relations.go's wire form.
type AssetRelation struct {
	ID            string            `json:"id"`
	SourceAssetID string            `json:"source_asset_id"`
	TargetAssetID string            `json:"target_asset_id"`
	RelationType  RelationType      `json:"relation_type"`
	CreatedAt     time.Time         `json:"created_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// EventType / EntityType values, matches internal/core/events.go.
const (
	EventCreated = "created"
	EventUpdated = "updated"
	EventDeleted = "deleted"

	EntityAsset    = "asset"
	EntityRelation = "relation"

	EventSchemaVersion = 1
)

// MetaChangeEvent is the payload published on SubjectMetaChangedAll. Before
// and After are kept as raw JSON because their concrete type depends on
// EntityType. Use DecodeBefore / DecodeAfter to unmarshal them.
type MetaChangeEvent struct {
	SchemaVersion int             `json:"schema_version"`
	EventType     string          `json:"event_type"`
	EntityType    string          `json:"entity_type"`
	EntityID      string          `json:"entity_id"`
	Source        string          `json:"source"`
	Timestamp     time.Time       `json:"timestamp"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
}

// DecodeBefore unmarshals the Before snapshot into v. v should be *Asset for
// EntityAsset events and *AssetRelation for EntityRelation events. Returns
// nil if Before is empty (e.g. created events).
func (e *MetaChangeEvent) DecodeBefore(v any) error {
	return decodeRaw(e.Before, v)
}

// DecodeAfter unmarshals the After snapshot into v. Returns nil if After is
// empty (e.g. deleted events).
func (e *MetaChangeEvent) DecodeAfter(v any) error {
	return decodeRaw(e.After, v)
}

func decodeRaw(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decode meta change event payload: %w", err)
	}
	return nil
}

// Request payloads. The shapes match internal/core/meta_handler.go.

type CreateAssetRequest struct {
	Name         string            `json:"name"`
	TemplateName string            `json:"template_name,omitempty"`
	Labels       []string          `json:"labels,omitempty"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
	Source       string            `json:"source,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

type GetAssetRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type UpdateAssetRequest struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	TemplateName string            `json:"template_name,omitempty"`
	Labels       []string          `json:"labels,omitempty"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
	Source       string            `json:"source,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

type DeleteAssetRequest struct {
	ID     string `json:"id"`
	Source string `json:"source,omitempty"`
}

type CreateRelationRequest struct {
	SourceAssetID string            `json:"source_asset_id"`
	TargetAssetID string            `json:"target_asset_id"`
	RelationType  RelationType      `json:"relation_type"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Source        string            `json:"source,omitempty"`
}

// ListRelationsRequest filters relations. AssetID is required; Direction
// defaults to "both" when empty.
type ListRelationsRequest struct {
	AssetID      string       `json:"asset_id"`
	RelationType RelationType `json:"relation_type,omitempty"`
	Direction    string       `json:"direction,omitempty"` // "outgoing", "incoming", "both"
}

type DeleteRelationRequest struct {
	ID     string `json:"id"`
	Source string `json:"source,omitempty"`
}

// coreResponse is the wrapper Core uses for request/reply replies.
type coreResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}
