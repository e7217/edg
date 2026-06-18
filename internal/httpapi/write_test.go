package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/e7217/edg/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHTTPTestServer builds a Server with a MetadataService over the same store,
// using an empty template loader and no event publisher (publish is nil-safe).
func newHTTPTestServer(store *core.Store, opts Options) *Server {
	service := core.NewMetadataService(store, core.NewTemplateLoader(), nil, core.ConstraintsEnforcementWarn)
	return NewServer(store, service, opts)
}

func doJSON(t *testing.T, method, url, token string, body any) (int, testResponse) {
	t.Helper()

	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, url, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
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

const writeTestToken = "test-token"

func TestServerWriteAssets(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: writeTestToken}).Handler())
	t.Cleanup(server.Close)

	// create
	status, resp := doJSON(t, http.MethodPost, server.URL+"/api/v1/assets", writeTestToken, core.CreateAssetRequest{Name: "pump-9"})
	require.Equal(t, http.StatusCreated, status)
	require.True(t, resp.Success)
	var created core.Asset
	require.NoError(t, json.Unmarshal(resp.Data, &created))
	require.NotEmpty(t, created.ID)
	assert.Equal(t, "pump-9", created.Name)

	// duplicate name -> 409
	status, resp = doJSON(t, http.MethodPost, server.URL+"/api/v1/assets", writeTestToken, core.CreateAssetRequest{Name: "pump-9"})
	require.Equal(t, http.StatusConflict, status)
	assert.Contains(t, resp.Error, "already exists")

	// missing name -> 400
	status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/assets", writeTestToken, core.CreateAssetRequest{})
	require.Equal(t, http.StatusBadRequest, status)

	// update (path id is authoritative)
	status, resp = doJSON(t, http.MethodPut, server.URL+"/api/v1/assets/"+created.ID, writeTestToken, core.UpdateAssetRequest{Name: "pump-9-renamed"})
	require.Equal(t, http.StatusOK, status)
	var updated core.Asset
	require.NoError(t, json.Unmarshal(resp.Data, &updated))
	assert.Equal(t, "pump-9-renamed", updated.Name)

	// update missing -> 404
	status, _ = doJSON(t, http.MethodPut, server.URL+"/api/v1/assets/nope", writeTestToken, core.UpdateAssetRequest{Name: "x"})
	require.Equal(t, http.StatusNotFound, status)

	// delete
	status, _ = doJSON(t, http.MethodDelete, server.URL+"/api/v1/assets/"+created.ID, writeTestToken, nil)
	require.Equal(t, http.StatusOK, status)

	got, err := store.GetAsset(created.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestServerWriteRelations(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: writeTestToken}).Handler())
	t.Cleanup(server.Close)

	// create a relation between two existing assets
	status, resp := doJSON(t, http.MethodPost, server.URL+"/api/v1/relations", writeTestToken, core.CreateRelationRequest{
		SourceAssetID: "sensor-1", TargetAssetID: "line-1", RelationType: core.RelationLocatedIn,
	})
	require.Equal(t, http.StatusCreated, status)
	var rel core.AssetRelation
	require.NoError(t, json.Unmarshal(resp.Data, &rel))
	require.NotEmpty(t, rel.ID)

	// invalid relation_type -> 400
	status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/relations", writeTestToken, core.CreateRelationRequest{
		SourceAssetID: "sensor-1", TargetAssetID: "line-1", RelationType: "bogus",
	})
	require.Equal(t, http.StatusBadRequest, status)

	// delete
	status, _ = doJSON(t, http.MethodDelete, server.URL+"/api/v1/relations/"+rel.ID, writeTestToken, nil)
	require.Equal(t, http.StatusOK, status)
}

func TestServerWriteRequiresTokenWhenUnset(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{}).Handler())
	t.Cleanup(server.Close)

	// writes are rejected when no token is configured
	status, resp := doJSON(t, http.MethodPost, server.URL+"/api/v1/assets", "", core.CreateAssetRequest{Name: "x"})
	require.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, resp.Error, "token")

	// reads remain open
	status, _ = getJSON(t, server.URL+"/api/v1/assets", "")
	require.Equal(t, http.StatusOK, status)
}

func TestServerWriteWithToken(t *testing.T) {
	store := newHTTPTestStore(t)
	server := httptest.NewServer(newHTTPTestServer(store, Options{Token: "secret"}).Handler())
	t.Cleanup(server.Close)

	// without token -> 401
	status, _ := doJSON(t, http.MethodPost, server.URL+"/api/v1/assets", "", core.CreateAssetRequest{Name: "tok-asset"})
	require.Equal(t, http.StatusUnauthorized, status)

	// with token -> 201
	status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/assets", "secret", core.CreateAssetRequest{Name: "tok-asset"})
	require.Equal(t, http.StatusCreated, status)
}
