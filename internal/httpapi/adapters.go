package httpapi

import (
	"net/http"

	"github.com/e7217/edg/internal/core"
)

// registerAdapterRoutes mounts the adapter runtime-status views. They are
// registered only when a registry is present, so a deployment with
// adapters.enabled=false returns 404 rather than an empty list that looks like
// "nothing is running".
func (s *Server) registerAdapterRoutes(mux *http.ServeMux) {
	if s.options.Adapters == nil {
		return
	}
	// The literal /adapters/drift is registered alongside /adapters/{id}; Go
	// 1.22's mux prefers the literal, so an adapter may not be named "drift"
	// (core.IsValidAdapterID reserves it).
	mux.HandleFunc("GET /api/v1/adapters", s.handleAdapters)
	mux.HandleFunc("GET /api/v1/adapters/drift", s.handleAdapterDrift)
	mux.HandleFunc("GET /api/v1/adapters/{id}", s.handleAdapter)
	mux.HandleFunc("GET /api/v1/assets/{id}/adapters", s.handleAssetAdapters)
}

func (s *Server) handleAdapters(w http.ResponseWriter, r *http.Request) {
	snapshot := s.options.Adapters.Snapshot()

	availability := core.Availability(r.URL.Query().Get("availability"))
	if availability != "" && !core.IsValidAvailability(availability) {
		writeError(w, http.StatusBadRequest, "invalid availability: "+string(availability))
		return
	}
	assetID := r.URL.Query().Get("asset_id")

	filtered := make([]*core.AdapterEntry, 0, len(snapshot.Adapters))
	for _, entry := range snapshot.Adapters {
		if availability != "" && entry.Availability != availability {
			continue
		}
		if assetID != "" && !containsString(entry.AssetIDs, assetID) {
			continue
		}
		filtered = append(filtered, entry)
	}
	snapshot.Adapters = filtered

	writeResponse(w, http.StatusOK, snapshot)
}

func (s *Server) handleAdapter(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.options.Adapters.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "adapter not found")
		return
	}
	writeResponse(w, http.StatusOK, entry)
}

func (s *Server) handleAdapterDrift(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, s.options.Adapters.Drift())
}

// handleAssetAdapters answers "who is collecting this asset". More than one is
// a misdeployment, which is why the answer is a list rather than an object.
func (s *Server) handleAssetAdapters(w http.ResponseWriter, r *http.Request) {
	assetID := r.PathValue("id")
	if !s.requireAssetExists(w, assetID) {
		return
	}
	ids := s.options.Adapters.AdaptersForAsset(assetID)
	entries := make([]*core.AdapterEntry, 0, len(ids))
	for _, id := range ids {
		if entry, ok := s.options.Adapters.Get(id); ok {
			entries = append(entries, entry)
		}
	}
	writeResponse(w, http.StatusOK, entries)
}

func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
