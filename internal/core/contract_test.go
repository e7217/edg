package core

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const contractTestTS = int64(1700000000000)

func reasons(vs []Violation) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Reason
	}
	return out
}

func declared(tags map[string]declaredTag) *assetProfile {
	return &assetProfile{Exists: true, Tags: tags}
}

// --- envelope rules ---

// The three payloads the 2026-09-14 review sent through as "validated".
func TestContract_EnvelopeRejectsWhatTheReviewFound(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)

	empty := AssetData{}
	res := c.Apply(&empty, nil)
	assert.True(t, res.Rejected, "an empty object must not be published")
	assert.ElementsMatch(t, []string{ViolationMissingAssetID, ViolationNoValues}, reasons(res.Violations))

	seconds := AssetData{AssetID: "a", Timestamp: 1789393384, Values: []TagValue{{Name: "t", Number: floatPtr(1)}}}
	res = c.Apply(&seconds, nil)
	assert.True(t, res.Rejected, "a seconds timestamp is stored as January 1970")
	assert.Equal(t, []string{ViolationTimestampUnit}, reasons(res.Violations))
}

func TestContract_MissingTimestampIsStampedNotRejected(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)
	fixed := time.UnixMilli(contractTestTS + 42)
	c.now = func() time.Time { return fixed }

	data := AssetData{AssetID: "a", Values: []TagValue{{Name: "t", Number: floatPtr(1)}}}
	res := c.Apply(&data, nil)
	assert.False(t, res.Rejected)
	assert.Empty(t, res.Violations)
	assert.True(t, res.TimestampFilled)
	assert.Equal(t, contractTestTS+42, data.Timestamp)
}

// --- value rules ---

func TestContract_BadValueIsDroppedAndSiblingsKept(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)
	nan := math.NaN()
	data := AssetData{AssetID: "a", Timestamp: contractTestTS, Values: []TagValue{
		{Name: "ok", Number: floatPtr(1)},
		{Name: "", Number: floatPtr(2)},
		{Name: "ok", Number: floatPtr(3)},
		{Name: "none"},
		{Name: "both", Number: floatPtr(4), Text: strPtr("x")},
		{Name: "nan", Number: &nan},
	}}
	res := c.Apply(&data, nil)

	assert.False(t, res.Rejected, "one bad register must not take the rest of the poll with it")
	assert.Equal(t, 5, res.DroppedValues)
	assert.Equal(t, []string{
		ViolationEmptyName, ViolationDuplicateName, ViolationNoReading,
		ViolationMultipleReadings, ViolationNonFinite,
	}, reasons(res.Violations))
	require.Len(t, data.Values, 1)
	assert.Equal(t, 1.0, *data.Values[0].Number, "the first of a duplicated name is the one kept")
}

func TestContract_AllValuesBadRejectsTheMessage(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)
	data := AssetData{AssetID: "a", Timestamp: contractTestTS, Values: []TagValue{{Name: "none"}}}
	res := c.Apply(&data, nil)
	assert.True(t, res.Rejected)
}

func TestContract_DeclaredTypeAndUnit(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)
	profile := declared(map[string]declaredTag{
		"temperature": {ValueType: ValueTypeNumber, Unit: "°C"},
		"state":       {ValueType: ValueTypeText},
		"pressure":    {ValueType: ValueTypeNumber, Unit: "bar"},
	})
	data := AssetData{AssetID: "a", Timestamp: contractTestTS, Values: []TagValue{
		{Name: "temperature", Text: strPtr("25.5")},            // the review's case
		{Name: "state", Text: strPtr("RUNNING")},               // ok
		{Name: "pressure", Number: floatPtr(3)},                // unit filled
		{Name: "humidity", Number: floatPtr(40)},               // undeclared: passes
		{Name: "temperature2", Number: floatPtr(1), Unit: "F"}, // undeclared: passes as sent
	}}
	res := c.Apply(&data, profile)

	assert.Equal(t, []string{ViolationTypeMismatch}, reasons(res.Violations))
	assert.Equal(t, "declared NUMBER, received TEXT", res.Violations[0].Detail)
	require.Len(t, data.Values, 4)
	assert.Equal(t, "bar", data.Values[1].Unit, "a missing unit is filled from the declaration")
}

func TestContract_UnitMismatchIsRefusedNotRelabelled(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)
	profile := declared(map[string]declaredTag{"t": {ValueType: ValueTypeNumber, Unit: "°C"}})
	data := AssetData{AssetID: "a", Timestamp: contractTestTS, Values: []TagValue{
		{Name: "t", Number: floatPtr(77), Unit: "°F"},
		{Name: "u", Number: floatPtr(1)},
	}}
	res := c.Apply(&data, profile)
	assert.Equal(t, []string{ViolationUnitMismatch}, reasons(res.Violations))
	require.Len(t, data.Values, 1)
	assert.Equal(t, "u", data.Values[0].Name)
}

func TestContract_ReservedMetadataKeyIsDropped(t *testing.T) {
	c := NewContractChecker(nil, DataContractEnforce)
	data := AssetData{AssetID: "a", Timestamp: contractTestTS,
		Values:   []TagValue{{Name: "t", Number: floatPtr(1)}},
		Metadata: map[string]string{"name": "spoof", "line": "L1"}}
	res := c.Apply(&data, nil)
	assert.Equal(t, []string{ViolationReservedMetadata}, reasons(res.Violations))
	assert.Equal(t, map[string]string{"line": "L1"}, data.Metadata)
	assert.False(t, res.Rejected)
}

func TestContract_WarnModeCountsButChangesNothing(t *testing.T) {
	c := NewContractChecker(nil, DataContractWarn)
	profile := declared(map[string]declaredTag{"t": {ValueType: ValueTypeNumber}})
	data := AssetData{AssetID: "a", Timestamp: contractTestTS,
		Values:   []TagValue{{Name: "t", Text: strPtr("x")}, {Name: "none"}},
		Metadata: map[string]string{"quality": "q"}}
	res := c.Apply(&data, profile)
	assert.Len(t, res.Violations, 3)
	assert.False(t, res.Rejected)
	assert.Zero(t, res.DroppedValues)
	assert.Len(t, data.Values, 2)
	assert.Contains(t, data.Metadata, "quality")

	empty := AssetData{}
	assert.False(t, c.Apply(&empty, nil).Rejected, "warn never rejects")
}

// --- declaration resolution ---

func newContractStore(t *testing.T) *Store {
	t.Helper()
	store := newMetricsTestStore(t)
	require.NoError(t, store.UpsertTemplate(&AssetTemplate{
		Name: "pump",
		Resources: []AssetResource{
			{Name: "temperature", ValueType: ValueTypeNumber, Unit: "°C"},
			{Name: "state", ValueType: ValueTypeText},
		},
	}))
	require.NoError(t, store.CreateAsset(&Asset{ID: "pump-1", Name: "Pump 1", TemplateName: "pump", Source: SourceManual}))
	return store
}

func TestContract_ProfileMergesTemplateAndPoints(t *testing.T) {
	store := newContractStore(t)
	_, err := store.UpsertPointList(&PointList{AssetID: "pump-1", Protocol: "modbus-tcp", Points: []Point{
		{Name: "temperature", ValueType: ValueTypeNumber, Unit: "°F", Address: "0", Enabled: true},
		{Name: "running", ValueType: ValueTypeFlag, Address: "1", Enabled: true},
	}})
	require.NoError(t, err)

	c := NewContractChecker(store, DataContractEnforce)
	p, err := c.Profile("pump-1")
	require.NoError(t, err)
	assert.True(t, p.Exists)
	assert.Equal(t, declaredTag{ValueType: ValueTypeNumber, Unit: "°F"}, p.Tags["temperature"],
		"a point declaration is about this asset and wins over its template")
	assert.Equal(t, declaredTag{ValueType: ValueTypeText}, p.Tags["state"])
	assert.Equal(t, declaredTag{ValueType: ValueTypeFlag}, p.Tags["running"])

	missing, err := c.Profile("nobody")
	require.NoError(t, err)
	assert.False(t, missing.Exists)
}

func TestContract_ProfileCacheIsBoundedByMasterData(t *testing.T) {
	store := newContractStore(t)
	c := NewContractChecker(store, DataContractEnforce)
	for _, id := range []string{"pump-1", "ghost-1", "ghost-2"} {
		_, err := c.Profile(id)
		require.NoError(t, err)
	}
	assert.Len(t, c.cache, 1, "undeclared ids must not grow the cache")
}

func TestContract_PointsChangedFlushesTheCache(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)
	store := newContractStore(t)
	c := NewContractChecker(store, DataContractEnforce)
	require.NoError(t, c.Start(nc))
	t.Cleanup(func() { _ = c.Stop() })
	require.NoError(t, nc.Flush())

	p, err := c.Profile("pump-1")
	require.NoError(t, err)
	_, declaredRunning := p.Tags["running"]
	require.False(t, declaredRunning)

	svc := NewMetadataService(store, nil, NewEventPublisher(nc), ConstraintsEnforcementWarn)
	_, err = svc.UpsertPointList(UpsertPointListRequest{AssetID: "pump-1", Protocol: "modbus-tcp", Points: []Point{
		{Name: "running", ValueType: ValueTypeFlag, Address: "1", Enabled: true},
	}})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		p, err := c.Profile("pump-1")
		require.NoError(t, err)
		_, ok := p.Tags["running"]
		return ok
	}, 2*time.Second, 20*time.Millisecond, "a point-list write must reach the contract without a restart")
}

// --- through the handler ---

func newContractHandlerFixture(t *testing.T, mode string) (*DataHandler, *nats.Conn, nats.JetStreamContext) {
	t.Helper()
	_, nc, js := startTestNATSServer(t, true)
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "CONTRACT",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)
	store := newContractStore(t)
	h := NewDataHandlerWithConfig(js, store, DataHandlerOptions{
		Contract: NewContractChecker(store, mode),
	})
	return h, nc, js
}

func fetchOne(t *testing.T, js nats.JetStreamContext, subject, durable string) *nats.Msg {
	t.Helper()
	sub, err := js.PullSubscribe(subject, durable)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	msgs, err := sub.Fetch(1, nats.MaxWait(2*time.Second))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	return msgs[0]
}

func streamCount(t *testing.T, js nats.JetStreamContext, subject string) uint64 {
	t.Helper()
	info, err := js.StreamInfo("CONTRACT", &nats.StreamInfoRequest{SubjectsFilter: subject})
	require.NoError(t, err)
	return info.State.Subjects[subject]
}

func publishRaw(h *DataHandler, v any) []byte {
	payload, _ := json.Marshal(v)
	h.HandleAssetData(&nats.Msg{Subject: "platform.data.asset", Data: payload})
	return payload
}

func TestHandler_RejectedMessageIsDeadLetteredWithViolations(t *testing.T) {
	h, _, js := newContractHandlerFixture(t, DataContractEnforce)
	before := contractMessagesRejected.Value()
	beforeReason := vecValue(t, "edg_core_data_contract_violations_total", "reason", ViolationNoValues)

	payload := publishRaw(h, map[string]any{})

	assert.Equal(t, before+1, contractMessagesRejected.Value())
	assert.Equal(t, beforeReason+1, vecValue(t, "edg_core_data_contract_violations_total", "reason", ViolationNoValues))
	assert.Zero(t, streamCount(t, js, "platform.data.validated"), "nothing may reach validated")

	var dl DeadLetterMessage
	require.NoError(t, json.Unmarshal(fetchOne(t, js, "platform.data.deadletter", "dl").Data, &dl))
	assert.Equal(t, errContractRejected.Error(), dl.Error)
	assert.JSONEq(t, string(payload), string(dl.Payload))
	assert.ElementsMatch(t, []string{ViolationMissingAssetID, ViolationNoValues}, reasons(dl.Violations))
}

func TestHandler_PartialDropPublishesRemainderAndDeadLettersOriginal(t *testing.T) {
	h, _, js := newContractHandlerFixture(t, DataContractEnforce)
	before := contractValuesDropped.Value()

	payload := publishRaw(h, AssetData{AssetID: "pump-1", Timestamp: contractTestTS, Values: []TagValue{
		{Name: "temperature", Text: strPtr("25.5")},
		{Name: "state", Text: strPtr("RUNNING")},
	}})

	assert.Equal(t, before+1, contractValuesDropped.Value())

	var published AssetData
	require.NoError(t, json.Unmarshal(fetchOne(t, js, "platform.data.validated", "v").Data, &published))
	require.Len(t, published.Values, 1)
	assert.Equal(t, "state", published.Values[0].Name)

	var dl DeadLetterMessage
	require.NoError(t, json.Unmarshal(fetchOne(t, js, "platform.data.deadletter", "dl").Data, &dl))
	assert.Equal(t, errContractPartial.Error(), dl.Error)
	assert.JSONEq(t, string(payload), string(dl.Payload), "the dead letter keeps what was removed")
	assert.Equal(t, []string{ViolationTypeMismatch}, reasons(dl.Violations))
}

func TestHandler_StampedTimestampIsPublished(t *testing.T) {
	h, _, js := newContractHandlerFixture(t, DataContractEnforce)
	publishRaw(h, AssetData{AssetID: "pump-1", Values: []TagValue{{Name: "temperature", Number: floatPtr(1)}}})

	var published AssetData
	require.NoError(t, json.Unmarshal(fetchOne(t, js, "platform.data.validated", "v").Data, &published))
	assert.Greater(t, published.Timestamp, minMillisTimestamp)
	assert.Equal(t, "°C", published.Values[0].Unit, "the declared unit is filled in")
}

func TestHandler_WarnModePublishesUnchangedAndDeadLettersNothing(t *testing.T) {
	h, _, js := newContractHandlerFixture(t, DataContractWarn)
	publishRaw(h, AssetData{AssetID: "pump-1", Timestamp: contractTestTS, Values: []TagValue{
		{Name: "temperature", Text: strPtr("25.5")},
	}})
	assert.Equal(t, uint64(1), streamCount(t, js, "platform.data.validated"))
	assert.Zero(t, streamCount(t, js, "platform.data.deadletter"))
}

// --- enrichment authority ---

// The review's fifth case: an adapter reporting the wrong equipment kept it.
func TestEnricher_MasterDataWinsOverAdapterMetadata(t *testing.T) {
	store := newTraversalTestStore(t)
	e := NewEnricher(store, EnricherOptions{})
	before := enricherMetadataOverrides.Value()

	data := AssetData{AssetID: "sensor-001", Metadata: map[string]string{
		"equipment": "Wrong Equipment",
		"firmware":  "1.2",
	}}
	require.NoError(t, e.Enrich(&data))

	assert.Equal(t, "Pump A", data.Metadata["equipment"])
	assert.Equal(t, "1.2", data.Metadata["firmware"], "keys master data does not derive are kept")
	assert.Equal(t, before+1, enricherMetadataOverrides.Value())
}
