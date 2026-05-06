# EDG Adapter Guide

This guide captures conventions for adapters that register or update metadata in EDG Core.

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

## Metadata Change Events

Subscribe to `platform.meta.*.changed` to react to asset and relation metadata changes. EDG Core publishes these events after successful store mutations only; failed create, update, or delete requests do not emit events.

Events are best-effort plain NATS messages. On adapter startup, first request the current asset list through `platform.meta.asset.list`, then apply `platform.meta.asset.changed` and `platform.meta.relation.changed` events for incremental updates.

See [Metadata Events](events.md) for the payload schema and examples.
