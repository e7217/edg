package core

import (
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
)

// EnricherOptions configures ontology-based metadata enrichment.
type EnricherOptions struct {
	RelationTypes []RelationType
	MaxDepth      int
}

// EnricherStats reports in-memory cache state.
type EnricherStats struct {
	CacheHits    uint64
	CacheMisses  uint64
	CacheEntries int
}

// Enricher adds ancestor-derived metadata to asset data.
type Enricher struct {
	store         *Store
	relationTypes []RelationType
	maxDepth      int

	mu            sync.RWMutex
	cache         map[string]map[string]string
	cacheHits     uint64
	cacheMisses   uint64
	cacheVersion  uint64
	subscriptions []*nats.Subscription
}

// NewEnricher creates an ontology-based asset data enricher.
func NewEnricher(store *Store, opts EnricherOptions) *Enricher {
	relationTypes := opts.RelationTypes
	if len(relationTypes) == 0 {
		relationTypes = DefaultHierarchicalRelationTypes()
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultTraversalMaxDepth
	}

	return &Enricher{
		store:         store,
		relationTypes: append([]RelationType(nil), relationTypes...),
		maxDepth:      maxDepth,
		cache:         make(map[string]map[string]string),
	}
}

// Enrich adds ancestor tags to data.Metadata without overwriting existing keys.
func (e *Enricher) Enrich(data *AssetData) error {
	if e == nil || e.store == nil || data == nil || data.AssetID == "" {
		return nil
	}

	tags, err := e.tagsForAsset(data.AssetID)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return nil
	}

	if data.Metadata == nil {
		data.Metadata = make(map[string]string, len(tags))
	}
	for key, value := range tags {
		if _, exists := data.Metadata[key]; exists {
			continue
		}
		data.Metadata[key] = value
	}
	return nil
}

// OnMetaChange flushes cached enrichment tags after metadata changes.
func (e *Enricher) OnMetaChange(subject string, _ []byte) {
	if e == nil {
		return
	}
	switch subject {
	case SubjectAssetChanged, SubjectRelationChanged:
		e.Flush()
	}
}

// Start subscribes to metadata change events for cache invalidation.
func (e *Enricher) Start(nc *nats.Conn) error {
	if e == nil || nc == nil {
		return nil
	}

	for _, subject := range []string{SubjectAssetChanged, SubjectRelationChanged} {
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			e.OnMetaChange(msg.Subject, msg.Data)
		})
		if err != nil {
			return fmt.Errorf("failed to subscribe enricher to %s: %w", subject, err)
		}
		e.mu.Lock()
		e.subscriptions = append(e.subscriptions, sub)
		e.mu.Unlock()
	}
	return nil
}

// Stop unsubscribes metadata change listeners created by Start.
func (e *Enricher) Stop() error {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	subs := append([]*nats.Subscription(nil), e.subscriptions...)
	e.subscriptions = nil
	e.mu.Unlock()

	for _, sub := range subs {
		if err := sub.Unsubscribe(); err != nil {
			return err
		}
	}
	return nil
}

// Flush clears cached enrichment tags.
func (e *Enricher) Flush() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.cache = make(map[string]map[string]string)
	e.cacheVersion++
}

// Stats returns a snapshot of cache counters.
func (e *Enricher) Stats() EnricherStats {
	if e == nil {
		return EnricherStats{}
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	return EnricherStats{
		CacheHits:    e.cacheHits,
		CacheMisses:  e.cacheMisses,
		CacheEntries: len(e.cache),
	}
}

func (e *Enricher) tagsForAsset(assetID string) (map[string]string, error) {
	e.mu.Lock()
	if cached, ok := e.cache[assetID]; ok {
		e.cacheHits++
		tags := cloneStringMap(cached)
		e.mu.Unlock()
		return tags, nil
	}
	e.cacheMisses++
	cacheVersion := e.cacheVersion
	e.mu.Unlock()

	ancestors, err := e.store.GetAncestors(assetID, e.relationTypes, e.maxDepth)
	if err != nil {
		return nil, err
	}
	tags := tagsFromAncestors(ancestors)

	e.mu.Lock()
	if e.cacheVersion == cacheVersion {
		e.cache[assetID] = cloneStringMap(tags)
	}
	e.mu.Unlock()

	return tags, nil
}

func tagsFromAncestors(ancestors []*AssetNode) map[string]string {
	tags := make(map[string]string)
	for _, ancestor := range ancestors {
		key := ancestor.TemplateName
		if key == "" {
			key = fmt.Sprintf("ancestor_%d", ancestor.Depth)
		}
		if _, exists := tags[key]; exists {
			continue
		}
		tags[key] = ancestor.Name
	}
	return tags
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
