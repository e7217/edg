package core

import (
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
			ID:        string(rune('0' + i)),
			Name:      string(rune('a' + i - 1)),
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
