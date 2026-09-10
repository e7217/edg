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

- The release bundle could not be built, and the installer it ships could not
  run. `.github/workflows/release.yml` copied a top-level `configs/` directory
  that has not existed since the deployment reorganisation, and
  `scripts/install.sh` copied the same non-existent path — so the packaging
  step aborted on three of four platforms and the installer aborted before
  writing any systemd unit, leaving `systemctl start edg-core` with no unit to
  start. The Windows leg failed one step earlier still, on a VictoriaMetrics
  binary name that asset has never used. None of it had been noticed because
  the release workflow has never run: release-please fails on every push
  without the repository's "Allow GitHub Actions to create and approve pull
  requests" setting, so no version tag has ever been cut (#114, #117).

- A host install silently ran on compiled-in defaults. `install.sh` placed the
  configs where nothing looked for them and the systemd unit passed no
  `-config`, so `nats.auth.mode` was `compat` while the freshly installed
  `config.prod.yaml` said `strict`, and the operator got no indication. The
  installer now links `<install root>/config.yaml` to the selected environment
  config — as the container image already did — and the unit passes `-config`
  explicitly. `EDG_ENV` selects the environment (default `prod`) (#117).

- `edg-core` now logs which configuration file it loaded, or that it found none
  and is using built-in defaults. Both #114 and #117 were invisible for months
  partly because this line did not exist.

- The installer no longer swallows a missing VictoriaMetrics binary with
  `|| true`, which used to report a successful install while writing a systemd
  unit whose `ExecStart` pointed at nothing. It also installs `templates/`,
  without which every templated asset create or update is rejected with
  "template not found".

- `INSTALL_DIR` now actually relocates a host install. The staging and
  production configs name their paths absolutely, and nothing rewrote them, so
  a relocated install put binaries in the chosen root while writing data to
  `/opt/edg` and looking for templates in a directory that did not exist —
  loading zero templates and rejecting every templated asset. The installer
  rewrites the paths and refuses to finish if any still point at the default
  root.

- Re-running the installer no longer discards edited configuration. An
  unchanged file is replaced; a changed one is kept and the shipped version is
  written beside it as `<name>.new`.

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
