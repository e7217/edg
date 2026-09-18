# Capacity Baseline

What one EDG node sustained, measured end to end: the real `edg-core` binary,
its built-in sink writing to a real VictoriaMetrics, driven through the Go SDK
by `edg-loadgen`. Every number below is from `scripts/capacity.sh`; the raw
results are in [`results/2026-09-18-ladder.jsonl`](results/2026-09-18-ladder.jsonl).

## Environment

| | |
| --- | --- |
| CPU | AMD Ryzen 7 8845HS, 8 vCPU (VM) |
| Memory | 9 GiB, about 3 GiB free during the runs |
| OS | Linux 7.0 |
| EDG | this branch; `data_contract: enforce`, `auth.mode: compat`, value logging off |
| VictoriaMetrics | v1.133.0 in Docker, `-dedup.minScrapeInterval=1ms` |
| Placement | **core, VictoriaMetrics and the load generator on the same machine** |

The last row matters: above about 200k points/s the three compete for the same
eight cores, so the ceiling below is the ceiling of this box running all of
it, not of the core alone. Treat it as a lower bound for a dedicated gateway.

## Results (steady load, 60 s per step)

Each simulated asset publishes one message per second carrying N numeric
values. *Latency* is from the reading's timestamp, set when it is sent, to its
arrival on `platform.data.validated` — adapter-to-core, data contract,
enrichment and the JetStream publish ack. *Stored* compares the lines the sink
wrote to VictoriaMetrics with the points sent.

| Load | msg/s | points/s | Loss | Stored | Latency p50 / p95 / p99 | Core CPU | Core RSS |
| --- | ---: | ---: | ---: | ---: | --- | ---: | ---: |
| 100 assets × 20 | 100 | 2,000 | 0 | 100% | 0 / 1 / 1 ms | 9% | 62 MB |
| 500 × 40 | 500 | 20,000 | 0 | 100% | 0 / 1 / 1 ms | 34% | 101 MB |
| 1,000 × 40 | 1,000 | 40,000 | 0 | 100% | 0 / 1 / 1 ms | 56% | 115 MB |
| 2,000 × 50 | 2,000 | 100,000 | 0 | 100% | 0 / 1 / 2 ms | 93% | 212 MB |
| **4,000 × 50** | **4,000** | **200,000** | **0** | **100%** | **1 / 3 / 20 ms** | 149% | 161 MB |
| 6,000 × 50 | 6,000 | 300,000 | 0 | 100% | 4 / 382 / 571 ms | 192% | 189 MB |
| 8,000 × 50 | 8,000 | 400,000 | **15.4%** | 84.6% | 4.4 / 8.5 / 17.8 s | 205% | 377 MB |
| 2,000 × 200 | 2,000 | 400,000 | 0 | 100% | 1 / 4 / 9 ms | 140% | 370 MB |

CPU is in units of one core (200% = two cores busy). The first step was run
twice: in the ladder it measured p95 3.1 s with a 25 ms handle p99, a
cold-start artifact of the first container on the box; the rerun above is the
representative one. Both are in the raw results.

## What it means

**Sizing.** A plant of 100 devices × 100 tags at 1 s is 10,000 points/s, well
inside the flat part of the curve: about a fifth of one core, latency ≤ 1 ms.
The box above held **200,000 points/s at 4,000 messages/s with a p99 of 20 ms
and nothing lost**.

**The limit is messages, not points.** 2,000 × 200 values (400k points/s) is
comfortable; 8,000 × 50 (the same points/s) is not. Each message costs a
decode, the data contract, enrichment and a synchronous JetStream publish on
the single ingest goroutine. Batch values into fewer, larger messages — one per
device per poll, which is what the SDKs already do.

**Overload loses data at the adapter-to-core hop, and it is now visible.**
Past the ingest goroutine's pace, messages queue in the NATS client until its
pending limit, and then the client drops them. Adapter-to-core is plain NATS
(ADR 0001), so nothing retries. At 8,000 × 50 that was 15.4% of readings. The
core now counts them in **`edg_core_data_messages_dropped_total`** — before
this, the only symptom was a gap in storage. The latency climb at 6,000 × 50
(p95 382 ms) is the queue building before that point: alert on
`edg_core_data_handle_seconds` and the drop counter, not on loss downstream.

**Storage kept up at every step.** The sink wrote at the offered rate, its
backlog never exceeded a few messages, and every accepted point was stored
exactly once.

## One fix made on the way

The first ladder measured **p95 347 ms / p99 424 ms at 200k points/s**. The
ingest handler wrote a log line per value — 200,000 synchronous log writes a
second on the ingest goroutine — plus a line per message from an undeclared
asset. Value logging is now off unless `log_data_values: true` (the dev config
sets it), and the undeclared-asset line is rate-limited to one per 10 s:

| 4,000 × 50 | p95 | p99 | CPU | RSS |
| --- | ---: | ---: | ---: | ---: |
| before | 347 ms | 424 ms | 184% | 252 MB |
| after | 3 ms | 20 ms | 149% | 161 MB |

## Reproducing

```bash
scripts/capacity.sh results.jsonl                      # the default ladder
LADDER="1000:40:1s 4000:50:1s" DURATION=60s scripts/capacity.sh out.jsonl
```

Each step gets a fresh core and a fresh VictoriaMetrics. `edg-loadgen` also runs
on its own against any core (`-profile steady|ramp|burst`, `-help`).

Not measured yet: a dedicated gateway with the load generator on another host,
ARM boards, and SD-card storage. Those are the numbers to take before quoting a
specific device.
