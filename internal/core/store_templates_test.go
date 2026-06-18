package core

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tmplIntPtr(v int) *int { return &v }

func sampleTemplate() *AssetTemplate {
	return &AssetTemplate{
		Name: "temp-sensor",
		Resources: []AssetResource{
			{Name: "temperature", ValueType: ValueTypeNumber, Unit: "C"},
			{Name: "status", ValueType: ValueTypeText},
		},
		Constraints: TemplateConstraints{
			RequiredRelations: []RelationConstraint{
				{Type: RelationPartOf, TargetTemplate: "equipment", Min: tmplIntPtr(1), Max: tmplIntPtr(1)},
			},
			ForbiddenRelations: []RelationConstraint{
				{Type: RelationConnectedTo, TargetTemplate: "factory"},
			},
		},
	}
}

func TestStoreTemplate_UpsertGetRoundTrip(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.UpsertTemplate(sampleTemplate()))

	got, err := store.GetTemplate("temp-sensor")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "temp-sensor", got.Name)

	require.Len(t, got.Resources, 2)
	assert.Equal(t, "status", got.Resources[0].Name) // ordered by name
	assert.Equal(t, "temperature", got.Resources[1].Name)
	assert.Equal(t, ValueTypeNumber, got.Resources[1].ValueType)
	assert.Equal(t, "C", got.Resources[1].Unit)

	require.Len(t, got.Constraints.RequiredRelations, 1)
	rc := got.Constraints.RequiredRelations[0]
	assert.Equal(t, RelationPartOf, rc.Type)
	assert.Equal(t, "equipment", rc.TargetTemplate)
	require.NotNil(t, rc.Min)
	assert.Equal(t, 1, *rc.Min)
	require.NotNil(t, rc.Max)
	assert.Equal(t, 1, *rc.Max)

	require.Len(t, got.Constraints.ForbiddenRelations, 1)
	fc := got.Constraints.ForbiddenRelations[0]
	assert.Equal(t, RelationConnectedTo, fc.Type)
	assert.Nil(t, fc.Min)
	assert.Nil(t, fc.Max)
}

func TestStoreTemplate_UpsertReplacesChildren(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.UpsertTemplate(&AssetTemplate{
		Name:      "t",
		Resources: []AssetResource{{Name: "a", ValueType: ValueTypeNumber}, {Name: "b", ValueType: ValueTypeText}},
	}))
	// Re-upsert with one resource: children must be replaced, not appended.
	require.NoError(t, store.UpsertTemplate(&AssetTemplate{
		Name:      "t",
		Resources: []AssetResource{{Name: "c", ValueType: ValueTypeFlag}},
	}))

	got, err := store.GetTemplate("t")
	require.NoError(t, err)
	require.Len(t, got.Resources, 1)
	assert.Equal(t, "c", got.Resources[0].Name)
}

func TestStoreTemplate_ListAndDelete(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.UpsertTemplate(&AssetTemplate{Name: "b"}))
	require.NoError(t, store.UpsertTemplate(&AssetTemplate{Name: "a"}))

	list, err := store.ListTemplates()
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "a", list[0].Name) // ordered by name
	assert.Equal(t, "b", list[1].Name)

	require.NoError(t, store.DeleteTemplate("a"))
	got, err := store.GetTemplate("a")
	require.NoError(t, err)
	assert.Nil(t, got)

	list, err = store.ListTemplates()
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestStoreTemplate_GetMissing(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	got, err := store.GetTemplate("nope")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestTemplateLoader_StoreBackedPersistsAndReloads(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	require.Equal(t, 0, loader.Count())

	require.NoError(t, loader.Upsert(sampleTemplate()))
	assert.True(t, loader.Exists("temp-sensor"))

	// A fresh loader over the same store reloads the persisted template.
	reloaded, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	assert.Equal(t, 1, reloaded.Count())
	got := reloaded.Get("temp-sensor")
	require.NotNil(t, got)
	require.Len(t, got.Resources, 2)

	// Delete persists too.
	require.NoError(t, loader.Delete("temp-sensor"))
	again, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	assert.Equal(t, 0, again.Count())
}

func TestTemplateLoader_ExportRoundTrip(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	require.NoError(t, loader.Upsert(sampleTemplate()))

	dir := t.TempDir()
	require.NoError(t, loader.ExportToDir(dir))

	// Import the exported YAML into a fresh in-memory loader.
	fresh := NewTemplateLoader()
	require.NoError(t, fresh.LoadFromFile(filepath.Join(dir, "temp-sensor.yaml")))
	got := fresh.Get("temp-sensor")
	require.NotNil(t, got)
	require.Len(t, got.Resources, 2)
	require.Len(t, got.Constraints.RequiredRelations, 1)
	assert.Equal(t, "equipment", got.Constraints.RequiredRelations[0].TargetTemplate)
}
