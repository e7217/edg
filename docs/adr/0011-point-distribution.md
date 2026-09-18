# ADR 0011: Point Distribution to Adapters

- Status: Accepted
- Date: 2026-09-18
- Supersedes: none
- Related: [ADR 0007](0007-nats-subject-authorization.md), [ADR 0008](0008-adapter-runtime-status.md), [ADR 0010](0010-data-contract.md)

## Context

P2 phase 1 (#131) made each asset's point list master data: which address on
the device holds which tag, its type, unit and decoding. It stopped there.
Adapters still read their own `mapping.yaml`, so the inventory in EDG and what
an adapter actually polled could disagree, and nothing detected it. The user
guide said so.

The design panel for phase 1 chose **asset-owned master data, pulled at boot**
over push-to-running-adapter. The reason carries into this phase: the Go SDK's
`Run` is single-shot, its collect ticker is never reset, and `Collect` runs
without a lock. Applying a new list to a running collector would need all three
to change. Resolving the list *before* `NewAdapter` makes the problem disappear
instead of managing it.

## Decision

### 1. `platform.meta.points.get`

A request/reply subject returning one asset's point list. An adapter may call
it (it is in the adapter role's read grants, ADR 0007). A declared asset with
nothing declared returns an empty list at version 0 — "nothing to poll" is a
state an adapter must be able to converge on. An undeclared asset is an error.

### 2. One adapter per list version

`RunProvisioned` (Go) and `run_provisioned` (Python) take a factory from point
list to collector (Go) or adapter (Python):

1. fetch the list, waiting for core if it is not up yet;
2. build a collector from it, and an Adapter reporting its version;
3. on `platform.meta.points.changed` for the asset with a newer version, stop
   that Adapter (disconnecting the device), fetch, and go to 2.

A list change therefore costs one device reconnect. That is the price of never
applying a list to a running collector, and a point-list change is an
engineering action measured in times per week, not per second.

**Change events are best-effort** (plain NATS publishes), and a reconnect can
drop one. Each running generation also re-reads its list's version every
`ReconcileInterval` (default 5 minutes). That bounds how long a missed event
leaves an adapter on an old list.

**An unusable list does not kill the adapter.** If the factory rejects a list —
an address that is not a register number, an unknown type — the adapter logs
it, keeps heartbeating with no collector, and waits for the next version. The
drift report then shows it stuck on that version.

### 3. Convergence is observable

Status frames already carried `config_version`, reserved in ADR 0008 and always
0. Each asset entry of a frame now carries the version the adapter is running
for that asset (and the frame-level field the same, for a single-asset
adapter). `GET /api/v1/adapters/drift` compares it with the declared version
and reports `config_stale`:

```json
{"kind": "config_stale", "subject": "pump-a", "adapters": ["modbus-a"],
 "detail": "running point list v3, declared v4"}
```

An adapter reporting 0 is configured locally and is not compared; only online
adapters are compared, since a dead adapter's last word is not a configuration.

### 4. The device connection stays local

The reference Modbus adapters take `host`, `port` and `unit_id` from their local
config and the register map from master data when `asset_id` is set and
`registers` is absent. Where a device is on this box's network is a fact about
the box; what to read from it is master data. `nats_url` (or `EDG_NATS_URL`)
points the adapter at a core on another host, which the reference adapters
could not do before.

## Consequences

- `mapping.yaml` with `registers` keeps working unchanged; provisioning is
  opt-in per adapter.
- A point-list write reconnects the device of every adapter following that
  asset.
- A deleted list is a change to version 0: the adapter rebuilds with nothing to
  poll and reports 0, which the drift check treats as locally configured. The
  list's absence is visible in the point API, not in drift.
- No local cache of the list. NATS is embedded in core, so an adapter that
  cannot reach core has nowhere to publish either; a cache would only let it
  poll into the void.

## Alternatives Considered

| Alternative | Reason rejected |
| --- | --- |
| Push a new list into a running collector | Needs a resettable ticker, a locked `Collect` and a mid-flight apply protocol in both SDKs; the phase-1 panel's reason for pull. |
| Adapter-owned declarations (key by adapter id) | Adapter identity is volatile registry state (ADR 0008); the asset id is already the join key for telemetry. |
| Restart the process on change (exit and let systemd restart) | Works only under a supervisor, loses the status plane's continuity, and turns a list edit into a crash-loop signal. |
| Rely on change events alone | They are best-effort; a missed one would leave an adapter stale with nothing to correct it. |
