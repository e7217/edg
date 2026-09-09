package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Subscription buffer sizing. Heartbeats are plain NATS: if the client's
// pending buffer overflows, messages are dropped silently and the registry
// would report false stale. The limits are raised well above any realistic
// fleet, and Dropped() is exported as a counter so a breach is visible rather
// than being mistaken for dead adapters.
const (
	adapterPendingMsgLimit   = 1 << 20
	adapterPendingBytesLimit = 256 * 1024 * 1024
)

// AdapterHandler bridges the adapter runtime-status plane to the registry.
type AdapterHandler struct {
	registry *AdapterRegistry
	clock    Clock
	nc       *nats.Conn

	probeTimeout time.Duration

	mu   sync.Mutex
	subs []*nats.Subscription

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
	// watching records whether the drop watcher goroutine was started, so Stop
	// does not block forever when RegisterHandlers never ran (or failed).
	watching bool
}

// AdapterHandlerOptions configures the handler.
type AdapterHandlerOptions struct {
	Clock Clock
	// ProbeTimeout bounds the active liveness probe.
	ProbeTimeout time.Duration
	// HelloDelay re-broadcasts hello once more after this long, catching
	// adapters that were themselves restarting during the first broadcast.
	HelloDelay time.Duration
}

// NewAdapterHandler builds a handler around a registry.
func NewAdapterHandler(registry *AdapterRegistry, opts AdapterHandlerOptions) *AdapterHandler {
	if opts.Clock == nil {
		opts.Clock = SystemClock
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = 2 * time.Second
	}
	return &AdapterHandler{
		registry:     registry,
		clock:        opts.Clock,
		probeTimeout: opts.ProbeTimeout,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// RegisterHandlers subscribes the adapter plane and broadcasts the initial
// hello. It follows MetaHandler.RegisterHandlers' shape.
func (h *AdapterHandler) RegisterHandlers(nc *nats.Conn) error {
	h.mu.Lock()
	h.nc = nc
	h.mu.Unlock()

	statusSub, err := nc.Subscribe(SubjectAdapterStatusAll, h.handleStatus)
	if err != nil {
		return err
	}
	if err := statusSub.SetPendingLimits(adapterPendingMsgLimit, adapterPendingBytesLimit); err != nil {
		return err
	}
	log.Printf("[Adapter] Subscribed: %s", SubjectAdapterStatusAll)

	listSub, err := nc.Subscribe(SubjectAdapterList, h.handleList)
	if err != nil {
		return err
	}
	log.Printf("[Adapter] Subscribed: %s", SubjectAdapterList)

	h.mu.Lock()
	h.subs = append(h.subs, statusSub, listSub)
	h.mu.Unlock()

	// Ask everyone to re-announce. Without this a core restart leaves the
	// registry empty until each adapter's next heartbeat, which for a slow
	// collector could be minutes of looking dead.
	h.BroadcastHello()

	h.mu.Lock()
	h.watching = true
	h.mu.Unlock()
	go h.watchDrops(statusSub)

	return nil
}

// BroadcastHello requests a re-announce from every adapter.
func (h *AdapterHandler) BroadcastHello() {
	h.mu.Lock()
	nc := h.nc
	h.mu.Unlock()
	if nc == nil {
		return
	}
	if err := nc.Publish(SubjectAdapterHello, []byte("{}")); err != nil {
		log.Printf("[Adapter] hello broadcast failed: %v", err)
	}
}

// Probe implements Prober. A missed heartbeat is only a hint; this turns it
// into an answer.
func (h *AdapterHandler) Probe(adapterID string) bool {
	h.mu.Lock()
	nc := h.nc
	h.mu.Unlock()
	if nc == nil {
		return false
	}
	// A dedicated reply subject rather than the default inbox: ADR 0007 denies
	// _INBOX publish to the adapter role, and relaxing that to let adapters
	// answer a probe would also let one race core to answer another client's
	// metadata request.
	reply := SubjectAdapterPongPrefix + adapterID + "." + probeNonce()
	sub, err := nc.SubscribeSync(reply)
	if err != nil {
		return false
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.PublishRequest(SubjectAdapterPingPrefix+adapterID, reply, []byte("{}")); err != nil {
		return false
	}
	if _, err := sub.NextMsg(h.probeTimeout); err != nil {
		return false
	}
	return true
}

// probeNonce keeps a late reply to an earlier probe from satisfying a later
// one, which would otherwise mask an adapter that has since gone quiet.
func probeNonce() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "0"
	}
	return hex.EncodeToString(buf)
}

func (h *AdapterHandler) handleStatus(msg *nats.Msg) {
	adapterID := strings.TrimPrefix(msg.Subject, SubjectAdapterStatusPrefix)
	if adapterID == msg.Subject || adapterID == "" {
		adapterStatusInvalid.Add(1)
		return
	}

	var frame AdapterStatusFrame
	if err := json.Unmarshal(msg.Data, &frame); err != nil {
		adapterStatusInvalid.Add(1)
		log.Printf("[Adapter] malformed status frame on %s: %v", msg.Subject, err)
		return
	}
	if frame.RunState != "" && !IsValidRunState(frame.RunState) {
		adapterStatusInvalid.Add(1)
		return
	}

	registry := h.currentRegistry()
	if registry == nil {
		adapterStatusInvalid.Add(1)
		return
	}
	if !registry.Observe(&frame, adapterID, h.clock.Now()) {
		adapterStatusInvalid.Add(1)
	}
}

// currentRegistry reads the registry under the lock. The handler is also the
// registry's Prober, so the two are mutually referential and the registry is
// assigned after construction.
func (h *AdapterHandler) currentRegistry() *AdapterRegistry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.registry
}

func (h *AdapterHandler) handleList(msg *nats.Msg) {
	registry := h.currentRegistry()
	if registry == nil {
		return
	}
	snap := registry.Snapshot()
	data, err := json.Marshal(Response{Success: true, Data: snap})
	if err != nil {
		log.Printf("[Adapter] failed to encode list response: %v", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		log.Printf("[Adapter] failed to respond to list: %v", err)
	}
}

// watchDrops turns silent NATS pending-buffer drops into a counter. A dropped
// heartbeat looks exactly like a dead adapter, so this distinguishes "we lost
// the message" from "it stopped sending".
func (h *AdapterHandler) watchDrops(sub *nats.Subscription) {
	defer close(h.done)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	var last int
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			dropped, err := sub.Dropped()
			if err != nil {
				return
			}
			if dropped > last {
				adapterStatusDropped.Add(int64(dropped - last))
				log.Printf("[Adapter] WARNING: %d status messages dropped; stale verdicts may be false", dropped-last)
				last = dropped
			}
		}
	}
}

// Stop unsubscribes and halts the drop watcher.
func (h *AdapterHandler) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
		h.mu.Lock()
		subs := h.subs
		h.subs = nil
		watching := h.watching
		h.mu.Unlock()
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
		if watching {
			<-h.done
		}
	})
}

// SetRegistry attaches the registry. It exists because the handler is also the
// registry's Prober, so the two are mutually referential and one of them has
// to be wired after construction.
func (h *AdapterHandler) SetRegistry(r *AdapterRegistry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registry = r
}
