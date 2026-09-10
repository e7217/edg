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

## Unauthenticated NATS → role-based subject authorization

Before this release the embedded NATS server had no authentication and, with an
unset bind host, listened on all interfaces. Anyone who could reach `:4222`
could delete master data, inject forged `platform.data.validated` past
validation, or purge the JetStream stream. See
[ADR 0007](adr/0007-nats-subject-authorization.md).

**Default upgrade path: no action required.** `nats.auth.mode` defaults to
`compat`, which enforces the subject matrix but still accepts anonymous
connections as the least-privileged `legacy` role. Existing adapters and
ADR 0006 fan-out consumers keep working unmodified. A permission violation in
NATS is transient — the connection stays up and only the forbidden subject is
refused — so nothing silently dies.

### What changes immediately, even in compat

| Behaviour | Before | After |
| --- | --- | --- |
| `platform.meta.{asset,relation}.{create,update,delete}` over NATS | anyone | `operator` role only |
| Publishing `platform.data.validated` / `.deadletter` | anyone | `core` only |
| Publishing `platform.meta.*.changed` | anyone | `core` only |
| `$JS.API.STREAM.{DELETE,PURGE}` | anyone | `core` / `operator` only |
| NATS monitoring port (8222) | `0.0.0.0` | `127.0.0.1` |

If an adapter created assets over NATS, move that to the HTTP write API or run
it with the `operator` credential. No shipped adapter or example did this.

**Remote access to `:8222`.** The monitoring port serves `/varz`, `/connz` and
`/debug/vars` with no authentication mechanism. It is now loopback-only; set
`nats.http_host: 0.0.0.0` to restore the old behaviour, understanding what it
exposes. The Docker healthcheck is unaffected — it calls loopback from inside
the container — but `compose.yml` no longer publishes the port to the host.

### Finding clients that need credentials before switching to strict

Credentials are generated on first boot; the banner prints the file path.
Anything publishing to a subject its role lacks appears in the core log:

```bash
journalctl -u edg-core | grep "Publish Violation"
```

That is the migration checklist for clients that do not use an EDG SDK. Once it
is empty, distribute credentials and set `nats.auth.mode: strict`.

```bash
# per-role secret
jq -r .adapter /opt/edg/data/nats-credentials.json
# clients connect with credentials in the URL
nats://adapter:<secret>@edg-core:4222
```

The SDKs need no code change: both already accept a URL, and the generated
secrets are URL-safe.
