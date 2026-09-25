package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPlantService(t *testing.T) *MetadataService {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "m.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	loader, err := NewTemplateLoaderWithStore(store)
	require.NoError(t, err)
	return NewMetadataService(store, loader, nil, ConstraintsEnforcementWarn)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// A plant as an SI would type it into a spreadsheet: Korean names, a pump that
// is part of a line, and two point lists with Modbus encodings.
func seedBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "templates/pump.yaml", "name: pump\nresources:\n  - name: temperature\n    valueType: NUMBER\n    unit: \"°C\"\n")
	write(t, dir, "templates/line.yaml", "name: line\nresources: []\n")
	write(t, dir, "assets.csv", "\ufeffid,name,template_name,labels,attr.vendor,ext.aas\n"+
		"line-1,1라인,line,,,\n"+
		"pump-a,A 펌프,pump,critical;cooling,Grundfos,urn:aas:pump-a\n"+
		"pump-b,B 펌프,pump\n") // a spreadsheet drops trailing empty cells
	write(t, dir, "relations.csv", "source_asset_id,relation_type,target_asset_id\n"+
		"pump-a,partOf,line-1\npump-b,partOf,line-1\n")
	write(t, dir, "points.csv", "asset_id,protocol,poll_interval_ms,name,value_type,unit,address,enabled,encoding.type,encoding.scale,encoding.function\n"+
		"pump-a,modbus-tcp,1000,temperature,NUMBER,°C,0,true,int16,0.1,holding\n"+
		"pump-a,,,flow,NUMBER,L/min,100,,float32,,input\n"+
		"pump-b,modbus-tcp,,temperature,NUMBER,°C,0,false,int16,0.1,holding\n")
	return dir
}

func TestImportPlant(t *testing.T) {
	svc := newPlantService(t)
	rep, err := svc.ImportPlant(seedBundle(t))
	require.NoError(t, err)
	assert.Empty(t, rep.Problems)
	assert.Equal(t, 2, rep.Templates)
	assert.True(t, rep.TemplatesChanged)
	assert.Equal(t, PlantCounts{Created: 3}, rep.Assets)
	assert.Equal(t, PlantCounts{Created: 2}, rep.Relations)
	assert.Equal(t, PlantCounts{Created: 2}, rep.Points)

	a, err := svc.store.GetAsset("pump-a")
	require.NoError(t, err)
	assert.Equal(t, "A 펌프", a.Name, "the BOM is stripped and Korean survives")
	assert.Equal(t, []string{"critical", "cooling"}, a.Labels)
	assert.Equal(t, map[string]string{"vendor": "Grundfos"}, a.Attributes)
	assert.Equal(t, map[string]string{"aas": "urn:aas:pump-a"}, a.ExternalIDs)

	pl, err := svc.store.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Equal(t, "modbus-tcp", pl.Protocol, "a blank protocol cell inherits the list's")
	assert.Equal(t, 1000, pl.PollIntervalMS)
	require.Len(t, pl.Points, 2)
	byName := map[string]Point{}
	for _, p := range pl.Points {
		byName[p.Name] = p
	}
	assert.Equal(t, 0.1, byName["temperature"].Encoding["scale"], "a numeric cell stays a number")
	assert.Equal(t, "int16", byName["temperature"].Encoding["type"])
	assert.NotContains(t, byName["flow"].Encoding, "scale", "an empty cell is an absent key")
	assert.True(t, byName["flow"].Enabled, "a blank enabled cell means enabled")

	b, err := svc.store.GetPointList("pump-b")
	require.NoError(t, err)
	assert.False(t, b.Points[0].Enabled)
}

// Importing the same bundle twice changes nothing, and in particular does not
// bump a point list's version -- that would rebuild every provisioned adapter.
func TestImportPlantIsIdempotent(t *testing.T) {
	svc := newPlantService(t)
	dir := seedBundle(t)
	_, err := svc.ImportPlant(dir)
	require.NoError(t, err)
	before, err := svc.store.GetPointList("pump-a")
	require.NoError(t, err)

	rep, err := svc.ImportPlant(dir)
	require.NoError(t, err)
	assert.False(t, rep.TemplatesChanged)
	assert.Equal(t, PlantCounts{Unchanged: 3}, rep.Assets)
	assert.Equal(t, PlantCounts{Unchanged: 2}, rep.Relations)
	assert.Equal(t, PlantCounts{Unchanged: 2}, rep.Points)

	after, err := svc.store.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Equal(t, before.Version, after.Version)
}

// Export then import into an empty database reproduces the plant, and a
// second export is byte-identical: the bundle is a faithful backup.
func TestExportPlantRoundTrips(t *testing.T) {
	src := newPlantService(t)
	_, err := src.ImportPlant(seedBundle(t))
	require.NoError(t, err)

	out1 := t.TempDir()
	require.NoError(t, src.ExportPlant(out1))

	dst := newPlantService(t)
	rep, err := dst.ImportPlant(out1)
	require.NoError(t, err)
	assert.Empty(t, rep.Problems)
	assert.Equal(t, 3, rep.Assets.Created)
	assert.Equal(t, 2, rep.Relations.Created)
	assert.Equal(t, 2, rep.Points.Created)

	out2 := t.TempDir()
	require.NoError(t, dst.ExportPlant(out2))
	for _, f := range []string{"assets.csv", "relations.csv", "points.csv"} {
		a, err := os.ReadFile(filepath.Join(out1, f))
		require.NoError(t, err)
		b, err := os.ReadFile(filepath.Join(out2, f))
		require.NoError(t, err)
		assert.Equal(t, string(a), string(b), f)
		assert.True(t, strings.HasPrefix(string(a), "\ufeff"), "%s carries a BOM for Excel", f)
	}
}

func TestImportPlantEditsAnExistingPlant(t *testing.T) {
	svc := newPlantService(t)
	dir := seedBundle(t)
	_, err := svc.ImportPlant(dir)
	require.NoError(t, err)
	v1, _ := svc.store.GetPointList("pump-a")

	write(t, dir, "assets.csv", "id,name,template_name\npump-a,A 펌프 (교체),pump\n")
	write(t, dir, "relations.csv", "source_asset_id,relation_type,target_asset_id\n")
	write(t, dir, "points.csv", "asset_id,protocol,name,value_type,address,encoding.type\n"+
		"pump-a,modbus-tcp,temperature,NUMBER,0,int16\n")
	rep, err := svc.ImportPlant(dir)
	require.NoError(t, err)
	assert.Equal(t, PlantCounts{Updated: 1}, rep.Assets)
	assert.Equal(t, PlantCounts{Updated: 1}, rep.Points)

	v2, _ := svc.store.GetPointList("pump-a")
	assert.Equal(t, v1.Version+1, v2.Version)
	assert.Len(t, v2.Points, 1, "a list is replaced wholesale")
	b, _ := svc.store.GetAsset("pump-b")
	assert.NotNil(t, b, "import never deletes: pump-b is absent from the file but kept")
}

// Every bad row is reported with file and line, and every good row applied.
func TestImportPlantReportsEveryProblem(t *testing.T) {
	svc := newPlantService(t)
	dir := t.TempDir()
	write(t, dir, "templates/pump.yaml", "name: pump\nresources: []\n")
	write(t, dir, "assets.csv", "id,name,template_name\n"+
		"pump-a,A,pump\n"+
		",nameless,pump\n"+
		"pump-c,C,no-such-template\n"+
		"bad id!,D,pump\n")
	write(t, dir, "relations.csv", "source_asset_id,relation_type,target_asset_id\n"+
		"pump-a,sitsOn,pump-a\n")
	write(t, dir, "points.csv", "asset_id,protocol,poll_interval_ms,name,value_type,address\n"+
		"pump-a,modbus-tcp,1000,t,NUMBER,0\n"+
		"pump-a,opcua,,u,NUMBER,1\n"+
		"ghost,modbus-tcp,,t,NUMBER,0\n")

	rep, err := svc.ImportPlant(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Assets.Created)
	assert.Equal(t, 3, rep.Assets.Failed)
	assert.Equal(t, 1, rep.Relations.Failed)
	assert.Equal(t, 2, rep.Points.Failed)

	all := strings.Join(rep.Problems, "\n")
	for _, want := range []string{
		"assets.csv:3: id is required",
		"assets.csv:4: pump-c: template not found",
		"assets.csv:5: bad id!:",
		"relations.csv:2: pump-a sitsOn pump-a:",
		`points.csv:3: asset pump-a: protocol "opcua" disagrees with "modbus-tcp" on line 2`,
		"points.csv:4: asset ghost: asset not found",
	} {
		assert.Contains(t, all, want)
	}
}

func TestImportPlantRejectsAMissingColumn(t *testing.T) {
	svc := newPlantService(t)
	dir := t.TempDir()
	write(t, dir, "assets.csv", "identifier,name\nx,y\n")
	_, err := svc.ImportPlant(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `assets.csv: missing column "id"`)
}

func TestEncodingCellRoundTrip(t *testing.T) {
	for _, v := range []any{0.1, 3.0, true, false, "CDAB", map[string]any{"bit": 3.0}} {
		cell, err := encodingCell(v)
		require.NoError(t, err)
		assert.Equal(t, v, parseEncodingCell(cell), "%#v via %q", v, cell)
	}
}
