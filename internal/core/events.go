package core

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	SubjectAssetChanged    = "platform.meta.asset.changed"
	SubjectRelationChanged = "platform.meta.relation.changed"
	// SubjectPointsChanged announces a replaced or deleted point list. The
	// data contract's declaration cache flushes on it, and it is the signal an
	// adapter re-reads its point list on.
	SubjectPointsChanged = "platform.meta.points.changed"

	SubjectAssetAncestors   = "platform.meta.asset.ancestors"
	SubjectAssetDescendants = "platform.meta.asset.descendants"
	SubjectAssetSubtree     = "platform.meta.asset.subtree"
	SubjectAssetConnected   = "platform.meta.asset.connected"

	SubjectAlarmRaised         = "platform.alarm.raised"
	SubjectAlarmImpactComputed = "platform.alarm.impact.computed"
	SubjectAlarmGrouped        = "platform.alarm.grouped"

	SubjectConstraintsViolation = "platform.meta.constraints.violation"

	EventSchemaVersion = 1
)

// Adapter runtime-status plane (ADR 0008). A fourth plane alongside data,
// meta and alarm: platform.meta.* is declarative master data, platform.data.*
// is telemetry, and this is volatile runtime state.
//
// It is deliberately not under platform.data.>, which the PLATFORM_DATA stream
// captures and persists for 7 days (ADR 0001/0005) — heartbeats would pollute
// it. Nor under platform.meta.*, whose subscribers use the
// platform.meta.*.changed wildcard and would drown in heartbeat traffic.
const (
	// SubjectAdapterStatusPrefix is completed with the adapter_id, which is
	// authoritative for identity.
	SubjectAdapterStatusPrefix = "platform.adapter.status."
	SubjectAdapterStatusAll    = "platform.adapter.status.>"
	// SubjectAdapterHello asks every adapter to re-announce. Published by core
	// at boot so a restart does not leave the registry blind until the next
	// heartbeat.
	SubjectAdapterHello = "platform.adapter.hello"
	// SubjectAdapterPingPrefix is completed with the adapter_id. Core is the
	// requester; the adapter answers. Turns a missed heartbeat into a
	// confirmed death instead of a guess.
	SubjectAdapterPingPrefix = "platform.adapter.ping."
	// SubjectAdapterChanged carries transitions only, never every heartbeat.
	SubjectAdapterChanged = "platform.adapter.changed"
	// SubjectAdapterPongPrefix is the reply subject a probed adapter answers
	// on, completed with "<adapter_id>.<nonce>".
	//
	// The probe deliberately does not use the default _INBOX: ADR 0007 denies
	// _INBOX publish to the adapter role so that an adapter cannot race core
	// to answer somebody else's metadata request. Giving adapters a dedicated
	// reply namespace keeps that property while still letting them answer a
	// liveness probe.
	SubjectAdapterPongPrefix = "platform.adapter.pong."
	SubjectAdapterPongAll    = "platform.adapter.pong.>"
	// SubjectAdapterList is a request/reply snapshot, mirroring the
	// platform.meta.asset.list convention.
	SubjectAdapterList = "platform.adapter.list"
)

type EventType string

const (
	EventCreated EventType = "created"
	EventUpdated EventType = "updated"
	EventDeleted EventType = "deleted"
)

type EntityType string

const (
	EntityAsset    EntityType = "asset"
	EntityRelation EntityType = "relation"
	EntityPoints   EntityType = "points"
)

type MetaChangeEvent struct {
	SchemaVersion int        `json:"schema_version"`
	EventType     EventType  `json:"event_type"`
	EntityType    EntityType `json:"entity_type"`
	EntityID      string     `json:"entity_id"`
	Source        string     `json:"source"`
	Timestamp     time.Time  `json:"timestamp"`
	Before        any        `json:"before,omitempty"`
	After         any        `json:"after,omitempty"`
}

type EventPublisher struct {
	nc *nats.Conn

	// recording, when set, keeps events in memory instead of sending them.
	// The CLI import paths write through MetadataService like the API does,
	// but have no core connection to publish on; they record, then tell a
	// running core what changed (SubjectImportApplied).
	recording *eventRecording
}

// RecordedEvent is one event a recording publisher captured.
type RecordedEvent struct {
	Subject string
	Event   MetaChangeEvent
}

type eventRecording struct {
	mu     sync.Mutex
	events []RecordedEvent
}

func NewEventPublisher(nc *nats.Conn) *EventPublisher {
	return &EventPublisher{nc: nc}
}

// NewRecordingEventPublisher returns a publisher that sends nothing and
// remembers every metadata change event. Other events are dropped, as they
// are without a connection.
func NewRecordingEventPublisher() *EventPublisher {
	return &EventPublisher{recording: &eventRecording{}}
}

// Recorded returns the captured metadata change events in order.
func (p *EventPublisher) Recorded() []RecordedEvent {
	if p == nil || p.recording == nil {
		return nil
	}
	p.recording.mu.Lock()
	defer p.recording.mu.Unlock()
	return append([]RecordedEvent(nil), p.recording.events...)
}

func (p *EventPublisher) PublishAssetChanged(ev MetaChangeEvent) {
	p.publishMetaChange(SubjectAssetChanged, normalizeMetaChangeEvent(ev, EntityAsset))
}

func (p *EventPublisher) PublishRelationChanged(ev MetaChangeEvent) {
	p.publishMetaChange(SubjectRelationChanged, normalizeMetaChangeEvent(ev, EntityRelation))
}

// PublishPointsChanged announces a point-list write. EntityID is the asset id.
func (p *EventPublisher) PublishPointsChanged(ev MetaChangeEvent) {
	p.publishMetaChange(SubjectPointsChanged, normalizeMetaChangeEvent(ev, EntityPoints))
}

func (p *EventPublisher) PublishAlarmImpactComputed(impact AlarmImpact) {
	p.publishJSON(SubjectAlarmImpactComputed, impact)
}

func (p *EventPublisher) PublishAlarmGrouped(group AlarmGroup) {
	p.publishJSON(SubjectAlarmGrouped, group)
}

// PublishAdapterChanged emits an adapter runtime transition. Best-effort like
// every other event here: a subscriber that misses one reconciles with
// platform.adapter.list.
func (p *EventPublisher) PublishAdapterChanged(ev AdapterChangeEvent) {
	p.publishJSON(SubjectAdapterChanged, ev)
}

func (p *EventPublisher) PublishConstraintViolation(violation ConstraintViolation) {
	p.publishJSON(SubjectConstraintsViolation, violation)
}

func (p *EventPublisher) publishMetaChange(subject string, ev MetaChangeEvent) {
	if p != nil && p.recording != nil {
		p.recording.mu.Lock()
		p.recording.events = append(p.recording.events, RecordedEvent{Subject: subject, Event: ev})
		p.recording.mu.Unlock()
		return
	}
	p.publishJSON(subject, ev)
}

func (p *EventPublisher) publishJSON(subject string, payload any) {
	if p == nil || p.nc == nil {
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[Events] Failed to marshal event for %s: %v", subject, err)
		return
	}

	if err := p.nc.Publish(subject, data); err != nil {
		log.Printf("[Events] Failed to publish event to %s: %v", subject, err)
	}
}

func normalizeMetaChangeEvent(ev MetaChangeEvent, entityType EntityType) MetaChangeEvent {
	if ev.SchemaVersion == 0 {
		ev.SchemaVersion = EventSchemaVersion
	}
	if ev.EntityType == "" {
		ev.EntityType = entityType
	}
	if ev.Source == "" {
		ev.Source = SourceManual
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	return ev
}
