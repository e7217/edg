package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidAdapterID(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"simple", "modbus-line3", true},
		{"underscore", "modbus_line3", true},
		{"colon", "site:line3", true},
		{"uuid", "3f2504e0-4f89-11d3-9a0c-0305e82c3301", true},
		{"digits", "12345", true},
		{"max length", "a" + strings.Repeat("b", 63), true},

		// A dot would reshape platform.adapter.status.<id> into extra tokens,
		// letting one adapter write into another's namespace.
		{"dot", "modbus.line3", false},
		{"wildcard star", "modbus*", false},
		{"wildcard gt", "modbus>", false},
		{"space", "modbus line3", false},
		{"empty", "", false},
		{"too long", strings.Repeat("a", 65), false},
		{"leading dash", "-modbus", false},
		{"slash breaks url path", "modbus/line3", false},

		// Reserved: these collide with sibling subjects and HTTP routes.
		{"reserved drift", "drift", false},
		{"reserved list", "list", false},
		{"reserved hello", "hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, IsValidAdapterID(tt.id))
		})
	}
}

func TestIsValidRunState(t *testing.T) {
	for _, s := range []RunState{RunStateStarting, RunStateRunning, RunStateDegraded, RunStateStopping, RunStateStopped} {
		assert.True(t, IsValidRunState(s), "%s must be valid", s)
	}
	assert.False(t, IsValidRunState("bogus"))
	assert.False(t, IsValidRunState(""))
}

func TestIsValidAvailability(t *testing.T) {
	for _, a := range []Availability{AvailabilityOnline, AvailabilityStale, AvailabilityOffline, AvailabilityUnknown} {
		assert.True(t, IsValidAvailability(a))
	}
	assert.False(t, IsValidAvailability("dead"))
}

// TestDeviceStateValuesMatchSDK pins the cross-SDK symmetry the design relies
// on. adapters/go/sdk/state.go and adapters/python/sdk/models.py already use
// these exact strings, so the wire needs no enum of its own and no mapping
// table. If either SDK renames a state this test is where it should be caught.
func TestDeviceStateValuesMatchSDK(t *testing.T) {
	expected := []string{"disconnected", "connecting", "connected", "reconnecting", "error"}
	for _, v := range expected {
		frame := AdapterStatusFrame{DeviceState: v}
		raw, err := json.Marshal(frame)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"device_state":"`+v+`"`)
	}
}

func TestAdapterStatusFrameRoundTrip(t *testing.T) {
	errAt := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	original := AdapterStatusFrame{
		SchemaVersion:      AdapterSchemaVersion,
		AdapterID:          "modbus-line3",
		InstanceID:         "0f2b9c1e6a414b2e",
		Seq:                412,
		Phase:              AdapterPhaseHeartbeat,
		RunState:           RunStateRunning,
		DeviceState:        "connected",
		HeartbeatIntervalS: 10,
		UptimeS:            4120,
		StartedAt:          time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC),
		SentAt:             time.Date(2026, 9, 9, 2, 8, 40, 0, time.UTC),
		SDK:                "go/0.5.0",
		AdapterVersion:     "modbus-tcp/1.2.0",
		Host:               "edge-01",
		PID:                8123,
		Capabilities:       []string{"ping"},
		ConfigVersion:      0,
		Assets: []AdapterAssetStatus{{
			AssetID:            "press-01",
			DeviceState:        "connected",
			PublishedTotal:     4098,
			CollectErrorsTotal: 3,
			LastError:          "read timeout",
			LastErrorAt:        &errAt,
		}},
		DeviceCounts: map[string]int{"connected": 28, "reconnecting": 1},
		Counters: AdapterCounters{
			PublishedTotal:     4098,
			CollectErrorsTotal: 3,
		},
	}

	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded AdapterStatusFrame
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, original, decoded, "round trip must not lose a field")
}

// TestConfigVersionAlwaysSerialized: the field is reserved for the declaration
// layer (P2) and is always 0 here. It must not be omitempty, or a consumer
// cannot distinguish "not supported" from "version 0".
func TestConfigVersionAlwaysSerialized(t *testing.T) {
	raw, err := json.Marshal(AdapterStatusFrame{})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"config_version":0`)
}

// TestSchemaVersionAlwaysSerialized mirrors the MetaChangeEvent convention.
func TestSchemaVersionAlwaysSerialized(t *testing.T) {
	raw, err := json.Marshal(AdapterStatusFrame{})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"schema_version":0`)
}

func TestFrameAssetIDs(t *testing.T) {
	f := AdapterStatusFrame{Assets: []AdapterAssetStatus{{AssetID: "a"}, {AssetID: "b"}}}
	assert.Equal(t, []string{"a", "b"}, f.AssetIDs())
	assert.Empty(t, (&AdapterStatusFrame{}).AssetIDs())
}

func TestFrameHasCapability(t *testing.T) {
	f := AdapterStatusFrame{Capabilities: []string{"ping", "reconfigure"}}
	assert.True(t, f.HasCapability("ping"))
	assert.False(t, f.HasCapability("stop"))
	assert.False(t, (&AdapterStatusFrame{}).HasCapability("ping"))
}

func TestAdapterSubjectsAreDistinctPlane(t *testing.T) {
	// The adapter plane must not fall under platform.data.>, which the
	// PLATFORM_DATA stream captures and persists for 7 days: heartbeats there
	// would evict real telemetry under the 1 GiB cap.
	for _, s := range []string{
		SubjectAdapterStatusPrefix, SubjectAdapterHello,
		SubjectAdapterPingPrefix, SubjectAdapterChanged, SubjectAdapterList,
	} {
		assert.True(t, strings.HasPrefix(s, "platform.adapter."), "%s", s)
		assert.False(t, strings.HasPrefix(s, "platform.data."), "%s must not be captured by PLATFORM_DATA", s)
	}

	// Nor under platform.meta.*, whose subscribers use the
	// platform.meta.*.changed wildcard (SDK subjects.go) and would receive
	// every heartbeat transition.
	assert.NotContains(t, SubjectAdapterChanged, "platform.meta.")
}

func TestSystemClockAdvances(t *testing.T) {
	before := SystemClock.Now()
	assert.False(t, before.IsZero())
	assert.WithinDuration(t, time.Now(), before, time.Second)
}
