package core

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

type AlarmHandlerOptions struct {
	Window            time.Duration
	RelationTypes     []RelationType
	MaxTraversalDepth int
}

type AlarmHandler struct {
	store         *Store
	publisher     *EventPublisher
	aggregator    *AlarmAggregator
	relationTypes []RelationType
	maxDepth      int
}

func NewAlarmHandler(store *Store, publisher *EventPublisher, opts AlarmHandlerOptions) *AlarmHandler {
	relationTypes := opts.RelationTypes
	if len(relationTypes) == 0 {
		relationTypes = DefaultHierarchicalRelationTypes()
	}
	maxDepth := opts.MaxTraversalDepth
	if maxDepth <= 0 {
		maxDepth = DefaultTraversalMaxDepth
	}

	aggregator := NewAlarmAggregator(store, publisher, AlarmAggregatorOptions{
		Window:        opts.Window,
		RelationTypes: relationTypes,
		MaxDepth:      maxDepth,
	})
	return &AlarmHandler{
		store:         store,
		publisher:     publisher,
		aggregator:    aggregator,
		relationTypes: append([]RelationType(nil), relationTypes...),
		maxDepth:      maxDepth,
	}
}

func (h *AlarmHandler) RegisterHandlers(nc *nats.Conn) error {
	if h == nil || nc == nil {
		return nil
	}
	if _, err := nc.Subscribe(SubjectAlarmRaised, h.handleAlarmRaised); err != nil {
		return err
	}
	log.Printf("[Alarm] Subscribed: %s", SubjectAlarmRaised)
	return nil
}

func (h *AlarmHandler) handleAlarmRaised(msg *nats.Msg) {
	var alarm Alarm
	if err := json.Unmarshal(msg.Data, &alarm); err != nil {
		log.Printf("[Alarm] Invalid alarm payload: %v", err)
		return
	}
	if err := h.Process(alarm); err != nil {
		log.Printf("[Alarm] Failed to process alarm: %v", err)
	}
}

func (h *AlarmHandler) Process(alarm Alarm) error {
	if h == nil {
		return nil
	}
	alarm = normalizeAlarm(alarm)
	if err := validateAlarm(alarm); err != nil {
		return err
	}

	impact, err := h.ComputeImpact(alarm)
	if err != nil {
		return err
	}
	h.publisher.PublishAlarmImpactComputed(impact)

	if h.aggregator != nil {
		if err := h.aggregator.Add(alarm); err != nil {
			return err
		}
	}
	return nil
}

func (h *AlarmHandler) ComputeImpact(alarm Alarm) (AlarmImpact, error) {
	if h == nil || h.store == nil {
		return AlarmImpact{}, fmt.Errorf("store is required")
	}
	asset, err := h.store.GetAsset(alarm.AssetID)
	if err != nil {
		return AlarmImpact{}, err
	}
	if asset == nil {
		return AlarmImpact{}, fmt.Errorf("asset not found: %s", alarm.AssetID)
	}

	affected, err := h.store.GetDescendants(alarm.AssetID, h.relationTypes, h.maxDepth)
	if err != nil {
		return AlarmImpact{}, err
	}
	connected, err := h.store.GetConnected(alarm.AssetID, RelationConnectedTo)
	if err != nil {
		return AlarmImpact{}, err
	}

	return AlarmImpact{
		Alarm:             alarm,
		AffectedAssets:    affected,
		AffectedAssetIDs:  assetNodeIDs(affected),
		ConnectedAssets:   connected,
		ConnectedAssetIDs: assetNodeIDs(connected),
		ComputedAt:        time.Now().UTC(),
		MaxDepth:          h.maxDepth,
	}, nil
}

func assetNodeIDs(nodes []*AssetNode) []string {
	ids := make([]string, 0, len(nodes))
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node == nil || seen[node.ID] {
			continue
		}
		seen[node.ID] = true
		ids = append(ids, node.ID)
	}
	return ids
}
