# EDG User Guide

This guide provides detailed instructions for installing, configuring, and monitoring the EDG platform.

## Installation Details

### Using Release Bundles

1. **Download the release bundle for your platform:**
   - Linux x86_64: `edg-vX.X.X-linux-amd64.tar.gz`
   - Linux ARM64: `edg-vX.X.X-linux-arm64.tar.gz`
   - macOS: `edg-vX.X.X-darwin-amd64.tar.gz`
   - Windows: `edg-vX.X.X-windows-amd64.zip`

2. **Extract the bundle:**
   ```bash
   tar -xzf edg-vX.X.X-linux-amd64.tar.gz
   cd edg-vX.X.X-linux-amd64
   ```

3. **Run the installation script (Linux/macOS):**
   ```bash
   sudo ./install.sh
   ```
   This will:
   - Install binaries to `/opt/edg/bin/`
   - Copy configs to `/opt/edg/configs/` and templates to `/opt/edg/templates/`
   - Link `/opt/edg/config.yaml` to the selected environment config
   - Create systemd services (Linux only)

   `EDG_ENV` selects which config becomes active (`dev`, `staging` or `prod`;
   default `prod`):

   ```bash
   sudo EDG_ENV=dev ./install.sh
   ```

   The link is what makes the installed config take effect. `edg-core` looks
   for `/opt/edg/config.yaml`, and the systemd unit passes `-config` pointing
   at it; without the link the process finds nothing and runs on compiled-in
   defaults, which use `nats.auth.mode: compat` rather than the `strict` the
   production config specifies. **Whatever happens, the startup log names the
   file it loaded** — check it before trusting a config change:

   ```
   [Config] loaded /opt/edg/config.yaml (-config flag)
   ```

   or, if nothing was found:

   ```
   [Config] no config file found (searched: ...); using built-in defaults.
   ```

### Managing Services (Systemd)

```bash
# Start services
sudo systemctl start edg-victoriametrics
sudo systemctl start edg-core

# Enable auto-start on boot
sudo systemctl enable edg-victoriametrics edg-core
```

EDG Core writes validated data to VictoriaMetrics through its built-in sink, so
there is no separate metrics agent to run.

### Manual Start (No Systemd)

If you are not using systemd, you can start components manually:

```bash
# Start VictoriaMetrics
/opt/edg/bin/victoria-metrics-prod -storageDataPath=/opt/edg/data/victoria-metrics &

# Start EDG Core (its built-in sink writes to VictoriaMetrics)
/opt/edg/bin/edg-core &
```

### Custom Installation Directory

```bash
sudo INSTALL_DIR=/srv/edg ./install.sh
```

The staging and production configs name their data and template directories
absolutely, so that the same command run from a different working directory
cannot quietly open a different database. The installer rewrites those paths to
the chosen root and refuses to finish if any of them still point at `/opt/edg`,
so a relocated install never ends up split across two directories.

### Re-running the Installer

Upgrading is `./install.sh` again over the same root. It never overwrites a
config you have edited: an unchanged file is replaced, a changed one is kept
and the shipped version is written beside it as `<name>.new` for you to merge.
The installer lists anything it preserved.

### Where Data Lives

Everything EDG writes sits under one install root, `/opt/edg` by default:

| Path | Contents |
| --- | --- |
| `/opt/edg/bin` | `edg-core`, `victoria-metrics-prod` |
| `/opt/edg/configs` | the shipped `config.{dev,staging,prod}.yaml` |
| `/opt/edg/templates` | template seed directory (`templates.dir`) |
| `/opt/edg/data` | `metadata.db`, `jetstream/`, `nats-credentials.json` (`storage.data_dir`) |

The Docker image builds the same layout, and `deploy/docker/compose.yml` mounts
its `edg-data` volume at `/opt/edg/data`. **Back that directory up and you have
the gateway's configuration and its in-flight data**: master data, the
JetStream backlog and the NATS role credentials.

It is not a complete backup. The measurements themselves live in
VictoriaMetrics, which is a separate `vm-data` volume in the compose stack and
`/opt/edg/data/victoria-metrics` in a host install. Back up both, or you keep
the plant model and lose every reading it describes.

To check the stack after a change to the compose file or the image, run
`scripts/compose-smoke.sh` — it boots the stack, waits for the healthcheck,
verifies both scrape targets and asserts that state survives a container
recreate. CI runs the same script.

## Configuration

### EDG Core
- **Data Storage**: `./data/metadata.db` (auto-created)
- **Schema Migrations**: embedded migrations run automatically on startup
- **Templates**: stored in SQLite (authoritative). The `templates.dir` directory
  (default `./templates/`) is **seed-imported on first boot** when the DB has no
  templates, and is the default target for the import/export commands below.
- **Config file**: set `EDG_CORE_CONFIG` or pass `--config` to choose a core YAML file.

### Adapter Runtime Status

Core tracks which adapters are alive and what they are collecting
([ADR 0008](adr/0008-adapter-runtime-status.md)).

```yaml
adapters:
  enabled: true
  stale_after_floor: 15s      # minimum deadline, so a fast poller is not
                              # declared stale by a momentary hiccup
  min_interval: 1s            # clamps the interval an adapter announces
  max_interval: 5m
  forget_after: 24h           # drop an adapter that has been stale this long
  probe_on_miss: true         # actively ping before declaring stale
  probe_timeout: 2s
  max_concurrent_probes: 32   # a partition expires the whole fleet at once
```

An adapter is `online` while frames keep arriving, `stale` once its deadline
passes and a probe goes unanswered, and `offline` when it said goodbye cleanly.
The deadline is three times the interval the adapter itself announces, so a slow
batch collector is not declared dead for being quiet.

Inspect it with `GET /api/v1/adapters`, `GET /api/v1/adapters/drift`, or the
Adapters section of the operator UI. Right after a core restart the list is
marked `warming` — an empty list then means "not heard from yet", not
"everything is dead".

**Runtime state is not persisted.** It is volatile by definition, and a stored
"connected" would be a lie after a restart. Core re-learns it by broadcasting
`platform.adapter.hello`, to which adapters respond immediately.

Setting `adapters.enabled: false` removes the routes entirely (404) rather than
serving an empty list.

### NATS Authorization

The embedded NATS server enforces a role-based subject matrix
([ADR 0007](adr/0007-nats-subject-authorization.md)).

```yaml
nats:
  host: 0.0.0.0          # client port
  http_host: 127.0.0.1   # monitoring port — no auth mechanism, keep on loopback
  auth:
    mode: compat         # compat | strict | off
    credentials_file: "" # empty -> <storage.data_dir>/nats-credentials.json
```

| Role | May do |
| --- | --- |
| `operator` | Everything an administrator needs, including master-data writes |
| `adapter` | Publish telemetry and alarms; **read** master data, not write it |
| `fanout` | Attach a durable JetStream consumer to the data stream, nothing else |
| `legacy` | `compat` mode only: anonymous clients get `adapter` ∪ `fanout` |

**Modes.** `compat` (default) enforces the matrix but still accepts anonymous
connections as `legacy`, so existing adapters keep working after an upgrade.
`strict` requires credentials. `off` disables authorization entirely and is
refused unless `nats.host` is a loopback address.

**Credentials** are generated on first boot into a `0600` JSON file; the boot
banner prints its path. Clients authenticate by putting them in the URL:

```bash
nats://adapter:<secret>@localhost:4222
```

Read a role's secret with:

```bash
jq -r .adapter /opt/edg/data/nats-credentials.json
```

To keep secrets off disk entirely, set all three of
`EDG_NATS_OPERATOR_PASSWORD`, `EDG_NATS_ADAPTER_PASSWORD` and
`EDG_NATS_FANOUT_PASSWORD`; the file is then neither created nor read. Setting
only some of them is an error rather than a silent fallback.

**Migrating to `strict`.** Roll credentials out to every client first, then flip
the mode. Anything still publishing to a forbidden subject shows up in the core
log as `Publish Violation - Subject "..."`, which is the checklist for clients
that do not use an EDG SDK.

Manage templates as files without running the server:

```bash
edg-core --import-templates ./templates   # YAML files -> SQLite (upsert)
edg-core --export-templates ./out          # SQLite -> one YAML per template
```

**JetStream reliability defaults:**
```yaml
jetstream:
  validated_subject: platform.data.validated
  dead_letter_subject: platform.data.deadletter
  stream:
    name: PLATFORM_DATA
    subjects:
      - platform.data.>
    storage: file
    max_age: 168h
    max_bytes: 1073741824
    replicas: 1
    retention: limits
    discard: old
```

## What "Reliable" Means

EDG persists data after the core successfully publishes validated data to
JetStream and receives a publish acknowledgement. The adapter-to-core hop is
plain NATS pub/sub, so adapters that need stronger end-to-end guarantees should
retry or buffer before publishing.

If publishing to `platform.data.validated` fails, core attempts to publish a JSON
failure envelope to `platform.data.deadletter`. Monitor these counters on the
core process — the `expvar` name at `/debug/vars`, the `_total` name at
`/metrics` (see [Metrics](#metrics)); they are the same counter:

- `edg_core_jetstream_publish_failures`
- `edg_core_jetstream_dead_letters`
- `edg_core_jetstream_dead_letter_failures`

See [ADR 0001](adr/0001-data-plane-reliability.md) for the reliability model and
failure-mode tradeoffs.

### VictoriaMetrics Sink

Core's built-in sink reads `platform.data.validated` with a durable JetStream
consumer and writes to VictoriaMetrics using the InfluxDB line protocol. It is
configured under the `sink:` block:

```yaml
sink:
  enabled: true              # set false to disable and use an external consumer
  url: http://localhost:8428 # override per host with the EDG_SINK_URL env var
  consumer_name: edg-core-vm-sink
  measurement: edg_data
  batch_max_size: 500
  flush_interval: 1s
  request_timeout: 5s
  consumer_stat_interval: 15s  # how often the backlog gauges are refreshed
```

Each numeric value becomes one metric named `edg_data_number`, tagged with
`asset_id`, `name`, `unit`, `quality`, and any enrichment metadata. Adapter
timestamps (epoch milliseconds) are preserved.

Sink health is exposed on the core process at `/debug/vars` and `/metrics`:

- `edg_core_sink_lines_written` — line-protocol lines, i.e. time series points,
  not messages. Values without a numeric reading produce no line.
- `edg_core_sink_batches_written`
- `edg_core_sink_write_failures`
- `edg_core_sink_decode_failures`

`/metrics` adds what a counter alone cannot answer — is the sink keeping up:

| Metric | Why it matters |
| --- | --- |
| `edg_core_sink_consumer_pending` | Messages waiting in the stream for the sink. **A sustained climb is the single most important warning in this system**: VictoriaMetrics is slower than the plant, and the stream will eventually hit `max_bytes` and discard the oldest data. |
| `edg_core_sink_write_seconds` | Write latency. Compare its tail with `request_timeout`. |
| `edg_core_sink_write_failures_total{reason}` | `transport` means VictoriaMetrics was unreachable; `http_status` means it answered and refused. |
| `edg_core_sink_messages_acked_total{outcome="poison"}` | Batches acked with nothing numeric to write. **This data is dropped, not stored.** |
| `edg_core_sink_fetch_errors_total` | The consumer stopped delivering. Without this, that looks exactly like an idle plant. |
| `edg_core_sink_up` | 1 while the drain loop is running. |

**Data Format:**
Incoming JSON from adapters:
```json
{
  "asset_id": "sensor-001",
  "values": [
    {"name": "temperature", "number": 25.5, "unit": "°C", "quality": "good"}
  ]
}
```

## Asset Metadata

EDG Core stores asset metadata in SQLite and exposes it through NATS metadata subjects.

### Undeclared Asset Policy

Master data is created explicitly (metadata API / CLI / UI / import). The data
plane no longer auto-registers assets. EDG Core controls what happens to data
whose `asset_id` has no declared Asset record with `unknown_asset_policy`.

| Policy | Behavior |
| --- | --- |
| `pass_through` | Default. The message is published to the validated data subject un-enriched (no ontology metadata is added). No Asset record is created. |
| `dead_letter` | The message is routed to the dead-letter subject instead of the validated subject. |

Either way, an undeclared-asset counter is incremented for operator visibility:
`edg_core_undeclared_assets` at `/debug/vars`, and
`edg_core_undeclared_assets_total{policy}` at `/metrics`, where the label says
which of the two behaviours above was applied.

```yaml
unknown_asset_policy: pass_through
```

> The removed `asset_registration:` block is ignored if still present; EDG Core
> logs a one-time startup warning pointing to `unknown_asset_policy`.

Asset records include:
- `id`: stable asset identifier
- `name`: unique display name
- `template_name`: optional asset template
- `labels`: optional list of tags
- `external_ids`: optional key/value identifiers such as `irdi`, `eclass`, or `aas`
- `source`: origin tag; core uses `manual` for explicit metadata creation and `auto` for data-plane auto-registration
- `attributes`: optional free-form key/value metadata
- `created_at` and `updated_at`: creation and last metadata update timestamps

### Create Asset

Subject: `platform.meta.asset.create`

```json
{
  "id": "pump-101",
  "name": "pump-101",
  "template_name": "vibration-sensor",
  "labels": ["line-a", "critical"],
  "external_ids": {
    "irdi": "0173-1#02-BAA120#008",
    "aas": "aas://example/pump-101"
  },
  "source": "manual",
  "attributes": {
    "manufacturer": "ACME",
    "model": "PX-10"
  }
}
```

`id` is optional; omit it and the server generates a UUID. Supply it when you
need the declared id to match what an adapter publishes — see
[Point Provisioning](#point-provisioning) for why that matters. It must start
with a letter or digit and contain only letters, digits and `. _ : -`, and a
duplicate is a conflict rather than an overwrite.

If `source` is omitted, EDG Core stores `manual`.

### Update Asset

Subject: `platform.meta.asset.update`

The update API replaces the asset's mutable metadata fields. Send the complete desired metadata state for the asset.

```json
{
  "id": "pump-101",
  "name": "pump-101",
  "labels": ["line-a", "critical", "inspected"],
  "external_ids": {
    "aas": "aas://example/pump-101"
  },
  "source": "aas",
  "attributes": {
    "manufacturer": "ACME",
    "model": "PX-10",
    "area": "north"
  }
}
```

## Point Provisioning

A *point* is one declared reading on an asset: which address on the field device
it lives at, what the tag is called on the data plane, and how to decode it. EDG
holds the plant's whole tag inventory as master data, so it is queryable,
diffable and backed up in one place instead of living in a file on each adapter
box.

```yaml
# pump-a.yaml — one file per asset
protocol: modbus-tcp        # opaque to the core; names the adapter that reads it
poll_interval_ms: 1000      # omit to leave the adapter's own default alone
points:
  - name: temperature       # matches TagValue.name on the data plane
    value_type: NUMBER      # NUMBER | TEXT | FLAG
    unit: "°C"
    address: "0"            # opaque: a Modbus register, an OPC-UA node id
    encoding:               # protocol-specific, arbitrary JSON
      function: holding
      type: int16
      scale: 0.1
    enabled: true           # omit it and the point is enabled; false keeps the
                            # declaration on record without polling it
```

### Import and export

```bash
# Bulk import a directory of <asset-id>.yaml
edg-core -import-points ./points

# Write every declared list back out
edg-core -export-points ./points
```

Import reports **every** rejected file rather than stopping at the first, so one
pass over a plant gives you the whole list of problems. Files that fail are
skipped; the rest are applied.

**Unknown keys are an error, not ignored.** Pasting a register straight out of an
adapter's `mapping.yaml` puts `function`, `type` and `scale` at the top level of
the point instead of under `encoding:`, and silently discarding them would store
a point with no decode rules while reporting success:

```
pump-a.yaml: line 6: unknown field "function" (protocol-specific settings belong under encoding:)
```

The file name is the asset id. A file whose `asset_id:` disagrees with its name
is rejected rather than resolved in either direction — silently preferring one
is how a whole directory ends up on a single asset.

**Choose the asset id when you create the asset**, and the point file is named
after something you can type:

```bash
curl -X POST -H "Authorization: Bearer $EDG_HTTP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":"pump-a","name":"Pump A","template_name":"pump"}' \
  localhost:8080/api/v1/assets
```

Omit `id` and the server generates a UUID, as it always did. Supplying one
matters for more than filenames: **the asset id is the only join between a
declared asset and the telemetry an adapter publishes for it.** An adapter
publishes whatever its own configuration says — the reference adapters derive
`modbus-127.0.0.1-1` from their host and unit id — so if the declared id is a
UUID, nothing an adapter sends will ever match a declared asset, and with
`unknown_asset_policy: pass_through` the data flows on un-enriched with no
warning.

Set the declared id to what the adapter actually publishes, or configure the
adapter to publish the id you declared. An id must start with a letter or digit
and contain only letters, digits and `. _ : -`.

If you already have assets on generated UUIDs, read them off the API:

```bash
curl -s -H "Authorization: Bearer $EDG_HTTP_TOKEN" \
  localhost:8080/api/v1/assets | jq -r '.data[] | "\(.id)\t\(.name)"'
```

### HTTP API

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/points` | Every declared list |
| `GET` | `/api/v1/points?name=temp` | Search points by name across all assets |
| `GET` | `/api/v1/assets/{id}/points` | One asset's list |
| `PUT` | `/api/v1/assets/{id}/points` | Replace one asset's list |
| `DELETE` | `/api/v1/assets/{id}/points` | Remove one asset's declarations |

The search is the question a plant-wide inventory is actually for: *which boxes
call this tag something else*. It is also in the operator UI's Points section.

Writes need a bearer token like every other mutation, which is why the CLI paths
above exist — a deployment that never sets `http.token_env` can still be
provisioned.

### Things worth knowing

- **Points attach to an asset, not a template.** `template_resources` declares
  what a *kind* of thing reports; a point declares where *this* box's copy
  lives. Two pumps on the same template can and usually do have different
  addresses.
- **A write replaces the list wholesale.** That is the operation an operator
  performs — *here is the list* — and a partial apply is what leaves a plant
  half-provisioned. A point absent from the new list is removed.
- **`version` advances on every write**, including one that changes nothing. It
  is the signal adapters will compare against once distribution lands, and a
  missed bump is worse than a redundant one.
- **`created_at` is preserved per point** across a re-import, so after bulk
  re-importing a spreadsheet you can still see which declarations are new.
- **Adapters do not read these lists yet.** They still load their own
  `mapping.yaml`. Declaring points centrally today gives you the inventory, the
  backup and the review; distribution is the next phase. Until then a point list
  and an adapter's actual configuration can disagree, and nothing detects it.
- **Nothing marks a point writable.** Control is not implemented — neither SDK
  can receive a command — so a `writable` flag would be a field with no readers
  that made the gateway look like it can write to a PLC when it cannot.

## Asset Relations

EDG Core stores directed asset relations and uses them to enrich validated data.
For `partOf` and `locatedIn`, the source asset is the child and the target asset
is the parent or location.

When an asset publishes data, core looks up its `partOf` and `locatedIn`
ancestors before publishing to `platform.data.validated`. Each ancestor with a
`template_name` becomes a metadata tag where the key is `template_name` and the
value is the ancestor asset name. Existing adapter-provided metadata keys are
preserved.

Example validated payload:

```json
{
  "asset_id": "sensor-001",
  "metadata": {
    "equipment": "pump-A",
    "line": "line-3",
    "factory": "factory-1"
  },
  "values": [
    {"name": "temperature", "number": 25.5, "quality": "good"}
  ]
}
```

The default enrichment depth is 10. Metadata cache entries are flushed when core
receives `platform.meta.asset.changed` or `platform.meta.relation.changed`.

Traversal subjects expose the same graph through NATS request/reply:

| Subject | Request | Response |
| --- | --- | --- |
| `platform.meta.asset.ancestors` | `{"asset_id":"sensor-001","relation_types":["partOf"],"max_depth":10}` | `{"nodes":[{"id":"pump-A","name":"pump-A","depth":1}]}` |
| `platform.meta.asset.descendants` | `{"asset_id":"factory-1","relation_types":["partOf"],"max_depth":10}` | `{"nodes":[...]}` |
| `platform.meta.asset.subtree` | `{"asset_id":"factory-1","relation_types":["partOf"],"max_depth":10}` | Recursive tree node with `children` |
| `platform.meta.asset.connected` | `{"asset_id":"pump-A","relation_type":"connectedTo"}` | `{"nodes":[...]}` |

If `relation_types` is omitted for tree traversal, core uses `partOf` and
`locatedIn`. If `relation_type` is omitted for `connected`, core returns all
one-hop relation types.

### Template Constraints

Templates can declare static relationship constraints. These constraints are
checked after relation changes and by the catalog check subject.

```yaml
name: temp-sensor
resources:
  - name: temperature
    valueType: NUMBER
constraints:
  required_relations:
    - type: partOf
      target_template: equipment
      min: 1
      max: 1
  forbidden_relations:
    - type: connectedTo
      target_template: factory
```

`required_relations` counts outgoing relations from the asset to assets with the
target template. `forbidden_relations` rejects any matching outgoing relation.
If `min` is omitted for a required relation, the default is `1`; if `max` is
omitted, there is no upper bound.

The enforcement mode is configured in core YAML:

```yaml
constraints:
  enforcement: warn # warn, enforce, or disabled
```

`warn` is the default and publishes `platform.meta.constraints.violation` while
allowing the metadata write. `enforce` rejects the relation change and rolls it
back. `disabled` skips constraint checks. To inspect the whole catalog, request
`platform.meta.constraints.check` or run:

```bash
edg-core --check-constraints --config /opt/edg/config.yaml
```

## Alarm Impact Analysis

Adapters and internal components can raise alarms by publishing JSON to
`platform.alarm.raised`:

```json
{
  "id": "alarm-001",
  "asset_id": "pump-A",
  "severity": "critical",
  "code": "pump.offline",
  "message": "Pump A offline"
}
```

Core validates that the asset exists, computes downstream impact with the asset
relation graph, and immediately publishes `platform.alarm.impact.computed`.
Downstream impact uses `partOf` and `locatedIn`; one-hop `connectedTo` assets are
included separately in `connected_asset_ids`.

Short alarm floods are grouped in memory. When the grouping window closes, core
publishes `platform.alarm.grouped` with the nearest common ancestor, alarm IDs,
asset IDs, and highest severity in the group.

```yaml
alarm:
  window_seconds: 5
  max_traversal_depth: 10
```

The in-memory window is intentionally short-lived. If the core process restarts,
pending groups that have not yet been emitted are lost; durable alarm history is
outside this PoC scope.

## HTTP Metadata API

EDG Core can expose an HTTP API for browser dashboards and operator tools. Reads
are available anonymously when no token is configured; **writes always require a
non-empty bearer token**. It is disabled by default outside the development config.

```yaml
http:
  enabled: true
  address: 127.0.0.1:8080
  token_env: EDG_HTTP_TOKEN
  cors_allowed_origins:
    - http://localhost:3000
```

If the environment variable named by `token_env` contains a value, requests must
include `Authorization: Bearer <token>`. If the variable is unset, the API is
anonymous and should remain bound to localhost.

> **Known issue ([#107](https://github.com/e7217/edg/issues/107)).** Configuring
> an HTTP token currently makes the embedded operator UI unreachable: the auth
> middleware requires a bearer header on every request including the UI's own
> HTML, which a browser navigation cannot supply. Until that is fixed, a
> deployment that sets a token should set `http.webui_enabled: false`. This is
> independent of the NATS authorization above — that one does not affect the UI.

All responses use the same envelope as NATS metadata replies:

```json
{"success": true, "data": {}}
```

Available endpoints:

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/v1/health` | Process health. |
| `GET` | `/api/v1/version` | Build version metadata. |
| `GET` | `/api/v1/assets?limit=100&offset=0` | List assets. |
| `GET` | `/api/v1/assets/{id}` | Get one asset. |
| `GET` | `/api/v1/assets/{id}/ancestors?relation_types=partOf,locatedIn&max_depth=10` | Traverse parents. |
| `GET` | `/api/v1/assets/{id}/descendants?relation_types=partOf&max_depth=10` | Traverse children. |
| `GET` | `/api/v1/assets/{id}/subtree?relation_types=partOf&max_depth=10` | Recursive tree. |
| `GET` | `/api/v1/assets/{id}/connected?relation_type=connectedTo` | One-hop relation query. |
| `GET` | `/api/v1/relations?source=&target=&type=` | List and filter relations. |
| `POST` | `/api/v1/assets` | Create an asset. **(write — token required)** |
| `PUT` | `/api/v1/assets/{id}` | Replace an asset's metadata. **(write)** |
| `DELETE` | `/api/v1/assets/{id}?source=` | Delete an asset. **(write)** |
| `POST` | `/api/v1/relations` | Create a relation. **(write)** |
| `DELETE` | `/api/v1/relations/{id}?source=` | Delete a relation. **(write)** |

Write requests share the same validation, constraint enforcement, and change
events as the NATS metadata API (both go through the core `MetadataService`).
Errors map to HTTP status codes: `400` validation, `404` not found, `409`
conflict (duplicate name), `422` constraint violation.

Example:

```bash
curl -H "Authorization: Bearer $EDG_HTTP_TOKEN" \
  "http://127.0.0.1:8080/api/v1/assets/factory-1/descendants?relation_types=partOf&max_depth=10"
```

## Operator UI

When `http.webui_enabled` is true, EDG Core serves a small built-in web UI at the
HTTP root (`http://<address>/`) — no separate service or build step. It lists
assets, relations, templates, and constraint violations, and provides simple
create/delete forms.

```yaml
http:
  enabled: true
  webui_enabled: true
```

The UI is embedded in the binary (`go:embed`). Reads load anonymously; **writes
require a bearer token** — paste it into the token field at the top of the page
(stored in the browser's local storage). The UI refreshes on demand via a button;
live push updates are a planned enhancement. Keep the HTTP address bound to
localhost unless a token is configured.

## Metrics

EDG Core serves Prometheus text exposition at `/metrics`. See
[ADR 0009](adr/0009-prometheus-metrics.md) for the design.

```yaml
metrics:
  enabled: true
  # A listener of core's own. Empty means /metrics is served only on the NATS
  # monitoring port, which is loopback by default -- unreachable from a
  # scraper in another container.
  address: 0.0.0.0:9464
```

It is reachable at two addresses:

| Address | When to use it |
| --- | --- |
| `http://<nats.http_host>:<nats.http_port>/metrics` | Always on, loopback by default. Good for `curl` on the box or `docker compose exec`. |
| `http://<metrics.address>/metrics` | The one a scraper uses. Container deployments must set this; the shipped configs use `0.0.0.0:9464`. |

> **`/metrics` is not authenticated, on either address.** Hardening the REST
> API with `http.token_env` does not harden it. It carries no secrets and no
> master data, but it does reveal counts, rates and the build stamp — keep it
> on a trusted network. The bundled `compose.yml` deliberately does not publish
> 9464 to the host; VictoriaMetrics scrapes it over the compose network.

A bind failure is never fatal: core logs it and keeps running. Metrics are how
you learn the gateway is unwell, not a precondition for it running.

### Scraping

VictoriaMetrics scrapes it directly — no separate Prometheus is needed. The
bundled stack ships `deploy/configs/victoriametrics/scrape.yml` and enables it
with `-promscrape.config`. Check the targets with:

```bash
curl -s localhost:8428/api/v1/targets | jq '.data.activeTargets[] | {job: .labels.job, health}'
```

### Where to look first

| Question | Metric |
| --- | --- |
| Is the gateway keeping up? | `edg_core_sink_consumer_pending` |
| Is data being dropped before storage? | `edg_core_sink_messages_acked_total{outcome="poison"}`, `edg_core_data_values_total{kind}` (only `number` reaches VictoriaMetrics) |
| Is ingest about to become a slow consumer? | `edg_core_data_handle_seconds` — if its tail approaches the interval between messages, NATS starts dropping deliveries silently |
| Is the stream about to discard old data? | `edg_core_js_bytes` against `edg_core_js_stream_max_bytes` |
| Is an adapter down? | `edg_core_adapters{availability}` for the fleet, `edg_core_adapter_up{adapter_id}` for one |
| Is the box about to die? | `process_resident_memory_bytes`, `process_open_fds` against `process_max_fds`, `go_goroutines` |
| Is SQLite the bottleneck? | `edg_core_store_connection_wait_seconds_total`, `edg_core_enricher_ancestor_lookup_seconds` |
| Is an alarm storm building? | `edg_core_alarm_groups_pending` — the per-alarm cost grows with it |

`edg_core_adapter_up` is derived from staleness, not from what the adapter last
reported. An adapter that crashed never gets to publish that it stopped, so
`edg_core_adapters_by_run_state` will keep showing it as `running` while
`edg_core_adapter_up` correctly reads 0.

### Cardinality

Series count is a property of the code, not of your plant: about 95 families
and 316 series regardless of how many assets, tags or request paths exist.
`edg_core_metrics_series` reports the current number.

Labels never carry asset ids, tag names or URL paths. The one identifier label
is `adapter_id`, and it is capped:

```yaml
adapters:
  metrics_max_tracked: 200   # -1 exposes only the aggregates
```

Adapters past the cap fold into `adapter_id="__overflow__"` and
`edg_core_adapter_series_dropped_total` records that it happened; the aggregate
`edg_core_adapters` still counts every adapter. **Give adapters stable ids.** An
adapter that generates a fresh id on every restart leaks series until it hits
the cap — the cap is a mitigation, not a fix.

### `/debug/vars`

The fifteen historical `expvar` counters are unchanged and still served at
`/debug/vars` on the monitoring port. Each has a `/metrics` counterpart with the
`_total` suffix, backed by the same storage, so the two can never disagree.
Nothing else is published there — see [ADR 0009](adr/0009-prometheus-metrics.md)
for why that matters on an unauthenticated port.

## Monitoring

- **Metrics**: see [Metrics](#metrics) above.
- **NATS Monitor**: http://localhost:8222 — bound to loopback by default
  (`nats.http_host`). It serves `/varz`, `/connz` and `/debug/vars` with no
  authentication of any kind, so exposing it publicly leaks the subject
  topology. See [ADR 0007](adr/0007-nats-subject-authorization.md).
- **Adapter status**: `GET /api/v1/adapters` and `/api/v1/adapters/drift`, or
  the operator UI. Drift reports two adapters collecting the same asset, and
  adapter clocks more than a minute off core's.
- **VictoriaMetrics UI (vmui)**: http://localhost:8428/vmui — query data and
  explore label cardinality without any extra service.
- **Grafana** (optional, `docker compose --profile grafana up`): http://localhost:3000
- **Logs**:
  - EDG Core: `journalctl -u edg-core -f`
