package sdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

// SDKVersion is reported in the status frame so an operator can tell which
// adapters still run an old SDK.
const SDKVersion = "go/0.6.0"

// Status reporting defaults.
const (
	DefaultHeartbeatInterval = 10 * time.Second
	// degradedAfterErrors is when consecutive collect failures stop being
	// noise. Deliberately independent of the device link: a PLC that answers
	// but returns garbage leaves the connection state "connected".
	degradedAfterErrors = 3
	// offlineFlushTimeout bounds the wait for the goodbye frame to reach the
	// server. Short: a shutting-down adapter must not hang on an unreachable
	// broker.
	offlineFlushTimeout = 2 * time.Second
)

// statusReporter publishes adapter runtime status (ADR 0008).
//
// Counters are atomics updated from the collect loop, and the frame is
// assembled by the reporter's own goroutine, so no hook on the hot path does
// more than an atomic add or a non-blocking signal.
type statusReporter struct {
	client *Client
	cfg    *AdapterConfig

	adapterID  string
	instanceID string
	startedAt  time.Time
	interval   time.Duration

	seq                      atomic.Int64
	publishedTotal           atomic.Int64
	collectErrorsTotal       atomic.Int64
	deviceErrorsTotal        atomic.Int64
	deviceReconnectsTotal    atomic.Int64
	consecutiveCollectErrors atomic.Int64

	mu          sync.Mutex
	deviceState DeviceState
	lastError   string
	lastErrorAt *time.Time

	// dirty carries edge-triggered sends. Buffered by one: a burst of
	// transitions between ticks collapses into a single frame rather than
	// queueing, and a full channel is dropped rather than blocking the caller.
	dirty chan struct{}

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}

	// subs are unsubscribed on stop. Without this an adapter that has stopped
	// collecting keeps answering probes with "ok" and re-announcing itself as
	// running, so core reports a dead adapter as healthy.
	subs []*nats.Subscription
}

func newStatusReporter(client *Client, cfg *AdapterConfig) *statusReporter {
	interval := cfg.HeartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	adapterID := cfg.AdapterID
	if adapterID == "" {
		// A one-adapter-per-asset deployment needs no new configuration.
		adapterID = cfg.AssetID
	}
	// adapter_id is the last token of the status subject, so it must be a
	// single NATS token. asset_id has no such constraint, and the default
	// falls back to it: an asset named "line3.press" would produce frames core
	// drops with no symptom beyond the adapter never appearing.
	if sanitized := sanitizeAdapterID(adapterID); sanitized != adapterID {
		cfg.Logger.Warn("adapter_id is not a valid NATS subject token; reporting under a sanitized id",
			"from", adapterID, "to", sanitized,
			"hint", "set AdapterID explicitly to control it")
		adapterID = sanitized
	}
	return &statusReporter{
		client:      client,
		cfg:         cfg,
		adapterID:   adapterID,
		instanceID:  newInstanceID(),
		startedAt:   time.Now(),
		interval:    interval,
		deviceState: DeviceDisconnected,
		dirty:       make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// adapterIDInvalid matches every character that cannot appear in a subject
// token. A dot would silently reshape platform.adapter.status.<id> into extra
// tokens; wildcards would make the frame unroutable.
var adapterIDInvalid = regexp.MustCompile(`[^A-Za-z0-9_:\-]`)

// sanitizeAdapterID makes an id usable as a subject token, deterministically so
// that a restart maps to the same id.
func sanitizeAdapterID(id string) string {
	out := adapterIDInvalid.ReplaceAllString(id, "-")
	if out == "" {
		return "adapter"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	// The first character must be alphanumeric.
	if c := out[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
		out = "a" + out
		if len(out) > 64 {
			out = out[:64]
		}
	}
	return out
}

// newInstanceID identifies this process run, so core can tell a restart from a
// continuing process and detect two adapters sharing an id.
func newInstanceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}

// start begins heartbeating and answering hello/ping.
func (r *statusReporter) start(ctx context.Context) {
	if err := r.subscribeControl(); err != nil {
		r.cfg.Logger.Warn("adapter status control subscriptions failed", "err", err)
	}
	r.publish(ctx, AdapterPhaseOnline)
	go r.loop(ctx)
}

func (r *statusReporter) subscribeControl() error {
	nc, err := r.client.conn()
	if err != nil {
		return err
	}
	// hello: core restarted and wants everyone to re-announce.
	hello, err := nc.Subscribe(SubjectAdapterHello, func(_ *nats.Msg) {
		r.publish(context.Background(), AdapterPhaseAnnounce)
	})
	if err != nil {
		return err
	}
	// ping: core missed a heartbeat and is checking before declaring us dead.
	ping, err := nc.Subscribe(SubjectAdapterPingPrefix+r.adapterID, func(msg *nats.Msg) {
		_ = msg.Respond([]byte(`{"ok":true}`))
	})
	if err != nil {
		_ = hello.Unsubscribe()
		return err
	}
	r.subs = append(r.subs, hello, ping)
	return nil
}

func (r *statusReporter) loop(ctx context.Context) {
	defer close(r.done)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-ticker.C:
			r.publish(ctx, AdapterPhaseHeartbeat)
		case <-r.dirty:
			// Edge-triggered: a state change is reported immediately rather
			// than waiting up to a whole interval.
			r.publish(ctx, AdapterPhaseHeartbeat)
		}
	}
}

// markDirty requests an out-of-band frame. Non-blocking, so it is safe to call
// from the collect loop and from state transitions.
func (r *statusReporter) markDirty() {
	select {
	case r.dirty <- struct{}{}:
	default:
	}
}

// setDeviceState records a transition. Called after the adapter releases its
// own lock — publishing under that lock would put network I/O inside the state
// machine.
func (r *statusReporter) setDeviceState(s DeviceState) {
	r.mu.Lock()
	changed := r.deviceState != s
	r.deviceState = s
	r.mu.Unlock()
	if changed {
		if s == DeviceError || s == DeviceDisconnected {
			r.deviceErrorsTotal.Add(1)
		}
		if s == DeviceReconnecting {
			r.deviceReconnectsTotal.Add(1)
		}
		r.markDirty()
	}
}

func (r *statusReporter) incPublished() {
	r.publishedTotal.Add(1)
	r.consecutiveCollectErrors.Store(0)
}

func (r *statusReporter) incCollectError(err error) {
	r.collectErrorsTotal.Add(1)
	n := r.consecutiveCollectErrors.Add(1)

	r.mu.Lock()
	now := time.Now()
	r.lastError = err.Error()
	r.lastErrorAt = &now
	r.mu.Unlock()

	// Report the moment the adapter becomes degraded rather than waiting for
	// the next tick; that transition is the whole point of the axis.
	if n == degradedAfterErrors {
		r.markDirty()
	}
}

func (r *statusReporter) runState() RunState {
	if r.consecutiveCollectErrors.Load() >= degradedAfterErrors {
		return RunStateDegraded
	}
	return RunStateRunning
}

// frame assembles the current status.
func (r *statusReporter) frame(phase string) AdapterStatusFrame {
	r.mu.Lock()
	deviceState := r.deviceState
	lastError := r.lastError
	lastErrorAt := r.lastErrorAt
	r.mu.Unlock()

	now := time.Now()
	runState := r.runState()
	if phase == AdapterPhaseOffline {
		runState = RunStateStopped
	}

	counters := AdapterCounters{
		PublishedTotal:           r.publishedTotal.Load(),
		CollectErrorsTotal:       r.collectErrorsTotal.Load(),
		DeviceErrorsTotal:        r.deviceErrorsTotal.Load(),
		DeviceReconnectsTotal:    r.deviceReconnectsTotal.Load(),
		ConsecutiveCollectErrors: r.consecutiveCollectErrors.Load(),
	}

	assets := []AdapterAssetStatus{{
		AssetID:            r.cfg.AssetID,
		DeviceState:        string(deviceState),
		PublishedTotal:     counters.PublishedTotal,
		CollectErrorsTotal: counters.CollectErrorsTotal,
		LastError:          lastError,
		LastErrorAt:        lastErrorAt,
	}}

	frame := AdapterStatusFrame{
		SchemaVersion:      AdapterSchemaVersion,
		AdapterID:          r.adapterID,
		InstanceID:         r.instanceID,
		Seq:                r.seq.Add(1),
		Phase:              phase,
		RunState:           runState,
		DeviceState:        string(deviceState),
		HeartbeatIntervalS: int(r.interval / time.Second),
		UptimeS:            int64(now.Sub(r.startedAt).Seconds()),
		StartedAt:          r.startedAt.UTC(),
		SentAt:             now.UTC(),
		SDK:                SDKVersion,
		AdapterVersion:     r.cfg.AdapterVersion,
		Capabilities:       []string{"ping"},
		Assets:             assets,
		DeviceCounts:       map[string]int{string(deviceState): 1},
		Counters:           counters,
	}
	if r.cfg.ReportHost {
		frame.Host, _ = os.Hostname()
		frame.PID = os.Getpid()
	}
	return frame
}

func (r *statusReporter) publish(ctx context.Context, phase string) {
	raw, err := json.Marshal(r.frame(phase))
	if err != nil {
		r.cfg.Logger.Warn("encode adapter status", "err", err)
		return
	}
	if err := r.client.PublishRaw(ctx, SubjectAdapterStatusPrefix+r.adapterID, raw); err != nil {
		// Status is best-effort by design: failing to report must never take
		// down an adapter that is otherwise collecting fine.
		r.cfg.Logger.Debug("publish adapter status", "err", err)
	}
}

// stopWith sends a final frame and halts. Called from a deferred function
// registered after the client's own close defer, so LIFO ordering guarantees
// the connection is still open here.
func (r *statusReporter) stopWith(ctx context.Context) {
	r.stopOnce.Do(func() {
		close(r.stop)
		<-r.done
		// Stop answering before saying goodbye: a probe answered after the
		// offline frame would resurrect a stopped adapter in core's registry.
		for _, sub := range r.subs {
			_ = sub.Unsubscribe()
		}
		r.subs = nil
		r.publish(ctx, AdapterPhaseOffline)
		// Publishing is buffered and Client.Close drains asynchronously, so
		// without an explicit flush the goodbye frame is lost whenever the
		// process exits promptly after Run returns — which is the normal case.
		// Core would then report the adapter stale minutes later instead of
		// knowing at once that it stopped cleanly.
		if err := r.client.Flush(offlineFlushTimeout); err != nil {
			r.cfg.Logger.Debug("flush adapter offline frame", "err", err)
		}
	})
}
