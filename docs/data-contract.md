# EDG Validated Data Contract

`platform.data.validated` is the public contract for asset data that EDG Core
has accepted and persisted to JetStream. The built-in VictoriaMetrics sink
([ADR 0005](adr/0005-embedded-vm-sink.md)) is **one** consumer of this subject;
third parties may attach their own consumers to fan data out to other
destinations. This contract and its compatibility rules are governed by
[ADR 0006](adr/0006-validated-data-contract.md).

For metadata change notifications (asset/relation create/update/delete), see the
separate [events contract](events.md).

## Subjects

| Subject | Consume for | Delivery |
| --- | --- | --- |
| `platform.data.asset` | Nothing — this is raw adapter input into core. | At-most-once plain NATS. Do **not** treat as storage. |
| `platform.data.validated` | **This contract.** Core-accepted, enriched asset data. | JetStream. Attach a **durable** consumer for at-least-once + backlog replay. |
| `platform.data.deadletter` | Operational alerting on persistence failures. | JetStream, best-effort envelope (see below). |

`platform.data.validated` is stored in the `PLATFORM_DATA` stream
(subjects `platform.data.>`, file storage, `retention: limits`, 7 d / 1 GiB,
`DiscardOld`, 1 replica — see [ADR 0001](adr/0001-data-plane-reliability.md)).

## Payload (schema v1)

The message body is a JSON object. The current schema is **v1**; v1 carries no
`schema_version` field, and its **absence denotes v1**.

```json
{
  "asset_id": "sensor-001",
  "timestamp": 1715083200000,
  "values": [
    { "name": "temperature", "number": 21.5, "unit": "C", "quality": "good" },
    { "name": "status", "text": "running", "quality": "good" },
    { "name": "alarm", "flag": false, "quality": "good" }
  ],
  "metadata": {
    "Building": "HQ",
    "Floor": "3F"
  }
}
```

### Top-level fields

- `asset_id` (string, required): identifier of the source asset.
- `timestamp` (int64, optional): event time in **epoch milliseconds**. When `0`
  or omitted, the built-in sink uses server-receive time. Both the Go and Python
  adapter SDKs populate milliseconds.
- `values` (array, required): one or more tag readings (below).
- `metadata` (object of string→string, optional): ontology enrichment tags added
  by core ([ADR 0002](adr/0002-ontology-enrichment.md)). Each key is an
  ancestor's `template_name`; each value is that ancestor asset's name. Keys are
  intentionally **low-cardinality** and are **only added, never overwritten**.
  May be absent when the asset has no enriched ancestors, or if enrichment
  failed (in which case the un-enriched payload is published).

### `values[]` fields

- `name` (string, required): tag name.
- Adapters set exactly one value field per reading. Core does **not** enforce
  this, so a consumer should read the value fields independently rather than
  assume mutual exclusion:
  - `number` (float) — numeric reading. **This is the only field the built-in VM
    sink stores** (as field `number`); `text`/`flag` are skipped by that sink but
    remain in the JSON for other consumers.
  - `text` (string) — textual reading.
  - `flag` (bool) — boolean reading.
- `unit` (string, optional): unit of measure for `number`.
- `quality` (string, required): reading quality (e.g. `good`).

Consumers MUST ignore unknown fields and treat `metadata` keys as an open set
(see *Versioning* below).

## Delivery and durability

| Integration | How it attaches | Guarantee |
| --- | --- | --- |
| Built-in VM sink | Durable pull consumer `edg-core-vm-sink`, acks after a `2xx` write. | At-least-once into storage. |
| External router (Benthos/Redpanda Connect, Vector) | Its **own** durable consumer with explicit ack. | At-least-once; independent of and non-blocking to the VM sink. |
| Core push (ADR 0006 option B, future) | Core owns the durable consumer and pushes to a local listener input. | At-least-once up to the receiver. |
| Plain core-NATS subscription (e.g. Telegraf direct) | `nats_consumer`-style live subscription. | **Best-effort only** — see the caveat below. |

Because the stream uses `limits` retention, independent consumers do not block
one another, but a consumer offline **longer than the 7 d / 1 GiB window loses
data**. At-least-once can also produce **duplicate** deliveries on retry, so
downstream writes should be idempotent (VictoriaMetrics is idempotent for an
identical metric/label-set/timestamp).

> ⚠️ **Telegraf caveat.** Telegraf's `nats_consumer` input only supports plain
> core-NATS subscriptions, so it **cannot be a durable JetStream consumer**.
> Subscribing it directly to `platform.data.validated` means messages published
> while Telegraf is down are missed and the backlog is **not** replayed —
> reopening the durability gap that [ADR 0005](adr/0005-embedded-vm-sink.md)
> closed. To use Telegraf, front it with a durable router (option A) or push to a
> Telegraf listener input from core (option B). Do not subscribe Telegraf
> directly for storage.

## Attaching a consumer

### Inspect with the NATS CLI

```bash
# durable pull consumer over the validated subject
nats consumer add PLATFORM_DATA my-fanout \
  --filter platform.data.validated --pull --ack explicit --deliver all
nats consumer next PLATFORM_DATA my-fanout --count 100 --ack
```

### Fan out with Benthos / Redpanda Connect (ADR 0006 option A)

The validated JSON is consumed as-is and fanned out to arbitrary destinations.
Core is not modified.

```yaml
input:
  nats_jetstream:
    urls: ["nats://edg-core:4222"]
    subject: "platform.data.validated"
    durable: "benthos-fanout"      # durable consumer = at-least-once + replay
    deliver: all
output:
  broker:
    pattern: fan_out
    outputs:
      - kafka:  { addresses: ["kafka:9092"], topic: "edg.validated" }
      - aws_s3: { bucket: "edg-archive", path: '${! now().ts_unix() }.json' }
      - mqtt:   { urls: ["tcp://broker:1883"], topic: "edg/data" }
```

[Vector](https://vector.dev) and other tools with a durable JetStream source
work the same way. Telegraf does **not** (see the caveat above).

## Dead-letter envelope

When core cannot publish to `platform.data.validated`, it attempts to write a
best-effort envelope to `platform.data.deadletter`:

```json
{
  "original_subject": "platform.data.asset",
  "target_subject": "platform.data.validated",
  "error": "context deadline exceeded",
  "payload": { "asset_id": "sensor-001", "...": "raw original message bytes" },
  "timestamp": "2026-05-07T12:34:56Z"
}
```

- `original_subject`: subject the raw message arrived on.
- `target_subject`: subject the failed publish targeted.
- `error`: publish error string.
- `payload`: the raw original message bytes (pre-enrichment).
- `timestamp`: RFC 3339 UTC time of the failure.

Watch the `edg_core_jetstream_dead_letters` and
`edg_core_jetstream_dead_letter_failures` expvar counters
([ADR 0001](adr/0001-data-plane-reliability.md)) to detect data-loss pressure.

## Versioning and compatibility

- **Additive changes** — new optional top-level/`values[]` fields, new `metadata`
  keys — are **non-breaking** and do not bump the schema. Consumers must ignore
  unknown fields.
- **Breaking changes** — removing or renaming a field, changing a field's type,
  or changing a field's meaning — bump `schema_version` and ship a note in
  [MIGRATION.md](MIGRATION.md).
- Treat the absence of `schema_version` as `1`.

## Cardinality

The built-in sink turns each `metadata` key into a VictoriaMetrics **label**, so
high-cardinality keys inflate series count. The v1 enrichment rule deliberately
uses stable `template_name` keys (not asset IDs or display names). External
consumers receive the same `metadata` and should apply their own
cardinality/cost controls. Inspect series growth with VictoriaMetrics' built-in
**vmui** cardinality explorer at `:8428/vmui`.
