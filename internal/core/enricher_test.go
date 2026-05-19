package core

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnricher_EmptyGraphNoop(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	enricher := NewEnricher(store, EnricherOptions{})
	data := &AssetData{AssetID: "sensor-001"}

	require.NoError(t, enricher.Enrich(data))
	assert.Nil(t, data.Metadata)
}

func TestEnricher_AddsAncestorMetadata(t *testing.T) {
	store := newTraversalTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{})

	data := &AssetData{
		AssetID:  "sensor-001",
		Metadata: map[string]string{"source": "adapter"},
	}

	require.NoError(t, enricher.Enrich(data))

	assert.Equal(t, "adapter", data.Metadata["source"])
	assert.Equal(t, "Pump A", data.Metadata["equipment"])
	assert.Equal(t, "Line 3", data.Metadata["line"])
	assert.Equal(t, "Factory 1", data.Metadata["factory"])
	assert.Equal(t, "Room 9", data.Metadata["room"])
}

func TestEnricher_MaxDepth(t *testing.T) {
	store := newTraversalTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{
		RelationTypes: []RelationType{RelationPartOf},
		MaxDepth:      2,
	})

	data := &AssetData{AssetID: "sensor-001"}
	require.NoError(t, enricher.Enrich(data))

	require.NotNil(t, data.Metadata)
	assert.Equal(t, "Pump A", data.Metadata["equipment"])
	assert.Equal(t, "Line 3", data.Metadata["line"])
	assert.NotContains(t, data.Metadata, "factory")
}

func TestEnricher_CacheHitMiss(t *testing.T) {
	store := newTraversalTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{})

	require.NoError(t, enricher.Enrich(&AssetData{AssetID: "sensor-001"}))
	stats := enricher.Stats()
	assert.Equal(t, uint64(0), stats.CacheHits)
	assert.Equal(t, uint64(1), stats.CacheMisses)
	assert.Equal(t, 1, stats.CacheEntries)

	require.NoError(t, enricher.Enrich(&AssetData{AssetID: "sensor-001"}))
	stats = enricher.Stats()
	assert.Equal(t, uint64(1), stats.CacheHits)
	assert.Equal(t, uint64(1), stats.CacheMisses)
	assert.Equal(t, 1, stats.CacheEntries)
}

func TestEnricher_OnMetaChangeFlushesCache(t *testing.T) {
	store := newTraversalTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{})

	require.NoError(t, enricher.Enrich(&AssetData{AssetID: "sensor-001"}))
	assert.Equal(t, 1, enricher.Stats().CacheEntries)

	enricher.OnMetaChange(SubjectRelationChanged, nil)

	assert.Equal(t, 0, enricher.Stats().CacheEntries)
}

func TestEnricher_StartSubscribesToMetaChanges(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store := newTraversalTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{})
	require.NoError(t, enricher.Start(nc))
	require.NoError(t, nc.Flush())

	require.NoError(t, enricher.Enrich(&AssetData{AssetID: "sensor-001"}))
	assert.Equal(t, 1, enricher.Stats().CacheEntries)

	nc.Publish(SubjectAssetChanged, []byte(`{}`))
	require.Eventually(t, func() bool {
		return enricher.Stats().CacheEntries == 0
	}, time.Second, 10*time.Millisecond)
}

func TestEnricher_ConcurrentAccess(t *testing.T) {
	store := newTraversalTestStore(t)
	enricher := NewEnricher(store, EnricherOptions{})

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := &AssetData{AssetID: "sensor-001"}
			errs <- enricher.Enrich(data)
			assert.Equal(t, "Factory 1", data.Metadata["factory"])
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}
