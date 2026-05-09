package sdk

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func mustNormalize(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("normalize remarshal: %v", err)
	}
	return out
}

// TestAssetDataRoundTrip verifies an AssetData decoded from a wire fixture
// re-marshals to a JSON value semantically equal to the fixture.
func TestAssetDataRoundTrip(t *testing.T) {
	raw := loadFixture(t, "asset_data.json")
	var ad AssetData
	if err := json.Unmarshal(raw, &ad); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if ad.AssetID != "sensor-001" {
		t.Errorf("AssetID: %q", ad.AssetID)
	}
	if ad.Timestamp != 1715299200000 {
		t.Errorf("Timestamp: %d", ad.Timestamp)
	}
	if len(ad.Values) != 3 {
		t.Fatalf("Values len: %d", len(ad.Values))
	}
	// Number, Text, Flag set on different rows.
	if ad.Values[0].Number == nil || *ad.Values[0].Number != 25.5 {
		t.Errorf("Values[0].Number: %v", ad.Values[0].Number)
	}
	if ad.Values[1].Text == nil || *ad.Values[1].Text != "running" {
		t.Errorf("Values[1].Text: %v", ad.Values[1].Text)
	}
	if ad.Values[2].Flag == nil || *ad.Values[2].Flag != true {
		t.Errorf("Values[2].Flag: %v", ad.Values[2].Flag)
	}

	out, err := json.Marshal(ad)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(mustNormalize(t, out), mustNormalize(t, raw)) {
		t.Errorf("round-trip mismatch:\nout:  %s\nwant: %s", out, raw)
	}
}

// TestTagValueOmitempty ensures absent optional fields are omitted (matches
// Python's TagValue.to_dict that drops None values).
func TestTagValueOmitempty(t *testing.T) {
	tv := TagValue{Name: "t", Quality: QualityGood}
	out, err := json.Marshal(tv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, k := range []string{"number", "text", "flag", "unit"} {
		if strings.Contains(string(out), `"`+k+`"`) {
			t.Errorf("expected %q omitted, got %s", k, out)
		}
	}
}

// TestAssetDataMetadataOmitempty checks Metadata is omitted when nil.
func TestAssetDataMetadataOmitempty(t *testing.T) {
	ad := AssetData{AssetID: "x", Timestamp: 1, Values: []TagValue{}}
	out, err := json.Marshal(ad)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), `"metadata"`) {
		t.Errorf("metadata should be omitted: %s", out)
	}
}

func TestAssetRoundTrip(t *testing.T) {
	raw := loadFixture(t, "asset.json")
	var a Asset
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Source != "opcua" {
		t.Errorf("Source: %q", a.Source)
	}
	if a.TemplateName != "temperature_sensor" {
		t.Errorf("TemplateName: %q", a.TemplateName)
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(mustNormalize(t, out), mustNormalize(t, raw)) {
		t.Errorf("round-trip mismatch:\nout:  %s\nwant: %s", out, raw)
	}
}

func TestAssetRelationRoundTrip(t *testing.T) {
	raw := loadFixture(t, "relation.json")
	var r AssetRelation
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.RelationType != RelationPartOf {
		t.Errorf("RelationType: %q", r.RelationType)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(mustNormalize(t, out), mustNormalize(t, raw)) {
		t.Errorf("round-trip mismatch:\nout:  %s\nwant: %s", out, raw)
	}
}

func TestRelationTypeWireValues(t *testing.T) {
	cases := map[RelationType]string{
		RelationPartOf:      "partOf",
		RelationConnectedTo: "connectedTo",
		RelationLocatedIn:   "locatedIn",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("RelationType wire: got %q want %q", got, want)
		}
	}
}

func TestMetaChangeEventDecodeAfterAsset(t *testing.T) {
	raw := loadFixture(t, "meta_change_event_asset_created.json")
	var ev MetaChangeEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.EventType != EventCreated || ev.EntityType != EntityAsset {
		t.Errorf("event_type/entity_type: %q/%q", ev.EventType, ev.EntityType)
	}
	if ev.SchemaVersion != EventSchemaVersion {
		t.Errorf("schema_version: %d", ev.SchemaVersion)
	}
	wantTS, _ := time.Parse(time.RFC3339, "2026-05-07T12:34:56Z")
	if !ev.Timestamp.Equal(wantTS) {
		t.Errorf("timestamp: got %s want %s", ev.Timestamp, wantTS)
	}

	var asset Asset
	if err := ev.DecodeAfter(&asset); err != nil {
		t.Fatalf("DecodeAfter: %v", err)
	}
	if asset.ID != "sensor-001" || asset.Source != "auto" {
		t.Errorf("asset decoded: %+v", asset)
	}

	// Before is empty for create events.
	var before Asset
	if err := ev.DecodeBefore(&before); err != nil {
		t.Errorf("DecodeBefore on empty: %v", err)
	}
	if !reflect.DeepEqual(before, Asset{}) {
		t.Errorf("DecodeBefore should not modify v when Before is empty: %+v", before)
	}
}

func TestMetaChangeEventDecodeBeforeRelation(t *testing.T) {
	raw := loadFixture(t, "meta_change_event_relation_deleted.json")
	var ev MetaChangeEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.EventType != EventDeleted || ev.EntityType != EntityRelation {
		t.Errorf("event_type/entity_type: %q/%q", ev.EventType, ev.EntityType)
	}

	var rel AssetRelation
	if err := ev.DecodeBefore(&rel); err != nil {
		t.Fatalf("DecodeBefore: %v", err)
	}
	if rel.RelationType != RelationPartOf {
		t.Errorf("relation type: %q", rel.RelationType)
	}
}

func TestSubjectsContractValues(t *testing.T) {
	cases := map[string]string{
		"data":             SubjectAssetData,
		"asset.create":     SubjectAssetCreate,
		"asset.get":        SubjectAssetGet,
		"asset.list":       SubjectAssetList,
		"asset.update":     SubjectAssetUpdate,
		"asset.delete":     SubjectAssetDelete,
		"relation.create":  SubjectRelationCreate,
		"relation.get":     SubjectRelationGet,
		"relation.list":    SubjectRelationList,
		"relation.delete":  SubjectRelationDelete,
		"asset.changed":    SubjectAssetChanged,
		"relation.changed": SubjectRelationChanged,
		"meta.changed":     SubjectMetaChangedAll,
	}
	want := map[string]string{
		"data":             "platform.data.asset",
		"asset.create":     "platform.meta.asset.create",
		"asset.get":        "platform.meta.asset.get",
		"asset.list":       "platform.meta.asset.list",
		"asset.update":     "platform.meta.asset.update",
		"asset.delete":     "platform.meta.asset.delete",
		"relation.create":  "platform.meta.relation.create",
		"relation.get":     "platform.meta.relation.get",
		"relation.list":    "platform.meta.relation.list",
		"relation.delete":  "platform.meta.relation.delete",
		"asset.changed":    "platform.meta.asset.changed",
		"relation.changed": "platform.meta.relation.changed",
		"meta.changed":     "platform.meta.*.changed",
	}
	for k, v := range cases {
		if v != want[k] {
			t.Errorf("subject %s: got %q want %q", k, v, want[k])
		}
	}
}
