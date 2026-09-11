package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/metrics"
)

// newMetricsTestStore is an empty in-memory metadata DB.
func newMetricsTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// snapshot reads several counters at once. The metrics are process-global by
// design -- they back an expvar contract -- so every assertion here is on a
// delta, never on an absolute value.
func snapshot(names ...func() int64) []int64 {
	out := make([]int64, len(names))
	for i, fn := range names {
		out[i] = fn()
	}
	return out
}

// vecValue reads one child of a labelled family out of the exposition, which
// is the surface an operator sees.
func vecValue(t *testing.T, family, label, value string) int64 {
	t.Helper()
	prefix := fmt.Sprintf("%s{%s=%q} ", family, label, value)
	for _, line := range strings.Split(string(metrics.Default.Gather()), "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			var n int64
			if _, err := fmt.Sscanf(rest, "%d", &n); err != nil {
				t.Fatalf("unparseable sample %q", line)
			}
			return n
		}
	}
	t.Fatalf("%s not found in the exposition", prefix)
	return 0
}

func histCount(t *testing.T, family string) int64 {
	t.Helper()
	prefix := family + "_count "
	for _, line := range strings.Split(string(metrics.Default.Gather()), "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			var n int64
			if _, err := fmt.Sscanf(rest, "%d", &n); err != nil {
				t.Fatalf("unparseable sample %q", line)
			}
			return n
		}
	}
	t.Fatalf("%s not found in the exposition", prefix)
	return 0
}

func TestIngestMetrics_SuccessPath(t *testing.T) {
	before := snapshot(
		dataMessagesReceived.Value,
		dataDecodeFailures.Value,
		func() int64 { return histCount(t, "edg_core_data_handle_seconds") },
	)
	beforeNumber := vecValue(t, "edg_core_data_values_total", "kind", "number")
	beforeText := vecValue(t, "edg_core_data_values_total", "kind", "text")

	handler := NewDataHandler(nil, nil)
	number, text := 25.5, "ok"
	payload, err := json.Marshal(&AssetData{
		AssetID:   "sensor-metrics-1",
		Timestamp: 1,
		Values: []TagValue{
			{Name: "temperature", Number: &number},
			{Name: "status", Text: &text},
			{Name: "nothing"}, // neither number, text nor flag
		},
	})
	require.NoError(t, err)
	handler.HandleAssetData(&nats.Msg{Subject: "platform.data.asset", Data: payload})

	assert.Equal(t, before[0]+1, dataMessagesReceived.Value(), "received counter")
	assert.Equal(t, before[1], dataDecodeFailures.Value(), "decode failures must not move")
	assert.Equal(t, before[2]+1, histCount(t, "edg_core_data_handle_seconds"), "handle histogram")
	assert.Equal(t, beforeNumber+1, vecValue(t, "edg_core_data_values_total", "kind", "number"))
	assert.Equal(t, beforeText+1, vecValue(t, "edg_core_data_values_total", "kind", "text"))
}

// A message that cannot be decoded must move the decode counter and nothing
// else: it never reaches the value or publish paths.
func TestIngestMetrics_DecodeFailure(t *testing.T) {
	beforeDecode := dataDecodeFailures.Value()
	beforeReceived := dataMessagesReceived.Value()
	beforePublished := jetStreamPublished.Value()

	handler := NewDataHandler(nil, nil)
	handler.HandleAssetData(&nats.Msg{Subject: "platform.data.asset", Data: []byte("not json")})

	assert.Equal(t, beforeDecode+1, dataDecodeFailures.Value())
	assert.Equal(t, beforeReceived+1, dataMessagesReceived.Value(), "a bad message is still a message")
	assert.Equal(t, beforePublished, jetStreamPublished.Value())
}

// The policy label must record what was actually done with the data, so an
// operator can tell "passed through un-enriched" from "never stored".
func TestUndeclaredAssetMetricCarriesPolicy(t *testing.T) {
	for _, policy := range []string{UnknownAssetPolicyPassThrough, UnknownAssetPolicyDeadLetter} {
		t.Run(policy, func(t *testing.T) {
			store := newMetricsTestStore(t)
			before := vecValue(t, "edg_core_undeclared_assets_total", "policy", policy)
			beforeTotal := undeclaredAssets.Value()

			handler := NewDataHandlerWithConfig(nil, store, DataHandlerOptions{
				UnknownAssetPolicy: policy,
			})
			value := 1.0
			payload, err := json.Marshal(&AssetData{
				AssetID: "never-declared",
				Values:  []TagValue{{Name: "v", Number: &value}},
			})
			require.NoError(t, err)
			handler.HandleAssetData(&nats.Msg{Subject: "platform.data.asset", Data: payload})

			assert.Equal(t, before+1, vecValue(t, "edg_core_undeclared_assets_total", "policy", policy))
			assert.Equal(t, beforeTotal+1, undeclaredAssets.Value(),
				"the legacy expvar total must advance with every child")
		})
	}
}

// The reason label must separate "VM was unreachable" from "VM said no",
// because those call for different operator responses.
func TestSinkWriteFailureReasons(t *testing.T) {
	assert.Equal(t, "http_status", writeFailureReason(errWriteRejected{status: 500}))
	assert.Equal(t, "http_status", writeFailureReason(fmt.Errorf("wrapped: %w", errWriteRejected{status: 503})))
	assert.Equal(t, "transport", writeFailureReason(fmt.Errorf("dial tcp: connection refused")))
	assert.Equal(t, "transport", writeFailureReason(context.DeadlineExceeded))
}

func TestSinkMetrics_RejectedWrite(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	beforeStatus := vecValue(t, "edg_core_sink_write_failures_total", "reason", "http_status")
	beforeTransport := vecValue(t, "edg_core_sink_write_failures_total", "reason", "transport")
	beforeNak := sinkMessagesNakked.Value()
	beforeWriteHist := histCount(t, "edg_core_sink_write_seconds")

	sink, err := NewVMSink(js, "platform.data.validated", sinkTestConfig(srv.URL))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	publishValidated(t, js, "sensor-metrics-2", 7)

	require.Eventually(t, func() bool {
		return vecValue(t, "edg_core_sink_write_failures_total", "reason", "http_status") > beforeStatus
	}, 5*time.Second, 50*time.Millisecond, "the rejected write was not counted under reason=http_status")

	assert.Equal(t, beforeTransport,
		vecValue(t, "edg_core_sink_write_failures_total", "reason", "transport"),
		"a 500 response is not a transport failure")
	assert.Greater(t, sinkMessagesNakked.Value(), beforeNak, "nakked messages are the redelivery pressure signal")
	assert.Greater(t, histCount(t, "edg_core_sink_write_seconds"), beforeWriteHist,
		"a failed write is still a timed write")
	assert.Equal(t, int64(1), sinkUp.Value(), "the sink reports itself up while draining")
}

func TestSinkMetrics_SuccessfulWriteAcksAndTracksBacklog(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, mock := newMockVM(0)
	defer srv.Close()

	beforeAcked := vecValue(t, "edg_core_sink_messages_acked_total", "outcome", "written")

	cfg := sinkTestConfig(srv.URL)
	// Injected so the backlog gauges refresh without waiting the production
	// 15 seconds.
	cfg.ConsumerStatInterval = 50 * time.Millisecond

	sink, err := NewVMSink(js, "platform.data.validated", cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	publishValidated(t, js, "sensor-metrics-3", 11)
	select {
	case <-mock.received:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the VM write")
	}

	require.Eventually(t, func() bool {
		return vecValue(t, "edg_core_sink_messages_acked_total", "outcome", "written") > beforeAcked
	}, 5*time.Second, 50*time.Millisecond, "a written batch was not acked under outcome=written")
}

// The backlog gauges are the reason ADR 0005's durable hop is observable at
// all, so they get a deterministic test rather than a timing-dependent one:
// seed a known backlog, refresh, and read it back.
func TestSinkMetrics_BacklogGaugesReportPendingMessages(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, mock := newMockVM(0)
	defer srv.Close()

	cfg := sinkTestConfig(srv.URL)
	cfg.ConsumerStatInterval = 50 * time.Millisecond
	sink, err := NewVMSink(js, "platform.data.validated", cfg)
	require.NoError(t, err)

	// Bind the durable consumer without starting the drain loop, so the
	// backlog is ours to control.
	sub, err := js.PullSubscribe("platform.data.validated", cfg.ConsumerName)
	require.NoError(t, err)
	sink.sub = sub

	const backlog = 3
	for i := 0; i < backlog; i++ {
		publishValidated(t, js, fmt.Sprintf("sensor-backlog-%d", i), float64(i))
	}
	require.Eventually(t, func() bool {
		info, err := sub.ConsumerInfo()
		return err == nil && info.NumPending == backlog
	}, 5*time.Second, 50*time.Millisecond, "the seeded backlog never reached the consumer")

	sink.refreshConsumerStats()
	assert.Equal(t, int64(backlog), sinkConsumerPending.Value(),
		"pending must report the messages waiting for the sink")
	assert.Equal(t, int64(0), sinkConsumerAckPending.Value())

	// Now let the drain loop run: the gauge must come back down on its own,
	// which is only possible if the loop refreshes it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	select {
	case <-mock.received:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the backlog to drain")
	}
	require.Eventually(t, func() bool {
		return sinkConsumerPending.Value() == 0
	}, 5*time.Second, 50*time.Millisecond,
		"pending stayed at %d after the backlog drained; the drain loop is not refreshing the gauges",
		sinkConsumerPending.Value())
}

// A ConsumerInfo failure must leave the last known values in place. Reporting
// zero pending for a consumer we could not reach reads as "all caught up",
// which is the opposite of what the failure implies.
func TestSinkMetrics_BacklogGaugesHoldLastValueOnError(t *testing.T) {
	t.Run("no subscription", func(t *testing.T) {
		sinkConsumerPending.Set(7)
		sink := &VMSink{}
		sink.refreshConsumerStats()
		assert.Equal(t, int64(7), sinkConsumerPending.Value())
	})

	t.Run("consumer query fails", func(t *testing.T) {
		_, _, js := startTestNATSServer(t, true)
		newSinkTestStream(t, js)
		srv, _ := newMockVM(0)
		defer srv.Close()

		cfg := sinkTestConfig(srv.URL)
		sink, err := NewVMSink(js, "platform.data.validated", cfg)
		require.NoError(t, err)
		sub, err := js.PullSubscribe("platform.data.validated", cfg.ConsumerName)
		require.NoError(t, err)
		sink.sub = sub

		publishValidated(t, js, "sensor-error-path", 1)
		require.Eventually(t, func() bool {
			info, err := sub.ConsumerInfo()
			return err == nil && info.NumPending == 1
		}, 5*time.Second, 50*time.Millisecond)
		sink.refreshConsumerStats()
		require.Equal(t, int64(1), sinkConsumerPending.Value())

		// Delete the consumer out from under the subscription, so the next
		// ConsumerInfo genuinely fails.
		require.NoError(t, js.DeleteConsumer("SINK_TEST", cfg.ConsumerName))
		require.Eventually(t, func() bool {
			_, err := sub.ConsumerInfo()
			return err != nil
		}, 5*time.Second, 50*time.Millisecond, "ConsumerInfo kept succeeding after the consumer was deleted")

		sink.refreshConsumerStats()
		assert.Equal(t, int64(1), sinkConsumerPending.Value(),
			"an unreachable consumer must not be reported as a drained one")
	})
}

// A batch with nothing numeric is acked to stop endless redelivery. That is a
// silent drop, and outcome=poison is the only thing that makes it visible.
func TestSinkMetrics_PoisonBatchIsCountedSeparately(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)
	newSinkTestStream(t, js)
	srv, _ := newMockVM(0)
	defer srv.Close()

	beforePoison := vecValue(t, "edg_core_sink_messages_acked_total", "outcome", "poison")
	beforeWritten := vecValue(t, "edg_core_sink_messages_acked_total", "outcome", "written")

	sink, err := NewVMSink(js, "platform.data.validated", sinkTestConfig(srv.URL))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sink.Start(ctx))
	defer sink.Stop()

	// Text-only: appendAssetDataLines emits nothing for a value with no number.
	text := "still fine"
	payload, err := json.Marshal(&AssetData{
		AssetID: "text-only",
		Values:  []TagValue{{Name: "status", Text: &text}},
	})
	require.NoError(t, err)
	_, err = js.Publish("platform.data.validated", payload)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return vecValue(t, "edg_core_sink_messages_acked_total", "outcome", "poison") > beforePoison
	}, 5*time.Second, 50*time.Millisecond, "a batch with no numeric lines was not counted as poison")

	assert.Equal(t, beforeWritten,
		vecValue(t, "edg_core_sink_messages_acked_total", "outcome", "written"),
		"nothing was written, so nothing may be counted as written")
}

func TestEnricherMetrics(t *testing.T) {
	store := newMetricsTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{MaxDepth: 5})

	beforeMiss := enricherCacheMisses.Value()
	beforeHit := enricherCacheHits.Value()
	beforeLookup := histCount(t, "edg_core_enricher_ancestor_lookup_seconds")
	beforeFlush := enricherFlushes.Value()

	_, err := enricher.tagsForAsset("asset-not-in-store")
	require.NoError(t, err)
	assert.Equal(t, beforeMiss+1, enricherCacheMisses.Value())
	assert.Equal(t, beforeHit, enricherCacheHits.Value())
	assert.Equal(t, beforeLookup+1, histCount(t, "edg_core_enricher_ancestor_lookup_seconds"),
		"a miss must time the recursive ancestor query")

	_, err = enricher.tagsForAsset("asset-not-in-store")
	require.NoError(t, err)
	assert.Equal(t, beforeHit+1, enricherCacheHits.Value())
	assert.Equal(t, beforeLookup+1, histCount(t, "edg_core_enricher_ancestor_lookup_seconds"),
		"a hit must not run the query")

	enricher.Flush()
	assert.Equal(t, beforeFlush+1, enricherFlushes.Value())
}

// TestMetricCardinalityBudget is the load-bearing test behind ADR 0009: the
// series count must be a property of the code, not of the plant.
func TestMetricCardinalityBudget(t *testing.T) {
	const budget = 400

	base := metrics.Default.SeriesCount()
	require.Less(t, base, budget, "the process already exceeds its own series budget")

	for _, assets := range []int{1, 100, 10000} {
		t.Run(fmt.Sprintf("assets=%d", assets), func(t *testing.T) {
			handler := NewDataHandler(nil, nil)
			value := 1.0
			for i := 0; i < assets; i++ {
				payload, err := json.Marshal(&AssetData{
					// Distinct ids, and ids shaped like label values, so a
					// leak would show up as a new series either way.
					AssetID: fmt.Sprintf("plant-a/line-%d/sensor-%d", i%17, i),
					Values:  []TagValue{{Name: fmt.Sprintf("tag-%d", i), Number: &value}},
				})
				require.NoError(t, err)
				handler.HandleAssetData(&nats.Msg{Subject: "platform.data.asset", Data: payload})
			}

			after := metrics.Default.SeriesCount()
			assert.Equal(t, base, after,
				"%d distinct asset ids changed the series count; a label is taking data-plane content", assets)
			assert.Less(t, after, budget)
		})
	}

	// And nothing that looks like an asset id may appear anywhere in the
	// exposition.
	out := string(metrics.Default.Gather())
	assert.NotContains(t, out, "plant-a/line-", "an asset id reached the exposition")
	assert.NotContains(t, out, "tag-", "a tag name reached the exposition")
}

func TestStoreMetrics(t *testing.T) {
	store := newMetricsTestStore(t)
	require.NoError(t, store.CreateAsset(&Asset{ID: "a1", Name: "A1", TemplateName: "sensor"}))
	require.NoError(t, store.CreateAsset(&Asset{ID: "a2", Name: "A2", TemplateName: "sensor"}))

	r := metrics.NewRegistry()
	store.RegisterMetrics(r, StoreMetricsOptions{})
	out := string(r.Gather())

	for _, name := range []string{
		"edg_core_store_connections_open",
		"edg_core_store_connections_in_use",
		"edg_core_store_connections_idle",
		"edg_core_store_connection_waits_total",
		"edg_core_store_connection_wait_seconds_total",
	} {
		assert.Contains(t, out, name+" ", "%s missing", name)
	}
	assert.Contains(t, out, "edg_core_store_assets 2")
	assert.Contains(t, out, "edg_core_store_relations 0")
	assert.Contains(t, out, "edg_core_store_points 0")
}

// The counts share the single SQLite handle with the ingest path, and
// /metrics is unauthenticated on the monitoring port. Without a cache, a
// remote caller could force two table scans per request.
func TestStoreCountsAreCached(t *testing.T) {
	store := newMetricsTestStore(t)
	require.NoError(t, store.CreateAsset(&Asset{ID: "a1", Name: "A1", TemplateName: "sensor"}))

	counts := &cachedStoreCounts{store: store, ttl: time.Hour, timeout: time.Second}
	require.Equal(t, 1, counts.get().assets)

	require.NoError(t, store.CreateAsset(&Asset{ID: "a2", Name: "A2", TemplateName: "sensor"}))
	assert.Equal(t, 1, counts.get().assets, "a second asset was reported before the TTL expired")

	// Expiring the entry must let the new value through.
	counts.mu.Lock()
	counts.fetched = time.Now().Add(-2 * time.Hour)
	counts.mu.Unlock()
	assert.Equal(t, 2, counts.get().assets)
}

// A failed count says nothing about how many assets exist, so the previous
// value must stand rather than collapsing to zero.
func TestStoreCountsHoldLastValueOnError(t *testing.T) {
	store := newMetricsTestStore(t)
	require.NoError(t, store.CreateAsset(&Asset{ID: "a1", Name: "A1", TemplateName: "sensor"}))

	counts := &cachedStoreCounts{store: store, ttl: time.Millisecond, timeout: time.Second}
	require.Equal(t, 1, counts.get().assets)

	beforeFailures := storeQueryFailures.Value()
	require.NoError(t, store.Close())
	time.Sleep(2 * time.Millisecond) // let the TTL lapse

	assert.Equal(t, 1, counts.get().assets, "a closed database reported zero assets")
	assert.Greater(t, storeQueryFailures.Value(), beforeFailures, "the failure was not counted")
}

func TestAlarmMetrics(t *testing.T) {
	store := newMetricsTestStore(t)
	require.NoError(t, store.CreateAsset(&Asset{ID: "alarm-asset", Name: "A", TemplateName: "sensor"}))

	beforeCritical := vecValue(t, "edg_core_alarm_received_total", "severity", "critical")
	beforeInvalid := alarmsInvalid.Value()
	beforeImpact := histCount(t, "edg_core_alarm_impact_affected_assets")
	beforeLCA := alarmLCAQueries.Value()

	handler := NewAlarmHandler(store, NewEventPublisher(nil), AlarmHandlerOptions{Window: time.Hour})
	require.NoError(t, handler.Process(Alarm{
		ID: "alarm-1", AssetID: "alarm-asset", Severity: SeverityCritical, Message: "hot",
	}))

	assert.Equal(t, beforeCritical+1, vecValue(t, "edg_core_alarm_received_total", "severity", "critical"))
	assert.Equal(t, beforeImpact+1, histCount(t, "edg_core_alarm_impact_affected_assets"))
	assert.Equal(t, int64(1), alarmGroupsPending.Value(), "the open group must be visible while the window is running")

	// A second alarm searches the open group, which is where the per-alarm
	// cost lives.
	require.NoError(t, handler.Process(Alarm{
		ID: "alarm-2", AssetID: "alarm-asset", Severity: SeverityWarning, Message: "warm",
	}))
	assert.Greater(t, alarmLCAQueries.Value(), beforeLCA,
		"the ancestor query run under the aggregator lock was not counted")

	// An alarm with no asset id fails validation and must be counted as
	// invalid rather than received.
	assert.Error(t, handler.Process(Alarm{ID: "alarm-3", Severity: SeverityInfo}))
	assert.Equal(t, beforeInvalid+1, alarmsInvalid.Value())
}

// An alarm naming an unknown asset is an impact failure, not a validation
// failure: the payload was well formed, the plant model was not.
func TestAlarmImpactFailureMetric(t *testing.T) {
	store := newMetricsTestStore(t)
	before := alarmImpactFailures.Value()

	handler := NewAlarmHandler(store, NewEventPublisher(nil), AlarmHandlerOptions{Window: time.Hour})
	assert.Error(t, handler.Process(Alarm{
		ID: "alarm-x", AssetID: "no-such-asset", Severity: SeverityInfo, Message: "?",
	}))
	assert.Equal(t, before+1, alarmImpactFailures.Value())
}
