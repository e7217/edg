# ADR 0006: Validated Data Contract and External Fan-out

## Status

Accepted (extends [ADR 0001](0001-data-plane-reliability.md) and
[ADR 0005](0005-embedded-vm-sink.md))

## Context

[ADR 0005](0005-embedded-vm-sink.md) replaced Telegraf with a built-in
VictoriaMetrics sink and stated that `platform.data.validated` "remains the
public contract", and that power users who need exotic fan-out (Kafka, S3, cloud
TSDBs) can run an external consumer such as Benthos/Redpanda Connect alongside
the built-in sink.

That "public contract" was never specified. In practice the payload is the
internal `AssetData` struct serialized as JSON (`internal/core/types.go`),
published by `DataHandler` (`internal/core/handler.go`) after ontology
enrichment ([ADR 0002](0002-ontology-enrichment.md)). There is no version field,
no documented schema, no stated delivery guarantee for third-party consumers,
and no policy for what counts as a breaking change. A consumer outside this
repository has nothing stable to build against.

A concrete near-term need motivates pinning this down: fanning validated data
out to **arbitrary destinations** (Kafka, S3, MQTT, cloud TSDBs) **without
building each backend into core**, which would bloat the single binary and
contradict the product's single-process promise.

Two facts shape the decision:

- **The stream is already a fan-out buffer.** `PLATFORM_DATA` uses
  `retention: limits` (7 d / 1 GiB, `DiscardOld`; see ADR 0001). Messages are
  retained by age/size regardless of consumer acks, so **multiple independent
  durable consumers can attach** and each tracks its own position without
  interfering with the built-in VM sink. The missing piece is a contract, not
  plumbing.
- **Telegraf cannot be a durable consumer.** As ADR 0005 noted, Telegraf's
  `nats_consumer` input only supports plain core-NATS subscriptions. Subscribing
  it directly to `platform.data.validated` reopens the exact at-least-once gap
  ADR 0005 closed: while Telegraf is down, live messages are missed and the
  JetStream backlog is not replayed.

## Decision

1. **`platform.data.validated` is a versioned public contract**, specified in
   [`docs/data-contract.md`](../data-contract.md). The current enriched
   `AssetData` JSON payload is **schema v1**. v1 carries **no** `schema_version`
   field; its **absence denotes v1**. A future backward-incompatible change MUST
   introduce an explicit `schema_version` and follow the compatibility policy
   below.

2. **External fan-out is a supported, opt-in pattern — not a core feature.**
   Core ships exactly one built-in sink (VictoriaMetrics, ADR 0005). Additional
   destinations are served by attaching consumers to the contract:

   | Option | Shape | Durability | Status |
   |---|---|---|---|
   | **A** (primary) | An external routing engine that supports durable JetStream consumers (Benthos/Redpanda Connect, Vector) attaches its **own durable consumer** and fans out. Core unchanged. | At-least-once, provided by the external durable consumer. | Recommended now. |
   | **B** (optional) | A future generic egress sink in core owns the durable consumer and **pushes** line protocol to a local listener input of a tool that cannot subscribe durably (e.g. Telegraf), acking after handoff. | At-least-once up to the receiver; preserves the ADR 0001/0005 boundary. | Deferred until a non-durable tool is mandatory. |
   | **C** (rejected) | Build backends (Kafka/S3/…) into core as first-class sinks. | n/a | Rejected: contradicts the single-binary promise; reconsider only if a backend must be first-class. |

3. **Durability tiers are explicit.** A durable pull/push consumer gets
   at-least-once with backlog replay. Option B gets at-least-once to the
   receiver. A plain core-NATS subscription (Telegraf-direct) is **best-effort
   only**. The contract doc states which tier a given integration provides, and
   documents the Telegraf-direct subscription as an anti-pattern.

4. **Compatibility policy.** Additive changes — new optional fields, new
   `metadata` keys — are **non-breaking** and require no version bump; consumers
   MUST ignore unknown fields and treat `metadata` keys as an open set.
   Removing or renaming a field, changing a field's type, or changing a field's
   meaning is **breaking** and requires a `schema_version` bump plus a migration
   note in [MIGRATION.md](../MIGRATION.md).

5. **Retention is the isolation boundary.** Because retention is `limits`, a
   consumer offline longer than the 7 d / 1 GiB window loses data — identical to
   the built-in sink. A destination that needs stronger isolation gets a
   dedicated stream or raised retention. This is an operator decision, not a
   core default.

### Alternatives considered

| Option | Why not |
|---|---|
| Re-publish to per-destination subjects inside core | Adds core code and a fan-out consumer to do what the stream already does. The `limits` stream is itself the fan-out point. |
| Default to `workqueue`/`interest` retention | `workqueue` delivers each message to a single consumer — it breaks fan-out. `interest` drops messages once all *registered* consumers ack, which is hostile to consumers that attach dynamically. `limits` is the correct policy and is already in place. |
| Add a `schema_version` field to the payload now | A code change with no breaking change to version yet. v1-by-absence keeps existing consumers (and the built-in sink) working untouched; the field is introduced only when the first breaking change needs it. |
| Recommend Telegraf as the primary fan-out engine | Its `nats_consumer` input cannot be a durable consumer, so it reopens the ADR 0005 durability gap. Durable-capable engines (Benthos/Redpanda Connect, Vector) are the right primary; Telegraf is served by option B. |

## Consequences

- [`docs/data-contract.md`](../data-contract.md) becomes the authority for
  third-party consumers, alongside [`docs/events.md`](../events.md) for the
  metadata-event contract. Changes to the validated payload are reviewed against
  the compatibility policy above.
- **No code change is required to support fan-out today.** The work is
  documentation. Option B, if it is ever needed, is a small `VMSink → Sink`
  interface extraction plus a generic egress sink — a separate decision and ADR.
- The Telegraf-direct-subscribe anti-pattern is written down so operators do not
  silently reopen the durability gap that ADR 0005 closed.
- Enrichment `metadata` becomes VictoriaMetrics labels in the built-in sink
  (ADR 0002 / ADR 0005). External consumers receive the same `metadata` and must
  apply their own cardinality and cost controls; the v1 enrichment rule keeps
  keys low-cardinality (stable `template_name`), but operators should still watch
  series growth via vmui's cardinality explorer.

## Validation

This ADR specifies a contract over **existing** behavior, so it introduces no
new tests. The contract document is checked against the implementation:

- Payload shape and field semantics against `internal/core/types.go`
  (`AssetData`, `TagValue`) and the publish path in `internal/core/handler.go`.
- `metadata` key/value rules against `internal/core/enricher.go` and ADR 0002.
- Timestamp precision (epoch milliseconds) against `internal/core/sink.go`.
- Stream subjects and `limits` retention defaults against
  `internal/core/config.go` and ADR 0001.
- The dead-letter envelope against `DeadLetterMessage` in
  `internal/core/handler.go`.

When option B is implemented, ADR-style validation (durable consumer + ack
boundary regression tests, mirroring ADR 0005) will accompany it.
