package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRelationType_IsValid_ValidTypes tests validation of valid relation types
func TestRelationType_IsValid_ValidTypes(t *testing.T) {
	validTypes := []RelationType{
		RelationPartOf,
		RelationConnectedTo,
		RelationLocatedIn,
	}

	for _, relType := range validTypes {
		t.Run(string(relType), func(t *testing.T) {
			assert.True(t, IsValidRelationType(relType), "expected %s to be valid", relType)
		})
	}
}

// TestRelationType_IsValid_InvalidType tests validation of invalid relation types
func TestRelationType_IsValid_InvalidType(t *testing.T) {
	invalidTypes := []RelationType{
		"invalidType",
		"",
		"PART_OF", // wrong case
		"part-of", // wrong format
	}

	for _, relType := range invalidTypes {
		t.Run(string(relType), func(t *testing.T) {
			assert.False(t, IsValidRelationType(relType), "expected %s to be invalid", relType)
		})
	}
}

// TestValidRelationTypes_ReturnsAll tests that all valid types are returned
func TestValidRelationTypes_ReturnsAll(t *testing.T) {
	validTypes := ValidRelationTypes()

	assert.Len(t, validTypes, 3, "expected 3 valid relation types")
	assert.Contains(t, validTypes, RelationPartOf)
	assert.Contains(t, validTypes, RelationConnectedTo)
	assert.Contains(t, validTypes, RelationLocatedIn)
}

// TestAssetRelation_JSONSerialization tests JSON marshaling and unmarshaling
func TestAssetRelation_JSONSerialization(t *testing.T) {
	now := time.Now()
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     now,
		Metadata: map[string]string{
			"installed_date": "2025-01-15",
			"notes":          "sensor mounted on equipment",
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(relation)
	require.NoError(t, err)

	// Unmarshal back
	var unmarshaled AssetRelation
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify fields
	assert.Equal(t, relation.ID, unmarshaled.ID)
	assert.Equal(t, relation.SourceAssetID, unmarshaled.SourceAssetID)
	assert.Equal(t, relation.TargetAssetID, unmarshaled.TargetAssetID)
	assert.Equal(t, relation.RelationType, unmarshaled.RelationType)
	assert.Equal(t, relation.Metadata, unmarshaled.Metadata)
}

// TestAssetRelation_JSONSerialization_OmitsNilMetadata tests that nil metadata is omitted
func TestAssetRelation_JSONSerialization_OmitsNilMetadata(t *testing.T) {
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
		Metadata:      nil, // Nil metadata
	}

	jsonData, err := json.Marshal(relation)
	require.NoError(t, err)

	// Check that metadata field is omitted when nil
	jsonStr := string(jsonData)
	assert.NotContains(t, jsonStr, "metadata", "metadata field should be omitted when nil")
}
