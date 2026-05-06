package core

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testMetaResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func requestMeta(t *testing.T, nc interface {
	Request(string, []byte, time.Duration) (*nats.Msg, error)
}, subject string, payload interface{}) testMetaResponse {
	t.Helper()

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	msg, err := nc.Request(subject, data, time.Second)
	require.NoError(t, err)

	var resp testMetaResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	return resp
}

type testMetaChangeEvent struct {
	SchemaVersion int             `json:"schema_version"`
	EventType     EventType       `json:"event_type"`
	EntityType    EntityType      `json:"entity_type"`
	EntityID      string          `json:"entity_id"`
	Source        string          `json:"source"`
	Timestamp     time.Time       `json:"timestamp"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
}

func subscribeMetaEvents(t *testing.T, nc *nats.Conn, subject string) chan testMetaChangeEvent {
	t.Helper()

	received := make(chan testMetaChangeEvent, 4)
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var ev testMetaChangeEvent
		if err := json.Unmarshal(msg.Data, &ev); err == nil {
			received <- ev
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})
	require.NoError(t, nc.Flush())
	return received
}

func requireMetaEvent(t *testing.T, received <-chan testMetaChangeEvent) testMetaChangeEvent {
	t.Helper()

	select {
	case ev := <-received:
		require.Equal(t, EventSchemaVersion, ev.SchemaVersion)
		require.False(t, ev.Timestamp.IsZero())
		return ev
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for metadata change event")
		return testMetaChangeEvent{}
	}
}

func requireNoMetaEvent(t *testing.T, received <-chan testMetaChangeEvent) {
	t.Helper()

	select {
	case ev := <-received:
		t.Fatalf("unexpected metadata change event: %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

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

// TestMetaHandler_CreateAssetExtendedMetadata tests create requests with extended fields.
func TestMetaHandler_CreateAssetExtendedMetadata(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	handler := NewMetaHandler(store, NewTemplateLoader(), NewEventPublisher(nc))
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectAssetCreate, CreateAssetRequest{
		Name:        "extended-create",
		Labels:      []string{"line-1"},
		ExternalIDs: map[string]string{"irdi": "0173-1#02-BAA120#008"},
		Source:      "aas",
		Attributes:  map[string]string{"manufacturer": "ACME"},
	})

	require.True(t, resp.Success, resp.Error)

	var asset Asset
	require.NoError(t, json.Unmarshal(resp.Data, &asset))
	assert.Equal(t, "extended-create", asset.Name)
	assert.Equal(t, map[string]string{"irdi": "0173-1#02-BAA120#008"}, asset.ExternalIDs)
	assert.Equal(t, "aas", asset.Source)
	assert.Equal(t, map[string]string{"manufacturer": "ACME"}, asset.Attributes)
	assert.False(t, asset.UpdatedAt.IsZero())

	ev := requireMetaEvent(t, assetEvents)
	assert.Equal(t, EventCreated, ev.EventType)
	assert.Equal(t, EntityAsset, ev.EntityType)
	assert.Equal(t, asset.ID, ev.EntityID)
	assert.Equal(t, "aas", ev.Source)
	assert.Empty(t, ev.Before)
	require.NotEmpty(t, ev.After)

	var after Asset
	require.NoError(t, json.Unmarshal(ev.After, &after))
	assert.Equal(t, asset.ID, after.ID)
	assert.Equal(t, "extended-create", after.Name)
}

// TestMetaHandler_UpdateAsset tests full asset metadata replacement through NATS.
func TestMetaHandler_UpdateAsset(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	createdAt := time.Now().Add(-time.Hour)
	require.NoError(t, store.CreateAsset(&Asset{
		ID:           "asset-update",
		Name:         "old-name",
		TemplateName: "old-template",
		Labels:       []string{"old"},
		ExternalIDs:  map[string]string{"irdi": "old"},
		Source:       SourceManual,
		Attributes:   map[string]string{"status": "old"},
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}))

	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	handler := NewMetaHandler(store, NewTemplateLoader(), NewEventPublisher(nc))
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectAssetUpdate, UpdateAssetRequest{
		ID:          "asset-update",
		Name:        "new-name",
		Labels:      []string{"new"},
		ExternalIDs: map[string]string{"aas": "aas://example/asset-update"},
		Source:      "aas",
		Attributes:  map[string]string{"status": "new"},
	})

	require.True(t, resp.Success, resp.Error)

	var asset Asset
	require.NoError(t, json.Unmarshal(resp.Data, &asset))
	assert.Equal(t, "new-name", asset.Name)
	assert.Equal(t, []string{"new"}, asset.Labels)
	assert.Equal(t, map[string]string{"aas": "aas://example/asset-update"}, asset.ExternalIDs)
	assert.Equal(t, "aas", asset.Source)
	assert.Equal(t, map[string]string{"status": "new"}, asset.Attributes)
	assert.True(t, asset.UpdatedAt.After(createdAt))

	ev := requireMetaEvent(t, assetEvents)
	assert.Equal(t, EventUpdated, ev.EventType)
	assert.Equal(t, EntityAsset, ev.EntityType)
	assert.Equal(t, "asset-update", ev.EntityID)
	assert.Equal(t, "aas", ev.Source)
	require.NotEmpty(t, ev.Before)
	require.NotEmpty(t, ev.After)

	var before Asset
	require.NoError(t, json.Unmarshal(ev.Before, &before))
	assert.Equal(t, "old-name", before.Name)
	assert.Equal(t, SourceManual, before.Source)

	var after Asset
	require.NoError(t, json.Unmarshal(ev.After, &after))
	assert.Equal(t, "new-name", after.Name)
	assert.Equal(t, "aas", after.Source)
}

// TestMetaHandler_UpdateAssetNotFound tests update failure for missing assets.
func TestMetaHandler_UpdateAssetNotFound(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	handler := NewMetaHandler(store, NewTemplateLoader())
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectAssetUpdate, UpdateAssetRequest{
		ID:   "missing",
		Name: "missing",
	})

	assert.False(t, resp.Success)
	assert.Equal(t, "asset not found", resp.Error)
}

// TestMetaHandler_UpdateAssetInvalidPayload tests malformed update requests.
func TestMetaHandler_UpdateAssetInvalidPayload(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	handler := NewMetaHandler(store, NewTemplateLoader())
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	msg, err := nc.Request(SubjectAssetUpdate, []byte("{invalid json}"), time.Second)
	require.NoError(t, err)

	var resp testMetaResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.False(t, resp.Success)
	assert.Equal(t, "invalid request format", resp.Error)
}

// TestMetaHandler_DeleteAssetPublishesChangedEvent tests deletion events.
func TestMetaHandler_DeleteAssetPublishesChangedEvent(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.CreateAsset(&Asset{
		ID:        "asset-delete",
		Name:      "delete-me",
		Source:    "opcua",
		CreatedAt: time.Now(),
	}))

	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	handler := NewMetaHandler(store, NewTemplateLoader(), NewEventPublisher(nc))
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectAssetDelete, DeleteAssetRequest{
		ID:     "asset-delete",
		Source: "operator",
	})
	require.True(t, resp.Success, resp.Error)

	ev := requireMetaEvent(t, assetEvents)
	assert.Equal(t, EventDeleted, ev.EventType)
	assert.Equal(t, EntityAsset, ev.EntityType)
	assert.Equal(t, "asset-delete", ev.EntityID)
	assert.Equal(t, "operator", ev.Source)
	require.NotEmpty(t, ev.Before)
	assert.Empty(t, ev.After)

	var before Asset
	require.NoError(t, json.Unmarshal(ev.Before, &before))
	assert.Equal(t, "delete-me", before.Name)
	assert.Equal(t, "opcua", before.Source)
}

// TestMetaHandler_AssetCreateFailureDoesNotPublishChangedEvent verifies store failures are silent.
func TestMetaHandler_AssetCreateFailureDoesNotPublishChangedEvent(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.CreateAsset(&Asset{
		ID:        "existing",
		Name:      "duplicate",
		CreatedAt: time.Now(),
	}))

	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	handler := NewMetaHandler(store, NewTemplateLoader(), NewEventPublisher(nc))
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectAssetCreate, CreateAssetRequest{Name: "duplicate"})
	assert.False(t, resp.Success)
	requireNoMetaEvent(t, assetEvents)
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

// TestHandleRelationCreateAndDelete_PublishChangedEvents tests relation event payloads.
func TestHandleRelationCreateAndDelete_PublishChangedEvents(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.CreateAsset(&Asset{ID: "asset-001", Name: "sensor-1", CreatedAt: time.Now()}))
	require.NoError(t, store.CreateAsset(&Asset{ID: "asset-002", Name: "equipment-1", CreatedAt: time.Now()}))

	relationEvents := subscribeMetaEvents(t, nc, SubjectRelationChanged)
	handler := NewMetaHandler(store, NewTemplateLoader(), NewEventPublisher(nc))
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectRelationCreate, CreateRelationRequest{
		SourceAssetID: "asset-001",
		TargetAssetID: "asset-002",
		RelationType:  RelationPartOf,
		Metadata:      map[string]string{"installed_date": "2025-01-15"},
		Source:        "aas",
	})
	require.True(t, resp.Success, resp.Error)

	var relation AssetRelation
	require.NoError(t, json.Unmarshal(resp.Data, &relation))

	created := requireMetaEvent(t, relationEvents)
	assert.Equal(t, EventCreated, created.EventType)
	assert.Equal(t, EntityRelation, created.EntityType)
	assert.Equal(t, relation.ID, created.EntityID)
	assert.Equal(t, "aas", created.Source)
	assert.Empty(t, created.Before)
	require.NotEmpty(t, created.After)

	var createdAfter AssetRelation
	require.NoError(t, json.Unmarshal(created.After, &createdAfter))
	assert.Equal(t, relation.ID, createdAfter.ID)
	assert.Equal(t, RelationPartOf, createdAfter.RelationType)

	resp = requestMeta(t, nc, SubjectRelationDelete, DeleteRelationRequest{
		ID:     relation.ID,
		Source: "operator",
	})
	require.True(t, resp.Success, resp.Error)

	deleted := requireMetaEvent(t, relationEvents)
	assert.Equal(t, EventDeleted, deleted.EventType)
	assert.Equal(t, EntityRelation, deleted.EntityType)
	assert.Equal(t, relation.ID, deleted.EntityID)
	assert.Equal(t, "operator", deleted.Source)
	require.NotEmpty(t, deleted.Before)
	assert.Empty(t, deleted.After)

	var deletedBefore AssetRelation
	require.NoError(t, json.Unmarshal(deleted.Before, &deletedBefore))
	assert.Equal(t, relation.ID, deletedBefore.ID)
	assert.Equal(t, "asset-001", deletedBefore.SourceAssetID)
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

// ==================== Reply Function Tests ====================

// TestMarshalResponse_Success tests successful response marshaling
func TestMarshalResponse_Success(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Normal response should marshal successfully
	resp := Response{
		Success: true,
		Data: map[string]string{
			"key": "value",
		},
	}

	data := handler.marshalResponse(resp)
	require.NotNil(t, data)

	// Verify marshaled data is valid JSON
	var result Response
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

// TestMarshalResponse_MarshalError tests fallback when JSON marshal fails
func TestMarshalResponse_MarshalError(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	loader := NewTemplateLoader()
	handler := NewMetaHandler(store, loader)

	// Create response with unmarshalable type (channel)
	unmarshalable := make(chan int)
	resp := Response{
		Success: true,
		Data:    unmarshalable,
	}

	// Call marshalResponse - should return fallback error response
	data := handler.marshalResponse(resp)
	require.NotNil(t, data)

	// Verify fallback response was returned
	var result Response
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)
	assert.False(t, result.Success, "Expected Success=false in fallback response")
	assert.Contains(t, result.Error, "internal error", "Expected error message in fallback response")
}
