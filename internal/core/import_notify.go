package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

// SubjectImportApplied tells a running core that a CLI import wrote master
// data straight to the database (#150).
//
// The CLI cannot announce the writes itself: platform.meta.*.changed is
// core-only (ADR 0007). So it sends only which entities changed, and core
// re-reads each one and publishes the change event under its own identity.
// What an event says is therefore always what the database holds.
const SubjectImportApplied = "platform.meta.import.applied"

// ImportBatchSize bounds the entities per notification, keeping a request
// well under NATS's default 1 MB max_payload for any realistic id length.
const ImportBatchSize = 1000

// ImportAppliedRequest lists what one import changed.
type ImportAppliedRequest struct {
	SchemaVersion int    `json:"schema_version"`
	Source        string `json:"source"`
	// TemplatesChanged makes core reload its template cache. There is no
	// per-template event: nothing subscribes to one.
	TemplatesChanged bool             `json:"templates_changed,omitempty"`
	Assets           []ImportedEntity `json:"assets,omitempty"`
	Relations        []ImportedEntity `json:"relations,omitempty"`
	Points           []ImportedEntity `json:"points,omitempty"`
}

// ImportedEntity is one changed entity. EventType is what the import did to
// it; core still reads the entity back, and announces a deletion if it is
// gone by then.
type ImportedEntity struct {
	ID        string    `json:"id"`
	EventType EventType `json:"event_type"`
}

// ImportAppliedResult is core's reply.
type ImportAppliedResult struct {
	// Announced counts the change events core published.
	Announced int `json:"announced"`
}

func (r ImportAppliedRequest) empty() bool {
	return !r.TemplatesChanged && len(r.Assets)+len(r.Relations)+len(r.Points) == 0
}

// BuildImportApplied turns what a recording publisher captured during an
// import into notification batches. Only entities that were written appear:
// an unchanged row never reaches the service, so it records nothing.
//
// An entity written twice is listed once, keeping the first event type: an
// asset created and then updated by the same import is new to everyone else.
func BuildImportApplied(source string, recorded []RecordedEvent, templatesChanged bool) []ImportAppliedRequest {
	type key struct{ subject, id string }
	seen := make(map[key]bool, len(recorded))
	var assets, relations, points []ImportedEntity
	for _, r := range recorded {
		k := key{r.Subject, r.Event.EntityID}
		if seen[k] {
			continue
		}
		seen[k] = true
		e := ImportedEntity{ID: r.Event.EntityID, EventType: r.Event.EventType}
		switch r.Subject {
		case SubjectAssetChanged:
			assets = append(assets, e)
		case SubjectRelationChanged:
			relations = append(relations, e)
		case SubjectPointsChanged:
			points = append(points, e)
		}
	}

	var batches []ImportAppliedRequest
	cur := ImportAppliedRequest{SchemaVersion: EventSchemaVersion, Source: source, TemplatesChanged: templatesChanged}
	size := 0
	flush := func() {
		if !cur.empty() {
			batches = append(batches, cur)
		}
		cur = ImportAppliedRequest{SchemaVersion: EventSchemaVersion, Source: source}
		size = 0
	}
	// Assets first: a relation or point list is about an asset, and a
	// subscriber handling events in order should learn of the asset first.
	for _, group := range []struct {
		list []ImportedEntity
		dst  func(*ImportAppliedRequest) *[]ImportedEntity
	}{
		{assets, func(r *ImportAppliedRequest) *[]ImportedEntity { return &r.Assets }},
		{relations, func(r *ImportAppliedRequest) *[]ImportedEntity { return &r.Relations }},
		{points, func(r *ImportAppliedRequest) *[]ImportedEntity { return &r.Points }},
	} {
		for _, e := range group.list {
			if size == ImportBatchSize {
				flush()
			}
			dst := group.dst(&cur)
			*dst = append(*dst, e)
			size++
		}
	}
	flush()
	return batches
}

// NotifyImportApplied sends the batches to a running core, in order, and
// returns how many change events core published. An error means core did not
// confirm every batch -- it is not running, is too old to know the subject, or
// refused the request. The import itself has already committed either way.
func NotifyImportApplied(nc *nats.Conn, batches []ImportAppliedRequest, timeout time.Duration) (announced int, err error) {
	for _, b := range batches {
		data, err := json.Marshal(b)
		if err != nil {
			return announced, err
		}
		msg, err := nc.Request(SubjectImportApplied, data, timeout)
		if err != nil {
			return announced, err
		}
		var resp struct {
			Success bool                `json:"success"`
			Data    ImportAppliedResult `json:"data"`
			Error   string              `json:"error"`
		}
		if err := json.Unmarshal(msg.Data, &resp); err != nil {
			return announced, fmt.Errorf("unreadable reply from core: %w", err)
		}
		if !resp.Success {
			return announced, errors.New(resp.Error)
		}
		announced += resp.Data.Announced
	}
	return announced, nil
}

// handleImportApplied re-announces what a CLI import changed. Each entity is
// read back from the database, so the event carries the stored state and never
// the requester's account of it; there is no before snapshot. An entity gone by
// then is announced as deleted.
func (h *MetaHandler) handleImportApplied(msg *nats.Msg) {
	var req ImportAppliedRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request: " + err.Error()})
		return
	}

	if req.TemplatesChanged {
		if err := h.loader.Reload(); err != nil {
			h.reply(msg, Response{Success: false, Error: err.Error()})
			return
		}
		// Declared tags come partly from templates, and no event covers them.
		if h.contract != nil {
			h.contract.Flush()
		}
	}

	announced := 0
	announce := func(list []ImportedEntity, read func(id string) (any, error), publish func(MetaChangeEvent)) error {
		for _, e := range list {
			current, err := read(e.ID)
			if err != nil {
				return err
			}
			ev := MetaChangeEvent{EventType: e.EventType, EntityID: e.ID, Source: req.Source, After: current}
			if current == nil {
				ev.EventType = EventDeleted
			}
			if a, ok := current.(*Asset); ok {
				ev.Source = a.Source // as asset events always carry it
			}
			publish(ev)
			announced++
		}
		return nil
	}
	err := announce(req.Assets, func(id string) (any, error) {
		a, err := h.store.GetAsset(id)
		if a == nil {
			return nil, err
		}
		return a, err
	}, h.events.PublishAssetChanged)
	if err == nil {
		err = announce(req.Relations, func(id string) (any, error) {
			r, err := h.store.GetRelation(id)
			if r == nil {
				return nil, err
			}
			return r, err
		}, h.events.PublishRelationChanged)
	}
	if err == nil {
		err = announce(req.Points, func(id string) (any, error) {
			pl, err := h.store.GetPointList(id)
			return pointListOrNil(pl), err
		}, h.events.PublishPointsChanged)
	}
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Import from %s applied: %d change event(s), templates reloaded: %t",
		req.Source, announced, req.TemplatesChanged)
	h.reply(msg, Response{Success: true, Data: ImportAppliedResult{Announced: announced}})
}
