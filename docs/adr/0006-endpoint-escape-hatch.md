---
description: "Design decision for OpenBao Config Operator: endpoint escape hatch. Rationale, constraints and consequences."
---

# 0006. Endpoint escape hatch

Status: accepted; only OpenBao paths are in scope per [ADR 0009](0009-openbao-only-target.md). Date: 2026-11-13.

## Context

No typed catalogue covers every OpenBao path, and new OpenBao releases add paths. Without an
escape hatch, users wait for a typed kind or manage those paths with another tool.
Rejecting arbitrary paths keeps the API narrow, but leaves those users without a path. A generic
endpoint needs switches for paths that cannot be read or deleted, for fields a read omits, and for
write-only fields.

## Decision

Ship `OpenBaoEndpoint` in phase 1 in the `core` group. It writes a payload to a path through the same
engine, with `path`, optional `readPath` and `deletePath`, `data` or `dataSecretRef`,
`writeOnlyFields`, `ignoreAbsentFields`, `disableRead` and `disableDelete`. It uses the same
connection, tenant boundary and lifecycle policies as typed kinds. Because `path` and `deletePath`
are free-form, it is a destructive kind ([ADR 0013](0013-destructive-delete-guard.md)):
`deletionPolicy: Delete` needs the `openbaoconfig.io/allow-destructive-delete: "true"` annotation.

## Consequences

- The operator can manage any OpenBao path from the first release, including features that are
  not typed.
- An `OpenBaoEndpoint` has no schema validation of its payload and no cross-resource references; a typed
  kind is the better choice when one exists.
- Secret material must come from `dataSecretRef`, never from `data`, because `data` is stored in
  the CR.
- Tenants are limited by their OpenBao policy, not by the kind; platform teams that want to block
  `OpenBaoEndpoint` use Kubernetes RBAC.
