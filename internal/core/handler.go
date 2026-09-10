package core

import (
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/internal/metrics"
)

// DataHandler handles NATS messages for asset data
type DataHandler struct {
	mu                 sync.Mutex
	data               []AssetData           // in-memory storage (PoC)
	store              *Store                // for undeclared-asset detection
	js                 nats.JetStreamContext // for publishing to JetStream
	validatedSubject   string
	deadLetterSubject  string
	unknownAssetPolicy string
	events             *EventPublisher // for metadata change notifications
	enricher           *Enricher
}

// These counters are stored in *expvar.Int and published under their historical
// expvar names, so /debug/vars is byte-for-byte what it was; the Desc alongside
// each one is what /metrics serves. See ADR 0009.
var (
	jetStreamPublishFailures = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_jetstream_publish_failures_total",
		Help: "Validated messages that could not be published to JetStream. Each one is also dead-lettered.",
	}, "edg_core_jetstream_publish_failures")

	jetStreamDeadLetters = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_jetstream_dead_letters_total",
		Help: "Messages written to the dead-letter subject.",
	}, "edg_core_jetstream_dead_letters")

	jetStreamDeadLetterFails = metrics.Default.NewCounterVecLegacy(metrics.Desc{
		Name: "edg_core_jetstream_dead_letter_failures_total",
		Help: "Dead-letter attempts that themselves failed. These messages are lost. stage=encode is a bug in this process; stage=publish is JetStream refusing the write.",
	}, "stage", []string{deadLetterStageEncode, deadLetterStagePublish},
		"edg_core_jetstream_dead_letter_failures")

	// The policy label says what happened to the data, not just that it was
	// undeclared: pass_through means it reached storage un-enriched,
	// dead_letter means it did not reach storage at all.
	undeclaredAssets = metrics.Default.NewCounterVecLegacy(metrics.Desc{
		Name: "edg_core_undeclared_assets_total",
		Help: "Messages whose asset_id has no master-data record, labelled with the unknown_asset_policy that was applied.",
	}, "policy", []string{UnknownAssetPolicyPassThrough, UnknownAssetPolicyDeadLetter},
		"edg_core_undeclared_assets")

	// Adapter runtime status (ADR 0008).
	adapterStatusInvalid = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_status_invalid_total",
		Help: "Adapter status frames rejected as malformed, oversized, or carrying an id that does not match the subject.",
	}, "edg_core_adapter_status_invalid")

	adapterStatusDropped = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_status_dropped_total",
		Help: "Adapter status frames dropped by NATS because the subscription's pending limit was reached.",
	}, "edg_core_adapter_status_dropped")

	adapterStale = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_stale_total",
		Help: "Transitions of an adapter into the stale availability state.",
	}, "edg_core_adapter_stale_total")

	adapterProbeRecovered = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_probe_recovered_total",
		Help: "Adapters that answered a liveness probe and so were kept out of the stale state.",
	}, "edg_core_adapter_probe_recovered")

	adapterProbesSkipped = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_probes_skipped_total",
		Help: "Liveness probes not sent because the concurrency limit was already reached; those adapters go stale without being probed.",
	}, "edg_core_adapter_probes_skipped")

	adapterStaleAverted = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_stale_averted_total",
		Help: "Stale markings abandoned because a fresh status frame arrived while the probe was in flight.",
	}, "edg_core_adapter_stale_averted")

	adapterForgetAverted = metrics.Default.NewCounterLegacy(metrics.Desc{
		Name: "edg_core_adapter_forget_averted_total",
		Help: "Registry evictions abandoned because the adapter reported in again before the forget deadline.",
	}, "edg_core_adapter_forget_averted")
)

// New instrumentation on the ingest path. None of this existed before: the
// process could count its failures but not its successes, so no failure rate
// could be computed from what it published.
var (
	dataMessagesReceived = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_data_messages_received_total",
		Help: "Messages accepted on platform.data.asset. This is the denominator every other ingest counter is measured against.",
	})

	dataDecodeFailures = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_data_decode_failures_total",
		Help: "Messages on platform.data.asset that were not valid asset-data JSON. They are dropped without a dead letter.",
	})

	// The VM sink discards every value without a numeric reading, so the kind
	// split is what turns "messages received" into "points that can reach
	// storage".
	dataValues = metrics.Default.NewCounterVec(metrics.Desc{
		Name: "edg_core_data_values_total",
		Help: "Tag values received by kind. Only kind=number reaches VictoriaMetrics; the rest are dropped by the sink encoder.",
	}, "kind", []string{valueKindNumber, valueKindText, valueKindFlag, valueKindEmpty})

	enrichFailures = metrics.Default.NewCounterVec(metrics.Desc{
		Name: "edg_core_enrich_failures_total",
		Help: "Enrichment failures. The message still goes through un-enriched, so this is silent data-quality loss rather than an outage.",
	}, "stage", []string{enrichStageEnrich, enrichStageEncode})

	jetStreamPublished = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_jetstream_published_total",
		Help: "Validated messages successfully published to JetStream.",
	})

	// The single most load-bearing metric here. HandleAssetData runs on the
	// NATS delivery goroutine and does a synchronous AssetExists, enrichment
	// that can fall through to a recursive CTE, and a JetStream publish. If it
	// stops fitting inside the message interval the subscription becomes a
	// slow consumer and messages are dropped silently.
	dataHandleSeconds = metrics.Default.NewHistogram(metrics.Desc{
		Name: "edg_core_data_handle_seconds",
		Help: "Time spent in HandleAssetData on the NATS delivery goroutine. Exceeding the message interval turns the subscription into a slow consumer. Buckets are provisional.",
	}, metrics.DefaultLatencyBounds)

	dataBufferEntries = metrics.Default.NewGauge(metrics.Desc{
		Name: "edg_core_data_buffer_entries",
		Help: "Entries in the in-memory PoC buffer. It is append-only with no truncation, so this only ever grows; see #115.",
	})
)

// Label values for the ingest counters.
const (
	deadLetterStageEncode  = "encode"
	deadLetterStagePublish = "publish"
	valueKindNumber        = "number"
	valueKindText          = "text"
	valueKindFlag          = "flag"
	valueKindEmpty         = "empty"
	enrichStageEnrich      = "enrich"
	enrichStageEncode      = "encode"
)

// errUndeclaredAsset is the dead-letter reason when an undeclared asset_id is
// routed under unknown_asset_policy = dead_letter.
var errUndeclaredAsset = errors.New("undeclared asset")

type DataHandlerOptions struct {
	ValidatedSubject   string
	DeadLetterSubject  string
	Events             *EventPublisher
	UnknownAssetPolicy string
	Enricher           *Enricher
}

// DeadLetterMessage records a failed core-to-JetStream publish attempt.
type DeadLetterMessage struct {
	OriginalSubject string          `json:"original_subject"`
	TargetSubject   string          `json:"target_subject"`
	Error           string          `json:"error"`
	Payload         json.RawMessage `json:"payload"`
	Timestamp       time.Time       `json:"timestamp"`
}

func NewDataHandler(js nats.JetStreamContext, store *Store, events ...*EventPublisher) *DataHandler {
	var publisher *EventPublisher
	if len(events) > 0 {
		publisher = events[0]
	}
	return NewDataHandlerWithConfig(js, store, DataHandlerOptions{
		Events: publisher,
	})
}

func NewDataHandlerWithConfig(js nats.JetStreamContext, store *Store, opts DataHandlerOptions) *DataHandler {
	validatedSubject := opts.ValidatedSubject
	if validatedSubject == "" {
		validatedSubject = DefaultValidatedDataSubject
	}
	deadLetterSubject := opts.DeadLetterSubject
	if deadLetterSubject == "" {
		deadLetterSubject = DefaultDeadLetterSubject
	}
	unknownAssetPolicy := opts.UnknownAssetPolicy
	if unknownAssetPolicy == "" {
		unknownAssetPolicy = UnknownAssetPolicyPassThrough
	}

	return &DataHandler{
		data:               make([]AssetData, 0),
		store:              store,
		js:                 js,
		validatedSubject:   validatedSubject,
		deadLetterSubject:  deadLetterSubject,
		unknownAssetPolicy: unknownAssetPolicy,
		events:             opts.Events,
		enricher:           opts.Enricher,
	}
}

func NewDataHandlerWithSubjects(js nats.JetStreamContext, store *Store, validatedSubject, deadLetterSubject string, events ...*EventPublisher) *DataHandler {
	var publisher *EventPublisher
	if len(events) > 0 {
		publisher = events[0]
	}
	return NewDataHandlerWithConfig(js, store, DataHandlerOptions{
		ValidatedSubject:  validatedSubject,
		DeadLetterSubject: deadLetterSubject,
		Events:            publisher,
	})
}

// HandleAssetData processes incoming NATS messages
func (h *DataHandler) HandleAssetData(msg *nats.Msg) {
	start := time.Now()
	defer func() { dataHandleSeconds.Observe(time.Since(start).Seconds()) }()

	dataMessagesReceived.Inc()

	var data AssetData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		dataDecodeFailures.Inc()
		log.Printf("[Core] Error parsing message: %v", err)
		return
	}
	countValueKinds(data.Values)

	// Undeclared assets follow the configured unknown_asset_policy. Master data is
	// created explicitly (API/CLI/UI/import); the data plane no longer auto-registers.
	if h.store != nil {
		if exists, _ := h.store.AssetExists(data.AssetID); !exists {
			undeclaredAssets.With(h.unknownAssetPolicy).Inc()
			if h.unknownAssetPolicy == UnknownAssetPolicyDeadLetter {
				log.Printf("[Core] undeclared asset -> dead-letter: %s", data.AssetID)
				h.publishDeadLetter(msg, errUndeclaredAsset)
				return
			}
			log.Printf("[Core] undeclared asset (pass_through): %s", data.AssetID)
		}
	}

	validatedData := msg.Data
	if h.enricher != nil {
		if err := h.enricher.Enrich(&data); err != nil {
			enrichFailures.With(enrichStageEnrich).Inc()
			log.Printf("[Core] Failed to enrich asset data: %v", err)
		} else if enrichedData, err := json.Marshal(data); err != nil {
			enrichFailures.With(enrichStageEncode).Inc()
			log.Printf("[Core] Failed to encode enriched asset data: %v", err)
		} else {
			validatedData = enrichedData
		}
	}

	h.mu.Lock()
	h.data = append(h.data, data)
	buffered := len(h.data)
	h.mu.Unlock()
	dataBufferEntries.Set(int64(buffered))

	// Publish validated data to JetStream for persistence
	if h.js != nil {
		if _, err := h.js.Publish(h.validatedSubject, validatedData); err != nil {
			jetStreamPublishFailures.Add(1)
			log.Printf("[Core] Failed to publish to JetStream: %v", err)
			h.publishDeadLetter(msg, err)
		} else {
			jetStreamPublished.Inc()
		}
	}

	// Log output
	log.Printf("[Core] Asset: %s, Tags: %d", data.AssetID, len(data.Values))
	for _, v := range data.Values {
		switch {
		case v.Number != nil:
			log.Printf("       ├─ %s = %.2f %s [%s]", v.Name, *v.Number, v.Unit, v.Quality)
		case v.Text != nil:
			log.Printf("       ├─ %s = %q [%s]", v.Name, *v.Text, v.Quality)
		case v.Flag != nil:
			log.Printf("       ├─ %s = %v [%s]", v.Name, *v.Flag, v.Quality)
		}
	}
}

func (h *DataHandler) publishDeadLetter(msg *nats.Msg, publishErr error) {
	if h.deadLetterSubject == "" || h.js == nil {
		return
	}

	envelope := DeadLetterMessage{
		OriginalSubject: msg.Subject,
		TargetSubject:   h.validatedSubject,
		Error:           publishErr.Error(),
		Payload:         append(json.RawMessage(nil), msg.Data...),
		Timestamp:       time.Now().UTC(),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		jetStreamDeadLetterFails.With(deadLetterStageEncode).Inc()
		log.Printf("[Core] Failed to encode dead-letter message: %v", err)
		return
	}
	if _, err := h.js.Publish(h.deadLetterSubject, data); err != nil {
		jetStreamDeadLetterFails.With(deadLetterStagePublish).Inc()
		log.Printf("[Core] Failed to publish dead-letter message: %v", err)
		return
	}
	jetStreamDeadLetters.Add(1)
}

// countValueKinds records the shape of an incoming payload. The sink writes
// only numeric readings, so this is where a plant that reports everything as
// text becomes visible instead of just producing an empty dashboard.
func countValueKinds(values []TagValue) {
	for _, v := range values {
		switch {
		case v.Number != nil:
			dataValues.With(valueKindNumber).Inc()
		case v.Text != nil:
			dataValues.With(valueKindText).Inc()
		case v.Flag != nil:
			dataValues.With(valueKindFlag).Inc()
		default:
			dataValues.With(valueKindEmpty).Inc()
		}
	}
}

// GetDataCount returns the number of stored data entries
func (h *DataHandler) GetDataCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.data)
}
