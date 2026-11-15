# 0021. One writer cluster per OpenBao namespace

Status: accepted. Date: 2026-11-14.

## Context

Two Kubernetes clusters running the operator against the same OpenBao namespace, for example
during a cluster migration, both believe they own the same objects. The path index
([ADR 0012](0012-ownership-path-index-and-marker.md)) only sees CRs in its own cluster.

## Decision

One OpenBao namespace has one writer cluster. The server-side marker carries a cluster id
(operator flag `--cluster-id`) and the CR UID; a marker from another cluster gives `Conflict`.

## Consequences

- Cross-cluster fights are detected for kinds that carry a marker; other kinds rely on the stated
  rule.
- A cluster migration moves ownership explicitly: `Observe` on the new cluster, `Orphan` and
  delete on the old one, then `Adopt`.
- Status: implemented for objects with server-side ownership markers; other kinds still rely on
  Kubernetes status and the local path index.
