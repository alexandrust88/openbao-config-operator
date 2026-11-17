---
description: "Design decision for OpenBao Config Operator: no OpenBao-to-Kubernetes secret sync. Rationale, constraints and consequences."
---

# 0007. No OpenBao-to-Kubernetes secret sync

Status: accepted, still accurate for v2 (2026-11-14). Date: 2026-11-13.

## Context

A configuration operator could also read OpenBao paths and render Kubernetes Secrets with
templates. External Secrets Operator (ESO) and other secret sync operators already do this, with refresh, templating and many providers. Copying secret values into Kubernetes
spreads them to etcd, backups and anyone with Secret read access.

## Decision

The operator configures OpenBao. Generic continuous delivery of OpenBao values into Kubernetes
Secrets belongs to ESO. Resource-specific outputs can use `writeSecretRef`, and the operator owns
that destination Secret. These include one-time values such as an AppRole secret-id or the key
shares of a sealable namespace. `RandomSecret` also supports an output Secret for the KV value it
generates or adopts, including recovery of that owned output. This bounded resource behaviour is
not a general-purpose secret synchronisation API.

`KVSecret` can write values from same-namespace Kubernetes Secret references into OpenBao; it does
not provide the reverse projection. Values in source, output or ESO-managed Kubernetes Secrets
are Kubernetes data and may be present in etcd and its backups.

## Consequences

- Users combine this operator with ESO: this operator writes the policy and the Kubernetes auth
  role, and ESO reads the secret with them.
- No generic secret sync kind, and no secret values in CR status or logs.
- Choose one owner for each destination Kubernetes Secret: do not combine `writeSecretRef` and
  ESO projection for the same target.
- One-time outputs must be written before status is updated, so a crash cannot lose them without
  a trace; adapters for these kinds need care and tests.
