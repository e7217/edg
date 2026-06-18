package core

import (
	"encoding/json"
	"log"

	"github.com/nats-io/nats.go"
)

// NATS subjects
const (
	SubjectAssetCreate      = "platform.meta.asset.create"
	SubjectAssetGet         = "platform.meta.asset.get"
	SubjectAssetList        = "platform.meta.asset.list"
	SubjectAssetUpdate      = "platform.meta.asset.update"
	SubjectAssetDelete      = "platform.meta.asset.delete"
	SubjectTemplateList     = "platform.meta.template.list"
	SubjectConstraintsCheck = "platform.meta.constraints.check"

	// Relation subjects
	SubjectRelationCreate = "platform.meta.relation.create"
	SubjectRelationGet    = "platform.meta.relation.get"
	SubjectRelationList   = "platform.meta.relation.list"
	SubjectRelationDelete = "platform.meta.relation.delete"
)

// MetaHandler handles metadata NATS messages by delegating mutations to the
// shared MetadataService. Read/traversal handlers still use store/loader directly.
type MetaHandler struct {
	store       *Store
	loader      *TemplateLoader
	constraints *ConstraintsEvaluator
	service     *MetadataService
}

type MetaHandlerOptions struct {
	Events                *EventPublisher
	ConstraintEnforcement string
}

// NewMetaHandler creates a new handler
func NewMetaHandler(store *Store, loader *TemplateLoader, events ...*EventPublisher) *MetaHandler {
	var publisher *EventPublisher
	if len(events) > 0 {
		publisher = events[0]
	}
	return NewMetaHandlerWithOptions(store, loader, MetaHandlerOptions{
		Events:                publisher,
		ConstraintEnforcement: ConstraintsEnforcementWarn,
	})
}

func NewMetaHandlerWithOptions(store *Store, loader *TemplateLoader, opts MetaHandlerOptions) *MetaHandler {
	enforcement := opts.ConstraintEnforcement
	if enforcement == "" {
		enforcement = ConstraintsEnforcementWarn
	}
	return &MetaHandler{
		store:       store,
		loader:      loader,
		constraints: NewConstraintsEvaluator(loader),
		service:     NewMetadataService(store, loader, opts.Events, enforcement),
	}
}

// RegisterHandlers registers NATS subscriptions
func (h *MetaHandler) RegisterHandlers(nc *nats.Conn) error {
	handlers := map[string]nats.MsgHandler{
		SubjectAssetCreate:      h.handleAssetCreate,
		SubjectAssetGet:         h.handleAssetGet,
		SubjectAssetList:        h.handleAssetList,
		SubjectAssetUpdate:      h.handleAssetUpdate,
		SubjectAssetDelete:      h.handleAssetDelete,
		SubjectTemplateList:     h.handleTemplateList,
		SubjectConstraintsCheck: h.handleConstraintsCheck,

		// Relation handlers
		SubjectRelationCreate: h.handleRelationCreate,
		SubjectRelationGet:    h.handleRelationGet,
		SubjectRelationList:   h.handleRelationList,
		SubjectRelationDelete: h.handleRelationDelete,

		SubjectAssetAncestors:   h.handleAssetAncestors,
		SubjectAssetDescendants: h.handleAssetDescendants,
		SubjectAssetSubtree:     h.handleAssetSubtree,
		SubjectAssetConnected:   h.handleAssetConnected,
	}

	for subject, handler := range handlers {
		if _, err := nc.Subscribe(subject, handler); err != nil {
			return err
		}
		log.Printf("[Meta] Subscribed: %s", subject)
	}

	return nil
}

// Response is a common response structure
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// AssetTraversalRequest is a graph traversal request for an asset.
type AssetTraversalRequest struct {
	AssetID       string         `json:"asset_id"`
	RelationTypes []RelationType `json:"relation_types,omitempty"`
	MaxDepth      int            `json:"max_depth,omitempty"`
}

// AssetConnectedRequest is a one-hop connected asset request.
type AssetConnectedRequest struct {
	AssetID      string       `json:"asset_id"`
	RelationType RelationType `json:"relation_type,omitempty"`
}

// AssetNodesResponse wraps traversal node lists.
type AssetNodesResponse struct {
	Nodes []*AssetNode `json:"nodes"`
}

// marshalResponse marshals response with fallback on error
func (h *MetaHandler) marshalResponse(resp Response) []byte {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[Meta] Failed to marshal response: %v", err)
		// Send fallback error response instead of corrupted data
		errorResp := Response{Success: false, Error: "internal error: response marshal failed"}
		if fallbackData, err2 := json.Marshal(errorResp); err2 != nil {
			log.Printf("[Meta] Failed to marshal fallback error response: %v", err2)
			data = []byte("{\"success\":false,\"error\":\"internal error\"}")
		} else {
			data = fallbackData
		}
	}
	return data
}

func (h *MetaHandler) reply(msg *nats.Msg, resp Response) {
	data := h.marshalResponse(resp)
	msg.Respond(data)
}

// CreateAssetRequest is a request to create an asset
type CreateAssetRequest struct {
	Name         string            `json:"name"`
	TemplateName string            `json:"template_name,omitempty"`
	Labels       []string          `json:"labels,omitempty"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
	Source       string            `json:"source,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

func (h *MetaHandler) handleAssetCreate(msg *nats.Msg) {
	var req CreateAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	asset, err := h.service.CreateAsset(req)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Asset created: %s (%s)", asset.Name, asset.ID)
	h.reply(msg, Response{Success: true, Data: asset})
}

// GetAssetRequest is a request to get an asset
type GetAssetRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func (h *MetaHandler) handleAssetGet(msg *nats.Msg) {
	var req GetAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	var asset *Asset
	var err error

	if req.ID != "" {
		asset, err = h.store.GetAsset(req.ID)
	} else if req.Name != "" {
		asset, err = h.store.GetAssetByName(req.Name)
	} else {
		h.reply(msg, Response{Success: false, Error: "id or name is required"})
		return
	}

	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	if asset == nil {
		h.reply(msg, Response{Success: false, Error: "asset not found"})
		return
	}

	h.reply(msg, Response{Success: true, Data: asset})
}

func (h *MetaHandler) handleAssetList(msg *nats.Msg) {
	assets, err := h.store.ListAssets()
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	h.reply(msg, Response{Success: true, Data: assets})
}

// UpdateAssetRequest is a request to replace asset metadata.
type UpdateAssetRequest struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	TemplateName string            `json:"template_name,omitempty"`
	Labels       []string          `json:"labels,omitempty"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
	Source       string            `json:"source,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

func (h *MetaHandler) handleAssetUpdate(msg *nats.Msg) {
	var req UpdateAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	updated, err := h.service.UpdateAsset(req)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Asset updated: %s (%s)", updated.Name, updated.ID)
	h.reply(msg, Response{Success: true, Data: updated})
}

// DeleteAssetRequest is a request to delete an asset
type DeleteAssetRequest struct {
	ID     string `json:"id"`
	Source string `json:"source,omitempty"`
}

func (h *MetaHandler) handleAssetDelete(msg *nats.Msg) {
	var req DeleteAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	if err := h.service.DeleteAsset(req); err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Asset deleted: %s", req.ID)
	h.reply(msg, Response{Success: true})
}

func (h *MetaHandler) handleTemplateList(msg *nats.Msg) {
	templates := h.loader.List()
	h.reply(msg, Response{Success: true, Data: templates})
}

func (h *MetaHandler) handleAssetAncestors(msg *nats.Msg) {
	var req AssetTraversalRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	if req.AssetID == "" {
		h.reply(msg, Response{Success: false, Error: "asset_id is required"})
		return
	}

	nodes, err := h.store.GetAncestors(req.AssetID, traversalRelationTypesOrDefault(req.RelationTypes), req.MaxDepth)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	h.reply(msg, Response{Success: true, Data: AssetNodesResponse{Nodes: nodes}})
}

func (h *MetaHandler) handleAssetDescendants(msg *nats.Msg) {
	var req AssetTraversalRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	if req.AssetID == "" {
		h.reply(msg, Response{Success: false, Error: "asset_id is required"})
		return
	}

	nodes, err := h.store.GetDescendants(req.AssetID, traversalRelationTypesOrDefault(req.RelationTypes), req.MaxDepth)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	h.reply(msg, Response{Success: true, Data: AssetNodesResponse{Nodes: nodes}})
}

func (h *MetaHandler) handleAssetSubtree(msg *nats.Msg) {
	var req AssetTraversalRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	if req.AssetID == "" {
		h.reply(msg, Response{Success: false, Error: "asset_id is required"})
		return
	}

	tree, err := h.store.GetSubtree(req.AssetID, traversalRelationTypesOrDefault(req.RelationTypes), req.MaxDepth)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	h.reply(msg, Response{Success: true, Data: tree})
}

func (h *MetaHandler) handleAssetConnected(msg *nats.Msg) {
	var req AssetConnectedRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	if req.AssetID == "" {
		h.reply(msg, Response{Success: false, Error: "asset_id is required"})
		return
	}

	nodes, err := h.store.GetConnected(req.AssetID, req.RelationType)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	h.reply(msg, Response{Success: true, Data: AssetNodesResponse{Nodes: nodes}})
}

func traversalRelationTypesOrDefault(relTypes []RelationType) []RelationType {
	if len(relTypes) == 0 {
		return DefaultHierarchicalRelationTypes()
	}
	return relTypes
}

func (h *MetaHandler) handleConstraintsCheck(msg *nats.Msg) {
	report, err := h.constraints.CheckAll(h.store)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	h.reply(msg, Response{Success: true, Data: report})
}

// ==================== AssetRelation Handlers ====================

// CreateRelationRequest is a request to create a relation
type CreateRelationRequest struct {
	SourceAssetID string            `json:"source_asset_id"`
	TargetAssetID string            `json:"target_asset_id"`
	RelationType  RelationType      `json:"relation_type"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Source        string            `json:"source,omitempty"`
}

func (h *MetaHandler) handleRelationCreate(msg *nats.Msg) {
	var req CreateRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	relation, err := h.service.CreateRelation(req)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Relation created: %s (%s -> %s, type: %s)",
		relation.ID, relation.SourceAssetID, relation.TargetAssetID, relation.RelationType)
	h.reply(msg, Response{Success: true, Data: relation})
}

// GetRelationRequest is a request to get a relation
type GetRelationRequest struct {
	ID string `json:"id"`
}

func (h *MetaHandler) handleRelationGet(msg *nats.Msg) {
	var req GetRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	if req.ID == "" {
		h.reply(msg, Response{Success: false, Error: "id is required"})
		return
	}

	relation, err := h.store.GetRelation(req.ID)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	if relation == nil {
		h.reply(msg, Response{Success: false, Error: "relation not found"})
		return
	}

	h.reply(msg, Response{Success: true, Data: relation})
}

// ListRelationsRequest is a request to list relations
type ListRelationsRequest struct {
	AssetID      string       `json:"asset_id,omitempty"`
	RelationType RelationType `json:"relation_type,omitempty"`
	Direction    string       `json:"direction,omitempty"` // "outgoing", "incoming", "both"
}

func (h *MetaHandler) handleRelationList(msg *nats.Msg) {
	var req ListRelationsRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	var relations []*AssetRelation
	var err error

	// If asset_id is provided, filter by direction
	if req.AssetID != "" {
		direction := req.Direction
		if direction == "" {
			direction = "both"
		}

		switch direction {
		case "outgoing":
			relations, err = h.store.GetRelationsBySourceAsset(req.AssetID)
		case "incoming":
			relations, err = h.store.GetRelationsByTargetAsset(req.AssetID)
		case "both":
			// Get both outgoing and incoming
			outgoing, err1 := h.store.GetRelationsBySourceAsset(req.AssetID)
			incoming, err2 := h.store.GetRelationsByTargetAsset(req.AssetID)
			if err1 != nil {
				err = err1
			} else if err2 != nil {
				err = err2
			} else {
				relations = append(outgoing, incoming...)
			}
		default:
			h.reply(msg, Response{Success: false, Error: "invalid direction (use: outgoing, incoming, both)"})
			return
		}

		if err != nil {
			h.reply(msg, Response{Success: false, Error: err.Error()})
			return
		}

		// Filter by relation type if provided
		if req.RelationType != "" {
			filtered := []*AssetRelation{}
			for _, rel := range relations {
				if rel.RelationType == req.RelationType {
					filtered = append(filtered, rel)
				}
			}
			relations = filtered
		}
	} else {
		// No asset_id provided - this could list all relations, but we'll return error for now
		h.reply(msg, Response{Success: false, Error: "asset_id is required"})
		return
	}

	h.reply(msg, Response{Success: true, Data: relations})
}

// DeleteRelationRequest is a request to delete a relation
type DeleteRelationRequest struct {
	ID     string `json:"id"`
	Source string `json:"source,omitempty"`
}

func (h *MetaHandler) handleRelationDelete(msg *nats.Msg) {
	var req DeleteRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	if err := h.service.DeleteRelation(req); err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Relation deleted: %s", req.ID)
	h.reply(msg, Response{Success: true})
}
