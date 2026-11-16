---
description: "Organize OpenBao namespaces and tenant connections with Kubernetes resources and scoped authentication."
---

# Namespaces and tenants

This guide gives a team its own OpenBao namespace and its own connection, so the team manages its
configuration from its Kubernetes namespace and cannot reach anything outside its OpenBao
namespace.

The boundary has three layers, each owned by the platform team:

| layer | what it limits |
|-------|----------------|
| the `OpenBaoServer`'s `allowedNamespaces` and `allowedAudiences` | which Kubernetes namespaces may connect at all |
| the connection's `namespace` | the OpenBao namespace every request starts from; a resource's `spec.namespace` can only go deeper |
| the OpenBao policy of the connection's identity | what the team may change inside its namespace |

[Concepts: servers, connections and the tenant boundary](../concepts/index.md#servers-connections-and-the-tenant-boundary)
explains the model.

## Before you begin

- A platform connection that may create OpenBao namespaces (`sys/namespaces/*`), for example a
  `ClusterOpenBaoConnection` limited to platform namespaces ([Connect to OpenBao](../get-started/connect.md)).
- An `OpenBaoServer` whose `allowedNamespaces` selects tenant namespaces by a label, such as
  `openbaoconfig.io/tenant: "true"`.

## 1. Create the OpenBao namespace

The platform team creates the namespace through its own connection:

```yaml title="examples/sys/openbaonamespace.yaml"
--8<-- "examples/sys/openbaonamespace.yaml"
```

The example creates `team-a` directly under the platform connection's namespace. The team
connection in step 3 uses `tenants/team-a`, so for that layout create an `OpenBaoNamespace` named
`tenants` first, then create `team-a` with `spec.namespace: tenants`: `spec.namespace` selects the
parent namespace. Whichever layout you choose, the connection's `namespace` must name the
namespace you created.

The namespace keeps `deletionPolicy: Orphan`: deleting an OpenBao namespace deletes everything in
it, so `Delete` takes effect only with the annotation
`openbaoconfig.io/allow-destructive-delete: "true"`.

## 2. Give the team an identity in OpenBao

Choose where the team logs in:

- **At the root**, with one auth mount for every team: the connection sets `authNamespace: ""` and
  the role and policy live in the root namespace, with paths prefixed by the team's namespace.
- **Inside the team's namespace**, with an auth mount of its own: the connection leaves
  `authNamespace` unset, and the role and policy live in the team's namespace.

[OpenBao policy: tenant operator in a child namespace](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace)
has the policy for both, including the `allowed_parameters` rule that keeps the team's roles
bound to its own Kubernetes namespace. [Connect to OpenBao](../get-started/connect.md#2-give-the-operator-an-identity-in-openbao)
has the commands to create the login.

## 3. Create the team's Kubernetes namespace and connection

Label the namespace so the server admits it, create the ServiceAccount the connection logs in
as, and create the connection with the team's OpenBao namespace:

```bash
kubectl create namespace team-a
kubectl label namespace team-a openbaoconfig.io/tenant=true
kubectl -n team-a create serviceaccount openbao-configurer
```

```yaml title="examples/core/openbaoconnection.yaml"
--8<-- "examples/core/openbaoconnection.yaml"
```

`namespace` is the base of every request made through the connection. A resource's own
`spec.namespace` is relative to it, so a resource reaches the base namespace and its children,
never a parent or a sibling. Do not change `namespace` after resources exist: a resource acquired
in the old namespace stops with the reason `NamespaceChanged` instead of creating a second object.

[examples/policies/templated/40-child-namespace.yaml](../../examples/policies/templated/40-child-namespace.yaml)
shows both ways to reach a child namespace side by side: a connection based in it, and the root
connection with `spec.namespace` on each object.

## 4. Let the team create resources

The chart's `<release>-tenant-editor` ClusterRole allows every namespaced kind except
`OpenBaoConnection` (the cluster-scoped kinds are left out too), and is aggregated into the built-in `admin` and `edit`
roles. A team with `edit` in its namespace can therefore create resources, but never change which
OpenBao identity they use. To grant only the operator's kinds, bind the role in the team's
namespace. The role name below is the one for a release named `openbao-config-operator`:

```bash
kubectl -n team-a create rolebinding team-a-openbao \
  --clusterrole=openbao-config-operator-tenant-editor --group=team-a
```

## 5. Limit what the team can consume

Every managed object costs drift reads against the shared server. Cap the number of objects per
namespace with a `ResourceQuota`, as described in [Tenant quotas](../operate/tenant-quotas.md). A
`RateLimitQuota` limits requests in OpenBao itself; it is a root-namespace kind that only a
connection based in the root may manage
([examples/sys/ratelimitquota.yaml](../../examples/sys/ratelimitquota.yaml)).

## 6. Check that it worked

```bash
kubectl -n openbao-config wait openbaonamespace/obco-team-a --for=condition=Ready --timeout=2m
kubectl -n team-a wait openbaoconnection/openbao --for=condition=Ready --timeout=2m
bao namespace list
```

The connection is `Ready` and OpenBao lists the team's namespace. As the team's identity,
`bao token capabilities` on a path in another namespace returns `deny`.

## Troubleshooting

| symptom | check |
|---------|-------|
| the connection reports `ReferenceNotReady` | its OpenBao `namespace` does not exist yet; it retries until the `OpenBaoNamespace` is `Ready` |
| the connection reports `ConnectionDenied` | the namespace label against the server's `allowedNamespaces`, and the audiences against `allowedAudiences` |
| the connection reports `AuthenticationFailed` | `authNamespace`: the login happens there, so the auth mount and role must exist in it |
| a resource reports `RootOnlyKind` | the kind works only in the root namespace; manage it through a root connection |
| a resource reports `NamespaceChanged` | the connection's `namespace` changed after the resource was created: restore it, or recreate the resource |

## Related

- API reference: [OpenBaoNamespace](../reference/api/sys.md#openbaonamespace),
  [OpenBaoConnection](../reference/api/core.md#openbaoconnection),
  [RateLimitQuota](../reference/api/sys.md#ratelimitquota).
- [Connections reference](../reference/connections.md): `namespace`, `authNamespace` and the
  tenant boundary rules.
- [Hardening: tenancy](../security/hardening.md#r-tenancy-01-server-allow-lists).
