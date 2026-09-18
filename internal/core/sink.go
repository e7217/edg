package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/internal/metrics"
)

// VM sink counters. The expvar names are the historical ones ADR 0001 and the
// user guide document; the Desc alongside each is what /metrics serves.
var (
	// "lines" here means InfluxDB line-protocol lines, i.e. individual time
	// series points -- not messages. appendAssetDataLines skips every value
	// without a numeric reading, so this is well below the message count.
	sinkLinesWritten = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_sink_lines_written_total",
		Help: "Line-protocol lines (time series points, not messages) accepted by VictoriaMetrics.",
	}, "edg_core_sink_lines_written")

	sinkBatchesWritten = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_sink_batches_written_total",
		Help: "Batches successfully written to VictoriaMetrics and then acked on JetStream.",
	}, "edg_core_sink_batches_written")

	// The reason label splits a transport failure (VM unreachable) from a
	// rejected write (VM answered, but not with 2xx). They call for different
	// responses and today both look identical in the log.
	sinkWriteFailures = metrics.Default.NewCounterVecLegacy(metrics.Desc{
		Name: "edg_core_sink_write_failures_total",
		Help: "Failed writes to VictoriaMetrics. The batch is nakked, so a sustained rate means redelivery pressure.",
	}, "reason", []string{sinkFailTransport, sinkFailHTTPStatus}, "edg_core_sink_write_failures")

	sinkDecodeFailures = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_sink_decode_failures_total",
		Help: "Messages in a batch that could not be decoded as asset data. They are skipped, not retried.",
	}, "edg_core_sink_decode_failures")

	// Non-timeout Fetch errors were previously silent: no counter, no log. A
	// consumer that has stopped delivering looks exactly like an idle plant.
	sinkFetchErrors = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_sink_fetch_errors_total",
		Help: "JetStream Fetch calls that failed for a reason other than the flush-window timeout.",
	})

	sinkMessagesAcked = metrics.Default.NewCounterVec(metrics.Desc{
		Name: "edg_core_sink_messages_acked_total",
		Help: "Messages acked. outcome=poison means the batch produced no numeric lines and was acked to stop endless redelivery -- that data is dropped, not stored.",
	}, "outcome", []string{sinkAckWritten, sinkAckPoison})

	sinkMessagesNakked = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_sink_messages_nakked_total",
		Help: "Messages nakked after a failed write. Its rate is the redelivery pressure on the stream.",
	})

	// Values the encoder did not turn into a line. Numbers and flags are always
	// written; text is written as a label (ADR 0010) unless one of these says
	// why not.
	sinkValuesSkipped = metrics.Default.NewCounterVec(metrics.Desc{
		Name: "edg_core_sink_values_skipped_total",
		Help: "Tag values not written to VictoriaMetrics, by reason. text_disabled: sink.text_values is drop. text_too_long: longer than sink.text_max_length. text_unencodable: contains a character line protocol cannot carry in a label.",
	}, "reason", []string{sinkSkipTextDisabled, sinkSkipTextTooLong, sinkSkipTextUnencodable})

	sinkWriteSeconds = metrics.Default.NewHistogram(metrics.Desc{
		Name: "edg_core_sink_write_seconds",
		Help: "Duration of a write to VictoriaMetrics, including the HTTP round trip. Compare the tail with sink.request_timeout. Buckets are provisional.",
	}, metrics.DefaultLatencyBounds)

	sinkUp = metrics.Default.NewGauge(metrics.Desc{
		Name: "edg_core_sink_up",
		Help: "1 while the sink drain loop is running.",
	})

	// Consumer backlog. ADR 0005 made the JetStream-to-storage hop durable but
	// left the backlog invisible; these are the numbers that say whether the
	// gateway is keeping up.
	sinkConsumerPending = metrics.Default.NewGauge(metrics.Desc{
		Name: "edg_core_sink_consumer_pending",
		Help: "Messages waiting in the stream for the sink's durable consumer. A sustained climb means VictoriaMetrics is slower than the plant.",
	})

	sinkConsumerAckPending = metrics.Default.NewGauge(metrics.Desc{
		Name: "edg_core_sink_consumer_ack_pending",
		Help: "Messages delivered to the sink and not yet acked.",
	})

	sinkConsumerRedelivered = metrics.Default.NewGauge(metrics.Desc{
		Name: "edg_core_sink_consumer_redelivered",
		Help: "Messages the stream is currently redelivering to the sink.",
	})
)

// Label values for the sink counters. They are constants so that a typo is a
// compile error rather than a silent fold into "other".
const (
	sinkFailTransport  = "transport"
	sinkFailHTTPStatus = "http_status"
	sinkAckWritten     = "written"
	sinkAckPoison      = "poison"

	sinkSkipTextDisabled    = "text_disabled"
	sinkSkipTextTooLong     = "text_too_long"
	sinkSkipTextUnencodable = "text_unencodable"
)

// Sink text modes.
const (
	// SinkTextLabel writes a text value as edg_data_text{value="..."} 1.
	SinkTextLabel = "label"
	// SinkTextDrop skips text values, the behaviour before ADR 0010.
	SinkTextDrop = "drop"
)

// encodeOptions is how appendAssetDataLines treats non-numeric values.
type encodeOptions struct {
	textMode      string
	textMaxLength int
}

// errWriteRejected marks a write that reached VictoriaMetrics and came back
// non-2xx, as opposed to one that never got there.
type errWriteRejected struct {
	status int
}

func (e errWriteRejected) Error() string {
	return fmt.Sprintf("VictoriaMetrics write returned status %d", e.status)
}

// writeFailureReason classifies a write error for the reason label.
func writeFailureReason(err error) string {
	var rejected errWriteRejected
	if errors.As(err, &rejected) {
		return sinkFailHTTPStatus
	}
	return sinkFailTransport
}

// VMSink consumes validated asset data from JetStream via a durable pull
// consumer and writes it to a VictoriaMetrics-compatible endpoint using the
// InfluxDB line protocol. It replaces the external Telegraf bridge so the
// "JetStream -> storage" hop honours the durable, ack-after-write boundary
// described in ADR 0001.
type VMSink struct {
	js            nats.JetStreamContext
	subject       string
	consumerName  string
	measurement   string
	encode        encodeOptions
	writeURL      string
	batchMaxSize  int
	flushInterval time.Duration
	httpClient    *http.Client
	ackWait       time.Duration
	// consumerStatInterval throttles the ConsumerInfo round trip that feeds
	// the backlog gauges. Injectable so tests do not have to wait real time.
	consumerStatInterval time.Duration

	sub    *nats.Subscription
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewVMSink builds a sink from SinkConfig. The subject is the validated-data
// subject the durable consumer reads from (the single source of truth lives in
// JetStreamConfig.ValidatedSubject). It does not touch the network until Start
// is called.
func NewVMSink(js nats.JetStreamContext, subject string, cfg SinkConfig) (*VMSink, error) {
	if js == nil {
		return nil, fmt.Errorf("vm sink requires a JetStream context")
	}
	if subject == "" {
		return nil, fmt.Errorf("vm sink requires a validated subject")
	}
	writeURL, err := buildWriteURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	// A zero interval would put a ConsumerInfo round trip on every pass of the
	// drain loop. Callers that build SinkConfig by hand get the default rather
	// than an accidental hot loop against the server.
	statInterval := cfg.ConsumerStatInterval
	if statInterval <= 0 {
		statInterval = DefaultSinkConsumerStatInterval
	}
	return &VMSink{
		js:            js,
		subject:       subject,
		consumerName:  cfg.ConsumerName,
		measurement:   cfg.Measurement,
		encode:        encodeOptions{textMode: cfg.TextValues, textMaxLength: cfg.TextMaxLength},
		writeURL:      writeURL,
		batchMaxSize:  cfg.BatchMaxSize,
		flushInterval: cfg.FlushInterval,
		httpClient:    &http.Client{Timeout: cfg.RequestTimeout},
		ackWait:       sinkAckWait(cfg.RequestTimeout),

		consumerStatInterval: statInterval,
	}, nil
}

// Start binds the durable pull consumer and launches the drain loop. The loop
// runs until ctx is cancelled or Stop is called. The durable consumer is left
// intact on stop so a restart resumes from the last acknowledged message.
func (s *VMSink) Start(ctx context.Context) error {
	stream, err := s.ensureConsumer()
	if err != nil {
		return err
	}
	// Bind rather than let PullSubscribe create the consumer: a bound
	// subscription never deletes it, and the consumer's config is ours.
	sub, err := s.js.PullSubscribe(s.subject, s.consumerName, nats.Bind(stream, s.consumerName))
	if err != nil {
		return fmt.Errorf("vm sink failed to subscribe: %w", err)
	}
	s.sub = sub

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	go s.run(runCtx)
	return nil
}

// sinkAckWait is how long a delivered message may stay unacknowledged before
// JetStream redelivers it. A batch is acked or nakked within one write, which
// request_timeout bounds, so twice that is ample. The JetStream default is 30s,
// and that is how long a hard-killed core used to leave its last batch
// stranded after a restart.
func sinkAckWait(requestTimeout time.Duration) time.Duration {
	wait := 2 * requestTimeout
	if wait < minSinkAckWait {
		wait = minSinkAckWait
	}
	return wait
}

const minSinkAckWait = 5 * time.Second

// ensureConsumer creates the durable consumer, or brings an existing one's
// AckWait in line with the configuration. It returns the stream name.
func (s *VMSink) ensureConsumer() (string, error) {
	stream, err := s.js.StreamNameBySubject(s.subject)
	if err != nil {
		return "", fmt.Errorf("vm sink: no stream captures %q: %w", s.subject, err)
	}
	info, err := s.js.ConsumerInfo(stream, s.consumerName)
	switch {
	case errors.Is(err, nats.ErrConsumerNotFound):
		_, err = s.js.AddConsumer(stream, &nats.ConsumerConfig{
			Durable:       s.consumerName,
			AckPolicy:     nats.AckExplicitPolicy,
			DeliverPolicy: nats.DeliverAllPolicy,
			FilterSubject: s.subject,
			AckWait:       s.ackWait,
		})
		if err != nil {
			return "", fmt.Errorf("vm sink failed to create consumer: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("vm sink failed to read consumer: %w", err)
	case info.Config.AckWait != s.ackWait:
		// A consumer created before sinkAckWait existed carries the 30s
		// default. AckWait is one of the fields JetStream lets us edit.
		cfg := info.Config
		cfg.AckWait = s.ackWait
		if _, err := s.js.UpdateConsumer(stream, &cfg); err != nil {
			return "", fmt.Errorf("vm sink failed to update consumer ack wait: %w", err)
		}
	}
	return stream, nil
}

// releaseBuffered naks whatever the subscription has already been handed but
// the loop never saw. A Fetch can return before the rest of its batch arrives;
// those messages sit in the client's buffer, and abandoning them at shutdown
// strands them until AckWait. Naked, they are redelivered at once.
func (s *VMSink) releaseBuffered() {
	if s.sub == nil {
		return
	}
	msgs, err := s.sub.Fetch(s.batchMaxSize, nats.MaxWait(releaseBufferedWait))
	if err != nil && !errors.Is(err, nats.ErrTimeout) {
		return
	}
	nakAll(msgs)
}

const releaseBufferedWait = 100 * time.Millisecond

// Stop signals the drain loop to exit and waits for it. It intentionally does
// not delete the durable consumer.
func (s *VMSink) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *VMSink) run(ctx context.Context) {
	defer s.wg.Done()
	sinkUp.Set(1)
	defer sinkUp.Set(0)
	defer s.releaseBuffered()

	// Consumer state comes from a NATS round trip, so it is refreshed on this
	// loop rather than at scrape time -- a scraper must never be able to put
	// traffic on the data path. The refresh sits above the select so it keeps
	// running while the plant is idle and Fetch only ever times out.
	var lastStat time.Time
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if now := time.Now(); now.Sub(lastStat) >= s.consumerStatInterval {
			lastStat = now
			s.refreshConsumerStats()
		}

		msgs, err := s.sub.Fetch(s.batchMaxSize, nats.MaxWait(s.flushInterval))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue // no messages within the flush window
			}
			// Connection draining/closed or transient consumer error.
			sinkFetchErrors.Inc()
			log.Printf("[Core] VM sink fetch failed: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.flushInterval):
				continue
			}
		}
		if len(msgs) == 0 {
			continue
		}

		body, lines := s.encodeBatch(msgs)
		if lines == 0 {
			// Nothing numeric to write (or all decode failures); ack so the
			// poison messages are not redelivered forever. This is a drop:
			// the outcome label is what makes it visible.
			sinkMessagesAcked.With(sinkAckPoison).Add(int64(len(msgs)))
			ackAll(msgs)
			continue
		}

		start := time.Now()
		err = s.write(ctx, body)
		sinkWriteSeconds.Observe(time.Since(start).Seconds())
		if err != nil {
			sinkWriteFailures.With(writeFailureReason(err)).Inc()
			sinkMessagesNakked.Add(int64(len(msgs)))
			log.Printf("[Core] VM sink write failed (%d lines requeued): %v", lines, err)
			nakAll(msgs)
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.flushInterval):
			}
			continue
		}

		sinkLinesWritten.Add(int64(lines))
		sinkBatchesWritten.Add(1)
		sinkMessagesAcked.With(sinkAckWritten).Add(int64(len(msgs)))
		ackAll(msgs)
	}
}

// refreshConsumerStats publishes the durable consumer's backlog.
//
// On error the previous values are left in place: reporting zero pending for a
// consumer we simply could not reach would read as "all caught up", which is
// the opposite of what a failed query implies.
func (s *VMSink) refreshConsumerStats() {
	if s.sub == nil {
		return
	}
	info, err := s.sub.ConsumerInfo()
	if err != nil || info == nil {
		return
	}
	sinkConsumerPending.Set(int64(info.NumPending))
	sinkConsumerAckPending.Set(int64(info.NumAckPending))
	sinkConsumerRedelivered.Set(int64(info.NumRedelivered))
}

// encodeBatch turns a batch of validated messages into a single line-protocol
// payload and returns the number of data lines produced.
func (s *VMSink) encodeBatch(msgs []*nats.Msg) ([]byte, int) {
	var buf bytes.Buffer
	lines := 0
	for _, msg := range msgs {
		var data AssetData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			sinkDecodeFailures.Add(1)
			log.Printf("[Core] VM sink could not decode validated message: %v", err)
			continue
		}
		lines += appendAssetDataLines(&buf, s.measurement, data, s.encode)
	}
	return buf.Bytes(), lines
}

func (s *VMSink) write(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.writeURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode/100 != 2 {
		return errWriteRejected{status: resp.StatusCode}
	}
	return nil
}

func ackAll(msgs []*nats.Msg) {
	for _, msg := range msgs {
		_ = msg.Ack()
	}
}

func nakAll(msgs []*nats.Msg) {
	for _, msg := range msgs {
		_ = msg.Nak()
	}
}

// buildWriteURL appends the InfluxDB-compatible write path and millisecond
// precision to a base URL such as "http://localhost:8428".
func buildWriteURL(base string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("vm sink url is empty")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid vm sink url %q: %w", base, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid vm sink url %q: scheme and host are required", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/write"
	q := u.Query()
	q.Set("precision", "ms")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// appendAssetDataLines writes one InfluxDB line per tag value and returns the
// count. Adapter timestamps are epoch milliseconds (see the Go and Python SDKs),
// so the sink emits millisecond precision.
//
// The field name becomes the VictoriaMetrics metric suffix, so one message can
// produce three metrics (ADR 0010):
//
//	edg_data_number{...}               the reading
//	edg_data_flag{...}                 1 or 0
//	edg_data_text{...,value="RUNNING"} 1
//
// VictoriaMetrics stores float samples only. A text reading is therefore a
// label on a constant sample -- the Prometheus "info metric" shape -- which
// keeps a state or alarm code queryable at the cost of one series per distinct
// string. sink.text_max_length is the guard on that cost.
func appendAssetDataLines(buf *bytes.Buffer, measurement string, data AssetData, opts encodeOptions) int {
	metaKeys := sortedKeys(data.Metadata)
	count := 0
	for _, v := range data.Values {
		var field, fieldValue, textValue string
		switch {
		case v.Number != nil:
			field = "number"
			fieldValue = strconv.FormatFloat(*v.Number, 'g', -1, 64)
		case v.Flag != nil:
			field = "flag"
			fieldValue = "0"
			if *v.Flag {
				fieldValue = "1"
			}
		case v.Text != nil:
			if reason := textSkipReason(*v.Text, opts); reason != "" {
				sinkValuesSkipped.With(reason).Inc()
				continue
			}
			field = "text"
			fieldValue = "1"
			textValue = *v.Text
		default:
			continue
		}

		buf.WriteString(escapeMeasurement(measurement))
		writeTag(buf, "asset_id", data.AssetID)
		writeTag(buf, "name", v.Name)
		writeTag(buf, "unit", v.Unit)
		writeTag(buf, "quality", v.Quality)
		for _, k := range metaKeys {
			// Reserved keys are removed by the data contract; this guards the
			// warn mode, where they reach the sink. A duplicated tag key fails
			// the whole batch at VictoriaMetrics and redelivers it forever.
			if ReservedMetadataKeys[k] {
				continue
			}
			writeTag(buf, k, data.Metadata[k])
		}
		if field == "text" {
			writeTag(buf, "value", textValue)
		}
		buf.WriteByte(' ')
		buf.WriteString(field)
		buf.WriteByte('=')
		buf.WriteString(fieldValue)
		if data.Timestamp > 0 {
			buf.WriteByte(' ')
			buf.WriteString(strconv.FormatInt(data.Timestamp, 10))
		}
		buf.WriteByte('\n')
		count++
	}
	return count
}

// textSkipReason says why a text value cannot become a label, or "" if it can.
func textSkipReason(text string, opts encodeOptions) string {
	if opts.textMode == SinkTextDrop {
		return sinkSkipTextDisabled
	}
	if text == "" {
		// An empty tag value is not representable; the line would carry no
		// value label at all and read as a different series.
		return sinkSkipTextUnencodable
	}
	if opts.textMaxLength > 0 && len(text) > opts.textMaxLength {
		return sinkSkipTextTooLong
	}
	if strings.ContainsAny(text, "\n\r\\") {
		return sinkSkipTextUnencodable
	}
	return ""
}

var (
	tagEscaper         = strings.NewReplacer(",", `\,`, "=", `\=`, " ", `\ `)
	measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `)
	lineBreakReplacer  = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
)

// writeTag appends a ",key=value" pair, skipping tags with an empty key or
// value (InfluxDB does not allow empty tag values). A line break cannot be
// escaped in line protocol and would split the line, so it becomes a space.
func writeTag(buf *bytes.Buffer, key, value string) {
	if key == "" || value == "" {
		return
	}
	if strings.ContainsAny(value, "\n\r") {
		value = lineBreakReplacer.Replace(value)
	}
	buf.WriteByte(',')
	buf.WriteString(tagEscaper.Replace(key))
	buf.WriteByte('=')
	buf.WriteString(tagEscaper.Replace(value))
}

func escapeMeasurement(s string) string {
	return measurementEscaper.Replace(s)
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
