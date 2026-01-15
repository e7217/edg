package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewStore_Success tests successful store creation with in-memory DB
func TestNewStore_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	require.NotNil(t, store)
	defer store.Close()

	// Verify DB is initialized by checking stats
	stats, err := store.GetStats()
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalAssets)
}

// TestCreateAsset_Success tests asset creation
func TestCreateAsset_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	asset := &Asset{
		ID:           "asset-001",
		Name:         "test-sensor",
		TemplateName: "temperature-sensor",
		Labels:       []string{"building-a", "floor-1"},
		CreatedAt:    time.Now(),
	}

	err = store.CreateAsset(asset)
	require.NoError(t, err)

	// Verify creation
	retrieved, err := store.GetAsset("asset-001")
	require.NoError(t, err)
	assert.Equal(t, asset.ID, retrieved.ID)
	assert.Equal(t, asset.Name, retrieved.Name)
	assert.Equal(t, asset.TemplateName, retrieved.TemplateName)
	assert.Equal(t, asset.Labels, retrieved.Labels)
}

// TestCreateAsset_DuplicateName tests duplicate name rejection
func TestCreateAsset_DuplicateName(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	asset1 := &Asset{
		ID:        "asset-001",
		Name:      "duplicate-name",
		CreatedAt: time.Now(),
	}

	asset2 := &Asset{
		ID:        "asset-002",
		Name:      "duplicate-name", // Same name, different ID
		CreatedAt: time.Now(),
	}

	// First insert should succeed
	err = store.CreateAsset(asset1)
	require.NoError(t, err)

	// Second insert should fail due to UNIQUE constraint
	err = store.CreateAsset(asset2)
	assert.Error(t, err)
}

// TestGetAsset_Success tests retrieval by ID
func TestGetAsset_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	asset := &Asset{
		ID:        "asset-001",
		Name:      "test-asset",
		CreatedAt: time.Now(),
	}

	err = store.CreateAsset(asset)
	require.NoError(t, err)

	retrieved, err := store.GetAsset("asset-001")
	require.NoError(t, err)
	assert.Equal(t, asset.ID, retrieved.ID)
	assert.Equal(t, asset.Name, retrieved.Name)
}

// TestGetAsset_NotFound tests retrieval of non-existent asset
func TestGetAsset_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	retrieved, err := store.GetAsset("non-existent")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestGetAssetByName_Success tests retrieval by name
func TestGetAssetByName_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	asset := &Asset{
		ID:        "asset-001",
		Name:      "unique-name",
		CreatedAt: time.Now(),
	}

	err = store.CreateAsset(asset)
	require.NoError(t, err)

	retrieved, err := store.GetAssetByName("unique-name")
	require.NoError(t, err)
	assert.Equal(t, asset.ID, retrieved.ID)
	assert.Equal(t, asset.Name, retrieved.Name)
}

// TestDeleteAsset_Success tests asset deletion
func TestDeleteAsset_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	asset := &Asset{
		ID:        "asset-001",
		Name:      "to-be-deleted",
		CreatedAt: time.Now(),
	}

	err = store.CreateAsset(asset)
	require.NoError(t, err)

	// Delete asset
	err = store.DeleteAsset("asset-001")
	require.NoError(t, err)

	// Verify deletion
	retrieved, err := store.GetAsset("asset-001")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestDeleteAsset_NotFound tests deletion of non-existent asset
func TestDeleteAsset_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	err = store.DeleteAsset("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "asset not found")
}

// TestListAssets tests listing all assets
func TestListAssets(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create multiple assets
	assets := []*Asset{
		{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()},
		{ID: "asset-002", Name: "sensor-2", CreatedAt: time.Now().Add(1 * time.Second)},
		{ID: "asset-003", Name: "sensor-3", CreatedAt: time.Now().Add(2 * time.Second)},
	}

	for _, asset := range assets {
		err = store.CreateAsset(asset)
		require.NoError(t, err)
	}

	// List all assets
	retrieved, err := store.ListAssets()
	require.NoError(t, err)
	assert.Len(t, retrieved, 3)

	// Verify ordering (DESC by created_at)
	assert.Equal(t, "asset-003", retrieved[0].ID)
	assert.Equal(t, "asset-002", retrieved[1].ID)
	assert.Equal(t, "asset-001", retrieved[2].ID)
}

// ==================== AssetRelation Tests ====================

// TestCreateRelation_Success tests successful relation creation
func TestCreateRelation_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create source and target assets
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Create relation
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
		Metadata: map[string]string{
			"installed_date": "2025-01-15",
		},
	}

	err = store.CreateRelation(relation)
	require.NoError(t, err)

	// Verify creation
	retrieved, err := store.GetRelation("rel-001")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, relation.ID, retrieved.ID)
	assert.Equal(t, relation.SourceAssetID, retrieved.SourceAssetID)
	assert.Equal(t, relation.TargetAssetID, retrieved.TargetAssetID)
	assert.Equal(t, relation.RelationType, retrieved.RelationType)
	assert.Equal(t, relation.Metadata, retrieved.Metadata)
}

// TestCreateRelation_DuplicateError tests duplicate relation rejection
func TestCreateRelation_DuplicateError(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create assets
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Create first relation
	relation1 := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(relation1))

	// Try to create duplicate (same source, target, type)
	relation2 := &AssetRelation{
		ID:            "rel-002",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf, // Same type
		CreatedAt:     time.Now(),
	}

	err = store.CreateRelation(relation2)
	assert.Error(t, err, "duplicate relation should be rejected")
}

// TestCreateRelation_InvalidSourceAsset tests creation with non-existent source
func TestCreateRelation_InvalidSourceAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create only target asset
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(targetAsset))

	// Try to create relation with invalid source
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "non-existent",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}

	err = store.CreateRelation(relation)
	assert.Error(t, err, "relation with invalid source should fail")
}

// TestCreateRelation_InvalidTargetAsset tests creation with non-existent target
func TestCreateRelation_InvalidTargetAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create only source asset
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))

	// Try to create relation with invalid target
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "non-existent",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}

	err = store.CreateRelation(relation)
	assert.Error(t, err, "relation with invalid target should fail")
}

// TestGetRelation_Found tests successful relation retrieval
func TestGetRelation_Found(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create assets and relation
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationConnectedTo,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(relation))

	// Retrieve relation
	retrieved, err := store.GetRelation("rel-001")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "rel-001", retrieved.ID)
}

// TestGetRelation_NotFound tests retrieval of non-existent relation
func TestGetRelation_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	retrieved, err := store.GetRelation("non-existent")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestGetRelationsBySourceAsset tests retrieval by source asset
func TestGetRelationsBySourceAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create assets
	source := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	target1 := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	target2 := &Asset{ID: "asset-003", Name: "equipment-2", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(source))
	require.NoError(t, store.CreateAsset(target1))
	require.NoError(t, store.CreateAsset(target2))

	// Create relations from source
	rel1 := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	rel2 := &AssetRelation{
		ID:            "rel-002",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-003",
		RelationType:  RelationConnectedTo,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(rel1))
	require.NoError(t, store.CreateRelation(rel2))

	// Get relations by source
	relations, err := store.GetRelationsBySourceAsset("asset-001")
	require.NoError(t, err)
	assert.Len(t, relations, 2)
}

// TestGetRelationsByTargetAsset tests retrieval by target asset
func TestGetRelationsByTargetAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create assets
	source1 := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	source2 := &Asset{ID: "asset-002", Name: "sensor-2", CreatedAt: time.Now()}
	target := &Asset{ID: "asset-003", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(source1))
	require.NoError(t, store.CreateAsset(source2))
	require.NoError(t, store.CreateAsset(target))

	// Create relations to target
	rel1 := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-003",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	rel2 := &AssetRelation{
		ID:            "rel-002",
		SourceAssetID: "asset-002",
		TargetAssetID: "asset-003",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(rel1))
	require.NoError(t, store.CreateRelation(rel2))

	// Get relations by target
	relations, err := store.GetRelationsByTargetAsset("asset-003")
	require.NoError(t, err)
	assert.Len(t, relations, 2)
}

// TestDeleteRelation_Success tests relation deletion
func TestDeleteRelation_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create assets and relation
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(relation))

	// Delete relation
	err = store.DeleteRelation("rel-001")
	require.NoError(t, err)

	// Verify deletion
	retrieved, err := store.GetRelation("rel-001")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestDeleteRelation_NotFound tests deletion of non-existent relation
func TestDeleteRelation_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	err = store.DeleteRelation("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relation not found")
}

// TestCascadeDelete_WhenAssetDeleted tests cascade deletion
func TestCascadeDelete_WhenAssetDeleted(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create assets
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Create relation
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(relation))

	// Delete source asset
	err = store.DeleteAsset("asset-001")
	require.NoError(t, err)

	// Relation should be automatically deleted (cascade)
	retrieved, err := store.GetRelation("rel-001")
	require.NoError(t, err)
	assert.Nil(t, retrieved, "relation should be cascade deleted")
}

// ==================== JSON Unmarshal Error Tests ====================

// TestGetAsset_InvalidLabelsJSON tests error handling when labels JSON is malformed
func TestGetAsset_InvalidLabelsJSON(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Insert asset with malformed JSON directly into DB
	_, err = store.db.Exec(
		`INSERT INTO assets (id, name, template_name, labels, created_at) VALUES (?, ?, ?, ?, ?)`,
		"asset-001", "test-sensor", "temperature", "invalid-json-{", time.Now(),
	)
	require.NoError(t, err)

	// Attempt to get asset - should return error due to invalid JSON
	asset, err := store.GetAsset("asset-001")
	assert.Error(t, err, "should return error for malformed labels JSON")
	assert.Nil(t, asset)
	if err != nil {
		assert.Contains(t, err.Error(), "unmarshal")
	}
}

// TestGetAssetByName_InvalidLabelsJSON tests error handling when labels JSON is malformed
func TestGetAssetByName_InvalidLabelsJSON(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Insert asset with malformed JSON directly into DB
	_, err = store.db.Exec(
		`INSERT INTO assets (id, name, template_name, labels, created_at) VALUES (?, ?, ?, ?, ?)`,
		"asset-001", "test-sensor", "temperature", "{invalid-json", time.Now(),
	)
	require.NoError(t, err)

	// Attempt to get asset by name - should return error due to invalid JSON
	asset, err := store.GetAssetByName("test-sensor")
	assert.Error(t, err, "should return error for malformed labels JSON")
	assert.Nil(t, asset)
	if err != nil {
		assert.Contains(t, err.Error(), "unmarshal")
	}
}

// TestListAssets_InvalidLabelsJSON tests error handling when any asset has malformed labels JSON
func TestListAssets_InvalidLabelsJSON(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Insert valid asset
	validAsset := &Asset{
		ID:        "asset-001",
		Name:      "valid-sensor",
		CreatedAt: time.Now(),
		Labels:    []string{"label1"},
	}
	require.NoError(t, store.CreateAsset(validAsset))

	// Insert asset with malformed JSON directly into DB
	_, err = store.db.Exec(
		`INSERT INTO assets (id, name, template_name, labels, created_at) VALUES (?, ?, ?, ?, ?)`,
		"asset-002", "invalid-sensor", "temperature", "not-json-at-all", time.Now(),
	)
	require.NoError(t, err)

	// Attempt to list assets - should return error due to invalid JSON
	assets, err := store.ListAssets()
	assert.Error(t, err, "should return error when any asset has malformed labels JSON")
	assert.Nil(t, assets)
}

// TestGetRelation_InvalidMetadataJSON tests error handling when metadata JSON is malformed
func TestGetRelation_InvalidMetadataJSON(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create valid assets first
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Insert relation with malformed metadata JSON directly into DB
	_, err = store.db.Exec(
		`INSERT INTO asset_relations (id, source_asset_id, target_asset_id, relation_type, created_at, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"rel-001", "asset-001", "asset-002", RelationPartOf, time.Now(), "{malformed-json:",
	)
	require.NoError(t, err)

	// Attempt to get relation - should return error due to invalid JSON
	relation, err := store.GetRelation("rel-001")
	assert.Error(t, err, "should return error for malformed metadata JSON")
	assert.Nil(t, relation)
	if err != nil {
		assert.Contains(t, err.Error(), "unmarshal")
	}
}

// TestGetRelationsBySourceAsset_InvalidMetadataJSON tests error handling for malformed metadata
func TestGetRelationsBySourceAsset_InvalidMetadataJSON(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create valid assets first
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Insert valid relation
	validRelation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, store.CreateRelation(validRelation))

	// Insert relation with malformed metadata JSON directly into DB
	_, err = store.db.Exec(
		`INSERT INTO asset_relations (id, source_asset_id, target_asset_id, relation_type, created_at, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"rel-002", "asset-001", "asset-002", RelationConnectedTo, time.Now(), "invalid-json-data",
	)
	require.NoError(t, err)

	// Attempt to get relations by source - should return error due to invalid JSON
	relations, err := store.GetRelationsBySourceAsset("asset-001")
	assert.Error(t, err, "should return error when any relation has malformed metadata JSON")
	assert.Nil(t, relations)
}

// TestGetRelationsByTargetAsset_InvalidMetadataJSON tests error handling for malformed metadata
func TestGetRelationsByTargetAsset_InvalidMetadataJSON(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create valid assets first
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Insert relation with malformed metadata JSON directly into DB
	_, err = store.db.Exec(
		`INSERT INTO asset_relations (id, source_asset_id, target_asset_id, relation_type, created_at, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"rel-001", "asset-001", "asset-002", RelationPartOf, time.Now(), "}invalid-json{",
	)
	require.NoError(t, err)

	// Attempt to get relations by target - should return error due to invalid JSON
	relations, err := store.GetRelationsByTargetAsset("asset-002")
	assert.Error(t, err, "should return error when any relation has malformed metadata JSON")
	assert.Nil(t, relations)
}
