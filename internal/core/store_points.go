package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// GetPointList returns one asset's declarations, or nil when the asset has
// none. A missing list is not an error: most assets in a plant model are
// logical groupings that nothing polls.
func (s *Store) GetPointList(assetID string) (*PointList, error) {
	pl := &PointList{AssetID: assetID}
	var pollInterval sql.NullInt64
	err := s.db.QueryRow(
		`SELECT protocol, poll_interval_ms, version, created_at, updated_at
		   FROM asset_point_lists WHERE asset_id = ?`,
		assetID,
	).Scan(&pl.Protocol, &pollInterval, &pl.Version, &pl.CreatedAt, &pl.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pollInterval.Valid {
		pl.PollIntervalMS = int(pollInterval.Int64)
	}

	points, err := s.pointsFor(assetID)
	if err != nil {
		return nil, err
	}
	pl.Points = points
	return pl, nil
}

func (s *Store) pointsFor(assetID string) ([]Point, error) {
	rows, err := s.db.Query(
		`SELECT name, value_type, unit, address, encoding, enabled, created_at, updated_at
		   FROM asset_points WHERE asset_id = ? ORDER BY name`,
		assetID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]Point, 0)
	for rows.Next() {
		var p Point
		var encoding string
		var enabled int
		if err := rows.Scan(&p.Name, &p.ValueType, &p.Unit, &p.Address,
			&encoding, &enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		if encoding != "" && encoding != "{}" {
			if err := json.Unmarshal([]byte(encoding), &p.Encoding); err != nil {
				return nil, fmt.Errorf("point %s/%s has unreadable encoding: %w", assetID, p.Name, err)
			}
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// ListPointLists returns every declared list, ordered by asset id.
func (s *Store) ListPointLists() ([]*PointList, error) {
	rows, err := s.db.Query(
		`SELECT asset_id, protocol, poll_interval_ms, version, created_at, updated_at
		   FROM asset_point_lists ORDER BY asset_id`,
	)
	if err != nil {
		return nil, err
	}

	lists := make([]*PointList, 0)
	for rows.Next() {
		pl := &PointList{}
		var pollInterval sql.NullInt64
		if err := rows.Scan(&pl.AssetID, &pl.Protocol, &pollInterval,
			&pl.Version, &pl.CreatedAt, &pl.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if pollInterval.Valid {
			pl.PollIntervalMS = int(pollInterval.Int64)
		}
		lists = append(lists, pl)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Points are fetched per list rather than in one join so that a list with no
	// points still appears -- an operator who declared a protocol and no points
	// yet needs to see that, not have the row vanish.
	for _, pl := range lists {
		points, err := s.pointsFor(pl.AssetID)
		if err != nil {
			return nil, err
		}
		pl.Points = points
	}
	return lists, nil
}

// CountPoints returns how many points are declared across the plant, for the
// metrics collector and the operator UI's summary.
func (s *Store) CountPoints() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM asset_points`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// UpsertPointList replaces an asset's declarations atomically and returns the
// new version.
//
// Points are upserted individually on (asset_id, name) and then the absent ones
// deleted, rather than the wholesale DELETE-then-INSERT that UpsertTemplate
// uses. That preserves created_at per point, so an operator can see which
// declarations are new after a bulk re-import -- the information
// UpsertTemplate throws away.
func (s *Store) UpsertPointList(pl *PointList) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var pollInterval any
	if pl.PollIntervalMS > 0 {
		pollInterval = pl.PollIntervalMS
	}

	// version starts at 1 and increases by 1 on every write, including writes
	// that change nothing. A no-op write bumping the version is the safe
	// direction: an adapter re-fetching an identical list costs one request,
	// whereas a missed bump leaves it polling a stale list indefinitely.
	if _, err := tx.Exec(
		`INSERT INTO asset_point_lists (asset_id, protocol, poll_interval_ms, version, updated_at)
		 VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT(asset_id) DO UPDATE SET
		   protocol = excluded.protocol,
		   poll_interval_ms = excluded.poll_interval_ms,
		   version = asset_point_lists.version + 1,
		   updated_at = CURRENT_TIMESTAMP`,
		pl.AssetID, pl.Protocol, pollInterval,
	); err != nil {
		return 0, err
	}

	names := make([]any, 0, len(pl.Points))
	for i := range pl.Points {
		p := &pl.Points[i]
		encoding, err := p.encodingJSON()
		if err != nil {
			return 0, err
		}
		enabled := 0
		if p.Enabled {
			enabled = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO asset_points
			   (asset_id, name, value_type, unit, address, encoding, enabled, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(asset_id, name) DO UPDATE SET
			   value_type = excluded.value_type,
			   unit = excluded.unit,
			   address = excluded.address,
			   encoding = excluded.encoding,
			   enabled = excluded.enabled,
			   updated_at = CURRENT_TIMESTAMP`,
			pl.AssetID, p.Name, p.ValueType, p.Unit, p.Address, encoding, enabled,
		); err != nil {
			return 0, err
		}
		names = append(names, p.Name)
	}

	if err := deleteAbsentPoints(tx, pl.AssetID, names); err != nil {
		return 0, err
	}

	var version int
	if err := tx.QueryRow(
		`SELECT version FROM asset_point_lists WHERE asset_id = ?`, pl.AssetID,
	).Scan(&version); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

// deleteAbsentPoints removes declarations the new list no longer contains.
func deleteAbsentPoints(tx *sql.Tx, assetID string, names []any) error {
	if len(names) == 0 {
		_, err := tx.Exec(`DELETE FROM asset_points WHERE asset_id = ?`, assetID)
		return err
	}
	placeholders := ""
	for i := range names {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
	}
	args := append([]any{assetID}, names...)
	_, err := tx.Exec(
		`DELETE FROM asset_points WHERE asset_id = ? AND name NOT IN (`+placeholders+`)`,
		args...,
	)
	return err
}

// DeletePointList removes an asset's declarations. Points cascade.
func (s *Store) DeletePointList(assetID string) error {
	_, err := s.db.Exec(`DELETE FROM asset_point_lists WHERE asset_id = ?`, assetID)
	return err
}

// SearchPoints finds declarations by point name across every asset. It is the
// query behind "which boxes call this tag something else".
func (s *Store) SearchPoints(name string) ([]PointSearchResult, error) {
	rows, err := s.db.Query(
		`SELECT p.asset_id, l.protocol, p.name, p.value_type, p.unit, p.address, p.enabled
		   FROM asset_points p
		   JOIN asset_point_lists l ON l.asset_id = p.asset_id
		  WHERE p.name LIKE ?
		  ORDER BY p.asset_id, p.name`,
		"%"+name+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PointSearchResult, 0)
	for rows.Next() {
		var r PointSearchResult
		var enabled int
		if err := rows.Scan(&r.AssetID, &r.Protocol, &r.Name, &r.ValueType,
			&r.Unit, &r.Address, &enabled); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// PointSearchResult is one hit from SearchPoints, flattened for display.
type PointSearchResult struct {
	AssetID   string `json:"asset_id"`
	Protocol  string `json:"protocol"`
	Name      string `json:"name"`
	ValueType string `json:"value_type"`
	Unit      string `json:"unit,omitempty"`
	Address   string `json:"address"`
	Enabled   bool   `json:"enabled"`
}
