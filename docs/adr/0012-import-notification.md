# ADR 0012: Telling a Running Core What an Import Wrote

- Status: Accepted
- Date: 2026-09-25
- Supersedes: none
- Related: [ADR 0007](0007-nats-subject-authorization.md), [ADR 0010](0010-data-contract.md), [ADR 0011](0011-point-distribution.md)

## Context

`-import-plant`, `-import-points` and `-import-templates` write the metadata
database directly and exit (#150). They build the same `MetadataService` the
API uses, and the service builds a change event for every write, but the CLI
handed it no publisher, so every event was dropped. A running core caches
master data in three places, and none of them re-reads the database on its own:

| Cache | Refreshed by | After a CLI import |
| --- | --- | --- |
| Template loader | startup only | new or edited templates invisible until restart |
| Data contract declarations | `asset.changed`, `points.changed` | a newly declared tag's type and unit not enforced |
| Enricher ancestor tags | `asset.changed`, `relation.changed` | stale ancestors on validated data |

Provisioned adapters caught up at their next reconcile, up to 5 minutes later.
The user guide told operators to restart core after every import. On a live
site a plant-bundle import is the normal L2 workflow, and "remember to restart"
is easy to forget.

Measured against a running core in strict mode, the bundle declaring a new
`vibration` point as `FLAG`, and then sending it a number:

| | before | after this ADR |
| --- | --- | --- |
| `vibration` as a number, after the import, no restart | kept on `platform.data.validated` | removed and dead-lettered |
| `platform.meta.points.changed` seen by a subscriber | none | `v2`, 12 ms after the import started |
| same bundle imported again | — | no events, no template reload |

## Decision

### 1. The CLI records the events, then names what changed

The CLI's service gets a recording publisher, so its events are kept in memory
instead of dropped. After the import commits, the CLI sends a request on
`platform.meta.import.applied` listing each changed entity by id and event type,
plus `templates_changed`. It does not send the events. Batches hold at most
1,000 entities, well under NATS's 1 MB default payload.

`templates_changed` compares a fingerprint of the template cache before and
after the import, so re-importing the same templates is no change.

### 2. Core reads each entity back and announces it itself

For each id, core reads the entity from the database and publishes the change
event under its own identity. `after` is what is stored. There is no `before`.
An entity that is gone by then is announced as `deleted`. With
`templates_changed` set, core also reloads the template loader and flushes the
data contract, because no event covers templates. The existing subscriptions do
the rest: enricher and contract flush, and `RunProvisioned` adapters rebuild.

`platform.meta.*.changed` stays core-only (ADR 0007). The content of a change
event is still only ever what core read from its database.

### 3. The notification is operator-only and best-effort

`platform.meta.import.applied` is in the operator role's write grants, alongside
the master-data mutations. The adapter, fanout and legacy roles are denied it.
The CLI dials this host's NATS port as `operator`, with a wildcard bind
dialled as loopback. It resolves the password the way core does, but never
creates a credentials file.

If core cannot be reached, the import still succeeds and prints the old restart
hint. That happens when core is stopped, when it is an older version without
the handler (the request times out), or when the credentials cannot be read.
Importing with core stopped is a documented, supported path. A `-dry-run`
never notifies.

### 4. Two defects on the same path, fixed with it

- `-import-points` rewrote every list, bumping its version even when nothing
  changed, so every re-import rebuilt every provisioned adapter. It now skips a
  list identical to the stored one, as `-import-plant` already did.
- `-dry-run` copied only the main database file. In WAL mode, a running core's
  recent commits live in the `-wal` file until a checkpoint, so the dry run
  compared against older data. It now takes the snapshot with `VACUUM INTO`,
  which includes them. A test shows the plain copy losing a commit and the
  snapshot keeping it.

## Alternatives

| Alternative | Why not |
| --- | --- |
| A payload-less `reload`: core flushes everything and re-announces every point list | Announces unchanged entities as `updated` on a public event contract (docs/events.md). Adapters filter by version, but other subscribers cannot. |
| The CLI publishes `*.changed` itself | Core-only by ADR 0007, and core's credential never touches disk. |
| Core republishes the events the CLI recorded, `before` included | Keeps `before`, but lets an operator put arbitrary content into an event carrying core's authority, and grows the payload. Nothing reads `before` today. |
| Route imports through the HTTP API when core is up | No template write endpoint and no bulk endpoint, so an import would be thousands of requests. Writes also need a token that default installs do not configure. A bigger feature than #150. |
| Detect a running core and refuse to import | Turns a working offline path into an error, and still leaves the running core stale when it is used. |

## Consequences

- An import is visible to a running core as soon as it commits. The restart
  hint appears only when core could not be told.
- Events announcing an import carry no `before` (docs/events.md). A subscriber
  that needs the previous state must keep it itself.
- Measured with 10,000 assets and 10,000 point lists imported into a running
  core, a subscriber received all 20,000 events within 0.74 s. The import's
  total time is dominated by SQLite writes, and did not change measurably:
  82.0 s before, 81.9 s and 83.0 s after, on a loaded shared disk. Earlier
  runs of the same import took 18 to 25 s, so the absolute figure reflects the
  disk, not this change.
- Only this host's core is told. Telling a core on another host would need a
  URL flag. Nobody has asked for one, so there is none.

## Validation

- `internal/core/import_notify_test.go`, `TestCLIImportReachesRunningCore`: a
  core with its own database connection stays stale after the import (the
  bug), and is current after the notification: the template is visible, the
  contract has the new tag, and `points.changed` arrives with `after` and no
  `before`.
- `internal/natsauth/roles_test.go`: the role matrix is data-driven over
  `metaWritePublish`, so the new subject is checked for every role against a
  real server.
- `cmd/core/import_notify_test.go`: the CLI authenticates as operator in strict
  mode, fails without creating a credentials file, and gives up quickly when
  no core is listening.
- `internal/core/store_snapshot_test.go`, `points_io_test.go` and
  `loader_test.go` cover the dry-run snapshot, `-import-points` idempotency,
  and a template fingerprint that survives a round trip through SQLite. That
  last case was a bug the end-to-end measurement found: `resources: []` read
  back as `null`.
