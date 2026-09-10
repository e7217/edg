package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleAssetData_Success tests successful data processing
func TestHandleAssetData_Success(t *testing.T) {
	handler := NewDataHandler(nil, nil)

	tempValue := 25.5
	data := &AssetData{
		AssetID:   "sensor-001",
		Timestamp: 1234567890,
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue, Unit: "celsius", Quality: "good"},
		},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	// Create mock NATS message
	msg := &nats.Msg{
		Subject: "platform.data.raw",
		Data:    jsonData,
	}

	before := vecValue(t, "edg_core_data_values_total", "kind", valueKindNumber)

	// Process message
	handler.HandleAssetData(msg)

	// The payload was decoded and every value in it accounted for. That is
	// what "processed" means here; there is no JetStream in this fixture.
	assert.Equal(t, before+1, vecValue(t, "edg_core_data_values_total", "kind", valueKindNumber))
}

// TestHandleAssetData_InvalidJSON tests handling of malformed JSON
func TestHandleAssetData_InvalidJSON(t *testing.T) {
	handler := NewDataHandler(nil, nil)

	// Create message with invalid JSON
	msg := &nats.Msg{
		Subject: "platform.data.raw",
		Data:    []byte("{invalid json}"),
	}

	beforeDecode := dataDecodeFailures.Value()
	beforeValues := vecValue(t, "edg_core_data_values_total", "kind", valueKindNumber)

	// Process message (should log error but not panic)
	handler.HandleAssetData(msg)

	// It failed at decode and went no further.
	assert.Equal(t, beforeDecode+1, dataDecodeFailures.Value())
	assert.Equal(t, beforeValues, vecValue(t, "edg_core_data_values_total", "kind", valueKindNumber))
}

// TestHandleAssetData_PassThrough_DoesNotRegister verifies the default policy:
// an undeclared asset_id is not registered, but its data still passes through.
func TestHandleAssetData_PassThrough_DoesNotRegister(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	handler := NewDataHandler(nil, store)

	before := undeclaredAssets.Value()
	tempValue := 25.5
	data := &AssetData{
		AssetID:   "undeclared-sensor",
		Timestamp: 1234567890,
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue},
		},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	handler.HandleAssetData(&nats.Msg{
		Subject: "platform.data.asset",
		Data:    jsonData,
	})

	asset, err := store.GetAsset("undeclared-sensor")
	require.NoError(t, err)
	assert.Nil(t, asset)
	assert.Equal(t, before+1, undeclaredAssets.Value())
	// That the data still reaches the validated subject is asserted against a
	// real JetStream in TestHandleAssetData_WithJetStreamAndStore; there is
	// nothing to publish to here.
}

// TestHandleAssetData_DeadLetterPolicy_SkipsValidated verifies the dead_letter
// policy: an undeclared asset_id returns early (not stored / not validated).
func TestHandleAssetData_DeadLetterPolicy_SkipsValidated(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// A real JetStream, because the claim in the name -- that the message does
	// not reach the validated subject -- is only checkable against one. With a
	// nil JetStream nothing is published either way, so the test could not
	// tell the two policies apart.
	_, _, js := startTestNATSServer(t, true)
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "DEADLETTER_POLICY_TEST",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)

	handler := NewDataHandlerWithConfig(js, store, DataHandlerOptions{
		UnknownAssetPolicy: UnknownAssetPolicyDeadLetter,
	})

	before := undeclaredAssets.Value()
	tempValue := 25.5
	data := &AssetData{
		AssetID: "undeclared-dl-sensor",
		Values:  []TagValue{{Name: "temperature", Number: &tempValue}},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	handler.HandleAssetData(&nats.Msg{
		Subject: "platform.data.asset",
		Data:    jsonData,
	})

	assert.Equal(t, before+1, undeclaredAssets.Value())
	asset, err := store.GetAsset("undeclared-dl-sensor")
	require.NoError(t, err)
	assert.Nil(t, asset)

	// Nothing on the validated subject, and the dead-letter subject got it.
	info, err := js.StreamInfo("DEADLETTER_POLICY_TEST")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), info.State.Msgs, "exactly the dead letter should be in the stream")

	sub, err := js.SubscribeSync(DefaultValidatedDataSubject, nats.DeliverAll())
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()
	_, err = sub.NextMsg(500 * time.Millisecond)
	assert.ErrorIs(t, err, nats.ErrTimeout, "the message must not reach the validated subject")
}

func TestNewDataHandlerWithSubjects(t *testing.T) {
	handler := NewDataHandlerWithSubjects(nil, nil, "custom.validated", "custom.deadletter")

	require.NotNil(t, handler)
	assert.Equal(t, "custom.validated", handler.validatedSubject)
	assert.Equal(t, "custom.deadletter", handler.deadLetterSubject)

	defaults := NewDataHandlerWithSubjects(nil, nil, "", "")
	assert.Equal(t, DefaultValidatedDataSubject, defaults.validatedSubject)
	assert.Equal(t, DefaultDeadLetterSubject, defaults.deadLetterSubject)
}

func TestNewDataHandlerWithConfig(t *testing.T) {
	publisher := NewEventPublisher(nil)
	handler := NewDataHandlerWithConfig(nil, nil, DataHandlerOptions{
		ValidatedSubject:   "custom.validated",
		DeadLetterSubject:  "custom.deadletter",
		Events:             publisher,
		UnknownAssetPolicy: UnknownAssetPolicyDeadLetter,
	})

	require.NotNil(t, handler)
	assert.Equal(t, "custom.validated", handler.validatedSubject)
	assert.Equal(t, "custom.deadletter", handler.deadLetterSubject)
	assert.Equal(t, UnknownAssetPolicyDeadLetter, handler.unknownAssetPolicy)
	assert.Same(t, publisher, handler.events)

	defaults := NewDataHandlerWithConfig(nil, nil, DataHandlerOptions{})
	assert.Equal(t, DefaultValidatedDataSubject, defaults.validatedSubject)
	assert.Equal(t, DefaultDeadLetterSubject, defaults.deadLetterSubject)
	assert.Equal(t, UnknownAssetPolicyPassThrough, defaults.unknownAssetPolicy)
}

func TestNewDataHandlerWithSubjectsAndEvents(t *testing.T) {
	publisher := NewEventPublisher(nil)
	handler := NewDataHandlerWithSubjects(nil, nil, "custom.validated", "custom.deadletter", publisher)

	require.NotNil(t, handler)
	assert.Equal(t, "custom.validated", handler.validatedSubject)
	assert.Equal(t, "custom.deadletter", handler.deadLetterSubject)
	assert.Same(t, publisher, handler.events)
}

func TestHandleAssetData_PassThrough_NoChangeEvent(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	handler := NewDataHandler(nil, store, NewEventPublisher(nc))

	tempValue := 25.5
	data := &AssetData{
		AssetID: "manual-event-sensor",
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue},
		},
	}
	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	handler.HandleAssetData(&nats.Msg{Data: jsonData})

	requireNoMetaEvent(t, assetEvents)
	asset, err := store.GetAsset("manual-event-sensor")
	require.NoError(t, err)
	assert.Nil(t, asset)
}

// TestHandleAssetData_ExistingAssetDoesNotPublishChangedEvent tests existing asset ingestion.
func TestHandleAssetData_ExistingAssetDoesNotPublishChangedEvent(t *testing.T) {
	_, nc, _ := startTestNATSServer(t, false)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.CreateAsset(&Asset{
		ID:        "existing-sensor",
		Name:      "existing-sensor",
		Source:    SourceManual,
		CreatedAt: time.Now(),
	}))

	assetEvents := subscribeMetaEvents(t, nc, SubjectAssetChanged)
	handler := NewDataHandler(nil, store, NewEventPublisher(nc))

	tempValue := 25.5
	data := &AssetData{
		AssetID: "existing-sensor",
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue},
		},
	}
	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	handler.HandleAssetData(&nats.Msg{Data: jsonData})
	requireNoMetaEvent(t, assetEvents)
}

func TestHandleAssetData_ExistingAssetUnaffected(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	createdAt := time.Now().Add(-time.Hour)
	require.NoError(t, store.CreateAsset(&Asset{
		ID:        "existing-manual-sensor",
		Name:      "Existing Manual Sensor",
		Source:    SourceManual,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}))

	handler := NewDataHandler(nil, store)

	tempValue := 25.5
	data := &AssetData{
		AssetID: "existing-manual-sensor",
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue},
		},
	}
	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	handler.HandleAssetData(&nats.Msg{Data: jsonData})

	asset, err := store.GetAsset("existing-manual-sensor")
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, "Existing Manual Sensor", asset.Name)
	assert.Equal(t, SourceManual, asset.Source)
}
