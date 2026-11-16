---
description: "Design decision for OpenBao Config Operator: pause annotation. Rationale, constraints and consequences."
---

# 0019. Pause annotation

Status: accepted. Date: 2026-11-14.

## Context

During an incident, a server migration or a restore, operators need to stop the operator from
touching some objects or a whole server without scaling it down for every tenant.

## Decision

`openbaoconfig.io/paused: "true"` on a managed object, its Kubernetes Namespace or its
`OpenBaoServer` stops reconciling: no reads, writes or deletes. A deletion keeps the finalizer.
The object reports reason `Paused`.

## Consequences

- Scope is chosen by where the annotation goes: one object, one tenant namespace or one server.
- A paused object shows stale status; the reason makes that visible.
- Status: implemented for resource, Namespace and OpenBaoServer annotations in the engine.
