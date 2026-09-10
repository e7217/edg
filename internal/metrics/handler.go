package metrics

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// bufPool recycles exposition buffers. A scrape allocates ~16 KiB and happens
// every 15s forever; on a 512 MB edge box that is worth not handing to the GC.
var bufPool = sync.Pool{New: func() any { b := make([]byte, 0, 16<<10); return &b }}

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// Handler serves the registry in Prometheus text exposition format.
//
// It is mounted on the embedded NATS monitoring mux (:8222), which is the only
// HTTP surface guaranteed to be up: http.enabled is false in both the staging
// and production configs.
func Handler(r *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		bp := bufPool.Get().(*[]byte)
		defer func() {
			*bp = (*bp)[:0]
			bufPool.Put(bp)
		}()
		*bp = r.Gather()

		h := w.Header()
		h.Set("Content-Type", ContentType)

		if !acceptsGzip(req.Header.Get("Accept-Encoding")) {
			h.Set("Content-Length", strconv.Itoa(len(*bp)))
			w.WriteHeader(http.StatusOK)
			if req.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(*bp)
			return
		}

		h.Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		if req.Method == http.MethodHead {
			return
		}
		zw := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(zw)
		zw.Reset(w)
		_, _ = zw.Write(*bp)
		_ = zw.Close()
	})
}

// acceptsGzip reports whether the client asked for gzip and did not disable it
// with q=0.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if strings.TrimSpace(fields[0]) != "gzip" {
			continue
		}
		for _, param := range fields[1:] {
			if strings.ReplaceAll(strings.TrimSpace(param), " ", "") == "q=0" {
				return false
			}
		}
		return true
	}
	return false
}
