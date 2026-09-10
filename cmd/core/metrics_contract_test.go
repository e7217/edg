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

// legacyCounters maps every counter published to the expvar global registry to
// its Prometheus name. Both surfaces are operator contracts: ADR 0001 tables
// the expvar names and the user guide lists them, so a rename here is a
// breaking change, not a cleanup.
var legacyCounters = map[string]string{
	"edg_core_jetstream_publish_failures":     "edg_core_jetstream_publish_failures_total",
	"edg_core_jetstream_dead_letters":         "edg_core_jetstream_dead_letters_total",
	"edg_core_jetstream_dead_letter_failures": "edg_core_jetstream_dead_letter_failures_total",
	"edg_core_undeclared_assets":              "edg_core_undeclared_assets_total",
	"edg_core_sink_lines_written":             "edg_core_sink_lines_written_total",
	"edg_core_sink_batches_written":           "edg_core_sink_batches_written_total",
	"edg_core_sink_write_failures":            "edg_core_sink_write_failures_total",
	"edg_core_sink_decode_failures":           "edg_core_sink_decode_failures_total",
	"edg_core_adapter_status_invalid":         "edg_core_adapter_status_invalid_total",
	"edg_core_adapter_status_dropped":         "edg_core_adapter_status_dropped_total",
	"edg_core_adapter_stale_total":            "edg_core_adapter_stale_total",
	"edg_core_adapter_probe_recovered":        "edg_core_adapter_probe_recovered_total",
	"edg_core_adapter_probes_skipped":         "edg_core_adapter_probes_skipped_total",
	"edg_core_adapter_stale_averted":          "edg_core_adapter_stale_averted_total",
	"edg_core_adapter_forget_averted":         "edg_core_adapter_forget_averted_total",
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

// One storage, two surfaces: the /metrics value must be the expvar value.
func TestLegacyCountersAgreeAcrossSurfaces(t *testing.T) {
	// Move the counters off zero so an implementation that reads the wrong
	// storage cannot pass by accident.
	for i, name := range sortedKeys(legacyCounters) {
		expvar.Get(name).(*expvar.Int).Add(int64(i + 1))
	}

	samples := parseExposition(t, metrics.Default.Gather())
	for expvarName, promName := range legacyCounters {
		want := expvar.Get(expvarName).(*expvar.Int).Value()
		got, ok := samples[promName]
		if !ok {
			t.Errorf("%s is not exposed on /metrics", promName)
			continue
		}
		if got != strconv.FormatInt(want, 10) {
			t.Errorf("%s = %s on /metrics but %d on /debug/vars", promName, got, want)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseExposition maps unlabelled sample names to their rendered value.
func parseExposition(t *testing.T, out []byte) map[string]string {
	t.Helper()
	samples := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok || strings.Contains(name, "{") {
			continue
		}
		samples[name] = value
	}
	return samples
}
