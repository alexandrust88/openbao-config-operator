---
description: "Connect one OpenBao Config Operator installation to multiple OpenBao servers with separate identities and credentials."
---

# Manage several OpenBao servers

In this tutorial you are the platform owner again. You start a second OpenBao next to the
quickstart's, declare it as its own server with its own connection, and give each server a
different configuration plus one policy they share. At the end you check that each server holds
exactly its own objects.

It takes about five minutes.

## Before you begin

Finish [Set up the platform](platform-owner.md). This tutorial reuses its namespace `platform`,
its connection `platform` to the first OpenBao, and its operator policy.

## 1. Start a second OpenBao

Apply [`examples/tutorials/multiple-instances/openbao-2.yaml`](../../examples/tutorials/multiple-instances/openbao-2.yaml):
the same dev-mode OpenBao as the quickstart's, in the namespace `openbao-2`.

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

The second OpenBao gets its own `OpenBaoServer` and its own connection. The server `openbao-2`
sets no `allowedNamespaces`, so no tenant connection may use it; the `ClusterOpenBaoConnection`
`platform-2` reaches it for the namespace `platform`.

The first OpenBao is now declared twice: as `quickstart` by the quickstart and as `openbao` by the
previous tutorial. The tutorials keep both. Each `OpenBaoServer` has its own allow-lists and its
own `rateLimit`, so on a real platform declare each OpenBao once.

=== "openbaoserver.yaml"

    ```yaml
    --8<-- "examples/tutorials/multiple-instances/openbaoserver.yaml"
    ```

=== "clusteropenbaoconnection.yaml"

    ```yaml
    --8<-- "examples/tutorials/multiple-instances/clusteropenbaoconnection.yaml"
    ```

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:connect"
```

```text title="Expected output"
openbaoserver.core.openbaoconfig.io/openbao-2 created
clusteropenbaoconnection.core.openbaoconfig.io/platform-2 created
openbaoserver.core.openbaoconfig.io/openbao-2 condition met
clusteropenbaoconnection.core.openbaoconfig.io/platform-2 condition met
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

    ```yaml
    --8<-- "examples/tutorials/multiple-instances/instance-1.yaml"
    ```

=== "instance-2.yaml"

    ```yaml
    --8<-- "examples/tutorials/multiple-instances/instance-2.yaml"
    ```

=== "shared-policy.yaml"

    ```yaml
    --8<-- "examples/tutorials/multiple-instances/shared-policy.yaml"
    ```

```bash
--8<-- "hack/docs/tutorial-multiple-instances.sh:configure"
```

Every resource reports `condition met`.

## 5. See which server each resource is bound to

Each resource records the cluster id of the OpenBao it was written to in `status.serverID`:

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
  `OpenBaoServer` and connection.
- Configuration that differs per server, and one policy kept identical on both.
- Proof that a resource bound to one server never writes to the other.

## Next steps

- [Onboard two tenants](tenant-onboarding.md) on the first server.
- [Connections reference](../reference/connections.md): `OpenBaoServer` and
  `ClusterOpenBaoConnection` fields.
