package metrics

import (
	"bytes"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

// goldenRegistry builds a registry covering every collector kind and every
// escaping edge case, so that one golden file is also the promtool input.
func goldenRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()

	c := r.NewCounter(Desc{
		Name: "edg_test_frames_total",
		Help: `A counter whose help has a backslash \ and a "quote" and a
newline.`,
	})
	c.Add(7)

	g := r.NewGauge(Desc{Name: "edg_test_clock_offset_seconds", Help: "A value that can go negative."})
	g.Set(-3)

	vec := r.NewCounterVec(Desc{
		Name: "edg_test_labelled_total",
		Help: "A labelled counter over a closed value set.",
	}, "reason", []string{"transport", "http_status"})
	vec.With("transport").Add(2)
	vec.With("http_status").Add(5)
	vec.With(`weird\value"with
newline`).Inc() // folds into other, and exercises label escaping via rejections

	h := r.NewHistogram(Desc{
		Name: "edg_test_seconds",
		Help: "A latency histogram. Buckets are provisional.",
	}, []float64{0.0005, 0.01, 1})
	for _, v := range []float64{0.0001, 0.002, 0.5, 30} {
		h.Observe(v)
	}

	r.NewFuncGauge(Desc{Name: "edg_test_load_factor", Help: "A value read at scrape time."},
		func() float64 { return 1.5 })

	r.NewLabelledGauge(Desc{Name: "edg_test_state", Help: "A state set."},
		"state", func() map[string]float64 {
			return map[string]float64{"running": 1, "stopped": 0}
		})

	return r
}

func TestGatherGolden(t *testing.T) {
	got := goldenRegistry(t).Gather()
	path := filepath.Join("testdata", "metrics_golden.txt")

	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run: go test ./internal/metrics -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("exposition mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestGatherIsStable guards the ordering promise the golden test depends on:
// two gathers of the same registry must be byte-identical, and map iteration
// order must not leak into the output.
func TestGatherIsStable(t *testing.T) {
	r := goldenRegistry(t)
	first := r.Gather()
	for i := 0; i < 20; i++ {
		if !bytes.Equal(first, r.Gather()) {
			t.Fatalf("gather %d differed from the first", i)
		}
	}
}

func TestAppendEscapedHelp(t *testing.T) {
	// The exposition format escapes only backslash and newline in HELP; a
	// double quote is literal there, unlike in a label value.
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{`back\slash`, `back\\slash`},
		{"two\nlines", `two\nlines`},
		{`a "quoted" word`, `a "quoted" word`},
		{"\\\n", `\\\n`},
		{"", ""},
	}
	for _, tc := range tests {
		if got := string(appendEscapedHelp(nil, tc.in)); got != tc.want {
			t.Errorf("appendEscapedHelp(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAppendEscapedLabelValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{`back\slash`, `back\\slash`},
		{`a "quoted" word`, `a \"quoted\" word`},
		{"two\nlines", `two\nlines`},
		{"\\\"\n", `\\\"\n`},
		{"", ""},
		{"utf8 ✓ 한글", "utf8 ✓ 한글"},
	}
	for _, tc := range tests {
		if got := string(appendEscapedLabelValue(nil, tc.in)); got != tc.want {
			t.Errorf("appendEscapedLabelValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAppendFloat(t *testing.T) {
	// Kept as vars so the compiler cannot constant-fold 0.1+0.2 into an exact 0.3.
	pointOne, pointTwo := 0.1, 0.2
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{1.5, "1.5"},
		{0.0005, "0.0005"},
		{pointOne + pointTwo, "0.30000000000000004"},
		{math.Inf(1), "+Inf"},
		{math.Inf(-1), "-Inf"},
		{math.NaN(), "NaN"},
		{1e15, "1e+15"}, // past the integer fast path, so exponent form
		{1e14, "100000000000000"},
	}
	for _, tc := range tests {
		if got := string(appendFloat(nil, tc.in)); got != tc.want {
			t.Errorf("appendFloat(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHistogramCumulativeAndInfEqualsCount(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram(Desc{Name: "edg_test_seconds", Help: "h."},
		[]float64{0.001, 0.01, 0.1})
	// One sample per bucket plus one over the top bound.
	for _, v := range []float64{0.0005, 0.005, 0.05, 5} {
		h.Observe(v)
	}

	lines := sampleLines(t, r.Gather(), "edg_test_seconds")
	want := []string{
		`edg_test_seconds_bucket{le="0.001"} 1`,
		`edg_test_seconds_bucket{le="0.01"} 2`,
		`edg_test_seconds_bucket{le="0.1"} 3`,
		`edg_test_seconds_bucket{le="+Inf"} 4`,
		`edg_test_seconds_sum 5.0555`,
		`edg_test_seconds_count 4`,
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d sample lines, want %d:\n%s", len(lines), len(want), strings.Join(lines, "\n"))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

// TestHistogramBoundIsInclusive pins the le semantics: a sample exactly on a
// bound belongs to that bucket, not the next one.
func TestHistogramBoundIsInclusive(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram(Desc{Name: "edg_test_seconds", Help: "h."}, []float64{0.001, 0.01})
	h.Observe(0.001)

	lines := sampleLines(t, r.Gather(), "edg_test_seconds")
	if lines[0] != `edg_test_seconds_bucket{le="0.001"} 1` {
		t.Errorf("sample on the bound landed in the wrong bucket: %q", lines[0])
	}
}

func TestHistogramDropsNaN(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram(Desc{Name: "edg_test_seconds", Help: "h."}, []float64{1})
	h.Observe(math.NaN())
	h.Observe(0.5)

	out := string(r.Gather())
	if strings.Contains(out, "NaN") {
		t.Fatalf("NaN reached the exposition, poisoning _sum forever:\n%s", out)
	}
	lines := sampleLines(t, r.Gather(), "edg_test_seconds")
	if lines[len(lines)-1] != "edg_test_seconds_count 1" {
		t.Errorf("NaN was counted: %q", lines[len(lines)-1])
	}
}

func TestHistogramRejectsBadBounds(t *testing.T) {
	tests := map[string][]float64{
		"unsorted": {1, 0.5},
		"infinite": {1, math.Inf(1)},
		"nan":      {math.NaN()},
		"empty":    {},
	}
	for name, bounds := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("NewHistogram(%v) did not panic", bounds)
				}
			}()
			NewRegistry().NewHistogram(Desc{Name: "edg_test_seconds", Help: "h."}, bounds)
		})
	}
}

// TestFamilyBlocksAreContiguous asserts the structural rule a Prometheus
// parser relies on: HELP, then TYPE, then that family's samples, with no other
// family interleaved and no family appearing twice.
func TestFamilyBlocksAreContiguous(t *testing.T) {
	out := string(goldenRegistry(t).Gather())

	var seen []string
	inFamily := ""
	sawType := false
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			name := strings.Fields(line)[2]
			for _, s := range seen {
				if s == name {
					t.Fatalf("family %s declared twice", name)
				}
			}
			seen = append(seen, name)
			inFamily, sawType = name, false
		case strings.HasPrefix(line, "# TYPE "):
			name := strings.Fields(line)[2]
			if name != inFamily {
				t.Fatalf("TYPE %s does not follow HELP %s", name, inFamily)
			}
			sawType = true
		default:
			if !sawType {
				t.Fatalf("sample %q appeared before its TYPE line", line)
			}
			metric := line
			if i := strings.IndexAny(metric, "{ "); i >= 0 {
				metric = metric[:i]
			}
			if !strings.HasPrefix(metric, inFamily) {
				t.Fatalf("sample %q does not belong to family %s", line, inFamily)
			}
		}
	}

	sorted := append([]string(nil), seen...)
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Fatalf("families are not name-ordered: %q before %q", sorted[i-1], sorted[i])
		}
	}
}

// sampleLines returns the sample lines of one family, in order.
func sampleLines(t *testing.T, out []byte, family string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, family) {
			got = append(got, line)
		}
	}
	if len(got) == 0 {
		t.Fatalf("family %s produced no samples:\n%s", family, out)
	}
	return got
}
