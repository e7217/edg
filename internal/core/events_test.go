package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetaChangeEvent_JSONSerialization(t *testing.T) {
	now := time.Now().UTC()
	ev := MetaChangeEvent{
		SchemaVersion: EventSchemaVersion,
		EventType:     EventUpdated,
		EntityType:    EntityAsset,
		EntityID:      "asset-001",
		Source:        "aas",
		Timestamp:     now,
		Before:        &Asset{ID: "asset-001", Name: "old", Source: SourceManual},
		After:         &Asset{ID: "asset-001", Name: "new", Source: "aas"},
	}

	data, err := json.Marshal(ev)
	require.NoError(t, err)

	var decoded struct {
		SchemaVersion int             `json:"schema_version"`
		EventType     EventType       `json:"event_type"`
		EntityType    EntityType      `json:"entity_type"`
		EntityID      string          `json:"entity_id"`
		Source        string          `json:"source"`
		Timestamp     time.Time       `json:"timestamp"`
		Before        json.RawMessage `json:"before"`
		After         json.RawMessage `json:"after"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, EventSchemaVersion, decoded.SchemaVersion)
	assert.Equal(t, EventUpdated, decoded.EventType)
	assert.Equal(t, EntityAsset, decoded.EntityType)
	assert.Equal(t, "asset-001", decoded.EntityID)
	assert.Equal(t, "aas", decoded.Source)
	assert.Equal(t, now, decoded.Timestamp)
	assert.NotEmpty(t, decoded.Before)
	assert.NotEmpty(t, decoded.After)
}

func TestMetaChangeEvent_OmitsNilBeforeAfter(t *testing.T) {
	ev := MetaChangeEvent{
		SchemaVersion: EventSchemaVersion,
		EventType:     EventCreated,
		EntityType:    EntityRelation,
		EntityID:      "rel-001",
		Source:        SourceManual,
		Timestamp:     time.Now(),
	}

	data, err := json.Marshal(ev)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.NotContains(t, decoded, "before")
	assert.NotContains(t, decoded, "after")
}

func TestEventPublisher_NormalizesAndPublishes(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	received := make(chan *MetaChangeEvent, 1)
	sub, err := nc.Subscribe(SubjectAssetChanged, func(msg *nats.Msg) {
		var ev MetaChangeEvent
		if err := json.Unmarshal(msg.Data, &ev); err == nil {
			received <- &ev
		}
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()
	require.NoError(t, nc.Flush())

	NewEventPublisher(nc).PublishAssetChanged(MetaChangeEvent{
		EventType: EventCreated,
		EntityID:  "asset-001",
		After:     &Asset{ID: "asset-001", Name: "sensor"},
	})

	select {
	case ev := <-received:
		assert.Equal(t, EventSchemaVersion, ev.SchemaVersion)
		assert.Equal(t, EntityAsset, ev.EntityType)
		assert.Equal(t, SourceManual, ev.Source)
		assert.False(t, ev.Timestamp.IsZero())
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for metadata event")
	}
}

func TestEventPublisher_NilSafe(t *testing.T) {
	assert.NotPanics(t, func() {
		NewEventPublisher(nil).PublishAssetChanged(MetaChangeEvent{})
		var publisher *EventPublisher
		publisher.PublishRelationChanged(MetaChangeEvent{})
	})
}
