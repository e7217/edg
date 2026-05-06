# EDG Metadata Events

EDG Core publishes best-effort NATS events after metadata mutations are stored.
Subscribers should use these events as change notifications and reconcile current
state with metadata request subjects when they start.

## Subjects

| Subject | Entity | When |
| --- | --- | --- |
| `platform.meta.asset.changed` | Asset | Asset create, update, delete, and data-plane auto-registration |
| `platform.meta.relation.changed` | Asset relation | Relation create and delete |

These are plain NATS publishes, not JetStream writes. A subscriber that is not
connected can miss events.

## Payload

```json
{
  "schema_version": 1,
  "event_type": "created",
  "entity_type": "asset",
  "entity_id": "sensor-001",
  "source": "auto",
  "timestamp": "2026-05-07T12:34:56Z",
  "after": {
    "id": "sensor-001",
    "name": "sensor-001",
    "source": "auto",
    "created_at": "2026-05-07T12:34:56Z",
    "updated_at": "2026-05-07T12:34:56Z"
  }
}
```

Fields:

- `schema_version`: event schema version. Current value is `1`.
- `event_type`: `created`, `updated`, or `deleted`.
- `entity_type`: `asset` or `relation`.
- `entity_id`: asset ID or relation ID.
- `source`: system that caused the change. Use `manual`, `auto`, or a stable adapter name.
- `timestamp`: publish timestamp from EDG Core.
- `before`: previous full object snapshot. Omitted for creates.
- `after`: new full object snapshot. Omitted for deletes.

## Subscribe

```bash
nats sub "platform.meta.*.changed"
```

Python:

```python
import asyncio
import json
import nats

async def main():
    nc = await nats.connect("nats://localhost:4222")

    async def changed(msg):
        event = json.loads(msg.data.decode())
        print(event["entity_type"], event["event_type"], event["entity_id"])

    await nc.subscribe("platform.meta.*.changed", cb=changed)
    await asyncio.Event().wait()

asyncio.run(main())
```

## Reconciliation

Events are best-effort notifications. Adapters should request
`platform.meta.asset.list` during startup, build their local view from that
response, then subscribe to `platform.meta.*.changed` for incremental updates.
