package core

import (
	"sort"
	"sync"
	"time"

	"github.com/e7217/edg/internal/metrics"
)

// DefaultAdapterMetricsMaxTracked caps the per-adapter series. Beyond it,
// adapters are folded into a single overflow label rather than dropped
// silently, so the aggregate counts stay honest.
const DefaultAdapterMetricsMaxTracked = 200

// adapterOverflowID is where adapters past the cap are folded. It is not a
// valid adapter id (IsValidAdapterID rejects the underscores at the ends), so
// it can never collide with a real one.
const adapterOverflowID = "__overflow__"

// adapterDroppedSeries counts adapters that could not get their own series.
var adapterDroppedSeries = metrics.Default.NewCounterVec(metrics.Desc{
	Name: "edg_core_adapter_series_dropped_total",
	Help: "Scrapes in which an adapter was folded into adapter_id=\"__overflow__\" because the per-adapter series cap was reached.",
}, "reason", []string{"max_tracked"})

// AdapterMetricsOptions tunes the per-adapter exposition.
type AdapterMetricsOptions struct {
	// MaxTracked caps per-adapter series. Zero means
	// DefaultAdapterMetricsMaxTracked; a negative value disables per-adapter
	// series entirely, leaving only the aggregates.
	MaxTracked int
}

// RegisterAdapterMetrics exposes the runtime-status registry (ADR 0008).
//
// Three layers, because they answer different questions. The aggregate by
// availability is what an alert fires on and costs four series regardless of
// fleet size. The per-adapter state set is what an operator opens the
// dashboard for. adapter_up is derived from the registry's own staleness
// verdict rather than from the last reported state, because an adapter that
// crashed cannot publish "stopped" -- staleness is the only thing that can
// detect it.
//
// adapter_id is the one identifier label ADR 0009 allows: it is operator-
// declared node topology, fixed at deployment, not data-plane content arriving
// over the wire.
func RegisterAdapterMetrics(r *metrics.Registry, reg *AdapterRegistry, opts AdapterMetricsOptions) {
	maxTracked := opts.MaxTracked
	if maxTracked == 0 {
		maxTracked = DefaultAdapterMetricsMaxTracked
	}

	src := &adapterMetricSource{reg: reg, maxTracked: maxTracked}
	r.BeforeGather(src.refresh)
	src.refresh()

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapters",
		Help: "Adapters known to the registry, by availability. Four series no matter how large the fleet is; this is what an alert should use.",
	}, "availability", src.byAvailability)

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapters_by_run_state",
		Help: "Adapters by their last reported run state. Unlike availability this is the adapter's own claim, so a crashed adapter keeps whatever it said last.",
	}, "run_state", src.byRunState)

	if maxTracked < 0 {
		return
	}

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapter_up",
		Help: "1 when the registry considers the adapter online. Derived from staleness, not from the reported run state: a crashed adapter never gets to publish that it stopped.",
	}, "adapter_id", src.up)

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapter_last_seen_timestamp_seconds",
		Help: "Unix time of the last status frame accepted from the adapter.",
	}, "adapter_id", src.lastSeen)

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapter_publish_hz",
		Help: "Publish rate the core derived from the adapter's own counter deltas over receive time, so it holds even when the adapter's clock is wrong.",
	}, "adapter_id", src.publishHz)

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapter_assets",
		Help: "Assets the adapter reports collecting.",
	}, "adapter_id", src.assetCount)

	r.NewLabelledGauge(metrics.Desc{
		Name: "edg_core_adapter_collect_errors_total",
		Help: "Collect errors reported by the adapter. A counter in meaning, exposed as a gauge because it is the adapter's number and resets when the adapter restarts.",
	}, "adapter_id", src.collectErrors)

	r.NewFuncGauge(metrics.Desc{
		Name: "edg_core_adapter_drift_issues",
		Help: "Runtime drift issues the registry can derive: assets claimed by more than one adapter, and adapters reporting assets with no master-data record.",
	}, func() float64 { return float64(src.driftIssues()) })
}

// adapterMetricSource takes one registry snapshot per scrape and serves every
// adapter family from it, so the layers cannot disagree with each other.
type adapterMetricSource struct {
	reg        *AdapterRegistry
	maxTracked int

	mu       sync.RWMutex
	entries  []*AdapterEntry // truncated to maxTracked, sorted by id
	overflow int
	counts   map[Availability]int
	runs     map[RunState]int
	drift    int
}

func (s *adapterMetricSource) refresh() {
	if s.reg == nil {
		return
	}
	snap := s.reg.Snapshot()

	entries := snap.Adapters
	sort.Slice(entries, func(i, j int) bool { return entries[i].AdapterID < entries[j].AdapterID })

	overflow := 0
	if s.maxTracked >= 0 && len(entries) > s.maxTracked {
		overflow = len(entries) - s.maxTracked
		entries = entries[:s.maxTracked]
		adapterDroppedSeries.With("max_tracked").Inc()
	}

	runs := make(map[RunState]int, 5)
	for _, e := range snap.Adapters {
		runs[e.RunState]++
	}

	s.mu.Lock()
	s.entries = entries
	s.overflow = overflow
	s.counts = s.reg.CountBy()
	s.runs = runs
	s.drift = s.reg.Drift().IssueCount
	s.mu.Unlock()
}

func (s *adapterMetricSource) byAvailability() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Every availability is always present, so a fleet going fully offline
	// shows as online=0 rather than as a series that vanished -- an absent
	// series and a zero are very different things to an alert.
	out := map[string]float64{
		string(AvailabilityOnline):  0,
		string(AvailabilityStale):   0,
		string(AvailabilityOffline): 0,
		string(AvailabilityUnknown): 0,
	}
	for k, v := range s.counts {
		out[string(k)] = float64(v)
	}
	return out
}

func (s *adapterMetricSource) byRunState() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]float64{
		string(RunStateStarting): 0,
		string(RunStateRunning):  0,
		string(RunStateDegraded): 0,
		string(RunStateStopping): 0,
		string(RunStateStopped):  0,
	}
	for k, v := range s.runs {
		out[string(k)] = float64(v)
	}
	return out
}

// perAdapter builds one series per tracked adapter, plus the overflow bucket
// when the cap was hit.
func (s *adapterMetricSource) perAdapter(pick func(*AdapterEntry) float64) map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]float64, len(s.entries)+1)
	for _, e := range s.entries {
		out[e.AdapterID] = pick(e)
	}
	if s.overflow > 0 {
		out[adapterOverflowID] = float64(s.overflow)
	}
	return out
}

func (s *adapterMetricSource) up() map[string]float64 {
	return s.perAdapter(func(e *AdapterEntry) float64 {
		if e.Availability == AvailabilityOnline {
			return 1
		}
		return 0
	})
}

func (s *adapterMetricSource) lastSeen() map[string]float64 {
	return s.perAdapter(func(e *AdapterEntry) float64 {
		if e.LastSeenAt.IsZero() {
			return 0
		}
		return float64(e.LastSeenAt.UnixNano()) / float64(time.Second)
	})
}

func (s *adapterMetricSource) publishHz() map[string]float64 {
	return s.perAdapter(func(e *AdapterEntry) float64 { return e.ObservedPublishHz })
}

func (s *adapterMetricSource) assetCount() map[string]float64 {
	return s.perAdapter(func(e *AdapterEntry) float64 { return float64(len(e.AssetIDs)) })
}

func (s *adapterMetricSource) collectErrors() map[string]float64 {
	return s.perAdapter(func(e *AdapterEntry) float64 { return float64(e.Counters.CollectErrorsTotal) })
}

func (s *adapterMetricSource) driftIssues() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drift
}
