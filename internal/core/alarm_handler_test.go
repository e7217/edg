package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreTraversal_FindLowestCommonAncestor(t *testing.T) {
	store := newAlarmTestStore(t)

	lca, err := store.FindLowestCommonAncestor(
		[]string{"sensor-001", "sensor-002"},
		[]RelationType{RelationPartOf},
		10,
	)

	require.NoError(t, err)
	require.NotNil(t, lca)
	assert.Equal(t, "pump-a", lca.ID)
	assert.Equal(t, 1, lca.Depth)
}

func TestStoreTraversal_FindLowestCommonAncestorReturnsNilForSeparateTrees(t *testing.T) {
	store := newAlarmTestStore(t)
	createTraversalAsset(t, store, "isolated-sensor", "Isolated Sensor", "sensor")

	lca, err := store.FindLowestCommonAncestor(
		[]string{"sensor-001", "isolated-sensor"},
		[]RelationType{RelationPartOf},
		10,
	)

	require.NoError(t, err)
	assert.Nil(t, lca)
}

func TestAlarmHandler_PublishesImpactAndGroupedEvents(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	store := newAlarmTestStore(t)

	impactCh := subscribeAlarmImpact(t, nc)
	groupCh := subscribeAlarmGroup(t, nc)

	handler := NewAlarmHandler(store, NewEventPublisher(nc), AlarmHandlerOptions{
		Window:            20 * time.Millisecond,
		MaxTraversalDepth: 10,
	})
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	alarm := Alarm{
		ID:        "alarm-1",
		AssetID:   "pump-a",
		Severity:  SeverityCritical,
		Code:      "pump.offline",
		Message:   "Pump A offline",
		Timestamp: time.Now().UTC(),
	}
	data, err := json.Marshal(alarm)
	require.NoError(t, err)
	require.NoError(t, nc.Publish(SubjectAlarmRaised, data))

	impact := requireAlarmImpact(t, impactCh)
	assert.Equal(t, "alarm-1", impact.Alarm.ID)
	assert.Equal(t, "pump-a", impact.Alarm.AssetID)
	assert.ElementsMatch(t, []string{"sensor-001", "sensor-002"}, impact.AffectedAssetIDs)

	group := requireAlarmGroup(t, groupCh)
	assert.Equal(t, "pump-a", group.GroupAssetID)
	assert.Equal(t, 1, group.AlarmCount)
	assert.Equal(t, []string{"alarm-1"}, group.AlarmIDs)
}

func TestAlarmHandler_RejectsMissingAsset(t *testing.T) {
	store := newAlarmTestStore(t)
	handler := NewAlarmHandler(store, nil, AlarmHandlerOptions{})

	err := handler.Process(Alarm{ID: "alarm-missing", AssetID: "missing", Severity: SeverityWarning})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "asset not found")
}

func subscribeAlarmImpact(t *testing.T, nc *nats.Conn) chan AlarmImpact {
	t.Helper()

	ch := make(chan AlarmImpact, 4)
	sub, err := nc.Subscribe(SubjectAlarmImpactComputed, func(msg *nats.Msg) {
		var impact AlarmImpact
		if err := json.Unmarshal(msg.Data, &impact); err == nil {
			ch <- impact
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})
	require.NoError(t, nc.Flush())
	return ch
}

func subscribeAlarmGroup(t *testing.T, nc *nats.Conn) chan AlarmGroup {
	t.Helper()

	ch := make(chan AlarmGroup, 4)
	sub, err := nc.Subscribe(SubjectAlarmGrouped, func(msg *nats.Msg) {
		var group AlarmGroup
		if err := json.Unmarshal(msg.Data, &group); err == nil {
			ch <- group
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})
	require.NoError(t, nc.Flush())
	return ch
}

func requireAlarmImpact(t *testing.T, ch <-chan AlarmImpact) AlarmImpact {
	t.Helper()

	select {
	case impact := <-ch:
		return impact
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for alarm impact")
		return AlarmImpact{}
	}
}

func requireAlarmGroup(t *testing.T, ch <-chan AlarmGroup) AlarmGroup {
	t.Helper()

	select {
	case group := <-ch:
		return group
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for alarm group")
		return AlarmGroup{}
	}
}

func newAlarmTestStore(t *testing.T) *Store {
	t.Helper()

	store := newTraversalTestStore(t)
	createTraversalAsset(t, store, "sensor-002", "Sensor 002", "sensor")
	createTraversalRelation(t, store, "rel-sensor-002-pump", "sensor-002", "pump-a", RelationPartOf)
	return store
}
