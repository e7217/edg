package core

import (
	"encoding/json"
	"testing"

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

	// Process message
	handler.HandleAssetData(msg)

	// Verify data was stored
	assert.Equal(t, 1, handler.GetDataCount())
}

// TestHandleAssetData_InvalidJSON tests handling of malformed JSON
func TestHandleAssetData_InvalidJSON(t *testing.T) {
	handler := NewDataHandler(nil, nil)

	// Create message with invalid JSON
	msg := &nats.Msg{
		Subject: "platform.data.raw",
		Data:    []byte("{invalid json}"),
	}

	// Process message (should log error but not panic)
	handler.HandleAssetData(msg)

	// Verify no data was stored
	assert.Equal(t, 0, handler.GetDataCount())
}

// TestHandleAssetData_AutoRegister tests auto-registration of unknown assets
func TestHandleAssetData_AutoRegister(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	handler := NewDataHandler(nil, store)

	tempValue := 25.5
	data := &AssetData{
		AssetID:   "new-sensor",
		Timestamp: 1234567890,
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue},
		},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	msg := &nats.Msg{
		Subject: "platform.data.raw",
		Data:    jsonData,
	}

	// Process message
	handler.HandleAssetData(msg)

	// Verify asset was auto-registered
	asset, err := store.GetAsset("new-sensor")
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, "new-sensor", asset.ID)
	assert.Equal(t, "new-sensor", asset.Name)
}

// TestGetDataCount tests thread-safe data count
func TestGetDataCount(t *testing.T) {
	handler := NewDataHandler(nil, nil)

	assert.Equal(t, 0, handler.GetDataCount())

	// Add some data
	for i := 0; i < 5; i++ {
		tempValue := float64(i)
		data := &AssetData{
			AssetID: "sensor-001",
			Values: []TagValue{
				{Name: "temp", Number: &tempValue},
			},
		}
		jsonData, err := json.Marshal(data)
		require.NoError(t, err)
		msg := &nats.Msg{Data: jsonData}
		handler.HandleAssetData(msg)
	}

	assert.Equal(t, 5, handler.GetDataCount())
}
