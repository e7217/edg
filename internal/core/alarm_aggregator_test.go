package core

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingAlarmGroupPublisher struct {
	mu     sync.Mutex
	groups []AlarmGroup
	ch     chan AlarmGroup
}

func newRecordingAlarmGroupPublisher() *recordingAlarmGroupPublisher {
	return &recordingAlarmGroupPublisher{
		ch: make(chan AlarmGroup, 8),
	}
}

func (p *recordingAlarmGroupPublisher) PublishAlarmGrouped(group AlarmGroup) {
	p.mu.Lock()
	p.groups = append(p.groups, group)
	p.mu.Unlock()
	p.ch <- group
}

func (p *recordingAlarmGroupPublisher) waitGroup(t *testing.T) AlarmGroup {
	t.Helper()

	select {
	case group := <-p.ch:
		return group
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for alarm group")
		return AlarmGroup{}
	}
}

func TestAlarmAggregator_GroupsSiblingAlarmsByNearestCommonAncestor(t *testing.T) {
	store := newAlarmTestStore(t)
	publisher := newRecordingAlarmGroupPublisher()
	aggregator := NewAlarmAggregator(store, publisher, AlarmAggregatorOptions{
		Window:   20 * time.Millisecond,
		MaxDepth: 10,
	})

	require.NoError(t, aggregator.Add(Alarm{ID: "alarm-1", AssetID: "sensor-001", Severity: SeverityCritical}))
	require.NoError(t, aggregator.Add(Alarm{ID: "alarm-2", AssetID: "sensor-002", Severity: SeverityWarning}))

	group := publisher.waitGroup(t)
	assert.Equal(t, "pump-a", group.GroupAssetID)
	assert.Equal(t, 2, group.AlarmCount)
	assert.ElementsMatch(t, []string{"alarm-1", "alarm-2"}, group.AlarmIDs)
	assert.ElementsMatch(t, []string{"sensor-001", "sensor-002"}, group.AssetIDs)
	assert.Equal(t, SeverityCritical, group.Severity)
	assert.False(t, group.WindowStartedAt.IsZero())
	assert.False(t, group.WindowEndedAt.IsZero())
}

func TestAlarmAggregator_SeparatesAlarmsWithoutCommonAncestor(t *testing.T) {
	store := newAlarmTestStore(t)
	createTraversalAsset(t, store, "isolated-sensor", "Isolated Sensor", "sensor")
	publisher := newRecordingAlarmGroupPublisher()
	aggregator := NewAlarmAggregator(store, publisher, AlarmAggregatorOptions{
		Window:   20 * time.Millisecond,
		MaxDepth: 10,
	})

	require.NoError(t, aggregator.Add(Alarm{ID: "alarm-1", AssetID: "sensor-001", Severity: SeverityWarning}))
	require.NoError(t, aggregator.Add(Alarm{ID: "alarm-2", AssetID: "isolated-sensor", Severity: SeverityInfo}))

	first := publisher.waitGroup(t)
	second := publisher.waitGroup(t)

	groupIDs := []string{first.GroupAssetID, second.GroupAssetID}
	assert.ElementsMatch(t, []string{"sensor-001", "isolated-sensor"}, groupIDs)
	assert.Equal(t, 1, first.AlarmCount)
	assert.Equal(t, 1, second.AlarmCount)
}
