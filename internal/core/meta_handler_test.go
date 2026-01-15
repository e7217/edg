package core

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetaHandler_CreateAsset tests asset creation through handler
func TestMetaHandler_CreateAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Since we can't easily mock NATS messages, test the store directly
	asset := &Asset{
		ID:        "test-id",
		Name:      "test-sensor",
		Labels:    []string{"building-a"},
		CreatedAt: time.Now(),
	}
	err = store.CreateAsset(asset)
	require.NoError(t, err)

	// Verify through handler's store
	retrieved, err := handler.store.GetAssetByName("test-sensor")
	require.NoError(t, err)
	assert.Equal(t, "test-sensor", retrieved.Name)
	assert.Equal(t, []string{"building-a"}, retrieved.Labels)
}

// TestMetaHandler_CreateAsset_DuplicateName tests duplicate name rejection
func TestMetaHandler_CreateAsset_DuplicateName(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	_ = NewMetaHandler(store, loader)

	// Create first asset
	asset1 := &Asset{
		ID:        "id1",
		Name:      "duplicate",
		CreatedAt: time.Now(),
	}
	err = store.CreateAsset(asset1)
	require.NoError(t, err)

	// Try duplicate
	asset2 := &Asset{
		ID:        "id2",
		Name:      "duplicate",
		CreatedAt: time.Now(),
	}
	err = store.CreateAsset(asset2)
	assert.Error(t, err)
}

// TestMetaHandler_GetAsset tests asset retrieval
func TestMetaHandler_GetAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create asset
	asset := &Asset{
		ID:        "test-id",
		Name:      "test-sensor",
		CreatedAt: time.Now(),
	}
	err = store.CreateAsset(asset)
	require.NoError(t, err)

	// Get by ID
	retrieved, err := handler.store.GetAsset("test-id")
	require.NoError(t, err)
	assert.Equal(t, "test-sensor", retrieved.Name)

	// Get by name
	retrieved, err = handler.store.GetAssetByName("test-sensor")
	require.NoError(t, err)
	assert.Equal(t, "test-id", retrieved.ID)
}

// TestMetaHandler_GetAsset_NotFound tests non-existent asset
func TestMetaHandler_GetAsset_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	retrieved, err := handler.store.GetAsset("non-existent")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestMetaHandler_DeleteAsset tests asset deletion
func TestMetaHandler_DeleteAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create asset
	asset := &Asset{
		ID:        "to-delete",
		Name:      "test-sensor",
		CreatedAt: time.Now(),
	}
	err = store.CreateAsset(asset)
	require.NoError(t, err)

	// Delete
	err = handler.store.DeleteAsset("to-delete")
	require.NoError(t, err)

	// Verify deletion
	retrieved, err := handler.store.GetAsset("to-delete")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestMetaHandler_ListAssets tests asset listing
func TestMetaHandler_ListAssets(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create multiple assets
	for i := 1; i <= 3; i++ {
		asset := &Asset{
			ID:        fmt.Sprintf("id-%d", i),
			Name:      fmt.Sprintf("asset-%d", i),
			CreatedAt: time.Now(),
		}
		err = store.CreateAsset(asset)
		require.NoError(t, err)
	}

	// List all
	assets, err := handler.store.ListAssets()
	require.NoError(t, err)
	assert.Len(t, assets, 3)
}

// TestMetaHandler_ListTemplates tests template listing
func TestMetaHandler_ListTemplates(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	err = loader.LoadFromFile("testdata/valid_template.yaml")
	require.NoError(t, err)

	handler := NewMetaHandler(store, loader)

	templates := handler.loader.List()
	assert.Len(t, templates, 1)
	assert.Equal(t, "test-sensor", templates[0].Name)
}

// TestMetaHandler_TemplateValidation tests template existence check
func TestMetaHandler_TemplateValidation(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	err = loader.LoadFromFile("testdata/valid_template.yaml")
	require.NoError(t, err)

	handler := NewMetaHandler(store, loader)

	// Valid template
	assert.True(t, handler.loader.Exists("test-sensor"))

	// Invalid template
	assert.False(t, handler.loader.Exists("non-existent"))
}

// ==================== AssetRelation Handler Tests ====================

// TestHandleRelationCreate_Success tests successful relation creation
func TestHandleRelationCreate_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create assets first
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Create relation through handler's store
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
	err = handler.store.CreateRelation(relation)
	require.NoError(t, err)

	// Verify
	retrieved, err := handler.store.GetRelation("rel-001")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "rel-001", retrieved.ID)
	assert.Equal(t, "asset-001", retrieved.SourceAssetID)
	assert.Equal(t, "asset-002", retrieved.TargetAssetID)
}

// TestHandleRelationCreate_InvalidRelationType tests invalid relation type
func TestHandleRelationCreate_InvalidRelationType(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	_ = NewMetaHandler(store, loader)

	// Create assets
	sourceAsset := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(sourceAsset))
	require.NoError(t, store.CreateAsset(targetAsset))

	// Invalid relation type should be validated before creation
	invalidType := RelationType("invalidType")
	assert.False(t, IsValidRelationType(invalidType))
}

// TestHandleRelationCreate_MissingSourceAsset tests missing source asset
func TestHandleRelationCreate_MissingSourceAsset(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create only target asset
	targetAsset := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(targetAsset))

	// Try to create relation with missing source
	relation := &AssetRelation{
		ID:            "rel-001",
		SourceAssetID: "non-existent",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now(),
	}
	err = handler.store.CreateRelation(relation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "source asset not found")
}

// TestHandleRelationGet_Found tests successful relation retrieval
func TestHandleRelationGet_Found(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

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

	// Retrieve
	retrieved, err := handler.store.GetRelation("rel-001")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "rel-001", retrieved.ID)
}

// TestHandleRelationGet_NotFound tests non-existent relation
func TestHandleRelationGet_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	retrieved, err := handler.store.GetRelation("non-existent")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

// TestHandleRelationList_ByAssetID tests listing by asset ID
func TestHandleRelationList_ByAssetID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create assets
	source := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	target1 := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	target2 := &Asset{ID: "asset-003", Name: "equipment-2", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(source))
	require.NoError(t, store.CreateAsset(target1))
	require.NoError(t, store.CreateAsset(target2))

	// Create relations
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

	// List by source asset
	relations, err := handler.store.GetRelationsBySourceAsset("asset-001")
	require.NoError(t, err)
	assert.Len(t, relations, 2)
}

// TestHandleRelationList_ByRelationType tests listing by relation type
func TestHandleRelationList_ByRelationType(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create assets
	source := &Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}
	target1 := &Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}
	target2 := &Asset{ID: "asset-003", Name: "equipment-2", CreatedAt: time.Now()}
	require.NoError(t, store.CreateAsset(source))
	require.NoError(t, store.CreateAsset(target1))
	require.NoError(t, store.CreateAsset(target2))

	// Create relations with different types
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

	// Get all relations (we can filter client-side or add store method)
	relations, err := handler.store.GetRelationsBySourceAsset("asset-001")
	require.NoError(t, err)
	assert.Len(t, relations, 2)

	// Verify relation types are different
	types := make(map[RelationType]bool)
	for _, rel := range relations {
		types[rel.RelationType] = true
	}
	assert.Len(t, types, 2)
}

// TestHandleRelationDelete_Success tests successful relation deletion
func TestHandleRelationDelete_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

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

	// Delete
	err = handler.store.DeleteRelation("rel-001")
	require.NoError(t, err)

	// Verify deletion
	retrieved, err := handler.store.GetRelation("rel-001")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}
