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

# Telegraf → Built-in VM Sink

`edg-core` now writes validated data to VictoriaMetrics through a built-in
durable sink (see [ADR 0005](adr/0005-embedded-vm-sink.md)). Telegraf is no
longer bundled or required.

## Existing systemd deployments

Remove the leftover Telegraf unit after upgrading the core binary:

```bash
sudo systemctl disable --now edg-telegraf
sudo rm -f /etc/systemd/system/edg-telegraf.service
sudo systemctl daemon-reload
```

The sink is enabled by default and writes to `http://localhost:8428`. Point it
elsewhere with the config (`sink.url`) or the `EDG_SINK_URL` environment
variable. To keep using an external Telegraf instead, set `sink.enabled: false`
in the core config — `platform.data.validated` remains published for any
external consumer.

## Metric name change

The stored metric is now `edg_data_number` instead of `nats_consumer_number`.

- Grafana dashboards shipped with EDG use the `$metric` template variable and
  need no change.
- Custom queries or dashboards referencing `nats_consumer_number` must be
  updated. Series written before the upgrade stay queryable under the old name
  until VictoriaMetrics retention expires.

## Docker Compose

Grafana now sits behind a `grafana` profile. The default `docker compose up`
runs only core and VictoriaMetrics; use `docker compose --profile grafana up`
to include Grafana. Inspect data without Grafana via vmui at
`http://localhost:8428/vmui`.
