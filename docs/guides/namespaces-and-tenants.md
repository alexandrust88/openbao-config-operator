---
title: Organise OpenBao namespaces and tenants with Kubernetes
description: Give a tenant its own OpenBao namespace and connection so it manages configuration without reaching other tenants.
---

# Organise OpenBao namespaces and tenants with Kubernetes

Give a tenant its own OpenBao namespace and its own connection, so the tenant manages its
configuration from its Kubernetes namespace with the OpenBao Config Operator and cannot reach
anything outside its OpenBao namespace.

The boundary has three layers, each owned by the platform team:

| layer | what it limits |
|-------|----------------|
| the `BaoServer`'s `allowedNamespaces` and `allowedAudiences` | which Kubernetes namespaces may connect at all |
| the connection's `spec.openbaoNamespace` | the OpenBao namespace every request starts from; a resource's `spec.openbaoNamespace` can only go deeper |
| the OpenBao policy of the connection's identity | what the tenant may change inside its OpenBao namespace |

[Concepts: servers, connections and the tenant boundary](../concepts/index.md#servers-connections-and-the-tenant-boundary)
explains the model.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/namespaces/*"         { capabilities = ["create", "read", "update", "delete"] }
    path "tenants/sys/namespaces/*" { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- A `BaoServer` named `openbao` whose `allowedNamespaces` selects tenant Kubernetes namespaces
  by a label, such as `obco.dev/tenant: "true"`, and whose `allowedAudiences` includes
  `openbao`.
- The `bao` CLI, with `BAO_ADDR` and a token that can enable an auth method, write a policy and
  create a role in `tenants/team-a` (step 2), and list the OpenBao namespaces in `tenants` (last
  step).

## 1. Create the OpenBao namespace

The platform team creates the OpenBao namespace `tenants/team-a` through its own connection:
first `tenants`, then `team-a` inside it, because `spec.openbaoNamespace` selects the parent OpenBao
namespace. Save this as `team-a-namespace.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: BaoNamespace
metadata:
  name: tenants
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: tenants
---
apiVersion: sys.obco.dev/v1alpha1
kind: BaoNamespace
metadata:
  name: team-a
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  openbaoNamespace: tenants
  name: team-a
```

```bash
kubectl apply -f team-a-namespace.yaml
kubectl -n openbao-config wait baonamespace/tenants baonamespace/team-a \
  --for=condition=Ready --timeout=2m
```

`team-a` reports `ReferenceNotReady` until `tenants` exists, then retries.

The OpenBao namespaces keep `deletionPolicy: Orphan`: deleting an OpenBao namespace deletes
everything in it, so `Delete` takes effect only with the annotation
`obco.dev/allow-destructive-delete: "true"`.

## 2. Give the tenant an identity in OpenBao

The tenant's connection logs in inside `tenants/team-a`, with a Kubernetes auth mount of its own
and the role `configurer-team-a`, bound to the ServiceAccount `openbao-configurer` of the
Kubernetes namespace `team-a` and the audience `openbao`. Save the policy from
[OpenBao policy: tenant operator in a child namespace](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace)
as `configurer-team-a.hcl`, in the form for an auth mount inside `tenants/team-a`: without the
`tenants/team-a/` prefix. It includes the `allowed_parameters` rule that keeps the tenant's roles
bound to its own Kubernetes namespace. Then create the login:

```bash
bao auth enable -namespace=tenants/team-a kubernetes
bao write -namespace=tenants/team-a auth/kubernetes/config \
  kubernetes_host=https://kubernetes.default.svc:443
bao policy write -namespace=tenants/team-a configurer-team-a configurer-team-a.hcl
bao write -namespace=tenants/team-a auth/kubernetes/role/configurer-team-a \
  bound_service_account_names=openbao-configurer \
  bound_service_account_namespaces=team-a \
  audience=openbao \
  token_policies=configurer-team-a \
  token_ttl=1h token_max_ttl=4h
```

Token review works as for the platform connection: when OpenBao runs elsewhere, also pass
`kubernetes_ca_cert` and `token_reviewer_jwt`, as in
[Connect to OpenBao](../get-started/connect.md#2-give-the-operator-an-identity-in-openbao). The
connection in step 3 leaves `openbaoAuthNamespace` unset, so it logs in in its own OpenBao namespace,
`tenants/team-a`.

To share one auth mount at the root for every tenant instead, create the role and the policy in
the root OpenBao namespace (leave out `-namespace=tenants/team-a` and keep the `tenants/team-a/`
prefix in the policy), skip `bao auth enable` and the config when the root mount `kubernetes`
exists, and set `openbaoAuthNamespace: ""` in the connection in step 3.

## 3. Create the tenant's Kubernetes namespace and connection

Label the Kubernetes namespace so the server admits it, create the ServiceAccount the connection
logs in as, and create the connection with the tenant's OpenBao namespace:

```bash
kubectl create namespace team-a --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace team-a obco.dev/tenant=true
kubectl -n team-a create serviceaccount openbao-configurer --dry-run=client -o yaml | kubectl apply -f -
```

Save this as `team-a-connection.yaml`:

```yaml
apiVersion: core.obco.dev/v1alpha1
kind: BaoConnection
metadata:
  name: openbao
  namespace: team-a
spec:
  serverRef:
    name: openbao
  openbaoNamespace: tenants/team-a
  auth:
    kubernetes:
      role: configurer-team-a
      serviceAccountRef:
        name: openbao-configurer
      audiences:
        - openbao
```

```bash
kubectl apply -f team-a-connection.yaml
kubectl -n team-a wait baoconnection/openbao --for=condition=Ready --timeout=2m
```

`openbaoNamespace` is the base of every request made through the connection. A resource's own
`spec.openbaoNamespace` is relative to it, so a resource reaches the base OpenBao namespace and its
children, never a parent or a sibling. Do not change `openbaoNamespace` after resources exist: a resource
acquired in the old OpenBao namespace stops with the reason `NamespaceChanged` instead of creating
a second object.

## 4. Let the tenant create resources

The chart's `<release>-tenant-editor` ClusterRole allows every namespaced kind except
`BaoConnection` (the cluster-scoped kinds are left out too), and is aggregated into the
built-in `admin` and `edit` roles. A tenant with `edit` in its Kubernetes namespace can therefore
create resources, but never change which OpenBao identity they use. To grant only the operator's
kinds, bind the role in the tenant's Kubernetes namespace. The role name below is the one for a
release named `openbao-config-operator`:

```bash
kubectl -n team-a create rolebinding team-a-openbao \
  --clusterrole=openbao-config-operator-tenant-editor --group=team-a
```

## 5. Limit what the tenant can consume

Every managed object costs drift reads against the shared server. Cap the number of objects per
Kubernetes namespace with a `ResourceQuota`, as described in
[Cap tenant object counts](../operate/tenant-quotas.md). A `RateLimitQuota` limits requests in
OpenBao itself; only a connection based in the root OpenBao namespace may manage it.

## 6. Check that it worked

Confirm the Kubernetes resources are ready, then that OpenBao lists the tenant's OpenBao
namespace:

```bash
kubectl -n openbao-config wait baonamespace/team-a --for=condition=Ready --timeout=2m
kubectl -n team-a wait baoconnection/openbao --for=condition=Ready --timeout=2m
bao namespace list -namespace=tenants
```

The connection is `Ready` and OpenBao lists `team-a` under `tenants`. After a later edit, check
`Synced` as well, because `Ready` keeps its last value while a write fails. As the tenant's
identity, `bao token capabilities` on a path in another OpenBao namespace returns `deny`.

## Troubleshooting

| symptom | check |
|---------|-------|
| the connection reports `ReferenceNotReady` | its `openbaoNamespace` does not exist yet; it retries until the `BaoNamespace` is `Ready` |
| the connection reports `ConnectionDenied` | the Kubernetes namespace label against the server's `allowedNamespaces`, and the audiences against `allowedAudiences` |
| the connection reports `AuthenticationFailed` | `openbaoAuthNamespace`: the login happens there, so the auth mount and role must exist in it |
| a resource reports `RootOnlyKind` | the kind works only in the root OpenBao namespace; manage it through a root connection |
| a resource reports `NamespaceChanged` | the connection's `openbaoNamespace` changed after the resource was created: restore it, or recreate the resource |

## Related

- API reference: [BaoNamespace](../reference/api/sys.md#baonamespace),
  [BaoConnection](../reference/api/core.md#baoconnection),
  [RateLimitQuota](../reference/api/sys.md#ratelimitquota).
- [Connections](../reference/connections.md)
- [Hardening: R-TENANCY-01 server allow-lists](../security/hardening.md#r-tenancy-01-server-allow-lists)
