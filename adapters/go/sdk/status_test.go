package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// collectStatus subscribes to every adapter status frame and returns a channel
// of decoded frames.
func collectStatus(t *testing.T, nc *nats.Conn) <-chan AdapterStatusFrame {
	t.Helper()

	frames := make(chan AdapterStatusFrame, 64)
	sub, err := nc.Subscribe(SubjectAdapterStatusPrefix+">", func(msg *nats.Msg) {
		var f AdapterStatusFrame
		if json.Unmarshal(msg.Data, &f) == nil {
			select {
			case frames <- f:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return frames
}

func waitForFrame(t *testing.T, frames <-chan AdapterStatusFrame, match func(AdapterStatusFrame) bool, what string) AdapterStatusFrame {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f := <-frames:
			if match(f) {
				return f
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		}
	}
}

// TestCollectorOnlyAdapterReportsStatus is the backward-compatibility contract:
// an adapter written before this feature, implementing nothing but Collect,
// must start reporting without a single line of change.
func TestCollectorOnlyAdapterReportsStatus(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	a := NewAdapter(AdapterConfig{
		AssetID:         "sensor-1",
		NATSURL:         url,
		CollectInterval: 20 * time.Millisecond,
	}, &fakeCollector{})
	_ = a.Run(ctx)

	f := waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.Phase == AdapterPhaseOnline
	}, "the online frame")

	if f.AdapterID != "sensor-1" {
		t.Errorf("adapter_id must fall back to asset_id, got %q", f.AdapterID)
	}
	if f.SchemaVersion != AdapterSchemaVersion {
		t.Errorf("schema_version = %d, want %d", f.SchemaVersion, AdapterSchemaVersion)
	}
	if f.InstanceID == "" {
		t.Error("instance_id must be set so core can detect restarts")
	}
	if f.HeartbeatIntervalS != int(DefaultHeartbeatInterval/time.Second) {
		t.Errorf("heartbeat_interval_s = %d, want the default", f.HeartbeatIntervalS)
	}
	if f.SDK != SDKVersion {
		t.Errorf("sdk = %q, want %q", f.SDK, SDKVersion)
	}
}

func TestDisableStatusReporting(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	a := NewAdapter(AdapterConfig{
		AssetID:                "sensor-1",
		NATSURL:                url,
		CollectInterval:        20 * time.Millisecond,
		DisableStatusReporting: true,
	}, &fakeCollector{})
	_ = a.Run(ctx)

	select {
	case f := <-frames:
		t.Fatalf("expected no status frames, got %+v", f)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestDeviceStateChangeIsEdgeTriggered: a transition must be reported at once,
// not up to a whole heartbeat interval later.
func TestDeviceStateChangeIsEdgeTriggered(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	collector := &deviceCollector{connectErrs: []error{ErrDeviceConnection}}
	a := NewAdapter(AdapterConfig{
		AssetID:           "sensor-1",
		NATSURL:           url,
		CollectInterval:   20 * time.Millisecond,
		HeartbeatInterval: time.Hour, // only an edge can produce a frame
		Backoff:           Backoff{Base: time.Millisecond, Max: 2 * time.Millisecond},
	}, collector)
	_ = a.Run(ctx)

	waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.DeviceState == string(DeviceConnected)
	}, "a connected frame without waiting a full heartbeat interval")
}

// TestDegradedWhileConnected pins the third axis. The device link is fine;
// the adapter is not. Neuron's two-axis model cannot express this.
func TestDegradedWhileConnected(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// No DeviceLifecycle, so the device is considered connected immediately;
	// Collect fails repeatedly with a non-device error.
	a := NewAdapter(AdapterConfig{
		AssetID:           "sensor-1",
		NATSURL:           url,
		CollectInterval:   10 * time.Millisecond,
		HeartbeatInterval: time.Hour,
	}, &alwaysFailingCollector{err: errors.New("garbage register value")})
	_ = a.Run(ctx)

	f := waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.RunState == RunStateDegraded
	}, "a degraded frame")

	if f.DeviceState != string(DeviceConnected) {
		t.Errorf("device_state = %q, want connected: the link is genuinely up", f.DeviceState)
	}
	if f.Counters.ConsecutiveCollectErrors < degradedAfterErrors {
		t.Errorf("consecutive_collect_errors = %d, want >= %d",
			f.Counters.ConsecutiveCollectErrors, degradedAfterErrors)
	}
}

// TestOfflineFrameSentBeforeDisconnect verifies the defer ordering: the
// goodbye must go out while the NATS connection is still open, which relies on
// the reporter's defer being registered after the client-close defer.
func TestOfflineFrameSentBeforeDisconnect(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	a := NewAdapter(AdapterConfig{
		AssetID:         "sensor-1",
		NATSURL:         url,
		CollectInterval: 20 * time.Millisecond,
	}, &fakeCollector{})
	_ = a.Run(ctx)

	f := waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.Phase == AdapterPhaseOffline
	}, "the offline frame")

	if f.RunState != RunStateStopped {
		t.Errorf("run_state = %q, want stopped", f.RunState)
	}
}

// TestRespondsToHello proves core can repopulate its registry after a restart
// without waiting for the next heartbeat.
func TestRespondsToHello(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a := NewAdapter(AdapterConfig{
			AssetID:           "sensor-1",
			NATSURL:           url,
			CollectInterval:   20 * time.Millisecond,
			HeartbeatInterval: time.Hour,
		}, &fakeCollector{})
		_ = a.Run(ctx)
	}()

	waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.Phase == AdapterPhaseOnline
	}, "the initial online frame")

	if err := control.Publish(SubjectAdapterHello, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	_ = control.Flush()

	waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.Phase == AdapterPhaseAnnounce
	}, "an announce frame in response to hello")

	cancel()
	wg.Wait()
}

// TestRespondsToPing: the probe is what turns a missed heartbeat into a
// confirmed verdict, so an adapter that cannot answer it will be wrongly
// declared dead.
func TestRespondsToPing(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a := NewAdapter(AdapterConfig{
			AssetID:         "sensor-1",
			NATSURL:         url,
			CollectInterval: 20 * time.Millisecond,
		}, &fakeCollector{})
		_ = a.Run(ctx)
	}()

	waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.Phase == AdapterPhaseOnline
	}, "the online frame")

	if _, err := control.Request(SubjectAdapterPingPrefix+"sensor-1", []byte("{}"), 2*time.Second); err != nil {
		t.Fatalf("adapter must answer a liveness probe: %v", err)
	}

	cancel()
	wg.Wait()
}

func TestHostReportingIsOptOut(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	frames := collectStatus(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	a := NewAdapter(AdapterConfig{
		AssetID:         "sensor-1",
		NATSURL:         url,
		CollectInterval: 20 * time.Millisecond,
	}, &fakeCollector{})
	_ = a.Run(ctx)

	f := waitForFrame(t, frames, func(f AdapterStatusFrame) bool {
		return f.Phase == AdapterPhaseOnline
	}, "the online frame")

	if f.Host != "" || f.PID != 0 {
		t.Errorf("host/pid must be opt-in, got host=%q pid=%d", f.Host, f.PID)
	}
}

// TestGoldenFrameRoundTrip is the cross-SDK contract. The same fixture is
// parsed by the Python SDK's test suite, so a field that drifts on either side
// fails here rather than producing frames core silently cannot read.
func TestGoldenFrameRoundTrip(t *testing.T) {
	path := filepath.Join("testdata", "adapter_status.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	var frame AdapterStatusFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("golden must decode into AdapterStatusFrame: %v", err)
	}

	if frame.SchemaVersion != AdapterSchemaVersion {
		t.Errorf("schema_version = %d, want %d", frame.SchemaVersion, AdapterSchemaVersion)
	}
	if frame.AdapterID != "modbus-line3" || frame.InstanceID == "" {
		t.Errorf("identity fields did not survive: %+v", frame)
	}
	if frame.RunState != RunStateRunning {
		t.Errorf("run_state = %q", frame.RunState)
	}
	if frame.DeviceState != string(DeviceConnected) {
		t.Errorf("device_state = %q, want the shared DeviceState vocabulary", frame.DeviceState)
	}
	if len(frame.Assets) != 1 || frame.Assets[0].AssetID != "press-01" {
		t.Errorf("assets did not survive: %+v", frame.Assets)
	}
	if frame.Counters.PublishedTotal != 4098 {
		t.Errorf("counters did not survive: %+v", frame.Counters)
	}

	// Re-encode and decode again: a field the Go struct silently drops would
	// disappear here even though the first decode succeeded.
	reencoded, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var second AdapterStatusFrame
	if err := json.Unmarshal(reencoded, &second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frame, second) {
		t.Errorf("round trip changed the frame:\n first: %+v\nsecond: %+v", frame, second)
	}
}

// alwaysFailingCollector fails every collect with a non-device error, which is
// what drives the degraded run state while leaving the link connected.
type alwaysFailingCollector struct{ err error }

func (c *alwaysFailingCollector) Collect(context.Context) ([]TagValue, error) {
	return nil, c.err
}
