// Command edg-loadgen measures what an EDG node can sustain.
//
// It simulates adapters with the Go SDK -- the same publish path a real
// adapter takes -- and measures, per run:
//
//   - loss: readings sent versus readings that reached platform.data.validated;
//   - latency: from the reading's timestamp (set at send) to its arrival on
//     platform.data.validated, which covers the adapter-to-core hop, the data
//     contract, enrichment and the JetStream publish ack;
//   - whether storage keeps up: the sink consumer's backlog, sampled from the
//     core's /metrics, and how long it takes to drain once sending stops;
//   - the core's cost: CPU and resident memory, also from /metrics.
//
//	edg-loadgen -url nats://127.0.0.1:4222 -metrics http://127.0.0.1:9464/metrics \
//	    -assets 100 -tags 50 -interval 1s -duration 60s
//
// Latency has millisecond resolution because it is read from the reading's
// own timestamp: carrying a finer send time in metadata would make it a label
// with a unique value per message, which is a cardinality leak in storage.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/adapters/go/sdk"
)

type options struct {
	URL        string
	MetricsURL string
	Assets     int
	Tags       int
	Interval   time.Duration
	Duration   time.Duration
	Conns      int
	Profile    string
	Ramp       time.Duration
	BurstEvery time.Duration
	BurstFor   time.Duration
	BurstX     int
	Settle     time.Duration
	Label      string
	JSONOut    string
}

// Result is one run, as written to -json.
type Result struct {
	Label        string  `json:"label"`
	Profile      string  `json:"profile"`
	Assets       int     `json:"assets"`
	TagsPerMsg   int     `json:"tags_per_message"`
	IntervalMS   int64   `json:"interval_ms"`
	DurationS    float64 `json:"duration_s"`
	TargetMsgS   float64 `json:"target_messages_per_s"`
	TargetPointS float64 `json:"target_points_per_s"`

	Sent         int64   `json:"messages_sent"`
	SendErrors   int64   `json:"send_errors"`
	Validated    int64   `json:"messages_validated"`
	LossPct      float64 `json:"loss_pct"`
	AchievedMsgS float64 `json:"achieved_messages_per_s"`
	AchievedPtS  float64 `json:"achieved_points_per_s"`

	LatencyP50MS float64 `json:"latency_p50_ms"`
	LatencyP95MS float64 `json:"latency_p95_ms"`
	LatencyP99MS float64 `json:"latency_p99_ms"`
	LatencyMaxMS float64 `json:"latency_max_ms"`

	StoredPoints    float64 `json:"points_stored"`
	StoredPct       float64 `json:"stored_pct"`
	SinkPendingMax  float64 `json:"sink_pending_max"`
	SinkPendingEnd  float64 `json:"sink_pending_at_stop"`
	SinkDrainS      float64 `json:"sink_drain_s"`
	SinkLinesPerS   float64 `json:"sink_lines_written_per_s"`
	HandleP99MS     float64 `json:"core_handle_p99_ms_bucket"`
	CoreCPUPct      float64 `json:"core_cpu_pct_avg"`
	CoreRSSMaxMB    float64 `json:"core_rss_max_mb"`
	CoreDropped     float64 `json:"core_ingest_dropped"`
	SlowConsumerHit bool    `json:"slow_consumer_suspected"`
}

func main() {
	var o options
	flag.StringVar(&o.URL, "url", "nats://127.0.0.1:4222", "EDG Core NATS URL, credentials included")
	flag.StringVar(&o.MetricsURL, "metrics", "", "EDG Core /metrics URL (backlog, CPU, memory); empty skips them")
	flag.IntVar(&o.Assets, "assets", 100, "simulated assets, each publishing one message per interval")
	flag.IntVar(&o.Tags, "tags", 20, "numeric values per message")
	flag.DurationVar(&o.Interval, "interval", time.Second, "publish interval per asset")
	flag.DurationVar(&o.Duration, "duration", 60*time.Second, "how long to send")
	flag.IntVar(&o.Conns, "conns", 8, "NATS connections the assets are spread over (adapter processes)")
	flag.StringVar(&o.Profile, "profile", "steady", "steady | ramp | burst")
	flag.DurationVar(&o.Ramp, "ramp", 30*time.Second, "ramp: time to bring all assets online")
	flag.DurationVar(&o.BurstEvery, "burst-every", 20*time.Second, "burst: period between bursts")
	flag.DurationVar(&o.BurstFor, "burst-for", 5*time.Second, "burst: length of a burst")
	flag.IntVar(&o.BurstX, "burst-x", 5, "burst: rate multiplier during a burst")
	flag.DurationVar(&o.Settle, "settle", 60*time.Second, "after sending, how long to wait for the backlog to drain")
	flag.StringVar(&o.Label, "label", "", "name for this run in the output")
	flag.StringVar(&o.JSONOut, "json", "", "append the result as one JSON line to this file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	res, err := run(ctx, o)
	if err != nil {
		log.Fatal(err)
	}
	printResult(res)
	if o.JSONOut != "" {
		f, err := os.OpenFile(o.JSONOut, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		b, _ := json.Marshal(res)
		fmt.Fprintln(f, string(b))
	}
}

func run(ctx context.Context, o options) (*Result, error) {
	runID := strconv.FormatInt(time.Now().Unix()%100000, 36)
	prefix := "loadgen-" + runID + "-"

	// The observer is its own connection, like any other consumer of
	// validated data, so it does not share a socket with the senders.
	obs, err := nats.Connect(o.URL, nats.Name("edg-loadgen-observer"))
	if err != nil {
		return nil, fmt.Errorf("observer connect: %w", err)
	}
	defer obs.Close()
	lat := newLatencies()
	var validated atomic.Int64
	sub, err := obs.Subscribe("platform.data.validated", func(m *nats.Msg) {
		now := time.Now().UnixMilli()
		var ad struct {
			AssetID   string `json:"asset_id"`
			Timestamp int64  `json:"timestamp"`
		}
		if json.Unmarshal(m.Data, &ad) != nil || !strings.HasPrefix(ad.AssetID, prefix) {
			return
		}
		validated.Add(1)
		lat.add(float64(now - ad.Timestamp))
	})
	if err != nil {
		return nil, err
	}
	// A slow observer would report loss that is really its own. A large
	// pending limit makes that unlikely; a dropped count makes it visible.
	_ = sub.SetPendingLimits(-1, 1<<30)
	defer sub.Unsubscribe()

	clients := make([]*sdk.Client, o.Conns)
	for i := range clients {
		clients[i] = sdk.NewClient(sdk.Options{URL: o.URL, Name: fmt.Sprintf("edg-loadgen-%d", i)})
		if err := clients[i].Connect(ctx); err != nil {
			return nil, fmt.Errorf("sender connect: %w", err)
		}
		defer clients[i].Close()
	}

	sampler := newCoreSampler(o.MetricsURL)
	before := sampler.scrape()

	var sent, sendErrs atomic.Int64
	sendCtx, stopSending := context.WithTimeout(ctx, o.Duration)
	defer stopSending()
	start := time.Now()
	var wg sync.WaitGroup
	for a := 0; a < o.Assets; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			simulateAsset(sendCtx, o, a, prefix+strconv.Itoa(a), clients[a%len(clients)], start, &sent, &sendErrs)
		}(a)
	}

	// Sample the core while sending.
	samples := make(chan coreSample, 1024)
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-sendCtx.Done():
				return
			case <-t.C:
				samples <- sampler.scrape()
			}
		}
	}()
	wg.Wait()
	sendEnd := time.Now()
	<-sampleDone
	close(samples)
	var during []coreSample
	for s := range samples {
		during = append(during, s)
	}
	for _, c := range clients {
		_ = c.Flush(5 * time.Second)
	}
	atStop := sampler.scrape()

	// Drain: validated catches up and every point sent has been written.
	// Judged by lines written, not by the backlog gauge, which the core
	// refreshes only every consumer_stat_interval.
	wantLines := float64(sent.Load() * int64(o.Tags))
	drainStart := time.Now()
	deadline := drainStart.Add(o.Settle)
	for time.Now().Before(deadline) {
		s := sampler.scrape()
		if validated.Load() >= sent.Load() && (!sampler.enabled() || s.lines-before.lines >= wantLines) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	drain := time.Since(drainStart)
	after := sampler.scrape()

	elapsed := sendEnd.Sub(start).Seconds()
	r := &Result{
		Label: o.Label, Profile: o.Profile, Assets: o.Assets, TagsPerMsg: o.Tags,
		IntervalMS: o.Interval.Milliseconds(), DurationS: elapsed,
		TargetMsgS: float64(o.Assets) / o.Interval.Seconds(),
		Sent:       sent.Load(), SendErrors: sendErrs.Load(), Validated: validated.Load(),
	}
	r.TargetPointS = r.TargetMsgS * float64(o.Tags)
	r.AchievedMsgS = float64(r.Sent) / elapsed
	r.AchievedPtS = r.AchievedMsgS * float64(o.Tags)
	if r.Sent > 0 {
		r.LossPct = 100 * float64(r.Sent-r.Validated) / float64(r.Sent)
	}
	r.LatencyP50MS, r.LatencyP95MS, r.LatencyP99MS, r.LatencyMaxMS = lat.summary()

	if sampler.enabled() {
		for _, s := range during {
			r.SinkPendingMax = math.Max(r.SinkPendingMax, s.pending)
			r.CoreRSSMaxMB = math.Max(r.CoreRSSMaxMB, s.rss/1e6)
		}
		r.SinkPendingEnd = atStop.pending
		r.SinkDrainS = drain.Seconds()
		r.StoredPoints = after.lines - before.lines
		if wantLines > 0 {
			r.StoredPct = 100 * r.StoredPoints / wantLines
		}
		if wall := atStop.at.Sub(before.at).Seconds(); wall > 0 {
			r.SinkLinesPerS = (atStop.lines - before.lines) / wall
		}
		if cpuWall := atStop.at.Sub(before.at).Seconds(); cpuWall > 0 {
			r.CoreCPUPct = 100 * (atStop.cpu - before.cpu) / cpuWall
		}
		r.HandleP99MS = handleP99(before.handle, atStop.handle) * 1000
		r.CoreDropped = after.dropped - before.dropped
		// Slow-consumer drops lose messages with no error anywhere; loss
		// together with a handle latency near the interval is the signature.
		r.SlowConsumerHit = r.CoreDropped > 0
	}
	return r, nil
}

// simulateAsset publishes one message per interval, scaled by the profile.
func simulateAsset(ctx context.Context, o options, idx int, assetID string, c *sdk.Client, start time.Time, sent, errs *atomic.Int64) {
	// Spread assets across the interval so a round is not one burst.
	offset := time.Duration(int64(o.Interval) * int64(idx) / int64(max(o.Assets, 1)))
	if o.Profile == "ramp" {
		offset += time.Duration(int64(o.Ramp) * int64(idx) / int64(max(o.Assets, 1)))
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(offset):
	}

	values := make([]sdk.TagValue, o.Tags)
	nums := make([]float64, o.Tags)
	for i := range values {
		values[i] = sdk.TagValue{Name: fmt.Sprintf("t%03d", i), Quality: sdk.QualityGood, Number: &nums[i]}
	}
	var seq float64
	next := time.Now()
	for {
		step := o.Interval
		if o.Profile == "burst" && o.BurstEvery > 0 {
			if time.Since(start)%o.BurstEvery < o.BurstFor {
				step = o.Interval / time.Duration(max(o.BurstX, 1))
			}
		}
		next = next.Add(step)
		seq++
		for i := range nums {
			nums[i] = seq + float64(i)
		}
		err := c.PublishAssetData(ctx, sdk.AssetData{
			AssetID: assetID, Timestamp: time.Now().UnixMilli(), Values: values,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errs.Add(1)
		} else {
			sent.Add(1)
		}
		d := time.Until(next)
		if d < 0 {
			next = time.Now() // fell behind; do not try to catch up in a burst
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}

// --- latency ---

type latencies struct {
	mu sync.Mutex
	v  []float64
}

func newLatencies() *latencies { return &latencies{v: make([]float64, 0, 1<<16)} }

func (l *latencies) add(ms float64) {
	l.mu.Lock()
	l.v = append(l.v, ms)
	l.mu.Unlock()
}

func (l *latencies) summary() (p50, p95, p99, maxv float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.v) == 0 {
		return
	}
	sort.Float64s(l.v)
	q := func(p float64) float64 { return l.v[int(math.Ceil(p*float64(len(l.v))))-1] }
	return q(0.50), q(0.95), q(0.99), l.v[len(l.v)-1]
}

// --- core /metrics ---

type coreSample struct {
	at      time.Time
	pending float64
	lines   float64
	cpu     float64
	rss     float64
	dropped float64
	handle  map[float64]float64 // le -> cumulative count
}

type coreSampler struct{ url string }

func newCoreSampler(url string) *coreSampler { return &coreSampler{url: url} }

func (s *coreSampler) enabled() bool { return s.url != "" }

func (s *coreSampler) scrape() coreSample {
	out := coreSample{at: time.Now(), handle: map[float64]float64{}}
	if s.url == "" {
		return out
	}
	resp, err := http.Get(s.url)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := parseSample(line)
		if !ok {
			continue
		}
		switch {
		case name == "edg_core_sink_consumer_pending":
			out.pending = val
		case name == "edg_core_sink_lines_written_total":
			out.lines = val
		case name == "process_cpu_seconds_total":
			out.cpu = val
		case name == "process_resident_memory_bytes":
			out.rss = val
		case name == "edg_core_data_messages_dropped_total":
			out.dropped = val
		case strings.HasPrefix(name, `edg_core_data_handle_seconds_bucket{le="`):
			le := strings.TrimSuffix(strings.TrimPrefix(name, `edg_core_data_handle_seconds_bucket{le="`), `"}`)
			bound, err := strconv.ParseFloat(le, 64)
			if le == "+Inf" {
				bound, err = math.Inf(1), nil
			}
			if err == nil {
				out.handle[bound] = val
			}
		}
	}
	return out
}

func parseSample(line string) (string, float64, bool) {
	i := strings.LastIndexByte(line, ' ')
	if i < 0 {
		return "", 0, false
	}
	v, err := strconv.ParseFloat(line[i+1:], 64)
	if err != nil {
		return "", 0, false
	}
	return line[:i], v, true
}

// handleP99 is the upper bound of the histogram bucket holding the 99th
// percentile of HandleAssetData over the run. Bucket resolution, so it is an
// upper bound, not an estimate.
func handleP99(before, after map[float64]float64) float64 {
	bounds := make([]float64, 0, len(after))
	for b := range after {
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)
	if len(bounds) == 0 {
		return 0
	}
	total := after[math.Inf(1)] - before[math.Inf(1)]
	if total <= 0 {
		return 0
	}
	for _, b := range bounds {
		if (after[b]-before[b])/total >= 0.99 {
			if math.IsInf(b, 1) {
				return bounds[len(bounds)-2]
			}
			return b
		}
	}
	return 0
}

func printResult(r *Result) {
	fmt.Printf("\n== %s (%s)\n", r.Label, r.Profile)
	fmt.Printf("target      %d assets x %d tags every %dms = %.0f msg/s, %.0f points/s\n",
		r.Assets, r.TagsPerMsg, r.IntervalMS, r.TargetMsgS, r.TargetPointS)
	fmt.Printf("achieved    %.0f msg/s, %.0f points/s over %.1fs (send errors %d)\n",
		r.AchievedMsgS, r.AchievedPtS, r.DurationS, r.SendErrors)
	fmt.Printf("validated   %d / %d sent, loss %.3f%%\n", r.Validated, r.Sent, r.LossPct)
	fmt.Printf("latency     p50 %.0fms  p95 %.0fms  p99 %.0fms  max %.0fms (send -> validated)\n",
		r.LatencyP50MS, r.LatencyP95MS, r.LatencyP99MS, r.LatencyMaxMS)
	if r.CoreRSSMaxMB > 0 {
		fmt.Printf("storage     %.0f of %.0f points stored (%.2f%%); %.0f points/s written while sending; rest drained in %.1fs\n",
			r.StoredPoints, float64(r.Sent)*float64(r.TagsPerMsg), r.StoredPct, r.SinkLinesPerS, r.SinkDrainS)
		fmt.Printf("            sink backlog max %.0f messages\n", r.SinkPendingMax)
		fmt.Printf("core        CPU %.0f%% of one core, RSS max %.0f MB, handle p99 <= %.2fms\n",
			r.CoreCPUPct, r.CoreRSSMaxMB, r.HandleP99MS)
	}
	if r.SlowConsumerHit {
		fmt.Printf("OVERLOAD    the core's ingest subscription dropped %.0f messages (edg_core_data_messages_dropped_total)\n", r.CoreDropped)
	}
}
