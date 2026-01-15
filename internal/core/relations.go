package core

import "time"

// RelationType represents the type of relationship between assets
type RelationType string

const (
	// RelationPartOf indicates a hierarchical relationship (ssn:isPartOf)
	RelationPartOf RelationType = "partOf"
	// RelationConnectedTo indicates a peer/network connection (sosa:isHostedBy)
	RelationConnectedTo RelationType = "connectedTo"
	// RelationLocatedIn indicates spatial containment (schema:containedInPlace)
	RelationLocatedIn RelationType = "locatedIn"
)

// AssetRelation represents a relationship between two assets
type AssetRelation struct {
	ID            string            `json:"id"`
	SourceAssetID string            `json:"source_asset_id"`
	TargetAssetID string            `json:"target_asset_id"`
	RelationType  RelationType      `json:"relation_type"`
	CreatedAt     time.Time         `json:"created_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// IsValidRelationType checks if a RelationType is valid
func IsValidRelationType(rt RelationType) bool {
	switch rt {
	case RelationPartOf, RelationConnectedTo, RelationLocatedIn:
		return true
	default:
		return false
	}
}

// ValidRelationTypes returns all valid relation types
func ValidRelationTypes() []RelationType {
	return []RelationType{
		RelationPartOf,
		RelationConnectedTo,
		RelationLocatedIn,
	}
}
