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
- Telegraf integration for metrics collection
- VictoriaMetrics integration for time-series data storage
- Docker Compose deployment configuration
- Asset metadata extensions for external IDs, source tracking, attributes, and update timestamps
- SQLite metadata schema migrations with embedded migration files
- Asset update metadata API subject (`platform.meta.asset.update`)

### Changed

### Deprecated

### Removed

### Fixed

### Security
