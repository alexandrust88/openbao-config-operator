# Why this operator exists

Several tools already manage Vault-style configuration from Kubernetes. This page explains what they leave
out for a platform team that runs OpenBao for many tenants, why that gap led to a new operator, and where the
older tools remain the better choice. It is not a feature list: the [kind catalogue](../kinds.md) and
[API coverage](../api-coverage.md) say what the operator covers, and the [known limitations](../reference/limitations.md)
say what it does not do yet.

The comparison covers the two projects closest in purpose:

- [Crossplane provider-vault](https://github.com/upbound/provider-vault) from Upbound, read at commit
  [`356438d`](https://github.com/upbound/provider-vault/tree/356438dabec87abf768a2b3c2cd1a95c648679dd)
  (2026-11-12). It is generated with upjet from terraform-provider-vault 5.9.0 (`Makefile`) and runs the
  Terraform provider in process.
- [redhat-cop vault-config-operator](https://github.com/redhat-cop/vault-config-operator), read at tag `v1.0.5`
  (commit [`3072fd2`](https://github.com/redhat-cop/vault-config-operator/tree/3072fd2d72fccc6f59087bba84b983c94f4d729e),
  2026-10-23).

External Secrets Operator comes last, because it solves a different problem. OpenTofu run by tofu-controller and
the bank-vaults configurer are compared briefly in the
[README](https://github.com/alexandrust88/openbao-config-operator#today-and-its-limits).

## The problem

A platform team runs one or a few OpenBao servers. Each tenant team gets a Kubernetes namespace and an OpenBao
namespace, and should configure its own policies, auth roles and mounts from Git without a ticket. The platform
team must keep three decisions to itself: which server a tenant's credentials are sent to, which OpenBao identity
a tenant acts as, and what happens to shared or data-bearing objects. The configuration must stay as declared
without the tool rewriting thousands of objects on every pass, and a server that already has configuration must
be taken over without surprises.

## Built for Vault, tested on Vault

Both projects target HashiCorp Vault. A search of both repositories, and of terraform-provider-vault, finds no
mention of OpenBao. That matters most for namespaces, the unit of tenancy in this design:

- vault-config-operator documents namespaces as a Vault Enterprise feature
  ([`docs/namespaces.md`](https://github.com/redhat-cop/vault-config-operator/blob/3072fd2d72fccc6f59087bba84b983c94f4d729e/docs/namespaces.md)),
  and its authentication section notes that Vault Community Edition ignores the namespace.
- provider-vault describes the `namespace` field of every managed resource as "Target namespace. (requires
  Enterprise)", and the Terraform provider documents its namespace resource as available only with Vault
  Enterprise.

OpenBao ships namespaces in its open source release, and the API is close enough that these tools may work
against it. Neither project tests that, and neither has kinds for what only OpenBao has: self-init,
`sys/workflows`, JWT roles with CEL, or OCI plugins. In return both carry Vault Enterprise kinds that OpenBao
does not serve; provider-vault, for example, has EGP and RGP Sentinel policies and the KMIP engine.

This operator takes the opposite bet: OpenBao only, minimum 2.6, with no flavor detection
([ADR 0009](../adr/0009-openbao-only-target.md)). Namespaces get a kind of their own that handles the
asynchronous delete, kinds that OpenBao serves only in the root namespace refuse a child namespace before any
call, and the OpenBao-only features have kinds ([architecture](../architecture.md#openbao-native-features)).

## Who decides where a tenant's credentials go

A tenant boundary has two halves: which server receives the tenant's login, and which identity that login is.
The two projects let the author of a resource decide both.

- In vault-config-operator, every resource carries an `authentication` block (auth mount, role, OpenBao
  namespace, ServiceAccount) and may carry a `connection` block with its own address and CA, which the README
  presents as a way to reach several Vault servers from one cluster
  ([README](https://github.com/redhat-cop/vault-config-operator/blob/3072fd2d72fccc6f59087bba84b983c94f4d729e/README.md#the-common-connection-section)).
  The ServiceAccount is the tenant's own, so the Vault role binding is the limit. The address, however, is
  whatever the resource says.
- In provider-vault, a namespaced `ProviderConfig` holds the address and the credentials. With the credentials
  source `Kubernetes`, the provider logs in with the token of its own pod's ServiceAccount, sent to the address
  in that `ProviderConfig`, under the role the `ProviderConfig` names
  ([`internal/clients/vault.go`](https://github.com/upbound/provider-vault/blob/356438dabec87abf768a2b3c2cd1a95c648679dd/internal/clients/vault.go#L209-L233)).
  Keeping tenants apart then rests on Kubernetes RBAC over who may write a `ProviderConfig`, or on credentials in
  a Secret per tenant.

This operator splits the decision along trust lines
([ADR 0003](../adr/0003-connections-and-tenant-boundary.md),
[ADR 0010](../adr/0010-openbaoserver-owns-address-and-audiences.md)). The platform owns the cluster-scoped
`OpenBaoServer`: address, CA, which Kubernetes namespaces may use it and which token audiences they may request.
The tenant owns an `OpenBaoConnection` in its namespace that names the server and a login from the same
namespace: a ServiceAccount, or a token, AppRole or JWT from a Secret there. For a ServiceAccount the operator
requests a short-lived token through TokenRequest, refuses
any audience the Kubernetes API server accepts, and never lends its own identity to a namespaced connection. The
tenant chooses its OpenBao role, and OpenBao decides what that role may do. The e2e suite includes a two-tenant
isolation test (`test/e2e/isolation_test.go`); the [connections reference](../reference/connections.md) has the
field rules.

## Ownership, adoption and deletion

A declarative tool has to answer three questions about an object it did not create: may it take the object
over, what happens when two resources name the same object, and what deleting a resource does.

vault-config-operator reads the object and, if it differs from the spec, writes the spec over it; there is no
adopt step and no check that another resource writes the same path
([`vaultobject.go`](https://github.com/redhat-cop/vault-config-operator/blob/3072fd2d72fccc6f59087bba84b983c94f4d729e/api/v1alpha1/utils/vaultobject.go#L198-L222)).
Deleting a resource deletes its object whenever the last reconcile succeeded and the kind is deletable
([`reconcile_skeleton.go`](https://github.com/redhat-cop/vault-config-operator/blob/3072fd2d72fccc6f59087bba84b983c94f4d729e/internal/controller/vaultresourcecontroller/reconcile_skeleton.go#L35-L53)),
so a resource that took over a hand-made policy also removes it. Only `RandomSecret` offers a retain policy.

Crossplane links a managed resource to its object through the external name, and taking over an existing object
is a matter of setting that annotation. Cluster-scoped managed resources default to `deletionPolicy: Delete`;
namespaced ones default to `managementPolicies: ["*"]`, which includes delete. Crossplane offers good tools here:
observe-only management policies and `Usage` objects that block the deletion of something still in use.

This operator makes the safe outcome the default ([ADR 0015](../adr/0015-create-and-orphan-by-default.md)).
`creationPolicy: Create` refuses an existing object it does not own; `Adopt` and `CreateOrAdopt` take one over on
request. An index on server, OpenBao namespace and path gives every path one owner: a second resource gets
`Conflict` and never writes, and entities, groups and namespaces carry a server-side marker that survives a
restore ([ADR 0012](../adr/0012-ownership-path-index-and-marker.md)). Ownership is bound to the server's cluster
id, so a DNS change that points a connection at another cluster stops the resource instead of rewriting the wrong
server. `deletionPolicy: Orphan` is the default, and kinds whose delete destroys data (mounts, namespaces, KV data,
audit devices, key material, MFA methods, entities and `OpenBaoEndpoint`) also need an explicit annotation
([ADR 0013](../adr/0013-destructive-delete-guard.md)). For a server that already has configuration, `obc-import`
writes resources in `Observe` mode, which reports drift and never writes
([ADR 0016](../adr/0016-observe-mode-and-import-tool.md), [import guide](../guides/import.md)).

## Drift, and silence at rest

Repairing drift means reading every object on a schedule, and a tool that also writes on every pass turns a
large tenant count into constant load and audit noise.

- vault-config-operator re-reads objects only when `ENABLE_DRIFT_DETECTION` is `true`, and then every 10 hours
  unless `SYNC_PERIOD_SECONDS` says otherwise; the README notes the feature is off by default for performance
  ([README](https://github.com/redhat-cop/vault-config-operator/blob/3072fd2d72fccc6f59087bba84b983c94f4d729e/README.md#initializing-the-connection-to-vault)).
  When it does re-read, it writes only if the object differs.
- provider-vault re-reads every managed resource every 10 minutes with 5% jitter, under a global cap of 10
  reconciles per second
  ([`cmd/provider/main.go`](https://github.com/upbound/provider-vault/blob/356438dabec87abf768a2b3c2cd1a95c648679dd/cmd/provider/main.go#L89-L136)).
  This is sound and on by default.

This operator re-reads access-critical kinds (policies, auth) every 15 minutes and the rest every hour, with
±20% jitter, inside a request budget per server, and caps rewrites of objects it already owns with a write
circuit breaker ([ADR 0014](../adr/0014-drift-tiers-and-request-budget.md),
[ADR 0018](../adr/0018-write-circuit-breaker.md)). Zero writes at rest is a design rule with a metric
(`openbao_writes_total`) and a measurement: the load test recorded no OpenBao and no Kubernetes writes over three
drift intervals at 5,000 objects ([load testing](../operate/load-testing.md#steady-state)). We know of no
published steady-state write measurement for either of the other projects.

## Write-only fields

OpenBao never returns passwords, private keys and similar inputs, so no tool can detect a change made to them
outside Git. The tools differ in how they notice a change on the Git side. This operator keeps a hash of the
write-only inputs of every kind and writes again when it changes. vault-config-operator does that for the database
secrets engine configuration, where it also handles the root password rotation; other kinds drop the write-only
field from the comparison (for example the LDAP bind password), so a change made only to that field in Git is not
noticed. Terraform, and so provider-vault, compares against the value in its
own state. This operator adds a write-once class for root credentials that OpenBao rotates after the first write,
re-sent only when the user bumps a rotation nonce
([ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md)), and keeps its hash an HMAC keyed per
resource, so equal inputs of two resources do not hash alike.

## Validation without webhooks

vault-config-operator validates updates, such as the immutability of paths and namespaces, in validating
admission webhooks, which need serving certificates from cert-manager or OLM
([README](https://github.com/redhat-cop/vault-config-operator/blob/3072fd2d72fccc6f59087bba84b983c94f4d729e/README.md#deploying-with-helm)).
provider-vault checks required fields with CEL rules in the CRD schema, and Crossplane's package manager supplies
the certificates of the webhook server the provider runs. This operator ships no webhook at all and expresses
immutability, one-of choices and cross-field rules in CEL, so the install has no certificate to rotate and no
webhook outage that blocks applies ([ADR 0005](../adr/0005-cel-validation-without-webhooks.md)).

## Why not External Secrets Operator

[External Secrets Operator](https://external-secrets.io) moves secret values. An `ExternalSecret` copies values
from OpenBao or Vault into Kubernetes Secrets, a
[`PushSecret`](https://external-secrets.io/latest/guides/pushsecrets/) writes a Kubernetes Secret into the
provider, and generators such as
[`VaultDynamicSecret`](https://external-secrets.io/latest/api/generator/vault/) request credentials from a
secrets engine. None of these create a policy, an auth method or role, a mount or a namespace. ESO needs that
configuration to exist before it can log in.

The two fit together: this operator writes the policy and the Kubernetes auth role, and ESO logs in with that
role to deliver the secret. That is why the operator does not sync secret values itself
([ADR 0007](../adr/0007-no-secret-sync.md)); it writes a Kubernetes Secret only for a value OpenBao returns once,
such as an AppRole secret ID.

## Where the others are better

- **Breadth.** provider-vault has 127 managed resource kinds, each generated twice (cluster-scoped and
  namespaced), and vault-config-operator has 96. This operator has 68 built-in kinds, covering 87.0% of the
  configuration paths of OpenBao 2.7.2, and leaves the rest to `OpenBaoEndpoint`
  ([API coverage](../api-coverage.md)).
- **HashiCorp Vault.** If you run Vault, and especially Vault Enterprise with Sentinel, replication or KMIP,
  either project fits and this one does not: Vault is neither tested nor promised here.
- **Composition.** Crossplane compositions and functions build higher-level platform APIs over managed
  resources, with the rest of Crossplane's ecosystem. This operator offers only its access kinds
  (`AccessLevel`, `AccessGrant`, `AccessLayout`) above the primitives.
- **Maturity.** vault-config-operator is at v1.0 and ships through OperatorHub, and the Terraform provider under
  provider-vault has been released since 2017. This operator is alpha (`v1alpha1`, v0.2.x) with one maintainer.
- **One tool for configuration and secrets.** vault-config-operator also generates and syncs secret values
  (`RandomSecret`, `VaultSecret`). This operator leaves that to ESO.

The gap this operator fills is narrower than any of these: typed resources built for OpenBao, tenants that
configure it from their own namespace as their own identity while the platform alone decides the server, one owner
per path, and drift repair that stays silent at rest.
