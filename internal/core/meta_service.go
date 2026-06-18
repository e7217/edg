package core

import (
	"log"
	"time"

	"github.com/google/uuid"
)

// MetadataService holds the transport-agnostic master-data mutation rules.
// HTTP, CLI, MCP, and the NATS handlers all call into this one place, so
// validation, constraint enforcement, and event publishing stay consistent.
type MetadataService struct {
	store                 *Store
	loader                *TemplateLoader
	events                *EventPublisher
	constraints           *ConstraintsEvaluator
	constraintEnforcement string
}

func NewMetadataService(store *Store, loader *TemplateLoader, events *EventPublisher, enforcement string) *MetadataService {
	if enforcement == "" {
		enforcement = ConstraintsEnforcementWarn
	}
	return &MetadataService{
		store:                 store,
		loader:                loader,
		events:                events,
		constraints:           NewConstraintsEvaluator(loader),
		constraintEnforcement: enforcement,
	}
}

// CheckConstraints evaluates all template constraints across the catalog.
func (s *MetadataService) CheckConstraints() (ConstraintsReport, error) {
	return s.constraints.CheckAll(s.store)
}

func (s *MetadataService) CreateAsset(req CreateAssetRequest) (*Asset, error) {
	if req.Name == "" {
		return nil, newServiceError(ErrValidation, "name is required")
	}
	if existing, _ := s.store.GetAssetByName(req.Name); existing != nil {
		return nil, newServiceError(ErrConflict, "asset name already exists")
	}
	if req.TemplateName != "" && !s.loader.Exists(req.TemplateName) {
		return nil, newServiceError(ErrValidation, "template not found")
	}

	asset := &Asset{
		ID:           uuid.New().String(),
		Name:         req.Name,
		TemplateName: req.TemplateName,
		Labels:       req.Labels,
		ExternalIDs:  req.ExternalIDs,
		Source:       req.Source,
		Attributes:   req.Attributes,
		CreatedAt:    time.Now(),
	}
	if err := s.store.CreateAsset(asset); err != nil {
		return nil, err
	}

	s.events.PublishAssetChanged(MetaChangeEvent{
		EventType: EventCreated,
		EntityID:  asset.ID,
		Source:    asset.Source,
		After:     asset,
	})
	return asset, nil
}

func (s *MetadataService) UpdateAsset(req UpdateAssetRequest) (*Asset, error) {
	if req.ID == "" {
		return nil, newServiceError(ErrValidation, "id is required")
	}
	if req.Name == "" {
		return nil, newServiceError(ErrValidation, "name is required")
	}
	if req.TemplateName != "" && !s.loader.Exists(req.TemplateName) {
		return nil, newServiceError(ErrValidation, "template not found")
	}

	existing, err := s.store.GetAsset(req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, newServiceError(ErrNotFound, "asset not found")
	}

	asset := &Asset{
		ID:           req.ID,
		Name:         req.Name,
		TemplateName: req.TemplateName,
		Labels:       req.Labels,
		ExternalIDs:  req.ExternalIDs,
		Source:       req.Source,
		Attributes:   req.Attributes,
		CreatedAt:    existing.CreatedAt,
	}
	if err := s.store.UpdateAsset(asset); err != nil {
		return nil, err
	}

	updated, err := s.store.GetAsset(req.ID)
	if err != nil {
		return nil, err
	}

	s.events.PublishAssetChanged(MetaChangeEvent{
		EventType: EventUpdated,
		EntityID:  updated.ID,
		Source:    updated.Source,
		Before:    existing,
		After:     updated,
	})
	return updated, nil
}

func (s *MetadataService) DeleteAsset(req DeleteAssetRequest) error {
	if req.ID == "" {
		return newServiceError(ErrValidation, "id is required")
	}

	before, err := s.store.GetAsset(req.ID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteAsset(req.ID); err != nil {
		return err
	}

	s.events.PublishAssetChanged(MetaChangeEvent{
		EventType: EventDeleted,
		EntityID:  req.ID,
		Source:    req.Source,
		Before:    before,
	})
	return nil
}

func (s *MetadataService) CreateRelation(req CreateRelationRequest) (*AssetRelation, error) {
	if req.SourceAssetID == "" {
		return nil, newServiceError(ErrValidation, "source_asset_id is required")
	}
	if req.TargetAssetID == "" {
		return nil, newServiceError(ErrValidation, "target_asset_id is required")
	}
	if req.RelationType == "" {
		return nil, newServiceError(ErrValidation, "relation_type is required")
	}
	if !IsValidRelationType(req.RelationType) {
		return nil, newServiceError(ErrValidation, "invalid relation_type")
	}

	relation := &AssetRelation{
		ID:            uuid.New().String(),
		SourceAssetID: req.SourceAssetID,
		TargetAssetID: req.TargetAssetID,
		RelationType:  req.RelationType,
		CreatedAt:     time.Now(),
		Metadata:      req.Metadata,
	}
	if err := s.store.CreateRelation(relation); err != nil {
		return nil, err
	}

	if violations, err := s.checkRelationConstraints(relation); err != nil {
		_ = s.store.DeleteRelation(relation.ID)
		return nil, err
	} else if !s.allowConstraintViolations(violations) {
		_ = s.store.DeleteRelation(relation.ID)
		return nil, newServiceError(ErrConstraint, "%s", constraintsViolationError(violations))
	}

	s.events.PublishRelationChanged(MetaChangeEvent{
		EventType: EventCreated,
		EntityID:  relation.ID,
		Source:    req.Source,
		After:     relation,
	})
	return relation, nil
}

func (s *MetadataService) DeleteRelation(req DeleteRelationRequest) error {
	if req.ID == "" {
		return newServiceError(ErrValidation, "id is required")
	}

	before, err := s.store.GetRelation(req.ID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteRelation(req.ID); err != nil {
		return err
	}

	s.events.PublishRelationChanged(MetaChangeEvent{
		EventType: EventDeleted,
		EntityID:  req.ID,
		Source:    req.Source,
		Before:    before,
	})
	return nil
}

// checkRelationConstraints and allowConstraintViolations are moved verbatim from
// MetaHandler, with the receiver changed to *MetadataService.

func (s *MetadataService) checkRelationConstraints(relation *AssetRelation) ([]ConstraintViolation, error) {
	if s == nil || s.constraintEnforcement == ConstraintsEnforcementDisabled || s.constraints == nil || relation == nil {
		return nil, nil
	}

	assetIDs := []string{relation.SourceAssetID, relation.TargetAssetID}
	seen := map[string]bool{}
	var violations []ConstraintViolation
	for _, assetID := range assetIDs {
		if seen[assetID] {
			continue
		}
		seen[assetID] = true
		asset, err := s.store.GetAsset(assetID)
		if err != nil {
			return nil, err
		}
		if asset == nil {
			continue
		}
		assetViolations, err := s.constraints.Check(asset, s.store)
		if err != nil {
			return nil, err
		}
		violations = append(violations, assetViolations...)
	}
	return violations, nil
}

func (s *MetadataService) allowConstraintViolations(violations []ConstraintViolation) bool {
	if len(violations) == 0 || s.constraintEnforcement == ConstraintsEnforcementDisabled {
		return true
	}
	for _, violation := range violations {
		violation.EnforcementMode = s.constraintEnforcement
		s.events.PublishConstraintViolation(violation)
		log.Printf("[Meta] Constraint violation: %s", violation.Message)
	}
	return s.constraintEnforcement != ConstraintsEnforcementEnforce
}
