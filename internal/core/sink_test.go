package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func floatPtr(v float64) *float64 { return &v }
func strPtr(v string) *string     { return &v }

// --- line protocol encoder (unit) ---

func TestAppendAssetDataLines_Basic(t *testing.T) {
	var buf bytes.Buffer
	n := appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID:   "sensor-001",
		Timestamp: 1700000000000,
		Values: []TagValue{
			{Name: "temperature", Number: floatPtr(25.5), Unit: "celsius", Quality: "good"},
		},
	}, labelText)

	assert.Equal(t, 1, n)
	assert.Equal(t,
		"edg_data,asset_id=sensor-001,name=temperature,unit=celsius,quality=good number=25.5 1700000000000\n",
		buf.String(),
	)
}

// labelText is the default encoding: text as a label (ADR 0010).
var labelText = encodeOptions{textMode: SinkTextLabel, textMaxLength: DefaultSinkTextMaxLength}

func boolPtr(v bool) *bool { return &v }

// Before ADR 0010 the sink wrote numbers only, so a machine state reported as
// text or a door switch reported as a flag was acked and never stored.
func TestAppendAssetDataLines_WritesEveryValueKind(t *testing.T) {
	var buf bytes.Buffer
	n := appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID:   "sensor-001",
		Timestamp: 1700000000000,
		Values: []TagValue{
			{Name: "state", Text: strPtr("RUNNING"), Quality: "good"},
			{Name: "online", Flag: boolPtr(true), Quality: "good"},
			{Name: "door", Flag: boolPtr(false), Quality: "good"},
			{Name: "temperature", Number: floatPtr(10), Quality: "good"},
		},
	}, labelText)

	assert.Equal(t, 4, n)
	assert.Equal(t,
		"edg_data,asset_id=sensor-001,name=state,quality=good,value=RUNNING text=1 1700000000000\n"+
			"edg_data,asset_id=sensor-001,name=online,quality=good flag=1 1700000000000\n"+
			"edg_data,asset_id=sensor-001,name=door,quality=good flag=0 1700000000000\n"+
			"edg_data,asset_id=sensor-001,name=temperature,quality=good number=10 1700000000000\n",
		buf.String())
}

func TestAppendAssetDataLines_TextSkipReasons(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		opts   encodeOptions
		reason string
	}{
		{"drop mode", "RUNNING", encodeOptions{textMode: SinkTextDrop}, sinkSkipTextDisabled},
		{"too long", strings.Repeat("x", 9), encodeOptions{textMode: SinkTextLabel, textMaxLength: 8}, sinkSkipTextTooLong},
		{"line break", "a\nb", labelText, sinkSkipTextUnencodable},
		{"backslash", `a\`, labelText, sinkSkipTextUnencodable},
		{"empty", "", labelText, sinkSkipTextUnencodable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := vecValue(t, "edg_core_sink_values_skipped_total", "reason", tc.reason)
			var buf bytes.Buffer
			n := appendAssetDataLines(&buf, "edg_data", AssetData{
				AssetID: "s1",
				Values:  []TagValue{{Name: "state", Text: strPtr(tc.text), Quality: "good"}},
			}, tc.opts)
			assert.Equal(t, 0, n)
			assert.Empty(t, buf.String())
			assert.Equal(t, before+1, vecValue(t, "edg_core_sink_values_skipped_total", "reason", tc.reason))
		})
	}
}

// A reserved key in metadata reaches the sink only in warn mode. Written, it
// would duplicate a tag key; VictoriaMetrics would reject the batch and the
// sink would redeliver it forever.
func TestAppendAssetDataLines_SkipsReservedMetadataKeys(t *testing.T) {
	var buf bytes.Buffer
	appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID:  "s1",
		Metadata: map[string]string{"name": "spoof", "value": "x", "line": "L1"},
		Values:   []TagValue{{Name: "t", Number: floatPtr(1), Quality: "good"}},
	}, labelText)
	assert.Equal(t, "edg_data,asset_id=s1,name=t,quality=good,line=L1 number=1\n", buf.String())
}

func TestAppendAssetDataLines_LineBreakInMetadataBecomesSpace(t *testing.T) {
	var buf bytes.Buffer
	appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID:  "s1",
		Metadata: map[string]string{"line": "L\n1"},
		Values:   []TagValue{{Name: "t", Number: floatPtr(1), Quality: "good"}},
	}, labelText)
	assert.Equal(t, 1, strings.Count(buf.String(), "\n"))
	assert.Contains(t, buf.String(), `line=L\ 1`)
}

func TestAppendAssetDataLines_ZeroTimestampOmitted(t *testing.T) {
	var buf bytes.Buffer
	appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID: "s1",
		Values:  []TagValue{{Name: "t", Number: floatPtr(1), Quality: "good"}},
	}, labelText)
	// No trailing timestamp: line ends right after the field.
	assert.Equal(t, "edg_data,asset_id=s1,name=t,quality=good number=1\n", buf.String())
}

func TestAppendAssetDataLines_EmptyTagValuesSkipped(t *testing.T) {
	var buf bytes.Buffer
	appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID: "s1",
		Values:  []TagValue{{Name: "t", Number: floatPtr(1), Unit: "", Quality: "good"}},
	}, labelText)
	// Empty unit must not produce a "unit=" tag.
	assert.NotContains(t, buf.String(), "unit=")
}

func TestAppendAssetDataLines_EscapesSpecialChars(t *testing.T) {
	var buf bytes.Buffer
	appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID: "asset 1,a=b",
		Values:  []TagValue{{Name: "temp value", Number: floatPtr(1), Quality: "good"}},
	}, labelText)
	out := buf.String()
	assert.Contains(t, out, `asset_id=asset\ 1\,a\=b`)
	assert.Contains(t, out, `name=temp\ value`)
}

func TestAppendAssetDataLines_MetadataSortedTags(t *testing.T) {
	var buf bytes.Buffer
	appendAssetDataLines(&buf, "edg_data", AssetData{
		AssetID:  "s1",
		Metadata: map[string]string{"line": "L1", "factory": "F1"},
		Values:   []TagValue{{Name: "t", Number: floatPtr(1), Quality: "good"}},
	}, labelText)
	// Metadata tags appear in deterministic sorted order (factory before line).
	assert.Contains(t, buf.String(), "factory=F1,line=L1 number=1")
}

func TestBuildWriteURL(t *testing.T) {
	got, err := buildWriteURL("http://localhost:8428")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8428/write?precision=ms", got)

	got, err = buildWriteURL("http://vm:8428/")
	require.NoError(t, err)
	assert.Equal(t, "http://vm:8428/write?precision=ms", got)

	_, err = buildWriteURL("")
	assert.Error(t, err)

	_, err = buildWriteURL("not-a-url")
	assert.Error(t, err)
}

// --- VM sink consumer loop (integration) ---

// mockVM records received line-protocol bodies and can fail a number of times
// before succeeding.
type mockVM struct {
	mu        sync.Mutex
	bodies    []string
	failFirst int
	attempts  int
	received  chan struct{}
}

func newMockVM(failFirst int) (*httptest.Server, *mockVM) {
	m := &mockVM{failFirst: failFirst, received: make(chan struct{}, 64)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.attempts++
		fail := m.attempts <= m.failFirst
		if !fail {
			m.bodies = append(m.bodies, string(body))
		}
		m.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		m.received <- struct{}{}
	}))
	return srv, m
}

func (m *mockVM) allBodies() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.bodies, "")
}

func (m *mockVM) lineCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, b := range m.bodies {
		total += strings.Count(b, "\n")
	}
	return total
}

func newSinkTestStream(t *testing.T, js nats.JetStreamContext) {
	t.Helper()
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "SINK_TEST",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)
}

func sinkTestConfig(url string) SinkConfig {
	return SinkConfig{
		Enabled:        true,
		URL:            url,
		ConsumerName:   "test-vm-sink",
		Measurement:    "edg_data",
		BatchMaxSize:   100,
		FlushInterval:  150 * time.Millisecond,
		RequestTimeout: 2 * time.Second,
	}
}

func publishValidated(t *testing.T, js nats.JetStreamContext, assetID string, value float64) {
	t.Helper()
	msg := assetDataMessage(t, assetID, value)
	_, err := js.Publish("platform.data.validated", msg.Data)
	require.NoError(t, err)
}

func TestVMSink_WritesValidatedDataToVM(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, mock := newMockVM(0)
	defer srv.Close()

	sink, err := NewVMSink(js, "platform.data.validated", sinkTestConfig(srv.URL))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	publishValidated(t, js, "sensor-001", 25.5)

	select {
	case <-mock.received:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for VM write")
	}

	assert.Contains(t, mock.allBodies(), "edg_data,asset_id=sensor-001")
	assert.Contains(t, mock.allBodies(), "number=25.5")
}

func TestVMSink_RetriesOnWriteFailure(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, mock := newMockVM(2) // fail first 2 attempts, then succeed
	defer srv.Close()

	before := sinkWriteFailures.Value()

	sink, err := NewVMSink(js, "platform.data.validated", sinkTestConfig(srv.URL))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	publishValidated(t, js, "sensor-001", 42)

	select {
	case <-mock.received:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for VM write after retries")
	}

	// The message was redelivered until the write succeeded; no data loss and
	// the failure counter advanced by the two rejected attempts.
	assert.Contains(t, mock.allBodies(), "number=42")
	assert.GreaterOrEqual(t, sinkWriteFailures.Value()-before, int64(2))
}

func TestVMSink_DrainsBacklogPublishedBeforeStart(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, mock := newMockVM(0)
	defer srv.Close()

	// Publish a backlog of validated messages BEFORE the sink exists. This is
	// the regression guard for ADR 0001: a durable consumer must replay the
	// backlog, which the old Telegraf queue-group subscription could not.
	const backlog = 5
	for i := 0; i < backlog; i++ {
		publishValidated(t, js, fmt.Sprintf("sensor-%03d", i), float64(i))
	}

	sink, err := NewVMSink(js, "platform.data.validated", sinkTestConfig(srv.URL))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	require.Eventually(t, func() bool {
		return mock.lineCount() >= backlog
	}, 5*time.Second, 50*time.Millisecond, "sink did not drain the full backlog")
}

// A consumer created before the sink managed its own AckWait carries
// JetStream's 30s default. Start must bring it in line: 30s is how long a
// restarted core left its predecessor's last batch undelivered.
func TestVMSink_StartMigratesConsumerAckWait(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, _ := newMockVM(0)
	defer srv.Close()

	cfg := sinkTestConfig(srv.URL)
	_, err := js.AddConsumer("SINK_TEST", &nats.ConsumerConfig{
		Durable:       cfg.ConsumerName,
		AckPolicy:     nats.AckExplicitPolicy,
		FilterSubject: "platform.data.validated",
		AckWait:       30 * time.Second,
	})
	require.NoError(t, err)

	sink, err := NewVMSink(js, "platform.data.validated", cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	info, err := js.ConsumerInfo("SINK_TEST", cfg.ConsumerName)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, info.Config.AckWait, "2 x 2s request_timeout, floored at 5s")
}

func TestSinkAckWait(t *testing.T) {
	assert.Equal(t, 5*time.Second, sinkAckWait(time.Second), "floor")
	assert.Equal(t, 10*time.Second, sinkAckWait(5*time.Second), "the default request_timeout")
	assert.Equal(t, 60*time.Second, sinkAckWait(30*time.Second))
}
