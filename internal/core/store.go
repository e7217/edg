package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store is a SQLite-based metadata store
type Store struct {
	db *sql.DB
}

// NewStore creates and initializes a new Store
func NewStore(dbPath string) (*Store, error) {
	// Create data directory if not exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open DB: %w", err)
	}

	// Enable foreign key constraints
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
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

	CREATE TABLE IF NOT EXISTS asset_relations (
		id TEXT PRIMARY KEY,
		source_asset_id TEXT NOT NULL,
		target_asset_id TEXT NOT NULL,
		relation_type TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		metadata TEXT,
		FOREIGN KEY (source_asset_id) REFERENCES assets(id) ON DELETE CASCADE,
		FOREIGN KEY (target_asset_id) REFERENCES assets(id) ON DELETE CASCADE,
		UNIQUE (source_asset_id, target_asset_id, relation_type)
	);
	CREATE INDEX IF NOT EXISTS idx_relations_source ON asset_relations(source_asset_id);
	CREATE INDEX IF NOT EXISTS idx_relations_target ON asset_relations(target_asset_id);
	CREATE INDEX IF NOT EXISTS idx_relations_type ON asset_relations(relation_type);
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
	labels, err := json.Marshal(asset.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal asset labels: %w", err)
	}

	_, err = s.db.Exec(
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

	if err := json.Unmarshal([]byte(labelsJSON), &asset.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset labels: %w", err)
	}
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

	if err := json.Unmarshal([]byte(labelsJSON), &asset.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset labels: %w", err)
	}
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
		if err := json.Unmarshal([]byte(labelsJSON), &asset.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal asset labels: %w", err)
		}
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

// ==================== AssetRelation Methods ====================

// CreateRelation creates a new asset relation
func (s *Store) CreateRelation(relation *AssetRelation) error {
	// Validate source and target assets exist
	sourceExists, err := s.AssetExists(relation.SourceAssetID)
	if err != nil {
		return fmt.Errorf("failed to check source asset: %w", err)
	}
	if !sourceExists {
		return fmt.Errorf("source asset not found: %s", relation.SourceAssetID)
	}

	targetExists, err := s.AssetExists(relation.TargetAssetID)
	if err != nil {
		return fmt.Errorf("failed to check target asset: %w", err)
	}
	if !targetExists {
		return fmt.Errorf("target asset not found: %s", relation.TargetAssetID)
	}

	// Marshal metadata
	var metadataJSON string
	if relation.Metadata != nil {
		metadata, err := json.Marshal(relation.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
		metadataJSON = string(metadata)
	}

	// Insert relation
	_, err = s.db.Exec(
		`INSERT INTO asset_relations (id, source_asset_id, target_asset_id, relation_type, created_at, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		relation.ID, relation.SourceAssetID, relation.TargetAssetID,
		relation.RelationType, relation.CreatedAt, metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("failed to create relation: %w", err)
	}
	return nil
}

// GetRelation retrieves a relation by ID
func (s *Store) GetRelation(id string) (*AssetRelation, error) {
	row := s.db.QueryRow(
		`SELECT id, source_asset_id, target_asset_id, relation_type, created_at, metadata
		 FROM asset_relations WHERE id = ?`,
		id,
	)

	var relation AssetRelation
	var metadataJSON sql.NullString
	err := row.Scan(
		&relation.ID, &relation.SourceAssetID, &relation.TargetAssetID,
		&relation.RelationType, &relation.CreatedAt, &metadataJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get relation: %w", err)
	}

	// Unmarshal metadata if present
	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &relation.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal relation metadata: %w", err)
		}
	}

	return &relation, nil
}

// GetRelationsBySourceAsset retrieves all relations from a source asset
func (s *Store) GetRelationsBySourceAsset(assetID string) ([]*AssetRelation, error) {
	rows, err := s.db.Query(
		`SELECT id, source_asset_id, target_asset_id, relation_type, created_at, metadata
		 FROM asset_relations WHERE source_asset_id = ? ORDER BY created_at DESC`,
		assetID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query relations: %w", err)
	}
	defer rows.Close()

	var relations []*AssetRelation
	for rows.Next() {
		var relation AssetRelation
		var metadataJSON sql.NullString
		if err := rows.Scan(
			&relation.ID, &relation.SourceAssetID, &relation.TargetAssetID,
			&relation.RelationType, &relation.CreatedAt, &metadataJSON,
		); err != nil {
			return nil, err
		}

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &relation.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal relation metadata: %w", err)
			}
		}

		relations = append(relations, &relation)
	}

	return relations, nil
}

// GetRelationsByTargetAsset retrieves all relations to a target asset
func (s *Store) GetRelationsByTargetAsset(assetID string) ([]*AssetRelation, error) {
	rows, err := s.db.Query(
		`SELECT id, source_asset_id, target_asset_id, relation_type, created_at, metadata
		 FROM asset_relations WHERE target_asset_id = ? ORDER BY created_at DESC`,
		assetID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query relations: %w", err)
	}
	defer rows.Close()

	var relations []*AssetRelation
	for rows.Next() {
		var relation AssetRelation
		var metadataJSON sql.NullString
		if err := rows.Scan(
			&relation.ID, &relation.SourceAssetID, &relation.TargetAssetID,
			&relation.RelationType, &relation.CreatedAt, &metadataJSON,
		); err != nil {
			return nil, err
		}

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &relation.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal relation metadata: %w", err)
			}
		}

		relations = append(relations, &relation)
	}

	return relations, nil
}

// DeleteRelation deletes a relation by ID
func (s *Store) DeleteRelation(id string) error {
	result, err := s.db.Exec(`DELETE FROM asset_relations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete relation: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("relation not found: %s", id)
	}
	return nil
}
