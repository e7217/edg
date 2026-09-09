package core

import (
	"sort"
	"sync"
	"time"
)

// Adapter registry defaults. All are overridable through AdaptersConfig.
const (
	DefaultAdapterMinInterval   = 1 * time.Second
	DefaultAdapterMaxInterval   = 300 * time.Second
	DefaultAdapterStaleFloor    = 15 * time.Second
	DefaultAdapterForgetAfter   = 24 * time.Hour
	DefaultAdapterMaxProbes     = 32
	DefaultAdapterDeadlineScale = 3
)

// AdapterChangePublisher receives transition events. Mirrors the
// AlarmGroupPublisher pattern in alarm_aggregator.go so the registry can be
// unit tested without NATS.
type AdapterChangePublisher interface {
	PublishAdapterChanged(ev AdapterChangeEvent)
}

// Prober asks an adapter whether it is still there. Returning true resets the
// deadline without emitting an event; false confirms the death that a missed
// heartbeat alone only suggests.
type Prober interface {
	Probe(adapterID string) bool
}

// AdapterRegistryOptions configures the registry. Zero values take defaults.
type AdapterRegistryOptions struct {
	Clock       Clock
	Publisher   AdapterChangePublisher
	Prober      Prober
	MinInterval time.Duration
	MaxInterval time.Duration
	StaleFloor  time.Duration
	ForgetAfter time.Duration
	MaxProbes   int
	ProbeOnMiss bool
	// WarmupFactor multiplies MaxInterval to decide how long Snapshot reports
	// itself as warming after start. Until then an empty registry means "not
	// heard from yet", not "everything is dead".
	WarmupFactor int
}

// AdapterRegistry tracks which adapters are alive.
//
// State is intentionally in memory only. Runtime liveness is volatile by
// definition: a persisted "connected" is a lie after a restart, and a persisted
// last_seen_at carries no monotonic component so it cannot be used for expiry
// either. Recovery after a core restart is instead handled by broadcasting
// hello and letting adapters re-announce.
type AdapterRegistry struct {
	mu      sync.RWMutex
	entries map[string]*AdapterEntry
	// byAsset is a set per asset, not a single adapter: two adapters claiming
	// the same asset is a real misdeployment, and modelling it as a set
	// detects that for free.
	byAsset map[string]map[string]bool
	// lastCounters keys off instance to reset rate derivation across restarts.
	lastObs map[string]observation

	opts      AdapterRegistryOptions
	startedAt time.Time

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

type observation struct {
	instanceID     string
	seq            int64
	publishedTotal int64
	at             time.Time
}

// NewAdapterRegistry builds a registry. Start must be called to run the reaper.
func NewAdapterRegistry(opts AdapterRegistryOptions) *AdapterRegistry {
	if opts.Clock == nil {
		opts.Clock = SystemClock
	}
	if opts.MinInterval <= 0 {
		opts.MinInterval = DefaultAdapterMinInterval
	}
	if opts.MaxInterval <= 0 {
		opts.MaxInterval = DefaultAdapterMaxInterval
	}
	if opts.StaleFloor <= 0 {
		opts.StaleFloor = DefaultAdapterStaleFloor
	}
	if opts.ForgetAfter <= 0 {
		opts.ForgetAfter = DefaultAdapterForgetAfter
	}
	if opts.MaxProbes <= 0 {
		opts.MaxProbes = DefaultAdapterMaxProbes
	}
	if opts.WarmupFactor <= 0 {
		opts.WarmupFactor = 2
	}
	return &AdapterRegistry{
		entries:   make(map[string]*AdapterEntry),
		byAsset:   make(map[string]map[string]bool),
		lastObs:   make(map[string]observation),
		opts:      opts,
		startedAt: opts.Clock.Now(),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Observe records a status frame received on subjectID's subject at receivedAt.
//
// subjectID is the last token of the NATS subject and is authoritative: a frame
// whose AdapterID disagrees is rejected, so an adapter cannot impersonate
// another by lying in the body.
func (r *AdapterRegistry) Observe(frame *AdapterStatusFrame, subjectID string, receivedAt time.Time) (accepted bool) {
	if frame == nil || !IsValidAdapterID(subjectID) {
		return false
	}
	if frame.AdapterID != "" && frame.AdapterID != subjectID {
		return false
	}

	var events []AdapterChangeEvent

	r.mu.Lock()
	prev, existed := r.entries[subjectID]

	entry := r.buildEntry(prev, frame, subjectID, receivedAt)

	switch {
	case !existed:
		events = append(events, AdapterChangeEvent{
			Change: AdapterChangeOnline, Reason: "first_seen",
			Timestamp: receivedAt, Adapter: entry,
		})
	case prev.InstanceID != "" && entry.InstanceID != "" && prev.InstanceID != entry.InstanceID:
		// A different process now owns this adapter_id: either a restart or
		// two adapters misconfigured with the same id.
		events = append(events, AdapterChangeEvent{
			Change: AdapterChangeReplaced, Reason: "instance_changed",
			Timestamp: receivedAt, Adapter: entry,
			Previous: summarize(prev),
		})
	case frame.Phase == AdapterPhaseOffline:
		events = append(events, AdapterChangeEvent{
			Change: AdapterChangeOffline, Reason: "graceful",
			Timestamp: receivedAt, Adapter: entry,
			Previous: summarize(prev),
		})
	case prev.Availability != AvailabilityOnline && entry.Availability == AvailabilityOnline:
		events = append(events, AdapterChangeEvent{
			Change: AdapterChangeOnline, Reason: "recovered",
			Timestamp: receivedAt, Adapter: entry,
			Previous: summarize(prev),
		})
	case watchedFieldsChanged(prev, entry):
		// Only watched fields produce an event. A heartbeat that moves nothing
		// but counters is silent, so a fleet at steady state emits ~0 events
		// per second no matter how fast it heartbeats.
		events = append(events, AdapterChangeEvent{
			Change: AdapterChangeUpdated, Timestamp: receivedAt,
			Adapter: entry, Previous: summarize(prev),
		})
	}

	r.entries[subjectID] = entry
	r.reindexAssets(subjectID, prev, entry)
	r.mu.Unlock()

	r.publish(events)
	return true
}

// buildEntry folds a frame into the previous entry. Caller holds the lock.
func (r *AdapterRegistry) buildEntry(prev *AdapterEntry, frame *AdapterStatusFrame, id string, at time.Time) *AdapterEntry {
	interval := r.clampInterval(time.Duration(frame.HeartbeatIntervalS) * time.Second)

	entry := &AdapterEntry{
		AdapterID:          id,
		InstanceID:         frame.InstanceID,
		RunState:           frame.RunState,
		DeviceState:        frame.DeviceState,
		LastSeenAt:         at,
		DeadlineAt:         at.Add(r.deadline(interval)),
		HeartbeatIntervalS: int(interval / time.Second),
		AssetIDs:           frame.AssetIDs(),
		Assets:             frame.Assets,
		DeviceCounts:       frame.DeviceCounts,
		Counters:           frame.Counters,
		SDK:                frame.SDK,
		AdapterVersion:     frame.AdapterVersion,
		Host:               frame.Host,
		PID:                frame.PID,
		Capabilities:       frame.Capabilities,
		ConfigVersion:      frame.ConfigVersion,
	}

	if frame.Phase == AdapterPhaseOffline {
		entry.Availability = AvailabilityOffline
		entry.StaleSince = &at
	} else {
		entry.Availability = AvailabilityOnline
	}

	entry.FirstSeenAt = at
	if prev != nil {
		entry.FirstSeenAt = prev.FirstSeenAt
	}

	// Clock skew is recorded for display only; it never feeds a decision.
	if !frame.SentAt.IsZero() {
		entry.ClockSkewS = frame.SentAt.Sub(at).Seconds()
	}

	entry.ObservedPublishHz = r.deriveRate(id, frame, at)
	return entry
}

// deriveRate differences PublishedTotal over the receive-time delta. Using the
// registry's own clock keeps the value correct when the adapter's is wrong.
// Caller holds the lock.
func (r *AdapterRegistry) deriveRate(id string, frame *AdapterStatusFrame, at time.Time) float64 {
	last, ok := r.lastObs[id]
	cur := observation{
		instanceID:     frame.InstanceID,
		seq:            frame.Seq,
		publishedTotal: frame.Counters.PublishedTotal,
		at:             at,
	}
	r.lastObs[id] = cur

	// A new instance or a seq regression means the counters restarted; a delta
	// across that boundary would be meaningless or negative.
	if !ok || last.instanceID != cur.instanceID || cur.seq < last.seq {
		return 0
	}
	elapsed := at.Sub(last.at).Seconds()
	if elapsed <= 0 {
		return 0
	}
	delta := cur.publishedTotal - last.publishedTotal
	if delta < 0 {
		return 0
	}
	return float64(delta) / elapsed
}

func (r *AdapterRegistry) clampInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return r.opts.MaxInterval
	}
	if d < r.opts.MinInterval {
		return r.opts.MinInterval
	}
	if d > r.opts.MaxInterval {
		return r.opts.MaxInterval
	}
	return d
}

func (r *AdapterRegistry) deadline(interval time.Duration) time.Duration {
	d := time.Duration(DefaultAdapterDeadlineScale) * interval
	if d < r.opts.StaleFloor {
		return r.opts.StaleFloor
	}
	return d
}

// reindexAssets keeps byAsset in sync. Caller holds the lock.
func (r *AdapterRegistry) reindexAssets(id string, prev, cur *AdapterEntry) {
	if prev != nil {
		for _, a := range prev.AssetIDs {
			if set := r.byAsset[a]; set != nil {
				delete(set, id)
				if len(set) == 0 {
					delete(r.byAsset, a)
				}
			}
		}
	}
	for _, a := range cur.AssetIDs {
		if r.byAsset[a] == nil {
			r.byAsset[a] = make(map[string]bool, 1)
		}
		r.byAsset[a][id] = true
	}
}

// watchedFieldsChanged decides whether a transition is worth an event.
func watchedFieldsChanged(prev, cur *AdapterEntry) bool {
	if prev.Availability != cur.Availability ||
		prev.RunState != cur.RunState ||
		prev.DeviceState != cur.DeviceState ||
		prev.ConfigVersion != cur.ConfigVersion {
		return true
	}
	if !equalStrings(prev.AssetIDs, cur.AssetIDs) || !equalStrings(prev.Capabilities, cur.Capabilities) {
		return true
	}
	return assetStatesChanged(prev.Assets, cur.Assets)
}

func assetStatesChanged(prev, cur []AdapterAssetStatus) bool {
	if len(prev) != len(cur) {
		return true
	}
	before := make(map[string]AdapterAssetStatus, len(prev))
	for _, a := range prev {
		before[a.AssetID] = a
	}
	for _, a := range cur {
		b, ok := before[a.AssetID]
		if !ok || b.DeviceState != a.DeviceState || b.LastError != a.LastError {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func summarize(e *AdapterEntry) *AdapterEntrySummary {
	if e == nil {
		return nil
	}
	return &AdapterEntrySummary{
		Availability: e.Availability,
		RunState:     e.RunState,
		DeviceState:  e.DeviceState,
	}
}

func (r *AdapterRegistry) publish(events []AdapterChangeEvent) {
	if r.opts.Publisher == nil {
		return
	}
	for _, ev := range events {
		ev.SchemaVersion = AdapterSchemaVersion
		r.opts.Publisher.PublishAdapterChanged(ev)
	}
}

// AdapterSnapshot is the registry's answer to "what is out there".
type AdapterSnapshot struct {
	Adapters []*AdapterEntry `json:"adapters"`
	// Warming is true for the first couple of heartbeat windows after start,
	// so an empty list is not misread as "everything is dead" right after a
	// core restart.
	Warming   bool      `json:"warming"`
	CheckedAt time.Time `json:"checked_at"`
}

// Snapshot returns every tracked adapter, sorted by id for stable output.
func (r *AdapterRegistry) Snapshot() AdapterSnapshot {
	now := r.opts.Clock.Now()

	r.mu.RLock()
	out := make([]*AdapterEntry, 0, len(r.entries))
	for _, e := range r.entries {
		clone := *e
		out = append(out, &clone)
	}
	r.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].AdapterID < out[j].AdapterID })
	return AdapterSnapshot{
		Adapters:  out,
		Warming:   now.Sub(r.startedAt) < time.Duration(r.opts.WarmupFactor)*r.opts.MaxInterval,
		CheckedAt: now,
	}
}

// Get returns one adapter.
func (r *AdapterRegistry) Get(id string) (*AdapterEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return nil, false
	}
	clone := *e
	return &clone, true
}

// AdaptersForAsset returns the adapters claiming an asset. More than one is a
// misdeployment worth surfacing, which is why this is a set.
func (r *AdapterRegistry) AdaptersForAsset(assetID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byAsset[assetID]))
	for id := range r.byAsset[assetID] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// CountBy returns how many adapters are in each availability.
func (r *AdapterRegistry) CountBy() map[Availability]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[Availability]int, 4)
	for _, e := range r.entries {
		out[e.Availability]++
	}
	return out
}

// Start runs the reaper until Stop. Expiry uses a single ticker scanning the
// map rather than a timer per entry: a thousand adapters expiring together
// would otherwise spawn a thousand goroutines, and a shared ticker makes the
// injected clock the only source of time in the whole path.
func (r *AdapterRegistry) Start() {
	go r.reapLoop()
}

// Stop halts the reaper. Safe to call more than once.
func (r *AdapterRegistry) Stop() {
	r.stopOnce.Do(func() {
		close(r.stop)
		<-r.done
	})
}

func (r *AdapterRegistry) reapLoop() {
	defer close(r.done)

	ticker := time.NewTicker(r.scanInterval())
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.Sweep(r.opts.Clock.Now())
		}
	}
}

// scanInterval is how often the reaper looks for expired adapters.
//
// It derives from StaleAfterFloor, not MaxInterval: the floor is the tightest
// deadline any adapter can have, so scanning at half of it bounds detection
// latency proportionally. Deriving it from MaxInterval instead would mean a
// 2s deadline still took up to 150s to notice — measured, not theoretical.
// Scanning a map of even a thousand entries costs microseconds, so a short
// interval is close to free.
func (r *AdapterRegistry) scanInterval() time.Duration {
	d := r.opts.StaleFloor / 2
	if d < time.Second {
		return time.Second
	}
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

// Sweep applies expiry at the given time. Exported so tests drive it directly
// with a fake clock instead of waiting on a ticker.
func (r *AdapterRegistry) Sweep(now time.Time) {
	expired, forgotten := r.collectExpired(now)

	// Probing happens outside the lock: it is a network round trip, and
	// holding the registry lock across it would stall every Observe.
	var events []AdapterChangeEvent
	sem := make(chan struct{}, r.opts.MaxProbes)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, id := range expired {
		if !r.opts.ProbeOnMiss || r.opts.Prober == nil {
			mu.Lock()
			events = append(events, r.markStale(id, now, "deadline_exceeded")...)
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(adapterID string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			default:
				// Over the concurrency budget: fall back to the deadline
				// verdict rather than queueing a probe storm.
				mu.Lock()
				events = append(events, r.markStale(adapterID, now, "deadline_exceeded")...)
				mu.Unlock()
				adapterProbesSkipped.Add(1)
				return
			}
			if r.opts.Prober.Probe(adapterID) {
				// Alive after all: a missed heartbeat was not a death.
				r.extendDeadline(adapterID, now)
				adapterProbeRecovered.Add(1)
				return
			}
			mu.Lock()
			events = append(events, r.markStale(adapterID, now, "probe_failed")...)
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	for _, id := range forgotten {
		events = append(events, r.forget(id, now)...)
	}

	r.publish(events)
}

// collectExpired partitions tracked adapters into those past their deadline and
// those past ForgetAfter.
func (r *AdapterRegistry) collectExpired(now time.Time) (expired, forgotten []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for id, e := range r.entries {
		switch {
		case e.StaleSince != nil && now.Sub(*e.StaleSince) >= r.opts.ForgetAfter:
			forgotten = append(forgotten, id)
		case e.Availability == AvailabilityOnline && now.After(e.DeadlineAt):
			expired = append(expired, id)
		}
	}
	sort.Strings(expired)
	sort.Strings(forgotten)
	return expired, forgotten
}

func (r *AdapterRegistry) markStale(id string, now time.Time, reason string) []AdapterChangeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[id]
	if !ok || e.Availability != AvailabilityOnline {
		return nil
	}
	prev := summarize(e)
	e.Availability = AvailabilityStale
	e.StaleSince = &now
	adapterStale.Add(1)

	clone := *e
	return []AdapterChangeEvent{{
		Change: AdapterChangeStale, Reason: reason,
		Timestamp: now, Adapter: &clone, Previous: prev,
	}}
}

func (r *AdapterRegistry) extendDeadline(id string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if e, ok := r.entries[id]; ok {
		e.DeadlineAt = now.Add(r.deadline(time.Duration(e.HeartbeatIntervalS) * time.Second))
	}
}

func (r *AdapterRegistry) forget(id string, now time.Time) []AdapterChangeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[id]
	if !ok {
		return nil
	}
	clone := *e
	delete(r.entries, id)
	delete(r.lastObs, id)
	for _, a := range e.AssetIDs {
		if set := r.byAsset[a]; set != nil {
			delete(set, id)
			if len(set) == 0 {
				delete(r.byAsset, a)
			}
		}
	}
	return []AdapterChangeEvent{{
		Change: AdapterChangeForgotten, Reason: "forget_after",
		Timestamp: now, Adapter: &clone,
	}}
}
