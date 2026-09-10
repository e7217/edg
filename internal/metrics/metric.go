package metrics

import (
	"expvar"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// DefaultLatencyBounds are the bucket bounds for the request-shaped latencies
// in this process (data handling, sink writes, ancestor lookups).
//
// They are provisional: nothing here has been measured against a real plant
// yet, and a wrong bucket layout looks precise while being wrong. Expect one
// revision after the first field deployment.
var DefaultLatencyBounds = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5,
}

// Counter is a monotonically increasing value.
//
// It stores through *expvar.Int rather than its own atomic so that the eight
// counters EDG already publishes keep their exact name and concrete type:
// existing call sites, existing tests that type-assert (*expvar.Int), and the
// bytes served at /debug/vars are all unchanged.
type Counter struct {
	d Desc
	v *expvar.Int
}

// Add increments the counter.
func (c *Counter) Add(delta int64) { c.v.Add(delta) }

// Inc increments by one.
func (c *Counter) Inc() { c.v.Add(1) }

// Value returns the current value.
func (c *Counter) Value() int64 { return c.v.Value() }

func (c *Counter) desc() Desc  { return c.d }
func (c *Counter) series() int { return 1 }

func (c *Counter) write(buf *[]byte, name string) {
	*buf = append(*buf, name...)
	*buf = append(*buf, ' ')
	*buf = appendInt(*buf, c.v.Value())
	*buf = append(*buf, '\n')
}

// NewCounter registers an unlabelled counter. It is NOT published to the
// expvar global registry: expvar.Handler dumps everything registered there, so
// publishing new metrics would silently add them to the unauthenticated
// /debug/vars surface as well.
func (r *Registry) NewCounter(d Desc) *Counter {
	d.Type = TypeCounter
	c := &Counter{d: d, v: new(expvar.Int)}
	r.add(d, c)
	return c
}

// NewCounterLegacy registers a counter that is also published to expvar under
// its historical name.
//
// Reserved for the eight counters ADR 0001 and the user guide already
// document. Anything else must use NewCounter -- see the /debug/vars note on
// NewCounter.
func (r *Registry) NewCounterLegacy(d Desc, expvarName string) *Counter {
	d.Type = TypeCounter
	c := &Counter{d: d, v: expvar.NewInt(expvarName)}
	r.add(d, c)
	return c
}

// CounterVec is a counter family over one label with a closed value set.
//
// Every child is materialised at registration, so With is a plain map read with
// no lock and no allocation, and the series count is known before the process
// serves a single request.
type CounterVec struct {
	d        Desc
	label    string
	children map[string]*Counter
	order    []string
	other    *Counter
	rejected *expvar.Int // surfaced via the registry's rejectionCollector
}

// NewCounterVec registers a labelled counter over a fixed set of values.
//
// The closed set is the point: an open API like WithLabelValues(anyString)
// makes an unbounded series set a one-line mistake away, and these metrics land
// in the same VictoriaMetrics instance as the plant's telemetry.
func (r *Registry) NewCounterVec(d Desc, label string, allowed []string) *CounterVec {
	d.Type = TypeCounter
	checkLabel(d.Name, label)
	if len(allowed)+1 > maxSeriesPerVec {
		panic(fmt.Sprintf("metrics: %s would create %d series, over the %d budget",
			d.Name, len(allowed)+1, maxSeriesPerVec))
	}

	v := &CounterVec{
		d:        d,
		label:    label,
		children: make(map[string]*Counter, len(allowed)+1),
		order:    append([]string(nil), allowed...),
		other:    &Counter{d: d, v: new(expvar.Int)},
		rejected: new(expvar.Int),
	}
	for _, value := range allowed {
		if _, dup := v.children[value]; dup {
			panic(fmt.Sprintf("metrics: %s declares label value %q twice", d.Name, value))
		}
		v.children[value] = &Counter{d: d, v: new(expvar.Int)}
	}
	sort.Strings(v.order)
	r.add(d, v)
	r.rejections.track(v)
	return v
}

// With returns the child for a label value. An unregistered value folds into
// "other" rather than creating a series, and is counted so the mistake is
// visible instead of silent.
func (v *CounterVec) With(value string) *Counter {
	if c, ok := v.children[value]; ok {
		return c
	}
	v.rejected.Add(1)
	return v.other
}

func (v *CounterVec) desc() Desc  { return v.d }
func (v *CounterVec) series() int { return len(v.order) + 1 }

func (v *CounterVec) write(buf *[]byte, name string) {
	for _, value := range v.order {
		writeLabelledSample(buf, name, v.label, value, v.children[value].Value())
	}
	writeLabelledSample(buf, name, v.label, "other", v.other.Value())
}

// Gauge is a value that can go up and down.
type Gauge struct {
	d Desc
	v atomic.Int64
}

// NewGauge registers a gauge.
func (r *Registry) NewGauge(d Desc) *Gauge {
	d.Type = TypeGauge
	g := &Gauge{d: d}
	r.add(d, g)
	return g
}

func (g *Gauge) Set(v int64)  { g.v.Store(v) }
func (g *Gauge) Add(v int64)  { g.v.Add(v) }
func (g *Gauge) Inc()         { g.v.Add(1) }
func (g *Gauge) Dec()         { g.v.Add(-1) }
func (g *Gauge) Value() int64 { return g.v.Load() }

func (g *Gauge) desc() Desc  { return g.d }
func (g *Gauge) series() int { return 1 }

func (g *Gauge) write(buf *[]byte, name string) {
	*buf = append(*buf, name...)
	*buf = append(*buf, ' ')
	*buf = appendInt(*buf, g.v.Load())
	*buf = append(*buf, '\n')
}

// Histogram is a cumulative histogram with fixed bucket bounds.
type Histogram struct {
	d      Desc
	bounds []float64

	mu     sync.Mutex
	counts []uint64
	sum    float64
	total  uint64
}

// NewHistogram registers a histogram. Bounds must be sorted ascending and must
// not include +Inf, which is implicit.
func (r *Registry) NewHistogram(d Desc, bounds []float64) *Histogram {
	d.Type = TypeHistogram
	if len(bounds) == 0 {
		panic(fmt.Sprintf("metrics: histogram %s has no bounds", d.Name))
	}
	if !sort.Float64sAreSorted(bounds) {
		panic(fmt.Sprintf("metrics: histogram %s bounds must be sorted ascending", d.Name))
	}
	for _, b := range bounds {
		if math.IsInf(b, 0) || math.IsNaN(b) {
			panic(fmt.Sprintf("metrics: histogram %s bounds must be finite; +Inf is implicit", d.Name))
		}
	}
	if len(bounds)+3 > maxSeriesPerVec {
		panic(fmt.Sprintf("metrics: histogram %s would create %d series, over the %d budget",
			d.Name, len(bounds)+3, maxSeriesPerVec))
	}
	h := &Histogram{
		d:      d,
		bounds: append([]float64(nil), bounds...),
		counts: make([]uint64, len(bounds)),
	}
	r.add(d, h)
	return h
}

// Observe records one sample. NaN is dropped: it would poison _sum for every
// future scrape, and a single bad duration must not destroy the metric.
func (h *Histogram) Observe(v float64) {
	if math.IsNaN(v) {
		return
	}
	// sort.SearchFloat64s returns the first index whose bound is >= v, which is
	// exactly the bucket a sample belongs to under Prometheus' le semantics.
	i := sort.SearchFloat64s(h.bounds, v)
	h.mu.Lock()
	if i < len(h.counts) {
		h.counts[i]++
	}
	h.sum += v
	h.total++
	h.mu.Unlock()
}

func (h *Histogram) desc() Desc { return h.d }

// series counts the buckets plus +Inf, _sum and _count.
func (h *Histogram) series() int { return len(h.bounds) + 3 }

func (h *Histogram) write(buf *[]byte, name string) {
	h.mu.Lock()
	counts := append([]uint64(nil), h.counts...)
	sum, total := h.sum, h.total
	h.mu.Unlock()

	// Buckets are cumulative: each reports everything at or below its bound.
	var cumulative uint64
	for i, bound := range h.bounds {
		cumulative += counts[i]
		*buf = append(*buf, name...)
		*buf = append(*buf, `_bucket{le="`...)
		*buf = appendFloat(*buf, bound)
		*buf = append(*buf, '"', '}', ' ')
		*buf = appendUint(*buf, cumulative)
		*buf = append(*buf, '\n')
	}
	// +Inf must equal _count, or the exposition is malformed.
	*buf = append(*buf, name...)
	*buf = append(*buf, `_bucket{le="+Inf"} `...)
	*buf = appendUint(*buf, total)
	*buf = append(*buf, '\n')

	*buf = append(*buf, name...)
	*buf = append(*buf, "_sum "...)
	*buf = appendFloat(*buf, sum)
	*buf = append(*buf, '\n')

	*buf = append(*buf, name...)
	*buf = append(*buf, "_count "...)
	*buf = appendUint(*buf, total)
	*buf = append(*buf, '\n')
}

// FuncGauge reads its value on demand. Used for numbers that already live
// somewhere else (pool stats, runtime metrics, NATS internals) rather than
// being counted by EDG.
type FuncGauge struct {
	d  Desc
	fn func() float64
}

// NewFuncGauge registers a gauge whose value is computed at scrape time.
//
// The function must be cheap and must not block: it runs while the response is
// being written. Anything doing I/O belongs behind a cached collector.
func (r *Registry) NewFuncGauge(d Desc, fn func() float64) *FuncGauge {
	d.Type = TypeGauge
	g := &FuncGauge{d: d, fn: fn}
	r.add(d, g)
	return g
}

// NewFuncCounter is NewFuncGauge for a value that only ever increases.
func (r *Registry) NewFuncCounter(d Desc, fn func() float64) *FuncGauge {
	d.Type = TypeCounter
	g := &FuncGauge{d: d, fn: fn}
	r.add(d, g)
	return g
}

func (g *FuncGauge) desc() Desc  { return g.d }
func (g *FuncGauge) series() int { return 1 }

func (g *FuncGauge) write(buf *[]byte, name string) {
	*buf = append(*buf, name...)
	*buf = append(*buf, ' ')
	*buf = appendFloat(*buf, g.fn())
	*buf = append(*buf, '\n')
}

// LabelledGauge exposes a label-to-value map computed on demand, for families
// whose series set is known to the producer but changes at runtime -- adapter
// state sets, aggregate counts by state.
type LabelledGauge struct {
	d     Desc
	label string
	fn    func() map[string]float64
}

// NewLabelledGauge registers a gauge family whose series are produced by fn.
//
// fn is responsible for keeping its key set bounded; the producers that use it
// (the adapter collector) cap and evict before returning.
func (r *Registry) NewLabelledGauge(d Desc, label string, fn func() map[string]float64) *LabelledGauge {
	d.Type = TypeGauge
	checkLabel(d.Name, label)
	g := &LabelledGauge{d: d, label: label, fn: fn}
	r.add(d, g)
	return g
}

func (g *LabelledGauge) desc() Desc  { return g.d }
func (g *LabelledGauge) series() int { return len(g.fn()) }

func (g *LabelledGauge) write(buf *[]byte, name string) {
	values := g.fn()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		*buf = append(*buf, name...)
		*buf = append(*buf, '{')
		*buf = append(*buf, g.label...)
		*buf = append(*buf, '=', '"')
		*buf = appendEscapedLabelValue(*buf, k)
		*buf = append(*buf, '"', '}', ' ')
		*buf = appendFloat(*buf, values[k])
		*buf = append(*buf, '\n')
	}
}

func checkLabel(metric, label string) {
	if !labelRe.MatchString(label) {
		panic(fmt.Sprintf("metrics: invalid label name %q on %s", label, metric))
	}
	if labelDenyList[label] {
		panic(fmt.Sprintf("metrics: label %q is unbounded in this system; see the cardinality note in ADR 0002 (metric %s)", label, metric))
	}
}

// HistogramSnapshot is a cumulative histogram sampled from somewhere that
// already keeps one, such as runtime/metrics.
type HistogramSnapshot struct {
	// Cumulative[i] is the number of samples <= the collector's bounds[i]. It
	// must be non-decreasing and every entry must be <= Count.
	Cumulative []uint64
	Count      uint64
	// Sum is exposed as _sum only when HasSum is set. runtime/metrics reports
	// no sum for its histograms, and an estimate from bucket midpoints would
	// look authoritative while being wrong, so those families omit it -- which
	// the exposition format permits.
	Sum    float64
	HasSum bool
}

// FuncHistogram exposes a cumulative histogram owned by another subsystem.
type FuncHistogram struct {
	d      Desc
	bounds []float64
	fn     func() HistogramSnapshot
}

// NewFuncHistogram registers a histogram read at scrape time.
func (r *Registry) NewFuncHistogram(d Desc, bounds []float64, fn func() HistogramSnapshot) *FuncHistogram {
	d.Type = TypeHistogram
	if len(bounds) == 0 || !sort.Float64sAreSorted(bounds) {
		panic(fmt.Sprintf("metrics: histogram %s needs non-empty ascending bounds", d.Name))
	}
	h := &FuncHistogram{d: d, bounds: append([]float64(nil), bounds...), fn: fn}
	r.add(d, h)
	return h
}

func (h *FuncHistogram) desc() Desc { return h.d }

func (h *FuncHistogram) series() int {
	n := len(h.bounds) + 2 // buckets, +Inf, _count
	if h.fn().HasSum {
		n++
	}
	return n
}

func (h *FuncHistogram) write(buf *[]byte, name string) {
	snap := h.fn()
	for i, bound := range h.bounds {
		var c uint64
		if i < len(snap.Cumulative) {
			c = snap.Cumulative[i]
		}
		*buf = append(*buf, name...)
		*buf = append(*buf, `_bucket{le="`...)
		*buf = appendFloat(*buf, bound)
		*buf = append(*buf, '"', '}', ' ')
		*buf = appendUint(*buf, c)
		*buf = append(*buf, '\n')
	}
	*buf = append(*buf, name...)
	*buf = append(*buf, `_bucket{le="+Inf"} `...)
	*buf = appendUint(*buf, snap.Count)
	*buf = append(*buf, '\n')

	if snap.HasSum {
		*buf = append(*buf, name...)
		*buf = append(*buf, "_sum "...)
		*buf = appendFloat(*buf, snap.Sum)
		*buf = append(*buf, '\n')
	}

	*buf = append(*buf, name...)
	*buf = append(*buf, "_count "...)
	*buf = appendUint(*buf, snap.Count)
	*buf = append(*buf, '\n')
}

// InfoGauge is a constant-1 gauge carrying build or version labels, the
// standard way to make static strings queryable in a TSDB.
type InfoGauge struct {
	d      Desc
	labels []string // alternating name, value
}

// NewInfoGauge registers an info metric. labels alternate name and value and
// are fixed for the life of the process.
func (r *Registry) NewInfoGauge(d Desc, labels ...string) *InfoGauge {
	d.Type = TypeGauge
	if len(labels)%2 != 0 {
		panic(fmt.Sprintf("metrics: %s got an odd number of label arguments", d.Name))
	}
	for i := 0; i < len(labels); i += 2 {
		checkLabel(d.Name, labels[i])
	}
	g := &InfoGauge{d: d, labels: append([]string(nil), labels...)}
	r.add(d, g)
	return g
}

func (g *InfoGauge) desc() Desc  { return g.d }
func (g *InfoGauge) series() int { return 1 }

func (g *InfoGauge) write(buf *[]byte, name string) {
	*buf = append(*buf, name...)
	*buf = append(*buf, '{')
	for i := 0; i < len(g.labels); i += 2 {
		if i > 0 {
			*buf = append(*buf, ',')
		}
		*buf = append(*buf, g.labels[i]...)
		*buf = append(*buf, '=', '"')
		*buf = appendEscapedLabelValue(*buf, g.labels[i+1])
		*buf = append(*buf, '"')
	}
	*buf = append(*buf, '}', ' ', '1', '\n')
}
