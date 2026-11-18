---
title: "Tutorial: manage several OpenBao servers"
description: "Connect one OpenBao Config Operator installation to two OpenBao servers and give each its own configuration."
---

# Manage several OpenBao servers

In this tutorial you are the platform owner again. You start a second OpenBao next to the
quickstart's, declare it to the OpenBao Config Operator as its own server with its own connection,
and give each server a different configuration plus one policy they share. At the end you check
that each server holds exactly its own objects.

It takes about five minutes.

## Before you begin

Finish [Set up the platform](platform-owner.md). This tutorial reuses its Kubernetes namespace
`platform`, its connection `platform` to the first OpenBao, and its operator policy.

## 1. Start a second OpenBao

Apply [`examples/tutorials/multiple-instances/openbao-2.yaml`](../../examples/tutorials/multiple-instances/openbao-2.yaml):
the same dev-mode OpenBao as the quickstart's, in the Kubernetes namespace `openbao-2`.

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:openbao-2"
```

```text title="Expected output"
namespace/openbao-2 created
serviceaccount/openbao created
clusterrolebinding.rbac.authorization.k8s.io/openbao-2-auth-delegator created
deployment.apps/openbao created
service/openbao created
deployment "openbao" successfully rolled out
```

## 2. Give the operator the same identity on the second server

The second server knows nothing of the first. Enable its Kubernetes auth method, point it at the
cluster's API server, and create the same policy and role as in [Set up the platform](platform-owner.md#1-give-the-operator-its-own-identity-in-openbao):

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:openbao-2-auth-enable"
--8<-- "hack/docs/tutorial-multiple-instances.sh:openbao-2-auth"
```

```text title="Expected output"
Success! Enabled kubernetes auth method at: kubernetes/
Success! Data written to: auth/kubernetes/config
Success! Uploaded policy: openbao-config-operator
Success! Data written to: auth/kubernetes/role/openbao-config-operator
```

## 3. Declare the second server and its connection

The second OpenBao gets its own `BaoServer` and its own connection. The server `openbao-2`
sets no `allowedNamespaces`, so no tenant connection may use it; the `ClusterBaoConnection`
`platform-2` reaches it for the Kubernetes namespace `platform`.

The first OpenBao is now declared twice: as `quickstart` by the quickstart and as `openbao` by the
previous tutorial. The tutorials keep both. Each `BaoServer` has its own allow-lists and its
own `rateLimit`, so on a real platform declare each OpenBao once.

=== "openbaoserver.yaml"

    ```yaml title="examples/tutorials/multiple-instances/openbaoserver.yaml"
    --8<-- "examples/tutorials/multiple-instances/openbaoserver.yaml"
    ```

=== "clusteropenbaoconnection.yaml"

    ```yaml title="examples/tutorials/multiple-instances/clusteropenbaoconnection.yaml"
    --8<-- "examples/tutorials/multiple-instances/clusteropenbaoconnection.yaml"
    ```

Apply both and wait until the server and the connection are `Ready`:

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:connect"
```

```text title="Expected output"
baoserver.core.obco.dev/openbao-2 created
clusterbaoconnection.core.obco.dev/platform-2 created
baoserver.core.obco.dev/openbao-2 condition met
clusterbaoconnection.core.obco.dev/platform-2 condition met
```

## 4. Configure each server

A resource's `connectionRef` decides which server it lands on:

- `instance-1.yaml`, through `platform`: a policy and a Kubernetes auth role, on the first server
  only;
- `instance-2.yaml`, through `platform-2`: a KV version 2 mount and a userpass auth method, on the
  second server only;
- `shared-policy.yaml`: the policy `ops-read` on both, as one resource per connection with the
  same `spec.name`.

=== "instance-1.yaml"

    ```yaml title="examples/tutorials/multiple-instances/instance-1.yaml"
    --8<-- "examples/tutorials/multiple-instances/instance-1.yaml"
    ```

=== "instance-2.yaml"

    ```yaml title="examples/tutorials/multiple-instances/instance-2.yaml"
    --8<-- "examples/tutorials/multiple-instances/instance-2.yaml"
    ```

=== "shared-policy.yaml"

    ```yaml title="examples/tutorials/multiple-instances/shared-policy.yaml"
    --8<-- "examples/tutorials/multiple-instances/shared-policy.yaml"
    ```

Apply the three files and wait until the six resources are `Ready`:

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:configure"
```

Each of the six resources reports `condition met`.

## 5. See which server each resource is bound to

Each resource records the cluster id of the OpenBao it was written to in `status.serverID`. List
the resources with their connection and that id:

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:servers"
```

Notice two different ids: the resources of `platform`, including `platform-auditor` from the first
tutorial, show the id of the first server, and those of `platform-2` the id of the second.
[Concepts: ownership and adoption](../concepts/index.md#ownership-and-adoption) explains why each
object stays bound to that id.

## 6. Check each server in OpenBao

Ask the first server for its policies and the role:

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:verify-1"
```

You should see `instance-1-app`, `ops-read` and `platform-auditor` among the policies, and
`[instance-1-app]` as the role's token policies.

Ask the second server for its policies, secrets engines and auth methods:

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:verify-2"
```

You should see `ops-read`, but neither `instance-1-app` nor `platform-auditor`; `instance-2-kv/`
among the secrets engines; and `userpass/` among the auth methods. The first server has neither
`instance-2-kv/` nor `userpass/`.

## What you built

- Two OpenBao servers managed from one Kubernetes cluster, each reached through its own
  `BaoServer` and connection.
- Configuration that differs per server, and one policy kept identical on both.
- A check, on both servers, that each resource reached only the server its connection names.

## Clean up

Keep the cluster for the next lesson. To stop here, delete it as [Tutorials](index.md#clean-up)
describes.

## Next steps

- [Onboard two tenants](tenant-onboarding.md) on the first server.
- [Connections reference](../reference/connections.md): `BaoServer` and
  `ClusterBaoConnection` fields.
