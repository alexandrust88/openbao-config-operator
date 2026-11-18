---
title: Initialise OpenBao for the OpenBao Config Operator
description: Create the operator's Kubernetes login at OpenBao's first boot, without handing out a root token.
---

# Initialise OpenBao for the operator

OpenBao can initialise itself at first boot from `initialize` blocks in its configuration. Use
that to create the operator's login before anyone holds a root token, then connect the operator with
a `ClusterBaoConnection`.

With [examples/self-init/openbao.hcl](../../examples/self-init/openbao.hcl) the server:

1. enables the `kubernetes` auth method and points it at the in-cluster API server;
2. writes the policy `openbao-config-operator` (a platform operator in the root OpenBao namespace;
   trim it with [OpenBao policy for the operator](../operate/openbao-policy.md));
3. creates the role `auth/kubernetes/role/openbao-config-operator`, bound to the ServiceAccount
   `openbao-config-operator` in the Kubernetes namespace `openbao-config-operator`, with audience
   `openbao-config-operator`;
4. revokes the root token it used. No root token is ever printed or stored.

The operator then logs in as itself, so nobody has to create a token for it.

## Before you begin

- You can add an extra OpenBao `-config` file next to the server configuration (storage, listener
  and seal). Self-init needs an auto-unseal seal; it does not work with Shamir.
- You can bind the OpenBao server's ServiceAccount to the `system:auth-delegator` ClusterRole, so
  the Kubernetes auth method can review tokens.
- You can install the operator with Helm ([Install](../get-started/install.md)).
- A bootstrap administrator login for OpenBao, such as an OIDC or userpass method added in its own
  `initialize` block. The example creates none, and after self-init no root token exists.
- The `bao` CLI, with `BAO_ADDR` and a token of that bootstrap administrator, for the last step.

## 1. Add the self-init configuration

Copy [examples/self-init/openbao.hcl](../../examples/self-init/openbao.hcl) onto the OpenBao server
as an extra `-config` (for the OpenBao Helm chart, as an extra config file next to `server.hcl`).
Self-init runs only once, on empty storage.

Bind the OpenBao server's ServiceAccount to `system:auth-delegator` so token review works.
The OpenBao Helm chart creates this binding by default (`server.authDelegator.enabled: true`);
otherwise create it:

```bash
kubectl create clusterrolebinding openbao-auth-delegator \
  --clusterrole=system:auth-delegator \
  --serviceaccount=<openbao-namespace>:<openbao-serviceaccount>
```

Replace `<openbao-namespace>` with the Kubernetes namespace OpenBao runs in and
`<openbao-serviceaccount>` with its ServiceAccount, which the OpenBao Helm chart names after the
release (`openbao` by default).

## 2. Install the operator

Install the operator in the Kubernetes namespace `openbao-config-operator` with release name
`openbao-config-operator` (the ServiceAccount name the role expects). The chart mounts a projected
token for the audience `openbao-config-operator` by default:

```bash
helm install openbao-config-operator oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version @VERSION@ \
  --namespace openbao-config-operator --create-namespace
```

## 3. Create the cluster connection

Declare the `BaoServer` named `openbao` for this OpenBao as in
[Connect to OpenBao, step 1](../get-started/connect.md#1-declare-the-server). Then save this as `cluster-connection.yaml`. `ClusterBaoConnection` is cluster-scoped; do not set
`metadata.namespace`. Keep `allowedNamespaces` aligned with the Kubernetes namespaces that may use
this connection:

```yaml
apiVersion: core.obco.dev/v1alpha1
kind: ClusterBaoConnection
metadata:
  name: platform
spec:
  serverRef:
    name: openbao
  auth:
    kubernetes:
      role: openbao-config-operator
      injectedIdentity: {}
  allowedNamespaces:
    matchLabels:
      obco.dev/platform: "true"
```

`injectedIdentity: {}` uses the chart's default token path `/var/run/secrets/openbao/token`. See
[Connect to OpenBao](../get-started/connect.md#log-in-as-the-operator).

Label each Kubernetes namespace that may use it, here `openbao-config`; without the label, its
resources report `ConnectionDenied`. Then apply the connection:

```bash
kubectl label namespace openbao-config obco.dev/platform=true
kubectl apply -f cluster-connection.yaml
kubectl wait clusterbaoconnection/platform --for=condition=Ready --timeout=2m
```

## 4. Check that it worked

Confirm that the connection is ready, then, as the bootstrap administrator, that OpenBao holds the
auth method, policy and role:

```bash
kubectl get clusterbaoconnection platform
bao read sys/auth
bao read sys/policies/acl/openbao-config-operator
bao read auth/kubernetes/role/openbao-config-operator
```

Expect the connection to report `Ready` with a successful login, and the Kubernetes auth mount,
the policy and the role to exist. Then reconcile a harmless configuration resource that uses this
connection.

## Limits after first boot

The example denies the operator access to change its own policy, auth mount, auth configuration and
login role. Use a separate bootstrap administrator identity to update those objects; an `ACLPolicy` or
`AuthMount` using this connection cannot manage them.

Existing installations need the updated policy applied separately: upgrading the operator or
changing the self-init file does not rerun initialisation. Before applying the updated policy, set
`deletionPolicy: Orphan` on any resources that manage these bootstrap objects and remove those
resources from Kubernetes and their GitOps source. Keep the OpenBao objects in place; otherwise the
new denials can leave reconciliation or deletion blocked.

After a policy or login-role update, check the `ClusterBaoConnection`'s `Ready` and
authenticated status, then reconcile a harmless configuration resource using that connection. Keep
the bootstrap administrator available until both checks succeed. If login fails, repair the
bootstrap objects with that administrator; restarting the operator does not repair OpenBao
configuration.

These denials prevent accidental self-lockout. They do not make broad policy-authoring rights a
safe tenant boundary: the operator can still author other powerful policies. Use separate,
restricted identities for tenant connections.

## Troubleshooting

| symptom | check |
|---------|-------|
| Self-init did nothing | Storage was not empty, or the seal is Shamir; self-init runs once on empty storage with auto-unseal |
| Connection login fails | Confirm the ServiceAccount name and Kubernetes namespace, the audience, and `system:auth-delegator` on the OpenBao server |
| Operator cannot manage its own login objects | Expected under the example denials; use the bootstrap administrator |
| Policy update blocks reconciliation | Orphan and remove resources that managed the bootstrap objects before applying the new denials |

## Related

- [ClusterBaoConnection](../reference/api/core.md#clusterbaoconnection)
- [BaoServer](../reference/api/core.md#baoserver)
- [Connect to OpenBao](../get-started/connect.md)
- [OpenBao policy for the operator](../operate/openbao-policy.md)
- [Install](../get-started/install.md)
