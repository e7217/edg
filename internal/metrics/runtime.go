package metrics

import (
	"runtime"
	"runtime/metrics"
	"sort"
	"sync"
)

// runtimeLatencyBounds are the bounds the runtime histograms are downsampled
// to. runtime/metrics reports ~160 buckets per histogram; carrying all of them
// would cost more series than the rest of this process combined, and nobody
// alerts on 160-bucket resolution.
var runtimeLatencyBounds = []float64{
	1e-6, 5e-6, 1e-5, 5e-5, 1e-4, 5e-4, 1e-3, 1e-2, 1e-1, 1,
}

// runtimeSampler reads runtime/metrics once per scrape and hands the values to
// every collector derived from it.
//
// metrics.Read is one call for all samples; without a shared reader each
// collector would repeat it, which is both wasteful and internally
// inconsistent (different collectors would report different instants).
type runtimeSampler struct {
	mu      sync.Mutex
	samples []metrics.Sample
	index   map[string]int
}

func newRuntimeSampler(names []string) *runtimeSampler {
	supported := make(map[string]bool, len(metrics.All()))
	for _, d := range metrics.All() {
		supported[d.Name] = true
	}

	s := &runtimeSampler{index: make(map[string]int, len(names))}
	for _, name := range names {
		// A metric can disappear between Go releases. Skipping it drops one
		// series; reading it unconditionally would panic on some future
		// toolchain, taking the whole scrape with it.
		if !supported[name] {
			continue
		}
		s.index[name] = len(s.samples)
		s.samples = append(s.samples, metrics.Sample{Name: name})
	}
	return s
}

// refresh reloads every sample. It runs once per scrape as a BeforeGather
// hook: metrics.Read can suspend the world, so calling it once per derived
// series would be both wasteful and inconsistent.
func (s *runtimeSampler) refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	metrics.Read(s.samples)
}

func (s *runtimeSampler) uint64(name string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index[name]
	if !ok {
		return 0
	}
	return float64(s.samples[i].Value.Uint64())
}

func (s *runtimeSampler) histogram(name string, bounds []float64) HistogramSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index[name]
	if !ok {
		return HistogramSnapshot{Cumulative: make([]uint64, len(bounds))}
	}
	return downsample(s.samples[i].Value.Float64Histogram(), bounds)
}

// downsample folds a runtime/metrics histogram onto a coarser bound set.
//
// A runtime bucket spans [Buckets[i], Buckets[i+1]), so its samples are known
// to be <= b only when Buckets[i+1] <= b. Attributing a partially-overlapping
// bucket would invent precision, so those samples fall through to +Inf: bucket
// counts are conservative lower bounds while _count stays exact.
func downsample(h *metrics.Float64Histogram, bounds []float64) HistogramSnapshot {
	out := HistogramSnapshot{Cumulative: make([]uint64, len(bounds))}
	if h == nil {
		return out
	}

	var total, running uint64
	target := 0
	for i, c := range h.Counts {
		total += c
		upper := h.Buckets[i+1]
		// Close out every target bound this runtime bucket has passed.
		for target < len(bounds) && bounds[target] < upper {
			out.Cumulative[target] = running
			target++
		}
		running += c
	}
	for ; target < len(bounds); target++ {
		out.Cumulative[target] = running
	}
	out.Count = total
	return out
}

// RegisterRuntime adds the go_* family: goroutines, heap, GC and scheduler
// health. These are the same runtime/metrics values client_golang's
// GoCollector reads.
func (r *Registry) RegisterRuntime() {
	const (
		goroutines = "/sched/goroutines:goroutines"
		gomaxprocs = "/sched/gomaxprocs:threads"
		heapAlloc  = "/memory/classes/heap/objects:bytes"
		memTotal   = "/memory/classes/total:bytes"
		heapObj    = "/gc/heap/objects:objects"
		allocBytes = "/gc/heap/allocs:bytes"
		gcCycles   = "/gc/cycles/total:gc-cycles"
		gcGoal     = "/gc/heap/goal:bytes"
		gcPauses   = "/gc/pauses:seconds"
		schedLat   = "/sched/latencies:seconds"
	)
	s := newRuntimeSampler([]string{
		goroutines, gomaxprocs, heapAlloc, memTotal, heapObj,
		allocBytes, gcCycles, gcGoal, gcPauses, schedLat,
	})
	s.refresh() // so a SeriesCount before the first scrape is not misleading
	r.BeforeGather(s.refresh)

	r.NewInfoGauge(Desc{Name: "go_info", Help: "Go runtime this binary was built with."},
		"version", runtime.Version())

	r.NewFuncGauge(Desc{
		Name: "go_goroutines",
		Help: "Goroutines currently alive. A monotonic climb is a leak; the NATS callback path creates one per delivery.",
	}, func() float64 { return s.uint64(goroutines) })

	r.NewFuncGauge(Desc{
		Name: "go_gomaxprocs",
		Help: "Value of GOMAXPROCS.",
	}, func() float64 { return s.uint64(gomaxprocs) })

	r.NewFuncGauge(Desc{
		Name: "go_memstats_heap_alloc_bytes",
		Help: "Bytes of live heap objects.",
	}, func() float64 { return s.uint64(heapAlloc) })

	r.NewFuncGauge(Desc{
		Name: "go_memstats_sys_bytes",
		Help: "Bytes obtained from the OS by the runtime. Compare with process_resident_memory_bytes.",
	}, func() float64 { return s.uint64(memTotal) })

	r.NewFuncGauge(Desc{
		Name: "go_memstats_heap_objects",
		Help: "Number of live heap objects.",
	}, func() float64 { return s.uint64(heapObj) })

	r.NewFuncGauge(Desc{
		Name: "go_memstats_next_gc_bytes",
		Help: "Heap size that will trigger the next collection.",
	}, func() float64 { return s.uint64(gcGoal) })

	r.NewFuncCounter(Desc{
		Name: "go_memstats_alloc_bytes_total",
		Help: "Bytes allocated on the heap since process start, freed or not. Its rate is allocation pressure.",
	}, func() float64 { return s.uint64(allocBytes) })

	r.NewFuncCounter(Desc{
		Name: "go_gc_cycles_total",
		Help: "Completed garbage collection cycles.",
	}, func() float64 { return s.uint64(gcCycles) })

	r.NewFuncHistogram(Desc{
		Name: "go_gc_pauses_seconds",
		Help: "Stop-the-world pause distribution, downsampled from the runtime's own buckets. No _sum: runtime/metrics does not report one.",
	}, runtimeLatencyBounds, func() HistogramSnapshot { return s.histogram(gcPauses, runtimeLatencyBounds) })

	r.NewFuncHistogram(Desc{
		Name: "go_sched_latencies_seconds",
		Help: "Time goroutines spend runnable but not running. Rising tails mean the box is CPU-starved. No _sum: runtime/metrics does not report one.",
	}, runtimeLatencyBounds, func() HistogramSnapshot { return s.histogram(schedLat, runtimeLatencyBounds) })
}

// sortedFloat64sAreSorted is kept next to the bounds it guards so a future edit
// to runtimeLatencyBounds fails loudly at init rather than silently producing a
// non-monotonic histogram.
func init() {
	if !sort.Float64sAreSorted(runtimeLatencyBounds) {
		panic("metrics: runtimeLatencyBounds must be sorted ascending")
	}
}

// RegisterProcess adds the process_* family. It is a no-op on platforms
// without /proc.
func (r *Registry) RegisterProcess() { r.registerProcess() }
