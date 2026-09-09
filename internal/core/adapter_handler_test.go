package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAdapterTestHandler wires a registry and handler onto an embedded NATS
// server using the package's existing startTestNATSServer helper.
func newAdapterTestHandler(t *testing.T, probeOnMiss bool) (*nats.Conn, *AdapterRegistry, *AdapterHandler, *fakeClock) {
	t.Helper()

	_, nc, _ := startTestNATSServer(t, false)
	clock := newFakeClock()

	handler := NewAdapterHandler(nil, AdapterHandlerOptions{
		Clock:        clock,
		ProbeTimeout: 300 * time.Millisecond,
	})
	registry := NewAdapterRegistry(AdapterRegistryOptions{
		Clock:       clock,
		Publisher:   NewEventPublisher(nc),
		Prober:      handler,
		ProbeOnMiss: probeOnMiss,
		StaleFloor:  15 * time.Second,
	})
	handler.SetRegistry(registry)

	require.NoError(t, handler.RegisterHandlers(nc))
	t.Cleanup(handler.Stop)
	require.NoError(t, nc.Flush())

	return nc, registry, handler, clock
}

// publishStatus sends a frame the way any NATS client would — deliberately
// without the SDK, because the wire contract must stand on its own.
func publishStatus(t *testing.T, nc *nats.Conn, id string, mutate func(*AdapterStatusFrame)) {
	t.Helper()
	frame := heartbeat(id, "inst-a", 1)
	if mutate != nil {
		mutate(frame)
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	require.NoError(t, nc.Publish(SubjectAdapterStatusPrefix+id, raw))
	require.NoError(t, nc.Flush())
}

func TestHandlerRecordsRawPublish(t *testing.T) {
	nc, registry, _, _ := newAdapterTestHandler(t, false)

	publishStatus(t, nc, "modbus-1", nil)

	require.Eventually(t, func() bool {
		_, ok := registry.Get("modbus-1")
		return ok
	}, 2*time.Second, 10*time.Millisecond, "a plain NATS publish must be enough to register")

	entry, _ := registry.Get("modbus-1")
	assert.Equal(t, AvailabilityOnline, entry.Availability)
	assert.Equal(t, []string{"press-01"}, entry.AssetIDs)
}

func TestHandlerRejectsImpersonation(t *testing.T) {
	nc, registry, _, _ := newAdapterTestHandler(t, false)

	frame := heartbeat("victim", "evil", 1)
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	require.NoError(t, nc.Publish(SubjectAdapterStatusPrefix+"attacker", raw))
	require.NoError(t, nc.Flush())

	time.Sleep(100 * time.Millisecond)
	_, ok := registry.Get("victim")
	assert.False(t, ok, "the body must not be able to claim another adapter's id")
	_, ok = registry.Get("attacker")
	assert.False(t, ok)
}

func TestHandlerRejectsMalformedFrame(t *testing.T) {
	nc, registry, _, _ := newAdapterTestHandler(t, false)

	require.NoError(t, nc.Publish(SubjectAdapterStatusPrefix+"modbus-1", []byte("{not json")))
	require.NoError(t, nc.Flush())

	time.Sleep(100 * time.Millisecond)
	_, ok := registry.Get("modbus-1")
	assert.False(t, ok)
}

func TestHandlerRejectsInvalidRunState(t *testing.T) {
	nc, registry, _, _ := newAdapterTestHandler(t, false)

	publishStatus(t, nc, "modbus-1", func(f *AdapterStatusFrame) { f.RunState = "exploded" })

	time.Sleep(100 * time.Millisecond)
	_, ok := registry.Get("modbus-1")
	assert.False(t, ok)
}

func TestChangeEventsReachSubscribers(t *testing.T) {
	nc, _, _, _ := newAdapterTestHandler(t, false)

	events := make(chan AdapterChangeEvent, 8)
	sub, err := nc.Subscribe(SubjectAdapterChanged, func(msg *nats.Msg) {
		var ev AdapterChangeEvent
		if json.Unmarshal(msg.Data, &ev) == nil {
			events <- ev
		}
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()
	require.NoError(t, nc.Flush())

	publishStatus(t, nc, "modbus-1", nil)

	select {
	case ev := <-events:
		assert.Equal(t, AdapterChangeOnline, ev.Change)
		assert.Equal(t, AdapterSchemaVersion, ev.SchemaVersion)
		require.NotNil(t, ev.Adapter)
		assert.Equal(t, "modbus-1", ev.Adapter.AdapterID)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for adapter change event")
	}
}

func TestListRequestReply(t *testing.T) {
	nc, _, _, _ := newAdapterTestHandler(t, false)

	publishStatus(t, nc, "modbus-1", nil)
	time.Sleep(100 * time.Millisecond)

	msg, err := nc.Request(SubjectAdapterList, []byte("{}"), 2*time.Second)
	require.NoError(t, err)

	var resp struct {
		Success bool            `json:"success"`
		Data    AdapterSnapshot `json:"data"`
	}
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.True(t, resp.Success, "must use the same Response envelope as the metadata plane")
	require.Len(t, resp.Data.Adapters, 1)
	assert.Equal(t, "modbus-1", resp.Data.Adapters[0].AdapterID)
}

// TestHelloTriggersReannounce is the core-restart recovery path: without it the
// registry stays blind until every adapter's next heartbeat, which for a slow
// collector is minutes of looking dead.
func TestHelloTriggersReannounce(t *testing.T) {
	nc, registry, handler, _ := newAdapterTestHandler(t, false)

	// Stand in for an adapter: answer hello with an announce frame.
	sub, err := nc.Subscribe(SubjectAdapterHello, func(_ *nats.Msg) {
		publishStatusNoT(nc, "modbus-1", AdapterPhaseAnnounce)
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()
	require.NoError(t, nc.Flush())

	handler.BroadcastHello()

	require.Eventually(t, func() bool {
		_, ok := registry.Get("modbus-1")
		return ok
	}, 2*time.Second, 10*time.Millisecond, "hello must repopulate the registry in one round trip")
}

// TestProbeOverNATS exercises the real request/reply probe rather than a fake.
func TestProbeOverNATS(t *testing.T) {
	nc, registry, _, clock := newAdapterTestHandler(t, true)

	publishStatus(t, nc, "modbus-1", nil)
	require.Eventually(t, func() bool {
		_, ok := registry.Get("modbus-1")
		return ok
	}, 2*time.Second, 10*time.Millisecond)

	// The adapter answers pings, so an expired deadline must not mark it stale.
	sub, err := nc.Subscribe(SubjectAdapterPingPrefix+"modbus-1", func(msg *nats.Msg) {
		_ = msg.Respond([]byte("{}"))
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	clock.Advance(31 * time.Second)
	registry.Sweep(clock.Now())

	entry, _ := registry.Get("modbus-1")
	assert.Equal(t, AvailabilityOnline, entry.Availability, "a responsive adapter must survive a missed heartbeat")

	// Now the adapter stops answering: the probe fails and the verdict lands.
	require.NoError(t, sub.Unsubscribe())
	require.NoError(t, nc.Flush())

	clock.Advance(31 * time.Second)
	registry.Sweep(clock.Now())

	entry, _ = registry.Get("modbus-1")
	assert.Equal(t, AvailabilityStale, entry.Availability)
}

func TestHandlerStopIsIdempotent(t *testing.T) {
	_, _, handler, _ := newAdapterTestHandler(t, false)
	handler.Stop()
	handler.Stop()
}

func publishStatusNoT(nc *nats.Conn, id, phase string) {
	frame := heartbeat(id, "inst-a", 1)
	frame.Phase = phase
	raw, _ := json.Marshal(frame)
	_ = nc.Publish(SubjectAdapterStatusPrefix+id, raw)
	_ = nc.Flush()
}

// TestStopWithoutRegisterDoesNotHang: main.go defers Stop right after
// construction, so a failed RegisterHandlers must not deadlock shutdown.
func TestStopWithoutRegisterDoesNotHang(t *testing.T) {
	h := NewAdapterHandler(nil, AdapterHandlerOptions{})
	done := make(chan struct{})
	go func() { h.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked when the drop watcher was never started")
	}
}

func TestHandlerWithoutRegistryIsSafe(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	h := NewAdapterHandler(nil, AdapterHandlerOptions{})
	require.NoError(t, h.RegisterHandlers(nc))
	t.Cleanup(h.Stop)

	assert.NotPanics(t, func() {
		publishStatusNoT(nc, "modbus-1", AdapterPhaseHeartbeat)
		time.Sleep(50 * time.Millisecond)
	})
}
