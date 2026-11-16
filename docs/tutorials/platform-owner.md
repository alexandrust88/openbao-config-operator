---
description: "Set up OpenBao Config Operator as a platform owner and delegate configuration access to tenants."
---

# Set up the platform

In this tutorial you are the platform owner: admin of OpenBao and of the Kubernetes cluster. You
give the operator its own OpenBao identity, declare the server and a platform connection, and
configure OpenBao in one apply: a policy and an auth method in the root namespace, and the
platform's own OpenBao namespace with a secrets engine inside it.

It takes about five minutes.

## Before you begin

Finish the [quickstart](../get-started/quickstart.md) up to step 9 and stay at the root of the
repository clone, as [Tutorials](index.md#before-you-begin) describes.

## 1. Give the operator its own identity in OpenBao

In the quickstart the operator logged in as the ServiceAccount of a tenant namespace. The platform
connection logs in as the operator itself, with the token the chart mounts in the manager pod for
the audience `openbao-config-operator`. The policy behind it is full admin, because you, the
platform owner, are:

```hcl title="examples/tutorials/platform-owner/openbao-config-operator.hcl"
--8<-- "examples/tutorials/platform-owner/openbao-config-operator.hcl"
```

Upload the policy and create the role `openbao-config-operator`, which accepts only the
ServiceAccount `openbao-config-operator` of the namespace `openbao-config-operator`:

```bash
--8<-- "hack/docs/tutorial-platform-owner.sh:operator-identity"
```

```text title="Expected output"
Success! Uploaded policy: openbao-config-operator
Success! Data written to: auth/kubernetes/role/openbao-config-operator
```

## 2. Declare the server and the platform connection

Three objects connect the platform to OpenBao:

- the namespace `platform`, where the platform team keeps its resources;
- the `OpenBaoServer` `openbao`, which tenant connections will use later: it admits namespaces
  labelled `openbaoconfig.io/tenant: "true"`, with the audience `openbao` only;
- the `ClusterOpenBaoConnection` `platform`, which logs in as the operator and serves resources
  in the namespace `platform` only.

=== "namespace.yaml"

    ```yaml
    --8<-- "examples/tutorials/platform-owner/namespace.yaml"
    ```

=== "openbaoserver.yaml"

    ```yaml
    --8<-- "examples/tutorials/platform-owner/openbaoserver.yaml"
    ```

=== "clusteropenbaoconnection.yaml"

    ```yaml
    --8<-- "examples/tutorials/platform-owner/clusteropenbaoconnection.yaml"
    ```

Apply them and wait until the server and the connection are `Ready`:

```bash
--8<-- "hack/docs/tutorial-platform-owner.sh:connect"
```

```text title="Expected output"
namespace/platform created
openbaoserver.core.openbaoconfig.io/openbao created
clusteropenbaoconnection.core.openbaoconfig.io/platform created
openbaoserver.core.openbaoconfig.io/openbao condition met
clusteropenbaoconnection.core.openbaoconfig.io/platform condition met
```

The connection is `Ready` once the operator has logged in to OpenBao as itself. The server's
`allowedNamespaces` and `allowedAudiences` limit `OpenBaoConnection` objects only, so they do not
apply to the `ClusterOpenBaoConnection` `platform`; its own `allowedNamespaces` limits it instead.

## 3. Configure OpenBao in one go

Four resources describe the platform's configuration:

- in the root namespace, the policy `platform-auditor` and an AppRole auth method at `approle`;
- the OpenBao namespace `platform`, and a KV version 2 secrets engine at `secret` inside it.

=== "root-config.yaml"

    ```yaml
    --8<-- "examples/tutorials/platform-owner/root-config.yaml"
    ```

=== "platform-namespace.yaml"

    ```yaml
    --8<-- "examples/tutorials/platform-owner/platform-namespace.yaml"
    ```

Notice `namespace: platform` in the `SecretsMount` spec: it is the OpenBao namespace, relative to
the connection's, which is the root. The mount waits until the OpenBao namespace exists.

Apply both files and wait until every resource is `Ready`:

```bash
--8<-- "hack/docs/tutorial-platform-owner.sh:configure"
```

```text title="Expected output"
policy.sys.openbaoconfig.io/platform-auditor created
authmount.sys.openbaoconfig.io/approle created
openbaonamespace.sys.openbaoconfig.io/platform created
secretsmount.sys.openbaoconfig.io/platform-secret created
policy.sys.openbaoconfig.io/platform-auditor condition met
authmount.sys.openbaoconfig.io/approle condition met
openbaonamespace.sys.openbaoconfig.io/platform condition met
secretsmount.sys.openbaoconfig.io/platform-secret condition met
```

List them with their `Ready` and `Synced` columns:

```bash
--8<-- "hack/docs/tutorial-platform-owner.sh:status"
```

Confirm:

- [ ] Every resource in the namespace `platform` shows `Ready` `True`.

## 4. Check the result in OpenBao

Read the policy, the auth methods, the namespaces, and the secrets engines of the namespace
`platform`:

```bash
--8<-- "hack/docs/tutorial-platform-owner.sh:verify"
```

You should see the policy as written in `root-config.yaml`, `approle/` among the auth methods,
`platform/` in the namespace list, and `secret/` of type `kv` in the namespace `platform`.

## What you built

- An OpenBao identity for the operator, and a `ClusterOpenBaoConnection` that only the platform
  namespace may use.
- An `OpenBaoServer` ready for tenant connections, limited by a namespace label and an audience.
- Root namespace configuration and a child OpenBao namespace, declared in Kubernetes and checked
  against OpenBao.

## Next steps

- [Manage several OpenBao servers](multiple-instances.md) from the same cluster.
- [Onboard two tenants](tenant-onboarding.md) on this server.
- [Connections reference](../reference/connections.md) for every field of the server and the
  connection.
