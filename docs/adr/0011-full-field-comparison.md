---
description: "Design decision for OpenBao Config Operator: full-field comparison with declared defaults. Rationale, constraints and consequences."
---

# 0011. Full-field comparison with declared defaults

Status: accepted; implemented. Date: 2026-11-14.

## Context

The v1 engine compares only the keys `Desired` returns. Removing a field from the spec therefore
never unsets it in OpenBao: the old value stays and the object reports `Ready`. The spec is not the
whole desired state.

## Decision

`Desired` returns the full payload: every typed field, with omitted optional fields at the server
default that the adapter declares. Fields are classed as normal, set, alias, write-only,
write-once, create-only or server-owned, and the engine compares by class. An integration test
per kind writes an empty spec and asserts that a second reconcile makes no write; a default test
sets and removes each optional field. `OpenBaoEndpoint` keeps comparing only its data keys.

## Consequences

- Removing a field converges the server back to the default.
- Every adapter must declare defaults, and a wrong default shows as a write on every reconcile;
  the no-write test catches it.
- An operator upgrade that changes a declared default rewrites objects; the write circuit breaker
  ([ADR 0018](0018-write-circuit-breaker.md)) bounds that.
