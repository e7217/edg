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
sudo systemctl start edg-core
sudo systemctl start edg-telegraf

# Enable auto-start on boot
sudo systemctl enable edg-core
sudo systemctl enable edg-telegraf
```

### Manual Start (No Systemd)

If you are not using systemd, you can start components manually:

```bash
# Start EDG Core
/opt/edg/bin/edg-core &

# Start Telegraf
/opt/edg/bin/telegraf --config /opt/edg/configs/telegraf/telegraf.conf &
```

### Custom Installation Directory

```bash
INSTALL_DIR=/custom/path ./install.sh
```

## Configuration

### EDG Core
- **Data Storage**: `./data/metadata.db` (auto-created)
- **Schema Migrations**: embedded migrations run automatically on startup
- **Templates**: `./templates/` (optional)
- **Config file**: set `EDG_CORE_CONFIG` or pass `--config` to choose a core YAML file.

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

### Telegraf
Configuration file: `/opt/edg/configs/telegraf/telegraf.conf`

**Key Settings:**
- Input: NATS (`platform.data.validated`)
- Output: VictoriaMetrics (`http://localhost:8428`)
- Parser: `json_v2` (handles nested arrays)

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

### Asset Registration Modes

EDG Core controls unknown asset handling with `asset_registration.mode`.

| Mode | Behavior |
| --- | --- |
| `auto` | Default. Unknown `asset_id` values from data messages create Asset records with `source: "auto"` and publish an asset-created metadata event. |
| `manual` | Unknown `asset_id` values continue through the validated data subject, but EDG Core does not create Asset records or publish asset-created metadata events. |

```yaml
asset_registration:
  mode: auto
```

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

EDG Core can expose a read-only HTTP API for browser dashboards and operator
tools. It is disabled by default outside the development config.

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

Example:

```bash
curl -H "Authorization: Bearer $EDG_HTTP_TOKEN" \
  "http://127.0.0.1:8080/api/v1/assets/factory-1/descendants?relation_types=partOf&max_depth=10"
```

## Monitoring

- **NATS Monitor**: http://localhost:8222
- **VictoriaMetrics UI**: http://localhost:8428
- **Grafana** (optional, docker-compose): http://localhost:3000
- **Logs**:
  - EDG Core: `journalctl -u edg-core -f`
  - Telegraf: `journalctl -u edg-telegraf -f`
