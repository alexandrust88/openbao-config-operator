---
title: Manage OpenBao policies with Kubernetes
description: Declare an OpenBao ACL policy as a Kubernetes custom resource, verify reconciliation and attach it to an authentication role.
---

# Manage OpenBao policies with Kubernetes

Use a `Policy` resource to keep an OpenBao ACL policy in Git. The operator writes its HCL to
OpenBao and reconciles later changes. Creating the policy does not grant anyone access: an auth
role or identity group must attach it to a login.

## Before you begin

- Install the operator and create a [Ready connection](../get-started/connect.md).
- This example uses connection `openbao` in Kubernetes namespace `openbao-config`.
- The connection's OpenBao identity needs `create`, `read` and `update` on
  `sys/policies/acl/guide-app-read`. See [permissions for the operator](../operate/openbao-policy.md).
- Use an unused policy name. To take over an existing policy, first follow the
  [import guide](import.md) and review its current permissions.

The example grants read access to application data in a KV v2 mount named `apps`. The policy
can be created before that mount exists; [Kubernetes auth](kubernetes-auth.md) creates the mount
and connects a workload to this policy.

## Declare the policy

Save this as `app-read.yaml`:

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: guide-app-read
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: guide-app-read
  policy: |
    path "apps/data/app/*" {
      capabilities = ["read"]
    }
```

```bash
kubectl apply -f app-read.yaml
kubectl -n openbao-config wait policy/guide-app-read --for=condition=Ready --timeout=2m
kubectl -n openbao-config get policy guide-app-read
```

Expect `READY` and `SYNCED` to be `True`. If the resource is not ready, inspect its conditions
and events with `kubectl -n openbao-config describe policy guide-app-read`.

## Verify the OpenBao result

Point the `bao` CLI at the same server and OpenBao namespace as the connection, using an identity
allowed to read this policy:

```bash
bao policy read guide-app-read
```

The returned HCL must contain the `apps/data/app/*` path and only the `read` capability.
`metadata.name` identifies the Kubernetes object; `spec.name` identifies the policy in OpenBao.
They happen to match here.

Attach the policy through a role's `policyRefs`, as in [Kubernetes authentication](kubernetes-auth.md),
or through an identity [Group](identity.md). Keep policy changes in the manifest and let your
[GitOps delivery](argocd.md) apply them. Avoid having another tool continuously write the same
OpenBao policy.

## Remove the Kubernetes resource

If another resource names this Policy through `policyRefs`, update that reference before deleting
the Policy resource. Otherwise the dependent resource reports `ReferenceNotReady`.

```bash
kubectl -n openbao-config delete policy guide-app-read
```

This example explicitly uses `deletionPolicy: Orphan`, so the OpenBao policy remains. Removing a
policy from OpenBao is a separate access change: review its users first. For lifecycle choices,
see [ownership and deletion](../operate/backup-restore.md#deletion-policy-and-orphaned-objects).

For identity-based path templates, continue with [templated policies](templated-policies.md).
The [Policy API reference](../reference/api/sys.md#policy) documents all fields.
