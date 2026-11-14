# 0016. Observe mode and import tool

Status: accepted. Date: 2026-11-14.

## Context

Teams adopt the operator on a running OpenBao with thousands of objects. Adopting everything at
once risks rewrites from small spec mistakes; adopting nothing until every CR is right blocks
progress.

## Decision

`spec.managementPolicy` is `Manage` (default) or `Observe`. `Observe` reads and reports drift
(`Synced` reason `Drifted` with the differing keys, or `Observed` when equal), never writes or
deletes and adds no finalizer. An import CLI, `cmd/obc-import`, reads a live server and emits CR
YAML with `managementPolicy: Observe` and `creationPolicy: Adopt`.

## Consequences

- Adoption is incremental: import, observe until no drift, then switch objects to `Manage`.
- The import tool must cover every typed kind's read shape; gaps fall back to `OpenBaoEndpoint`.
- Status: field and reasons defined in `api/common/v1alpha1`; engine support and CLI in progress.
