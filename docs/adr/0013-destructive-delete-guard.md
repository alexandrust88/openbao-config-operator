# 0013. Destructive-delete guard

Status: accepted. Date: 2026-11-14.

## Context

`deletionPolicy: Delete` on a mount, a namespace or a KV secret removes data, not just
configuration. A CR deleted from Git by mistake, or an update that recreates a mount because a
create-only field changed, can destroy every secret below it. v1 guarded only the removal of human
access in `AccessLayout`.

## Decision

Kinds whose delete destroys data report `Destructive() == true`: `SecretsMount`, `AuthMount`,
`OpenBaoNamespace`, `KVSecret` and `Audit`, and `OpenBaoEndpoint`, whose free-form `path` and
`deletePath` can name a mount or a namespace. They delete only when the CR carries
`openbaoconfig.io/allow-destructive-delete: "true"`; otherwise the finalizer holds with reason
`DeleteGuarded`. The same annotation gates an update that would recreate a mount.

## Consequences

- Destroying data needs two explicit choices: `Delete` and the annotation.
- A guarded CR stays in `Terminating` until someone adds the annotation or switches to `Orphan`.
- Status: annotation and reason defined; engine enforcement in progress.
