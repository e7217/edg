package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store is a SQLite-based metadata store
type Store struct {
	db *sql.DB
}

// NewStore creates and initializes a new Store
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open DB: %w", err)
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize DB: %w", err)
	}

	return store, nil
}

// init creates tables
func (s *Store) init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS assets (
		id TEXT PRIMARY KEY,
		name TEXT UNIQUE NOT NULL,
		template_name TEXT,
		labels TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_assets_name ON assets(name);
	CREATE INDEX IF NOT EXISTS idx_assets_template ON assets(template_name);
	`
	_, err := s.db.Exec(schema)
	return err
}

// Close closes the DB connection
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateAsset creates a new asset
func (s *Store) CreateAsset(asset *Asset) error {
	labels, _ := json.Marshal(asset.Labels)

	_, err := s.db.Exec(
		`INSERT INTO assets (id, name, template_name, labels, created_at) VALUES (?, ?, ?, ?, ?)`,
		asset.ID, asset.Name, asset.TemplateName, string(labels), asset.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create asset: %w", err)
	}
	return nil
}

// GetAsset retrieves an asset by ID
func (s *Store) GetAsset(id string) (*Asset, error) {
	row := s.db.QueryRow(
		`SELECT id, name, template_name, labels, created_at FROM assets WHERE id = ?`,
		id,
	)

	var asset Asset
	var labelsJSON string
	err := row.Scan(&asset.ID, &asset.Name, &asset.TemplateName, &labelsJSON, &asset.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get asset: %w", err)
	}

	json.Unmarshal([]byte(labelsJSON), &asset.Labels)
	return &asset, nil
}

// GetAssetByName retrieves an asset by name
func (s *Store) GetAssetByName(name string) (*Asset, error) {
	row := s.db.QueryRow(
		`SELECT id, name, template_name, labels, created_at FROM assets WHERE name = ?`,
		name,
	)

	var asset Asset
	var labelsJSON string
	err := row.Scan(&asset.ID, &asset.Name, &asset.TemplateName, &labelsJSON, &asset.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get asset: %w", err)
	}

	json.Unmarshal([]byte(labelsJSON), &asset.Labels)
	return &asset, nil
}

// ListAssets retrieves all assets
func (s *Store) ListAssets() ([]*Asset, error) {
	rows, err := s.db.Query(
		`SELECT id, name, template_name, labels, created_at FROM assets ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list assets: %w", err)
	}
	defer rows.Close()

	var assets []*Asset
	for rows.Next() {
		var asset Asset
		var labelsJSON string
		if err := rows.Scan(&asset.ID, &asset.Name, &asset.TemplateName, &labelsJSON, &asset.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(labelsJSON), &asset.Labels)
		assets = append(assets, &asset)
	}

	return assets, nil
}

// DeleteAsset deletes an asset by ID
func (s *Store) DeleteAsset(id string) error {
	result, err := s.db.Exec(`DELETE FROM assets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete asset: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("asset not found: %s", id)
	}
	return nil
}

// AssetExists checks if an asset exists
func (s *Store) AssetExists(id string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateAssetTemplate updates an asset's template
func (s *Store) UpdateAssetTemplate(id, templateName string) error {
	result, err := s.db.Exec(
		`UPDATE assets SET template_name = ? WHERE id = ?`,
		templateName, id,
	)
	if err != nil {
		return fmt.Errorf("failed to update asset: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("asset not found: %s", id)
	}
	return nil
}

// StoreStats contains store statistics
type StoreStats struct {
	TotalAssets int       `json:"total_assets"`
	LastUpdated time.Time `json:"last_updated"`
}

// GetStats returns store statistics
func (s *Store) GetStats() (*StoreStats, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&count)
	if err != nil {
		return nil, err
	}

	return &StoreStats{
		TotalAssets: count,
		LastUpdated: time.Now(),
	}, nil
}
