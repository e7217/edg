# ADR 0010: The Data Contract of `platform.data.validated`

- Status: Accepted
- Date: 2026-09-18
- Supersedes: none
- Related: [ADR 0001](0001-data-plane-reliability.md), [ADR 0002](0002-ontology-enrichment.md), [ADR 0005](0005-embedded-vm-sink.md), [ADR 0009](0009-prometheus-metrics.md)

## Context

The subject is called `platform.data.validated`, and only core may publish to it
(ADR 0007). Until this ADR, "validated" meant "decoded as JSON". The
2026-09-14 project review sent five payloads through an isolated core and all
five were published:

| Input | What happened |
| --- | --- |
| `{}` | Published with an empty `asset_id`, timestamp 0 and no values. |
| `"25.5"` as text for a tag the template declares `NUMBER` | Published. `ValidateAssetData` existed but only tests called it. |
| A boolean state | Published, then acked by the sink with nothing written. |
| Adapter metadata `equipment: Wrong Equipment` for an asset whose master data says otherwise | The adapter's value was stored. `Enrich` never overwrote an existing key. |
| A normal number | Stored. |

Three separate promises were broken at once: that validated data is
well-formed, that it agrees with master data, and that what is accepted is
stored. The README claimed "template-driven schema and quality checks applied
before data reaches storage", which was not true.

## Decision

### 1. Two layers of rules

**Envelope rules** hold for every message, whether or not its asset is
declared:

| Rule | Violation reason | Effect |
| --- | --- | --- |
| `asset_id` is not empty | `missing_asset_id` | message rejected |
| `values` is not empty | `no_values` | message rejected |
| `timestamp` is epoch milliseconds | `timestamp_not_milliseconds` | message rejected |
| each value has a name | `empty_name` | value dropped |
| names are unique within the message | `duplicate_name` | later duplicates dropped |
| exactly one of `number`, `text`, `flag` | `no_reading`, `multiple_readings` | value dropped |
| a number is finite | `non_finite_number` | value dropped |
| metadata does not use a key the sink writes (`asset_id`, `name`, `unit`, `quality`, `value`) | `reserved_metadata_key` | key dropped |

A timestamp of 0 or absent is not a violation: the core stamps the time it
received the message and counts it (`edg_core_data_timestamps_filled_total`).
A timestamp below 10¹² is rejected because it is almost certainly seconds — a
seconds value stored as milliseconds lands in January 1970, which is worse than
no data because it looks like data.

**Declaration rules** apply where master data declares a tag. A declaration
comes from the asset's point list (P2) or, failing that, its template's
resources; a point declaration wins because it is about this asset, the
template about its kind.

| Rule | Violation reason | Effect |
| --- | --- | --- |
| the reading's kind matches the declared `value_type` | `type_mismatch` | value dropped |
| the unit matches the declared unit | `unit_mismatch` | value dropped |
| — | — | a missing unit is filled from the declaration |

A unit mismatch is refused, not relabelled: rewriting `77 °F` as `77 °C` stores
a wrong number that looks right. A tag that is not declared passes: master data
that declares nothing constrains nothing, and adapters legitimately send
diagnostics nobody modelled.

### 2. Remove the smallest wrong thing, and never silently

A malformed envelope rejects the message. A malformed value drops that value
and keeps its siblings — one bad register must not take the other forty-nine of
a Modbus poll with it. Every removal:

- increments `edg_core_data_contract_violations_total{reason}`;
- increments `edg_core_data_messages_rejected_total` or
  `edg_core_data_values_dropped_total`;
- publishes a dead-letter record carrying the **original** payload and a
  `violations` array. For a partial drop, what was published is the payload
  minus the listed values.

### 3. Master data is the authority for what it derives

`Enrich` now overwrites adapter metadata for keys that master data derives
(`line`, `equipment`, …) and counts each override in
`edg_core_enrich_metadata_overrides_total`. Keys master data does not derive
are left as the adapter sent them. The review's fifth case was a stale adapter
config silently winning over the plant's actual structure; with this change it
is corrected and counted instead.

### 4. The sink stores every value kind

VictoriaMetrics stores float samples. Each value kind becomes a field and hence
a metric:

| Reading | Line protocol | Metric |
| --- | --- | --- |
| `number` | `number=25.5` | `edg_data_number` |
| `flag` | `flag=1` / `flag=0` | `edg_data_flag` |
| `text` | tag `value=RUNNING`, field `text=1` | `edg_data_text{value="RUNNING"} 1` |

Text becomes a label on a constant sample — the Prometheus "info metric" shape —
so a machine state or alarm code is queryable (`edg_data_text{name="state",value="FAULT"}`).
Its cost is one series per distinct string, so text longer than
`sink.text_max_length` (default 128 bytes) is not written and is counted in
`edg_core_sink_values_skipped_total{reason="text_too_long"}`. Text containing a
line break or backslash, which line protocol cannot carry in a label, is
skipped as `text_unencodable`. `sink.text_values: drop` restores the old
numbers-only behaviour.

### 5. `warn` mode for the upgrade

`data_contract.mode: warn` counts and logs every violation but publishes the
message unchanged, and dead-letters nothing. It exists so a running plant can
measure what `enforce` would remove before turning it on. The default is
`enforce`: a contract that is off by default is the state this ADR replaces.

### 6. Declarations are cached, bounded by master data

The ingest path used to run `AssetExists` against SQLite for every message.
It now resolves an asset's declarations once, caches them, and flushes the
cache on `platform.meta.asset.changed` and the new
`platform.meta.points.changed`. Undeclared ids are never cached, so the cache
is bounded by the number of declared assets, not by whatever ids adapters send.
A template import takes effect on restart, as it already did for the
`TemplateLoader`.

`platform.meta.points.changed` is core-only (ADR 0007) and is also the signal an
adapter will re-read its point list on once point distribution lands.

## Consequences

- An adapter publishing seconds timestamps stops reaching storage. That is the
  intended outcome — its data was being stored in 1970 — and the dead letter
  says why. Both SDKs publish milliseconds.
- An adapter whose units disagree with master data loses those values until one
  of them is corrected. `warn` mode finds these before the switch.
- `edg_data_flag` and `edg_data_text` are new metrics; dashboards that relied on
  booleans being absent are unaffected.
- `outcome="poison"` on `edg_core_sink_messages_acked_total` now means a batch
  in which nothing was encodable (unencodable text, or `text_values: drop`),
  not merely "nothing numeric".
- A failed declaration lookup lets the message through with envelope checks
  only, counted in `edg_core_data_contract_lookup_failures_total`. A database
  hiccup must not dead-letter a declared asset's data, nor apply the undeclared
  policy to it.

## Alternatives Considered

| Alternative | Reason rejected |
| --- | --- |
| Reject the whole message on any violation | One bad register would discard a whole poll. Industrial payloads are wide. |
| Coerce `"25.5"` to 25.5 | Hides the adapter bug the declaration exists to catch, and a coercion rule per type pair is a second contract nobody reads. |
| Store text in a separate log store | Adds a service; ADR 0005's point is one node, one sink. The label form covers states and codes, which is what plants report as text. |
| Keep adapter metadata authoritative | Makes a stale adapter config indistinguishable from the plant's structure. |
| Default to `warn` | Leaves "validated" meaning "decoded" for everyone who does not read this ADR. |
