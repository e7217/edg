package core

import (
	"fmt"
	"strings"
	"time"
)

const (
	ConstraintRequiredRelation  = "required_relation"
	ConstraintForbiddenRelation = "forbidden_relation"

	ConstraintsEnforcementWarn     = "warn"
	ConstraintsEnforcementEnforce  = "enforce"
	ConstraintsEnforcementDisabled = "disabled"
)

type ConstraintViolation struct {
	AssetID         string       `json:"asset_id"`
	AssetName       string       `json:"asset_name,omitempty"`
	TemplateName    string       `json:"template_name,omitempty"`
	ConstraintType  string       `json:"constraint_type"`
	RelationType    RelationType `json:"relation_type"`
	TargetTemplate  string       `json:"target_template"`
	ObservedCount   int          `json:"observed_count"`
	ExpectedMin     *int         `json:"expected_min,omitempty"`
	ExpectedMax     *int         `json:"expected_max,omitempty"`
	Message         string       `json:"message"`
	DetectedAt      time.Time    `json:"detected_at"`
	EnforcementMode string       `json:"enforcement_mode,omitempty"`
}

type ConstraintsReport struct {
	ViolationCount int                   `json:"violation_count"`
	Violations     []ConstraintViolation `json:"violations"`
	CheckedAt      time.Time             `json:"checked_at"`
}

type ConstraintsEvaluator struct {
	loader *TemplateLoader
}

func NewConstraintsEvaluator(loader *TemplateLoader) *ConstraintsEvaluator {
	return &ConstraintsEvaluator{loader: loader}
}

func (e *ConstraintsEvaluator) Check(asset *Asset, store *Store) ([]ConstraintViolation, error) {
	if e == nil || e.loader == nil || store == nil || asset == nil || asset.TemplateName == "" {
		return nil, nil
	}
	template := e.loader.Get(asset.TemplateName)
	if template == nil {
		return nil, nil
	}

	relations, err := store.GetRelationsBySourceAsset(asset.ID)
	if err != nil {
		return nil, err
	}

	violations := []ConstraintViolation{}
	for _, constraint := range template.Constraints.RequiredRelations {
		count, err := countMatchingRelations(store, relations, constraint)
		if err != nil {
			return nil, err
		}
		min := constraint.MinValue(1)
		max, hasMax := constraint.MaxValue()
		if count < min {
			violations = append(violations, newConstraintViolation(
				asset,
				ConstraintRequiredRelation,
				constraint,
				count,
				&min,
				maxPtrIf(hasMax, max),
				fmt.Sprintf("asset %s requires at least %d %s relation(s) to template %s, found %d",
					asset.ID, min, constraint.Type, constraint.TargetTemplate, count),
			))
		}
		if hasMax && count > max {
			violations = append(violations, newConstraintViolation(
				asset,
				ConstraintRequiredRelation,
				constraint,
				count,
				&min,
				&max,
				fmt.Sprintf("asset %s allows at most %d %s relation(s) to template %s, found %d",
					asset.ID, max, constraint.Type, constraint.TargetTemplate, count),
			))
		}
	}

	for _, constraint := range template.Constraints.ForbiddenRelations {
		count, err := countMatchingRelations(store, relations, constraint)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			zero := 0
			violations = append(violations, newConstraintViolation(
				asset,
				ConstraintForbiddenRelation,
				constraint,
				count,
				nil,
				&zero,
				fmt.Sprintf("asset %s forbids %s relation(s) to template %s, found %d",
					asset.ID, constraint.Type, constraint.TargetTemplate, count),
			))
		}
	}

	return violations, nil
}

func (e *ConstraintsEvaluator) CheckAll(store *Store) (ConstraintsReport, error) {
	report := ConstraintsReport{CheckedAt: time.Now().UTC()}
	if e == nil || store == nil {
		return report, nil
	}

	assets, err := store.ListAssets()
	if err != nil {
		return report, err
	}
	for _, asset := range assets {
		violations, err := e.Check(asset, store)
		if err != nil {
			return report, err
		}
		report.Violations = append(report.Violations, violations...)
	}
	report.ViolationCount = len(report.Violations)
	return report, nil
}

func countMatchingRelations(store *Store, relations []*AssetRelation, constraint RelationConstraint) (int, error) {
	count := 0
	for _, relation := range relations {
		if relation.RelationType != constraint.Type {
			continue
		}
		target, err := store.GetAsset(relation.TargetAssetID)
		if err != nil {
			return 0, err
		}
		if target == nil {
			continue
		}
		if constraint.TargetTemplate == "" || target.TemplateName == constraint.TargetTemplate {
			count++
		}
	}
	return count, nil
}

func newConstraintViolation(asset *Asset, constraintType string, constraint RelationConstraint, count int, min, max *int, message string) ConstraintViolation {
	return ConstraintViolation{
		AssetID:        asset.ID,
		AssetName:      asset.Name,
		TemplateName:   asset.TemplateName,
		ConstraintType: constraintType,
		RelationType:   constraint.Type,
		TargetTemplate: constraint.TargetTemplate,
		ObservedCount:  count,
		ExpectedMin:    min,
		ExpectedMax:    max,
		Message:        message,
		DetectedAt:     time.Now().UTC(),
	}
}

func maxPtrIf(ok bool, value int) *int {
	if !ok {
		return nil
	}
	return &value
}

func (c RelationConstraint) MinValue(defaultValue int) int {
	if c.Min == nil {
		return defaultValue
	}
	return *c.Min
}

func (c RelationConstraint) MaxValue() (int, bool) {
	if c.Max == nil {
		return 0, false
	}
	return *c.Max, true
}

func validateTemplateConstraints(loader *TemplateLoader) error {
	if loader == nil {
		return nil
	}
	for _, template := range loader.List() {
		constraints := append([]RelationConstraint{}, template.Constraints.RequiredRelations...)
		constraints = append(constraints, template.Constraints.ForbiddenRelations...)
		for _, constraint := range constraints {
			if constraint.Type == "" {
				return fmt.Errorf("template %s constraint relation type is required", template.Name)
			}
			if !IsValidRelationType(constraint.Type) {
				return fmt.Errorf("template %s constraint has invalid relation type: %s", template.Name, constraint.Type)
			}
			if constraint.TargetTemplate == "" {
				return fmt.Errorf("template %s constraint target_template is required", template.Name)
			}
			if !loader.Exists(constraint.TargetTemplate) {
				return fmt.Errorf("template %s constraint references unknown target_template: %s", template.Name, constraint.TargetTemplate)
			}
			if constraint.Min != nil && *constraint.Min < 0 {
				return fmt.Errorf("template %s constraint min must be >= 0", template.Name)
			}
			if constraint.Max != nil && *constraint.Max < 0 {
				return fmt.Errorf("template %s constraint max must be >= 0", template.Name)
			}
			if constraint.Min != nil && constraint.Max != nil && *constraint.Min > *constraint.Max {
				return fmt.Errorf("template %s constraint min cannot exceed max", template.Name)
			}
		}
	}
	return nil
}

func constraintsViolationError(violations []ConstraintViolation) string {
	if len(violations) == 0 {
		return ""
	}
	messages := make([]string, 0, len(violations))
	for _, violation := range violations {
		messages = append(messages, violation.Message)
	}
	return "constraint violation: " + strings.Join(messages, "; ")
}
