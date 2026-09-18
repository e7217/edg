package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/internal/metrics"
)

// DataHandler handles NATS messages for asset data
type DataHandler struct {
	store              *Store                // for undeclared-asset detection
	js                 nats.JetStreamContext // for publishing to JetStream
	validatedSubject   string
	deadLetterSubject  string
	unknownAssetPolicy string
	events             *EventPublisher // for metadata change notifications
	enricher           *Enricher
	contract           *ContractChecker
	logValues          bool
	undeclaredLog      *rateLimitedLog
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

	// The data contract (ADR 0010). Together with the dead-letter counters
	// these say where data went that did not reach platform.data.validated:
	// rejected (whole message), dropped (one value), or never decoded.
	contractViolations = metrics.Default.NewCounterVec(metrics.Desc{
		Name: "edg_core_data_contract_violations_total",
		Help: "Data contract violations by reason. Under data_contract.mode=enforce each one removed a message, a value or a metadata key; under warn it was only counted.",
	}, "reason", ViolationReasons)

	contractMessagesRejected = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_data_messages_rejected_total",
		Help: "Messages rejected by the data contract. Each one is dead-lettered with its violations.",
	})

	contractValuesDropped = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_data_values_dropped_total",
		Help: "Values removed from otherwise valid messages by the data contract. The rest of the message is published; the original is dead-lettered.",
	})

	contractTimestampsFilled = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_data_timestamps_filled_total",
		Help: "Messages without a timestamp, stamped with the time the core received them.",
	})

	contractLookupFailures = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_data_contract_lookup_failures_total",
		Help: "Master-data lookups for the data contract that failed. The message is checked against the envelope rules only and passes as if its asset were declared.",
	})

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

// Dead-letter reasons for the data contract (ADR 0010).
var (
	errContractRejected = errors.New("data contract: message rejected")
	errContractPartial  = errors.New("data contract: values dropped, remainder published")
)

type DataHandlerOptions struct {
	ValidatedSubject   string
	DeadLetterSubject  string
	Events             *EventPublisher
	UnknownAssetPolicy string
	Enricher           *Enricher
	// Contract applies the data contract. Nil builds an enforcing checker on
	// the handler's store.
	Contract *ContractChecker
	// LogValues logs every received value. Off by default: at 200k points/s
	// it is 200k synchronous log writes a second on the ingest goroutine, and
	// it was the difference between a p99 of 424ms and 2ms (docs/perf).
	LogValues bool
}

// DeadLetterMessage records a failed core-to-JetStream publish attempt.
type DeadLetterMessage struct {
	OriginalSubject string          `json:"original_subject"`
	TargetSubject   string          `json:"target_subject"`
	Error           string          `json:"error"`
	Payload         json.RawMessage `json:"payload"`
	Timestamp       time.Time       `json:"timestamp"`
	// Violations is set when the data contract removed something. For a
	// partial drop the payload is the original message, and what was
	// published is the payload minus the listed values.
	Violations []Violation `json:"violations,omitempty"`
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

	contract := opts.Contract
	if contract == nil {
		contract = NewContractChecker(store, DataContractEnforce)
	}

	return &DataHandler{
		contract:           contract,
		logValues:          opts.LogValues,
		undeclaredLog:      &rateLimitedLog{every: undeclaredLogEvery},
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

	var profile *assetProfile
	if data.AssetID != "" {
		p, err := h.contract.Profile(data.AssetID)
		if err != nil {
			contractLookupFailures.Inc()
			log.Printf("[Core] data contract lookup failed for %s: %v", data.AssetID, err)
		} else {
			profile = p
		}
	}

	result := h.contract.Apply(&data, profile)
	if result.TimestampFilled {
		contractTimestampsFilled.Inc()
	}
	for _, v := range result.Violations {
		contractViolations.With(v.Reason).Inc()
	}
	if len(result.Violations) > 0 {
		log.Printf("[Core] data contract (%s): asset %q, %d violation(s), first: %s %s",
			h.contract.Mode(), data.AssetID, len(result.Violations),
			result.Violations[0].Reason, result.Violations[0].Detail)
	}
	if result.Rejected {
		contractMessagesRejected.Inc()
		h.publishDeadLetter(msg, errContractRejected, result.Violations...)
		return
	}
	contractValuesDropped.Add(int64(result.DroppedValues))
	enforced := h.contract.Mode() == DataContractEnforce && len(result.Violations) > 0

	// Undeclared assets follow the configured unknown_asset_policy. Master data is
	// created explicitly (API/CLI/UI/import); the data plane no longer auto-registers.
	// A failed lookup leaves profile nil and is not treated as undeclared: a
	// database hiccup must not dead-letter a declared asset's data.
	if profile != nil && !profile.Exists {
		undeclaredAssets.With(h.unknownAssetPolicy).Inc()
		if h.unknownAssetPolicy == UnknownAssetPolicyDeadLetter {
			h.undeclaredLog.printf("[Core] undeclared asset -> dead-letter: %s", data.AssetID)
			h.publishDeadLetter(msg, errUndeclaredAsset, result.Violations...)
			return
		}
		h.undeclaredLog.printf("[Core] undeclared asset (pass_through): %s", data.AssetID)
	}

	// The contract may have stamped a timestamp, filled a unit or removed
	// something, so the message is re-encoded whenever it was touched rather
	// than forwarding the adapter's bytes.
	validatedData := msg.Data
	if result.TimestampFilled || enforced || len(result.Violations) > 0 {
		if encoded, err := json.Marshal(data); err == nil {
			validatedData = encoded
		}
	}
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
	// A partial drop still owes the operator the values it removed.
	if enforced {
		h.publishDeadLetter(msg, errContractPartial, result.Violations...)
	}

	if !h.logValues {
		return
	}
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

// undeclaredLogEvery bounds the undeclared-asset log. A plant that has not
// declared its assets yet sends every message through that path, and a line
// per message is a log write per message on the ingest goroutine.
// edg_core_undeclared_assets_total counts every one.
const undeclaredLogEvery = 10 * time.Second

// rateLimitedLog prints at most one line per interval and says how many it
// suppressed. HandleAssetData can run concurrently, so it is locked.
type rateLimitedLog struct {
	every time.Duration

	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (l *rateLimitedLog) printf(format string, args ...any) {
	l.mu.Lock()
	now := time.Now()
	if now.Sub(l.last) < l.every {
		l.suppressed++
		l.mu.Unlock()
		return
	}
	if l.suppressed > 0 {
		format += fmt.Sprintf(" (and %d more since the last line)", l.suppressed)
	}
	l.last, l.suppressed = now, 0
	l.mu.Unlock()
	log.Printf(format, args...)
}

func (h *DataHandler) publishDeadLetter(msg *nats.Msg, publishErr error, violations ...Violation) {
	if h.deadLetterSubject == "" || h.js == nil {
		return
	}

	envelope := DeadLetterMessage{
		OriginalSubject: msg.Subject,
		TargetSubject:   h.validatedSubject,
		Error:           publishErr.Error(),
		Payload:         append(json.RawMessage(nil), msg.Data...),
		Timestamp:       time.Now().UTC(),
		Violations:      violations,
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
