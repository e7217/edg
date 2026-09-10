# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 1.0.0 (2026-09-10)


### ⚠ BREAKING CHANGES

* **core:** nats.http_host now defaults to 127.0.0.1. The NATS monitoring port (/varz, /connz, /debug/vars) has no authentication mechanism, so it is no longer bound to all interfaces; set nats.http_host explicitly to restore remote access. Master-data writes over NATS now require the operator role, and publishing platform.data.validated directly is denied to every role but core. `nats.auth.mode: off` restores the previous behaviour but is refused on a non-loopback nats.host.
* **core:** the `asset_registration.mode` config (auto|manual) is removed and replaced by `unknown_asset_policy` (pass_through|dead_letter). Existing configs that set asset_registration are ignored with a warning.
* **sink:** replace Telegraf with built-in VictoriaMetrics sink ([#97](https://github.com/e7217/edg/issues/97))

### Features

* add alarm impact analysis ([#85](https://github.com/e7217/edg/issues/85)) ([706f6f1](https://github.com/e7217/edg/commit/706f6f1ea00bdedc401a11986139cdfa47951819))
* add asset registration modes ([#82](https://github.com/e7217/edg/issues/82)) ([#83](https://github.com/e7217/edg/issues/83)) ([19d22f8](https://github.com/e7217/edg/commit/19d22f81c8fa1c8e95d964db72d659ae54b91e37))
* Add CI/CD pipeline for automatic deployment to dev server ([#16](https://github.com/e7217/edg/issues/16)) ([09e8a4b](https://github.com/e7217/edg/commit/09e8a4bbb24c8c31d8fb87266d26a294da0d8223))
* add Go SDK for adapter development ([#71](https://github.com/e7217/edg/issues/71)) ([#72](https://github.com/e7217/edg/issues/72)) ([3b12b9c](https://github.com/e7217/edg/commit/3b12b9c2da465b4b296aa998a5564f409abb1dc4))
* add Modbus TCP reference adapter examples ([#77](https://github.com/e7217/edg/issues/77)) ([#78](https://github.com/e7217/edg/issues/78)) ([f88d689](https://github.com/e7217/edg/commit/f88d6890c2458aa7a41bf721259df5167f187152))
* add ontology enrichment traversal ([#84](https://github.com/e7217/edg/issues/84)) ([278712c](https://github.com/e7217/edg/commit/278712c522d3119ca26ab9461d8098cd73ae4bc5))
* add optional Grafana to docker-compose ([#46](https://github.com/e7217/edg/issues/46)) ([a79dd33](https://github.com/e7217/edg/commit/a79dd3324248725bae439b1e5720ceeaae0f70d3))
* add read-only HTTP metadata API ([#88](https://github.com/e7217/edg/issues/88)) ([1a15605](https://github.com/e7217/edg/commit/1a156055c3908131f9aeae2f8fed8483e543ac80))
* Add release-please automation infrastructure ([#36](https://github.com/e7217/edg/issues/36)) ([8647dbf](https://github.com/e7217/edg/commit/8647dbf8b4bae2dff60622f427981c10735c3e8e))
* add template constraint validation ([#86](https://github.com/e7217/edg/issues/86)) ([50ceec0](https://github.com/e7217/edg/commit/50ceec0d654845c2f0128c010b4948e78e047f0b))
* auto-create data directory for SQLite store ([#8](https://github.com/e7217/edg/issues/8)) ([3258206](https://github.com/e7217/edg/commit/32582063949e2a92238708701eb645cef2ba425b))
* bundle VictoriaMetrics binary and add license notices ([#14](https://github.com/e7217/edg/issues/14)) ([23ebf93](https://github.com/e7217/edg/commit/23ebf93c7088aa3620aeb3b6719c51213b7f2e81)), closes [#13](https://github.com/e7217/edg/issues/13)
* **core,sdk:** adapter runtime status plane (platform.adapter.*) ([#113](https://github.com/e7217/edg/issues/113)) ([7a63ce3](https://github.com/e7217/edg/commit/7a63ce393ad6e14568b2ef93e1068f008a2ef75b))
* **core:** extract MetadataService from MetaHandler (Phase 1) ([#99](https://github.com/e7217/edg/issues/99)) ([e967c96](https://github.com/e7217/edg/commit/e967c9604d26f17f83968be5308b8edb8c3f9817))
* **core:** make templates DB-authoritative with import/export (Phase 4) ([#102](https://github.com/e7217/edg/issues/102)) ([4aa0d7e](https://github.com/e7217/edg/commit/4aa0d7ec7fcf398ca2e2e3fba5557d10641910c6))
* **core:** remove auto-registration, add unknown_asset_policy (Phase 2) ([#100](https://github.com/e7217/edg/issues/100)) ([9a89a5d](https://github.com/e7217/edg/commit/9a89a5da2483c11073e09ad8ba26d840b4a953f1))
* **core:** role-based NATS subject authorization ([#111](https://github.com/e7217/edg/issues/111)) ([2eec645](https://github.com/e7217/edg/commit/2eec64551651ffd7eac742d2aaf51b1025011bd3))
* document data plane reliability ([#65](https://github.com/e7217/edg/issues/65)) ([#69](https://github.com/e7217/edg/issues/69)) ([3f40868](https://github.com/e7217/edg/commit/3f408686ae4fc4994318f5801bb0816f340a71f4))
* Enable NATS JetStream for message persistence ([#48](https://github.com/e7217/edg/issues/48)) ([5062afd](https://github.com/e7217/edg/commit/5062afdbf0a6ef999282c6bd1f8eef21f983eb04))
* extend asset metadata model ([#66](https://github.com/e7217/edg/issues/66)) ([#68](https://github.com/e7217/edg/issues/68)) ([c1d2284](https://github.com/e7217/edg/commit/c1d228453045277d324e3320f7409ff795de05d9))
* **grafana:** Improve legend format with asset_id, name, and unit (closes [#54](https://github.com/e7217/edg/issues/54)) ([#55](https://github.com/e7217/edg/issues/55)) ([8cc62a9](https://github.com/e7217/edg/commit/8cc62a9581b762042f8d98122a7da2a16fce8159))
* **httpapi:** add embedded operator UI + template/constraint reads (Phase 5) ([#103](https://github.com/e7217/edg/issues/103)) ([94d94e5](https://github.com/e7217/edg/commit/94d94e5c593b4e5840a4f43ac05c6bd90ece9445))
* **httpapi:** add master-data write endpoints + auth hardening (Phase 3) ([#101](https://github.com/e7217/edg/issues/101)) ([737f901](https://github.com/e7217/edg/commit/737f9012abd2d7034d20ff9654e33c62d5b016e9))
* implement Telegraf integration with VictoriaMetrics ([#9](https://github.com/e7217/edg/issues/9)) ([#11](https://github.com/e7217/edg/issues/11)) ([ebde886](https://github.com/e7217/edg/commit/ebde8862eba407993c32af71344ccc6a34288c8f))
* **metrics:** Prometheus /metrics endpoint with a stdlib-only registry ([#116](https://github.com/e7217/edg/issues/116)) ([d032bfc](https://github.com/e7217/edg/commit/d032bfcb393966fb6630b07b72b6b97223253849))
* migrate to Docker-based deployment with self-hosted runner ([#18](https://github.com/e7217/edg/issues/18)) ([10d3f4d](https://github.com/e7217/edg/commit/10d3f4d2d1a17f8f87c20d54b9d0e1de949c8031))
* publish metadata change events ([#67](https://github.com/e7217/edg/issues/67)) ([#70](https://github.com/e7217/edg/issues/70)) ([950d20f](https://github.com/e7217/edg/commit/950d20f5a551b7b4083ae83c8eb8428e29f47f71))
* **python-sdk:** Add device connection recovery framework (closes [#58](https://github.com/e7217/edg/issues/58)) ([#59](https://github.com/e7217/edg/issues/59)) ([e26040b](https://github.com/e7217/edg/commit/e26040b141a0d6be1df1ed530a546c8d5b4ba23b))
* **sink:** replace Telegraf with built-in VictoriaMetrics sink ([#97](https://github.com/e7217/edg/issues/97)) ([77fa548](https://github.com/e7217/edg/commit/77fa548568760b797fef6abdff04ffb05f2dfaed))


### Bug Fixes

* Add missing rdfs and rdf prefixes to JSON-LD context ([#43](https://github.com/e7217/edg/issues/43)) ([261adc7](https://github.com/e7217/edg/commit/261adc74c62f4e3bf226a65cd80d681c2b1fa12d))
* **ci:** verify SDK, example and Python modules; align Go versions ([#110](https://github.com/e7217/edg/issues/110)) ([959c23a](https://github.com/e7217/edg/commit/959c23a5e712fd7fedf877d38fc34ee37ae6adf7))
* correct Telegraf environment variable substitution syntax ([#20](https://github.com/e7217/edg/issues/20)) ([c63e206](https://github.com/e7217/edg/commit/c63e206f95c0f4e7f96bbb3341bb5b27b3c9d035)), closes [#19](https://github.com/e7217/edg/issues/19)
* **deploy:** make the compose stack start, and add a smoke test that proves it ([#118](https://github.com/e7217/edg/issues/118)) ([4007e7c](https://github.com/e7217/edg/commit/4007e7c06a39765c35bf7c300dc8f333ea722320))
* **deploy:** make the release bundle build and the installer work ([#120](https://github.com/e7217/edg/issues/120)) ([a8d4f53](https://github.com/e7217/edg/commit/a8d4f53a1abae7481a8c9e6571bb228d4b7b6049))
* **deploy:** stop .env.example steering the compose build to the dev config ([#119](https://github.com/e7217/edg/issues/119)) ([7725bae](https://github.com/e7217/edg/commit/7725bae2b10fa4e446c6bc981cb76b01f783c1d0))
* disable VictoriaMetrics healthcheck due to missing tools ([#21](https://github.com/e7217/edg/issues/21)) ([#22](https://github.com/e7217/edg/issues/22)) ([5383d87](https://github.com/e7217/edg/commit/5383d87b2a5ff5674be369ec417d5d79abb01a99))
* Fix Grafana provisioning path in CI deployment (closes [#49](https://github.com/e7217/edg/issues/49)) ([#50](https://github.com/e7217/edg/issues/50)) ([5b67220](https://github.com/e7217/edg/commit/5b672206db8e412320c2780fdcc994185f2598b4))
* Handle JSON marshaling errors in meta_handler reply function ([#42](https://github.com/e7217/edg/issues/42)) ([0a85884](https://github.com/e7217/edg/commit/0a85884fcc8455338f5ee349879cd20281615237))
* Handle JSON marshaling errors in store.go (closes [#2](https://github.com/e7217/edg/issues/2)) ([#41](https://github.com/e7217/edg/issues/41)) ([c4e652e](https://github.com/e7217/edg/commit/c4e652e77149e2a3b2af0cb8e35aadeaa4289ac0))
* Use absolute path for Grafana provisioning in CI (closes [#51](https://github.com/e7217/edg/issues/51)) ([#52](https://github.com/e7217/edg/issues/52)) ([505336d](https://github.com/e7217/edg/commit/505336d6b3f7d7e0f772ac12110f53aafa0eb28d))

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
