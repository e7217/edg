package core

import (
	"encoding/json"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	SubjectAssetChanged    = "platform.meta.asset.changed"
	SubjectRelationChanged = "platform.meta.relation.changed"

	SubjectAssetAncestors   = "platform.meta.asset.ancestors"
	SubjectAssetDescendants = "platform.meta.asset.descendants"
	SubjectAssetSubtree     = "platform.meta.asset.subtree"
	SubjectAssetConnected   = "platform.meta.asset.connected"

	SubjectAlarmRaised         = "platform.alarm.raised"
	SubjectAlarmImpactComputed = "platform.alarm.impact.computed"
	SubjectAlarmGrouped        = "platform.alarm.grouped"

	EventSchemaVersion = 1
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
}

func NewEventPublisher(nc *nats.Conn) *EventPublisher {
	return &EventPublisher{nc: nc}
}

func (p *EventPublisher) PublishAssetChanged(ev MetaChangeEvent) {
	p.publishMetaChange(SubjectAssetChanged, normalizeMetaChangeEvent(ev, EntityAsset))
}

func (p *EventPublisher) PublishRelationChanged(ev MetaChangeEvent) {
	p.publishMetaChange(SubjectRelationChanged, normalizeMetaChangeEvent(ev, EntityRelation))
}

func (p *EventPublisher) PublishAlarmImpactComputed(impact AlarmImpact) {
	p.publishJSON(SubjectAlarmImpactComputed, impact)
}

func (p *EventPublisher) PublishAlarmGrouped(group AlarmGroup) {
	p.publishJSON(SubjectAlarmGrouped, group)
}

func (p *EventPublisher) publishMetaChange(subject string, ev MetaChangeEvent) {
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
