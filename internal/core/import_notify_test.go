package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveCore is the part of a running edg-core that caches master data: the
// template loader, the data contract, and the meta handler that answers
// SubjectImportApplied. It has its own connection to the database file, as
// the real process does.
type liveCore struct {
	store    *Store
	loader   *TemplateLoader
	contract *ContractChecker
}

func startLiveCore(t *testing.T, dbPath string) (*liveCore, *nats.Conn) {
	t.Helper()
	_, nc, _ := startTestNATSServer(t, false)
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	loader, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	contract := NewContractChecker(store, DataContractEnforce)
	require.NoError(t, contract.Start(nc))
	t.Cleanup(func() { _ = contract.Stop() })
	handler := NewMetaHandlerWithOptions(store, loader, MetaHandlerOptions{
		Events:   NewEventPublisher(nc),
		Contract: contract,
	})
	require.NoError(t, handler.RegisterHandlers(nc))
	return &liveCore{store: store, loader: loader, contract: contract}, nc
}

// cliService is what -import-plant builds: a second connection to the same
// file, writing through MetadataService with a recording publisher.
func cliService(t *testing.T, dbPath string) (*MetadataService, *EventPublisher) {
	t.Helper()
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	loader, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	rec := NewRecordingEventPublisher()
	return NewMetadataService(store, loader, rec, ConstraintsEnforcementWarn), rec
}

func declaredTags(t *testing.T, c *ContractChecker, assetID string) []string {
	t.Helper()
	p, err := c.Profile(assetID)
	require.NoError(t, err)
	var names []string
	for name := range p.Tags {
		names = append(names, name)
	}
	return names
}

// #150: a CLI import used to be invisible to a running core until restart.
// With the notification, the core's caches follow and adapters are told.
func TestCLIImportReachesRunningCore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metadata.db")

	// The plant as it was when core started.
	first, _ := cliService(t, dbPath)
	bundle := seedBundle(t)
	_, err := first.ImportPlant(bundle)
	require.NoError(t, err)

	core, nc := startLiveCore(t, dbPath)
	pointsEvents := subscribeMetaEvents(t, nc, SubjectPointsChanged)
	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	assert.ElementsMatch(t, []string{"temperature", "flow"}, declaredTags(t, core.contract, "pump-a"),
		"core has cached pump-a's declarations")

	// An operator adds a template and a point, and imports again.
	write(t, bundle, "templates/valve.yaml", "name: valve\nresources: []\n")
	write(t, bundle, "points.csv", "asset_id,protocol,poll_interval_ms,name,value_type,unit,address,enabled,encoding.type,encoding.scale,encoding.function\n"+
		"pump-a,modbus-tcp,1000,temperature,NUMBER,°C,0,true,int16,0.1,holding\n"+
		"pump-a,,,flow,NUMBER,L/min,100,,float32,,input\n"+
		"pump-a,,,vibration,NUMBER,mm/s,200,,float32,,input\n"+
		"pump-b,modbus-tcp,,temperature,NUMBER,°C,0,false,int16,0.1,holding\n")
	svc, rec := cliService(t, dbPath)
	_, err = svc.ImportPlant(bundle)
	require.NoError(t, err)

	// Without a notification the running core does not know (the bug).
	assert.False(t, core.loader.Exists("valve"))
	assert.NotContains(t, declaredTags(t, core.contract, "pump-a"), "vibration")

	batches := BuildImportApplied("cli:import-plant", rec.Recorded(), true)
	announced, err := NotifyImportApplied(nc, batches, time.Second)
	require.NoError(t, err)
	assert.Equal(t, 1, announced, "only pump-a's list changed")

	ev := requireMetaEvent(t, pointsEvents)
	assert.Equal(t, "pump-a", ev.EntityID)
	assert.Equal(t, EventUpdated, ev.EventType)
	assert.Equal(t, "cli:import-plant", ev.Source)
	assert.Empty(t, ev.Before, "core announces what the database holds, not what the CLI claims was there")
	var after PointList
	require.NoError(t, json.Unmarshal(ev.After, &after))
	assert.Equal(t, 2, after.Version, "the version an adapter compares against")
	requireNoMetaEvent(t, pointsEvents)
	requireNoMetaEvent(t, assetEvents)

	assert.True(t, core.loader.Exists("valve"), "the template cache was reloaded")
	assert.Contains(t, declaredTags(t, core.contract, "pump-a"), "vibration", "the contract cache was flushed")
}

func TestBuildImportAppliedDeduplicatesAndBatches(t *testing.T) {
	var recorded []RecordedEvent
	add := func(subject, id string, et EventType) {
		recorded = append(recorded, RecordedEvent{Subject: subject, Event: MetaChangeEvent{EntityID: id, EventType: et}})
	}
	add(SubjectAssetChanged, "a1", EventCreated)
	add(SubjectAssetChanged, "a1", EventUpdated) // created then updated in one import is still new
	add(SubjectRelationChanged, "r1", EventCreated)
	add(SubjectPointsChanged, "a1", EventCreated)
	for i := 0; i < ImportBatchSize; i++ {
		add(SubjectPointsChanged, fmt.Sprintf("p%04d", i), EventUpdated)
	}

	batches := BuildImportApplied("cli:import-points", recorded, false)
	require.Len(t, batches, 2)
	total := 0
	for _, b := range batches {
		assert.Equal(t, EventSchemaVersion, b.SchemaVersion)
		assert.Equal(t, "cli:import-points", b.Source)
		n := len(b.Assets) + len(b.Relations) + len(b.Points)
		assert.LessOrEqual(t, n, ImportBatchSize)
		total += n
	}
	assert.Equal(t, 1+1+1+ImportBatchSize, total)
	assert.Equal(t, []ImportedEntity{{ID: "a1", EventType: EventCreated}}, batches[0].Assets)
}

func TestBuildImportAppliedWithNothingChanged(t *testing.T) {
	assert.Empty(t, BuildImportApplied("cli:import-plant", nil, false))
	only := BuildImportApplied("cli:import-templates", nil, true)
	require.Len(t, only, 1)
	assert.True(t, only[0].TemplatesChanged)
}

// An entity gone by the time core reads it is announced as deleted, and a
// relation is announced with the database's copy.
func TestImportAppliedHandlerReadsTheDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metadata.db")
	core, nc := startLiveCore(t, dbPath)
	relEvents := subscribeMetaEvents(t, nc, SubjectRelationChanged)
	pointsEvents := subscribeMetaEvents(t, nc, SubjectPointsChanged)

	require.NoError(t, core.store.CreateAsset(&Asset{ID: "a", Name: "A", Source: SourceManual}))
	require.NoError(t, core.store.CreateAsset(&Asset{ID: "b", Name: "B", Source: SourceManual}))
	rel := &AssetRelation{ID: "r1", SourceAssetID: "a", TargetAssetID: "b", RelationType: RelationPartOf}
	require.NoError(t, core.store.CreateRelation(rel))

	announced, err := NotifyImportApplied(nc, []ImportAppliedRequest{{
		SchemaVersion: EventSchemaVersion,
		Source:        "cli:import-plant",
		Relations:     []ImportedEntity{{ID: "r1", EventType: EventCreated}},
		Points:        []ImportedEntity{{ID: "a", EventType: EventUpdated}},
	}}, time.Second)
	require.NoError(t, err)
	assert.Equal(t, 2, announced)

	ev := requireMetaEvent(t, relEvents)
	assert.Equal(t, EventCreated, ev.EventType)
	assert.Contains(t, string(ev.After), `"target_asset_id":"b"`)

	ev = requireMetaEvent(t, pointsEvents)
	assert.Equal(t, EventDeleted, ev.EventType, "a has no point list any more")
	assert.Empty(t, ev.After)
}

func TestImportAppliedHandlerRejectsAMalformedRequest(t *testing.T) {
	_, nc := startLiveCore(t, filepath.Join(t.TempDir(), "metadata.db"))
	msg, err := nc.Request(SubjectImportApplied, []byte("{not json"), time.Second)
	require.NoError(t, err)
	var resp testMetaResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.Error)
}

// Nobody listening is an error the CLI turns into a restart hint.
func TestNotifyImportAppliedWithoutCore(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	_, err := NotifyImportApplied(nc, []ImportAppliedRequest{{Source: "x", TemplatesChanged: true}}, 200*time.Millisecond)
	require.Error(t, err)
}
