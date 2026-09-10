package metrics

import (
	"expvar"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// mustPanic asserts that fn panics and that the message mentions want.
func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		v := recover()
		if v == nil {
			t.Fatalf("expected a panic mentioning %q, got none", want)
		}
		if msg := fmt.Sprint(v); !strings.Contains(msg, want) {
			t.Fatalf("panic %q does not mention %q", msg, want)
		}
	}()
	fn()
}

func TestDuplicateNamePanics(t *testing.T) {
	r := NewRegistry()
	d := Desc{Name: "edg_test_dup_total", Help: "h."}
	r.NewCounter(d)
	mustPanic(t, "duplicate", func() { r.NewCounter(d) })
}

// A duplicate must be caught across collector kinds too, not just within one.
func TestDuplicateNameAcrossKindsPanics(t *testing.T) {
	r := NewRegistry()
	r.NewCounter(Desc{Name: "edg_test_dup_total", Help: "h."})
	mustPanic(t, "duplicate", func() {
		r.NewGauge(Desc{Name: "edg_test_dup_total", Help: "h."})
	})
}

func TestInvalidMetricNamePanics(t *testing.T) {
	for _, name := range []string{"", "1_leading_digit", "has-dash", "has space", "has.dot", "€uro"} {
		t.Run(name, func(t *testing.T) {
			mustPanic(t, "invalid metric name", func() {
				NewRegistry().NewCounter(Desc{Name: name, Help: "h."})
			})
		})
	}
}

func TestMissingHelpPanics(t *testing.T) {
	mustPanic(t, "no help text", func() {
		NewRegistry().NewCounter(Desc{Name: "edg_test_nohelp_total"})
	})
}

func TestInvalidLabelNamePanics(t *testing.T) {
	for _, label := range []string{"", "1digit", "has-dash", "has space"} {
		t.Run(label, func(t *testing.T) {
			mustPanic(t, "invalid label name", func() {
				NewRegistry().NewCounterVec(
					Desc{Name: "edg_test_vec_total", Help: "h."}, label, []string{"a"})
			})
		})
	}
}

// The deny list is the last line of defence behind the closed value set: a
// label whose values come from the network must never become a series key.
func TestDeniedLabelPanics(t *testing.T) {
	for _, label := range []string{"asset_id", "id", "name", "subject", "url", "path", "tag"} {
		t.Run(label, func(t *testing.T) {
			mustPanic(t, "unbounded", func() {
				NewRegistry().NewCounterVec(
					Desc{Name: "edg_test_vec_total", Help: "h."}, label, []string{"a"})
			})
		})
		t.Run(label+"/labelled_gauge", func(t *testing.T) {
			mustPanic(t, "unbounded", func() {
				NewRegistry().NewLabelledGauge(
					Desc{Name: "edg_test_lg", Help: "h."}, label,
					func() map[string]float64 { return nil })
			})
		})
	}
}

// adapter_id is the one identifier label EDG allows: it is operator-declared
// node topology, not data-plane content. See ADR 0009.
func TestAdapterIDLabelAllowed(t *testing.T) {
	NewRegistry().NewLabelledGauge(Desc{Name: "edg_test_adapter_up", Help: "h."},
		"adapter_id", func() map[string]float64 { return nil })
}

func TestVecCrossProductBudget(t *testing.T) {
	allowed := make([]string, maxSeriesPerVec) // +1 for "other" tips it over
	for i := range allowed {
		allowed[i] = fmt.Sprintf("v%d", i)
	}
	mustPanic(t, "budget", func() {
		NewRegistry().NewCounterVec(Desc{Name: "edg_test_vec_total", Help: "h."}, "reason", allowed)
	})

	// One fewer must be accepted, so the check is a boundary and not a blanket
	// refusal.
	v := NewRegistry().NewCounterVec(
		Desc{Name: "edg_test_vec_total", Help: "h."}, "reason", allowed[:maxSeriesPerVec-1])
	if got := v.series(); got != maxSeriesPerVec {
		t.Errorf("series() = %d, want %d", got, maxSeriesPerVec)
	}
}

func TestVecDuplicateValuePanics(t *testing.T) {
	mustPanic(t, "twice", func() {
		NewRegistry().NewCounterVec(Desc{Name: "edg_test_vec_total", Help: "h."},
			"reason", []string{"a", "a"})
	})
}

func TestUnknownLabelValueFoldsToOther(t *testing.T) {
	r := NewRegistry()
	v := r.NewCounterVec(Desc{Name: "edg_test_vec_total", Help: "h."},
		"reason", []string{"transport"})

	before := r.SeriesCount()
	for i := 0; i < 1000; i++ {
		v.With(fmt.Sprintf("asset-%d", i)).Inc()
	}
	if after := r.SeriesCount(); after != before {
		t.Fatalf("1000 unknown label values changed the series count %d -> %d", before, after)
	}

	out := string(r.Gather())
	if !strings.Contains(out, `edg_test_vec_total{reason="other"} 1000`) {
		t.Errorf("unknown values did not accumulate in other:\n%s", out)
	}
	if !strings.Contains(out, `edg_core_metrics_label_rejected_total{metric="edg_test_vec_total"} 1000`) {
		t.Errorf("rejections were not reported:\n%s", out)
	}
	if strings.Contains(out, "asset-0") {
		t.Errorf("an unknown label value leaked into the exposition:\n%s", out)
	}
}

func TestKnownLabelValueIsNotCountedAsRejected(t *testing.T) {
	r := NewRegistry()
	v := r.NewCounterVec(Desc{Name: "edg_test_vec_total", Help: "h."},
		"reason", []string{"transport"})
	v.With("transport").Add(3)

	if !strings.Contains(string(r.Gather()),
		`edg_core_metrics_label_rejected_total{metric="edg_test_vec_total"} 0`) {
		t.Errorf("a declared value was counted as a rejection:\n%s", r.Gather())
	}
}

func TestSeriesCountMatchesExposition(t *testing.T) {
	r := goldenRegistry(t)
	want := 0
	for _, line := range strings.Split(strings.TrimRight(string(r.Gather()), "\n"), "\n") {
		if !strings.HasPrefix(line, "#") {
			want++
		}
	}
	if got := r.SeriesCount(); got != want {
		t.Errorf("SeriesCount() = %d, but the exposition has %d sample lines", got, want)
	}
}

// SeriesCount is itself exposed as a gauge; a naive implementation recurses.
func TestSeriesCountDoesNotRecurse(t *testing.T) {
	done := make(chan int, 1)
	go func() { done <- NewRegistry().SeriesCount() }()
	select {
	case n := <-done:
		// Only edg_core_metrics_series: the rejection family has no vecs yet.
		if n != 1 {
			t.Errorf("empty registry reports %d series, want 1", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SeriesCount did not return; it is recursing or deadlocked")
	}
}

// TestConcurrentWritesAndScrapes is the -race workload: writers hammer every
// collector kind while a scraper gathers.
func TestConcurrentWritesAndScrapes(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter(Desc{Name: "edg_test_counter_total", Help: "h."})
	g := r.NewGauge(Desc{Name: "edg_test_gauge", Help: "h."})
	h := r.NewHistogram(Desc{Name: "edg_test_seconds", Help: "h."}, DefaultLatencyBounds)
	v := r.NewCounterVec(Desc{Name: "edg_test_vec_total", Help: "h."},
		"reason", []string{"transport", "http_status"})

	const writers, iterations = 8, 2000
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				c.Inc()
				g.Set(int64(i))
				h.Observe(float64(i%100) / 1000)
				v.With("transport").Inc()
				v.With("unknown").Inc()
			}
		}(w)
	}

	stop := make(chan struct{})
	var scraper sync.WaitGroup
	scraper.Add(1)
	go func() {
		defer scraper.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if len(r.Gather()) == 0 {
					panic("empty scrape")
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	scraper.Wait()

	if got, want := c.Value(), int64(writers*iterations); got != want {
		t.Errorf("counter lost increments: got %d, want %d", got, want)
	}
	if got, want := v.With("transport").Value(), int64(writers*iterations); got != want {
		t.Errorf("vec child lost increments: got %d, want %d", got, want)
	}
	if !strings.Contains(string(r.Gather()),
		fmt.Sprintf("edg_test_seconds_count %d", writers*iterations)) {
		t.Errorf("histogram lost observations")
	}
}

// The point of NewCounterVecLegacy: the historical unlabelled expvar counter
// and the labelled Prometheus family are the same events counted once, so
// their totals cannot drift.
func TestCounterVecLegacyKeepsExpvarTotal(t *testing.T) {
	const expvarName = "edg_test_legacy_vec"
	r := NewRegistry()
	v := r.NewCounterVecLegacy(Desc{Name: "edg_test_legacy_vec_total", Help: "h."},
		"reason", []string{"transport", "http_status"}, expvarName)

	v.With("transport").Add(3)
	v.With("http_status").Inc()
	v.With("not-declared").Add(10) // folds into other, still counted in the total

	legacy, ok := expvar.Get(expvarName).(*expvar.Int)
	if !ok {
		t.Fatalf("%s is %T, want *expvar.Int", expvarName, expvar.Get(expvarName))
	}
	if got := legacy.Value(); got != 14 {
		t.Errorf("expvar total = %d, want 14", got)
	}
	if got := v.Value(); got != 14 {
		t.Errorf("CounterVec.Value() = %d, want 14", got)
	}

	// The children must sum to the same number, or one surface is lying.
	var sum int64
	for _, line := range strings.Split(string(r.Gather()), "\n") {
		if !strings.HasPrefix(line, "edg_test_legacy_vec_total{") {
			continue
		}
		_, value, _ := strings.Cut(line, " ")
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatalf("unparseable sample %q", line)
		}
		sum += n
	}
	if sum != 14 {
		t.Errorf("children sum to %d, but the expvar total is 14", sum)
	}
}

// A vec without a legacy total must not touch the expvar global.
func TestCounterVecWithoutLegacyDoesNotPublish(t *testing.T) {
	r := NewRegistry()
	r.NewCounterVec(Desc{Name: "edg_test_plain_vec_total", Help: "h."},
		"reason", []string{"a"}).With("a").Inc()
	if v := expvar.Get("edg_test_plain_vec_total"); v != nil {
		t.Errorf("NewCounterVec published %T to the expvar global", v)
	}
}
