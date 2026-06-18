package core

import (
	"encoding/json"
	"errors"
	"expvar"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
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

var (
	jetStreamPublishFailures = expvar.NewInt("edg_core_jetstream_publish_failures")
	jetStreamDeadLetters     = expvar.NewInt("edg_core_jetstream_dead_letters")
	jetStreamDeadLetterFails = expvar.NewInt("edg_core_jetstream_dead_letter_failures")
	undeclaredAssets         = expvar.NewInt("edg_core_undeclared_assets")
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
	var data AssetData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		log.Printf("[Core] Error parsing message: %v", err)
		return
	}

	// Undeclared assets follow the configured unknown_asset_policy. Master data is
	// created explicitly (API/CLI/UI/import); the data plane no longer auto-registers.
	if h.store != nil {
		if exists, _ := h.store.AssetExists(data.AssetID); !exists {
			undeclaredAssets.Add(1)
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
			log.Printf("[Core] Failed to enrich asset data: %v", err)
		} else if enrichedData, err := json.Marshal(data); err != nil {
			log.Printf("[Core] Failed to encode enriched asset data: %v", err)
		} else {
			validatedData = enrichedData
		}
	}

	h.mu.Lock()
	h.data = append(h.data, data)
	h.mu.Unlock()

	// Publish validated data to JetStream for persistence
	if h.js != nil {
		if _, err := h.js.Publish(h.validatedSubject, validatedData); err != nil {
			jetStreamPublishFailures.Add(1)
			log.Printf("[Core] Failed to publish to JetStream: %v", err)
			h.publishDeadLetter(msg, err)
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
		jetStreamDeadLetterFails.Add(1)
		log.Printf("[Core] Failed to encode dead-letter message: %v", err)
		return
	}
	if _, err := h.js.Publish(h.deadLetterSubject, data); err != nil {
		jetStreamDeadLetterFails.Add(1)
		log.Printf("[Core] Failed to publish dead-letter message: %v", err)
		return
	}
	jetStreamDeadLetters.Add(1)
}

// GetDataCount returns the number of stored data entries
func (h *DataHandler) GetDataCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.data)
}
