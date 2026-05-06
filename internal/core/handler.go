package core

import (
	"encoding/json"
	"expvar"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// DataHandler handles NATS messages for asset data
type DataHandler struct {
	mu                sync.Mutex
	data              []AssetData           // in-memory storage (PoC)
	store             *Store                // for auto-registration
	js                nats.JetStreamContext // for publishing to JetStream
	validatedSubject  string
	deadLetterSubject string
}

var (
	jetStreamPublishFailures = expvar.NewInt("edg_core_jetstream_publish_failures")
	jetStreamDeadLetters     = expvar.NewInt("edg_core_jetstream_dead_letters")
	jetStreamDeadLetterFails = expvar.NewInt("edg_core_jetstream_dead_letter_failures")
)

// DeadLetterMessage records a failed core-to-JetStream publish attempt.
type DeadLetterMessage struct {
	OriginalSubject string          `json:"original_subject"`
	TargetSubject   string          `json:"target_subject"`
	Error           string          `json:"error"`
	Payload         json.RawMessage `json:"payload"`
	Timestamp       time.Time       `json:"timestamp"`
}

func NewDataHandler(js nats.JetStreamContext, store *Store) *DataHandler {
	return &DataHandler{
		data:              make([]AssetData, 0),
		store:             store,
		js:                js,
		validatedSubject:  DefaultValidatedDataSubject,
		deadLetterSubject: DefaultDeadLetterSubject,
	}
}

func NewDataHandlerWithSubjects(js nats.JetStreamContext, store *Store, validatedSubject, deadLetterSubject string) *DataHandler {
	handler := NewDataHandler(js, store)
	if validatedSubject != "" {
		handler.validatedSubject = validatedSubject
	}
	if deadLetterSubject != "" {
		handler.deadLetterSubject = deadLetterSubject
	}
	return handler
}

// HandleAssetData processes incoming NATS messages
func (h *DataHandler) HandleAssetData(msg *nats.Msg) {
	var data AssetData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		log.Printf("[Core] Error parsing message: %v", err)
		return
	}

	// Auto-register asset if not exists
	if h.store != nil {
		if exists, _ := h.store.AssetExists(data.AssetID); !exists {
			now := time.Now()
			asset := &Asset{
				ID:        data.AssetID,
				Name:      data.AssetID,
				Source:    SourceAuto,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := h.store.CreateAsset(asset); err == nil {
				log.Printf("[Core] Auto-registered asset: %s", data.AssetID)
			}
		}
	}

	h.mu.Lock()
	h.data = append(h.data, data)
	h.mu.Unlock()

	// Publish validated data to JetStream for persistence
	if h.js != nil {
		if _, err := h.js.Publish(h.validatedSubject, msg.Data); err != nil {
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
	if h.deadLetterSubject == "" {
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
