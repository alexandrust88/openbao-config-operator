# 0004. Creation and deletion policy defaults

Status: superseded in part by [ADR 0015](0015-create-and-orphan-by-default.md) (both defaults). Date: 2026-11-13.

## Context

An operator that overwrites an existing object without asking, and always deletes it when the
CR is deleted, can take over or remove configuration nobody meant to hand over. Two policies make
this explicit: `creationPolicy` (`Create`, `Adopt`, `CreateOrAdopt`) and `deletionPolicy`
(`Delete`, `Orphan`), with an "acquired" marker in status that records whether the operator
created or adopted the object.

Most users of this operator move existing OpenBao configuration under GitOps. With `Create`,
every existing object needs an explicit `Adopt` first. A blanket `Orphan` leaves stale policies
and roles behind, which keeps access open.

## Decision

- `creationPolicy` defaults to `CreateOrAdopt`. `status.acquired` (`Created` or `Adopted`) marks
  the object as ours; `Create` fails with reason `Conflict` when the object exists and is not ours.
- `deletionPolicy` defaults per kind through `Adapter.DefaultDeletionPolicy`: `Delete` for leaf
  configuration (policies, roles, quotas), `Orphan` for objects that hold data or identities
  (namespaces, mounts, entities, groups, KV secrets).
- A finalizer is added only for `Delete`. If the connection or a credential is missing at
  deletion, the finalizer stays and the reason is `CleanupBlocked`.

## Consequences

- Importing existing configuration needs no extra field; adoption is visible in status and events.
- A typo in a name can adopt an unrelated object. `Create` exists for teams that want to prevent
  that.
- Deleting a CR removes access it granted, but never destroys data unless the user chose `Delete`
  on a data-holding kind.

## Update (2026-11-14)

The deletion default is now `Orphan` for every kind, set by a CRD default on the shared spec
(design rule 6 in [architecture](../architecture.md#design-rules)). `Delete` is opt-in per object. A
CR deleted from Git must never silently remove an auth mount or a policy. The per-kind default
described above no longer applies.
