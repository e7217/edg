package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAssetID(t *testing.T) {
	valid := []string{
		"pump-a",
		"line3.flow",
		// What the reference adapters actually publish. Refusing this would mean
		// a declared id could never match the telemetry, which is the point.
		"modbus-127.0.0.1-1",
		"tag:pump:01",
		"Pump_A_2026",
		// Every id already in the wild is a server-generated UUID and must stay
		// valid, so one rule covers the whole system.
		"ce90dbd0-34c7-4163-82d5-3dcc5dbea55b",
		"a",
	}
	for _, id := range valid {
		assert.NoError(t, ValidateAssetID(id), "%q should be valid", id)
	}

	invalid := map[string]string{
		"":               "required",
		"-leading-dash":  "must start with",
		".leading-dot":   "must start with",
		"has space":      "must start with",
		"has/slash":      "must start with",
		"has,comma":      "must start with",
		"has=equals":     "must start with",
		"has\\backslash": "must start with",
		".":              "must start with",
		"..":             "must start with",
		"한글":             "must start with",
	}
	for id, want := range invalid {
		err := ValidateAssetID(id)
		require.Error(t, err, "%q should be invalid", id)
		assert.Contains(t, err.Error(), want, "for %q", id)
	}

	long := make([]byte, assetIDMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	err := ValidateAssetID(string(long))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "longer than")
}

func TestCreateAssetWithOperatorSuppliedID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

	// The id an adapter would publish, chosen by the operator so the two agree.
	asset, err := svc.CreateAsset(CreateAssetRequest{ID: "modbus-127.0.0.1-1", Name: "Pump A"})
	require.NoError(t, err)
	assert.Equal(t, "modbus-127.0.0.1-1", asset.ID)

	// Which is the whole point: ingest now finds it.
	exists, err := store.AssetExists("modbus-127.0.0.1-1")
	require.NoError(t, err)
	assert.True(t, exists)
}

// Omitting the id must behave exactly as before, so nothing existing changes.
func TestCreateAssetWithoutIDStillGeneratesUUID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

	asset, err := svc.CreateAsset(CreateAssetRequest{Name: "Pump A"})
	require.NoError(t, err)
	assert.Len(t, asset.ID, 36, "a generated id is still a UUID")
	assert.NoError(t, ValidateAssetID(asset.ID), "and satisfies the same rule")
}

// A duplicate id is a conflict, never an overwrite: replacing an asset would
// take its point list and its relations with it.
func TestCreateAssetRejectsDuplicateID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

	_, err = svc.CreateAsset(CreateAssetRequest{ID: "pump-a", Name: "Pump A"})
	require.NoError(t, err)
	_, err = svc.CreateAsset(CreateAssetRequest{ID: "pump-a", Name: "A Different Pump"})
	require.Error(t, err)
	assert.Equal(t, ErrConflict, KindOf(err))
	assert.Contains(t, err.Error(), "asset id already exists")

	// The original survived untouched.
	asset, err := store.GetAsset("pump-a")
	require.NoError(t, err)
	assert.Equal(t, "Pump A", asset.Name)
}

func TestCreateAssetRejectsInvalidID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

	// A path separator would escape the point-export directory; a comma would
	// need escaping in every line-protocol write.
	for _, bad := range []string{"pumps/a", "pump,a", "-pump"} {
		_, err := svc.CreateAsset(CreateAssetRequest{ID: bad, Name: "x-" + bad})
		require.Error(t, err, "id %q", bad)
		assert.Equal(t, ErrValidation, KindOf(err))
	}
}

// The end that matters: an operator-chosen id makes the declared point list and
// the adapter's telemetry join, which is what a UUID made impossible.
func TestOperatorIDJoinsPointsToTelemetry(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

	const publishedID = "modbus-127.0.0.1-1"
	_, err = svc.CreateAsset(CreateAssetRequest{ID: publishedID, Name: "Pump A"})
	require.NoError(t, err)
	_, err = svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  publishedID,
		Protocol: "modbus-tcp",
		Points:   []Point{{Name: "temperature", ValueType: ValueTypeNumber, Address: "0", Enabled: true}},
	})
	require.NoError(t, err)

	// What the ingest path does with an adapter's asset_id.
	exists, err := store.AssetExists(publishedID)
	require.NoError(t, err)
	require.True(t, exists, "telemetry for this id is no longer an undeclared asset")

	pl, err := svc.GetPointList(publishedID)
	require.NoError(t, err)
	require.Len(t, pl.Points, 1)
	assert.Equal(t, "temperature", pl.Points[0].Name,
		"and the declared point is reachable from the id the adapter publishes")
}

// The id identifies rather than renames. If an update could change it, the
// asset's point list, its relations and every reading already stored under the
// old id would be orphaned at once.
func TestUpdateAssetCannotChangeTheID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)

	_, err = svc.CreateAsset(CreateAssetRequest{ID: "pump-a", Name: "Pump A"})
	require.NoError(t, err)
	_, err = svc.UpsertPointList(UpsertPointListRequest{
		AssetID: "pump-a", Protocol: "modbus-tcp",
		Points: []Point{{Name: "t", ValueType: ValueTypeNumber, Address: "0", Enabled: true}},
	})
	require.NoError(t, err)

	// ID is the lookup key, so this renames the asset and nothing else.
	updated, err := svc.UpdateAsset(UpdateAssetRequest{ID: "pump-a", Name: "Pump A (renamed)"})
	require.NoError(t, err)
	assert.Equal(t, "pump-a", updated.ID)
	assert.Equal(t, "Pump A (renamed)", updated.Name)

	pl, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Len(t, pl.Points, 1, "the point list is still reachable")
}
