# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Documented the data plane reliability model in ADR 0001
- Added configurable JetStream stream policy and dead-letter subject handling
- Added Go chaos regressions for JetStream backlog recovery, discard pressure,
  concurrent asset auto-registration, and dead-letter publication
- Initial EDG Platform Core implementation with embedded NATS server
- Metadata storage system using SQLite database
- Template loading and management system
- Data handler for asset data processing via NATS subjects
- Meta handler for metadata operations
- Automatic release infrastructure with release-please
- Version information display with `--version` flag
- Cross-platform build support (Linux, macOS, Windows for amd64 and arm64)
- Built-in VictoriaMetrics sink: a durable JetStream consumer in core writes
  validated data to VictoriaMetrics, replacing the external Telegraf bridge
  (ADR 0005). Override the endpoint with `EDG_SINK_URL`.
- VictoriaMetrics integration for time-series data storage
- Docker Compose deployment configuration
- Asset metadata extensions for external IDs, source tracking, attributes, and update timestamps
- SQLite metadata schema migrations with embedded migration files
- Asset update metadata API subject (`platform.meta.asset.update`)
- Metadata change events for asset and relation create/update/delete notifications
- Go SDK for adapter development at `adapters/go/sdk` with feature parity to the Python SDK
- Modbus TCP reference adapter examples for Python (`adapters/python/examples/modbus_tcp`) and Go (`adapters/go/sdk/examples/modbus_tcp_sensor`) with YAML-driven register mapping, configurable word order, and integration tests against in-process Modbus TCP servers
- Configurable asset registration mode for choosing automatic metadata creation or manual asset governance

### Changed

- The VictoriaMetrics metric name is now `edg_data_number` (was
  `nats_consumer_number`, which leaked the Telegraf input plugin name into the
  data schema). Existing series remain queryable until retention expiry.

### Deprecated

### Removed

- Telegraf is no longer bundled or required. Its Docker image, systemd unit,
  config, and release-pipeline steps have been removed.

### Fixed

- The bundled `docker compose` stack could not start from a clean volume. The
  image bakes `config.prod.yaml`, whose data paths were `/var/lib/edg` — a
  directory nothing creates, in an image whose data volume is mounted at
  `/opt/edg/data` and which runs as a non-root user. `restart: unless-stopped`
  turned the failure into a silent crash loop. The staging and production
  configs now use `/opt/edg`, which is the install root that
  `scripts/install.sh`, the container image and the compose volume already
  agree on, and `scripts/compose-smoke.sh` runs in CI so the stack cannot break
  this way unnoticed again (#114).

  **Migration.** Only affects a deployment that passed
  `-config .../config.prod.yaml` or `config.staging.yaml` by hand and created
  `/var/lib/edg` itself; the container stack never ran, so it has no data to
  move. Move the old directory before upgrading:

  ```bash
  sudo systemctl stop edg-core
  sudo mv /var/lib/edg/* /opt/edg/data/
  ```

- `templates.dir` in the staging and production configs pointed at
  `/etc/edg/templates`, which no deployment creates either, so first boot
  seeded no templates and logged only a warning.

- `deploy/docker/compose.yml` declared its `service-net` network as `external`,
  which requires `docker network create service-net` out of band. That step was
  documented nowhere, so `docker compose up` failed outright on any machine
  that did not already have one lying around. Compose now creates and owns the
  network, pinned to the same name so a separately started adapter container
  can still join it with `--network service-net`.

- The `JetStream -> storage` hop now honours the durable, ack-after-write
  boundary described in ADR 0001. The former Telegraf `queue_group` subscription
  did not replay the JetStream backlog after downtime; the built-in durable
  consumer does.

### Security
