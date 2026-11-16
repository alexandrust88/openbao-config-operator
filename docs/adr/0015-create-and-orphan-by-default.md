---
description: "Design decision for OpenBao Config Operator: create by default and orphan by default. Rationale, constraints and consequences."
---

# 0015. Create by default and orphan by default

Status: accepted. Date: 2026-11-14.

Supersedes the defaults of [ADR 0004](0004-creation-and-deletion-policy-defaults.md).

## Context

ADR 0004 chose `creationPolicy: CreateOrAdopt`, so a typo in a name silently adopts an unrelated
object, and two CRs on one path both adopt it. It chose per-kind deletion defaults, later changed
to `Orphan` everywhere. Adoption and deletion change what the operator owns and should be explicit.

## Decision

- `creationPolicy` defaults to `Create`: an existing object that is not ours gives `Conflict`.
  `Adopt` and `CreateOrAdopt` are explicit. Bulk adoption goes through the import tool
  ([ADR 0016](0016-observe-mode-and-import-tool.md)).
- `deletionPolicy` defaults to `Orphan` for every kind through a CRD default. `Delete` is opt-in;
  destructive kinds also need the guard annotation ([ADR 0013](0013-destructive-delete-guard.md)).

## Consequences

- Importing existing configuration needs `Adopt` or `CreateOrAdopt` per object.
- Config paths that always read back with defaults (for example `KVConfig`, `PKIConfig`) can never
  be created and need `Adopt` or `CreateOrAdopt`.
- A CR removed from Git leaves its OpenBao object; stale policies and roles must be cleaned up
  deliberately.
- Status: implemented (CRD defaults in `api/common/v1alpha1`).
