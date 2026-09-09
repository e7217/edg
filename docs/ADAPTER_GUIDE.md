# EDG Adapter Guide

This guide captures conventions for adapters that register or update metadata in EDG Core.

## SDKs

EDG ships two SDKs that wrap the NATS subject contract described below:

- **Python** — [`adapters/python/sdk`](../adapters/python/sdk)
- **Go** — [`adapters/go/sdk`](../adapters/go/sdk)

Both SDKs cover the same surface (asset data publish, asset and relation CRUD, metadata change event subscription, device connect/reconnect hooks) and use the same wire format. Pick whichever fits your toolchain — adapters can also talk to the subjects directly without an SDK.

## Connecting

Credentials go in the NATS URL. Neither SDK has credential parameters, because
the URL already carries them and the generated secrets are URL-safe:

```
nats://adapter:<secret>@edg-core:4222
```

```go
sdk.NewAdapter(sdk.AdapterConfig{AssetID: "sensor-001", NATSURL: os.Getenv("EDG_NATS_URL")})
```

```python
BaseAdapter(asset_id="sensor-001", nats_url=os.environ["EDG_NATS_URL"])
```

Use the `adapter` role. It may publish `platform.data.asset` and
`platform.alarm.raised` and **read** master data, but it may not create, update
or delete assets and relations — master data is declared explicitly through the
HTTP write API, not as a side effect of data flow. See
[ADR 0007](adr/0007-nats-subject-authorization.md) for the full matrix.

If a call is refused, both SDKs raise a typed error naming the subject
(`ErrForbidden` in Go, `ForbiddenError` in Python) instead of letting the
request time out with no explanation. A denied **subscription** cannot be
reported synchronously — NATS delivers that refusal asynchronously — so it is
logged as `nats permission denied` with the subject; if an adapter receives no
metadata events at all, check the log for that line.

Neither SDK logs the URL verbatim; the password is redacted.

## Asset Source Values

Set `source` to the system that supplied the asset metadata.

- `manual`: explicit user or operator metadata creation
- `auto`: EDG Core data-plane auto-registration
- Adapter names: use a short lower-case name such as `aas`, `opcua`, `modbus`, `mqtt-sparkplug`, `mes`, or `erp`

Core validates only that `source` is non-empty. Adapters should keep names stable because downstream filters can use `source` to select assets from a specific integration.

## External IDs

Store external identifiers in `external_ids` as string key/value pairs. Common keys include:

- `irdi`
- `eclass`
- `aas`
- `opcua_node_id`
- `erp_asset_id`

## Attributes

Use `attributes` for adapter-specific metadata that does not need indexed querying yet. Keep values as strings and avoid embedding large nested JSON payloads.

## Modbus TCP Reference Adapter

A working Modbus TCP adapter is provided in both languages for vendors that expose holding/input registers over plain TCP:

- Python: [`adapters/python/examples/modbus_tcp`](../adapters/python/examples/modbus_tcp)
- Go: [`adapters/go/sdk/examples/modbus_tcp_sensor`](../adapters/go/sdk/examples/modbus_tcp_sensor)

Both implementations read a YAML mapping file at startup and translate each row into a `TagValue`. The mapping schema is shared between the two implementations:

```yaml
version: 1
host: 127.0.0.1
port: 502
unit_id: 1
poll_interval: 1.0    # seconds between collect cycles
timeout: 1.0          # per-request timeout in seconds

registers:
  - name: temperature
    function: holding   # holding | input
    address: 0
    type: int16         # uint16 | int16 | uint32 | int32 | float32
    scale: 0.1          # raw * scale
    unit: "°C"
  - name: flow_rate
    function: input
    address: 100
    type: float32
    word_order: ABCD    # ABCD | CDAB | BADC | DCBA
    unit: "L/min"
```

`word_order` follows the convention printed in most PLC vendor manuals: `ABCD` is normal big-endian, `CDAB` is the common "word-swap" variant (Modicon-style), `BADC` swaps bytes inside each word, and `DCBA` swaps both. For 16-bit types `word_order` is ignored. Set `source: "modbus"` on assets created from a Modbus adapter so downstream filters stay consistent.

The reference adapters delegate the wire-level protocol work to permissively licensed third-party libraries:

- Python: [`pymodbus`](https://github.com/pymodbus-dev/pymodbus) (BSD-3-Clause)
- Go: [`goburrow/modbus`](https://github.com/goburrow/modbus) (BSD-3-Clause)

Modbus RTU (serial), write function codes, and multi-unit deployments are intentionally out of scope for these references; copy the example and extend as needed.

## Metadata Change Events

Subscribe to `platform.meta.*.changed` to react to asset and relation metadata changes. EDG Core publishes these events after successful store mutations only; failed create, update, or delete requests do not emit events.

Events are best-effort plain NATS messages. On adapter startup, first request the current asset list through `platform.meta.asset.list`, then apply `platform.meta.asset.changed` and `platform.meta.relation.changed` events for incremental updates.

See [Metadata Events](events.md) for the payload schema and examples.


## Runtime Status

Both SDKs publish adapter runtime status automatically
([ADR 0008](adr/0008-adapter-runtime-status.md)). An adapter that implements
only `Collect` reports without any code change — reporting is on by default,
because an adapter nobody can see is the problem this plane exists to solve.

```go
sdk.NewAdapter(sdk.AdapterConfig{
    AssetID:           "sensor-001",
    AdapterID:         "modbus-line3",      // optional; defaults to AssetID
    AdapterVersion:    "modbus-tcp/1.2.0",  // optional, shown in the UI
    HeartbeatInterval: 10 * time.Second,    // optional, defaults to 10s
}, collector)
```

```python
BaseAdapter(
    asset_id="sensor-001",
    adapter_id="modbus-line3",
    adapter_version="modbus-tcp/1.2.0",
    heartbeat_interval=10.0,
)
```

**`adapter_id` must be a single NATS subject token** — letters, digits, `_`,
`-`, `:`, at most 64 characters, no dots. It is the last token of the status
subject and is authoritative for identity, so a frame that claims a different id
in its body is rejected.

**The heartbeat interval is yours to choose.** It is announced in band and core
derives its staleness deadline from it (three times the interval, with a floor),
so a 15-minute batch collector is not declared dead for being quiet.

Three things are reported independently:

| | Meaning |
| --- | --- |
| `run_state` | The adapter process: `running`, or `degraded` after three consecutive collect failures |
| `device_state` | The link to the equipment, using the SDK's existing states |
| `availability` | Whether core can still hear you. **Derived by core; adapters cannot assert it.** |

`degraded` with `device_state: connected` is a real and common combination: a
PLC that answers but returns garbage.

**Answer the probe.** When a deadline passes, core sends a request to
`platform.adapter.ping.<adapter_id>` before declaring you stale. Both SDKs reply
automatically; an adapter talking to NATS directly should do the same, or it
will be reported stale after every missed heartbeat.

`host` and `pid` are opt-in (`ReportHost` / `report_host=True`): adapter
inventory is more sensitive than the asset list.

Set `DisableStatusReporting` / `disable_status_reporting=True` to opt out
entirely.
