# Onboard two tenants

In this tutorial you are the platform admin on day 1 of two tenant teams, `tenant-a` and
`tenant-b`. For each one you create a Kubernetes namespace that the tenant administers and
nothing more, an OpenBao namespace with a login inside it, and the connection that keeps the
tenant there. Two tenants let you see the boundary between them in the next tutorial.

It takes about five minutes.

## Before you begin

Finish [Set up the platform](platform-owner.md). This tutorial uses its `OpenBaoServer` `openbao`
and its `ClusterOpenBaoConnection` `platform`.

## 1. Create the tenants' Kubernetes namespaces

For each tenant, `namespaces.yaml` holds:

- the namespace, with the label `openbaoconfig.io/tenant: "true"` that the server `openbao` admits;
- the ServiceAccount `openbao-config`, which the tenant's connection will log in as;
- a RoleBinding that gives the user `tenant-a-admin` (or `tenant-b-admin`) the built-in
  ClusterRole `admin` in that namespace only.

```yaml title="examples/tutorials/tenant-onboarding/namespaces.yaml"
--8<-- "examples/tutorials/tenant-onboarding/namespaces.yaml"
```

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:kubernetes"
```

```text title="Expected output"
namespace/tenant-a created
serviceaccount/openbao-config created
rolebinding.rbac.authorization.k8s.io/tenant-a-admins created
namespace/tenant-b created
serviceaccount/openbao-config created
rolebinding.rbac.authorization.k8s.io/tenant-b-admins created
```

## 2. Check what a tenant admin may do

The chart aggregates its `<release>-tenant-editor` role into `admin`, so a namespace admin may
create the operator's resources. Ask the API server, impersonating `tenant-a-admin`:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:rbac-allowed"
```

Both answers are `yes`. Now ask for what the tenant must not do: create a connection (which
chooses the OpenBao identity), touch `tenant-b`, use a `ClusterOpenBaoConnection`, or create a
namespace:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:rbac-denied"
```

Every answer is `no`. [Hardening: RBAC](../security/hardening.md#rbac) explains the roles.

## 3. Create the tenants' OpenBao namespaces and logins

Through the platform connection, create for each tenant:

- the OpenBao namespace `tenant-a`;
- a Kubernetes auth method inside it, and its configuration;
- the policy `tenant-admin`, which lets the tenant create child namespaces and secrets engines in
  them, and policies and auth roles whose names start with `team-`; a role can bind only
  ServiceAccounts of the tenant's own Kubernetes namespace. Everything stays inside the OpenBao
  namespace `tenant-a`;
- the role `operator`, which lets only the ServiceAccount `openbao-config` of the Kubernetes
  namespace `tenant-a` log in, with that policy.

`openbao-tenant-b.yaml` is the same with `tenant-b`.

```yaml title="examples/tutorials/tenant-onboarding/openbao-tenant-a.yaml"
--8<-- "examples/tutorials/tenant-onboarding/openbao-tenant-a.yaml"
```

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:openbao"
```

Every resource reports `condition met`.

## 4. Connect each tenant to its OpenBao namespace

Each tenant gets an `OpenBaoConnection` in its own Kubernetes namespace, based in its OpenBao
namespace (`namespace: tenant-a`). You create it because the tenant cannot.

```yaml title="examples/tutorials/tenant-onboarding/connections.yaml"
--8<-- "examples/tutorials/tenant-onboarding/connections.yaml"
```

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:connections"
```

```text title="Expected output"
openbaoconnection.core.openbaoconfig.io/openbao created
openbaoconnection.core.openbaoconfig.io/openbao created
openbaoconnection.core.openbaoconfig.io/openbao condition met
openbaoconnection.core.openbaoconfig.io/openbao condition met
```

Every resource that `tenant-a` creates through this connection starts in the OpenBao namespace
`tenant-a`, and its own `spec.namespace` can only go deeper.

## 5. Check the result in OpenBao

List the namespaces and read the tenant's role:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:verify"
```

You should see `platform/`, `tenant-a/` and `tenant-b/`, and a role bound to the ServiceAccount
`openbao-config` of the Kubernetes namespace `tenant-a`, with the policy `tenant-admin`.

## What you built

- Two tenant namespaces whose admins hold no cluster rights and cannot create connections.
- Two OpenBao namespaces, each with its own Kubernetes login and a policy that scopes the tenant.
- One `OpenBaoConnection` per tenant, based in its OpenBao namespace and admitted by the server's
  label and audience rules.

## Next steps

- [Onboard teams as a tenant](team-onboarding.md): continue as the `tenant-a` admin.
- [Namespaces and tenants](../guides/namespaces-and-tenants.md): the same boundary for your own
  layout.
- [Connections reference: tenant boundary rules](../reference/connections.md#tenant-boundary-rules).
