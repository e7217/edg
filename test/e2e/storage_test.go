//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// value mirrors the wire format of one reading.
type value struct {
	Name    string   `json:"name"`
	Number  *float64 `json:"number,omitempty"`
	Text    *string  `json:"text,omitempty"`
	Flag    *bool    `json:"flag,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Quality string   `json:"quality"`
}

func num(name string, v float64) value { return value{Name: name, Number: &v, Quality: "GOOD"} }
func txt(name, v string) value         { return value{Name: name, Text: &v, Quality: "GOOD"} }
func flg(name string, v bool) value    { return value{Name: name, Flag: &v, Quality: "GOOD"} }

func publish(t *testing.T, nc *nats.Conn, msg any) {
	t.Helper()
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, nc.Publish("platform.data.asset", b))
}

func message(asset string, ts int64, values ...value) map[string]any {
	return map[string]any{"asset_id": asset, "timestamp": ts, "values": values}
}

// seed declares a pump and a sensor that is partOf it, so enrichment derives
// equipment="Pump A" for the sensor.
func seed(c *core) {
	c.api("POST", "/api/v1/assets", map[string]any{"id": "pump-a", "name": "Pump A", "template_name": "equipment"})
	c.api("POST", "/api/v1/assets", map[string]any{"id": "sensor-1", "name": "Sensor 1", "template_name": "pump-sensor"})
	c.api("POST", "/api/v1/relations", map[string]any{
		"source_asset_id": "sensor-1", "target_asset_id": "pump-a", "relation_type": "partOf"})
}

func TestStorage(t *testing.T) {
	vm := startVictoria(t)
	c := newCore(t, vm.url())
	seed(c)
	nc := c.connect()

	// A base timestamp per subtest keeps their samples apart in time as well
	// as by asset, so a count is never polluted by a neighbour.
	base := time.Now().Add(-time.Hour).UnixMilli()

	// The five inputs of the 2026-09-14 review, and what each must become.
	t.Run("review cases", func(t *testing.T) {
		ts := base
		rejectedBefore := c.metric("edg_core_data_messages_rejected_total", "")
		mismatchBefore := c.metric("edg_core_data_contract_violations_total", `reason="type_mismatch"`)
		overrideBefore := c.metric("edg_core_enrich_metadata_overrides_total", "")

		publish(t, nc, message("sensor-1", ts, num("temperature", 25.5)))    // 1 normal
		publish(t, nc, map[string]any{})                                     // 2 empty object
		publish(t, nc, message("sensor-1", ts+1, txt("temperature", "25.5"), // 3 type mismatch...
			txt("state", "RUNNING"))) //   ...beside a valid value
		publish(t, nc, message("sensor-1", ts+2, flg("running", true))) // 4 boolean
		msg := message("sensor-1", ts+3, num("temperature", 26.0))      // 5 metadata conflict
		msg["metadata"] = map[string]string{"equipment": "Wrong Equipment"}
		publish(t, nc, msg)
		publish(t, nc, message("sensor-1", 1789393384, num("temperature", 1))) // seconds timestamp

		waitFor(t, "the review samples", 15*time.Second, func() bool {
			return vm.sampleCount(`{__name__=~"edg_data_.*",asset_id="sensor-1"}`) >= 4
		})
		time.Sleep(time.Second) // anything that should not arrive has had its chance

		temps := vm.export(`edg_data_number{asset_id="sensor-1",name="temperature"}`)
		require.Len(t, temps, 1, "one series: every sample carries master data's equipment")
		assert.Equal(t, "Pump A", temps[0].Metric["equipment"], "master data wins over adapter metadata")
		assert.Equal(t, "°C", temps[0].Metric["unit"], "the declared unit is filled in")
		assert.Equal(t, []float64{25.5, 26.0}, temps[0].Values)
		assert.Equal(t, []int64{ts, ts + 3}, temps[0].Timestamps)

		states := vm.export(`edg_data_text{asset_id="sensor-1",name="state"}`)
		require.Len(t, states, 1)
		assert.Equal(t, "RUNNING", states[0].Metric["value"])
		assert.Equal(t, []int64{ts + 1}, states[0].Timestamps, "the valid sibling of a dropped value is stored")

		flags := vm.export(`edg_data_flag{asset_id="sensor-1",name="running"}`)
		require.Len(t, flags, 1)
		assert.Equal(t, []float64{1}, flags[0].Values)

		assert.Zero(t, vm.sampleCount(`{__name__=~"edg_data_.*",asset_id=""}`))
		assert.Equal(t, rejectedBefore+2, c.metric("edg_core_data_messages_rejected_total", ""),
			"the empty object and the seconds timestamp")
		assert.Equal(t, mismatchBefore+1,
			c.metric("edg_core_data_contract_violations_total", `reason="type_mismatch"`))
		assert.Equal(t, overrideBefore+1, c.metric("edg_core_enrich_metadata_overrides_total", ""))
	})

	// Every published value is stored exactly once, with its value and time.
	t.Run("count and values", func(t *testing.T) {
		const n = 1000
		ts := base + 10_000
		for i := 0; i < n; i++ {
			publish(t, nc, message("volume-1", ts+int64(i),
				num("a", float64(i)), num("b", float64(i)*0.5), flg("c", i%2 == 0)))
		}
		require.NoError(t, nc.Flush())
		waitFor(t, fmt.Sprintf("%d samples", 3*n), 30*time.Second, func() bool {
			return vm.sampleCount(`{__name__=~"edg_data_.*",asset_id="volume-1"}`) >= 3*n
		})
		time.Sleep(time.Second)
		assertExactSeries(t, vm, `edg_data_number{asset_id="volume-1",name="a"}`, ts, n,
			func(i int) float64 { return float64(i) })
		assertExactSeries(t, vm, `edg_data_number{asset_id="volume-1",name="b"}`, ts, n,
			func(i int) float64 { return float64(i) * 0.5 })
		assertExactSeries(t, vm, `edg_data_flag{asset_id="volume-1",name="c"}`, ts, n,
			func(i int) float64 {
				if i%2 == 0 {
					return 1
				}
				return 0
			})
	})

	// Storage down: JetStream holds the data and the sink delivers it when
	// storage returns (ADR 0005).
	t.Run("storage outage", func(t *testing.T) {
		const n = 300
		ts := base + 20_000
		failuresBefore := c.metric("edg_core_sink_write_failures_total", "")
		vm.stop()
		for i := 0; i < n; i++ {
			publish(t, nc, message("outage-1", ts+int64(i), num("x", float64(i))))
		}
		require.NoError(t, nc.Flush())
		waitFor(t, "the sink to notice the outage", 15*time.Second, func() bool {
			return c.metric("edg_core_sink_write_failures_total", "") > failuresBefore
		})
		vm.start()
		waitFor(t, "the backlog to drain", 60*time.Second, func() bool {
			return vm.sampleCount(`edg_data_number{asset_id="outage-1"}`) >= n
		})
		time.Sleep(time.Second)
		assertExactSeries(t, vm, `edg_data_number{asset_id="outage-1",name="x"}`, ts, n,
			func(i int) float64 { return float64(i) })
	})

	// Storage down and the core restarted: the backlog is in the durable
	// consumer, not in memory, so a restart resumes it.
	t.Run("core restart with backlog", func(t *testing.T) {
		const n = 200
		ts := base + 30_000
		publishedBefore := c.metric("edg_core_jetstream_published_total", "")
		vm.stop()
		for i := 0; i < n; i++ {
			publish(t, nc, message("restart-1", ts+int64(i), num("x", float64(i))))
		}
		require.NoError(t, nc.Flush())
		waitFor(t, "the backlog to reach JetStream", 15*time.Second, func() bool {
			return c.metric("edg_core_jetstream_published_total", "") >= publishedBefore+n
		})
		nc.Close()
		c.stop()
		vm.start()
		c.start()
		nc = c.connect()
		// The bound is the point: a message the old process was handed but
		// never settled is redelivered after the sink's AckWait (twice
		// request_timeout, at least 5s). It used to be JetStream's default
		// 30s, which is what this subtest measured before the fix.
		waitFor(t, "the backlog to drain after restart", 15*time.Second, func() bool {
			return vm.sampleCount(`edg_data_number{asset_id="restart-1"}`) >= n
		})
		time.Sleep(time.Second)
		assertExactSeries(t, vm, `edg_data_number{asset_id="restart-1",name="x"}`, ts, n,
			func(i int) float64 { return float64(i) })
	})

	// At-least-once means a retry can deliver the same reading twice. It must
	// not be stored twice (ADR 0005 claims this; the deployment flag makes it
	// true).
	t.Run("redelivered reading is stored once", func(t *testing.T) {
		ts := base + 40_000
		m := message("dup-1", ts, num("x", 7))
		publish(t, nc, m)
		publish(t, nc, m)
		require.NoError(t, nc.Flush())
		waitFor(t, "the duplicate", 15*time.Second, func() bool {
			return c.metric("edg_core_sink_lines_written_total", "") > 0 &&
				vm.sampleCount(`edg_data_number{asset_id="dup-1"}`) >= 1
		})
		time.Sleep(2 * time.Second)
		assert.Equal(t, 1, vm.sampleCount(`edg_data_number{asset_id="dup-1"}`))
	})
}

// assertExactSeries checks that selector has exactly n samples at ts+i with
// want(i), i.e. nothing missing, nothing duplicated, nothing out of place.
func assertExactSeries(t *testing.T, vm *victoria, selector string, ts int64, n int, want func(int) float64) {
	t.Helper()
	got := vm.export(selector)
	require.Len(t, got, 1, selector)
	s := got[0]
	require.Len(t, s.Values, n, "%s: samples missing or duplicated", selector)
	for i := 0; i < n; i++ {
		if s.Timestamps[i] != ts+int64(i) || s.Values[i] != want(i) {
			t.Fatalf("%s: sample %d is (%d, %v), want (%d, %v)",
				selector, i, s.Timestamps[i], s.Values[i], ts+int64(i), want(i))
		}
	}
}
