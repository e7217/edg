package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreTraversal_GetAncestors(t *testing.T) {
	store := newTraversalTestStore(t)

	nodes, err := store.GetAncestors("sensor-001", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)

	require.Len(t, nodes, 3)
	assert.Equal(t, "pump-a", nodes[0].ID)
	assert.Equal(t, "equipment", nodes[0].TemplateName)
	assert.Equal(t, 1, nodes[0].Depth)
	assert.Equal(t, "line-3", nodes[1].ID)
	assert.Equal(t, 2, nodes[1].Depth)
	assert.Equal(t, "factory-1", nodes[2].ID)
	assert.Equal(t, 3, nodes[2].Depth)
}

func TestStoreTraversal_RelationTypeFilter(t *testing.T) {
	store := newTraversalTestStore(t)

	nodes, err := store.GetAncestors("sensor-001", []RelationType{RelationLocatedIn}, 10)
	require.NoError(t, err)

	require.Len(t, nodes, 1)
	assert.Equal(t, "room-9", nodes[0].ID)
	assert.Equal(t, RelationLocatedIn, nodes[0].RelationType)
}

func TestStoreTraversal_MaxDepth(t *testing.T) {
	store := newTraversalTestStore(t)

	nodes, err := store.GetAncestors("sensor-001", []RelationType{RelationPartOf}, 2)
	require.NoError(t, err)

	require.Len(t, nodes, 2)
	assert.Equal(t, "pump-a", nodes[0].ID)
	assert.Equal(t, "line-3", nodes[1].ID)
}

func TestStoreTraversal_GetDescendants(t *testing.T) {
	store := newTraversalTestStore(t)

	nodes, err := store.GetDescendants("factory-1", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)

	require.Len(t, nodes, 3)
	assert.Equal(t, "line-3", nodes[0].ID)
	assert.Equal(t, "pump-a", nodes[1].ID)
	assert.Equal(t, "sensor-001", nodes[2].ID)
}

func TestStoreTraversal_GetSubtree(t *testing.T) {
	store := newTraversalTestStore(t)

	tree, err := store.GetSubtree("factory-1", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)
	require.NotNil(t, tree)

	assert.Equal(t, "factory-1", tree.ID)
	require.Len(t, tree.Children, 1)
	assert.Equal(t, "line-3", tree.Children[0].ID)
	require.Len(t, tree.Children[0].Children, 1)
	assert.Equal(t, "pump-a", tree.Children[0].Children[0].ID)
	require.Len(t, tree.Children[0].Children[0].Children, 1)
	assert.Equal(t, "sensor-001", tree.Children[0].Children[0].Children[0].ID)
}

func TestStoreTraversal_GetConnected(t *testing.T) {
	store := newTraversalTestStore(t)

	nodes, err := store.GetConnected("pump-a", RelationConnectedTo)
	require.NoError(t, err)

	require.Len(t, nodes, 1)
	assert.Equal(t, "motor-1", nodes[0].ID)
	assert.Equal(t, RelationConnectedTo, nodes[0].RelationType)
}

func TestStoreTraversal_CycleGuard(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	createTraversalAsset(t, store, "a", "A", "node")
	createTraversalAsset(t, store, "b", "B", "node")
	createTraversalRelation(t, store, "rel-a-b", "a", "b", RelationPartOf)
	createTraversalRelation(t, store, "rel-b-a", "b", "a", RelationPartOf)

	nodes, err := store.GetAncestors("a", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)

	require.Len(t, nodes, 1)
	assert.Equal(t, "b", nodes[0].ID)
}

func TestStoreTraversal_MissingAssetReturnsEmptyResults(t *testing.T) {
	store := newTraversalTestStore(t)

	ancestors, err := store.GetAncestors("missing", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)
	assert.Empty(t, ancestors)

	descendants, err := store.GetDescendants("missing", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)
	assert.Empty(t, descendants)

	connected, err := store.GetConnected("missing", RelationConnectedTo)
	require.NoError(t, err)
	assert.Empty(t, connected)

	tree, err := store.GetSubtree("missing", []RelationType{RelationPartOf}, 10)
	require.NoError(t, err)
	assert.Nil(t, tree)
}

func newTraversalTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	createTraversalAsset(t, store, "factory-1", "Factory 1", "factory")
	createTraversalAsset(t, store, "line-3", "Line 3", "line")
	createTraversalAsset(t, store, "pump-a", "Pump A", "equipment")
	createTraversalAsset(t, store, "sensor-001", "Sensor 001", "sensor")
	createTraversalAsset(t, store, "room-9", "Room 9", "room")
	createTraversalAsset(t, store, "motor-1", "Motor 1", "equipment")

	createTraversalRelation(t, store, "rel-sensor-pump", "sensor-001", "pump-a", RelationPartOf)
	createTraversalRelation(t, store, "rel-pump-line", "pump-a", "line-3", RelationPartOf)
	createTraversalRelation(t, store, "rel-line-factory", "line-3", "factory-1", RelationPartOf)
	createTraversalRelation(t, store, "rel-sensor-room", "sensor-001", "room-9", RelationLocatedIn)
	createTraversalRelation(t, store, "rel-pump-motor", "pump-a", "motor-1", RelationConnectedTo)

	return store
}

func createTraversalAsset(t *testing.T, store *Store, id, name, templateName string) {
	t.Helper()

	require.NoError(t, store.CreateAsset(&Asset{
		ID:           id,
		Name:         name,
		TemplateName: templateName,
		CreatedAt:    time.Now(),
	}))
}

func createTraversalRelation(t *testing.T, store *Store, id, sourceID, targetID string, relationType RelationType) {
	t.Helper()

	require.NoError(t, store.CreateRelation(&AssetRelation{
		ID:            id,
		SourceAssetID: sourceID,
		TargetAssetID: targetID,
		RelationType:  relationType,
		CreatedAt:     time.Now(),
	}))
}
