package core

import (
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
