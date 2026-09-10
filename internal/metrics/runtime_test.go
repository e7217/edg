package metrics

import (
	"math"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
)

func TestDownsamplePreservesTotalCount(t *testing.T) {
	// A real runtime histogram, so the bucket layout is the one we ship
	// against rather than a hand-made approximation of it.
	runtime.GC()
	sample := []metrics.Sample{{Name: "/gc/pauses:seconds"}}
	metrics.Read(sample)
	h := sample[0].Value.Float64Histogram()

	var want uint64
	for _, c := range h.Counts {
		want += c
	}
	if want == 0 {
		t.Skip("no GC pauses recorded yet")
	}

	got := downsample(h, runtimeLatencyBounds)
	if got.Count != want {
		t.Errorf("_count = %d, want %d: downsampling lost samples", got.Count, want)
	}
	if len(got.Cumulative) != len(runtimeLatencyBounds) {
		t.Fatalf("got %d buckets, want %d", len(got.Cumulative), len(runtimeLatencyBounds))
	}
	for i := 1; i < len(got.Cumulative); i++ {
		if got.Cumulative[i] < got.Cumulative[i-1] {
			t.Errorf("bucket %d (%v) is below bucket %d: not cumulative", i, got.Cumulative[i], i-1)
		}
	}
	if last := got.Cumulative[len(got.Cumulative)-1]; last > got.Count {
		t.Errorf("largest bucket %d exceeds _count %d", last, got.Count)
	}
}

// Every sample must land at or below its true bound: downsampling may be
// conservative, never optimistic, or a latency SLO reads better than reality.
func TestDownsampleIsConservative(t *testing.T) {
	h := &metrics.Float64Histogram{
		// Buckets [-Inf,0.5) [0.5,2) [2,7) [7,+Inf)
		Buckets: []float64{math.Inf(-1), 0.5, 2, 7, math.Inf(1)},
		Counts:  []uint64{3, 5, 2, 1},
	}
	bounds := []float64{0.5, 1, 2, 10}
	got := downsample(h, bounds)

	// le=0.5   : only bucket 0 is entirely <= 0.5             -> 3
	// le=1     : bucket 1 spans [0.5,2), not entirely <= 1    -> 3
	// le=2     : bucket 1 ends at 2, so it now qualifies      -> 8
	// le=10    : bucket 2 ends at 7                           -> 10
	want := []uint64{3, 3, 8, 10}
	for i := range want {
		if got.Cumulative[i] != want[i] {
			t.Errorf("le=%v: got %d, want %d", bounds[i], got.Cumulative[i], want[i])
		}
	}
	if got.Count != 11 {
		t.Errorf("_count = %d, want 11", got.Count)
	}
	if got.HasSum {
		t.Error("runtime histograms have no sum; HasSum must be false")
	}
}

func TestDownsampleEmpty(t *testing.T) {
	got := downsample(nil, runtimeLatencyBounds)
	if got.Count != 0 || len(got.Cumulative) != len(runtimeLatencyBounds) {
		t.Errorf("nil histogram produced %+v", got)
	}
}

// An unknown metric name must be skipped, not panic: runtime/metrics names come
// and go between Go releases and a scrape must survive the toolchain moving.
func TestRuntimeSamplerSkipsUnknownNames(t *testing.T) {
	s := newRuntimeSampler([]string{"/sched/goroutines:goroutines", "/not/a/real:metric"})
	s.refresh()
	if got := s.uint64("/sched/goroutines:goroutines"); got <= 0 {
		t.Errorf("goroutines = %v, want > 0", got)
	}
	if got := s.uint64("/not/a/real:metric"); got != 0 {
		t.Errorf("unknown metric returned %v, want 0", got)
	}
}

func TestRegisterRuntimeExposesExpectedFamilies(t *testing.T) {
	r := NewRegistry()
	r.RegisterRuntime()
	out := string(r.Gather())

	for _, name := range []string{
		"go_info{version=",
		"go_goroutines ",
		"go_gomaxprocs ",
		"go_memstats_heap_alloc_bytes ",
		"go_memstats_sys_bytes ",
		"go_memstats_heap_objects ",
		"go_memstats_next_gc_bytes ",
		"go_memstats_alloc_bytes_total ",
		"go_gc_cycles_total ",
		`go_gc_pauses_seconds_bucket{le="+Inf"}`,
		"go_gc_pauses_seconds_count ",
		`go_sched_latencies_seconds_bucket{le="+Inf"}`,
	} {
		if !strings.Contains(out, name) {
			t.Errorf("%s missing from the exposition", name)
		}
	}
	if strings.Contains(out, "go_gc_pauses_seconds_sum") {
		t.Error("a _sum was invented for a runtime histogram that reports none")
	}
	if !strings.Contains(out, "go_info{version=\""+runtime.Version()+"\"} 1") {
		t.Errorf("go_info does not carry the running Go version:\n%s", out)
	}
}

// The whole point of BeforeGather: one metrics.Read per scrape feeding every
// derived series, so they all describe the same instant.
func TestRuntimeSamplerRefreshesOnGather(t *testing.T) {
	r := NewRegistry()
	r.RegisterRuntime()

	before := valueOf(t, r.Gather(), "go_goroutines")
	stop := make(chan struct{})
	const spawned = 25
	for i := 0; i < spawned; i++ {
		go func() { <-stop }()
	}
	defer close(stop)

	// Goroutines start asynchronously; retry until the sample moves or we give
	// up, so the test is not a race on the scheduler.
	var after float64
	for i := 0; i < 100; i++ {
		after = valueOf(t, r.Gather(), "go_goroutines")
		if after > before {
			return
		}
		runtime.Gosched()
	}
	t.Errorf("go_goroutines stayed at %v after starting %d goroutines: the sample is not refreshed per scrape", after, spawned)
}

func valueOf(t *testing.T, out []byte, name string) float64 {
	t.Helper()
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, name+" "); ok {
			v, err := strconv.ParseFloat(rest, 64)
			if err != nil {
				t.Fatalf("%s = %q: %v", name, rest, err)
			}
			return v
		}
	}
	t.Fatalf("%s not found in:\n%s", name, out)
	return 0
}
