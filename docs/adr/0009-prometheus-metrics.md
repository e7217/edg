# ADR 0009: Prometheus Metrics

- Status: Accepted
- Date: 2026-09-10
- Supersedes: none
- Related: [ADR 0001](0001-data-plane-reliability.md), [ADR 0002](0002-ontology-enrichment.md), [ADR 0005](0005-embedded-vm-sink.md), [ADR 0007](0007-nats-subject-authorization.md), [ADR 0008](0008-adapter-runtime-status.md)

## Context

EDG published fifteen `expvar` counters on the NATS monitoring port at
`/debug/vars`. They are real and documented — ADR 0001 tables three of them and
tells operators they "must monitor stream state and the dead-letter counters to
detect data loss pressure" — but `/debug/vars` is a JSON blob no time-series
database scrapes, and counters alone answer almost nothing.

The gaps were not evenly distributed. The process could count several of its
failures and none of its successes, so no failure *rate* was computable from
what it published. Four things were entirely invisible:

- **The JetStream backlog.** ADR 0005 made the JetStream-to-storage hop durable
  and then left the consumer's pending count unreported. "Is the gateway
  keeping up?" had no answer.
- **A non-timeout `Fetch` error in the sink.** No counter, no log line. A
  consumer that had stopped delivering looked exactly like an idle plant.
- **A poison batch.** A batch that decodes but yields no numeric lines is acked
  — leaving it to redeliver forever is worse — which is a silent drop of real
  data.
- **Ingest latency.** `HandleAssetData` runs on the NATS delivery goroutine and
  performs a synchronous `AssetExists`, enrichment that can fall through to a
  recursive CTE, and a JetStream publish. When that stops fitting inside the
  message interval the subscription becomes a slow consumer and messages are
  dropped with no error anywhere.

## Decision

Expose a Prometheus text exposition endpoint at `/metrics`, produced by a
registry written against the standard library alone, and have VictoriaMetrics
scrape it with its built-in `-promscrape.config`.

### 1. No `prometheus/client_golang`

`internal/metrics` implements exposition format 0.0.4 directly. The argument,
in order of weight:

1. **ADR 0005 already ruled on this dependency.** It rejected Prometheus
   `remote_write` because it "adds protobuf + snappy dependencies".
   `client_golang` links `google.golang.org/protobuf` unconditionally, because
   `Registry.Gather` returns `[]*dto.MetricFamily` — a protobuf generated type
   — even when nothing is ever serialised as protobuf. Adopting it means
   amending an accepted ADR.
2. **Measured cost.** Direct dependencies 7 → 8, linked modules 19 → 27, Go
   packages 265 → 314, stripped binary 18,551,024 → 20,452,176 bytes
   (+1.81 MiB, +10.25%), and eight rows in the hand-maintained
   `THIRD_PARTY_LICENSES.md`. The product's pitch is one small self-contained
   binary.
3. **Version pinning.** `client_golang` v1.24 requires `go 1.25.0`, which the
   repository's `go.mod`, CI and Dockerfile do not have. Pinning to v1.20.5
   avoids that but means adopting a dependency we can never update — which is
   itself the debt.
4. **Almost nothing is lost.** `runtime/metrics` is the same source
   `client_golang`'s `GoCollector` reads. What is given up is quantile
   summaries (a per-instance quantile is a non-aggregatable anti-pattern) and
   exemplars (EDG has no tracing).

The one real risk is getting the format wrong, and that is delegated to the
reference implementation: CI runs `promtool check metrics` against both a
golden file and a live scrape of the real binary. `promtool` is a CI binary,
never a `go.mod` entry, so the dependency graph is unchanged — and a CI step
asserts `internal/metrics` imports nothing outside the standard library.

### 2. The fifteen legacy counters keep their storage

`Counter` stores through `*expvar.Int`. `NewCounterLegacy` publishes that
`expvar.Int` under its historical name; `NewCounter` does not publish at all.
The result is that `/debug/vars` is byte-for-byte what it was — same names,
same concrete type, same JSON — while `/metrics` serves the same numbers under
Prometheus-conventional names with the `_total` suffix.

Three of them gained a label rather than a second metric. Every child feeds the
same `expvar.Int`, so the children sum to the historical total by construction:

| `/debug/vars` (unchanged) | `/metrics` |
|---|---|
| `edg_core_jetstream_publish_failures` | `edg_core_jetstream_publish_failures_total` |
| `edg_core_jetstream_dead_letters` | `edg_core_jetstream_dead_letters_total` |
| `edg_core_jetstream_dead_letter_failures` | `edg_core_jetstream_dead_letter_failures_total{stage}` |
| `edg_core_undeclared_assets` | `edg_core_undeclared_assets_total{policy}` |
| `edg_core_sink_lines_written` | `edg_core_sink_lines_written_total` |
| `edg_core_sink_batches_written` | `edg_core_sink_batches_written_total` |
| `edg_core_sink_write_failures` | `edg_core_sink_write_failures_total{reason}` |
| `edg_core_sink_decode_failures` | `edg_core_sink_decode_failures_total` |
| `edg_core_adapter_status_invalid` | `edg_core_adapter_status_invalid_total` |
| `edg_core_adapter_status_dropped` | `edg_core_adapter_status_dropped_total` |
| `edg_core_adapter_stale_total` | `edg_core_adapter_stale_total` |
| `edg_core_adapter_probe_recovered` | `edg_core_adapter_probe_recovered_total` |
| `edg_core_adapter_probes_skipped` | `edg_core_adapter_probes_skipped_total` |
| `edg_core_adapter_stale_averted` | `edg_core_adapter_stale_averted_total` |
| `edg_core_adapter_forget_averted` | `edg_core_adapter_forget_averted_total` |

**Nothing else may reach the `expvar` global registry.** `expvar.Handler` dumps
every published variable, and the monitoring port has no authentication of any
kind. A `Func` collector published there — a `SELECT COUNT(*)`, a JetStream
`ConsumerInfo` round trip — would let any caller on that interface force that
work once per request: an amplification vector, not a cosmetic issue.
`NewCounterLegacy` is the only door to `expvar`, it accepts only a `Counter`,
and a test pins the `/debug/vars` key set to exactly `{cmdline, memstats}` plus
those fifteen.

### 3. Labels come from closed sets, never from the network

> **Rule.** A label value in EDG's operational metrics comes from (a) a
> compile-time constant set, (b) a validated enum, or (c) an explicitly
> bounded, evictable operator-declared identifier. `adapter_id` is the only
> instance of (c).

`asset_id` is refused. ADR 0002 already ruled that enrichment "uses stable
template names as tag keys **to avoid creating tag keys from asset IDs or
display names**", and four things make it worse here than there:

1. It is pure duplication. The sink already writes `asset_id` into the same
   VictoriaMetrics instance, so "is asset X reporting?" is answered today by
   `count_over_time(edg_data_number{asset_id="X"}[5m])`.
2. A scrape metric is worse than an event metric for this. `/metrics` is
   scraped every 15s regardless of traffic, so a device that reported once and
   vanished keeps its series alive for the whole staleness window — it
   *amplifies* churn.
3. The input is untrusted. `asset_id` arrives over an open wire contract and
   `unknown_asset_policy: pass_through` is the default, so unknown ids are
   accepted (`internal/core/handler.go`).
4. `CounterVec` children are never evicted, so one misconfigured adapter would
   OOM **edg-core itself** — the gateway dies, which is a far worse failure
   than high cardinality in the TSDB.

`adapter_id` is the exception because it is a different class of input: it is
node topology an operator declared, in the same family as `instance`, and it
changes only at deployment. It is still capped (`adapters.metrics_max_tracked`,
default 200) with the remainder folded into `adapter_id="__overflow__"`, and
the fold is counted rather than silent.

Enforcement is threefold: structural (`CounterVec`/`CounterVec2` take a closed
value set, materialise the whole cross product at registration, assert it
against a 256 budget, and fold unknown values into `other` while incrementing
`edg_core_metrics_label_rejected_total`); static (a deny list panics on
`asset_id`, `id`, `name`, `subject`, `url`, `path`, `tag`); and dynamic
(`TestMetricCardinalityBudget` drives 1, 100 and 10,000 distinct asset ids
through the ingest path and asserts the series count does not move).

### 4. Two mount points, and why the obvious one is not enough

`/metrics` is served on the NATS monitoring mux (`nats.http_port`, default
8222) and on an optional dedicated listener (`metrics.address`).

The monitoring mount is free — that port is always up, unlike the HTTP API,
which is `enabled: false` in both the staging and production configs. But
ADR 0007 moved `nats.http_host` to `127.0.0.1` and stopped publishing 8222 from
the container, precisely because `/varz`, `/connz` and `/debug/vars` sit there
unauthenticated. A loopback endpoint cannot be scraped by VictoriaMetrics in a
sibling container, so the monitoring mount alone would have shipped a metrics
feature that produces no metrics in the deployment we actually ship.

Hence `metrics.address`. The container configs set `0.0.0.0:9464` and
deliberately do **not** publish it: the trust boundary is the compose network,
the same one VictoriaMetrics sits on. The default is empty, because opening a
second port on a host nobody asked about is a cost with no matching benefit.

Neither mount is fatal. A bind failure logs and continues, and so does a
missing monitoring port. Metrics are how an operator learns the gateway is
unwell; they are not a precondition for it running.

`ns.HTTPHandler()` returning an `*http.ServeMux` is an undocumented
implementation detail of nats-server (the field is assigned one, the accessor
returns `http.Handler`). `cmd/core/metrics_mount_test.go` asserts it against a
real server, and also asserts `/metrics` is a 404 *before* we mount, so a
future nats-server that ships its own `/metrics` is a red test rather than a
silent shadowing.

### 5. Scrape-time work stays off the data path

`/metrics` is unauthenticated on both mounts, so no collector may do unbounded
work per request.

- Anything reading a subsystem — `runtime/metrics`, `/proc`, `Jsz`, the adapter
  registry — is sampled once per scrape through a `BeforeGather` hook, not once
  per derived series. Every series in a response therefore describes the same
  instant.
- `ConsumerInfo` is a NATS round trip, so the sink refreshes its backlog gauges
  on its own drain loop (`sink.consumer_stat_interval`, default 15s), never at
  scrape time.
- `edg_core_alarm_groups_pending` is pushed from inside the aggregator's mutex,
  so a scrape never queues behind the lock the alarm path holds while running
  SQL.
- `edg_core_store_assets` and `_relations` are `SELECT COUNT(*)` on the same
  serialized SQLite handle the ingest path uses, so they sit behind a
  one-minute cache with a two-second timeout.

Every one of these keeps its last known value on error. A failed query says
nothing about the quantity it measures, and reporting zero would read as "all
caught up" or "the database is empty" — the opposite of what a failure implies.

### 6. Histograms, not averages

The three things this codebase actually needs to know are all tail phenomena:
the moment a sink write exceeds `sink.request_timeout`, the moment the
enricher's recursive CTE stalls on SD-card SQLite, and the quasi-quadratic
spike when the alarm aggregator walks every open group under its lock. An
average hides exactly those. The bucket bounds are provisional and say so in
their HELP text — a wrong bucket layout looks precise while being wrong, and
one revision after the first field deployment should be expected.

The two `runtime/metrics` histograms are downsampled from ~160 buckets to ten,
and carry no `_sum`, because `runtime/metrics` reports none and a sum estimated
from bucket midpoints would look authoritative while being wrong. Downsampling
only credits a runtime bucket to a target bound when the bucket's upper edge is
at or below it, so bucket counts are conservative lower bounds while `_count`
stays exact: an SLO computed from these can never read better than reality.

## Consequences

- A steady-state process exposes about 95 families and roughly 316 series with
  no adapters connected. That is a compile-time number: it does not grow with
  assets, tags, adapters (past the cap) or request paths.
  `edg_core_metrics_series` reports it, so the cost is itself observable.
- `/debug/vars` remains supported and unchanged. ADR 0001's counter table and
  the user guide stay correct as written.
- The exposition format is now ours to keep correct. That obligation is
  discharged by golden tests, a live `promtool` check in CI, and mutation
  testing of the encoder.
- **The metrics endpoint is unauthenticated on both mounts.** An operator who
  hardened the REST API with `http.token_env` has not thereby hardened
  `/metrics`. It carries no secrets and no master data, but it does reveal
  counts, rates and the build stamp. Keep it on a trusted network.
- Adapter metrics read `AdapterRegistry` (ADR 0008) directly rather than a new
  subject, so they agree with `GET /api/v1/adapters` by construction and no SDK
  change was required.

## Validation

- `internal/metrics` unit tests: golden exposition, escape tables, `+Inf` /
  `-Inf` / `NaN`, histogram cumulativeness with `le="+Inf"` equal to `_count`,
  duplicate-name and deny-list panics, the 256 cross-product budget, and the
  `other` fold.
- CI job `Prometheus exposition`: `promtool check metrics` against both the
  golden file and a live scrape of the real binary started with
  `config.prod.yaml`, plus an assertion that `internal/metrics` imports nothing
  outside the standard library.
- `TestDebugVarsSurfaceIsExactlyTheLegacyCounters` pins the unauthenticated
  `/debug/vars` key set.
- `TestMetricCardinalityBudget`, `TestRouteLabelNeverCarriesAPathParameter` and
  `TestAdapterMetricsDoNotCarryAssetIDs` pin the cardinality rule.
- Mutation testing: 45 mutations across the encoder, registry, and the ingest,
  sink, HTTP, store, alarm and adapter instrumentation. All 45 fail the tests.
- End to end: `docker compose up` with VictoriaMetrics reporting both scrape
  targets healthy and answering PromQL for `edg_core_*` series labelled
  `job="edg-core"`.
