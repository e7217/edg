package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An adapter provisions itself over platform.meta.points.get (ADR 0011).
func TestPointsGet(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	handler := NewMetaHandler(store, NewTemplateLoader())
	require.NoError(t, handler.RegisterHandlers(nc))

	require.NoError(t, store.CreateAsset(&Asset{ID: "pump-a", Name: "Pump A", Source: SourceManual}))

	resp := requestMeta(t, nc, SubjectPointsGet, PointsGetRequest{AssetID: "pump-a"})
	require.True(t, resp.Success, resp.Error)
	var empty PointList
	require.NoError(t, json.Unmarshal(resp.Data, &empty))
	assert.Equal(t, 0, empty.Version, "a declared asset with no list converges on version 0")
	assert.Empty(t, empty.Points)

	_, err = store.UpsertPointList(&PointList{AssetID: "pump-a", Protocol: "modbus-tcp", PollIntervalMS: 500,
		Points: []Point{{Name: "temperature", ValueType: ValueTypeNumber, Address: "0", Enabled: true,
			Encoding: map[string]any{"function": "holding", "type": "int16", "scale": 0.1}}}})
	require.NoError(t, err)

	resp = requestMeta(t, nc, SubjectPointsGet, PointsGetRequest{AssetID: "pump-a"})
	require.True(t, resp.Success, resp.Error)
	var pl PointList
	require.NoError(t, json.Unmarshal(resp.Data, &pl))
	assert.Equal(t, 1, pl.Version)
	assert.Equal(t, 500, pl.PollIntervalMS)
	require.Len(t, pl.Points, 1)
	assert.Equal(t, 0.1, pl.Points[0].Encoding["scale"], "encoding reaches the adapter with its types intact")

	resp = requestMeta(t, nc, SubjectPointsGet, PointsGetRequest{AssetID: "nobody"})
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "not found")
}

func provisioned(id string, version int) *AdapterStatusFrame {
	f := heartbeat(id, "i-"+id, 1)
	f.Assets[0].ConfigVersion = version
	return f
}

func TestConfigDrift(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)

	// Converged: no issue.
	reg.Observe(provisioned("a-ok", 3), "a-ok", clock.Now())
	assert.Empty(t, reg.ConfigDrift(map[string]int{"press-01": 3}))

	// Behind.
	issues := reg.ConfigDrift(map[string]int{"press-01": 4})
	require.Len(t, issues, 1)
	assert.Equal(t, DriftConfigStale, issues[0].Kind)
	assert.Equal(t, "press-01", issues[0].Subject)
	assert.Equal(t, []string{"a-ok"}, issues[0].Adapters)
	assert.Equal(t, "running point list v3, declared v4", issues[0].Detail)

	// The list was deleted under it.
	issues = reg.ConfigDrift(map[string]int{})
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Detail, "no list is declared")
}

// An adapter configured from its own mapping file reports 0 and is not drift.
func TestConfigDriftIgnoresLocallyConfiguredAdapters(t *testing.T) {
	reg, clock, _ := newTestRegistry(t)
	reg.Observe(heartbeat("local", "i1", 1), "local", clock.Now())
	assert.Empty(t, reg.ConfigDrift(map[string]int{"press-01": 7}))
}
