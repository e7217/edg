package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/e7217/edg/internal/core"
	"github.com/e7217/edg/internal/webui"
)

const (
	defaultAddress  = "127.0.0.1:8080"
	defaultTokenEnv = "EDG_HTTP_TOKEN"
)

type Options struct {
	Address            string
	Token              string
	TokenEnv           string
	CORSAllowedOrigins []string
	WebUIEnabled       bool
	Version            string
	BuildTime          string
	GitCommit          string
}

type Server struct {
	store   *core.Store
	service *core.MetadataService
	options Options
	handler http.Handler
}

func NewServer(store *core.Store, service *core.MetadataService, opts Options) *Server {
	if opts.Address == "" {
		opts.Address = defaultAddress
	}
	if opts.TokenEnv == "" {
		opts.TokenEnv = defaultTokenEnv
	}
	if opts.Token == "" && opts.TokenEnv != "" {
		opts.Token = os.Getenv(opts.TokenEnv)
	}
	server := &Server{
		store:   store,
		service: service,
		options: opts,
	}
	server.handler = server.buildHandler()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Run(ctx context.Context) error {
	httpServer := &http.Server{
		Addr:              s.options.Address,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	mux.HandleFunc("GET /api/v1/assets", s.handleAssets)
	mux.HandleFunc("GET /api/v1/assets/{id}", s.handleAsset)
	mux.HandleFunc("GET /api/v1/assets/{id}/ancestors", s.handleAncestors)
	mux.HandleFunc("GET /api/v1/assets/{id}/descendants", s.handleDescendants)
	mux.HandleFunc("GET /api/v1/assets/{id}/subtree", s.handleSubtree)
	mux.HandleFunc("GET /api/v1/assets/{id}/connected", s.handleConnected)
	mux.HandleFunc("GET /api/v1/relations", s.handleRelations)
	mux.HandleFunc("GET /api/v1/templates", s.handleTemplates)
	mux.HandleFunc("GET /api/v1/templates/{name}", s.handleTemplate)
	mux.HandleFunc("GET /api/v1/constraints", s.handleConstraints)

	// Write endpoints (Phase 3). All mutations go through MetadataService so
	// validation, constraint enforcement, and change events match the NATS path.
	mux.HandleFunc("POST /api/v1/assets", s.handleAssetCreate)
	mux.HandleFunc("PUT /api/v1/assets/{id}", s.handleAssetUpdate)
	mux.HandleFunc("DELETE /api/v1/assets/{id}", s.handleAssetDelete)
	mux.HandleFunc("POST /api/v1/relations", s.handleRelationCreate)
	mux.HandleFunc("DELETE /api/v1/relations/{id}", s.handleRelationDelete)

	// Embedded operator UI (Phase 5). Served at the root; the more specific
	// /api/v1/... patterns take precedence in the Go 1.22 mux.
	if s.options.WebUIEnabled {
		mux.Handle("GET /", http.FileServerFS(webui.FS()))
	}
	return s.cors(s.auth(mux))
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.store.ListTemplates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, templates)
}

func (s *Server) handleTemplate(w http.ResponseWriter, r *http.Request) {
	template, err := s.store.GetTemplate(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if template == nil {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	writeResponse(w, http.StatusOK, template)
}

func (s *Server) handleConstraints(w http.ResponseWriter, r *http.Request) {
	report, err := s.service.CheckConstraints()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, report)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, map[string]string{
		"version":    s.options.Version,
		"build_time": s.options.BuildTime,
		"git_commit": s.options.GitCommit,
	})
}

func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	assets, err := s.store.ListAssets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, paginateAssets(assets, limit, offset))
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	asset, err := s.store.GetAsset(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if asset == nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	writeResponse(w, http.StatusOK, asset)
}

func (s *Server) handleAncestors(w http.ResponseWriter, r *http.Request) {
	if !s.requireAssetExists(w, r.PathValue("id")) {
		return
	}
	relTypes, maxDepth, err := parseTraversalQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodes, err := s.store.GetAncestors(r.PathValue("id"), relTypes, maxDepth)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, nodes)
}

func (s *Server) handleDescendants(w http.ResponseWriter, r *http.Request) {
	if !s.requireAssetExists(w, r.PathValue("id")) {
		return
	}
	relTypes, maxDepth, err := parseTraversalQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodes, err := s.store.GetDescendants(r.PathValue("id"), relTypes, maxDepth)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, nodes)
}

func (s *Server) handleSubtree(w http.ResponseWriter, r *http.Request) {
	if !s.requireAssetExists(w, r.PathValue("id")) {
		return
	}
	relTypes, maxDepth, err := parseTraversalQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tree, err := s.store.GetSubtree(r.PathValue("id"), relTypes, maxDepth)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tree == nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	writeResponse(w, http.StatusOK, tree)
}

func (s *Server) handleConnected(w http.ResponseWriter, r *http.Request) {
	if !s.requireAssetExists(w, r.PathValue("id")) {
		return
	}
	relType := core.RelationType(r.URL.Query().Get("relation_type"))
	if relType != "" && !core.IsValidRelationType(relType) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid relation_type: %s", relType))
		return
	}
	nodes, err := s.store.GetConnected(r.PathValue("id"), relType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, nodes)
}

func (s *Server) handleRelations(w http.ResponseWriter, r *http.Request) {
	relations, err := s.store.ListRelations()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	source := r.URL.Query().Get("source")
	target := r.URL.Query().Get("target")
	relType := core.RelationType(r.URL.Query().Get("type"))
	if relType != "" && !core.IsValidRelationType(relType) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid type: %s", relType))
		return
	}

	filtered := make([]*core.AssetRelation, 0, len(relations))
	for _, relation := range relations {
		if source != "" && relation.SourceAssetID != source {
			continue
		}
		if target != "" && relation.TargetAssetID != target {
			continue
		}
		if relType != "" && relation.RelationType != relType {
			continue
		}
		filtered = append(filtered, relation)
	}
	writeResponse(w, http.StatusOK, filtered)
}

func (s *Server) requireAssetExists(w http.ResponseWriter, assetID string) bool {
	exists, err := s.store.AssetExists(assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "asset not found")
		return false
	}
	return true
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	default:
		return false
	}
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.options.Token == "" {
			// Reads stay open when no token is configured, but writes are never
			// anonymous: they require a non-empty bearer token.
			if isWriteMethod(r.Method) {
				writeError(w, http.StatusUnauthorized, "write access requires a configured bearer token")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.options.Token {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	allowAll := false
	for _, origin := range s.options.CORSAllowedOrigins {
		if origin == "*" {
			allowAll = true
			continue
		}
		allowed[origin] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || allowed[origin]) {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseTraversalQuery(r *http.Request) ([]core.RelationType, int, error) {
	relTypes, err := parseRelationTypes(r.URL.Query().Get("relation_types"))
	if err != nil {
		return nil, 0, err
	}
	maxDepth, err := parseOptionalInt(r.URL.Query().Get("max_depth"), 0, "max_depth")
	if err != nil {
		return nil, 0, err
	}
	return relTypes, maxDepth, nil
}

func parseRelationTypes(raw string) ([]core.RelationType, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	relTypes := make([]core.RelationType, 0, len(parts))
	for _, part := range parts {
		relType := core.RelationType(strings.TrimSpace(part))
		if relType == "" {
			continue
		}
		if !core.IsValidRelationType(relType) {
			return nil, fmt.Errorf("invalid relation_type: %s", relType)
		}
		relTypes = append(relTypes, relType)
	}
	return relTypes, nil
}

func parsePagination(r *http.Request) (int, int, error) {
	limit, err := parseOptionalInt(r.URL.Query().Get("limit"), 100, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err := parseOptionalInt(r.URL.Query().Get("offset"), 0, "offset")
	if err != nil {
		return 0, 0, err
	}
	if limit < 0 {
		return 0, 0, fmt.Errorf("limit must be >= 0")
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("offset must be >= 0")
	}
	return limit, offset, nil
}

func parseOptionalInt(raw string, defaultValue int, name string) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func paginateAssets(assets []*core.Asset, limit, offset int) []*core.Asset {
	if offset >= len(assets) {
		return []*core.Asset{}
	}
	end := offset + limit
	if limit == 0 || end > len(assets) {
		end = len(assets)
	}
	return assets[offset:end]
}
