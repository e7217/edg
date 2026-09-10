// Package metrics exposes EDG's internal counters in Prometheus text
// exposition format, using only the standard library.
//
// Why not prometheus/client_golang: ADR 0005 rejected Prometheus remote_write
// specifically because it "adds protobuf + snappy dependencies", and
// client_golang pulls google.golang.org/protobuf in unconditionally --
// Registry.Gather returns []*dto.MetricFamily, so even a pure text exposition
// path links it. Adopting it would have meant amending that ADR, growing the
// direct dependency list from 7 to 8 and the linked module set from 19 to 27,
// and adding ~1.8 MiB to a binary whose whole pitch is that it is one small
// file. What we give up is nearly nothing: runtime/metrics is the same source
// client_golang's GoCollector reads.
//
// The one real risk is getting the exposition format wrong, which is why the
// encoder is golden-tested and CI runs promtool against a live scrape.
package metrics

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
)

// MetricType is the Prometheus type of a metric family.
type MetricType string

const (
	TypeCounter   MetricType = "counter"
	TypeGauge     MetricType = "gauge"
	TypeHistogram MetricType = "histogram"
)

// Desc describes one metric family.
type Desc struct {
	// Name must follow Prometheus naming: counters end in _total, durations in
	// _seconds, sizes in _bytes.
	Name string
	Help string
	Type MetricType
}

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// maxSeriesPerVec bounds a labelled family. The label values are a closed set
// fixed at registration, so exceeding this is a programming error caught by
// tests rather than a cardinality incident found in production.
const maxSeriesPerVec = 256

// labelDenyList are label names whose values are unbounded in this system.
// ADR 0002 and ADR 0005 both warn about cardinality, and these metrics land in
// the same VictoriaMetrics instance as the data plane, so an asset id in a
// label would multiply the series count by the size of the plant.
//
// adapter_id is deliberately absent: it is operator-declared node topology,
// the same class of identifier as "instance", and it changes only on
// deployment. asset_id arrives from the network over an open wire contract.
var labelDenyList = map[string]bool{
	"asset_id": true, "id": true, "name": true,
	"subject": true, "url": true, "path": true, "tag": true,
}

// collector produces exposition lines for one family.
type collector interface {
	desc() Desc
	// write appends the sample lines (not HELP/TYPE) for this family.
	write(buf *[]byte, name string)
	// series reports how many time series this collector currently exposes.
	series() int
}

// Registry owns a set of metric families.
//
// Registration happens during process start-up and panics on programming
// errors (bad name, duplicate, oversized label set) so that a cardinality
// mistake is a failed test rather than a production incident.
type Registry struct {
	mu         sync.RWMutex
	collectors []collector
	names      map[string]bool

	rejections *rejectionCollector
	preGather  []func()
}

// NewRegistry builds a registry pre-loaded with its own self-observation
// metrics.
func NewRegistry() *Registry {
	r := &Registry{names: make(map[string]bool)}
	r.rejections = &rejectionCollector{d: Desc{
		Name: "edg_core_metrics_label_rejected_total",
		Help: "Label values rejected because they are not in the metric's declared value set.",
		Type: TypeCounter,
	}}
	r.add(r.rejections.d, r.rejections)
	r.NewFuncGauge(Desc{
		Name: "edg_core_metrics_series",
		Help: "Time series currently exposed by this process.",
	}, func() float64 { return float64(r.SeriesCount()) })
	return r
}

// BeforeGather registers a hook run once at the start of every Gather.
//
// It exists so that a subsystem read as several metrics -- runtime/metrics,
// /proc, a pool snapshot -- is sampled once per scrape instead of once per
// series, which is both cheaper and internally consistent: every derived
// metric then describes the same instant.
func (r *Registry) BeforeGather(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preGather = append(r.preGather, fn)
}

func (r *Registry) add(d Desc, c collector) {
	if !nameRe.MatchString(d.Name) {
		panic(fmt.Sprintf("metrics: invalid metric name %q", d.Name))
	}
	if d.Help == "" {
		panic(fmt.Sprintf("metrics: metric %q has no help text", d.Name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[d.Name] {
		panic(fmt.Sprintf("metrics: duplicate metric name %q", d.Name))
	}
	r.names[d.Name] = true
	r.collectors = append(r.collectors, c)
}

// Gather renders the whole registry in text exposition format 0.0.4.
//
// Families are emitted in name order so the output is byte-stable, which is
// what makes golden tests meaningful.
func (r *Registry) Gather() []byte {
	r.mu.RLock()
	collectors := append([]collector(nil), r.collectors...)
	hooks := append([]func(){}, r.preGather...)
	r.mu.RUnlock()

	for _, fn := range hooks {
		fn()
	}

	sort.Slice(collectors, func(i, j int) bool {
		return collectors[i].desc().Name < collectors[j].desc().Name
	})

	buf := make([]byte, 0, 16<<10)
	for _, c := range collectors {
		d := c.desc()
		buf = append(buf, "# HELP "...)
		buf = append(buf, d.Name...)
		buf = append(buf, ' ')
		buf = appendEscapedHelp(buf, d.Help)
		buf = append(buf, '\n')
		buf = append(buf, "# TYPE "...)
		buf = append(buf, d.Name...)
		buf = append(buf, ' ')
		buf = append(buf, d.Type...)
		buf = append(buf, '\n')
		c.write(&buf, d.Name)
	}
	return buf
}

// SeriesCount reports how many time series the registry currently exposes. It
// backs edg_core_metrics_series and the cardinality budget test.
func (r *Registry) SeriesCount() int {
	r.mu.RLock()
	collectors := append([]collector(nil), r.collectors...)
	r.mu.RUnlock()
	n := 0
	for _, c := range collectors {
		n += c.series()
	}
	return n
}

// rejectionCollector reports, per labelled family, how many times a label value
// outside the declared set was folded into "other".
//
// It keeps its own slice rather than walking the registry so that counting
// series never re-enters the registry lock.
type rejectionCollector struct {
	d     Desc
	mu    sync.RWMutex
	vecs  []*CounterVec
	vecs2 []*CounterVec2
}

func (c *rejectionCollector) track(v *CounterVec) {
	c.mu.Lock()
	c.vecs = append(c.vecs, v)
	c.mu.Unlock()
}

func (c *rejectionCollector) track2(v *CounterVec2) {
	c.mu.Lock()
	c.vecs2 = append(c.vecs2, v)
	c.mu.Unlock()
}

func (c *rejectionCollector) desc() Desc { return c.d }

func (c *rejectionCollector) series() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.vecs) + len(c.vecs2)
}

func (c *rejectionCollector) write(buf *[]byte, name string) {
	c.mu.RLock()
	type entry struct {
		name     string
		rejected int64
	}
	entries := make([]entry, 0, len(c.vecs)+len(c.vecs2))
	for _, v := range c.vecs {
		entries = append(entries, entry{v.d.Name, v.rejected.Value()})
	}
	for _, v := range c.vecs2 {
		entries = append(entries, entry{v.d.Name, v.rejected.Value()})
	}
	c.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	for _, e := range entries {
		writeLabelledSample(buf, name, "metric", e.name, e.rejected)
	}
}

// Default is the process-wide registry every EDG metric registers with.
//
// A global is not a stylistic choice here: the legacy counters publish to the
// expvar global registry, and expvar.Publish panics on a duplicate name, so
// each of those declarations must run exactly once per process. Tests that
// need isolation build their own registry with NewRegistry.
var Default = NewRegistry()
