---
description: "Design decision for OpenBao Config Operator: connection kinds and the tenant boundary. Rationale, constraints and consequences."
---

# 0003. Connection kinds and the tenant boundary

Status: accepted; implemented; amended by [ADR 0010](0010-openbaoserver-owns-address-and-audiences.md) (address and audiences move to OpenBaoServer). Date: 2026-11-13.

## Context

Connection and authentication details repeated in every resource are verbose and drift apart.
A shared connection that logs in with the operator's own token makes the boundary between
tenants only Kubernetes RBAC on who may create a connection: every tenant acts with the
operator's OpenBao policy.

The OpenBao policy behind a token is the only real limit on what a request can do.

## Decision

- `OpenBaoConnection` is namespaced and usable only from its own namespace.
  `ClusterOpenBaoConnection` is cluster-scoped, created by platform admins, and limited by
  `spec.allowedNamespaces`. Resources reference one with `connectionRef: {kind, name}`.
- A namespaced connection that uses Kubernetes or JWT auth must name a `serviceAccountRef` in its
  own namespace. The operator gets a short-lived token for that ServiceAccount through the
  TokenRequest API and logs in with it. It never logs in as its own ServiceAccount for a
  namespaced connection.
- Credentials are read at login time, never at startup. One client per connection is cached,
  keyed by connection UID, generation and a hash of the credential inputs.

## Consequences

- A tenant acts only with the OpenBao policy bound to its own ServiceAccount; the operator's
  identity cannot be borrowed.
- The operator needs `create` on `serviceaccounts/token`. That permission must be documented and
  scoped in the chart.
- The platform team configures one OpenBao role per tenant ServiceAccount.
- Cluster connections are a privileged path and must be guarded by RBAC and `allowedNamespaces`.
