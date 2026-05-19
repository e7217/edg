package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const DefaultAlarmWindow = 5 * time.Second

type AlarmGroupPublisher interface {
	PublishAlarmGrouped(group AlarmGroup)
}

type AlarmAggregatorOptions struct {
	Window        time.Duration
	RelationTypes []RelationType
	MaxDepth      int
}

type AlarmAggregator struct {
	store         *Store
	publisher     AlarmGroupPublisher
	window        time.Duration
	relationTypes []RelationType
	maxDepth      int

	mu     sync.Mutex
	groups map[string]*pendingAlarmGroup
}

type pendingAlarmGroup struct {
	key             string
	groupAsset      *AssetNode
	alarms          []Alarm
	assetIDs        []string
	assetIDSet      map[string]bool
	windowStartedAt time.Time
	timer           *time.Timer
}

func NewAlarmAggregator(store *Store, publisher AlarmGroupPublisher, opts AlarmAggregatorOptions) *AlarmAggregator {
	window := opts.Window
	if window <= 0 {
		window = DefaultAlarmWindow
	}
	relationTypes := opts.RelationTypes
	if len(relationTypes) == 0 {
		relationTypes = DefaultHierarchicalRelationTypes()
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultTraversalMaxDepth
	}

	return &AlarmAggregator{
		store:         store,
		publisher:     publisher,
		window:        window,
		relationTypes: append([]RelationType(nil), relationTypes...),
		maxDepth:      maxDepth,
		groups:        make(map[string]*pendingAlarmGroup),
	}
}

func (a *AlarmAggregator) Add(alarm Alarm) error {
	if a == nil {
		return nil
	}
	alarm = normalizeAlarm(alarm)

	a.mu.Lock()
	defer a.mu.Unlock()

	group, lca, err := a.findGroupForAssetLocked(alarm.AssetID)
	if err != nil {
		return err
	}
	if group == nil {
		group = a.newPendingGroupLocked(alarm)
		return nil
	}

	oldKey := group.key
	group.alarms = append(group.alarms, alarm)
	if !group.assetIDSet[alarm.AssetID] {
		group.assetIDSet[alarm.AssetID] = true
		group.assetIDs = append(group.assetIDs, alarm.AssetID)
	}
	if lca != nil {
		group.groupAsset = lca
		group.key = lca.ID
	}

	if oldKey != group.key {
		delete(a.groups, oldKey)
		if existing := a.groups[group.key]; existing != nil && existing != group {
			a.mergeGroupsLocked(existing, group)
			group = existing
		}
		a.groups[group.key] = group
	}
	return nil
}

func (a *AlarmAggregator) newPendingGroupLocked(alarm Alarm) *pendingAlarmGroup {
	now := time.Now().UTC()
	groupAsset := a.assetNodeFor(alarm.AssetID)
	group := &pendingAlarmGroup{
		key:             groupAsset.ID,
		groupAsset:      groupAsset,
		alarms:          []Alarm{alarm},
		assetIDs:        []string{alarm.AssetID},
		assetIDSet:      map[string]bool{alarm.AssetID: true},
		windowStartedAt: now,
	}
	group.timer = time.AfterFunc(a.window, func() {
		a.flush(group)
	})
	a.groups[group.key] = group
	return group
}

func (a *AlarmAggregator) findGroupForAssetLocked(assetID string) (*pendingAlarmGroup, *AssetNode, error) {
	if a.store == nil {
		return nil, nil, nil
	}

	var selected *pendingAlarmGroup
	var selectedLCA *AssetNode
	for _, group := range a.groups {
		candidates := append([]string{}, group.assetIDs...)
		candidates = append(candidates, assetID)
		lca, err := a.store.FindLowestCommonAncestor(candidates, a.relationTypes, a.maxDepth)
		if err != nil {
			return nil, nil, err
		}
		if lca == nil {
			continue
		}
		if selectedLCA == nil || lca.Depth < selectedLCA.Depth || (lca.Depth == selectedLCA.Depth && lca.ID < selectedLCA.ID) {
			selected = group
			selectedLCA = lca
		}
	}
	return selected, selectedLCA, nil
}

func (a *AlarmAggregator) mergeGroupsLocked(dst, src *pendingAlarmGroup) {
	if src.timer != nil {
		src.timer.Stop()
	}
	dst.alarms = append(dst.alarms, src.alarms...)
	for _, assetID := range src.assetIDs {
		if dst.assetIDSet[assetID] {
			continue
		}
		dst.assetIDSet[assetID] = true
		dst.assetIDs = append(dst.assetIDs, assetID)
	}
}

func (a *AlarmAggregator) flush(group *pendingAlarmGroup) {
	a.mu.Lock()
	if current := a.groups[group.key]; current != group {
		a.mu.Unlock()
		return
	}
	delete(a.groups, group.key)
	alarmGroup := group.toAlarmGroup(time.Now().UTC())
	a.mu.Unlock()

	if a.publisher != nil {
		a.publisher.PublishAlarmGrouped(alarmGroup)
	}
}

func (g *pendingAlarmGroup) toAlarmGroup(now time.Time) AlarmGroup {
	alarmIDs := make([]string, 0, len(g.alarms))
	alarms := make([]Alarm, len(g.alarms))
	copy(alarms, g.alarms)
	for _, alarm := range g.alarms {
		alarmIDs = append(alarmIDs, alarm.ID)
	}
	assetIDs := append([]string(nil), g.assetIDs...)

	group := AlarmGroup{
		ID:              uuid.New().String(),
		Severity:        maxAlarmSeverity(g.alarms),
		AlarmCount:      len(g.alarms),
		AlarmIDs:        alarmIDs,
		AssetIDs:        assetIDs,
		Alarms:          alarms,
		WindowStartedAt: g.windowStartedAt,
		WindowEndedAt:   now,
		CreatedAt:       now,
	}
	if g.groupAsset != nil {
		group.GroupAssetID = g.groupAsset.ID
		group.GroupAssetName = g.groupAsset.Name
		group.GroupTemplateName = g.groupAsset.TemplateName
	}
	return group
}

func (a *AlarmAggregator) assetNodeFor(assetID string) *AssetNode {
	if a.store != nil {
		asset, err := a.store.GetAsset(assetID)
		if err == nil && asset != nil {
			return &AssetNode{
				ID:           asset.ID,
				Name:         asset.Name,
				TemplateName: asset.TemplateName,
				Depth:        0,
			}
		}
	}
	return &AssetNode{ID: assetID, Name: assetID, Depth: 0}
}

func normalizeAlarm(alarm Alarm) Alarm {
	if alarm.ID == "" {
		alarm.ID = uuid.New().String()
	}
	if alarm.Timestamp.IsZero() {
		alarm.Timestamp = time.Now().UTC()
	}
	if alarm.Severity == "" {
		alarm.Severity = SeverityInfo
	}
	if alarm.Metadata == nil {
		alarm.Metadata = map[string]string{}
	}
	return alarm
}

func validateAlarm(alarm Alarm) error {
	if alarm.AssetID == "" {
		return fmt.Errorf("asset_id is required")
	}
	if alarm.Severity == "" {
		return nil
	}
	if !alarm.Severity.IsValid() {
		return fmt.Errorf("invalid severity: %s", alarm.Severity)
	}
	return nil
}
