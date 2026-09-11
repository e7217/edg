package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/core"
	"github.com/e7217/edg/internal/metrics"
)

const metricsTestToken = "metrics-test-token"

// newMetricsTestServer has both a token and an adapter registry, so every
// pattern in routeLabels is actually registered and reachable.
func newMetricsTestServer(t *testing.T) *Server {
	t.Helper()
	registry := core.NewAdapterRegistry(core.AdapterRegistryOptions{})
	registry.Observe(adapterFrame("modbus-1", "sensor-1"), "modbus-1", time.Now())
	return newHTTPTestServer(newHTTPTestStore(t), Options{
		Adapters: registry,
		Token:    metricsTestToken,
	})
}

func serve(t *testing.T, srv *Server, method, path string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+metricsTestToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return req, rec
}

func vec2Value(t *testing.T, family, route, class string) int64 {
	t.Helper()
	prefix := fmt.Sprintf("%s{route=%q,class=%q} ", family, route, class)
	for _, line := range strings.Split(string(metrics.Default.Gather()), "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			var n int64
			_, err := fmt.Sscanf(rest, "%d", &n)
			require.NoError(t, err)
			return n
		}
	}
	t.Fatalf("%s not found in the exposition", prefix)
	return 0
}

func vecValue(t *testing.T, family, label, value string) int64 {
	t.Helper()
	prefix := fmt.Sprintf("%s{%s=%q} ", family, label, value)
	for _, line := range strings.Split(string(metrics.Default.Gather()), "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			var n int64
			_, err := fmt.Sscanf(rest, "%d", &n)
			require.NoError(t, err)
			return n
		}
	}
	t.Fatalf("%s not found in the exposition", prefix)
	return 0
}

// Every pattern registered on the mux must be a declared label value, or its
// requests silently pile up under route="other" and the metric is useless for
// the endpoint that is actually failing.
func TestRouteLabelsCoverEveryRegisteredPattern(t *testing.T) {
	declared := make(map[string]bool, len(routeLabels))
	for _, r := range routeLabels {
		declared[r] = true
	}

	srv := newMetricsTestServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/health"},
		{http.MethodGet, "/api/v1/version"},
		{http.MethodGet, "/api/v1/assets"},
		{http.MethodGet, "/api/v1/assets/x"},
		{http.MethodGet, "/api/v1/assets/x/ancestors"},
		{http.MethodGet, "/api/v1/assets/x/descendants"},
		{http.MethodGet, "/api/v1/assets/x/subtree"},
		{http.MethodGet, "/api/v1/assets/x/connected"},
		{http.MethodGet, "/api/v1/assets/x/adapters"},
		{http.MethodGet, "/api/v1/relations"},
		{http.MethodGet, "/api/v1/templates"},
		{http.MethodGet, "/api/v1/templates/x"},
		{http.MethodGet, "/api/v1/constraints"},
		{http.MethodGet, "/api/v1/adapters"},
		{http.MethodGet, "/api/v1/adapters/drift"},
		{http.MethodGet, "/api/v1/adapters/x"},
		{http.MethodPost, "/api/v1/assets"},
		{http.MethodPut, "/api/v1/assets/x"},
		{http.MethodDelete, "/api/v1/assets/x"},
		{http.MethodPost, "/api/v1/relations"},
		{http.MethodDelete, "/api/v1/relations/x"},
		{http.MethodGet, "/api/v1/points"},
		{http.MethodGet, "/api/v1/assets/x/points"},
		{http.MethodPut, "/api/v1/assets/x/points"},
		{http.MethodDelete, "/api/v1/assets/x/points"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, _ := serve(t, srv, tc.method, tc.path)

			if req.Pattern == "" {
				t.Fatalf("no pattern matched %s %s; the route table is out of date", tc.method, tc.path)
			}
			if !declared[req.Pattern] {
				t.Errorf("pattern %q is registered on the mux but missing from routeLabels, so its requests fold into route=\"other\"", req.Pattern)
			}
		})
	}
}

// The list above is hand-maintained and so cannot catch a route nobody thought
// to add. Assert the other direction as well: every declared label must
// correspond to a pattern the mux actually serves, which fails when a route is
// renamed or removed and its label left behind.
func TestEveryRouteLabelIsAServedPattern(t *testing.T) {
	// Its own server with the webui enabled: "GET /" is in routeLabels, and the
	// file server it mounts would otherwise swallow the unmatched-route test's
	// request, so the two cannot share a fixture.
	registry := core.NewAdapterRegistry(core.AdapterRegistryOptions{})
	registry.Observe(adapterFrame("modbus-1", "sensor-1"), "modbus-1", time.Now())
	srv := newHTTPTestServer(newHTTPTestStore(t), Options{
		Adapters:     registry,
		Token:        metricsTestToken,
		WebUIEnabled: true,
	})
	handler := srv.Handler()

	for _, label := range routeLabels {
		method, pattern, ok := strings.Cut(label, " ")
		if !ok {
			t.Errorf("route label %q is not \"METHOD /path\"", label)
			continue
		}
		// Turn the pattern back into a concrete path: {id} -> x.
		path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(pattern, "x")
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+metricsTestToken)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if req.Pattern != label {
			t.Errorf("label %q resolved to pattern %q; the label is stale or the route moved",
				label, req.Pattern)
		}
	}
}

// A path parameter must never reach the route label: that is the difference
// between 22 series and one per asset.
func TestRouteLabelNeverCarriesAPathParameter(t *testing.T) {
	srv := newMetricsTestServer(t)

	before := vec2Value(t, "edg_core_http_requests_total", "GET /api/v1/assets/{id}", "2xx") +
		vec2Value(t, "edg_core_http_requests_total", "GET /api/v1/assets/{id}", "4xx") +
		vec2Value(t, "edg_core_http_requests_total", "GET /api/v1/assets/{id}", "5xx")
	beforeSeries := metrics.Default.SeriesCount()

	for i := 0; i < 200; i++ {
		serve(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/assets/plant-a-line-3-sensor-%d", i))
	}

	after := vec2Value(t, "edg_core_http_requests_total", "GET /api/v1/assets/{id}", "2xx") +
		vec2Value(t, "edg_core_http_requests_total", "GET /api/v1/assets/{id}", "4xx") +
		vec2Value(t, "edg_core_http_requests_total", "GET /api/v1/assets/{id}", "5xx")
	assert.Equal(t, before+200, after, "200 requests must land on the one {id} pattern")
	assert.Equal(t, beforeSeries, metrics.Default.SeriesCount(), "the series count moved")
	assert.NotContains(t, string(metrics.Default.Gather()), "plant-a-line-3-sensor-")
}

// An unmatched path has no pattern, so it must fold into "other" rather than
// carrying the raw URL.
func TestUnmatchedRouteFoldsIntoOther(t *testing.T) {
	srv := newMetricsTestServer(t)
	before := vec2Value(t, "edg_core_http_requests_total", "other", "4xx")

	_, rec := serve(t, srv, http.MethodGet, "/no/such/path")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, before+1, vec2Value(t, "edg_core_http_requests_total", "other", "4xx"))
	assert.NotContains(t, string(metrics.Default.Gather()), "/no/such/path")
}

// The two 401 branches mean different things to an operator: one is a
// misconfigured deployment, the other is a bad client.
func TestUnauthorizedReasons(t *testing.T) {
	t.Run("write without token", func(t *testing.T) {
		srv := newHTTPTestServer(newHTTPTestStore(t), Options{}) // no token configured
		before := vecValue(t, "edg_core_http_unauthorized_total", "reason", "write_without_token")

		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/assets", nil))

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, before+1, vecValue(t, "edg_core_http_unauthorized_total", "reason", "write_without_token"))
	})

	t.Run("bad token", func(t *testing.T) {
		srv := newHTTPTestServer(newHTTPTestStore(t), Options{Token: "the-real-token"})
		before := vecValue(t, "edg_core_http_unauthorized_total", "reason", "bad_token")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		srv.Handler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, before+1, vecValue(t, "edg_core_http_unauthorized_total", "reason", "bad_token"))
	})
}
