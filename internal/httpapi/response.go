package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/e7217/edg/internal/core"
)

func writeResponse(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, core.Response{Success: true, Data: data})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, core.Response{Success: false, Error: message})
}

func writeJSON(w http.ResponseWriter, status int, resp core.Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, `{"success":false,"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}
