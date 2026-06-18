package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/e7217/edg/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerTemplatesEndpoint(t *testing.T) {
	store := newHTTPTestStore(t)
	require.NoError(t, store.UpsertTemplate(&core.AssetTemplate{
		Name:      "temp-sensor",
		Resources: []core.AssetResource{{Name: "temperature", ValueType: core.ValueTypeNumber}},
	}))
	server := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/templates", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)
	var templates []*core.AssetTemplate
	require.NoError(t, json.Unmarshal(resp.Data, &templates))
	require.Len(t, templates, 1)
	assert.Equal(t, "temp-sensor", templates[0].Name)

	status, _ = getJSON(t, server.URL+"/api/v1/templates/temp-sensor", "")
	require.Equal(t, http.StatusOK, status)

	status, _ = getJSON(t, server.URL+"/api/v1/templates/nope", "")
	require.Equal(t, http.StatusNotFound, status)
}

func TestServerConstraintsEndpoint(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/constraints", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)
}

func TestServerWebUI(t *testing.T) {
	store := newHTTPTestStore(t)

	// Disabled: the root path is not served.
	off := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(off.Close)
	res, err := http.Get(off.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)

	// Enabled: the root serves the embedded HTML, and /api still works.
	on := httptest.NewServer(newHTTPTestServer(store, Options{WebUIEnabled: true}).Handler())
	t.Cleanup(on.Close)

	res, err = http.Get(on.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	assert.Contains(t, string(body), "EDG Master Data")

	status, _ := getJSON(t, on.URL+"/api/v1/health", "")
	assert.Equal(t, http.StatusOK, status)
}
