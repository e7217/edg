package metrics

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestHandlerServesExposition(t *testing.T) {
	r := goldenRegistry(t)
	rec := httptest.NewRecorder()
	Handler(r).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != ContentType {
		t.Errorf("Content-Type = %q, want %q", got, ContentType)
	}
	if !bytes.Equal(rec.Body.Bytes(), r.Gather()) {
		t.Error("served bytes differ from Gather()")
	}
	if got, err := strconv.Atoi(rec.Header().Get("Content-Length")); err != nil || got != rec.Body.Len() {
		t.Errorf("Content-Length = %q, want %d", rec.Header().Get("Content-Length"), rec.Body.Len())
	}
}

func TestHandlerGzip(t *testing.T) {
	r := goldenRegistry(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	Handler(r).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(body, r.Gather()) {
		t.Error("decompressed bytes differ from Gather()")
	}
}

// The gzip.Writer is pooled; a reused writer that is not Reset produces a
// corrupt second response.
func TestHandlerGzipIsReusable(t *testing.T) {
	r := goldenRegistry(t)
	h := Handler(r)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("scrape %d: gzip reader: %v", i, err)
		}
		body, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("scrape %d: gunzip: %v", i, err)
		}
		if !bytes.Equal(body, r.Gather()) {
			t.Fatalf("scrape %d: decompressed bytes differ from Gather()", i)
		}
	}
}

func TestHandlerRejectsGzipQZero(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0, identity")
	rec := httptest.NewRecorder()
	Handler(goldenRegistry(t)).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
}

func TestHandlerMethods(t *testing.T) {
	h := Handler(goldenRegistry(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/metrics", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, %d body bytes; want 200 and no body", rec.Code, rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}
