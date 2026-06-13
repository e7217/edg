package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"expvar"
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
)

// VM sink expvar counters, consistent with the edg_core_* naming used elsewhere.
var (
	sinkLinesWritten   = expvar.NewInt("edg_core_sink_lines_written")
	sinkBatchesWritten = expvar.NewInt("edg_core_sink_batches_written")
	sinkWriteFailures  = expvar.NewInt("edg_core_sink_write_failures")
	sinkDecodeFailures = expvar.NewInt("edg_core_sink_decode_failures")
)

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
	writeURL      string
	batchMaxSize  int
	flushInterval time.Duration
	httpClient    *http.Client

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
	return &VMSink{
		js:            js,
		subject:       subject,
		consumerName:  cfg.ConsumerName,
		measurement:   cfg.Measurement,
		writeURL:      writeURL,
		batchMaxSize:  cfg.BatchMaxSize,
		flushInterval: cfg.FlushInterval,
		httpClient:    &http.Client{Timeout: cfg.RequestTimeout},
	}, nil
}

// Start binds the durable pull consumer and launches the drain loop. The loop
// runs until ctx is cancelled or Stop is called. The durable consumer is left
// intact on stop so a restart resumes from the last acknowledged message.
func (s *VMSink) Start(ctx context.Context) error {
	sub, err := s.js.PullSubscribe(s.subject, s.consumerName)
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
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, err := s.sub.Fetch(s.batchMaxSize, nats.MaxWait(s.flushInterval))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue // no messages within the flush window
			}
			// Connection draining/closed or transient consumer error.
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
			// poison messages are not redelivered forever.
			ackAll(msgs)
			continue
		}

		if err := s.write(ctx, body); err != nil {
			sinkWriteFailures.Add(1)
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
		ackAll(msgs)
	}
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
		lines += appendAssetDataLines(&buf, s.measurement, data)
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
		return fmt.Errorf("VictoriaMetrics write returned status %d", resp.StatusCode)
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

// appendAssetDataLines writes one InfluxDB line per numeric tag value and
// returns the count. Adapter timestamps are epoch milliseconds (see the Go and
// Python SDKs), so the sink emits millisecond precision. Non-numeric values
// (text/flag) are skipped, matching the prior Telegraf behaviour.
func appendAssetDataLines(buf *bytes.Buffer, measurement string, data AssetData) int {
	metaKeys := sortedKeys(data.Metadata)
	count := 0
	for _, v := range data.Values {
		if v.Number == nil {
			continue
		}
		buf.WriteString(escapeMeasurement(measurement))
		writeTag(buf, "asset_id", data.AssetID)
		writeTag(buf, "name", v.Name)
		writeTag(buf, "unit", v.Unit)
		writeTag(buf, "quality", v.Quality)
		for _, k := range metaKeys {
			writeTag(buf, k, data.Metadata[k])
		}
		buf.WriteString(" number=")
		buf.Write(strconv.AppendFloat(nil, *v.Number, 'g', -1, 64))
		if data.Timestamp > 0 {
			buf.WriteByte(' ')
			buf.WriteString(strconv.FormatInt(data.Timestamp, 10))
		}
		buf.WriteByte('\n')
		count++
	}
	return count
}

var (
	tagEscaper         = strings.NewReplacer(",", `\,`, "=", `\=`, " ", `\ `)
	measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `)
)

// writeTag appends a ",key=value" pair, skipping tags with an empty key or
// value (InfluxDB does not allow empty tag values).
func writeTag(buf *bytes.Buffer, key, value string) {
	if key == "" || value == "" {
		return
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
