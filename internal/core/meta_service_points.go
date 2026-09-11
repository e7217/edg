package core

// Point-list operations on MetadataService.
//
// Every mutation goes through here rather than through Store directly, so the
// HTTP API, the CLI importer and any future NATS subject enforce the same rules.
// A direct Store write would bypass validation and, once distribution lands, the
// version bump an adapter relies on to notice a change.

// GetPointList returns one asset's declarations. A declared asset with no point
// list yields an empty list rather than a not-found, because "this asset has
// nothing to poll" is a normal state, not a missing record.
func (s *MetadataService) GetPointList(assetID string) (*PointList, error) {
	if assetID == "" {
		return nil, newServiceError(ErrValidation, "asset_id is required")
	}
	exists, err := s.store.AssetExists(assetID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, newServiceError(ErrNotFound, "asset not found")
	}
	pl, err := s.store.GetPointList(assetID)
	if err != nil {
		return nil, err
	}
	if pl == nil {
		return &PointList{AssetID: assetID, Points: []Point{}}, nil
	}
	return pl, nil
}

// ListPointLists returns every declared list.
func (s *MetadataService) ListPointLists() ([]*PointList, error) {
	return s.store.ListPointLists()
}

// SearchPoints finds declarations by point name across the plant.
func (s *MetadataService) SearchPoints(name string) ([]PointSearchResult, error) {
	return s.store.SearchPoints(name)
}

// UpsertPointList validates and replaces an asset's declarations, returning the
// stored list with its new version.
func (s *MetadataService) UpsertPointList(req UpsertPointListRequest) (*PointList, error) {
	if req.AssetID == "" {
		return nil, newServiceError(ErrValidation, "asset_id is required")
	}
	if req.Protocol == "" {
		return nil, newServiceError(ErrValidation, "protocol is required")
	}
	if req.PollIntervalMS < 0 {
		return nil, newServiceError(ErrValidation, "poll_interval_ms must not be negative")
	}
	if len(req.Points) > maxPointsPerAsset {
		return nil, newServiceError(ErrValidation,
			"%d points exceeds the %d per-asset limit", len(req.Points), maxPointsPerAsset)
	}

	// The asset must exist. assets has no foreign keys and assets.template_name
	// is unvalidated TEXT, so the FK on asset_point_lists is the only structural
	// guard -- and relying on a constraint violation would surface as an opaque
	// SQLite error instead of a 404.
	exists, err := s.store.AssetExists(req.AssetID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, newServiceError(ErrNotFound, "asset not found")
	}

	seen := make(map[string]bool, len(req.Points))
	points := make([]Point, 0, len(req.Points))
	for i := range req.Points {
		p := req.Points[i]
		if err := p.Validate(); err != nil {
			return nil, newServiceError(ErrValidation, "%s", err.Error())
		}
		if seen[p.Name] {
			return nil, newServiceError(ErrValidation, "point %q is declared more than once", p.Name)
		}
		seen[p.Name] = true
		// Encoding must round-trip before anything is written, or a bad bulk
		// import fails half way through with some points already stored.
		if _, err := p.encodingJSON(); err != nil {
			return nil, newServiceError(ErrValidation, "%s", err.Error())
		}
		points = append(points, p)
	}

	pl := &PointList{
		AssetID:        req.AssetID,
		Protocol:       req.Protocol,
		PollIntervalMS: req.PollIntervalMS,
		Points:         points,
	}
	if _, err := s.store.UpsertPointList(pl); err != nil {
		return nil, err
	}
	// Read back rather than returning what was written: version, created_at and
	// updated_at are assigned by the store, and the caller needs the version.
	return s.store.GetPointList(req.AssetID)
}

// DeletePointList removes an asset's declarations.
func (s *MetadataService) DeletePointList(req DeletePointListRequest) error {
	if req.AssetID == "" {
		return newServiceError(ErrValidation, "asset_id is required")
	}
	pl, err := s.store.GetPointList(req.AssetID)
	if err != nil {
		return err
	}
	if pl == nil {
		return newServiceError(ErrNotFound, "point list not found")
	}
	return s.store.DeletePointList(req.AssetID)
}
