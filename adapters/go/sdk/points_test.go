package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// fakeCore answers platform.meta.points.get from a mutable list and can
// announce changes, standing in for EDG Core.
type fakeCore struct {
	t  *testing.T
	nc *nats.Conn

	mu       sync.Mutex
	list     *PointList
	requests int
}

func newFakeCore(t *testing.T, nc *nats.Conn, list *PointList) *fakeCore {
	t.Helper()
	fc := &fakeCore{t: t, nc: nc, list: list}
	_, err := nc.Subscribe(SubjectPointsGet, func(msg *nats.Msg) {
		fc.mu.Lock()
		fc.requests++
		var resp any
		if fc.list == nil {
			resp = map[string]any{"success": false, "error": "asset not found"}
		} else {
			resp = map[string]any{"success": true, "data": fc.list}
		}
		fc.mu.Unlock()
		b, _ := json.Marshal(resp)
		_ = msg.Respond(b)
	})
	if err != nil {
		t.Fatalf("subscribe points.get: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return fc
}

// set replaces the list; announce also publishes the change event.
func (fc *fakeCore) set(list *PointList, announce bool) {
	fc.mu.Lock()
	fc.list = list
	fc.mu.Unlock()
	if !announce {
		return
	}
	ev := map[string]any{"schema_version": 1, "event_type": "updated", "entity_type": "points",
		"entity_id": list.AssetID, "after": list}
	b, _ := json.Marshal(ev)
	if err := fc.nc.Publish(SubjectPointsChanged, b); err != nil {
		fc.t.Fatal(err)
	}
}

func listV(version int, names ...string) *PointList {
	pl := &PointList{AssetID: "pump-a", Protocol: "test", Version: version, PollIntervalMS: 20}
	for _, n := range names {
		pl.Points = append(pl.Points, Point{Name: n, ValueType: "NUMBER", Address: n, Enabled: true})
	}
	return pl
}

// recordingFactory builds collectors that report the names of their points,
// and records every list it was asked to build from.
type recordingFactory struct {
	mu    sync.Mutex
	built []int
}

func (f *recordingFactory) build(pl *PointList) (Collector, error) {
	f.mu.Lock()
	f.built = append(f.built, pl.Version)
	f.mu.Unlock()
	var out []TagValue
	for _, p := range pl.EnabledPoints() {
		v := float64(pl.Version)
		out = append(out, TagValue{Name: p.Name, Number: &v, Quality: QualityGood})
	}
	return &fakeCollector{out: out}, nil
}

func (f *recordingFactory) versions() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.built...)
}

func watchData(t *testing.T, nc *nats.Conn) <-chan AssetData {
	t.Helper()
	got := make(chan AssetData, 64)
	_, err := nc.Subscribe(SubjectAssetData, func(msg *nats.Msg) {
		var ad AssetData
		if json.Unmarshal(msg.Data, &ad) == nil {
			select {
			case got <- ad:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return got
}

// waitForTag waits for published data carrying tag with value want.
func waitForTag(t *testing.T, got <-chan AssetData, tag string, want float64) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ad := <-got:
			for _, v := range ad.Values {
				if v.Name == tag && v.Number != nil && *v.Number == want {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no data with %s=%v", tag, want)
		}
	}
}

func runProvisioned(t *testing.T, url string, f *recordingFactory, reconcile time.Duration) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		done <- RunProvisioned(ctx, ProvisionedConfig{
			Adapter: AdapterConfig{AssetID: "pump-a", NATSURL: url,
				DisableStatusReporting: true},
			RetryInterval:     20 * time.Millisecond,
			ReconcileInterval: reconcile,
		}, f.build)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			t.Error("RunProvisioned did not return after cancel")
		}
	})
	return cancel, done
}

func TestRunProvisionedBuildsFromTheDeclaredList(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	newFakeCore(t, control, listV(1, "temperature"))
	got := watchData(t, control)

	f := &recordingFactory{}
	runProvisioned(t, url, f, -1)
	waitForTag(t, got, "temperature", 1)
	if v := f.versions(); len(v) != 1 || v[0] != 1 {
		t.Fatalf("built %v, want [1]", v)
	}
}

func TestRunProvisionedRebuildsOnChangeEvent(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	core := newFakeCore(t, control, listV(1, "temperature"))
	got := watchData(t, control)

	f := &recordingFactory{}
	runProvisioned(t, url, f, -1)
	waitForTag(t, got, "temperature", 1)

	core.set(listV(2, "temperature", "pressure"), true)
	waitForTag(t, got, "pressure", 2)
	if v := f.versions(); len(v) != 2 || v[1] != 2 {
		t.Fatalf("built %v, want [1 2]", v)
	}
}

// Events are best-effort. A change whose event was lost is still picked up
// by the periodic reconcile.
func TestRunProvisionedReconcilesAMissedEvent(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	core := newFakeCore(t, control, listV(1, "temperature"))
	got := watchData(t, control)

	f := &recordingFactory{}
	runProvisioned(t, url, f, 50*time.Millisecond)
	waitForTag(t, got, "temperature", 1)

	core.set(listV(3, "flow"), false) // no event
	waitForTag(t, got, "flow", 3)
}

func TestRunProvisionedIgnoresStaleAndForeignEvents(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	core := newFakeCore(t, control, listV(5, "temperature"))
	got := watchData(t, control)

	f := &recordingFactory{}
	runProvisioned(t, url, f, -1)
	waitForTag(t, got, "temperature", 5)

	core.set(listV(4, "old"), true) // older than what runs
	other := listV(9, "x")
	other.AssetID = "pump-b"
	b, _ := json.Marshal(map[string]any{"entity_id": "pump-b", "after": other})
	if err := control.Publish(SubjectPointsChanged, b); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if v := f.versions(); len(v) != 1 {
		t.Fatalf("rebuilt on an irrelevant event: %v", v)
	}
}

func TestRunProvisionedUndeclaredAssetIsAnError(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	newFakeCore(t, control, nil)

	f := &recordingFactory{}
	_, done := runProvisioned(t, url, f, -1)
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunProvisioned kept running for an asset core does not know")
	}
}

// A list the factory rejects leaves the adapter alive and waiting, and the
// next list recovers it.
func TestRunProvisionedSurvivesAnUnusableList(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	core := newFakeCore(t, control, listV(1, "bad"))
	got := watchData(t, control)

	f := &recordingFactory{}
	build := func(pl *PointList) (Collector, error) {
		if pl.Version == 1 {
			f.mu.Lock()
			f.built = append(f.built, 1)
			f.mu.Unlock()
			return nil, errors.New("address \"bad\" is not a register")
		}
		return f.build(pl)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunProvisioned(ctx, ProvisionedConfig{
			Adapter:           AdapterConfig{AssetID: "pump-a", NATSURL: url, DisableStatusReporting: true},
			ReconcileInterval: -1,
		}, build)
	}()

	waitFor(t, func() bool { return len(f.versions()) == 1 })
	core.set(listV(2, "good"), true)
	waitForTag(t, got, "good", 2)
	cancel()
	<-done
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestEnabledPoints(t *testing.T) {
	pl := &PointList{Points: []Point{{Name: "a", Enabled: true}, {Name: "b"}, {Name: "c", Enabled: true}}}
	got := pl.EnabledPoints()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("got %+v", got)
	}
}

// An adapter stuck on a list it cannot use must not look converged: it keeps
// reporting the last version it could run.
func TestRunProvisionedReportsTheLastUsableVersion(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	core := newFakeCore(t, control, listV(1, "temperature"))
	got := watchData(t, control)

	frames := make(chan AdapterStatusFrame, 64)
	if _, err := control.Subscribe(SubjectAdapterStatusPrefix+"pump-a", func(msg *nats.Msg) {
		var f AdapterStatusFrame
		if json.Unmarshal(msg.Data, &f) == nil {
			select {
			case frames <- f:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	build := func(pl *PointList) (Collector, error) {
		if pl.Version == 2 {
			return nil, errors.New("address is not a register")
		}
		return (&recordingFactory{}).build(pl)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunProvisioned(ctx, ProvisionedConfig{
			Adapter:           AdapterConfig{AssetID: "pump-a", NATSURL: url, HeartbeatInterval: time.Second},
			ReconcileInterval: -1,
		}, build)
	}()
	waitForTag(t, got, "temperature", 1)

	// Each generation is a new Adapter and so a new instance id.
	var first string
	select {
	case f := <-frames:
		first = f.InstanceID
	case <-time.After(3 * time.Second):
		t.Fatal("no status frame from the first generation")
	}

	core.set(listV(2, "bad"), true)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-frames:
			if f.InstanceID == first {
				continue
			}
			if f.ConfigVersion != 1 || f.Assets[0].ConfigVersion != 1 {
				t.Fatalf("rebuilt generation reports v%d/v%d, want the last usable v1",
					f.ConfigVersion, f.Assets[0].ConfigVersion)
			}
			return
		case <-deadline:
			t.Fatal("no frame from the rebuilt generation")
		}
	}
}
