# Architecture Decision Records

This directory records EDG architecture decisions that affect runtime behavior,
operator expectations, or compatibility.

## Records

- [ADR 0001: Data Plane Reliability](0001-data-plane-reliability.md)
- [ADR 0002: Ontology Enrichment and Traversal](0002-ontology-enrichment.md)
- [ADR 0003: Relationship-Based Control and Conditional Triggers](0003-relationship-based-control.md)
- [ADR 0004: Ontology Rule Engine](0004-ontology-rule-engine.md)
- [ADR 0005: Built-in VictoriaMetrics Sink](0005-embedded-vm-sink.md)
- ADR 0006: *Withdrawn.* A June draft on the validated-data contract, never
  merged; its validation half became ADR 0010, and the consumer-facing half is
  tracked in [#152](https://github.com/e7217/edg/issues/152).
- [ADR 0007: NATS Subject Authorization](0007-nats-subject-authorization.md)
- [ADR 0008: Adapter Runtime Status](0008-adapter-runtime-status.md)
- [ADR 0009: Prometheus Metrics](0009-prometheus-metrics.md)
- [ADR 0010: The Data Contract of `platform.data.validated`](0010-data-contract.md)
- [ADR 0011: Point Distribution to Adapters](0011-point-distribution.md)

## Format

ADRs use a lightweight MADR-style structure: context, decision, consequences,
and validation. See <https://adr.github.io/madr/> for the full template.
