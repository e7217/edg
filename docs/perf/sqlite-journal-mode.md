# SQLite Journal Mode

EDG keeps master data in SQLite and reads it while it writes it: the ingest
path looks an asset up per message and traverses relations on a cache miss,
provisioning reads span several statements, and the metrics collector counts
rows. Under SQLite's rollback journal — the default this replaces — a reader's
shared lock blocks the writer and the writer blocks readers. `_busy_timeout`
made the loser wait instead of failing; it did not remove the contention.

This is the measurement behind [#130](https://github.com/e7217/edg/issues/130)
and the decision that came out of it.

## Method

`TestJournalModeContention` in `internal/core/store_journal_test.go`, on a
file-backed database (a `:memory:` one cannot be WAL at all, so it cannot
speak for production):

- 200 assets, each `partOf` a line and carrying a point list;
- 8 reader goroutines cycling the three reads the gateway actually does —
  `AssetExists`, `GetAncestors`, `GetPointList`;
- 2 writer goroutines replacing point lists through `MetadataService`;
- 10 seconds per configuration.

```bash
EDG_BENCH=1 go test ./internal/core -run TestJournalModeContention -v
```

Machine: AMD Ryzen 7 8845HS, 8 vCPU, NVMe, Linux 7.0.

## Result

| journal / synchronous | reads/s | read p50 | read p95 | read p99 | writes/s | write p99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| delete / full (the old default) | 54 | 3.2 ms | 930 ms | **2.03 s** | 398 | 4.5 ms |
| **wal / full (the new default)** | **25,476** | 63 µs | 1.1 ms | **1.4 ms** | 684 | 3.7 ms |
| wal / normal | 25,984 | 63 µs | 1.1 ms | 1.5 ms | 1,867 | 5.9 ms |

Reads: **472× the throughput and a p99 1,450× lower**. The rollback journal's
read p99 of two seconds is what a point-list read looked like while two writers
were working — on the same goroutine the ingest path uses.

## Decision

**`journal_mode: wal`, `synchronous: full` by default.**

WAL at `full` already collects the whole read benefit, so the default changes
concurrency and *not* durability: every commit is still fsynced. `normal`
roughly triples write throughput by dropping one fsync per commit, at the cost
of losing the last commits (never the database) on a power cut. That is a
deliberate trade for a gateway on an SD card, so it is a setting rather than
the default:

```yaml
storage:
  journal_mode: wal     # or delete, SQLite's rollback journal
  synchronous: full     # or normal, off
```

The mode is a property of the database file: an existing deployment converts on
first start with the new binary, and setting `journal_mode: delete` converts
back.

## What this changes for backups

WAL keeps recent commits in `metadata.db-wal` beside the database, so
**copying `metadata.db` alone can miss them** — and the file is still there
after a crash, which is exactly when a backup matters. Either:

- take a logical backup: `edg-core -export-plant ./plant` (templates, assets,
  relations and points as CSVs — also diffable and portable between sites); or
- copy `metadata.db`, `metadata.db-wal` and `metadata.db-shm` together with the
  core stopped; or
- `sqlite3 metadata.db ".backup backup.db"` while it runs.

## The driver changed with it

The numbers above were taken with the cgo driver. EDG now uses
`modernc.org/sqlite`, SQLite transpiled to Go, so that a cross-compiled binary
works at all (#79). Same bench, same machine:

| | reads/s | read p99 | writes/s |
| --- | ---: | ---: | ---: |
| cgo, wal/full | 25,476 | 1.4 ms | 684 |
| pure Go, wal/full | 19,888 | 1.9 ms | 501 |
| pure Go, delete/full | 294 | 333 ms | 351 |

About 22% fewer reads and 27% fewer writes, and 1.7 MB more binary. Against the
rollback journal it is still 68× the reads, and it is the difference between a
binary that runs on an ARM board and one that exits at startup.

## Not measured

SD-card and eMMC write amplification, which is the real argument for
`synchronous: normal` on edge boards. That needs the hardware.
