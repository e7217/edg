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
   - Copy configs to `/opt/edg/configs/`
   - Create systemd services (Linux only)

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
INSTALL_DIR=/custom/path ./install.sh
```

## Configuration

### EDG Core
- **Data Storage**: `./data/metadata.db` (auto-created)
- **Schema Migrations**: embedded migrations run automatically on startup
- **Templates**: stored in SQLite (authoritative). The `templates.dir` directory
  (default `./templates/`) is **seed-imported on first boot** when the DB has no
  templates, and is the default target for the import/export commands below.
- **Config file**: set `EDG_CORE_CONFIG` or pass `--config` to choose a core YAML file.

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
failure envelope to `platform.data.deadletter`. Monitor these expvar counters on
the core process:

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
```

Each numeric value becomes one metric named `edg_data_number`, tagged with
`asset_id`, `name`, `unit`, `quality`, and any enrichment metadata. Adapter
timestamps (epoch milliseconds) are preserved.

Sink health is exposed via expvar on the core process:

- `edg_core_sink_lines_written`
- `edg_core_sink_batches_written`
- `edg_core_sink_write_failures`
- `edg_core_sink_decode_failures`

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

Either way, an undeclared-asset counter (`edg_core_undeclared_assets`, expvar) is
incremented for operator visibility.

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

## Monitoring

- **NATS Monitor**: http://localhost:8222
- **VictoriaMetrics UI (vmui)**: http://localhost:8428/vmui — query data and
  explore label cardinality without any extra service.
- **Grafana** (optional, `docker compose --profile grafana up`): http://localhost:3000
- **Logs**:
  - EDG Core: `journalctl -u edg-core -f`
