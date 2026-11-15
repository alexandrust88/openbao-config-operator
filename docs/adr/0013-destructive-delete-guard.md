# 0013. Destructive-delete guard

Status: accepted. Date: 2026-11-14.

## Context

`deletionPolicy: Delete` on a mount, a namespace or a KV secret removes data, not just
configuration. A CR deleted from Git by mistake, or an update that recreates a mount because a
create-only field changed, can destroy every secret below it. v1 guarded only the removal of human
access in `AccessLayout`.

## Decision

Kinds whose delete destroys data report `Destructive() == true`: `SecretsMount`, `AuthMount`,
`OpenBaoNamespace`, `KVSecret`, `RandomSecret` and `Audit`, and `OpenBaoEndpoint`, whose free-form `path` and
`deletePath` can name a mount or a namespace. They delete only when the CR carries
`openbaoconfig.io/allow-destructive-delete: "true"`; otherwise the finalizer holds with reason
`DeleteGuarded`. The same annotation gates an update that would recreate the object.

## Update (2026-11-15)

Key material is data too: deleting a transit key, a PKI issuer or key, an SSH CA key or a TOTP key destroys what
was encrypted, issued or enrolled with it, and a `TransitKey` delete first sets `deletion_allowed`, so OpenBao's
own guard does not stop it. Deleting an MFA method erases what users enrolled with it, such as every TOTP secret
of a TOTP method; the guard is per kind, so every MFA method type is guarded. Deleting an entity erases the MFA
TOTP secrets enrolled for it. `TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`, `MFAMethod` and `Entity`
now report `Destructive() == true` and need the same annotation for `Delete`.

A guarded resource whose Kubernetes Namespace is terminating cannot wait for the annotation: holding the finalizer
would block the Namespace forever. The engine then leaves the object in OpenBao, records a Warning event with
reason `Orphaned` and removes the finalizer, so a deleted Namespace destroys nothing. In a live Namespace the
resource stays `DeleteGuarded`.

## Consequences

- Destroying data needs two explicit choices: `Delete` and the annotation.
- A guarded CR in a live Namespace stays in `Terminating` until someone adds the annotation or switches to
  `Orphan`; in a terminating Namespace it is orphaned.
- Status: implemented in the engine and covered by reconciler and envtest cases.
