# Metadata Migrations

EDG Core embeds SQLite metadata migrations in the binary.

## Startup

On startup, `edg-core` applies pending migrations before opening the metadata store:

```bash
edg-core
```

The migration source is currently embedded, so deployments do not need to ship a separate migrations directory. Config files still expose `storage.migrations_dir: embedded` to reserve the setting for future external migration sources.

## Rollback

Rollback is explicit because down migrations can remove data:

```bash
edg-core --migrate-down 1
```

The command rolls back the metadata database at `./data/metadata.db` by the requested number of migration steps and exits.

## Operational Notes

- Back up `metadata.db` before rolling back production data.
- Keep app binaries and migrations aligned; do not run an older binary against a database migrated by a newer binary unless a rollback has been completed.
- Migration v2 adds `external_ids`, `source`, `attributes`, and `updated_at` to `assets`.
