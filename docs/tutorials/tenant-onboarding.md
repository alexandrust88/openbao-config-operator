---
title: "Tutorial: onboard two OpenBao tenants"
description: "Onboard two isolated OpenBao tenants: a Kubernetes namespace, an OpenBao namespace with a login, and a connection limited to it."
---

# Onboard two tenants

In this tutorial you are the platform admin on day 1 of two tenant teams, `tenant-a` and
`tenant-b`. For each one you create a Kubernetes namespace that the tenant administers and
nothing more, an OpenBao namespace with a login inside it, and the connection through which the
OpenBao Config Operator keeps the tenant there. Two tenants let you see the boundary between them
in the next tutorial.

It takes about five minutes.

## Before you begin

Finish [Set up the platform](platform-owner.md). This tutorial uses its `BaoServer` `openbao`
and its `ClusterBaoConnection` `platform`.

## 1. Create the tenants' Kubernetes namespaces

For each tenant, `namespaces.yaml` holds:

- the Kubernetes namespace, with the label `obco.dev/tenant: "true"` that the server
  `openbao` admits;
- the ServiceAccount `openbao-config`, which the tenant's connection will log in as;
- a RoleBinding that gives the user `tenant-a-admin` (or `tenant-b-admin`) the built-in
  ClusterRole `admin` in that Kubernetes namespace only.

```yaml title="examples/tutorials/tenant-onboarding/namespaces.yaml"
--8<-- "examples/tutorials/tenant-onboarding/namespaces.yaml"
```

Apply the file:

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

The chart aggregates its `<release>-tenant-editor` role into `admin`, so the admin of a Kubernetes
namespace may create the operator's resources there. Ask the API server, impersonating
`tenant-a-admin`, whether the tenant may create a `BaoNamespace` and a `KubernetesAuthRole`:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:rbac-allowed"
```

Both answers are `yes`. Now ask for what the tenant must not do: create a connection (which
chooses the OpenBao identity), create a resource in `tenant-b`, create a `ClusterBaoConnection`,
or create a Kubernetes namespace:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:rbac-denied"
```

Every answer is `no`. [Hardening: RBAC](../security/hardening.md#rbac) explains the roles.

## 3. Create the tenants' OpenBao namespaces and logins

Through the platform connection, create for each tenant:

- the OpenBao namespace `tenant-a`;
- a Kubernetes auth method inside it, and its configuration;
- the policy `tenant-admin`, which lets the tenant create child OpenBao namespaces, secrets engines
  in them and KV data in their `secret` mounts, and policies and auth roles whose names start with
  `team-`; a role can bind only ServiceAccounts of the tenant's own Kubernetes namespace.
  Everything stays inside the OpenBao namespace `tenant-a`;
- the role `operator`, which lets only the ServiceAccount `openbao-config` of the Kubernetes
  namespace `tenant-a` log in, with that policy.

The two files differ only in the tenant's name:

=== "Tenant A"

    ```yaml title="examples/tutorials/tenant-onboarding/openbao-tenant-a.yaml"
    --8<-- "examples/tutorials/tenant-onboarding/openbao-tenant-a.yaml"
    ```

=== "Tenant B"

    ```yaml title="examples/tutorials/tenant-onboarding/openbao-tenant-b.yaml"
    --8<-- "examples/tutorials/tenant-onboarding/openbao-tenant-b.yaml"
    ```

Apply both and wait until the ten resources are `Ready`:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:openbao"
```

Each of the ten resources reports `condition met`.

## 4. Connect each tenant to its OpenBao namespace

Each tenant gets a `BaoConnection` in its own Kubernetes namespace, based in its OpenBao
namespace (`namespace: tenant-a`). You create it because the tenant cannot.

```yaml title="examples/tutorials/tenant-onboarding/connections.yaml"
--8<-- "examples/tutorials/tenant-onboarding/connections.yaml"
```

Apply the file and wait until both connections are `Ready`:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:connections"
```

```text title="Expected output"
baoconnection.core.obco.dev/openbao created
baoconnection.core.obco.dev/openbao created
baoconnection.core.obco.dev/openbao condition met
baoconnection.core.obco.dev/openbao condition met
```

Every resource that `tenant-a` creates through this connection starts in the OpenBao namespace
`tenant-a`, and its own `spec.openbaoNamespace` can only go deeper.

## 5. Check the result in OpenBao

List the OpenBao namespaces and read the role of `tenant-a`:

```bash
--8<-- "hack/docs/tutorial-tenant-onboarding.sh:verify"
```

Read the second tenant's role too:

```bash
kubectl -n openbao exec deploy/openbao -- bao read -namespace=tenant-b auth/kubernetes/role/operator
```

You should see `platform/`, `tenant-a/` and `tenant-b/`. Each role is bound to ServiceAccount
`openbao-config` in its own Kubernetes namespace (`tenant-a` or `tenant-b`), with the policy
`tenant-admin` in that OpenBao namespace.

## What you built

- Two tenant Kubernetes namespaces whose admins hold no cluster rights and cannot create
  connections.
- Two OpenBao namespaces, each with its own Kubernetes login and a policy that scopes the tenant.
- One `BaoConnection` per tenant, based in its OpenBao namespace and admitted by the server's
  label and audience rules.

## Clean up

Keep the cluster for the next lesson. To stop here, delete it as [Tutorials](index.md#clean-up)
describes.

## Next steps

- [Onboard teams as a tenant](team-onboarding.md): continue as the `tenant-a` admin.
- [Namespaces and tenants](../guides/namespaces-and-tenants.md): the same boundary for your own
  layout.
- [Connections reference: tenant boundary rules](../reference/connections.md#tenant-boundary-rules).
