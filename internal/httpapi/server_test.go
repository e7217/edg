package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/e7217/edg/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func TestServerHealthAndVersion(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{
		Version:   "test",
		BuildTime: "now",
		GitCommit: "abc123",
	}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/health", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)

	var health map[string]string
	require.NoError(t, json.Unmarshal(resp.Data, &health))
	assert.Equal(t, "ok", health["status"])

	status, resp = getJSON(t, server.URL+"/api/v1/version", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)

	var version map[string]string
	require.NoError(t, json.Unmarshal(resp.Data, &version))
	assert.Equal(t, "test", version["version"])
	assert.Equal(t, "abc123", version["git_commit"])
}

func TestServerAssetsRelationsAndTraversal(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/assets?limit=2&offset=0", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)

	var assets []*core.Asset
	require.NoError(t, json.Unmarshal(resp.Data, &assets))
	require.Len(t, assets, 2)

	status, resp = getJSON(t, server.URL+"/api/v1/assets/sensor-1", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)

	var asset core.Asset
	require.NoError(t, json.Unmarshal(resp.Data, &asset))
	assert.Equal(t, "sensor-1", asset.ID)

	status, resp = getJSON(t, server.URL+"/api/v1/assets/line-1/descendants?relation_types=partOf&max_depth=5", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)

	var descendants []*core.AssetNode
	require.NoError(t, json.Unmarshal(resp.Data, &descendants))
	assert.ElementsMatch(t, []string{"equipment-1", "sensor-1"}, assetNodeIDs(descendants))

	status, resp = getJSON(t, server.URL+"/api/v1/relations?source=sensor-1&type=partOf", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)

	var relations []*core.AssetRelation
	require.NoError(t, json.Unmarshal(resp.Data, &relations))
	require.Len(t, relations, 1)
	assert.Equal(t, "equipment-1", relations[0].TargetAssetID)
}

func TestServerNotFoundAndBadQuery(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/assets/missing", "")
	require.Equal(t, http.StatusNotFound, status)
	require.False(t, resp.Success)
	assert.Contains(t, resp.Error, "asset not found")

	status, resp = getJSON(t, server.URL+"/api/v1/assets/line-1/descendants?max_depth=abc", "")
	require.Equal(t, http.StatusBadRequest, status)
	require.False(t, resp.Success)
	assert.Contains(t, resp.Error, "max_depth")

	status, resp = getJSON(t, server.URL+"/api/v1/assets/missing/descendants", "")
	require.Equal(t, http.StatusNotFound, status)
	require.False(t, resp.Success)
	assert.Contains(t, resp.Error, "asset not found")
}

func TestServerBearerAuth(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: "secret"}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/assets", "")
	require.Equal(t, http.StatusUnauthorized, status)
	require.False(t, resp.Success)

	status, resp = getJSON(t, server.URL+"/api/v1/assets", "wrong")
	require.Equal(t, http.StatusUnauthorized, status)

	status, resp = getJSON(t, server.URL+"/api/v1/assets", "secret")
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success)
}

// #107: configuring a token made the operator UI unreachable, because a
// browser navigating to the page cannot send an Authorization header.
func TestTokenKeepsUIReachableAndDataProtected(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: "secret", WebUIEnabled: true}).Handler())
	t.Cleanup(server.Close)

	for _, path := range []string{"/", "/index.html"} {
		resp, err := http.Get(server.URL + path)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Contains(t, string(body), "<html", path)
		// The page reaches data only through the API, with the token.
		assert.Contains(t, string(body), `headers["Authorization"] = "Bearer " + token()`)
	}

	status, _ := getJSON(t, server.URL+"/api/v1/health", "")
	assert.Equal(t, http.StatusOK, status, "a health probe needs no secret")

	for _, path := range []string{"/api/v1/assets", "/api/v1/version", "/api/v1/relations", "/api/v1/points"} {
		status, _ := getJSON(t, server.URL+path, "")
		assert.Equal(t, http.StatusUnauthorized, status, "%s must stay behind the token", path)
	}
}

// Without the UI mounted, nothing outside /api is served, so nothing outside
// /api may skip auth either.
func TestPublicPathsNeedTheUI(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: "secret"}).Handler())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: "secret", WebUIEnabled: true}).Handler())
	t.Cleanup(server.Close)

	status, resp := getJSON(t, server.URL+"/api/v1/nope", "secret")
	assert.Equal(t, http.StatusNotFound, status)
	assert.False(t, resp.Success)
	assert.Equal(t, "no such API route", resp.Error)
}

func TestCORSPreflight(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{
		Token: "secret", CORSAllowedOrigins: []string{"http://ui.example"},
	}).Handler())
	t.Cleanup(server.Close)

	preflight := func(origin string) *http.Response {
		req, err := http.NewRequest(http.MethodOptions, server.URL+"/api/v1/assets/x", nil)
		require.NoError(t, err)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPatch)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp
	}

	ok := preflight("http://ui.example")
	assert.Equal(t, http.StatusNoContent, ok.StatusCode, "a preflight carries no token and must not need one")
	assert.Contains(t, ok.Header.Get("Access-Control-Allow-Methods"), "PATCH")

	denied := preflight("http://evil.example")
	assert.NotEqual(t, http.StatusNoContent, denied.StatusCode, "an unlisted origin gets no blanket 204")
	assert.Empty(t, denied.Header.Get("Access-Control-Allow-Origin"))
}

func getJSON(t *testing.T, url, token string) (int, testResponse) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	var resp testResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	return res.StatusCode, resp
}

func newHTTPTestStore(t *testing.T) *core.Store {
	t.Helper()

	store, err := core.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	createHTTPAsset(t, store, "line-1", "Line 1", "line")
	createHTTPAsset(t, store, "equipment-1", "Equipment 1", "equipment")
	createHTTPAsset(t, store, "sensor-1", "Sensor 1", "sensor")

	createHTTPRelation(t, store, "rel-equipment-line", "equipment-1", "line-1", core.RelationPartOf)
	createHTTPRelation(t, store, "rel-sensor-equipment", "sensor-1", "equipment-1", core.RelationPartOf)
	return store
}

func createHTTPAsset(t *testing.T, store *core.Store, id, name, templateName string) {
	t.Helper()

	require.NoError(t, store.CreateAsset(&core.Asset{
		ID:           id,
		Name:         name,
		TemplateName: templateName,
		CreatedAt:    time.Now(),
	}))
}

func createHTTPRelation(t *testing.T, store *core.Store, id, sourceID, targetID string, relationType core.RelationType) {
	t.Helper()

	require.NoError(t, store.CreateRelation(&core.AssetRelation{
		ID:            id,
		SourceAssetID: sourceID,
		TargetAssetID: targetID,
		RelationType:  relationType,
		CreatedAt:     time.Now(),
	}))
}

func assetNodeIDs(nodes []*core.AssetNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}
