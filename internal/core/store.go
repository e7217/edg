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
	return NewStoreWithMigrations(dbPath, true)
}

// NewStoreWithMigrations creates a Store and optionally applies embedded migrations.
func NewStoreWithMigrations(dbPath string, autoMigrate bool) (*Store, error) {
	// Create data directory if not exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open DB: %w", err)
	}
	if dbPath == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	// Enable foreign key constraints
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	store := &Store{db: db}
	if autoMigrate {
		if err := runMigrations(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to initialize DB: %w", err)
		}
	}

	if !autoMigrate {
		if err := verifyStoreSchema(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to verify DB schema: %w", err)
		}
	}

	return store, nil
}

func verifyStoreSchema(db *sql.DB) error {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'assets'`,
	).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("assets table not found; run migrations or enable auto_migrate")
	}

	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'asset_relations'`,
	).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("asset_relations table not found; run migrations or enable auto_migrate")
	}

	return nil
}

// Close closes the DB connection
func (s *Store) Close() error {
	return s.db.Close()
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func normalizeAsset(asset *Asset, isCreate bool) {
	now := time.Now()
	if isCreate && asset.CreatedAt.IsZero() {
		asset.CreatedAt = now
	}
	if asset.UpdatedAt.IsZero() {
		if asset.CreatedAt.IsZero() {
			asset.UpdatedAt = now
		} else {
			asset.UpdatedAt = asset.CreatedAt
		}
	}
	if asset.Source == "" {
		asset.Source = SourceManual
	}
	if asset.Labels == nil {
		asset.Labels = []string{}
	}
	if asset.ExternalIDs == nil {
		asset.ExternalIDs = map[string]string{}
	}
	if asset.Attributes == nil {
		asset.Attributes = map[string]string{}
	}
}

func marshalStringSlice(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func marshalStringMap(values map[string]string) (string, error) {
	if values == nil {
		values = map[string]string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func scanAsset(row scanner) (*Asset, error) {
	var asset Asset
	var labelsJSON, externalIDsJSON, attributesJSON string
	var createdAt, updatedAt sql.NullTime

	err := row.Scan(
		&asset.ID,
		&asset.Name,
		&asset.TemplateName,
		&labelsJSON,
		&externalIDsJSON,
		&asset.Source,
		&attributesJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	if createdAt.Valid {
		asset.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		asset.UpdatedAt = updatedAt.Time
	}

	if err := json.Unmarshal([]byte(labelsJSON), &asset.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset labels: %w", err)
	}
	if err := json.Unmarshal([]byte(externalIDsJSON), &asset.ExternalIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset external IDs: %w", err)
	}
	if err := json.Unmarshal([]byte(attributesJSON), &asset.Attributes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset attributes: %w", err)
	}
	normalizeAsset(&asset, false)
	return &asset, nil
}

func scanAssets(rows *sql.Rows) ([]*Asset, error) {
	var assets []*Asset
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return assets, nil
}

// CreateAsset creates a new asset
func (s *Store) CreateAsset(asset *Asset) error {
	normalizeAsset(asset, true)

	labels, err := marshalStringSlice(asset.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal asset labels: %w", err)
	}
	externalIDs, err := marshalStringMap(asset.ExternalIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal asset external IDs: %w", err)
	}
	attributes, err := marshalStringMap(asset.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal asset attributes: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO assets (id, name, template_name, labels, external_ids, source, attributes, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, asset.Name, asset.TemplateName, labels, externalIDs, asset.Source, attributes,
		asset.CreatedAt, asset.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create asset: %w", err)
	}
	return nil
}

// GetAsset retrieves an asset by ID
func (s *Store) GetAsset(id string) (*Asset, error) {
	row := s.db.QueryRow(
		`SELECT id, name, template_name, labels, external_ids, source, attributes, created_at, updated_at
		 FROM assets WHERE id = ?`,
		id,
	)

	asset, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get asset: %w", err)
	}
	return asset, nil
}

// GetAssetByName retrieves an asset by name
func (s *Store) GetAssetByName(name string) (*Asset, error) {
	row := s.db.QueryRow(
		`SELECT id, name, template_name, labels, external_ids, source, attributes, created_at, updated_at
		 FROM assets WHERE name = ?`,
		name,
	)

	asset, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get asset: %w", err)
	}
	return asset, nil
}

// ListAssets retrieves all assets
func (s *Store) ListAssets() ([]*Asset, error) {
	rows, err := s.db.Query(
		`SELECT id, name, template_name, labels, external_ids, source, attributes, created_at, updated_at
		 FROM assets ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list assets: %w", err)
	}
	defer rows.Close()

	return scanAssets(rows)
}

// ListAssetsBySource retrieves assets created by a source.
func (s *Store) ListAssetsBySource(source string) ([]*Asset, error) {
	rows, err := s.db.Query(
		`SELECT id, name, template_name, labels, external_ids, source, attributes, created_at, updated_at
		 FROM assets WHERE source = ? ORDER BY created_at DESC`,
		source,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list assets by source: %w", err)
	}
	defer rows.Close()

	return scanAssets(rows)
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
		`UPDATE assets SET template_name = ?, updated_at = ? WHERE id = ?`,
		templateName, time.Now(), id,
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

// UpdateAsset replaces an asset's mutable metadata fields.
func (s *Store) UpdateAsset(asset *Asset) error {
	normalizeAsset(asset, false)

	labels, err := marshalStringSlice(asset.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal asset labels: %w", err)
	}
	externalIDs, err := marshalStringMap(asset.ExternalIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal asset external IDs: %w", err)
	}
	attributes, err := marshalStringMap(asset.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal asset attributes: %w", err)
	}

	asset.UpdatedAt = time.Now()
	result, err := s.db.Exec(
		`UPDATE assets
		 SET name = ?, template_name = ?, labels = ?, external_ids = ?, source = ?, attributes = ?, updated_at = ?
		 WHERE id = ?`,
		asset.Name, asset.TemplateName, labels, externalIDs, asset.Source, attributes, asset.UpdatedAt, asset.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update asset: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("asset not found: %s", asset.ID)
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

// ListRelations retrieves all asset relations.
func (s *Store) ListRelations() ([]*AssetRelation, error) {
	rows, err := s.db.Query(
		`SELECT id, source_asset_id, target_asset_id, relation_type, created_at, metadata
		 FROM asset_relations ORDER BY created_at DESC`,
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate relations: %w", err)
	}
	return relations, nil
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
