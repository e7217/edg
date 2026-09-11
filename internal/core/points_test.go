package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPointsTestService(t *testing.T) (*MetadataService, *Store) {
	t.Helper()
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	require.NoError(t, store.CreateAsset(&Asset{ID: "pump-a", Name: "Pump A", TemplateName: "pump"}))
	require.NoError(t, store.CreateAsset(&Asset{ID: "pump-b", Name: "Pump B", TemplateName: "pump"}))
	return NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn), store
}

func modbusPoint(name, address string) Point {
	return Point{
		Name:      name,
		ValueType: ValueTypeNumber,
		Unit:      "°C",
		Address:   address,
		Encoding: map[string]any{
			"function": "holding",
			"type":     "int16",
			// A number, not the string "0.1". Storing protocol scales as strings
			// would force every adapter to re-parse them.
			"scale": 0.1,
		},
		Enabled: true,
	}
}

func TestUpsertPointList(t *testing.T) {
	svc, _ := newPointsTestService(t)

	pl, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:        "pump-a",
		Protocol:       "modbus-tcp",
		PollIntervalMS: 1000,
		Points:         []Point{modbusPoint("temperature", "0"), modbusPoint("pressure", "1")},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, pl.Version, "a first write starts at version 1")
	assert.Equal(t, "modbus-tcp", pl.Protocol)
	assert.Equal(t, 1000, pl.PollIntervalMS)
	require.Len(t, pl.Points, 2)

	// Points come back name-ordered, and the encoding keeps its JSON types.
	assert.Equal(t, "pressure", pl.Points[0].Name)
	assert.Equal(t, "temperature", pl.Points[1].Name)
	assert.Equal(t, 0.1, pl.Points[1].Encoding["scale"],
		"a numeric scale must survive the round trip as a number")
	assert.Equal(t, "holding", pl.Points[1].Encoding["function"])
	assert.True(t, pl.Points[1].Enabled)
	assert.False(t, pl.Points[1].CreatedAt.IsZero())
}

// The version is what an adapter will compare against its own config_version,
// so it must move on every write -- including one that changes nothing.
func TestUpsertPointListBumpsVersionEveryWrite(t *testing.T) {
	svc, _ := newPointsTestService(t)
	req := UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "0")},
	}

	first, err := svc.UpsertPointList(req)
	require.NoError(t, err)
	second, err := svc.UpsertPointList(req)
	require.NoError(t, err)
	third, err := svc.UpsertPointList(req)
	require.NoError(t, err)

	assert.Equal(t, 1, first.Version)
	assert.Equal(t, 2, second.Version, "an identical re-write still bumps the version")
	assert.Equal(t, 3, third.Version)
}

// Wholesale replacement is the operation an operator actually performs: here is
// the list. A point absent from the new list must disappear.
func TestUpsertPointListReplacesWholesale(t *testing.T) {
	svc, _ := newPointsTestService(t)

	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "0"), modbusPoint("retired", "9")},
	})
	require.NoError(t, err)

	pl, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "0"), modbusPoint("flow", "2")},
	})
	require.NoError(t, err)

	names := []string{}
	for _, p := range pl.Points {
		names = append(names, p.Name)
	}
	assert.ElementsMatch(t, []string{"temperature", "flow"}, names)
}

// created_at per point is the reason UpsertPointList does not use the
// DELETE-then-INSERT that UpsertTemplate does: an operator re-importing a
// spreadsheet needs to see which declarations are new.
//
// The stored value is backdated before the second write. CURRENT_TIMESTAMP has
// one-second resolution in SQLite, so comparing two timestamps taken inside the
// same second cannot tell "preserved" from "overwritten" -- an earlier version
// of this test passed with created_at explicitly clobbered.
func TestUpsertPointListPreservesCreatedAt(t *testing.T) {
	svc, store := newPointsTestService(t)

	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "0")},
	})
	require.NoError(t, err)

	const backdated = "2020-01-02 03:04:05"
	_, err = store.db.Exec(
		`UPDATE asset_points SET created_at = ? WHERE asset_id = ? AND name = ?`,
		backdated, "pump-a", "temperature")
	require.NoError(t, err)

	second, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "40001"), modbusPoint("flow", "2")},
	})
	require.NoError(t, err)

	byName := map[string]Point{}
	for _, p := range second.Points {
		byName[p.Name] = p
	}
	assert.Equal(t, 2020, byName["temperature"].CreatedAt.Year(),
		"an updated point keeps its original created_at")
	assert.Equal(t, "40001", byName["temperature"].Address, "and takes the new address")
	assert.Greater(t, byName["flow"].CreatedAt.Year(), 2020,
		"a point added in this write is newly created")
	assert.Greater(t, byName["temperature"].UpdatedAt.Year(), 2020,
		"but updated_at does move")
}

// The encoding is arbitrary JSON on purpose: a numeric scale must stay numeric
// rather than being flattened to the string "0.1", which every adapter would
// then have to re-parse.
func TestPointEncodingKeepsJSONTypes(t *testing.T) {
	svc, _ := newPointsTestService(t)
	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points: []Point{{
			Name: "mixed", ValueType: ValueTypeNumber, Address: "0", Enabled: true,
			Encoding: map[string]any{
				"scale":     0.1,
				"registers": float64(2),
				"signed":    true,
				"function":  "holding",
			},
		}},
	})
	require.NoError(t, err)

	pl, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	enc := pl.Points[0].Encoding
	assert.Equal(t, 0.1, enc["scale"], "a float stays a float")
	assert.Equal(t, float64(2), enc["registers"], "an integer stays a number")
	assert.Equal(t, true, enc["signed"], "a bool stays a bool")
	assert.Equal(t, "holding", enc["function"])
}

func TestUpsertPointListValidation(t *testing.T) {
	svc, _ := newPointsTestService(t)
	base := func() UpsertPointListRequest {
		return UpsertPointListRequest{
			AssetID:  "pump-a",
			Protocol: "modbus-tcp",
			Points:   []Point{modbusPoint("temperature", "0")},
		}
	}

	tests := map[string]struct {
		mutate func(*UpsertPointListRequest)
		kind   ErrorKind
		msg    string
	}{
		"no asset id": {
			mutate: func(r *UpsertPointListRequest) { r.AssetID = "" },
			kind:   ErrValidation, msg: "asset_id is required",
		},
		"unknown asset": {
			mutate: func(r *UpsertPointListRequest) { r.AssetID = "no-such-asset" },
			kind:   ErrNotFound, msg: "asset not found",
		},
		"no protocol": {
			mutate: func(r *UpsertPointListRequest) { r.Protocol = "" },
			kind:   ErrValidation, msg: "protocol is required",
		},
		"negative poll interval": {
			mutate: func(r *UpsertPointListRequest) { r.PollIntervalMS = -1 },
			kind:   ErrValidation, msg: "poll_interval_ms",
		},
		"point without a name": {
			mutate: func(r *UpsertPointListRequest) { r.Points[0].Name = "" },
			kind:   ErrValidation, msg: "point name is required",
		},
		"point without an address": {
			mutate: func(r *UpsertPointListRequest) { r.Points[0].Address = "" },
			kind:   ErrValidation, msg: "requires an address",
		},
		"invalid value type": {
			mutate: func(r *UpsertPointListRequest) { r.Points[0].ValueType = "INT16" },
			kind:   ErrValidation, msg: "invalid value_type",
		},
		"duplicate point name": {
			mutate: func(r *UpsertPointListRequest) {
				r.Points = append(r.Points, modbusPoint("temperature", "5"))
			},
			kind: ErrValidation, msg: "declared more than once",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := base()
			tc.mutate(&req)
			_, err := svc.UpsertPointList(req)
			require.Error(t, err)
			assert.Equal(t, tc.kind, KindOf(err))
			assert.Contains(t, err.Error(), tc.msg)
		})
	}
}

// A rejected write must leave nothing behind. The encoding check runs before
// any row is touched for exactly this reason.
func TestUpsertPointListRejectionIsAtomic(t *testing.T) {
	svc, _ := newPointsTestService(t)

	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("good", "0")},
	})
	require.NoError(t, err)

	// The second point is invalid; the first is not.
	_, err = svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("replacement", "1"), {Name: "broken"}},
	})
	require.Error(t, err)

	pl, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	require.Len(t, pl.Points, 1)
	assert.Equal(t, "good", pl.Points[0].Name, "the rejected write changed nothing")
	assert.Equal(t, 1, pl.Version, "and did not bump the version")
}

// An asset with nothing to poll is a normal state, not a missing record: most
// assets in a plant model are logical groupings.
func TestGetPointListForAssetWithNone(t *testing.T) {
	svc, _ := newPointsTestService(t)

	pl, err := svc.GetPointList("pump-b")
	require.NoError(t, err)
	assert.Equal(t, "pump-b", pl.AssetID)
	assert.Empty(t, pl.Points)
	assert.Equal(t, 0, pl.Version)

	_, err = svc.GetPointList("no-such-asset")
	require.Error(t, err)
	assert.Equal(t, ErrNotFound, KindOf(err))
}

func TestDeletePointList(t *testing.T) {
	svc, store := newPointsTestService(t)
	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "0")},
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeletePointList(DeletePointListRequest{AssetID: "pump-a"}))

	pl, err := store.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Nil(t, pl)

	count, err := store.CountPoints()
	require.NoError(t, err)
	assert.Equal(t, 0, count, "points cascade with the list")

	err = svc.DeletePointList(DeletePointListRequest{AssetID: "pump-a"})
	assert.Equal(t, ErrNotFound, KindOf(err), "deleting twice is a not-found")
}

// Deleting the asset must take its declarations with it, or the plant model
// accumulates point lists for equipment that no longer exists.
func TestDeleteAssetCascadesPointList(t *testing.T) {
	svc, store := newPointsTestService(t)
	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID:  "pump-a",
		Protocol: "modbus-tcp",
		Points:   []Point{modbusPoint("temperature", "0")},
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteAsset(DeleteAssetRequest{ID: "pump-a"}))

	pl, err := store.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Nil(t, pl, "the point list went with the asset")
	count, err := store.CountPoints()
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// Two assets on the same template must be able to hold different addresses.
// That is the whole reason points hang off the asset rather than the template.
func TestTwoAssetsSameTemplateDifferentAddresses(t *testing.T) {
	svc, _ := newPointsTestService(t)

	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID: "pump-a", Protocol: "modbus-tcp",
		Points: []Point{modbusPoint("temperature", "0")},
	})
	require.NoError(t, err)
	_, err = svc.UpsertPointList(UpsertPointListRequest{
		AssetID: "pump-b", Protocol: "modbus-tcp",
		Points: []Point{modbusPoint("temperature", "100")},
	})
	require.NoError(t, err)

	a, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	b, err := svc.GetPointList("pump-b")
	require.NoError(t, err)
	assert.Equal(t, "0", a.Points[0].Address)
	assert.Equal(t, "100", b.Points[0].Address)
}

func TestListAndSearchPoints(t *testing.T) {
	svc, _ := newPointsTestService(t)
	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID: "pump-a", Protocol: "modbus-tcp",
		Points: []Point{modbusPoint("temperature", "0"), modbusPoint("flow_rate", "2")},
	})
	require.NoError(t, err)
	_, err = svc.UpsertPointList(UpsertPointListRequest{
		AssetID: "pump-b", Protocol: "opcua",
		Points: []Point{modbusPoint("temperature", "ns=2;s=Temp")},
	})
	require.NoError(t, err)

	lists, err := svc.ListPointLists()
	require.NoError(t, err)
	require.Len(t, lists, 2)
	assert.Equal(t, "pump-a", lists[0].AssetID, "lists are asset-ordered")
	assert.Len(t, lists[0].Points, 2)

	// The query behind "which boxes call this tag something else".
	hits, err := svc.SearchPoints("temp")
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "pump-a", hits[0].AssetID)
	assert.Equal(t, "modbus-tcp", hits[0].Protocol)
	assert.Equal(t, "pump-b", hits[1].AssetID)
	assert.Equal(t, "opcua", hits[1].Protocol)

	none, err := svc.SearchPoints("no-such-tag")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestPointListWithNoPointsIsVisible(t *testing.T) {
	svc, _ := newPointsTestService(t)
	// An operator who declared a protocol but has not entered points yet must
	// still see the row, not have it vanish from the list.
	_, err := svc.UpsertPointList(UpsertPointListRequest{
		AssetID: "pump-a", Protocol: "modbus-tcp", Points: []Point{},
	})
	require.NoError(t, err)

	lists, err := svc.ListPointLists()
	require.NoError(t, err)
	require.Len(t, lists, 1)
	assert.Equal(t, "pump-a", lists[0].AssetID)
	assert.Empty(t, lists[0].Points)
}
