---
title: "Tutorial: onboard teams in an OpenBao tenant"
description: "As a tenant admin, give two teams their own child OpenBao namespace and check that neither they nor you can leave the tenant."
---

# Onboard teams as a tenant

In this tutorial you are the admin of `tenant-a`, with admin rights in the Kubernetes namespace
`tenant-a` and nothing else. Through the OpenBao Config Operator you onboard two of your teams,
`devteam` and `payments`: each gets a child OpenBao namespace with a generated secret that only its
own workloads can read. Then you try to leave your tenant and watch the boundary hold.

It takes about five minutes.

## Before you begin

Finish [Onboard two tenants](tenant-onboarding.md). Every command that acts as the tenant admin
carries `--as tenant-a-admin`, so the API server applies that user's rights, not yours. Commands
that read OpenBao through `kubectl -n openbao exec` run with your rights and, unless they set
`BAO_TOKEN`, with the dev server's root token, to show you the result.

## 1. Onboard the team devteam

One file onboards a team, all in the Kubernetes namespace `tenant-a` and through its connection
`openbao`:

- the ServiceAccount `devteam`, which the team's workloads run as;
- the OpenBao namespace `devteam`, created under `tenant-a`;
- a KV version 2 mount at `secret` in `tenant-a/devteam`, and a generated password at `app`;
- the policy `team-devteam` and the Kubernetes auth role `team-devteam` in `tenant-a`, which give
  the ServiceAccount `devteam` read access to `tenant-a/devteam` and nothing else.

```yaml title="examples/tutorials/team-onboarding/devteam.yaml"
--8<-- "examples/tutorials/team-onboarding/devteam.yaml"
```

Apply it as the tenant admin:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:devteam"
```

```text title="Expected output"
serviceaccount/devteam created
baonamespace.sys.obco.dev/devteam created
secretsmount.sys.obco.dev/devteam-secret created
randomsecret.secrets.obco.dev/devteam-app created
aclpolicy.sys.obco.dev/team-devteam created
kubernetesauthrole.auth.obco.dev/team-devteam created
```

## 2. Onboard the team payments

[`payments.yaml`](../../examples/tutorials/team-onboarding/payments.yaml) is the same file with
`payments` in place of `devteam`. Apply it:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:payments"
```

Wait until both teams' resources are `Ready`:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:wait"
```

Each of the ten resources reports `condition met`.

## 3. Check the result in OpenBao

List the child OpenBao namespaces of `tenant-a` and read the policy of `devteam`:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:verify"
```

You should see `devteam/` and `payments/`, and a policy that grants `read` on
`devteam/secret/data/*`. The path starts with `devteam/` because the policy lives in `tenant-a`
and reaches the child OpenBao namespace by its name.

## 4. Log in as a devteam workload

Request a token for the ServiceAccount `devteam`, log in to the auth method of `tenant-a` with the
role `team-devteam`, and read the team's password:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:team-login"
```

OpenBao prints a random 32-character password. Now try the password of `payments` with the same
token:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:team-denied"
```

OpenBao refuses the read with a `403` error, and the command fails: the token's only policy
besides `default` is `team-devteam`.

## 5. Try to leave your tenant

First, write a policy in the OpenBao root namespace through the platform's
`ClusterBaoConnection`:

```yaml title="examples/tutorials/team-onboarding/reach-root.yaml"
--8<-- "examples/tutorials/team-onboarding/reach-root.yaml"
```

Apply it:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:reach-root"
```

Kubernetes accepts the resource, because you may create policies in the Kubernetes namespace
`tenant-a`. The operator does not use the connection for it. Read why:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:reach-root-status"
```

The message contains `namespace tenant-a is not in allowedNamespaces`. The resource stays `Ready`
`False`, its `Synced` condition has the reason `ConnectionDenied`, and the root OpenBao namespace
has no policy `tenant-a-in-root`.

Second, use the connection of `tenant-b`. A resource can only use a connection of its own
Kubernetes namespace, so it must live in `tenant-b`:

```yaml title="examples/tutorials/team-onboarding/reach-tenant-b.yaml"
--8<-- "examples/tutorials/team-onboarding/reach-tenant-b.yaml"
```

Apply it:

```bash
--8<-- "hack/docs/tutorial-team-onboarding.sh:reach-tenant-b"
```

The API server refuses it: `tenant-a-admin` is `forbidden` in the Kubernetes namespace `tenant-b`.

Your own connection cannot leave either: its base is the OpenBao namespace `tenant-a`, and a
resource's `spec.openbaoNamespace` can only go deeper. OpenBao enforces the same boundary on its side:
the connection's token belongs to `tenant-a`, so a write with it in the root or in `tenant-b`
returns `403`.

## What you built

- Two teams onboarded by a tenant admin without any cluster rights: child OpenBao namespaces,
  KV mounts, generated secrets, policies and auth roles.
- A workload login that reads its own team's secret and is refused the other team's.
- Two attempts to leave the tenant, one stopped by the operator and one by Kubernetes RBAC.

## Clean up

This is the last lesson. Delete the kind cluster as [Tutorials](index.md#clean-up) describes.

## Next steps

- [Namespaces and tenants](../guides/namespaces-and-tenants.md) and
  [OpenBao policy: tenant operator in a child namespace](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace)
  for your own tenant layout.
- [Status and events](../reference/status-and-events.md) lists every condition reason, including
  `ConnectionDenied`.
