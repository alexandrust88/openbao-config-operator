The KV mount must exist and its version must match `kvVersion`. Secret input references resolve
within the Kubernetes namespace of the resource.

The resource manages its declared keys, preserving keys owned by other writers. Removing a managed
key and deleting a whole KV path are different operations. Review `deletionPolicy` before deleting
resources: the default `Orphan` retains data, while destructive deletion requires the explicit
allow-destructive-delete annotation. Parent mount or OpenBao namespace deletion bypasses that
per-resource retention choice by deleting the whole container.

Ready confirms reconciliation of the declared values, not application access. Test reads with the
application's identity. Grant read/create/update on `<mount>/data/<path>` for KV v2 (or
`<mount>/<path>` for v1); KV v2 cleanup can also require delete on `<mount>/metadata/<path>`.
