package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/e7217/edg/internal/core"
)

// httpStatusForError maps a MetadataService error to an HTTP status code using
// its typed ErrorKind. Unknown errors map to 500.
func httpStatusForError(err error) int {
	switch core.KindOf(err) {
	case core.ErrValidation:
		return http.StatusBadRequest
	case core.ErrNotFound:
		return http.StatusNotFound
	case core.ErrConflict:
		return http.StatusConflict
	case core.ErrConstraint:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func (s *Server) handleAssetCreate(w http.ResponseWriter, r *http.Request) {
	var req core.CreateAssetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.service.CreateAsset(req)
	if err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusCreated, asset)
}

func (s *Server) handleAssetUpdate(w http.ResponseWriter, r *http.Request) {
	var req core.UpdateAssetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = r.PathValue("id") // the path id is authoritative
	asset, err := s.service.UpdateAsset(req)
	if err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, asset)
}

func (s *Server) handleAssetDelete(w http.ResponseWriter, r *http.Request) {
	req := core.DeleteAssetRequest{
		ID:     r.PathValue("id"),
		Source: r.URL.Query().Get("source"),
	}
	if err := s.service.DeleteAsset(req); err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, map[string]string{"id": req.ID})
}

func (s *Server) handleRelationCreate(w http.ResponseWriter, r *http.Request) {
	var req core.CreateRelationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	relation, err := s.service.CreateRelation(req)
	if err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusCreated, relation)
}

func (s *Server) handleRelationDelete(w http.ResponseWriter, r *http.Request) {
	req := core.DeleteRelationRequest{
		ID:     r.PathValue("id"),
		Source: r.URL.Query().Get("source"),
	}
	if err := s.service.DeleteRelation(req); err != nil {
		writeError(w, httpStatusForError(err), err.Error())
		return
	}
	writeResponse(w, http.StatusOK, map[string]string{"id": req.ID})
}
