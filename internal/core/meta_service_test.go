package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) *MetadataService {
	t.Helper()
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	// nil EventPublisher is safe: publish methods guard on p == nil.
	return NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)
}

func TestService_CreateAsset_Success(t *testing.T) {
	s := newTestService(t)

	asset, err := s.CreateAsset(CreateAssetRequest{Name: "pump-101"})
	require.NoError(t, err)
	require.NotEmpty(t, asset.ID)
	require.Equal(t, "pump-101", asset.Name)

	got, err := s.store.GetAsset(asset.ID)
	require.NoError(t, err)
	require.Equal(t, "pump-101", got.Name)
}

func TestService_CreateAsset_NameRequired(t *testing.T) {
	s := newTestService(t)

	_, err := s.CreateAsset(CreateAssetRequest{})
	require.Error(t, err)
	require.Equal(t, "name is required", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_CreateAsset_DuplicateName(t *testing.T) {
	s := newTestService(t)
	_, err := s.CreateAsset(CreateAssetRequest{Name: "dup"})
	require.NoError(t, err)

	_, err = s.CreateAsset(CreateAssetRequest{Name: "dup"})
	require.Error(t, err)
	require.Equal(t, "asset name already exists", err.Error())
	require.Equal(t, ErrConflict, KindOf(err))
}

func TestService_CreateAsset_TemplateNotFound(t *testing.T) {
	s := newTestService(t)

	_, err := s.CreateAsset(CreateAssetRequest{Name: "x", TemplateName: "missing"})
	require.Error(t, err)
	require.Equal(t, "template not found", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_UpdateAsset_NotFound(t *testing.T) {
	s := newTestService(t)

	_, err := s.UpdateAsset(UpdateAssetRequest{ID: "nope", Name: "n"})
	require.Error(t, err)
	require.Equal(t, "asset not found", err.Error())
	require.Equal(t, ErrNotFound, KindOf(err))
}

func TestService_UpdateAsset_Success(t *testing.T) {
	s := newTestService(t)
	created, err := s.CreateAsset(CreateAssetRequest{Name: "old"})
	require.NoError(t, err)

	updated, err := s.UpdateAsset(UpdateAssetRequest{ID: created.ID, Name: "new"})
	require.NoError(t, err)
	require.Equal(t, "new", updated.Name)
	require.Equal(t, created.CreatedAt.UnixMilli(), updated.CreatedAt.UnixMilli())
}

func TestService_DeleteAsset_IDRequired(t *testing.T) {
	s := newTestService(t)

	err := s.DeleteAsset(DeleteAssetRequest{})
	require.Error(t, err)
	require.Equal(t, "id is required", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_DeleteAsset_Success(t *testing.T) {
	s := newTestService(t)
	created, err := s.CreateAsset(CreateAssetRequest{Name: "gone"})
	require.NoError(t, err)

	require.NoError(t, s.DeleteAsset(DeleteAssetRequest{ID: created.ID}))

	got, err := s.store.GetAsset(created.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestService_CreateRelation_Validation(t *testing.T) {
	s := newTestService(t)

	_, err := s.CreateRelation(CreateRelationRequest{TargetAssetID: "b", RelationType: RelationPartOf})
	require.Equal(t, "source_asset_id is required", err.Error())

	_, err = s.CreateRelation(CreateRelationRequest{SourceAssetID: "a", RelationType: RelationPartOf})
	require.Equal(t, "target_asset_id is required", err.Error())

	_, err = s.CreateRelation(CreateRelationRequest{SourceAssetID: "a", TargetAssetID: "b"})
	require.Equal(t, "relation_type is required", err.Error())

	_, err = s.CreateRelation(CreateRelationRequest{SourceAssetID: "a", TargetAssetID: "b", RelationType: "bogus"})
	require.Equal(t, "invalid relation_type", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_CreateRelation_Success(t *testing.T) {
	s := newTestService(t)
	a, err := s.CreateAsset(CreateAssetRequest{Name: "child"})
	require.NoError(t, err)
	b, err := s.CreateAsset(CreateAssetRequest{Name: "parent"})
	require.NoError(t, err)

	rel, err := s.CreateRelation(CreateRelationRequest{
		SourceAssetID: a.ID, TargetAssetID: b.ID, RelationType: RelationPartOf,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rel.ID)
	require.Equal(t, RelationPartOf, rel.RelationType)
}

func TestService_DeleteRelation_Success(t *testing.T) {
	s := newTestService(t)
	a, _ := s.CreateAsset(CreateAssetRequest{Name: "c"})
	b, _ := s.CreateAsset(CreateAssetRequest{Name: "p"})
	rel, err := s.CreateRelation(CreateRelationRequest{
		SourceAssetID: a.ID, TargetAssetID: b.ID, RelationType: RelationPartOf,
	})
	require.NoError(t, err)

	require.NoError(t, s.DeleteRelation(DeleteRelationRequest{ID: rel.ID}))

	got, err := s.store.GetRelation(rel.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}
