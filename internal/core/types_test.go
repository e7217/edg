package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssetData_JSON tests AssetData JSON serialization
func TestAssetData_JSON(t *testing.T) {
	tempValue := 25.5
	statusValue := "normal"

	data := &AssetData{
		AssetID:   "sensor-001",
		Timestamp: 1234567890,
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue, Unit: "celsius"},
			{Name: "status", Text: &statusValue},
		},
		Metadata: map[string]string{
			"location": "building-a",
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	// Unmarshal back
	var decoded AssetData
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	// Verify fields
	assert.Equal(t, data.AssetID, decoded.AssetID)
	assert.Equal(t, data.Timestamp, decoded.Timestamp)
	assert.Len(t, decoded.Values, 2)
	assert.Equal(t, "temperature", decoded.Values[0].Name)
	assert.Equal(t, tempValue, *decoded.Values[0].Number)
	assert.Equal(t, "building-a", decoded.Metadata["location"])
}

// TestTagValue_Number tests NUMBER type TagValue
func TestTagValue_Number(t *testing.T) {
	value := 42.0
	tag := TagValue{
		Name:    "temperature",
		Number:  &value,
		Unit:    "celsius",
		Quality: "good",
	}

	// Marshal and verify
	jsonData, err := json.Marshal(tag)
	require.NoError(t, err)

	var decoded TagValue
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "temperature", decoded.Name)
	require.NotNil(t, decoded.Number)
	assert.Equal(t, 42.0, *decoded.Number)
	assert.Nil(t, decoded.Text)
	assert.Nil(t, decoded.Flag)
}

// TestTagValue_Text tests TEXT type TagValue
func TestTagValue_Text(t *testing.T) {
	value := "running"
	tag := TagValue{
		Name:    "status",
		Text:    &value,
		Quality: "good",
	}

	// Marshal and verify
	jsonData, err := json.Marshal(tag)
	require.NoError(t, err)

	var decoded TagValue
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "status", decoded.Name)
	require.NotNil(t, decoded.Text)
	assert.Equal(t, "running", *decoded.Text)
	assert.Nil(t, decoded.Number)
	assert.Nil(t, decoded.Flag)
}
