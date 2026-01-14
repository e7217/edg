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
