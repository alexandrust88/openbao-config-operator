# Concepts

A summary of [architecture v2](architecture.md), the source of truth for the design. Read this
page first, then the full design when you need the detail.

## What the operator does

The operator keeps the configuration of an existing OpenBao server in the state that Kubernetes
custom resources declare. One custom resource is one object at one OpenBao API path: a policy, an
auth role, a mount, an entity. It does not deploy, initialise, unseal or upgrade OpenBao, and it
does not sync secret values into Kubernetes ([ADR 0007](adr/0007-no-secret-sync.md)). v2 targets
OpenBao 2.6 and later only ([ADR 0009](adr/0009-openbao-only-target.md)).

## Servers, connections and the tenant boundary

| kind | scope | owner | holds |
|------|-------|-------|-------|
| `OpenBaoServer` | cluster | platform | address, CA bundle, allowed namespaces and token audiences, rate limit |
| `OpenBaoConnection` | namespaced | tenant | `serverRef`, OpenBao namespace, how to log in |
| `ClusterOpenBaoConnection` | cluster | platform | the same, plus `allowedNamespaces`; may use the operator's own identity |

Every resource names a connection in `spec.connectionRef`. A tenant cannot choose the address,
borrow the operator's identity or mint a token the Kubernetes API server accepts; the OpenBao
policy bound to the tenant's ServiceAccount is the limit of what it can configure
([ADR 0003](adr/0003-connections-and-tenant-boundary.md),
[ADR 0010](adr/0010-openbaoserver-owns-address-and-audiences.md)). The operator keeps one
authenticated client per connection and renews its token instead of logging in on every reconcile.

## Common spec and status

Every kind embeds the same fields:

| field | default | meaning |
|-------|---------|---------|
| `connectionRef` | required | the connection to use; immutable |
| `namespace` | `""` | OpenBao namespace relative to the connection's; immutable |
| `creationPolicy` | `Create` | `Create`, `Adopt` or `CreateOrAdopt` an existing object ([ADR 0015](adr/0015-create-and-orphan-by-default.md)) |
| `deletionPolicy` | `Orphan` | `Orphan` leaves the object; `Delete` removes it with the resource |
| `driftDetection`, `driftInterval` | `Enabled`, tier default | how often the object is re-read |
| `managementPolicy` | `Manage` | `Observe` reads and reports drift, never writes ([ADR 0016](adr/0016-observe-mode-and-import-tool.md)) |

Status carries `Ready` (OpenBao matches the spec) and `Synced` (the last reconcile succeeded)
conditions with typed reasons and `observedGeneration`, plus the full OpenBao `path`, the
absolute `namespace`, `acquired` and an HMAC of write-only inputs.

## The engine

One generic reconciler serves every kind; each kind supplies a small adapter with its path, its
desired payload and its comparison rules ([ADR 0002](adr/0002-generic-engine-with-adapters.md)).
The engine enforces the design rules:

1. A steady-state reconcile reads and does not write.
2. The spec is the whole desired state: every typed field is compared
   ([ADR 0011](adr/0011-full-field-comparison.md)).
3. One path has one owner; a second resource on the same path gets `Conflict`
   ([ADR 0012](adr/0012-ownership-path-index-and-marker.md)).
4. Create by default, adopt on request; retain by default, delete on request. Mounts, namespaces,
   KV data and audit devices also need the annotation
   `openbaoconfig.io/allow-destructive-delete: "true"`
   ([ADR 0013](adr/0013-destructive-delete-guard.md)).
5. Secrets only by reference: secret inputs come from Kubernetes Secrets and never appear in a
   spec, status, event or log.
6. No root token at runtime.

Drift is checked in two tiers, every 15 minutes for access-critical kinds and every hour for the
rest, inside a per-server request budget ([ADR 0014](adr/0014-drift-tiers-and-request-budget.md)).
A write circuit breaker caps unexplained rewrites ([ADR 0018](adr/0018-write-circuit-breaker.md))
and the `openbaoconfig.io/paused` annotation stops reconciling ([ADR 0019](adr/0019-pause-annotation.md)).

## Kinds

Typed kinds cover the objects compiled into OpenBao, in the groups `core`, `sys`, `auth`,
`secrets`, `identity` and `access`, all `v1alpha1` under the provisional domain `openbaoconfig.io`
([ADR 0008](adr/0008-provisional-api-group-domain.md)). Kinds for external plugins are in the
code but disabled behind `--enable-plugin-kinds`. `OpenBaoEndpoint` writes any payload to any
path for everything without a typed kind ([ADR 0006](adr/0006-endpoint-escape-hatch.md)).
Validation lives in the CRD schema (OpenAPI and CEL), with no admission webhooks
([ADR 0005](adr/0005-cel-validation-without-webhooks.md)).

The `access` group builds on the primitives: `AccessLevel` names a set of capabilities,
`AccessGrant` gives workloads access through Kubernetes auth roles and
[templated policies](templated-policies.md), and `AccessLayout` gives people access through OIDC
groups.

See the [kind catalogue](kinds.md) for the status of each kind and the
[API reference](reference/api/index.md) for every field.
