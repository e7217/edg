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

func (h *MetaHandler) reply(msg *nats.Msg, resp Response) {
	data, _ := json.Marshal(resp)
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
