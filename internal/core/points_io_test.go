package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestImportExportPointListsRoundTrip(t *testing.T) {
	svc, _ := newPointsTestService(t)
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "pump-a.yaml"), []byte(`
protocol: modbus-tcp
poll_interval_ms: 1000
points:
  - name: temperature
    value_type: NUMBER
    unit: "°C"
    address: "0"
    encoding:
      function: holding
      type: int16
      scale: 0.1
    enabled: true
  - name: pressure
    value_type: NUMBER
    unit: bar
    address: "1"
    enabled: true
`), 0o644))

	n, err := svc.ImportPointLists(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	pl, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Equal(t, "modbus-tcp", pl.Protocol)
	assert.Equal(t, 1000, pl.PollIntervalMS)
	require.Len(t, pl.Points, 2)
	// A YAML scale must land as a number, not the string "0.1".
	byName := map[string]Point{}
	for _, p := range pl.Points {
		byName[p.Name] = p
	}
	assert.Equal(t, 0.1, byName["temperature"].Encoding["scale"])

	// Export, then re-import into a fresh directory and compare.
	out := filepath.Join(t.TempDir(), "exported")
	exported, err := svc.ExportPointLists(out)
	require.NoError(t, err)
	assert.Equal(t, 1, exported)

	data, err := os.ReadFile(filepath.Join(out, "pump-a.yaml"))
	require.NoError(t, err)
	var written PointList
	require.NoError(t, yaml.Unmarshal(data, &written))
	assert.Equal(t, "modbus-tcp", written.Protocol)
	require.Len(t, written.Points, 2)
	assert.Equal(t, "pressure", written.Points[0].Name, "points are exported name-ordered")

	// Store-assigned fields must not be written, or a round trip would claim a
	// version the database never issued.
	assert.NotContains(t, string(data), "version:")
	assert.NotContains(t, string(data), "created_at")
	assert.NotContains(t, string(data), "updated_at")

	n, err = svc.ImportPointLists(out)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	after, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Equal(t, pl.Points, after.Points, "a round trip changes nothing but the version")
	assert.Equal(t, pl.Version+1, after.Version)
}

// Import must report every bad file, not stop at the first: an operator
// importing a plant wants all the problems in one pass.
func TestImportPointListsReportsEveryProblem(t *testing.T) {
	svc, _ := newPointsTestService(t)
	dir := t.TempDir()

	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("pump-a.yaml", "protocol: modbus-tcp\npoints:\n  - name: t\n    value_type: NUMBER\n    address: \"0\"\n")
	write("pump-b.yaml", "protocol: modbus-tcp\npoints:\n  - name: t\n    value_type: INT16\n    address: \"0\"\n")
	write("no-such-asset.yaml", "protocol: modbus-tcp\npoints: []\n")
	write("broken.yaml", "protocol: [this is not a string\n")
	write("notes.txt", "ignored")

	n, err := svc.ImportPointLists(dir)
	require.Error(t, err)
	assert.Equal(t, 1, n, "the one good file still imported")
	msg := err.Error()
	assert.Contains(t, msg, "3 of 4 point files rejected")
	assert.Contains(t, msg, "pump-b.yaml")
	assert.Contains(t, msg, "invalid value_type")
	assert.Contains(t, msg, "no-such-asset.yaml")
	assert.Contains(t, msg, "broken.yaml")
	assert.NotContains(t, msg, "notes.txt", "non-yaml files are skipped, not rejected")

	pl, err := svc.GetPointList("pump-a")
	require.NoError(t, err)
	assert.Len(t, pl.Points, 1)
}

// The filename owns identity. A file that names a different asset is a mistake,
// and silently preferring either one is how a directory lands on one asset.
func TestImportPointListsRejectsAssetIDMismatch(t *testing.T) {
	svc, _ := newPointsTestService(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pump-a.yaml"),
		[]byte("asset_id: pump-b\nprotocol: modbus-tcp\npoints: []\n"), 0o644))

	_, err := svc.ImportPointLists(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match the filename")

	pl, err := svc.GetPointList("pump-b")
	require.NoError(t, err)
	assert.Empty(t, pl.Points, "nothing was written to the asset the file named")
}

// An asset id becomes a filename, so one that could escape the directory is
// refused rather than sanitised: a renamed file would not import back.
func TestExportPointListsRefusesUnsafeAssetID(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.CreateAsset(&Asset{ID: "../escape", Name: "E", TemplateName: "t"}))
	_, err = store.UpsertPointList(&PointList{AssetID: "../escape", Protocol: "modbus-tcp"})
	require.NoError(t, err)

	svc := NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)
	dir := t.TempDir()
	_, err = svc.ExportPointLists(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be written as a filename")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing was written outside the directory")
}

func TestImportPointListsMissingDirectory(t *testing.T) {
	svc, _ := newPointsTestService(t)
	_, err := svc.ImportPointLists(filepath.Join(t.TempDir(), "nope"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read point directory")
}
