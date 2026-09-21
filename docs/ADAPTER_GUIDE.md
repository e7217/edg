# EDG Adapter Guide

This guide captures conventions for adapters that register or update metadata in EDG Core.

## SDKs

EDG ships two SDKs that wrap the NATS subject contract described below:

- **Python** — [`adapters/python/sdk`](../adapters/python/sdk)
- **Go** — [`adapters/go/sdk`](../adapters/go/sdk)

Both SDKs cover the same surface (asset data publish, asset and relation CRUD, metadata change event subscription, device connect/reconnect hooks, and provisioning from a declared point list) and use the same wire format. Pick whichever fits your toolchain — adapters can also talk to the subjects directly without an SDK.

Four reference adapters are included, each of which can take what it reads from
master data instead of a local file ([ADR 0011](adr/0011-point-distribution.md)):

| Protocol | Example | Language |
| --- | --- | --- |
| Modbus TCP | [`modbus_tcp_sensor`](../adapters/go/sdk/examples/modbus_tcp_sensor), [`modbus_tcp`](../adapters/python/examples/modbus_tcp) | Go, Python |
| Modbus RTU (serial) | [`modbus_tcp_sensor`](../adapters/go/sdk/examples/modbus_tcp_sensor) with `transport: rtu` | Go |
| OPC UA | [`opcua_sensor`](../adapters/go/sdk/examples/opcua_sensor) | Go |
| MELSEC MC (3E) | [`melsec_mc_sensor`](../adapters/go/sdk/examples/melsec_mc_sensor) | Go |

None has been run against physical hardware yet; they are tested against
simulators and, for MELSEC, against the published frame encoding.

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

### Registers from master data

Instead of listing `registers`, name the asset whose point list the adapter
should poll ([ADR 0011](adr/0011-point-distribution.md)). The device connection
stays local; the register map comes from EDG and follows it when it changes:

```yaml
host: 192.168.10.21      # this box's view of the device
port: 502
unit_id: 1
asset_id: pump-a         # poll the point list declared for pump-a
nats_url: nats://adapter:SECRET@edg-core:4222   # or EDG_NATS_URL
```

Each point maps to a register: `address` is the register number, and
`encoding` carries what a mapping row carries:

```yaml
# declared in EDG for pump-a (edg-core -import-points, or PUT /api/v1/assets/pump-a/points)
protocol: modbus-tcp
poll_interval_ms: 1000
points:
  - name: temperature
    value_type: NUMBER
    unit: "°C"
    address: "0"
    encoding: {function: holding, type: int16, scale: 0.1}
```

One invalid point rejects the whole list — a register map with a hole in it
polls successfully and says nothing about the hole. The adapter then keeps
heartbeating, collects nothing, logs why, and picks up the next version.

Your own adapter gets the same behaviour from the SDK:

```go
err := sdk.RunProvisioned(ctx, sdk.ProvisionedConfig{
    Adapter: sdk.AdapterConfig{AssetID: "pump-a", NATSURL: url},
}, func(pl *sdk.PointList) (sdk.Collector, error) {
    return newCollector(pl.EnabledPoints()) // called once per list version
})
```

```python
await run_provisioned("pump-a", lambda pl: MyAdapter(pl, asset_id="pump-a"), nats_url=url)
```

### Modbus RTU (serial)

The Go reference also speaks Modbus RTU over a serial line — RS-485 on most
plant floors. The register map and provisioning are the same; only the
connection changes:

```yaml
transport: rtu
serial:
  port: /dev/ttyUSB0     # COM3 on Windows
  baud_rate: 9600        # default 9600
  data_bits: 8           # default 8
  parity: E              # N | E | O, default E (the Modbus specification's)
  stop_bits: 1           # default 1
unit_id: 3
asset_id: kiln-1         # or list registers: as for TCP
```

Set `parity` and `stop_bits` to what the device's panel says: many ship `N` with
one stop bit, which the specification would pair with two. A point list for an
RTU adapter declares `protocol: modbus-rtu`; one declared `modbus-tcp` is
refused, so a list cannot be applied to the wrong kind of adapter. The Python
reference is TCP only.

Write function codes and several units on one adapter are intentionally out of scope for these references; copy the example and extend as needed.

## OPC UA Reference Adapter

[`adapters/go/sdk/examples/opcua_sensor`](../adapters/go/sdk/examples/opcua_sensor)
reads a set of variables from one OPC UA server in a single Read request per
poll, using [`gopcua/opcua`](https://github.com/gopcua/opcua) (MIT):

```yaml
endpoint: opc.tcp://192.168.10.30:4840
# username: operator      # omit for anonymous
# password: secret
poll_interval: 1.0
nodes:
  - name: temperature
    node_id: "ns=2;s=Temperature"
    unit: "°C"
```

Or, with `asset_id` and no `nodes`, from master data: each point's `address`
is its NodeId, and `encoding` is unused.

```yaml
# declared in EDG for press-01
protocol: opcua
points:
  - {name: temperature, value_type: NUMBER, unit: "°C", address: "ns=2;s=Temperature"}
  - {name: running,     value_type: FLAG,   address: "ns=2;s=Running"}
  - {name: state,       value_type: TEXT,   address: "ns=2;s=State"}
```

- **Types map to the three reading kinds**: integers, floats and DateTime (as
  epoch ms) → `number`; Boolean → `flag`; String and LocalizedText → `text`.
  Other types are skipped and logged.
- **Status maps to quality**: Good → `GOOD`, Uncertain → `UNCERTAIN`. A Bad
  node yields no value that poll and is logged — there is no reading to report.
- **A NodeId must be written in full** (`ns=2;s=Temperature`, `i=2258`). A bare
  number would parse as `ns=0;i=<n>`, which is nearly always a Modbus register
  pasted into the wrong list, so it is refused.
- **Security mode None only**, with anonymous or username login. Signing and
  encryption need certificates on both ends; extend `ConnectDevice` when a
  server requires them.

## MELSEC Reference Adapter

[`adapters/go/sdk/examples/melsec_mc_sensor`](../adapters/go/sdk/examples/melsec_mc_sensor)
reads Mitsubishi MELSEC PLCs (Q, L, iQ-R, and FX5 with MC protocol enabled)
over the **MC protocol, 3E frame, binary code**, on TCP. The protocol client is
part of the example — no third-party PLC library.

On the PLC, enable the MC protocol on the Ethernet port with communication data
code *binary*, and open a TCP port for it; that port goes in the config.

```yaml
host: 192.168.3.39
port: 5007
points:
  - {name: temperature, address: D100, type: int16, scale: 0.1, unit: "°C"}
  - {name: counter,     address: D200, type: int32}   # D200/D201, low word first
  - {name: running,     address: M100}                # a bit device reads as a flag
  - {name: alarm,       address: D300.4}              # bit 4 of a word
```

- **Devices**: D, W, R, ZR (words) and M, L, B, X, Y (bits). X, Y, B and W are
  numbered in **hexadecimal**, as on the PLC (`X1F`), the rest in decimal.
- **Types**: `int16` (default), `uint16`, `int32`, `uint32`, `float32` — a
  32-bit value is the device and the next one, low word first, which is how
  MELSEC stores DINT and REAL — and `bit`.
- **A refused device** (the PLC's end code, e.g. out of range) skips that point
  and is logged; the rest of the poll is published. A transport error
  reconnects.
- **From master data** with `asset_id` and no `points`: a point's `address` is
  the device and `encoding` carries `type` and `scale`; the list's protocol is
  `melsec-mc`.

## Metadata Change Events

Subscribe to `platform.meta.*.changed` to react to asset and relation metadata changes. EDG Core publishes these events after successful store mutations only; failed create, update, or delete requests do not emit events.

Events are best-effort plain NATS messages. On adapter startup, first request the current asset list through `platform.meta.asset.list`, then apply `platform.meta.asset.changed` and `platform.meta.relation.changed` events for incremental updates. `platform.meta.points.changed` announces a replaced or deleted point list for one asset.

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

**`adapter_id` must be a single NATS subject token**: letters, digits, `_`, `-`
and `:`, at most 64 characters, **starting with a letter or digit**, and not one
of the reserved names `drift`, `list` or `hello` (they collide with sibling
subjects and HTTP routes). It is the last token of the status subject and is
authoritative for identity, so a frame claiming a different id in its body is
rejected.

If you leave it empty it defaults to `asset_id`, which has no such constraint.
Both SDKs sanitize an invalid id deterministically (invalid characters become
`-`) and log a warning naming the substitution — an asset called `line3.press`
reports as `line3-press`. Set `adapter_id` explicitly if you would rather
choose it yourself.

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
