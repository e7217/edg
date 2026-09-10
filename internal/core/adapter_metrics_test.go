package core

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/metrics"
)

// metricAdapterFrame is a minimal valid status frame.
func metricAdapterFrame(id string, assets ...string) *AdapterStatusFrame {
	statuses := make([]AdapterAssetStatus, 0, len(assets))
	for _, a := range assets {
		statuses = append(statuses, AdapterAssetStatus{AssetID: a, DeviceState: "connected"})
	}
	return &AdapterStatusFrame{
		SchemaVersion:      AdapterSchemaVersion,
		AdapterID:          id,
		InstanceID:         "inst-" + id,
		Seq:                1,
		Phase:              AdapterPhaseHeartbeat,
		RunState:           RunStateRunning,
		DeviceState:        "connected",
		HeartbeatIntervalS: 10,
		Assets:             statuses,
	}
}

func gatherLines(t *testing.T, r *metrics.Registry, family string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(r.Gather()), "\n") {
		if strings.HasPrefix(line, family+"{") || strings.HasPrefix(line, family+" ") {
			out = append(out, line)
		}
	}
	return out
}

func TestAdapterMetricsThreeLayers(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{})
	now := time.Now()
	require.True(t, reg.Observe(metricAdapterFrame("modbus-1", "sensor-1"), "modbus-1", now))
	require.True(t, reg.Observe(metricAdapterFrame("opcua-1", "sensor-2", "sensor-3"), "opcua-1", now))

	r := metrics.NewRegistry()
	RegisterAdapterMetrics(r, reg, AdapterMetricsOptions{})
	out := string(r.Gather())

	assert.Contains(t, out, `edg_core_adapters{availability="online"} 2`)
	assert.Contains(t, out, `edg_core_adapters{availability="stale"} 0`,
		"a zero must be reported rather than the series disappearing")
	assert.Contains(t, out, `edg_core_adapters_by_run_state{run_state="running"} 2`)
	assert.Contains(t, out, `edg_core_adapter_up{adapter_id="modbus-1"} 1`)
	assert.Contains(t, out, `edg_core_adapter_up{adapter_id="opcua-1"} 1`)
	assert.Contains(t, out, `edg_core_adapter_assets{adapter_id="opcua-1"} 2`)
	assert.Contains(t, out, "edg_core_adapter_last_seen_timestamp_seconds{adapter_id=\"modbus-1\"}")
}

// adapter_up must follow the registry's staleness verdict, not the adapter's
// last reported run state. A crashed adapter never gets to publish "stopped",
// so a metric derived from the report would keep saying it is running.
func TestAdapterUpIsDerivedFromStalenessNotReportedState(t *testing.T) {
	clock := newFakeClock()
	reg := NewAdapterRegistry(AdapterRegistryOptions{
		Clock:       clock,
		StaleFloor:  time.Second,
		MinInterval: time.Second,
		MaxInterval: time.Minute,
		ProbeOnMiss: false,
	})
	require.True(t, reg.Observe(metricAdapterFrame("modbus-1", "sensor-1"), "modbus-1", clock.Now()))

	r := metrics.NewRegistry()
	RegisterAdapterMetrics(r, reg, AdapterMetricsOptions{})
	require.Contains(t, string(r.Gather()), `edg_core_adapter_up{adapter_id="modbus-1"} 1`)

	// The adapter stops reporting. Its last frame still said run_state=running.
	clock.Advance(time.Hour)
	reg.Sweep(clock.Now())

	out := string(r.Gather())
	assert.Contains(t, out, `edg_core_adapter_up{adapter_id="modbus-1"} 0`,
		"a silent adapter is still reported as up")
	assert.Contains(t, out, `edg_core_adapters_by_run_state{run_state="running"} 1`,
		"run_state is the adapter's own claim and must not be rewritten")
}

// The cap keeps a fleet of ephemeral adapter ids from becoming a fleet of
// series, and the overflow bucket keeps the fact visible.
func TestAdapterMetricsCap(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{})
	now := time.Now()
	const fleet = 50
	for i := 0; i < fleet; i++ {
		id := fmt.Sprintf("adapter-%02d", i)
		require.True(t, reg.Observe(metricAdapterFrame(id, "sensor-1"), id, now))
	}

	r := metrics.NewRegistry()
	before := adapterDroppedSeries.With("max_tracked").Value()
	RegisterAdapterMetrics(r, reg, AdapterMetricsOptions{MaxTracked: 10})

	lines := gatherLines(t, r, "edg_core_adapter_up")
	assert.Len(t, lines, 11, "10 tracked adapters plus the overflow bucket")
	assert.Contains(t, strings.Join(lines, "\n"),
		fmt.Sprintf(`edg_core_adapter_up{adapter_id="%s"} %d`, adapterOverflowID, fleet-10))
	assert.Greater(t, adapterDroppedSeries.With("max_tracked").Value(), before,
		"folding adapters into the overflow bucket must be counted")

	// The aggregate still counts every adapter: the cap limits series, not
	// truth.
	assert.Contains(t, string(r.Gather()),
		fmt.Sprintf(`edg_core_adapters{availability="online"} %d`, fleet))
}

// A negative cap turns off per-adapter series entirely, for a deployment that
// wants the aggregates and nothing else.
func TestAdapterMetricsPerAdapterCanBeDisabled(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{})
	require.True(t, reg.Observe(metricAdapterFrame("modbus-1", "sensor-1"), "modbus-1", time.Now()))

	r := metrics.NewRegistry()
	RegisterAdapterMetrics(r, reg, AdapterMetricsOptions{MaxTracked: -1})
	out := string(r.Gather())

	assert.NotContains(t, out, "edg_core_adapter_up")
	assert.Contains(t, out, `edg_core_adapters{availability="online"} 1`)
}

// The adapter families must not multiply with the number of assets: asset ids
// stay in the API payload, never in a label.
func TestAdapterMetricsDoNotCarryAssetIDs(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{})
	assets := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		assets = append(assets, fmt.Sprintf("plant-a-sensor-%d", i))
	}
	require.True(t, reg.Observe(metricAdapterFrame("modbus-1", assets...), "modbus-1", time.Now()))

	r := metrics.NewRegistry()
	RegisterAdapterMetrics(r, reg, AdapterMetricsOptions{})
	out := string(r.Gather())

	assert.NotContains(t, out, "plant-a-sensor-", "an asset id reached a label")
	assert.Contains(t, out, `edg_core_adapter_assets{adapter_id="modbus-1"} 300`,
		"the asset count is the right way to expose this")
}

// Two adapters claiming the same asset is the drift signal ADR 0008 defines.
func TestAdapterDriftMetric(t *testing.T) {
	reg := NewAdapterRegistry(AdapterRegistryOptions{})
	now := time.Now()
	require.True(t, reg.Observe(metricAdapterFrame("modbus-1", "sensor-1"), "modbus-1", now))

	r := metrics.NewRegistry()
	RegisterAdapterMetrics(r, reg, AdapterMetricsOptions{})
	assert.Contains(t, string(r.Gather()), "edg_core_adapter_drift_issues 0")

	require.True(t, reg.Observe(metricAdapterFrame("opcua-1", "sensor-1"), "opcua-1", now))
	assert.Contains(t, string(r.Gather()), "edg_core_adapter_drift_issues 1",
		"an asset claimed by two adapters was not reported as drift")
}
