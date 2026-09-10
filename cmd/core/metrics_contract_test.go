package main

import (
	"encoding/json"
	"expvar"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/e7217/edg/internal/metrics"
)

// legacyCounter records one counter published to the expvar global registry.
// Both surfaces are operator contracts: ADR 0001 tables the expvar names and
// the user guide lists them, so a rename is a breaking change, not a cleanup.
type legacyCounter struct {
	prom string
	// labelled families keep the expvar name as an unlabelled grand total fed
	// by every child, so their /metrics side has no single matching sample.
	labelled bool
}

var legacyCounters = map[string]legacyCounter{
	"edg_core_jetstream_publish_failures":     {prom: "edg_core_jetstream_publish_failures_total"},
	"edg_core_jetstream_dead_letters":         {prom: "edg_core_jetstream_dead_letters_total"},
	"edg_core_jetstream_dead_letter_failures": {prom: "edg_core_jetstream_dead_letter_failures_total", labelled: true},
	"edg_core_undeclared_assets":              {prom: "edg_core_undeclared_assets_total", labelled: true},
	"edg_core_sink_lines_written":             {prom: "edg_core_sink_lines_written_total"},
	"edg_core_sink_batches_written":           {prom: "edg_core_sink_batches_written_total"},
	"edg_core_sink_write_failures":            {prom: "edg_core_sink_write_failures_total", labelled: true},
	"edg_core_sink_decode_failures":           {prom: "edg_core_sink_decode_failures_total"},
	"edg_core_adapter_status_invalid":         {prom: "edg_core_adapter_status_invalid_total"},
	"edg_core_adapter_status_dropped":         {prom: "edg_core_adapter_status_dropped_total"},
	"edg_core_adapter_stale_total":            {prom: "edg_core_adapter_stale_total"},
	"edg_core_adapter_probe_recovered":        {prom: "edg_core_adapter_probe_recovered_total"},
	"edg_core_adapter_probes_skipped":         {prom: "edg_core_adapter_probes_skipped_total"},
	"edg_core_adapter_stale_averted":          {prom: "edg_core_adapter_stale_averted_total"},
	"edg_core_adapter_forget_averted":         {prom: "edg_core_adapter_forget_averted_total"},
}

// TestDebugVarsSurfaceIsExactlyTheLegacyCounters is the guard behind the rule
// in ADR 0009: nothing but those counters may reach the expvar global.
//
// It matters because expvar.Handler dumps every published var and the NATS
// monitoring mux serves it with no authentication. A Func collector published
// there -- a SELECT COUNT(*), a JetStream ConsumerInfo round trip -- would let
// any caller on that interface force that work once per request.
func TestDebugVarsSurfaceIsExactlyTheLegacyCounters(t *testing.T) {
	want := []string{"cmdline", "memstats"}
	for name := range legacyCounters {
		want = append(want, name)
	}
	sort.Strings(want)

	rec := httptest.NewRecorder()
	expvar.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/debug/vars status = %d", rec.Code)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("/debug/vars is not valid JSON: %v\n%s", err, rec.Body.String())
	}
	got := make([]string, 0, len(payload))
	for name := range payload {
		got = append(got, name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("/debug/vars key set changed.\ngot:  %v\nwant: %v\n"+
			"Publishing anything else to expvar exposes it on the unauthenticated "+
			"monitoring port; use metrics.Default.NewCounter instead of NewCounterLegacy.", got, want)
	}
}

// The legacy counters must stay *expvar.Int: existing tests type-assert that
// concrete type, and so may operator tooling.
func TestLegacyCountersAreExpvarInts(t *testing.T) {
	for name := range legacyCounters {
		v := expvar.Get(name)
		if v == nil {
			t.Errorf("%s is no longer published to expvar", name)
			continue
		}
		if _, ok := v.(*expvar.Int); !ok {
			t.Errorf("%s is %T, want *expvar.Int", name, v)
		}
	}
}

// Every legacy counter must appear on /metrics under its Prometheus name.
func TestLegacyCountersAreExposedOnMetrics(t *testing.T) {
	totals := familyTotals(t, metrics.Default.Gather())
	for expvarName, lc := range legacyCounters {
		if _, ok := totals[lc.prom]; !ok {
			t.Errorf("%s (expvar %s) is not exposed on /metrics", lc.prom, expvarName)
		}
	}
}

// One storage, two surfaces: for the unlabelled families the /metrics sample
// is literally the expvar.Int, so writing one must move the other.
//
// The labelled families are excluded on purpose. Writing to their expvar total
// directly, as this test does, bypasses the children -- the sum-equals-total
// invariant only holds for increments that go through the Vec, and that is
// covered where it can be driven properly, in
// internal/metrics.TestCounterVecLegacyKeepsExpvarTotal.
func TestLegacyCountersShareStorage(t *testing.T) {
	names := make([]string, 0, len(legacyCounters))
	for name, lc := range legacyCounters {
		if !lc.labelled {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	// Move each counter to a distinct non-zero value so an implementation that
	// reads the wrong storage cannot pass by coincidence.
	for i, name := range names {
		expvar.Get(name).(*expvar.Int).Add(int64(i + 1))
	}

	totals := familyTotals(t, metrics.Default.Gather())
	for _, name := range names {
		want := expvar.Get(name).(*expvar.Int).Value()
		if got := totals[legacyCounters[name].prom]; got != want {
			t.Errorf("%s = %d on /metrics but %d on /debug/vars",
				legacyCounters[name].prom, got, want)
		}
	}
}

// familyTotals sums every sample of each family, so a labelled family and an
// unlabelled one are compared the same way.
func familyTotals(t *testing.T, out []byte) map[string]int64 {
	t.Helper()
	totals := make(map[string]int64)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue // histogram sums and float gauges are not counters
		}
		totals[name] += n
	}
	return totals
}
