package core

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock makes every expiry assertion deterministic. The repository has no
// other clock abstraction; the reaper is the first thing that genuinely cannot
// be tested without one, and every test in this file would otherwise need a
// real sleep.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recordingPublisher collects change events for assertions.
type recordingPublisher struct {
	mu     sync.Mutex
	events []AdapterChangeEvent
}

func (p *recordingPublisher) PublishAdapterChanged(ev AdapterChangeEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
}

func (p *recordingPublisher) drain() []AdapterChangeEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.events
	p.events = nil
	return out
}

func (p *recordingPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

func newTestRegistry(t *testing.T) (*AdapterRegistry, *fakeClock, *recordingPublisher) {
	t.Helper()
	clock := newFakeClock()
	pub := &recordingPublisher{}
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock:      clock,
		Publisher:  pub,
		StaleFloor: 15 * time.Second,
	})
	return reg, clock, pub
}

func heartbeat(id, instance string, seq int64) *AdapterStatusFrame {
	return &AdapterStatusFrame{
		SchemaVersion:      AdapterSchemaVersion,
		AdapterID:          id,
		InstanceID:         instance,
		Seq:                seq,
		Phase:              AdapterPhaseHeartbeat,
		RunState:           RunStateRunning,
		DeviceState:        "connected",
		HeartbeatIntervalS: 10,
		Assets:             []AdapterAssetStatus{{AssetID: "press-01", DeviceState: "connected"}},
	}
}

func TestObserveFirstFrameGoesOnline(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)

	require.True(t, reg.Observe(heartbeat("modbus-1", "inst-a", 1), "modbus-1", clock.Now()))

	entry, ok := reg.Get("modbus-1")
	require.True(t, ok)
	assert.Equal(t, AvailabilityOnline, entry.Availability)
	assert.Equal(t, RunStateRunning, entry.RunState)
	assert.Equal(t, clock.Now().Add(30*time.Second), entry.DeadlineAt, "deadline is 3x the announced interval")

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeOnline, events[0].Change)
	assert.Equal(t, "first_seen", events[0].Reason)
	assert.Equal(t, AdapterSchemaVersion, events[0].SchemaVersion)
}

// TestRepeatedHeartbeatIsSilent is the noise-suppression contract: at steady
// state a fleet heartbeating at any rate produces no events at all.
func TestRepeatedHeartbeatIsSilent(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)

	reg.Observe(heartbeat("modbus-1", "inst-a", 1), "modbus-1", clock.Now())
	pub.drain()

	for i := int64(2); i < 10; i++ {
		clock.Advance(10 * time.Second)
		f := heartbeat("modbus-1", "inst-a", i)
		f.Counters.PublishedTotal = i * 100 // counters move, nothing watched does
		reg.Observe(f, "modbus-1", clock.Now())
	}

	assert.Zero(t, pub.count(), "counter-only heartbeats must not emit events")
}

func TestDeviceStateChangeEmitsUpdated(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "inst-a", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(10 * time.Second)
	f := heartbeat("modbus-1", "inst-a", 2)
	f.DeviceState = "reconnecting"
	reg.Observe(f, "modbus-1", clock.Now())

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeUpdated, events[0].Change)
	require.NotNil(t, events[0].Previous)
	assert.Equal(t, "connected", events[0].Previous.DeviceState)
	assert.Equal(t, "reconnecting", events[0].Adapter.DeviceState)
}

// TestDegradedWhileDeviceConnected is the blind spot Neuron's two-axis model
// has: a PLC that answers but returns garbage leaves the link connected while
// the adapter is failing.
func TestDegradedWhileDeviceConnected(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "inst-a", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(10 * time.Second)
	f := heartbeat("modbus-1", "inst-a", 2)
	f.RunState = RunStateDegraded
	f.Counters.ConsecutiveCollectErrors = 3
	reg.Observe(f, "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, RunStateDegraded, entry.RunState)
	assert.Equal(t, "connected", entry.DeviceState, "the device link is genuinely still up")
	assert.Equal(t, AvailabilityOnline, entry.Availability, "and we can still hear it")
	assert.Len(t, pub.drain(), 1)
}

func TestInstanceChangeIsReplaced(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "inst-a", 5), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(10 * time.Second)
	reg.Observe(heartbeat("modbus-1", "inst-b", 1), "modbus-1", clock.Now())

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeReplaced, events[0].Change)
	assert.Equal(t, "instance_changed", events[0].Reason)
}

func TestGracefulOfflinePhase(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "inst-a", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(5 * time.Second)
	f := heartbeat("modbus-1", "inst-a", 2)
	f.Phase = AdapterPhaseOffline
	f.RunState = RunStateStopped
	reg.Observe(f, "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityOffline, entry.Availability)
	require.NotNil(t, entry.StaleSince)

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeOffline, events[0].Change)
	assert.Equal(t, "graceful", events[0].Reason, "a clean shutdown must be distinguishable from silence")
}

// TestSubjectTokenIsAuthoritative closes the impersonation path: the subject
// the frame arrived on wins over whatever the body claims.
func TestSubjectTokenIsAuthoritative(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)

	f := heartbeat("victim", "inst-evil", 1)
	assert.False(t, reg.Observe(f, "attacker", clock.Now()),
		"a frame claiming another adapter_id must be rejected")

	_, ok := reg.Get("victim")
	assert.False(t, ok)
	_, ok = reg.Get("attacker")
	assert.False(t, ok)
	assert.Zero(t, pub.count())
}

func TestEmptyBodyIDAdoptsSubjectToken(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("", "inst-a", 1)
	require.True(t, reg.Observe(f, "modbus-1", clock.Now()))

	entry, ok := reg.Get("modbus-1")
	require.True(t, ok)
	assert.Equal(t, "modbus-1", entry.AdapterID)
}

func TestInvalidSubjectTokenRejected(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	for _, bad := range []string{"", "has.dot", "drift", "list", "hello"} {
		assert.False(t, reg.Observe(heartbeat(bad, "i", 1), bad, clock.Now()), "id %q", bad)
	}
}

// TestByAssetIsASet catches the misdeployment where two adapters poll the same
// equipment — double writes and a confusing history.
func TestByAssetIsASet(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	reg.Observe(heartbeat("modbus-2", "i2", 1), "modbus-2", clock.Now())

	assert.Equal(t, []string{"modbus-1", "modbus-2"}, reg.AdaptersForAsset("press-01"),
		"both adapters claim the same asset and both must be visible")
}

func TestAssetReindexOnChange(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())

	clock.Advance(10 * time.Second)
	f := heartbeat("modbus-1", "i1", 2)
	f.Assets = []AdapterAssetStatus{{AssetID: "press-02"}}
	reg.Observe(f, "modbus-1", clock.Now())

	assert.Empty(t, reg.AdaptersForAsset("press-01"), "the old asset must be unlinked")
	assert.Equal(t, []string{"modbus-1"}, reg.AdaptersForAsset("press-02"))
}

// TestObservedPublishHzUsesReceiveTime is what answers "is the gateway falling
// behind", and it must stay correct when the adapter's clock is wrong.
func TestObservedPublishHzUsesReceiveTime(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f1 := heartbeat("modbus-1", "i1", 1)
	f1.Counters.PublishedTotal = 100
	f1.SentAt = clock.Now().Add(-time.Hour) // adapter clock an hour behind
	reg.Observe(f1, "modbus-1", clock.Now())

	clock.Advance(10 * time.Second)
	f2 := heartbeat("modbus-1", "i1", 2)
	f2.Counters.PublishedTotal = 200
	f2.SentAt = clock.Now().Add(-time.Hour)
	reg.Observe(f2, "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.InDelta(t, 10.0, entry.ObservedPublishHz, 0.001, "100 publishes over 10 received seconds")
	assert.InDelta(t, -3600.0, entry.ClockSkewS, 1.0, "skew is recorded but never used for a decision")
}

func TestRateResetsOnRestart(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f1 := heartbeat("modbus-1", "i1", 500)
	f1.Counters.PublishedTotal = 50000
	reg.Observe(f1, "modbus-1", clock.Now())

	clock.Advance(10 * time.Second)
	f2 := heartbeat("modbus-1", "i2", 1) // new instance, counters back to ~0
	f2.Counters.PublishedTotal = 5
	reg.Observe(f2, "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Zero(t, entry.ObservedPublishHz, "a restart must reset the rate, never go negative")
}

func TestRateResetsOnSeqRegression(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f1 := heartbeat("modbus-1", "i1", 500)
	f1.Counters.PublishedTotal = 50000
	reg.Observe(f1, "modbus-1", clock.Now())

	clock.Advance(10 * time.Second)
	f2 := heartbeat("modbus-1", "i1", 3) // seq went backwards
	f2.Counters.PublishedTotal = 30
	reg.Observe(f2, "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Zero(t, entry.ObservedPublishHz)
}

// TestDeadlineHonoursSlowAdapters: a 15-minute batch collector must not be
// declared dead just because it is quiet.
func TestDeadlineHonoursSlowAdapters(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("opcua-batch", "i1", 1)
	f.HeartbeatIntervalS = 60
	reg.Observe(f, "opcua-batch", clock.Now())

	entry, _ := reg.Get("opcua-batch")
	assert.Equal(t, clock.Now().Add(180*time.Second), entry.DeadlineAt)
}

func TestDeadlineFloorForFastAdapters(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("fast", "i1", 1)
	f.HeartbeatIntervalS = 1 // 3x would be 3s, below the 15s floor
	reg.Observe(f, "fast", clock.Now())

	entry, _ := reg.Get("fast")
	assert.Equal(t, clock.Now().Add(15*time.Second), entry.DeadlineAt)
}

func TestIntervalClamped(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("slow", "i1", 1)
	f.HeartbeatIntervalS = 99999
	reg.Observe(f, "slow", clock.Now())

	entry, _ := reg.Get("slow")
	assert.Equal(t, int(DefaultAdapterMaxInterval/time.Second), entry.HeartbeatIntervalS)
}

func TestSnapshotIsSortedAndWarming(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("zebra", "i1", 1), "zebra", clock.Now())
	reg.Observe(heartbeat("alpha", "i2", 1), "alpha", clock.Now())

	snap := reg.Snapshot()
	require.Len(t, snap.Adapters, 2)
	assert.Equal(t, "alpha", snap.Adapters[0].AdapterID)
	assert.True(t, snap.Warming, "right after start an empty-ish registry is not proof of death")

	clock.Advance(2*DefaultAdapterMaxInterval + time.Second)
	assert.False(t, reg.Snapshot().Warming)
}

func TestSnapshotReturnsCopies(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())

	snap := reg.Snapshot()
	snap.Adapters[0].RunState = RunStateStopped

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, RunStateRunning, entry.RunState, "callers must not be able to mutate registry state")
}

func TestCountBy(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("a", "i1", 1), "a", clock.Now())
	reg.Observe(heartbeat("b", "i2", 1), "b", clock.Now())

	f := heartbeat("c", "i3", 1)
	f.Phase = AdapterPhaseOffline
	reg.Observe(f, "c", clock.Now())

	counts := reg.CountBy()
	assert.Equal(t, 2, counts[AvailabilityOnline])
	assert.Equal(t, 1, counts[AvailabilityOffline])
}

func TestNilPublisherIsSafe(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{Clock: newFakeClock()})
	assert.NotPanics(t, func() {
		reg.Observe(heartbeat("a", "i1", 1), "a", time.Now())
	})
}

func TestConcurrentObserveIsRaceFree(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := int64(1); j <= 20; j++ {
				reg.Observe(heartbeat("modbus-1", "i1", j), "modbus-1", clock.Now())
				reg.Snapshot()
				reg.AdaptersForAsset("press-01")
			}
		}(i)
	}
	wg.Wait()

	_, ok := reg.Get("modbus-1")
	assert.True(t, ok)
}

// fakeProber answers probes from a fixed script.
type fakeProber struct {
	mu       sync.Mutex
	alive    map[string]bool
	calls    []string
	maxSeen  int
	inFlight int
}

func newFakeProber(alive map[string]bool) *fakeProber {
	return &fakeProber{alive: alive}
}

func (p *fakeProber) Probe(id string) bool {
	p.mu.Lock()
	p.calls = append(p.calls, id)
	p.inFlight++
	if p.inFlight > p.maxSeen {
		p.maxSeen = p.inFlight
	}
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive[id]
}

func (p *fakeProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func newProbingRegistry(t *testing.T, prober Prober) (*AdapterRegistry, *fakeClock, *recordingPublisher) {
	t.Helper()
	clock := newFakeClock()
	pub := &recordingPublisher{}
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock:       clock,
		Publisher:   pub,
		Prober:      prober,
		ProbeOnMiss: true,
		StaleFloor:  15 * time.Second,
		MaxProbes:   4,
	})
	return reg, clock, pub
}

func TestSweepBeforeDeadlineKeepsOnline(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(29 * time.Second) // deadline is 30s
	reg.Sweep(clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityOnline, entry.Availability)
	assert.Zero(t, pub.count())
}

func TestSweepAfterDeadlineGoesStale(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityStale, entry.Availability)
	require.NotNil(t, entry.StaleSince)

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeStale, events[0].Change)
	assert.Equal(t, "deadline_exceeded", events[0].Reason)
}

// TestProbeRecoversMissedHeartbeat is why the probe exists: a dropped
// heartbeat is not proof of death, and reporting one as such trains operators
// to ignore the signal.
func TestProbeRecoversMissedHeartbeat(t *testing.T) {
	prober := newFakeProber(map[string]bool{"modbus-1": true})
	reg, clock, pub := newProbingRegistry(t, prober)

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityOnline, entry.Availability, "the probe answered, so it is alive")
	assert.Equal(t, 1, prober.callCount())
	assert.Zero(t, pub.count(), "a recovered probe must be silent, not a flapping event pair")
	assert.Equal(t, clock.Now().Add(30*time.Second), entry.DeadlineAt, "deadline extended")
}

func TestProbeFailureConfirmsStale(t *testing.T) {
	prober := newFakeProber(map[string]bool{"modbus-1": false})
	reg, clock, pub := newProbingRegistry(t, prober)

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityStale, entry.Availability)

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, "probe_failed", events[0].Reason, "a confirmed death must be distinguishable from a guess")
}

func TestStaleAdapterRecoversOnNextHeartbeat(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())
	pub.drain()

	clock.Advance(time.Second)
	reg.Observe(heartbeat("modbus-1", "i1", 2), "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityOnline, entry.Availability)

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeOnline, events[0].Change)
	assert.Equal(t, "recovered", events[0].Reason)
}

func TestForgetAfterRemovesEntry(t *testing.T) {
	clock := newFakeClock()
	pub := &recordingPublisher{}
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock: clock, Publisher: pub, StaleFloor: 15 * time.Second,
		ForgetAfter: time.Hour,
	})

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())
	pub.drain()

	clock.Advance(time.Hour + time.Second)
	reg.Sweep(clock.Now())

	_, ok := reg.Get("modbus-1")
	assert.False(t, ok)
	assert.Empty(t, reg.AdaptersForAsset("press-01"), "the asset index must be cleaned up too")

	events := pub.drain()
	require.Len(t, events, 1)
	assert.Equal(t, AdapterChangeForgotten, events[0].Change)
}

// TestProbeConcurrencyIsBounded: a network partition expires the whole fleet
// at once, and an unbounded probe fan-out would be a self-inflicted storm.
func TestProbeConcurrencyIsBounded(t *testing.T) {
	alive := map[string]bool{}
	prober := newFakeProber(alive)
	reg, clock, _ := newProbingRegistry(t, prober) // MaxProbes: 4

	for i := 0; i < 50; i++ {
		id := "adapter-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		reg.Observe(heartbeat(id, "i1", 1), id, clock.Now())
	}

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())

	prober.mu.Lock()
	maxSeen := prober.maxSeen
	prober.mu.Unlock()
	assert.LessOrEqual(t, maxSeen, 4, "concurrent probes must respect MaxProbes")

	counts := reg.CountBy()
	assert.Equal(t, 50, counts[AvailabilityStale], "every expired adapter still gets a verdict")
}

func TestSweepIsIdempotent(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())
	require.Len(t, pub.drain(), 1)

	reg.Sweep(clock.Now())
	assert.Zero(t, pub.count(), "an already-stale adapter must not re-emit")
}

func TestOfflineAdapterIsNotProbed(t *testing.T) {
	prober := newFakeProber(map[string]bool{})
	reg, clock, _ := newProbingRegistry(t, prober)

	f := heartbeat("modbus-1", "i1", 1)
	f.Phase = AdapterPhaseOffline
	reg.Observe(f, "modbus-1", clock.Now())

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())

	assert.Zero(t, prober.callCount(), "a cleanly stopped adapter is known dead; probing it is noise")
}

func TestStartStopReaper(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{Clock: newFakeClock()})
	reg.Start()
	reg.Stop()
	reg.Stop() // must be idempotent
}

// TestScanIntervalTracksStaleFloor pins the detection-latency bound. Deriving
// the scan interval from MaxInterval instead would mean a 2s deadline took up
// to 150s to notice, which a live smoke test demonstrated.
func TestScanIntervalTracksStaleFloor(t *testing.T) {
	tests := []struct {
		name  string
		floor time.Duration
		want  time.Duration
	}{
		{"tight floor clamps to one second", 2 * time.Second, time.Second},
		{"default floor halves", 15 * time.Second, 7500 * time.Millisecond},
		{"loose floor caps at thirty seconds", 5 * time.Minute, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewAdapterRegistry(AdapterRegistryOptions{
				Clock: newFakeClock(), StaleFloor: tt.floor,
			})
			assert.Equal(t, tt.want, reg.scanInterval())
		})
	}
}

// TestScanIntervalIndependentOfMaxInterval: a fleet of slow collectors must not
// slow down detection for a fast one.
func TestScanIntervalIndependentOfMaxInterval(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock: newFakeClock(), StaleFloor: 2 * time.Second, MaxInterval: time.Hour,
	})
	assert.Equal(t, time.Second, reg.scanInterval())
}

func TestDriftDetectsMultiAdapter(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	reg.Observe(heartbeat("modbus-2", "i2", 1), "modbus-2", clock.Now())

	report := reg.Drift()
	require.Equal(t, 1, report.IssueCount)
	assert.Equal(t, DriftMultiAdapter, report.Issues[0].Kind)
	assert.Equal(t, "press-01", report.Issues[0].Subject)
	assert.Equal(t, []string{"modbus-1", "modbus-2"}, report.Issues[0].Adapters)
}

func TestDriftDetectsClockSkew(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("modbus-1", "i1", 1)
	f.SentAt = clock.Now().Add(2 * time.Hour)
	reg.Observe(f, "modbus-1", clock.Now())

	report := reg.Drift()
	require.Equal(t, 1, report.IssueCount)
	assert.Equal(t, DriftClockSkew, report.Issues[0].Kind)
	assert.Equal(t, "modbus-1", report.Issues[0].Subject)
}

func TestDriftIgnoresSmallSkew(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("modbus-1", "i1", 1)
	f.SentAt = clock.Now().Add(5 * time.Second)
	reg.Observe(f, "modbus-1", clock.Now())

	assert.Zero(t, reg.Drift().IssueCount, "ordinary NTP jitter must not be reported")
}

// TestDriftDoesNotReportUnservedAssets documents a deliberate omission: the
// registry cannot distinguish a sensor that lost its collector from a line or
// factory node that was never meant to have one, and flagging every logical
// grouping asset would bury the real signals.
func TestDriftDoesNotReportUnservedAssets(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())

	report := reg.Drift()
	for _, issue := range report.Issues {
		assert.NotEqual(t, "asset_unserved", issue.Kind)
	}
}

func TestDriftIsEmptyForHealthyFleet(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())

	report := reg.Drift()
	assert.Zero(t, report.IssueCount)
	assert.NotNil(t, report.Issues, "must serialize as [] not null")
}

// blockingProber holds a probe open so a test can deterministically inject a
// heartbeat into the window where the registry has released its lock.
type blockingProber struct {
	entered chan string
	release chan bool
}

func newBlockingProber() *blockingProber {
	return &blockingProber{entered: make(chan string, 4), release: make(chan bool, 4)}
}

func (p *blockingProber) Probe(id string) bool {
	p.entered <- id
	return <-p.release
}

// TestHeartbeatDuringProbeIsNotOverwritten pins a race an adversarial review
// found: the stale verdict is computed before a probe that can take seconds,
// and a heartbeat arriving in that window legitimately revives the adapter.
// Marking it stale anyway produced a false event and an entry whose LastSeenAt
// was newer than its StaleSince.
func TestHeartbeatDuringProbeIsNotOverwritten(t *testing.T) {
	clock := newFakeClock()
	pub := &recordingPublisher{}
	prober := newBlockingProber()
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock: clock, Publisher: pub, Prober: prober,
		ProbeOnMiss: true, StaleFloor: 15 * time.Second,
	})

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	clock.Advance(31 * time.Second)
	done := make(chan struct{})
	go func() { defer close(done); reg.Sweep(clock.Now()) }()

	<-prober.entered // the registry lock is released here

	// The adapter is alive after all and its heartbeat lands mid-probe.
	clock.Advance(time.Second)
	reg.Observe(heartbeat("modbus-1", "i1", 2), "modbus-1", clock.Now())

	prober.release <- false // the probe still fails (a lost reply)
	<-done

	entry, ok := reg.Get("modbus-1")
	require.True(t, ok)
	assert.Equal(t, AvailabilityOnline, entry.Availability,
		"a heartbeat received during the probe must win over the stale verdict computed before it")
	assert.Nil(t, entry.StaleSince)

	for _, ev := range pub.drain() {
		assert.NotEqual(t, AdapterChangeStale, ev.Change, "no false stale event")
	}
}

// TestRestartDuringProbeIsNotForgotten is the same window applied to the forget
// path, which had no revalidation at all: an adapter that restarts while a
// probe is in flight was deleted outright, dropping a healthy adapter from the
// API and re-emitting first_seen on its next frame.
func TestRestartDuringProbeIsNotForgotten(t *testing.T) {
	clock := newFakeClock()
	pub := &recordingPublisher{}
	prober := newBlockingProber()
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock: clock, Publisher: pub, Prober: prober,
		ProbeOnMiss: true, StaleFloor: 15 * time.Second, ForgetAfter: time.Hour,
	})

	// "old" said goodbye long ago and is due to be forgotten; "other" is
	// merely expired, which is what opens the probe window.
	stopped := heartbeat("old", "i1", 1)
	stopped.Phase = AdapterPhaseOffline
	reg.Observe(stopped, "old", clock.Now())
	reg.Observe(heartbeat("other", "i2", 1), "other", clock.Now())
	pub.drain()

	clock.Advance(time.Hour + time.Minute)
	done := make(chan struct{})
	go func() { defer close(done); reg.Sweep(clock.Now()) }()

	<-prober.entered

	// "old" comes back with a fresh instance while the probe is in flight.
	reg.Observe(heartbeat("old", "i2", 1), "old", clock.Now())

	prober.release <- false
	<-done

	entry, ok := reg.Get("old")
	require.True(t, ok, "a restarted adapter must not be deleted by a forget decision made before it came back")
	assert.Equal(t, AvailabilityOnline, entry.Availability)
	assert.Contains(t, reg.AdaptersForAsset("press-01"), "old",
		"the asset index must still point at the restarted adapter")
}

// TestChangeEventsDoNotShareRegistryPointers: events are published outside the
// lock while markStale mutates entries in place, so handing out the stored
// pointer is a data race. Run under -race.
func TestChangeEventsDoNotShareRegistryPointers(t *testing.T) {
	clock := newFakeClock()
	seen := make(chan *AdapterEntry, 16)
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock:      clock,
		Publisher:  publisherFunc(func(ev AdapterChangeEvent) { seen <- ev.Adapter }),
		StaleFloor: 15 * time.Second,
	})

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	published := <-seen

	clock.Advance(31 * time.Second)
	reg.Sweep(clock.Now())

	assert.Equal(t, AvailabilityOnline, published.Availability,
		"the entry handed to a subscriber must not be mutated by a later transition")

	stored, _ := reg.Get("modbus-1")
	assert.Equal(t, AvailabilityStale, stored.Availability)
}

type publisherFunc func(AdapterChangeEvent)

func (f publisherFunc) PublishAdapterChanged(ev AdapterChangeEvent) { f(ev) }

// TestMissingIntervalGetsTightestDeadline: an adapter announcing nothing (or
// rounding a sub-second interval down to zero) must get the tightest deadline,
// not the loosest. Mapping it to MaxInterval gave a misconfigured adapter a
// 15-minute grace period — failing open on the one question this plane answers.
func TestMissingIntervalGetsTightestDeadline(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	f := heartbeat("modbus-1", "i1", 1)
	f.HeartbeatIntervalS = 0
	reg.Observe(f, "modbus-1", clock.Now())

	entry, _ := reg.Get("modbus-1")
	assert.Equal(t, clock.Now().Add(15*time.Second), entry.DeadlineAt,
		"a zero interval must fall back to the floor, not to the maximum")
}

// TestReaperLoopActuallyExpires closes a gap an adversarial review found: every
// expiry test drove Sweep directly, so gutting reapLoop entirely left the whole
// suite green. Nothing exercised the ticker that makes expiry happen in
// production.
func TestReaperLoopActuallyExpires(t *testing.T) {
	clock := newFakeClock()
	pub := &recordingPublisher{}
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock: clock, Publisher: pub,
		// A 2s floor gives a 1s scan interval, so the loop ticks promptly
		// without the test depending on wall-clock precision.
		StaleFloor: 2 * time.Second,
	})

	reg.Observe(heartbeat("modbus-1", "i1", 1), "modbus-1", clock.Now())
	pub.drain()

	reg.Start()
	t.Cleanup(reg.Stop)

	// Advance the injected clock past the deadline and let the real ticker fire.
	clock.Advance(time.Hour)

	require.Eventually(t, func() bool {
		entry, ok := reg.Get("modbus-1")
		return ok && entry.Availability == AvailabilityStale
	}, 5*time.Second, 20*time.Millisecond,
		"the reaper goroutine must expire adapters without anyone calling Sweep")
}

// gatedProber blocks every probe until the test releases it, so in-flight
// concurrency is observable rather than a scheduling accident. The previous
// version of this test used a prober that returned immediately, so probes
// almost never overlapped and the assertion passed even with the semaphore
// widened to a million.
type gatedProber struct {
	mu       sync.Mutex
	inFlight int
	maxSeen  int
	calls    int
	release  chan struct{}
}

func newGatedProber() *gatedProber {
	return &gatedProber{release: make(chan struct{})}
}

func (p *gatedProber) Probe(string) bool {
	p.mu.Lock()
	p.inFlight++
	p.calls++
	if p.inFlight > p.maxSeen {
		p.maxSeen = p.inFlight
	}
	p.mu.Unlock()

	<-p.release

	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
	return false
}

func (p *gatedProber) snapshot() (maxSeen, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxSeen, p.calls
}

// TestProbeConcurrencyIsActuallyBounded replaces an assertion that passed even
// with MaxProbes raised to a million, because the prober never blocked and so
// probes never actually overlapped.
func TestProbeConcurrencyIsActuallyBounded(t *testing.T) {
	const adapters = 40
	const limit = 4

	clock := newFakeClock()
	prober := newGatedProber()
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock: clock, Publisher: &recordingPublisher{}, Prober: prober,
		ProbeOnMiss: true, StaleFloor: 15 * time.Second, MaxProbes: limit,
	})

	for i := 0; i < adapters; i++ {
		id := fmt.Sprintf("adapter-%02d", i)
		reg.Observe(heartbeat(id, "i1", 1), id, clock.Now())
	}

	clock.Advance(31 * time.Second)
	done := make(chan struct{})
	go func() { defer close(done); reg.Sweep(clock.Now()) }()

	// Wait until the semaphore is saturated, then confirm it never exceeds the
	// limit even though every probe is blocked and all 40 goroutines exist.
	require.Eventually(t, func() bool {
		maxSeen, _ := prober.snapshot()
		return maxSeen >= limit
	}, 3*time.Second, 5*time.Millisecond, "probes should saturate the budget")

	time.Sleep(50 * time.Millisecond) // give any unbounded excess a chance to appear
	maxSeen, _ := prober.snapshot()
	assert.LessOrEqual(t, maxSeen, limit,
		"concurrent probes must respect MaxProbes; %d adapters expired at once", adapters)

	close(prober.release)
	<-done

	assert.Equal(t, adapters, reg.CountBy()[AvailabilityStale],
		"every expired adapter still gets a verdict, probed or shed by the budget")
}

// TestDeviceCountsAndTruncationSurvive: a gateway fronting hundreds of assets
// reports a rollup instead of the full list, and the rollup changing is the
// only signal that half of them died.
func TestDeviceCountsAndTruncationSurvive(t *testing.T) {
	reg, clock, pub := newTestRegistry(t)

	f := heartbeat("gateway-1", "i1", 1)
	f.Assets = nil
	f.AssetsTruncated = true
	f.DeviceCounts = map[string]int{"connected": 200}
	reg.Observe(f, "gateway-1", clock.Now())
	pub.drain()

	entry, _ := reg.Get("gateway-1")
	assert.Equal(t, map[string]int{"connected": 200}, entry.DeviceCounts)

	clock.Advance(10 * time.Second)
	f2 := heartbeat("gateway-1", "i1", 2)
	f2.Assets = nil
	f2.AssetsTruncated = true
	f2.DeviceCounts = map[string]int{"connected": 100, "error": 100}
	reg.Observe(f2, "gateway-1", clock.Now())

	entry, _ = reg.Get("gateway-1")
	assert.Equal(t, 100, entry.DeviceCounts["error"],
		"half the fleet going down must be visible in the rollup")
}
