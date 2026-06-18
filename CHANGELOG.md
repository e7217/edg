# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 1.0.0 (2026-06-18)


### ⚠ BREAKING CHANGES

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
* **core:** extract MetadataService from MetaHandler (Phase 1) ([#99](https://github.com/e7217/edg/issues/99)) ([e967c96](https://github.com/e7217/edg/commit/e967c9604d26f17f83968be5308b8edb8c3f9817))
* **core:** make templates DB-authoritative with import/export (Phase 4) ([#102](https://github.com/e7217/edg/issues/102)) ([4aa0d7e](https://github.com/e7217/edg/commit/4aa0d7ec7fcf398ca2e2e3fba5557d10641910c6))
* **core:** remove auto-registration, add unknown_asset_policy (Phase 2) ([#100](https://github.com/e7217/edg/issues/100)) ([9a89a5d](https://github.com/e7217/edg/commit/9a89a5da2483c11073e09ad8ba26d840b4a953f1))
* document data plane reliability ([#65](https://github.com/e7217/edg/issues/65)) ([#69](https://github.com/e7217/edg/issues/69)) ([3f40868](https://github.com/e7217/edg/commit/3f408686ae4fc4994318f5801bb0816f340a71f4))
* Enable NATS JetStream for message persistence ([#48](https://github.com/e7217/edg/issues/48)) ([5062afd](https://github.com/e7217/edg/commit/5062afdbf0a6ef999282c6bd1f8eef21f983eb04))
* extend asset metadata model ([#66](https://github.com/e7217/edg/issues/66)) ([#68](https://github.com/e7217/edg/issues/68)) ([c1d2284](https://github.com/e7217/edg/commit/c1d228453045277d324e3320f7409ff795de05d9))
* **grafana:** Improve legend format with asset_id, name, and unit (closes [#54](https://github.com/e7217/edg/issues/54)) ([#55](https://github.com/e7217/edg/issues/55)) ([8cc62a9](https://github.com/e7217/edg/commit/8cc62a9581b762042f8d98122a7da2a16fce8159))
* **httpapi:** add embedded operator UI + template/constraint reads (Phase 5) ([#103](https://github.com/e7217/edg/issues/103)) ([94d94e5](https://github.com/e7217/edg/commit/94d94e5c593b4e5840a4f43ac05c6bd90ece9445))
* **httpapi:** add master-data write endpoints + auth hardening (Phase 3) ([#101](https://github.com/e7217/edg/issues/101)) ([737f901](https://github.com/e7217/edg/commit/737f9012abd2d7034d20ff9654e33c62d5b016e9))
* implement Telegraf integration with VictoriaMetrics ([#9](https://github.com/e7217/edg/issues/9)) ([#11](https://github.com/e7217/edg/issues/11)) ([ebde886](https://github.com/e7217/edg/commit/ebde8862eba407993c32af71344ccc6a34288c8f))
* migrate to Docker-based deployment with self-hosted runner ([#18](https://github.com/e7217/edg/issues/18)) ([10d3f4d](https://github.com/e7217/edg/commit/10d3f4d2d1a17f8f87c20d54b9d0e1de949c8031))
* publish metadata change events ([#67](https://github.com/e7217/edg/issues/67)) ([#70](https://github.com/e7217/edg/issues/70)) ([950d20f](https://github.com/e7217/edg/commit/950d20f5a551b7b4083ae83c8eb8428e29f47f71))
* **python-sdk:** Add device connection recovery framework (closes [#58](https://github.com/e7217/edg/issues/58)) ([#59](https://github.com/e7217/edg/issues/59)) ([e26040b](https://github.com/e7217/edg/commit/e26040b141a0d6be1df1ed530a546c8d5b4ba23b))
* **sink:** replace Telegraf with built-in VictoriaMetrics sink ([#97](https://github.com/e7217/edg/issues/97)) ([77fa548](https://github.com/e7217/edg/commit/77fa548568760b797fef6abdff04ffb05f2dfaed))


### Bug Fixes

* Add missing rdfs and rdf prefixes to JSON-LD context ([#43](https://github.com/e7217/edg/issues/43)) ([261adc7](https://github.com/e7217/edg/commit/261adc74c62f4e3bf226a65cd80d681c2b1fa12d))
* correct Telegraf environment variable substitution syntax ([#20](https://github.com/e7217/edg/issues/20)) ([c63e206](https://github.com/e7217/edg/commit/c63e206f95c0f4e7f96bbb3341bb5b27b3c9d035)), closes [#19](https://github.com/e7217/edg/issues/19)
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

- The `JetStream -> storage` hop now honours the durable, ack-after-write
  boundary described in ADR 0001. The former Telegraf `queue_group` subscription
  did not replay the JetStream backlog after downtime; the built-in durable
  consumer does.

### Security
