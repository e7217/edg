package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Point-list import and export as YAML files, one per asset.
//
// This path exists because HTTP writes are refused outright when no bearer token
// is configured, which is the default. Without a file path a deployment that
// never sets http.token_env could not be provisioned at all, and the operator's
// artifact is a spreadsheet or an existing mapping.yaml either way.

// pointFileSuffix is the extension ImportPointLists reads and ExportPointLists
// writes.
const pointFileSuffix = ".yaml"

// ImportPointLists reads <asset-id>.yaml from dir and upserts each through the
// service, so file import enforces exactly the same rules as the API.
//
// It reports every file that failed rather than stopping at the first, because
// an operator importing a plant wants the whole list of problems in one pass,
// not one per run.
func (s *MetadataService) ImportPointLists(dir string) (imported int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("failed to read point directory: %w", err)
	}

	var problems []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), pointFileSuffix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		req, err := readPointFile(path, strings.TrimSuffix(e.Name(), pointFileSuffix))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if _, err := s.UpsertPointList(*req); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		imported++
	}

	if len(problems) > 0 {
		return imported, fmt.Errorf("%d of %d point files rejected:\n  %s",
			len(problems), len(problems)+imported, strings.Join(problems, "\n  "))
	}
	return imported, nil
}

// readPointFile parses one file. The filename is the asset id unless the file
// names one itself, and a disagreement is an error rather than a preference --
// silently choosing one is how a whole directory ends up on a single asset.
func readPointFile(path, fileAssetID string) (*UpsertPointListRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pl PointList
	if err := yaml.Unmarshal(data, &pl); err != nil {
		return nil, err
	}
	if pl.AssetID != "" && pl.AssetID != fileAssetID {
		return nil, fmt.Errorf("asset_id %q does not match the filename %q", pl.AssetID, fileAssetID)
	}
	return &UpsertPointListRequest{
		AssetID:        fileAssetID,
		Protocol:       pl.Protocol,
		PollIntervalMS: pl.PollIntervalMS,
		Points:         pl.Points,
	}, nil
}

// ExportPointLists writes one <asset-id>.yaml per declared list into dir.
//
// version, created_at and updated_at are deliberately not written: they are
// assigned by the store, so a round trip must not carry them back in and claim a
// version the database did not issue.
func (s *MetadataService) ExportPointLists(dir string) (exported int, err error) {
	lists, err := s.ListPointLists()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("failed to create point directory: %w", err)
	}

	for _, pl := range lists {
		// The asset id becomes a filename, so anything that could escape the
		// directory or collide is refused rather than sanitised -- a silently
		// renamed file would not import back to the same asset.
		if err := checkPointFilename(pl.AssetID); err != nil {
			return exported, err
		}
		sort.Slice(pl.Points, func(i, j int) bool { return pl.Points[i].Name < pl.Points[j].Name })
		data, err := yaml.Marshal(pl)
		if err != nil {
			return exported, err
		}
		path := filepath.Join(dir, pl.AssetID+pointFileSuffix)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return exported, err
		}
		exported++
	}
	return exported, nil
}

// checkPointFilename refuses an asset id that cannot safely become a filename.
func checkPointFilename(assetID string) error {
	if assetID == "" {
		return fmt.Errorf("cannot export a point list with an empty asset_id")
	}
	if assetID != filepath.Base(assetID) || strings.ContainsAny(assetID, `/\`) {
		return fmt.Errorf("asset_id %q cannot be written as a filename; export it over the API instead", assetID)
	}
	if assetID == "." || assetID == ".." {
		return fmt.Errorf("asset_id %q cannot be written as a filename", assetID)
	}
	return nil
}
