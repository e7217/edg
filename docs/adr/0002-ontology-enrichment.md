# ADR 0002: Ontology Enrichment and Traversal

## Status

Accepted

## Context

EDG already stores asset relations using `partOf`, `connectedTo`, and
`locatedIn`, but validated data and operator tooling could not use those
relations directly. Operators need line, equipment, area, and factory tags on
time-series data without duplicating that context in every adapter payload.

The first step should keep EDG lightweight: no graph database, no new HTTP API,
and no extra data-plane service hop.

## Decision

Core enriches data in `HandleAssetData` before publishing
`platform.data.validated`. The enrichment follows `partOf` and `locatedIn`
ancestors only. For each ancestor, `template_name` becomes the metadata key and
the asset name becomes the metadata value. If `template_name` is missing, core
uses `ancestor_<depth>`.

Core also exposes graph traversal through NATS request/reply subjects:

| Subject | Meaning |
|---|---|
| `platform.meta.asset.ancestors` | Walk source-to-target through selected relation types. |
| `platform.meta.asset.descendants` | Walk target-to-source through selected relation types. |
| `platform.meta.asset.subtree` | Return descendants as a recursive tree. |
| `platform.meta.asset.connected` | Return one-hop connected assets in either direction. |

SQLite recursive CTEs implement traversal. The data-plane enricher keeps an
in-memory asset-to-tag cache. Asset or relation metadata events flush the cache
conservatively.

## Consequences

Validated data gains optional `metadata` keys. Existing consumers remain
compatible because `metadata` was already part of the payload and is only added
when relationships exist.

Traversal stays inside the existing NATS metadata plane. An HTTP facade can be
added later without changing Store traversal semantics.

Telegraf and VictoriaMetrics deployments must watch tag cardinality. The v1
enrichment rule uses stable template names as tag keys to avoid creating tag
keys from asset IDs or display names.

## Validation

The core test suite includes regression coverage for:

- Ancestor, descendant, subtree, connected, max-depth, relation-filter, missing
  asset, and cycle traversal cases.
- Enrichment output, cache hit/miss behavior, metadata event invalidation, and
  concurrent access.
- DataHandler validated payload enrichment.
- NATS request/reply traversal subjects.
