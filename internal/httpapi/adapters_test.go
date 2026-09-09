package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/core"
)

// adapterFrame builds a status frame for the given adapter and assets.
func adapterFrame(id string, assets ...string) *core.AdapterStatusFrame {
	statuses := make([]core.AdapterAssetStatus, 0, len(assets))
	for _, a := range assets {
		statuses = append(statuses, core.AdapterAssetStatus{AssetID: a, DeviceState: "connected"})
	}
	return &core.AdapterStatusFrame{
		SchemaVersion:      core.AdapterSchemaVersion,
		AdapterID:          id,
		InstanceID:         "inst-" + id,
		Seq:                1,
		Phase:              core.AdapterPhaseHeartbeat,
		RunState:           core.RunStateRunning,
		DeviceState:        "connected",
		HeartbeatIntervalS: 10,
		Assets:             statuses,
	}
}

// newAdapterTestServer builds a server whose registry already holds sensor-1's
// collector, reusing newHTTPTestStore's fixture assets.
func newAdapterTestServer(t *testing.T) (*httptest.Server, *core.AdapterRegistry) {
	t.Helper()

	store := newHTTPTestStore(t)
	registry := core.NewAdapterRegistry(core.AdapterRegistryOptions{})
	registry.Observe(adapterFrame("modbus-1", "sensor-1"), "modbus-1", time.Now())

	srv := httptest.NewServer(newHTTPTestServer(store, Options{Adapters: registry}).Handler())
	t.Cleanup(srv.Close)
	return srv, registry
}

func TestListAdapters(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, resp := getJSON(t, srv.URL+"/api/v1/adapters", "")
	require.Equal(t, http.StatusOK, status)
	snapshot := decodeSnapshot(t, resp)
	require.Len(t, snapshot.Adapters, 1)
	assert.Equal(t, "modbus-1", snapshot.Adapters[0].AdapterID)
	assert.Equal(t, core.AvailabilityOnline, snapshot.Adapters[0].Availability)
}

func TestListAdaptersFilters(t *testing.T) {
	srv, registry := newAdapterTestServer(t)
	registry.Observe(adapterFrame("opcua-1", "equipment-1"), "opcua-1", time.Now())

	status, resp := getJSON(t, srv.URL+"/api/v1/adapters?asset_id=sensor-1", "")
	require.Equal(t, http.StatusOK, status)
	byAsset := decodeSnapshot(t, resp)
	require.Len(t, byAsset.Adapters, 1)
	assert.Equal(t, "modbus-1", byAsset.Adapters[0].AdapterID)

	status, resp = getJSON(t, srv.URL+"/api/v1/adapters?availability=online", "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decodeSnapshot(t, resp).Adapters, 2)

	status, resp = getJSON(t, srv.URL+"/api/v1/adapters?availability=stale", "")
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, decodeSnapshot(t, resp).Adapters)
}

func TestListAdaptersRejectsUnknownAvailability(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, _ := getJSON(t, srv.URL+"/api/v1/adapters?availability=zombie", "")
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestGetAdapter(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, resp := getJSON(t, srv.URL+"/api/v1/adapters/modbus-1", "")
	require.Equal(t, http.StatusOK, status)
	var entry core.AdapterEntry
	require.NoError(t, json.Unmarshal(resp.Data, &entry))
	assert.Equal(t, "modbus-1", entry.AdapterID)
	assert.Equal(t, []string{"sensor-1"}, entry.AssetIDs)
}

func TestGetAdapterNotFound(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, _ := getJSON(t, srv.URL+"/api/v1/adapters/nope", "")
	assert.Equal(t, http.StatusNotFound, status)
}

// TestDriftRouteBeatsIDRoute: /adapters/drift must not be read as an adapter
// named "drift". Go 1.22's mux prefers the literal pattern, and
// core.IsValidAdapterID reserves the name so the two can never collide.
func TestDriftRouteBeatsIDRoute(t *testing.T) {
	srv, registry := newAdapterTestServer(t)
	registry.Observe(adapterFrame("modbus-2", "sensor-1"), "modbus-2", time.Now())

	status, resp := getJSON(t, srv.URL+"/api/v1/adapters/drift", "")
	require.Equal(t, http.StatusOK, status)
	var report core.AdapterDriftReport
	require.NoError(t, json.Unmarshal(resp.Data, &report))
	require.Equal(t, 1, report.IssueCount, "two adapters claim sensor-1")
	assert.Equal(t, core.DriftMultiAdapter, report.Issues[0].Kind)
}

func TestAssetAdapters(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, resp := getJSON(t, srv.URL+"/api/v1/assets/sensor-1/adapters", "")
	require.Equal(t, http.StatusOK, status)
	var entries []core.AdapterEntry
	require.NoError(t, json.Unmarshal(resp.Data, &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "modbus-1", entries[0].AdapterID)
}

func TestAssetAdaptersUnknownAsset(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, _ := getJSON(t, srv.URL+"/api/v1/assets/nope/adapters", "")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestAssetWithNoAdapterReturnsEmptyList(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	status, resp := getJSON(t, srv.URL+"/api/v1/assets/equipment-1/adapters", "")
	require.Equal(t, http.StatusOK, status)
	var entries []core.AdapterEntry
	require.NoError(t, json.Unmarshal(resp.Data, &entries))
	assert.Empty(t, entries)
}

// TestRoutesAbsentWithoutRegistry: a deployment with adapters.enabled=false
// must 404 rather than serve an empty list, which would read as "nothing is
// running" instead of "this is not tracked here".
func TestRoutesAbsentWithoutRegistry(t *testing.T) {
	store := newHTTPTestStore(t)
	srv := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(srv.Close)

	for _, path := range []string{
		"/api/v1/adapters", "/api/v1/adapters/drift",
		"/api/v1/adapters/modbus-1", "/api/v1/assets/sensor-1/adapters",
	} {
		res, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		_ = res.Body.Close()
		assert.Equal(t, http.StatusNotFound, res.StatusCode, "path %s", path)
	}
}

// TestAdapterRoutesFollowExistingAuthPolicy: the routes are reads, so they must
// behave exactly like the other GETs — anonymous when no token is configured.
func TestAdapterRoutesFollowExistingAuthPolicy(t *testing.T) {
	store := newHTTPTestStore(t)
	registry := core.NewAdapterRegistry(core.AdapterRegistryOptions{})
	registry.Observe(adapterFrame("modbus-1", "sensor-1"), "modbus-1", time.Now())

	srv := httptest.NewServer(newHTTPTestServer(store, Options{
		Adapters: registry, Token: "secret",
	}).Handler())
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/api/v1/adapters")
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode, "a configured token must gate reads too")

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/adapters", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestDriftSerializesEmptyIssuesAsArray(t *testing.T) {
	srv, _ := newAdapterTestServer(t)

	res, err := http.Get(srv.URL + "/api/v1/adapters/drift")
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&envelope))
	assert.Contains(t, string(envelope.Data), `"issues":[]`, "null would break naive UI iteration")
}

// decodeSnapshot unwraps the Response envelope used by every route here.
func decodeSnapshot(t *testing.T, resp testResponse) core.AdapterSnapshot {
	t.Helper()
	var snapshot core.AdapterSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snapshot))
	return snapshot
}
