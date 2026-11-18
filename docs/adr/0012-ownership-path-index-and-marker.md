---
description: "Design decision for OpenBao Config Operator: ownership: path index and server-side marker. Rationale, constraints and consequences."
---

# 0012. Ownership: path index and server-side marker

Status: accepted; implemented (path index and markers on entities, groups and namespaces, and the KV version 2 metadata marker). Date: 2026-11-14.

Update 2026-11-18: the KV version 2 metadata marker is no longer planned. The `KVSecretMetadata` kind writes and reads it in `custom_metadata` (`internal/controller/secrets/kvsecretmetadata.go`). `KVSecret` and `RandomSecret` write no marker and rely on `status.acquired` and the path index.

## Context

v1 marks ownership only in `status.acquired`. Two CRs on the same path both believe they own it
and overwrite each other on every reconcile. After status loss (restore, re-create) the operator
cannot tell its own object from a foreign one.

## Decision

Ownership is checked in order: a server-side marker (managed-by, cluster id, CR UID) where the API
has metadata (entities, groups, namespaces, KV v2 metadata), then `status.acquired` with a matching
`status.path`. A field index on server + absolute namespace + path finds duplicates: the older CR
keeps the path, the newer gets `Conflict` and never writes.

## Consequences

- Two tenants cannot fight over one path inside one cluster.
- After status loss, marked kinds recover without writes; other kinds need a one-time `Adopt`.
- Which kinds can carry a marker must be tested per kind.
- Status: implemented for the path index and markers on entities, groups and namespaces. The KV v2
  metadata marker remains planned.
