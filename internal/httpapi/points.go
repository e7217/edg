package httpapi

import (
	"net/http"

	"github.com/e7217/edg/internal/core"
)

// registerPointRoutes mounts the point-list views and writes.
//
// The patterns registered here must also appear in routeLabels, or their
// requests fold into route="other" on /metrics --
// TestRouteLabelsCoverEveryRegisteredPattern fails if they do not.
func (s *Server) registerPointRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/points", s.handlePoints)
	mux.HandleFunc("GET /api/v1/assets/{id}/points", s.handleAssetPoints)
	mux.HandleFunc("PUT /api/v1/assets/{id}/points", s.handleAssetPointsUpsert)
	mux.HandleFunc("DELETE /api/v1/assets/{id}/points", s.handleAssetPointsDelete)
}

// handlePoints lists every declared point list, or searches points by name when
// ?name= is given. Search is the query behind "which boxes call this tag
// something else", which is the first thing an operator asks of a plant-wide
// tag inventory.
func (s *Server) handlePoints(w http.ResponseWriter, r *http.Request) {
	if name := r.URL.Query().Get("name"); name != "" {
		hits, err := s.service.SearchPoints(name)
		if err != nil {
			writeError(w, httpStatusForError(err), err.Error())
			return
		}
		writeResponse(w, http.StatusOK, hits)
		return
	}

	lists, err := s.service.ListPointLists()
	if err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, lists)
}

func (s *Server) handleAssetPoints(w http.ResponseWriter, r *http.Request) {
	pl, err := s.service.GetPointList(r.PathValue("id"))
	if err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, pl)
}

// handleAssetPointsUpsert replaces an asset's point list wholesale.
//
// PUT rather than POST because it is idempotent in effect: the same body always
// produces the same stored list. The version still advances, which is what a
// future distribution phase needs to notice a write at all.
func (s *Server) handleAssetPointsUpsert(w http.ResponseWriter, r *http.Request) {
	var req core.UpsertPointListRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The path is authoritative for identity, so a body that disagrees is a
	// mistake rather than a second opinion.
	if req.AssetID != "" && req.AssetID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, "asset_id in the body does not match the path")
		return
	}
	req.AssetID = r.PathValue("id")

	pl, err := s.service.UpsertPointList(req)
	if err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, pl)
}

func (s *Server) handleAssetPointsDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeletePointList(core.DeletePointListRequest{
		AssetID: r.PathValue("id"),
	}); err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, map[string]string{"asset_id": r.PathValue("id")})
}
