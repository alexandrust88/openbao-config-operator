---
description: "Bound KV v2 destructive cleanup to a persisted version cutoff, retaining metadata and newer writes."
---

# 0024. Bounded KV history cleanup

Status: proposed for this release. Date: 2026-11-17.

## Context

KV v2 metadata deletion removes all versions and has no compare-and-set condition.
A concurrent write after an empty read can therefore be lost (TD-008).

## Decision

For `KVSecret` and `RandomSecret`, keep the existing owned-path and destructive-annotation
guards. Remove managed keys with CAS, then persist the observed empty version in the optional
`status.purgeThroughVersion` before any irreversible history destruction. The existing
`ErrInProgress` engine path persists this checkpoint and schedules another pass.

Later passes read version metadata and destroy only not-yet-destroyed versions at or below
the checkpoint, in batches of at most 64. They neither remove keys again nor extend the
checkpoint. Failed batches can be retried idempotently. Metadata and newer versions remain.
The [OpenBao KV v2 API](https://openbao.org/api-docs/secret/kv/kv-v2/) supports explicit-version
destruction but no conditional metadata deletion.

## Consequences

- Connections need metadata `read` and destroy `update`, rather than metadata `delete`.
- Retained metadata keeps version numbering and per-path configuration. A new resource can
  reuse the path with CAS, but does not claim it created the historical path.
- A missing permission blocks cleanup; there is no unsafe fallback to metadata deletion.
- Removing destructive permission after checkpointing prevents further destruction.
- Administrators must not delete/recreate metadata during cleanup: resetting version numbers
  is not guarded atomically by OpenBao. This change protects concurrent version writes, not
  arbitrary administrative replacement of a path. Older historical values remain within the
  explicitly authorised destructive scope.
- An older manager must not process a pending checkpoint; upgrade/downgrade instructions
  describe the additional permissions and this boundary.
