package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/core"
)

const pointsToken = "points-test-token"

func newPointsServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := newHTTPTestStore(t)
	srv := httptest.NewServer(newHTTPTestServer(store, Options{Token: pointsToken}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func samplePointList() core.UpsertPointListRequest {
	return core.UpsertPointListRequest{
		Protocol:       "modbus-tcp",
		PollIntervalMS: 1000,
		Points: []core.Point{{
			Name:      "temperature",
			ValueType: core.ValueTypeNumber,
			Unit:      "°C",
			Address:   "0",
			Encoding:  map[string]any{"function": "holding", "type": "int16", "scale": 0.1},
			Enabled:   true,
		}},
	}
}

func TestPointsAPIRoundTrip(t *testing.T) {
	srv := newPointsServer(t)

	status, resp := doJSON(t, http.MethodPut, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken, samplePointList())
	require.Equal(t, http.StatusOK, status, resp.Error)
	require.True(t, resp.Success)

	var pl core.PointList
	require.NoError(t, json.Unmarshal(resp.Data, &pl))
	assert.Equal(t, "sensor-1", pl.AssetID)
	assert.Equal(t, 1, pl.Version)
	require.Len(t, pl.Points, 1)
	// The numeric scale must survive JSON and SQLite as a number, not "0.1".
	assert.Equal(t, 0.1, pl.Points[0].Encoding["scale"])

	status, resp = getJSON(t, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(resp.Data, &pl))
	assert.Equal(t, "modbus-tcp", pl.Protocol)

	status, resp = getJSON(t, srv.URL+"/api/v1/points", pointsToken)
	require.Equal(t, http.StatusOK, status)
	var lists []core.PointList
	require.NoError(t, json.Unmarshal(resp.Data, &lists))
	require.Len(t, lists, 1)

	status, _ = doJSON(t, http.MethodDelete, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken, nil)
	require.Equal(t, http.StatusOK, status)

	status, resp = getJSON(t, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken)
	require.Equal(t, http.StatusOK, status, "an asset with no points is 200 with an empty list, not 404")
	require.NoError(t, json.Unmarshal(resp.Data, &pl))
	assert.Empty(t, pl.Points)
}

func TestPointsAPISearch(t *testing.T) {
	srv := newPointsServer(t)
	for _, id := range []string{"sensor-1", "equipment-1"} {
		status, resp := doJSON(t, http.MethodPut, srv.URL+"/api/v1/assets/"+id+"/points", pointsToken, samplePointList())
		require.Equal(t, http.StatusOK, status, resp.Error)
	}

	status, resp := getJSON(t, srv.URL+"/api/v1/points?name=temp", pointsToken)
	require.Equal(t, http.StatusOK, status)
	var hits []core.PointSearchResult
	require.NoError(t, json.Unmarshal(resp.Data, &hits))
	require.Len(t, hits, 2)
	assert.Equal(t, "temperature", hits[0].Name)

	status, resp = getJSON(t, srv.URL+"/api/v1/points?name=nothing-like-this", pointsToken)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(resp.Data, &hits))
	assert.Empty(t, hits)
}

// The path owns identity. A body that disagrees is a mistake, not a second
// opinion -- silently preferring one would let a bulk script write every list
// onto one asset.
func TestPointsAPIRejectsMismatchedAssetID(t *testing.T) {
	srv := newPointsServer(t)
	req := samplePointList()
	req.AssetID = "some-other-asset"

	status, resp := doJSON(t, http.MethodPut, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken, req)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, resp.Error, "does not match the path")
}

func TestPointsAPIErrorMapping(t *testing.T) {
	srv := newPointsServer(t)

	// An unknown asset is a 404, not a 500 from an opaque FK violation.
	status, _ := doJSON(t, http.MethodPut, srv.URL+"/api/v1/assets/no-such-asset/points", pointsToken, samplePointList())
	assert.Equal(t, http.StatusNotFound, status)

	bad := samplePointList()
	bad.Points[0].ValueType = "INT16"
	status, resp := doJSON(t, http.MethodPut, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken, bad)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, resp.Error, "invalid value_type")

	status, _ = doJSON(t, http.MethodDelete, srv.URL+"/api/v1/assets/sensor-1/points", pointsToken, nil)
	assert.Equal(t, http.StatusNotFound, status, "deleting a list that does not exist is a 404")
}

// Writes must be token-gated like every other mutation.
func TestPointsAPIWritesRequireToken(t *testing.T) {
	srv := newPointsServer(t)

	status, _ := doJSON(t, http.MethodPut, srv.URL+"/api/v1/assets/sensor-1/points", "", samplePointList())
	assert.Equal(t, http.StatusUnauthorized, status)

	status, _ = doJSON(t, http.MethodDelete, srv.URL+"/api/v1/assets/sensor-1/points", "", nil)
	assert.Equal(t, http.StatusUnauthorized, status)
}
