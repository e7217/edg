package core

import (
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// NATS subjects
const (
	SubjectAssetCreate  = "platform.meta.asset.create"
	SubjectAssetGet     = "platform.meta.asset.get"
	SubjectAssetList    = "platform.meta.asset.list"
	SubjectAssetDelete  = "platform.meta.asset.delete"
	SubjectTemplateList = "platform.meta.template.list"

	// Relation subjects
	SubjectRelationCreate = "platform.meta.relation.create"
	SubjectRelationGet    = "platform.meta.relation.get"
	SubjectRelationList   = "platform.meta.relation.list"
	SubjectRelationDelete = "platform.meta.relation.delete"
)

// MetaHandler handles metadata NATS messages
type MetaHandler struct {
	store  *Store
	loader *TemplateLoader
}

// NewMetaHandler creates a new handler
func NewMetaHandler(store *Store, loader *TemplateLoader) *MetaHandler {
	return &MetaHandler{
		store:  store,
		loader: loader,
	}
}

// RegisterHandlers registers NATS subscriptions
func (h *MetaHandler) RegisterHandlers(nc *nats.Conn) error {
	handlers := map[string]nats.MsgHandler{
		SubjectAssetCreate:  h.handleAssetCreate,
		SubjectAssetGet:     h.handleAssetGet,
		SubjectAssetList:    h.handleAssetList,
		SubjectAssetDelete:  h.handleAssetDelete,
		SubjectTemplateList: h.handleTemplateList,

		// Relation handlers
		SubjectRelationCreate: h.handleRelationCreate,
		SubjectRelationGet:    h.handleRelationGet,
		SubjectRelationList:   h.handleRelationList,
		SubjectRelationDelete: h.handleRelationDelete,
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

// marshalResponse marshals response with fallback on error
func (h *MetaHandler) marshalResponse(resp Response) []byte {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[Meta] Failed to marshal response: %v", err)
		// Send fallback error response instead of corrupted data
		errorResp := Response{Success: false, Error: "internal error: response marshal failed"}
		data, _ = json.Marshal(errorResp)
	}
	return data
}

func (h *MetaHandler) reply(msg *nats.Msg, resp Response) {
	data := h.marshalResponse(resp)
	msg.Respond(data)
}

// CreateAssetRequest is a request to create an asset
type CreateAssetRequest struct {
	Name         string   `json:"name"`
	TemplateName string   `json:"template_name,omitempty"`
	Labels       []string `json:"labels,omitempty"`
}

func (h *MetaHandler) handleAssetCreate(msg *nats.Msg) {
	var req CreateAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	if req.Name == "" {
		h.reply(msg, Response{Success: false, Error: "name is required"})
		return
	}

	// check for duplicate
	existing, _ := h.store.GetAssetByName(req.Name)
	if existing != nil {
		h.reply(msg, Response{Success: false, Error: "asset name already exists"})
		return
	}

	// check if template exists (optional)
	if req.TemplateName != "" && !h.loader.Exists(req.TemplateName) {
		h.reply(msg, Response{Success: false, Error: "template not found"})
		return
	}

	asset := &Asset{
		ID:           uuid.New().String(),
		Name:         req.Name,
		TemplateName: req.TemplateName,
		Labels:       req.Labels,
		CreatedAt:    time.Now(),
	}

	if err := h.store.CreateAsset(asset); err != nil {
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

// DeleteAssetRequest is a request to delete an asset
type DeleteAssetRequest struct {
	ID string `json:"id"`
}

func (h *MetaHandler) handleAssetDelete(msg *nats.Msg) {
	var req DeleteAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	if req.ID == "" {
		h.reply(msg, Response{Success: false, Error: "id is required"})
		return
	}

	if err := h.store.DeleteAsset(req.ID); err != nil {
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

// ==================== AssetRelation Handlers ====================

// CreateRelationRequest is a request to create a relation
type CreateRelationRequest struct {
	SourceAssetID string            `json:"source_asset_id"`
	TargetAssetID string            `json:"target_asset_id"`
	RelationType  RelationType      `json:"relation_type"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

func (h *MetaHandler) handleRelationCreate(msg *nats.Msg) {
	var req CreateRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	// Validate required fields
	if req.SourceAssetID == "" {
		h.reply(msg, Response{Success: false, Error: "source_asset_id is required"})
		return
	}
	if req.TargetAssetID == "" {
		h.reply(msg, Response{Success: false, Error: "target_asset_id is required"})
		return
	}
	if req.RelationType == "" {
		h.reply(msg, Response{Success: false, Error: "relation_type is required"})
		return
	}

	// Validate relation type
	if !IsValidRelationType(req.RelationType) {
		h.reply(msg, Response{Success: false, Error: "invalid relation_type"})
		return
	}

	// Create relation
	relation := &AssetRelation{
		ID:            uuid.New().String(),
		SourceAssetID: req.SourceAssetID,
		TargetAssetID: req.TargetAssetID,
		RelationType:  req.RelationType,
		CreatedAt:     time.Now(),
		Metadata:      req.Metadata,
	}

	if err := h.store.CreateRelation(relation); err != nil {
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
	ID string `json:"id"`
}

func (h *MetaHandler) handleRelationDelete(msg *nats.Msg) {
	var req DeleteRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}

	if req.ID == "" {
		h.reply(msg, Response{Success: false, Error: "id is required"})
		return
	}

	if err := h.store.DeleteRelation(req.ID); err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}

	log.Printf("[Meta] Relation deleted: %s", req.ID)
	h.reply(msg, Response{Success: true})
}
