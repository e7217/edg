# EDG Adapter Guide

This guide captures conventions for adapters that register or update metadata in EDG Core.

## SDKs

EDG ships two SDKs that wrap the NATS subject contract described below:

- **Python** — [`adapters/python/sdk`](../adapters/python/sdk)
- **Go** — [`adapters/go/sdk`](../adapters/go/sdk)

Both SDKs cover the same surface (asset data publish, asset and relation CRUD, metadata change event subscription, device connect/reconnect hooks) and use the same wire format. Pick whichever fits your toolchain — adapters can also talk to the subjects directly without an SDK.

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
