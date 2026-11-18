---
title: Configure OpenBao Kubernetes authentication with GitOps
description: Use AuthMount, KubernetesAuthConfig and KubernetesAuthRole resources to bind a workload ServiceAccount to an OpenBao ACL policy.
---

# Configure Kubernetes authentication

Workloads log in to OpenBao with their ServiceAccount token and get exactly the policies their
role grants. You declare the auth method, its configuration and the role as Kubernetes resources,
and the OpenBao Config Operator writes them to OpenBao.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/auth"                 { capabilities = ["read"] }
    path "sys/auth/kubernetes"      { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "sys/auth/kubernetes/tune" { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "auth/kubernetes/config"   { capabilities = ["create", "read", "update", "delete"] }
    path "auth/kubernetes/role/*"   { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

    If you chose Kubernetes auth in Connect to OpenBao, the mount `kubernetes` and its configuration
    exist already and the operator's own connection logs in through them: append only the
    `auth/kubernetes/role/*` line and go to step 3.

- The `ACLPolicy` named `app-read` from [Manage OpenBao policies with Kubernetes](policies.md) is
  `Ready`.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable the auth method

An `AuthMount` enables the Kubernetes auth method at `auth/kubernetes`. Skip steps 1 and 2 when
the mount exists already (see Before you begin) or someone else manages the method. To manage an
existing mount from Git instead, add `creationPolicy: Adopt` to the `AuthMount` and the
`KubernetesAuthConfig`; the default, `Create`, refuses an existing object with the reason
`Conflict`. An adopted `KubernetesAuthConfig` replaces the configuration logins use, so give it
the values that work today. Save this as `auth-mount.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: AuthMount
metadata:
  name: kubernetes
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: kubernetes
  type: kubernetes
```

```bash
kubectl apply -f auth-mount.yaml
kubectl -n openbao-config wait authmount/kubernetes --for=condition=Ready --timeout=2m
```

## 2. Configure token review

OpenBao checks each login by sending the workload's token to the Kubernetes TokenReview API. A
`KubernetesAuthConfig` gives it the API server address, the cluster CA and the token of a
ServiceAccount bound to `system:auth-delegator`. Save this as `auth-config.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: openbao-token-reviewer
  namespace: openbao-config
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: openbao-token-reviewer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - kind: ServiceAccount
    name: openbao-token-reviewer
    namespace: openbao-config
---
apiVersion: v1
kind: Secret
metadata:
  name: openbao-token-reviewer
  namespace: openbao-config
  annotations:
    kubernetes.io/service-account.name: openbao-token-reviewer
type: kubernetes.io/service-account-token
---
apiVersion: auth.obco.dev/v1alpha1
kind: KubernetesAuthConfig
metadata:
  name: kubernetes
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  kubernetesHost: <kubernetes-api-url>
  kubernetesCACert: |
    <cluster-ca-pem>
  tokenReviewerJWTSecretRef:
    name: openbao-token-reviewer
    key: token
  disableLocalCAJWT: true
```

Replace `<kubernetes-api-url>` with the API server address as OpenBao reaches it, and
`<cluster-ca-pem>` with the cluster CA certificate in PEM form, indented like the placeholder.
These commands print the address and the CA certificate your kubeconfig uses:

```bash
kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}'
kubectl config view --raw --minify \
  -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d
```

```bash
kubectl apply -f auth-config.yaml
kubectl -n openbao-config wait kubernetesauthconfig/kubernetes --for=condition=Ready --timeout=2m
```

If OpenBao runs as a pod in the same cluster, it can review tokens with its own ServiceAccount
instead: apply only the `KubernetesAuthConfig`, set only
`kubernetesHost: https://kubernetes.default.svc:443`, leave out `kubernetesCACert`,
`tokenReviewerJWTSecretRef` and `disableLocalCAJWT`, and bind OpenBao's ServiceAccount to
`system:auth-delegator`. The [quickstart](../get-started/quickstart.md) works that way.
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

## 3. Create a role

A `KubernetesAuthRole` binds a ServiceAccount name and its Kubernetes namespace to policies. This
one admits the ServiceAccount `app` in the Kubernetes namespace `apps`; create them if they do not
exist:

```bash
kubectl create namespace apps --dry-run=client -o yaml | kubectl apply -f -
kubectl -n apps create serviceaccount app --dry-run=client -o yaml | kubectl apply -f -
```

`policyRefs` makes the role wait until the `ACLPolicy` is `Ready`, then attaches its OpenBao policy.
Save this as `app-role.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: KubernetesAuthRole
metadata:
  name: app
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  name: app
  boundServiceAccountNames:
    - app
  boundServiceAccountNamespaces:
    - apps
  audience: openbao
  tokenTTL: 15m
  policyRefs:
    - name: app-read
```

```bash
kubectl apply -f app-role.yaml
```

Token limits, bound CIDRs and the Kubernetes namespace selector are in the example with
[every field](../reference/examples/auth.md#kubernetesauthrole).

Set `audience` and have the workload request a token for it. A role without an audience accepts
any token the API server signed for the ServiceAccount, including the automounted one, which is
also a Kubernetes API credential.

Bind one ServiceAccount name per role where you can. `"*"` in `boundServiceAccountNames` or
`boundServiceAccountNamespaces` lets every matching ServiceAccount use the role's policies.

!!! warning "Tenants writing their own roles"
    A tenant connection that may write roles could bind ServiceAccounts of another Kubernetes
    namespace. Pin `bound_service_account_namespaces` with `allowed_parameters` in the tenant's
    OpenBao policy: [OpenBao policy: tenant operator](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace).

## 4. Check that it worked

The role is `Ready`:

```bash
kubectl -n openbao-config wait kubernetesauthrole/app --for=condition=Ready --timeout=2m
```

After a later edit, check `Synced` as well, because `Ready` keeps its last value while a write
fails:

```bash
kubectl -n openbao-config get authmounts,kubernetesauthconfigs,kubernetesauthroles
```

OpenBao holds the role. Set the CLI to the same OpenBao namespace as the connection:

```bash
bao read auth/kubernetes/role/app
```

Log in as the workload with a token for the role's audience:

```bash
bao write auth/kubernetes/login role=app \
  jwt="$(kubectl -n apps create token app --audience openbao)"
```

The response lists `app-read` in the token's policies.

## Troubleshooting

| symptom | check |
|---------|-------|
| `AuthMount` or `KubernetesAuthConfig` reports `Conflict` | the mount or its configuration exists already, for example from Connect to OpenBao: skip steps 1 and 2, or set `creationPolicy: Adopt` (step 1) |
| `AuthMount` reports `PermissionDenied` | enabling and tuning an auth method needs `sudo` on `sys/auth/kubernetes` and `sys/auth/kubernetes/tune` |
| `KubernetesAuthConfig` reports `ReferenceNotReady` | the Secret `openbao-token-reviewer` and its key `token` exist in the Kubernetes namespace of the resource |
| role reports `ReferenceNotReady` | the `ACLPolicy` in `policyRefs` exists in the role's Kubernetes namespace and is `Ready` |
| role reports `ReconcileError` and names a policy | the `ACLPolicy` uses the same `connectionRef` and `spec.openbaoNamespace` as the role |
| login fails with `permission denied` and `service account name not authorized` | `boundServiceAccountNames` and `boundServiceAccountNamespaces` |
| login fails with `invalid audience` | the token was requested without `--audience`, or for another audience than the role's `audience` |
| login fails with a TokenReview error | the reviewer ServiceAccount's `system:auth-delegator` binding, `kubernetesHost` and the CA certificate |

## Related

- API reference: [AuthMount](../reference/api/sys.md#authmount),
  [KubernetesAuthConfig](../reference/api/auth.md#kubernetesauthconfig),
  [KubernetesAuthRole](../reference/api/auth.md#kubernetesauthrole),
  [ACLPolicy](../reference/api/sys.md#aclpolicy).
- [Manage OpenBao policies with Kubernetes](policies.md): declare and verify the ACL before
  attaching it to a role.
- [Grant KV access with access levels, grants and layouts](access-model.md): `AccessGrant` writes
  the policy and one role per subject for you.
- [Write OpenBao templated policies with Kubernetes](templated-policies.md): one policy for many
  ServiceAccounts.
