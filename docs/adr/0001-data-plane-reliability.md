# ADR 0001: Data Plane Reliability

## Status

Accepted (amended by [ADR 0005](0005-embedded-vm-sink.md): the storage consumer
is now a built-in durable sink in core, not Telegraf)

## Context

EDG advertises reliable edge ingestion, but the current data plane has multiple
hops with different delivery semantics. The adapter-to-core hop uses plain NATS
pub/sub. The core-to-storage hop uses NATS JetStream. A durable consumer then
reads the validated stream and writes to VictoriaMetrics.

The reliability claim must be explicit enough for operators and adapter authors
to know where EDG provides persistence and where callers still need retry or
buffering.

## Decision

EDG defines "at-least-once delivery" as starting after the core receives a
successful JetStream publish acknowledgement for `platform.data.validated`.

The default stream policy is:

| Field | Value |
|---|---|
| Stream | `PLATFORM_DATA` |
| Subjects | `platform.data.>` |
| Storage | `file` |
| Retention | `limits` |
| Max age | `168h` / 7 days |
| Max bytes | `1073741824` / 1 GiB |
| Replicas | `1` |
| Discard | `old` |

The subject model is:

| Subject | Meaning |
|---|---|
| `platform.data.asset` | Raw adapter input received by core. |
| `platform.data.validated` | Core-accepted payload published with JetStream ack. |
| `platform.data.deadletter` | Publish failure envelope for data that could not be persisted to the validated subject. |

The hop model is:

| Hop | Delivery model | Responsibility |
|---|---|---|
| Adapter to core | At-most-once NATS pub/sub | Adapters retry or buffer when stronger guarantees are needed. |
| Core to JetStream | Publish ack required | Core records publish failures and attempts dead-letter publication. |
| JetStream to storage | Durable pull consumer | The built-in VM sink (ADR 0005) acks only after a successful VictoriaMetrics write, and replays the backlog after downtime. |

The core exposes these counters on both `/debug/vars` and `/metrics`
(see [ADR 0009](0009-prometheus-metrics.md)); the two names are the same
counter, and neither surface is going away:

| `expvar` counter | Prometheus name | Meaning |
|---|---|---|
| `edg_core_jetstream_publish_failures` | `edg_core_jetstream_publish_failures_total` | Validated publish attempts that failed. |
| `edg_core_jetstream_dead_letters` | `edg_core_jetstream_dead_letters_total` | Failed validated publishes successfully written to the dead-letter subject. |
| `edg_core_jetstream_dead_letter_failures` | `edg_core_jetstream_dead_letter_failures_total{stage}` | Dead-letter encoding or publish failures. |

## Consequences

Operators can size JetStream storage by changing `jetstream.stream.max_bytes` in
the core configuration. When the stream reaches the byte limit, JetStream uses
`DiscardOld`, so the oldest retained messages are removed first. Operators must
monitor stream state and the dead-letter counters to detect data loss pressure.
[ADR 0009](0009-prometheus-metrics.md) is how: `edg_core_js_bytes` against
`edg_core_js_stream_max_bytes` for stream pressure, and
`edg_core_sink_consumer_pending` for whether the sink is keeping up with it.

Single-node deployments use one replica. Multi-node replication is out of scope
for this decision and requires a later ADR because it changes deployment,
storage, and upgrade requirements.

Adapter authors must not treat a successful plain NATS publish as durable
storage. Adapters that need end-to-end acknowledgement should use a later
request/reply or SDK-level acknowledgement pattern.

## Validation

The core test suite includes regression coverage for:

- JetStream backlog recovery after a consumer attaches.
- `DiscardOld` behavior under small `MaxBytes` pressure.
- Concurrent auto-registration of the same asset.
- Dead-letter publication when the validated publish ack fails.

