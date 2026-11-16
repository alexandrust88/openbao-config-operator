---
description: "Design decision for OpenBao Config Operator: one generic engine with per-kind adapters and FieldSpec. Rationale, constraints and consequences."
---

# 0002. One generic engine with per-kind adapters and FieldSpec

Status: accepted; the comparison rule (only the keys the spec sets) is superseded by [ADR 0011](0011-full-field-comparison.md). Date: 2026-11-13.

## Context

A controller per kind repeats finalizer, condition and deletion logic, and once several kinds
are hand-written the comparison of desired with observed state drifts apart: struct-tag walkers,
hand-built maps and per-kind equivalence functions in one code base. The catalogue has more than
a hundred kinds, so that repetition does not scale.

The same comparison problems come back for every kind: fields that are never returned by a read,
fields accepted only on create, keys renamed between read and write (`token_period` and
`period`), unordered lists, and durations written as `"1h"` and read as `3600`.

## Decision

`internal/engine` holds one generic reconciler. Each kind supplies an `Adapter` with `Path`,
`Desired`, `Fields` and `DefaultDeletionPolicy` (`internal/engine/contract.go`). Kinds that do not
fit "write a payload to a path" add optional `Observer`, `Writer`, `Deleter` or `StatusWriter`
implementations. `FieldSpec` declares `WriteOnly`, `CreateOnly`, `Aliases`, `Sets` and
`Durations`, and the engine compares only the keys the spec sets.

## Consequences

- A new kind is a types file plus a small adapter; finalizers, conditions, drift, events and
  metrics come from the engine.
- Comparison rules are data, so they can be unit-tested per kind against recorded reads.
- Kinds with multi-step flows (PKI issuers, mounts with tune) need the optional interfaces; if
  many kinds need them, the interfaces must grow rather than adapters bypassing the engine.
- Write-only fields cannot be compared, so drift in them is detected only through
  `status.writeOnlyHash` when the input changes.
