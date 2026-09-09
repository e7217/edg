# ADR 0008: Adapter Runtime Status

## Status

Accepted (extends [ADR 0001](0001-data-plane-reliability.md), constrained by
[ADR 0007](0007-nats-subject-authorization.md))

## Context

EDG runs adapters as external processes joined by a NATS wire contract. That
choice buys language freedom, no plugin ABI, and a core binary that stays small.
It costs the one thing an operator asks first: **is it running?**

The subject space had three planes — `platform.meta.*` (declarative master
data), `platform.data.*` (telemetry), `platform.alarm.*` (events) — and no
plane for volatile runtime state. The Go SDK has tracked `DeviceState` since
#71 and never told anyone. `rg 'heartbeat|platform.adapter'` over the repository
returned nothing.

Benchmarking against EMQX Neuron made the gap concrete. What makes Neuron feel
like a product is not its driver count but its operability layer: `link_state`,
`running_state`, `last_rtt_ms`, `tag_read_errors_total`, a device card per node.
An EDG operator with 200 adapters could not answer "which one died".

## Decision

Add a fourth plane, `platform.adapter.*`, carrying volatile runtime state.

| Subject | Direction | Purpose |
|---|---|---|
| `platform.adapter.status.<adapter_id>` | adapter → core | Status frame. The last token is authoritative for identity. |
| `platform.adapter.hello` | core → all | Asks everyone to re-announce. |
| `platform.adapter.ping.<adapter_id>` | core ↔ adapter | Liveness probe (request/reply). |
| `platform.adapter.changed` | core → subscribers | Transitions only, never every heartbeat. |
| `platform.adapter.list` | request/reply | Snapshot, in the same `Response` envelope as the metadata plane. |

Not under `platform.data.>`: the `PLATFORM_DATA` stream captures that prefix and
persists it for 7 days under a 1 GiB cap (ADR 0001/0005), so heartbeats would
evict real telemetry. Not under `platform.meta.*`: its subscribers use the
`platform.meta.*.changed` wildcard and would drown.

### Three axes, and who owns each

| Axis | Field | Asserted by |
|---|---|---|
| Process lifecycle | `run_state` — starting/running/**degraded**/stopping/stopped | adapter |
| Device link | `device_state` — the SDK's existing 5 values | adapter |
| Reachability | `availability` — online/stale/offline (`unknown` reserved) | **core only** |

An adapter cannot assert its own availability. Whether anyone can still hear it
is a judgement only the receiver can make, and a process that has crashed is in
no position to report it.

`degraded` is the axis Neuron's two-value model cannot express: three
consecutive collect failures while `device_state` stays `connected` — a PLC
that answers but returns garbage. The SDK derives it from a counter it already
maintains.

`device_state` serialises the SDK's existing constants verbatim. Go's
`DeviceState` is a string type and the Python enum uses the same values, so the
two SDKs were already symmetric; no new vocabulary and no mapping table.

### State is in memory only

The single most consequential decision, and the one most likely to be
re-litigated.

A SQLite-backed two-layer design (declaration + observation) was worked out in
full and rejected on its own admission: a persisted `last_seen_at` has no
monotonic component, so it cannot drive expiry, and every availability would
have to reset to `unknown` on restart anyway. The table that carried most of the
risk and most of the code contributed nothing to the only question this plane
answers. A persisted `connected` is, after a restart, simply a lie.

Recovery is therefore by protocol, not by storage: core broadcasts
`platform.adapter.hello` at boot and adapters re-announce within one round trip.
`Snapshot` carries a `warming` flag for the first two heartbeat windows so an
empty registry immediately after a restart is not misread as "everything is
dead".

The declaration layer — which adapters *should* exist — is a real need and a
separate one. The wire contract accommodates it without a schema bump:
`availability: unknown` is reserved and never published today, and
`config_version` is always present and always 0.

### Expiry is judged by core's clock alone

`deadline = max(3 × announced_interval, stale_after_floor)`. The adapter
announces its own interval in band, so a 15-minute batch collector and a
1-second poller coexist without central configuration.

The frame carries `sent_at`, but it appears in **no branch condition** — only in
a display-only skew figure. A wrong adapter clock cannot make a live adapter
look dead. `observed_publish_hz` is likewise differenced over core's receive
times, so it stays correct when the adapter's clock is hours off.

### A missed heartbeat is not a death

When a deadline passes, core sends `platform.adapter.ping.<id>` and waits.
Answered: the deadline is extended and **no event is emitted** — reporting a
recovered blip would train operators to ignore the signal. Unanswered: `stale`,
with `reason: probe_failed` rather than `deadline_exceeded`, so a confirmed
death is distinguishable from a guess. Concurrent probes are bounded, because a
network partition expires the whole fleet at once and an unbounded fan-out would
be a self-inflicted storm.

### Events fire only on transitions

`platform.adapter.changed` is emitted when a watched field actually changes —
availability, run state, device state, the asset set, capabilities,
config_version, or a per-asset device state or error. A heartbeat that moves
only counters is silent, so a fleet at steady state generates approximately zero
event traffic no matter how fast it heartbeats.

### Alternatives considered

| Option | Why not |
|---|---|
| **JetStream KV with per-key TTL** | Elegant on paper: the server's own clock expires the lease, `RePublish` mirrors writes back into `platform.*`, and core needs no expiry code at all. Rejected on two counts. The write contract moves off `platform.*` onto `$KV.<bucket>.<key>` plus magic headers and revision bookkeeping, so a plain NATS client can no longer participate — a direct violation of the wire-contract-first constraint. Worse, `kv.Put` is an ordinary publish and `kv.Update` passes no TTL, so a developer reaching for the most natural KV call leaves a dead adapter marked **online forever**, and core has no way to prevent it. A monitoring tool that fails silently in the safe-looking direction is the worst failure grade available. |
| **SQLite two-layer (declaration + observation)** | See above: the observation half cannot answer the liveness question, and the declaration half is an inventory problem, not a runtime one. Deferred with the wire contract prepared for it. |
| **Registration handshake (request/reply)** | Core could validate the payload and id at registration time. But registration alone cannot answer "when did it die", so heartbeats and expiry are needed regardless, making this a strict subset. Reduced to a single start-up ping for duplicate-instance fencing, which is the one thing it does uniquely. |
| **A `servedBy` relation in `asset_relations`** | Reuses existing storage, but `RelationType` is ontology vocabulary (ssn/sosa/schema.org). `enricher.go` turns ancestor `TemplateName` into tag keys, so adapter names would leak into every `AssetData.Metadata`; constraint cardinality and traversal depth would both be polluted. Documented as a non-goal. |
| **`$SYS.ACCOUNT.*.DISCONNECT`** | Near-instant detection instead of ~30s. Requires the system account, and it reports NATS connections rather than adapter health — an adapter can hold its connection while failing every collect. A worthwhile future addition alongside ADR 0007's accounts, not a replacement. |
| **Per-entry `time.AfterFunc`** (the `alarm_aggregator.go` idiom) | A thousand adapters expiring together spawn a thousand goroutines, and the expiry clock binds to the runtime timer, which makes clock injection awkward. A single reaper ticker solves both. |
| **Centrally negotiated heartbeat cadence via hello** | Tunes the fleet without redeploying config, but makes an adapter's effective behaviour depend on a broadcast it may or may not have received — action at a distance that will confuse someone during an incident. The interval comes from local config and is only announced. |

## Consequences

- No new dependency, no new infrastructure, no migration. `go.mod` unchanged.
- **Upgrading the SDK turns reporting on.** That is the right default for
  observability but it is a behaviour change: adapters begin publishing to a
  new subject. `DisableStatusReporting` opts out.
- Detection latency is roughly `3 × interval + probe timeout`, plus up to half
  the reaper scan interval. A live smoke test caught the scan interval being
  derived from `MaxInterval`, which made a 2s deadline take 150s to notice; it
  now derives from `StaleAfterFloor`.
- `Clock` is the repository's first time abstraction. It exists because the
  reaper is otherwise untestable without real sleeps; no expiry test sleeps.
- Per-asset detail is capped, with `device_counts` as the rollup. An adapter
  fronting hundreds of assets would otherwise send a 10 KB frame per heartbeat.
- ADR 0007's matrix must grant `platform.adapter.*` publish to the adapter role
  and `platform.adapter.ping.>` reply. Until then the plane inherits whatever
  the deployment's mode allows.

### Known limitations

- **Status can be forged.** Anyone permitted to publish `platform.adapter.*`
  can claim any `adapter_id` the subject token allows, including a false
  `phase: offline` that makes a healthy adapter vanish from the UI. Mitigated:
  the subject token is authoritative over the body, so an adapter cannot
  impersonate another by lying, and the next genuine heartbeat restores the
  truth within one interval. Full mitigation is per-adapter credentials, which
  ADR 0007 lists as follow-up work.
- **`host`/`pid` are opt-in.** Adapter inventory is more sensitive than the
  asset list, and this plane carries no authorization of its own.
- **No history.** Only current state is kept. Capturing
  `platform.adapter.changed` into a JetStream stream would give history with
  **no wire-contract change**, which is the intended path if it is wanted.
- **Drift does not report unserved assets.** The registry cannot distinguish a
  sensor that lost its collector from a line or factory node that was never
  meant to have one. That check needs the declaration layer.
- **Duplicate-id fencing fails open.** If an existing instance is wedged and
  cannot answer the start-up ping, both processes run and double-poll. Failing
  closed would block legitimate restarts. Revisit with field data.

## Validation

- Registry unit tests with an injected clock: first sighting, counter-only
  heartbeats staying silent, device-state and degraded transitions, instance
  replacement, graceful offline, deadline arithmetic for slow and fast
  adapters, interval clamping, asset re-indexing, rate derivation surviving a
  restart and a sequence regression, and rate correctness with the adapter
  clock an hour off.
- Reaper tests: before/after deadline, probe recovery emitting no event, probe
  failure recording `probe_failed`, recovery on the next heartbeat, forget-after
  cleanup including the asset index, bounded probe concurrency under a 50-adapter
  partition, and idempotent sweeps.
- Handler integration tests over embedded NATS publishing **raw JSON without the
  SDK**, because the wire contract has to stand on its own: registration,
  impersonation rejection, malformed and invalid frames, change events reaching
  subscribers, the list envelope, hello-driven re-announce, and a real
  request/reply probe recovering then failing.
- SDK tests in both languages: a Collect-only adapter reporting unchanged,
  opt-out, edge-triggered transitions, degraded-while-connected, the goodbye
  frame arriving before disconnect, hello and ping responses, and host/pid
  staying opt-in.
- A shared golden fixture parsed by both SDK suites.
- Mutation checks confirming the tests fail when probe results are ignored and
  when noise suppression is removed.
- End-to-end against a running `edg-core`: a raw NATS publish registers, appears
  in `list`, and produces a `stale/deadline_exceeded` transition after silence.
