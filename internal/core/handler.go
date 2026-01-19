package core

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// DataHandler handles NATS messages for asset data
type DataHandler struct {
	mu    sync.Mutex
	data  []AssetData            // in-memory storage (PoC)
	store *Store                 // for auto-registration
	js    nats.JetStreamContext  // for publishing to JetStream
}

func NewDataHandler(js nats.JetStreamContext, store *Store) *DataHandler {
	return &DataHandler{
		data:  make([]AssetData, 0),
		store: store,
		js:    js,
	}
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
			asset := &Asset{
				ID:        data.AssetID,
				Name:      data.AssetID,
				CreatedAt: time.Now(),
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
		if _, err := h.js.Publish("platform.data.validated", msg.Data); err != nil {
			log.Printf("[Core] Failed to publish to JetStream: %v", err)
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

// GetDataCount returns the number of stored data entries
func (h *DataHandler) GetDataCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.data)
}
