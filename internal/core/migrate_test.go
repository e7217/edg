package core

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunMigrationsCreatesSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metadata.db")

	require.NoError(t, RunMigrations(dbPath))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	assert.True(t, tableExists(t, db, "assets"))
	assert.True(t, tableExists(t, db, "asset_relations"))
	assert.True(t, tableExists(t, db, "templates"))
	assert.True(t, tableExists(t, db, "template_resources"))
	assert.True(t, tableExists(t, db, "template_constraints"))
	assert.True(t, columnExists(t, db, "assets", "external_ids"))
	assert.True(t, columnExists(t, db, "assets", "source"))
	assert.True(t, columnExists(t, db, "assets", "attributes"))
	assert.True(t, columnExists(t, db, "assets", "updated_at"))
	assert.True(t, indexExists(t, db, "idx_assets_source"))
	assert.True(t, indexExists(t, db, "idx_relations_source_type"))
	assert.True(t, indexExists(t, db, "idx_relations_target_type"))
}

func TestRunMigrationsPreservesV1Data(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationSteps(db, 1))

	createdAt := time.Now().Add(-time.Hour).UTC()
	_, err = db.Exec(
		`INSERT INTO assets (id, name, template_name, labels, created_at) VALUES (?, ?, ?, ?, ?)`,
		"asset-v1", "legacy-asset", "legacy-template", `["legacy"]`, createdAt,
	)
	require.NoError(t, err)

	require.NoError(t, runMigrationSteps(db, 1))

	store := &Store{db: db}
	asset, err := store.GetAsset("asset-v1")
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, "legacy-asset", asset.Name)
	assert.Equal(t, []string{"legacy"}, asset.Labels)
	assert.Equal(t, map[string]string{}, asset.ExternalIDs)
	assert.Equal(t, SourceManual, asset.Source)
	assert.Equal(t, map[string]string{}, asset.Attributes)
	assert.False(t, asset.UpdatedAt.IsZero())
}

func TestMigrationDownSteps(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))
	require.True(t, tableExists(t, db, "templates"))
	require.True(t, columnExists(t, db, "assets", "source"))
	require.True(t, indexExists(t, db, "idx_relations_source_type"))

	// Undo 0004 (templates).
	require.NoError(t, runMigrationSteps(db, -1))
	assert.False(t, tableExists(t, db, "templates"))
	assert.False(t, tableExists(t, db, "template_resources"))
	assert.True(t, indexExists(t, db, "idx_relations_source_type"))
	assert.True(t, tableExists(t, db, "assets"))

	// Undo 0003 (relation indexes).
	require.NoError(t, runMigrationSteps(db, -1))
	assert.False(t, indexExists(t, db, "idx_relations_source_type"))
	assert.False(t, indexExists(t, db, "idx_relations_target_type"))
	assert.True(t, columnExists(t, db, "assets", "source"))
	assert.True(t, tableExists(t, db, "assets"))

	// Undo 0002 (asset extensions).
	require.NoError(t, runMigrationSteps(db, -1))
	assert.False(t, columnExists(t, db, "assets", "source"))
	assert.True(t, tableExists(t, db, "assets"))

	// Undo 0001 (init).
	require.NoError(t, runMigrationSteps(db, -1))
	assert.False(t, tableExists(t, db, "assets"))
	assert.False(t, tableExists(t, db, "asset_relations"))
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		name,
	).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue interface{}
		var pk int
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk))
		if name == column {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
		name,
	).Scan(&count)
	require.NoError(t, err)
	return count > 0
}
