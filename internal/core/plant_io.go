package core

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// A plant bundle is a whole plant's master data as a directory an operator can
// open in a spreadsheet, diff, and hand to the next site:
//
//	templates/<name>.yaml   asset kinds
//	assets.csv              one row per asset
//	relations.csv           one row per relation
//	points.csv              one row per point, grouped by asset
//
// CSV because the artifact an SI actually works in is a spreadsheet. The files
// are written UTF-8 with a byte-order mark, which is what makes Excel read
// Korean names correctly; the mark is stripped on read.
//
// Import is an upsert in dependency order -- templates, assets, relations,
// points -- and never deletes: an asset absent from assets.csv is left alone.
// It is idempotent: importing a bundle twice changes nothing the second time,
// and an unchanged point list is not rewritten, so adapters provisioned from it
// (ADR 0011) are not rebuilt for nothing.

const (
	plantTemplatesDir = "templates"
	plantAssetsFile   = "assets.csv"
	plantRelsFile     = "relations.csv"
	plantPointsFile   = "points.csv"

	csvListSep = ";"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// PlantCounts is what an import did to one kind of record.
type PlantCounts struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Failed    int `json:"failed"`
}

// PlantImportReport summarises an import. Problems name file and line.
type PlantImportReport struct {
	Templates int `json:"templates"`
	// TemplatesChanged is whether any template's content changed, which the
	// net-new count above cannot tell for an edited template.
	TemplatesChanged bool        `json:"templates_changed"`
	Assets           PlantCounts `json:"assets"`
	Relations        PlantCounts `json:"relations"`
	Points           PlantCounts `json:"point_lists"`
	Problems         []string    `json:"problems,omitempty"`
}

// String renders the report for a terminal.
func (r PlantImportReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "templates:   %d loaded\n", r.Templates)
	for _, row := range []struct {
		name string
		c    PlantCounts
	}{{"assets", r.Assets}, {"relations", r.Relations}, {"point lists", r.Points}} {
		fmt.Fprintf(&b, "%-12s %d created, %d updated, %d unchanged, %d failed\n",
			row.name+":", row.c.Created, row.c.Updated, row.c.Unchanged, row.c.Failed)
	}
	if len(r.Problems) > 0 {
		fmt.Fprintf(&b, "%d problem(s):\n", len(r.Problems))
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	return b.String()
}

// ExportPlant writes the whole of master data to dir.
func (s *MetadataService) ExportPlant(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := s.loader.ExportToDir(filepath.Join(dir, plantTemplatesDir)); err != nil {
		return err
	}

	assets, err := s.store.ListAssets()
	if err != nil {
		return err
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].ID < assets[j].ID })
	attrKeys, extKeys := map[string]bool{}, map[string]bool{}
	for _, a := range assets {
		for k := range a.Attributes {
			attrKeys[k] = true
		}
		for k := range a.ExternalIDs {
			extKeys[k] = true
		}
	}
	header := []string{"id", "name", "template_name", "labels", "source"}
	header = append(header, prefixed("attr.", attrKeys)...)
	header = append(header, prefixed("ext.", extKeys)...)
	rows := [][]string{header}
	for _, a := range assets {
		row := []string{a.ID, a.Name, a.TemplateName, strings.Join(a.Labels, csvListSep), a.Source}
		for _, k := range sortedSet(attrKeys) {
			row = append(row, a.Attributes[k])
		}
		for _, k := range sortedSet(extKeys) {
			row = append(row, a.ExternalIDs[k])
		}
		rows = append(rows, row)
	}
	if err := writeCSV(filepath.Join(dir, plantAssetsFile), rows); err != nil {
		return err
	}

	rels, err := s.store.ListRelations()
	if err != nil {
		return err
	}
	sort.Slice(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a.SourceAssetID != b.SourceAssetID {
			return a.SourceAssetID < b.SourceAssetID
		}
		if a.RelationType != b.RelationType {
			return a.RelationType < b.RelationType
		}
		return a.TargetAssetID < b.TargetAssetID
	})
	rows = [][]string{{"source_asset_id", "relation_type", "target_asset_id"}}
	for _, r := range rels {
		rows = append(rows, []string{r.SourceAssetID, string(r.RelationType), r.TargetAssetID})
	}
	if err := writeCSV(filepath.Join(dir, plantRelsFile), rows); err != nil {
		return err
	}

	lists, err := s.store.ListPointLists()
	if err != nil {
		return err
	}
	sort.Slice(lists, func(i, j int) bool { return lists[i].AssetID < lists[j].AssetID })
	encKeys := map[string]bool{}
	for _, pl := range lists {
		for _, p := range pl.Points {
			for k := range p.Encoding {
				encKeys[k] = true
			}
		}
	}
	header = []string{"asset_id", "protocol", "poll_interval_ms", "name", "value_type", "unit", "address", "enabled"}
	header = append(header, prefixed("encoding.", encKeys)...)
	rows = [][]string{header}
	for _, pl := range lists {
		for _, p := range pl.Points {
			poll := ""
			if pl.PollIntervalMS > 0 {
				poll = strconv.Itoa(pl.PollIntervalMS)
			}
			row := []string{pl.AssetID, pl.Protocol, poll, p.Name, p.ValueType, p.Unit, p.Address,
				strconv.FormatBool(p.Enabled)}
			for _, k := range sortedSet(encKeys) {
				cell, err := encodingCell(p.Encoding[k])
				if err != nil {
					return fmt.Errorf("asset %s point %s encoding.%s: %w", pl.AssetID, p.Name, k, err)
				}
				row = append(row, cell)
			}
			rows = append(rows, row)
		}
	}
	return writeCSV(filepath.Join(dir, plantPointsFile), rows)
}

// ImportPlant applies a bundle. It returns an error only when the bundle
// cannot be read at all; per-row failures are in the report, and every row
// that can be applied is.
func (s *MetadataService) ImportPlant(dir string) (PlantImportReport, error) {
	var rep PlantImportReport

	tdir := filepath.Join(dir, plantTemplatesDir)
	if st, err := os.Stat(tdir); err == nil && st.IsDir() {
		before := s.loader.Count()
		fingerprint := s.loader.Fingerprint()
		if err := s.loader.LoadFromDir(tdir); err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: %v", plantTemplatesDir, err))
		}
		rep.TemplatesChanged = s.loader.Fingerprint() != fingerprint
		rep.Templates = s.loader.Count() - before
		if rep.Templates < 0 {
			rep.Templates = 0
		}
	}

	if err := s.importAssets(filepath.Join(dir, plantAssetsFile), &rep); err != nil {
		return rep, err
	}
	if err := s.importRelations(filepath.Join(dir, plantRelsFile), &rep); err != nil {
		return rep, err
	}
	if err := s.importPoints(filepath.Join(dir, plantPointsFile), &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func (s *MetadataService) importAssets(path string, rep *PlantImportReport) error {
	tbl, err := readCSV(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := tbl.require("id", "name"); err != nil {
		return err
	}
	for _, r := range tbl.rows {
		fail := func(format string, args ...any) {
			rep.Assets.Failed++
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s:%d: ", plantAssetsFile, r.line)+fmt.Sprintf(format, args...))
		}
		id := r.get("id")
		if id == "" {
			fail("id is required")
			continue
		}
		want := &Asset{
			ID:           id,
			Name:         r.get("name"),
			TemplateName: r.get("template_name"),
			Labels:       splitList(r.get("labels")),
			Source:       r.get("source"),
			Attributes:   r.prefixed("attr."),
			ExternalIDs:  r.prefixed("ext."),
		}
		if want.Source == "" {
			want.Source = SourceManual
		}
		existing, err := s.store.GetAsset(id)
		if err != nil {
			return err
		}
		if existing == nil {
			if _, err := s.CreateAsset(CreateAssetRequest{
				ID: id, Name: want.Name, TemplateName: want.TemplateName, Labels: want.Labels,
				ExternalIDs: want.ExternalIDs, Source: want.Source, Attributes: want.Attributes,
			}); err != nil {
				fail("%s: %v", id, err)
				continue
			}
			rep.Assets.Created++
			continue
		}
		if sameAsset(existing, want) {
			rep.Assets.Unchanged++
			continue
		}
		if _, err := s.UpdateAsset(UpdateAssetRequest{
			ID: id, Name: want.Name, TemplateName: want.TemplateName, Labels: want.Labels,
			ExternalIDs: want.ExternalIDs, Source: want.Source, Attributes: want.Attributes,
		}); err != nil {
			fail("%s: %v", id, err)
			continue
		}
		rep.Assets.Updated++
	}
	return nil
}

func (s *MetadataService) importRelations(path string, rep *PlantImportReport) error {
	tbl, err := readCSV(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := tbl.require("source_asset_id", "relation_type", "target_asset_id"); err != nil {
		return err
	}
	existing, err := s.store.ListRelations()
	if err != nil {
		return err
	}
	have := map[[3]string]bool{}
	for _, r := range existing {
		have[[3]string{r.SourceAssetID, string(r.RelationType), r.TargetAssetID}] = true
	}
	for _, r := range tbl.rows {
		key := [3]string{r.get("source_asset_id"), r.get("relation_type"), r.get("target_asset_id")}
		if have[key] {
			rep.Relations.Unchanged++
			continue
		}
		if _, err := s.CreateRelation(CreateRelationRequest{
			SourceAssetID: key[0], RelationType: RelationType(key[1]), TargetAssetID: key[2],
		}); err != nil {
			rep.Relations.Failed++
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s:%d: %s %s %s: %v",
				plantRelsFile, r.line, key[0], key[1], key[2], err))
			continue
		}
		have[key] = true
		rep.Relations.Created++
	}
	return nil
}

func (s *MetadataService) importPoints(path string, rep *PlantImportReport) error {
	tbl, err := readCSV(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := tbl.require("asset_id", "name", "value_type", "address"); err != nil {
		return err
	}

	// Group rows by asset, in file order, so a list is one upsert.
	type group struct {
		req      UpsertPointListRequest
		firstRow int
		bad      bool
	}
	var order []string
	groups := map[string]*group{}
	for _, r := range tbl.rows {
		assetID := r.get("asset_id")
		fail := func(format string, args ...any) {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s:%d: ", plantPointsFile, r.line)+fmt.Sprintf(format, args...))
		}
		if assetID == "" {
			fail("asset_id is required")
			continue
		}
		g, ok := groups[assetID]
		if !ok {
			g = &group{req: UpsertPointListRequest{AssetID: assetID}, firstRow: r.line}
			groups[assetID] = g
			order = append(order, assetID)
		}
		// protocol and poll_interval_ms belong to the list. Repeating them on
		// every row is allowed, disagreeing is not; blank inherits.
		if p := r.get("protocol"); p != "" {
			if g.req.Protocol != "" && g.req.Protocol != p {
				fail("asset %s: protocol %q disagrees with %q on line %d", assetID, p, g.req.Protocol, g.firstRow)
				g.bad = true
			}
			g.req.Protocol = p
		}
		if raw := r.get("poll_interval_ms"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				fail("asset %s: poll_interval_ms %q is not an integer", assetID, raw)
				g.bad = true
			} else if g.req.PollIntervalMS != 0 && g.req.PollIntervalMS != v {
				fail("asset %s: poll_interval_ms %d disagrees with %d on line %d", assetID, v, g.req.PollIntervalMS, g.firstRow)
				g.bad = true
			} else {
				g.req.PollIntervalMS = v
			}
		}
		enabled := true
		if raw := r.get("enabled"); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				fail("asset %s point %s: enabled %q is not true or false", assetID, r.get("name"), raw)
				g.bad = true
			}
			enabled = v
		}
		enc := map[string]any{}
		for k, raw := range r.prefixed("encoding.") {
			enc[k] = parseEncodingCell(raw)
		}
		if len(enc) == 0 {
			enc = nil
		}
		g.req.Points = append(g.req.Points, Point{
			Name: r.get("name"), ValueType: r.get("value_type"), Unit: r.get("unit"),
			Address: r.get("address"), Encoding: enc, Enabled: enabled,
		})
	}

	for _, assetID := range order {
		g := groups[assetID]
		if g.bad {
			rep.Points.Failed++
			continue
		}
		existing, err := s.store.GetPointList(assetID)
		if err != nil {
			return err
		}
		if existing != nil && samePointList(existing, &g.req) {
			rep.Points.Unchanged++
			continue
		}
		if _, err := s.UpsertPointList(g.req); err != nil {
			rep.Points.Failed++
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s:%d: asset %s: %v", plantPointsFile, g.firstRow, assetID, err))
			continue
		}
		if existing == nil {
			rep.Points.Created++
		} else {
			rep.Points.Updated++
		}
	}
	return nil
}

func sameAsset(a, b *Asset) bool {
	return a.Name == b.Name && a.TemplateName == b.TemplateName && a.Source == b.Source &&
		reflect.DeepEqual(nilIfEmptySlice(a.Labels), nilIfEmptySlice(b.Labels)) &&
		reflect.DeepEqual(nilIfEmptyMap(a.Attributes), nilIfEmptyMap(b.Attributes)) &&
		reflect.DeepEqual(nilIfEmptyMap(a.ExternalIDs), nilIfEmptyMap(b.ExternalIDs))
}

// samePointList compares what a list declares, ignoring version and times. It
// compares through JSON so an encoding read back from SQLite and one parsed
// from a CSV cell agree on number types.
func samePointList(have *PointList, want *UpsertPointListRequest) bool {
	if have.Protocol != want.Protocol || have.PollIntervalMS != want.PollIntervalMS || len(have.Points) != len(want.Points) {
		return false
	}
	norm := func(points []Point) string {
		type p struct {
			Name, ValueType, Unit, Address string
			Enabled                        bool
			Encoding                       map[string]any
		}
		out := make([]p, len(points))
		for i, x := range points {
			out[i] = p{x.Name, x.ValueType, x.Unit, x.Address, x.Enabled, nilIfEmptyAnyMap(x.Encoding)}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		b, _ := json.Marshal(out)
		return string(b)
	}
	return norm(have.Points) == norm(want.Points)
}

// encodingCell renders one encoding value for a CSV cell. Scalars are written
// as themselves; anything nested as JSON.
func encodingCell(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	default:
		b, err := json.Marshal(x)
		return string(b), err
	}
}

// parseEncodingCell reverses encodingCell. A cell that reads as a number is a
// number and true/false is a bool, so scale: 0.1 stays numeric; a JSON object
// or array is parsed; anything else is a string. The consequence -- a string
// value that looks numeric becomes a number -- is the same one YAML has, and
// no protocol encoding uses one.
func parseEncodingCell(raw string) any {
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(raw); err == nil && (raw == "true" || raw == "false") {
		return b
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			return v
		}
	}
	return raw
}

// --- CSV plumbing ---

type csvRow struct {
	line   int
	header []string
	cells  []string
}

func (r csvRow) get(col string) string {
	for i, h := range r.header {
		if h == col && i < len(r.cells) {
			return strings.TrimSpace(r.cells[i])
		}
	}
	return ""
}

// prefixed collects the non-empty cells of every column named prefix+key.
func (r csvRow) prefixed(prefix string) map[string]string {
	var out map[string]string
	for i, h := range r.header {
		if !strings.HasPrefix(h, prefix) || i >= len(r.cells) {
			continue
		}
		v := strings.TrimSpace(r.cells[i])
		if v == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[strings.TrimPrefix(h, prefix)] = v
	}
	return out
}

type csvTable struct {
	file   string
	header []string
	rows   []csvRow
}

func (t *csvTable) require(cols ...string) error {
	for _, c := range cols {
		found := false
		for _, h := range t.header {
			if h == c {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: missing column %q (header: %s)", t.file, c, strings.Join(t.header, ","))
		}
	}
	return nil
}

func readCSV(path string) (*csvTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	rd := csv.NewReader(bytes.NewReader(data))
	rd.FieldsPerRecord = -1 // a spreadsheet drops trailing empty cells
	t := &csvTable{file: filepath.Base(path)}
	line := 0
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.file, err)
		}
		line++
		if t.header == nil {
			for _, h := range rec {
				t.header = append(t.header, strings.TrimSpace(h))
			}
			continue
		}
		if isBlank(rec) {
			continue
		}
		t.rows = append(t.rows, csvRow{line: line, header: t.header, cells: rec})
	}
	if t.header == nil {
		return nil, fmt.Errorf("%s: empty file", t.file)
	}
	return t, nil
}

func writeCSV(path string, rows [][]string) error {
	var buf bytes.Buffer
	buf.Write(utf8BOM)
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func isBlank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, csvListSep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func prefixed(prefix string, keys map[string]bool) []string {
	out := sortedSet(keys)
	for i := range out {
		out[i] = prefix + out[i]
	}
	return out
}

func sortedSet(keys map[string]bool) []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nilIfEmptySlice(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nilIfEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func nilIfEmptyAnyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	return m
}
