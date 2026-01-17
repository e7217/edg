# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 1.0.0 (2026-01-17)


### Features

* Add CI/CD pipeline for automatic deployment to dev server ([#16](https://github.com/e7217/edg/issues/16)) ([09e8a4b](https://github.com/e7217/edg/commit/09e8a4bbb24c8c31d8fb87266d26a294da0d8223))
* Add release-please automation infrastructure ([#36](https://github.com/e7217/edg/issues/36)) ([8647dbf](https://github.com/e7217/edg/commit/8647dbf8b4bae2dff60622f427981c10735c3e8e))
* auto-create data directory for SQLite store ([#8](https://github.com/e7217/edg/issues/8)) ([3258206](https://github.com/e7217/edg/commit/32582063949e2a92238708701eb645cef2ba425b))
* bundle VictoriaMetrics binary and add license notices ([#14](https://github.com/e7217/edg/issues/14)) ([23ebf93](https://github.com/e7217/edg/commit/23ebf93c7088aa3620aeb3b6719c51213b7f2e81)), closes [#13](https://github.com/e7217/edg/issues/13)
* implement Telegraf integration with VictoriaMetrics ([#9](https://github.com/e7217/edg/issues/9)) ([#11](https://github.com/e7217/edg/issues/11)) ([ebde886](https://github.com/e7217/edg/commit/ebde8862eba407993c32af71344ccc6a34288c8f))
* migrate to Docker-based deployment with self-hosted runner ([#18](https://github.com/e7217/edg/issues/18)) ([10d3f4d](https://github.com/e7217/edg/commit/10d3f4d2d1a17f8f87c20d54b9d0e1de949c8031))


### Bug Fixes

* Add missing rdfs and rdf prefixes to JSON-LD context ([#43](https://github.com/e7217/edg/issues/43)) ([261adc7](https://github.com/e7217/edg/commit/261adc74c62f4e3bf226a65cd80d681c2b1fa12d))
* correct Telegraf environment variable substitution syntax ([#20](https://github.com/e7217/edg/issues/20)) ([c63e206](https://github.com/e7217/edg/commit/c63e206f95c0f4e7f96bbb3341bb5b27b3c9d035)), closes [#19](https://github.com/e7217/edg/issues/19)
* disable VictoriaMetrics healthcheck due to missing tools ([#21](https://github.com/e7217/edg/issues/21)) ([#22](https://github.com/e7217/edg/issues/22)) ([5383d87](https://github.com/e7217/edg/commit/5383d87b2a5ff5674be369ec417d5d79abb01a99))
* Handle JSON marshaling errors in meta_handler reply function ([#42](https://github.com/e7217/edg/issues/42)) ([0a85884](https://github.com/e7217/edg/commit/0a85884fcc8455338f5ee349879cd20281615237))
* Handle JSON marshaling errors in store.go (closes [#2](https://github.com/e7217/edg/issues/2)) ([#41](https://github.com/e7217/edg/issues/41)) ([c4e652e](https://github.com/e7217/edg/commit/c4e652e77149e2a3b2af0cb8e35aadeaa4289ac0))

## [Unreleased]

### Added

- Initial EDG Platform Core implementation with embedded NATS server
- Metadata storage system using SQLite database
- Template loading and management system
- Data handler for asset data processing via NATS subjects
- Meta handler for metadata operations
- Automatic release infrastructure with release-please
- Version information display with `--version` flag
- Cross-platform build support (Linux, macOS, Windows for amd64 and arm64)
- Telegraf integration for metrics collection
- VictoriaMetrics integration for time-series data storage
- Docker Compose deployment configuration

### Changed

### Deprecated

### Removed

### Fixed

### Security
