---
title: Manage OpenBao policies with Kubernetes
description: Declare an OpenBao ACL policy as a Kubernetes custom resource, verify reconciliation and attach it to an authentication role.
---

# Manage OpenBao policies with Kubernetes

Use an `ACLPolicy` resource to keep an OpenBao ACL policy in Git. The OpenBao Config Operator writes
its HCL to OpenBao and reconciles later changes. Creating the policy does not grant anyone access:
an auth role or identity group must attach it to a login.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/policies/acl/app-read" { capabilities = ["create", "read", "update"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- Use an unused policy name. To take over an existing policy, first follow
  [Import existing OpenBao configuration safely](import.md) and review its current permissions.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

The example grants read access to application data in the KV version 2 mount `secret`. The policy
can be created before that mount exists; [Manage KV secrets from Kubernetes](kv-secrets.md) creates
the mount, and [Configure Kubernetes authentication](kubernetes-auth.md) attaches the policy to a
workload login.

## 1. Declare the policy

Save this as `app-read.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: app-read
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: app-read
  policy: |
    path "secret/data/app/*" {
      capabilities = ["read"]
    }
```

```bash
kubectl apply -f app-read.yaml
kubectl -n openbao-config wait aclpolicies.sys.obco.dev/app-read --for=condition=Ready --timeout=2m
```

## 2. Check that it worked

Confirm the Kubernetes resource is ready, then that OpenBao holds the policy:

```bash
kubectl -n openbao-config get aclpolicies.sys.obco.dev app-read
bao policy read app-read
```

Expect `READY` and `SYNCED` to be `True`. After a later edit, check `SYNCED` as well, because
`READY` keeps its last value while a write fails. The returned HCL must contain the `secret/data/app/*` path
and only the `read` capability. `metadata.name` identifies the Kubernetes object; `spec.name`
identifies the policy in OpenBao. They match here.

Attach the policy through a role's `policyRefs`, as in
[Configure Kubernetes authentication](kubernetes-auth.md), or through an identity group
([Manage OpenBao identity with Kubernetes](identity.md)). Keep policy changes in the manifest and
let your GitOps delivery ([Deliver OpenBao configuration with Argo CD](argocd.md)) apply them.
Do not let another tool write the same OpenBao policy: the operator restores its own text at every
drift check.

Deleting the `ACLPolicy` resource keeps the OpenBao policy because of `deletionPolicy: Orphan`.
Deleting the policy in OpenBao removes access from every token that carries it, so review its
users first.

## Troubleshooting

| symptom | check |
|---------|-------|
| the resource is not `Ready` | `kubectl -n openbao-config describe aclpolicies.sys.obco.dev app-read` for conditions and events |
| OpenBao returns a permission error on `bao policy read` | the token can read `sys/policies/acl/app-read`, `BAO_ADDR` points at the connection's server, and `BAO_NAMESPACE` at its OpenBao namespace (unset for the root) |
| a dependent role reports `ReferenceNotReady` after you delete the `ACLPolicy` | update that role's `policyRefs` before deleting the `ACLPolicy` resource |

## Related

- API reference: [ACLPolicy](../reference/api/sys.md#aclpolicy).
- [Configure Kubernetes authentication](kubernetes-auth.md)
- [Manage OpenBao identity with Kubernetes](identity.md)
- [Write OpenBao templated policies with Kubernetes](templated-policies.md)
- [Backup and restore: deletion policy and orphaned objects](../operate/backup-restore.md#deletion-policy-and-orphaned-objects)
