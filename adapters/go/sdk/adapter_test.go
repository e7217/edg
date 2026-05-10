package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type fakeCollector struct {
	calls atomic.Int32
	out   []TagValue
	err   error
}

func (f *fakeCollector) Collect(ctx context.Context) ([]TagValue, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func newAdapter(t *testing.T, url string, c Collector, opts ...func(*AdapterConfig)) *Adapter {
	t.Helper()
	cfg := AdapterConfig{
		AssetID:         "test-asset",
		NATSURL:         url,
		CollectInterval: 20 * time.Millisecond,
	}
	for _, fn := range opts {
		fn(&cfg)
	}
	return NewAdapter(cfg, c)
}

func TestAdapterPublishesCollectedValues(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)

	got := make(chan AssetData, 4)
	sub, err := control.Subscribe(SubjectAssetData, func(msg *nats.Msg) {
		var ad AssetData
		if err := json.Unmarshal(msg.Data, &ad); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		select {
		case got <- ad:
		default:
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	n := 1.5
	c := &fakeCollector{out: []TagValue{{Name: "x", Quality: QualityGood, Number: &n}}}
	a := newAdapter(t, url, c, func(cfg *AdapterConfig) {
		cfg.Metadata = map[string]string{"k": "v"}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Errorf("Run: %v", err)
	}

	select {
	case ad := <-got:
		if ad.AssetID != "test-asset" {
			t.Errorf("AssetID: %q", ad.AssetID)
		}
		if ad.Metadata["k"] != "v" {
			t.Errorf("Metadata: %v", ad.Metadata)
		}
	default:
		t.Fatalf("no AssetData received; collector calls = %d", c.calls.Load())
	}
}

func TestAdapterRequiresAssetID(t *testing.T) {
	a := NewAdapter(AdapterConfig{}, &fakeCollector{})
	err := a.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing AssetID")
	}
}

func TestAdapterEmptyValuesSkipsPublish(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	got := make(chan struct{}, 1)
	sub, _ := control.Subscribe(SubjectAssetData, func(*nats.Msg) {
		got <- struct{}{}
	})
	defer sub.Unsubscribe()

	c := &fakeCollector{out: nil}
	a := newAdapter(t, url, c)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = a.Run(ctx)

	select {
	case <-got:
		t.Errorf("expected no publish for empty values")
	default:
	}
	if c.calls.Load() == 0 {
		t.Errorf("collector should have been called")
	}
}

// deviceCollector implements Collector + DeviceLifecycle + DeviceObserver.
type deviceCollector struct {
	mu sync.Mutex

	connectErrs []error // pop one per ConnectDevice
	connectN    atomic.Int32
	disconnectN atomic.Int32
	healthErr   error

	collectVals []TagValue
	collectErr  error

	onConnected     atomic.Int32
	onReconnected   atomic.Int32
	onDisconnectedN atomic.Int32
}

func (d *deviceCollector) ConnectDevice(ctx context.Context) error {
	d.connectN.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.connectErrs) == 0 {
		return nil
	}
	err := d.connectErrs[0]
	d.connectErrs = d.connectErrs[1:]
	return err
}

func (d *deviceCollector) DisconnectDevice(ctx context.Context) error {
	d.disconnectN.Add(1)
	return nil
}

func (d *deviceCollector) CheckDeviceHealth(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.healthErr
}

func (d *deviceCollector) Collect(ctx context.Context) ([]TagValue, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.collectErr != nil {
		return nil, d.collectErr
	}
	return d.collectVals, nil
}

func (d *deviceCollector) OnDeviceConnected(ctx context.Context)              { d.onConnected.Add(1) }
func (d *deviceCollector) OnDeviceDisconnected(ctx context.Context, _ error)  { d.onDisconnectedN.Add(1) }
func (d *deviceCollector) OnDeviceReconnected(ctx context.Context)            { d.onReconnected.Add(1) }

func TestAdapterDeviceConnectFailureBackoffAndRecovery(t *testing.T) {
	url := startTestNATSServer(t)

	d := &deviceCollector{
		connectErrs: []error{ErrDeviceConnection, ErrDeviceConnection}, // fail twice, then succeed
		collectVals: []TagValue{{Name: "ok", Quality: QualityGood}},
	}

	a := newAdapter(t, url, d, func(cfg *AdapterConfig) {
		cfg.MaxRetries = 5
		cfg.Backoff = Backoff{Base: 1 * time.Millisecond, Max: 10 * time.Millisecond}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if d.connectN.Load() < 3 {
		t.Errorf("expected >= 3 ConnectDevice calls, got %d", d.connectN.Load())
	}
	if d.onConnected.Load()+d.onReconnected.Load() == 0 {
		t.Errorf("expected at least one connected/reconnected callback")
	}
	if d.disconnectN.Load() == 0 {
		t.Errorf("expected DisconnectDevice on shutdown")
	}
	if a.DeviceState() != DeviceDisconnected {
		t.Errorf("post-Run state: %s", a.DeviceState())
	}
}

func TestAdapterDeviceConnectMaxRetriesGivesUp(t *testing.T) {
	url := startTestNATSServer(t)

	failures := []error{}
	for i := 0; i < 10; i++ {
		failures = append(failures, ErrDeviceConnection)
	}
	d := &deviceCollector{connectErrs: failures}

	a := newAdapter(t, url, d, func(cfg *AdapterConfig) {
		cfg.MaxRetries = 3
		cfg.Backoff = Backoff{Base: 1 * time.Millisecond, Max: 5 * time.Millisecond}
	})

	err := a.Run(context.Background())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !errors.Is(err, ErrDevice) {
		t.Errorf("error chain missing ErrDevice: %v", err)
	}
	if a.DeviceState() != DeviceError {
		t.Errorf("expected DeviceError state, got %s", a.DeviceState())
	}
	if d.onDisconnectedN.Load() == 0 {
		t.Errorf("expected OnDeviceDisconnected on give up")
	}
}

func TestAdapterContextCancelStopsCleanly(t *testing.T) {
	url := startTestNATSServer(t)
	d := &deviceCollector{
		collectVals: []TagValue{{Name: "x", Quality: QualityGood}},
	}
	a := newAdapter(t, url, d)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	// Wait until the device has connected.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && d.connectN.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}
	if d.disconnectN.Load() == 0 {
		t.Errorf("expected DisconnectDevice on shutdown")
	}
}

func TestAdapterCollectorWithoutLifecycleStillRuns(t *testing.T) {
	url := startTestNATSServer(t)
	c := &fakeCollector{out: []TagValue{{Name: "x", Quality: QualityGood}}}
	a := newAdapter(t, url, c)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Errorf("Run: %v", err)
	}
	if c.calls.Load() == 0 {
		t.Errorf("collector not called")
	}
	if a.DeviceState() != DeviceConnected {
		t.Errorf("state: %s", a.DeviceState())
	}
}
