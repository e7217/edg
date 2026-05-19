package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstraintsEvaluator_CheckRequiredAndForbiddenRelations(t *testing.T) {
	store, loader := newConstraintTestStore(t)
	evaluator := NewConstraintsEvaluator(loader)

	sensor, err := store.GetAsset("sensor-1")
	require.NoError(t, err)

	violations, err := evaluator.Check(sensor, store)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	assert.Equal(t, ConstraintRequiredRelation, violations[0].ConstraintType)
	assert.Equal(t, RelationPartOf, violations[0].RelationType)
	assert.Equal(t, "equipment", violations[0].TargetTemplate)

	createTraversalRelation(t, store, "rel-sensor-equipment", "sensor-1", "equipment-1", RelationPartOf)

	violations, err = evaluator.Check(sensor, store)
	require.NoError(t, err)
	assert.Empty(t, violations)

	createTraversalRelation(t, store, "rel-sensor-factory", "sensor-1", "factory-1", RelationConnectedTo)

	violations, err = evaluator.Check(sensor, store)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	assert.Equal(t, ConstraintForbiddenRelation, violations[0].ConstraintType)
	assert.Equal(t, RelationConnectedTo, violations[0].RelationType)
	assert.Equal(t, "factory", violations[0].TargetTemplate)
}

func TestMetaHandler_RelationCreateEnforceRejectsAndRollsBackConstraintViolation(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	store, loader := newConstraintTestStore(t)

	handler := NewMetaHandlerWithOptions(store, loader, MetaHandlerOptions{
		Events:                NewEventPublisher(nc),
		ConstraintEnforcement: ConstraintsEnforcementEnforce,
	})
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectRelationCreate, CreateRelationRequest{
		SourceAssetID: "sensor-1",
		TargetAssetID: "factory-1",
		RelationType:  RelationPartOf,
	})

	require.False(t, resp.Success)
	assert.Contains(t, resp.Error, "constraint violation")

	relations, err := store.GetRelationsBySourceAsset("sensor-1")
	require.NoError(t, err)
	assert.Empty(t, relations)
}

func TestMetaHandler_RelationCreateWarnPublishesViolationAndAllows(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	store, loader := newConstraintTestStore(t)
	violations := subscribeConstraintViolations(t, nc)

	handler := NewMetaHandlerWithOptions(store, loader, MetaHandlerOptions{
		Events:                NewEventPublisher(nc),
		ConstraintEnforcement: ConstraintsEnforcementWarn,
	})
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectRelationCreate, CreateRelationRequest{
		SourceAssetID: "sensor-1",
		TargetAssetID: "factory-1",
		RelationType:  RelationPartOf,
	})

	require.True(t, resp.Success, resp.Error)
	violation := requireConstraintViolation(t, violations)
	assert.Equal(t, "sensor-1", violation.AssetID)
	assert.Equal(t, ConstraintRequiredRelation, violation.ConstraintType)
}

func TestMetaHandler_ConstraintsCheckReportsCatalogViolations(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	store, loader := newConstraintTestStore(t)

	handler := NewMetaHandlerWithOptions(store, loader, MetaHandlerOptions{
		ConstraintEnforcement: ConstraintsEnforcementWarn,
	})
	require.NoError(t, handler.RegisterHandlers(nc))
	require.NoError(t, nc.Flush())

	resp := requestMeta(t, nc, SubjectConstraintsCheck, map[string]string{})
	require.True(t, resp.Success, resp.Error)

	var report ConstraintsReport
	require.NoError(t, json.Unmarshal(resp.Data, &report))
	assert.Equal(t, 1, report.ViolationCount)
	require.Len(t, report.Violations, 1)
	assert.Equal(t, "sensor-1", report.Violations[0].AssetID)
}

func subscribeConstraintViolations(t *testing.T, nc *nats.Conn) chan ConstraintViolation {
	t.Helper()

	ch := make(chan ConstraintViolation, 4)
	sub, err := nc.Subscribe(SubjectConstraintsViolation, func(msg *nats.Msg) {
		var violation ConstraintViolation
		if err := json.Unmarshal(msg.Data, &violation); err == nil {
			ch <- violation
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})
	require.NoError(t, nc.Flush())
	return ch
}

func requireConstraintViolation(t *testing.T, ch <-chan ConstraintViolation) ConstraintViolation {
	t.Helper()

	select {
	case violation := <-ch:
		return violation
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for constraint violation")
		return ConstraintViolation{}
	}
}

func newConstraintTestStore(t *testing.T) (*Store, *TemplateLoader) {
	t.Helper()

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	createTraversalAsset(t, store, "sensor-1", "Sensor 1", "sensor")
	createTraversalAsset(t, store, "equipment-1", "Equipment 1", "equipment")
	createTraversalAsset(t, store, "factory-1", "Factory 1", "factory")

	one := 1
	loader := NewTemplateLoader()
	loader.templates["sensor"] = &AssetTemplate{
		Name: "sensor",
		Constraints: TemplateConstraints{
			RequiredRelations: []RelationConstraint{
				{Type: RelationPartOf, TargetTemplate: "equipment", Min: &one, Max: &one},
			},
			ForbiddenRelations: []RelationConstraint{
				{Type: RelationConnectedTo, TargetTemplate: "factory"},
			},
		},
	}
	loader.templates["equipment"] = &AssetTemplate{Name: "equipment"}
	loader.templates["factory"] = &AssetTemplate{Name: "factory"}
	return store, loader
}
