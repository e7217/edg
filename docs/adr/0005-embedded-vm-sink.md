# ADR 0005: Built-in VictoriaMetrics Sink

## Status

Accepted (amends [ADR 0001](0001-data-plane-reliability.md))

## Context

EDG positions itself as a single Go binary with embedded NATS and SQLite that
needs no external services to run a node. Storage, however, depended on
**Telegraf**: a separate ~150 MB agent whose entire job (per the former
`deploy/configs/telegraf/telegraf.conf`) was to subscribe to
`platform.data.validated`, parse the JSON, and write the InfluxDB line protocol
to VictoriaMetrics. Shipping it meant a binary, a TOML config, a systemd unit, a
Dockerfile, a Docker image, and a release-pipeline step — all to move bytes from
one local subject to one local HTTP endpoint.

The Telegraf hop also contradicted ADR 0001. That ADR defines the
`JetStream -> Telegraf` hop as a **durable consumer** that acknowledges after a
successful downstream write. The actual `telegraf.conf` used a `queue_group`
plain core-NATS subscription with no durable consumer. If Telegraf was down,
messages published to `platform.data.validated` were retained in JetStream but
were **not replayed** — a plain subscriber only sees live traffic. The
"at-least-once into storage" guarantee silently degraded to best-effort at this
hop.

## Decision

Replace Telegraf with a sink built into `edg-core`.

- A **durable JetStream pull consumer** (default name `edg-core-vm-sink`) reads
  `platform.data.validated`.
- Each batch is encoded as InfluxDB line protocol and POSTed to a
  VictoriaMetrics-compatible `/write` endpoint with `precision=ms`.
- Messages are acknowledged **only after** a `2xx` response; a failed write
  `Nak`s the batch for redelivery. This makes the ADR 0001 boundary real.

The sink is configured under a new `sink:` block:

| Field | Default | Meaning |
|---|---|---|
| `enabled` | `true` | Run the sink. When `false`, `platform.data.validated` is still published for any external consumer. |
| `url` | `http://localhost:8428` | VictoriaMetrics base URL. Overridable with the `EDG_SINK_URL` env var (used for container service names). |
| `consumer_name` | `edg-core-vm-sink` | Durable pull-consumer name. |
| `measurement` | `edg_data` | Line-protocol measurement name. |
| `batch_max_size` | `500` | Max messages fetched per write. |
| `flush_interval` | `1s` | Max wait before flushing a partial batch. |
| `request_timeout` | `5s` | HTTP write timeout. |

Wire mapping (one line per numeric value):

```
edg_data,asset_id=<id>,name=<name>,unit=<unit>,quality=<quality>[,<meta>=<v>...] number=<value> <ts_ms>
```

- Tags: `asset_id`, `values[].name/unit/quality`, plus enrichment `metadata`
  keys. Empty tag values are skipped.
- Field: `number` (text/flag values are not stored, matching prior behaviour).
- Timestamp: `AssetData.Timestamp`, which both the Go and Python SDKs populate
  in **epoch milliseconds**; omitted (server time used) when zero.

The metric name in VictoriaMetrics therefore becomes `edg_data_number`,
replacing the former `nats_consumer_number` (a name that leaked the Telegraf
input plugin into the data schema).

Grafana is reclassified as an **optional, central** layer behind a Docker
Compose `grafana` profile. Day-to-day node inspection uses VictoriaMetrics'
built-in **vmui** at `:8428/vmui`, which adds no extra process and includes a
cardinality explorer.

### Alternatives considered

| Option | Why not |
|---|---|
| Keep Telegraf, fix the durable setting | Telegraf's `nats_consumer` input only supports plain subscriptions; the durable boundary cannot be satisfied by configuration. The 150 MB footprint remains. |
| Separate `edg sink` process | Better fault isolation, but contradicts the "one process" goal; isolation is already provided by the JetStream buffer (7 d / 1 GiB, DiscardOld). |
| Prometheus remote_write | Adds protobuf + snappy dependencies and is VM-specific; line protocol is also accepted by InfluxDB/Timescale, preserving the "repoint the URL" flexibility for free. |
| Keep `nats_consumer_number` metric name | Pins a tool name into the schema permanently. Pre-alpha + Grafana's `$metric` template variable make renaming cheap now. |

The decisive observation is that the first option is impossible, so a new
consumer had to be written regardless; building it into core is the only option
consistent with the product's single-binary promise.

## Consequences

- One fewer process, config file, systemd unit, Docker image, and release step.
- The ADR 0001 durability boundary now holds: a durable consumer replays the
  JetStream backlog after downtime, and acks follow the write.
- At-least-once delivery can produce duplicate writes on retry; VictoriaMetrics
  is idempotent for an identical (metric, label set, timestamp), so this is safe.
- Tag cardinality is now controlled directly in core. Per ADR 0002, enrichment
  keys are intentionally low-cardinality; operators should still watch series
  growth via vmui's cardinality explorer. A metadata allowlist is deferred until
  there is evidence it is needed.
- Existing time series stored under `nats_consumer_number` remain queryable until
  retention expiry; new data lands under `edg_data_number`. See
  [MIGRATION.md](../MIGRATION.md).
- The `platform.data.validated` subject remains the public contract. Power users
  who need exotic fan-out (Kafka, S3, cloud TSDBs) can run an external consumer
  such as Benthos/Redpanda Connect alongside the built-in sink.

## Validation

The core test suite covers:

- Line-protocol encoding: tag escaping, empty-tag skipping, non-numeric skipping,
  metadata ordering, and millisecond timestamp handling.
- A live durable consumer writing to a mock VictoriaMetrics endpoint.
- Retry with no data loss when the endpoint returns errors before succeeding.
- **Backlog drain**: messages published to `platform.data.validated` before the
  sink starts are fully delivered once it attaches — the direct regression guard
  for the ADR 0001 gap this decision closes.
