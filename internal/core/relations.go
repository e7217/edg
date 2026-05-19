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

const DefaultTraversalMaxDepth = 10

// Direction describes the relation direction used to reach an asset node.
type Direction string

const (
	DirectionIncoming Direction = "incoming"
	DirectionOutgoing Direction = "outgoing"
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

// TraversalOptions configures graph traversal queries.
type TraversalOptions struct {
	RelationTypes []RelationType `json:"relation_types,omitempty"`
	MaxDepth      int            `json:"max_depth,omitempty"`
}

// AssetNode is an asset returned by graph traversal.
type AssetNode struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	TemplateName string       `json:"template_name,omitempty"`
	Depth        int          `json:"depth"`
	RelationType RelationType `json:"relation_type,omitempty"`
	ParentID     string       `json:"parent_id,omitempty"`
	Direction    Direction    `json:"direction,omitempty"`
}

// AssetTreeNode is a recursive asset subtree node.
type AssetTreeNode struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	TemplateName string           `json:"template_name,omitempty"`
	Depth        int              `json:"depth"`
	RelationType RelationType     `json:"relation_type,omitempty"`
	ParentID     string           `json:"parent_id,omitempty"`
	Children     []*AssetTreeNode `json:"children,omitempty"`
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

// DefaultHierarchicalRelationTypes returns relation types used for hierarchy traversal.
func DefaultHierarchicalRelationTypes() []RelationType {
	return []RelationType{RelationPartOf, RelationLocatedIn}
}
