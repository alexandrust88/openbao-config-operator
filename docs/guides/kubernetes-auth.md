# Kubernetes auth

This guide sets up OpenBao's Kubernetes auth method from Kubernetes resources, so that workloads
log in with their ServiceAccount token and get exactly the policies their role grants.

## Before you begin

- A `Ready` connection whose OpenBao policy allows the paths below
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):
    - `sys/auth` (`read`), and `sys/auth/<mount>` and `sys/auth/<mount>/tune` (`create`, `read`,
      `update`, `delete`, `sudo`) to manage the mount itself;
    - `auth/<mount>/config` and `auth/<mount>/role/*` to manage its configuration and roles.
- If the auth method already exists and someone else manages it, skip step 1 and set only the
  roles.

## 1. Enable the auth method and configure it

An `AuthMount` enables the method, and a `KubernetesAuthConfig` tells it how to review tokens. This
example also creates the ServiceAccount whose token OpenBao uses for the TokenReview API, binds it
to `system:auth-delegator`, and enables the KV engine that the role's policy grants:

```yaml title="examples/policies/templated/00-kubernetes-auth.yaml"
--8<-- "examples/policies/templated/00-kubernetes-auth.yaml"
```

Replace `kubernetesCACert` with the cluster CA (`kubectl config view --raw --minify -o
jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d`).

If OpenBao runs as a pod in the same cluster, it can review tokens with its own ServiceAccount
instead: set only `kubernetesHost: https://kubernetes.default.svc:443`, leave out
`kubernetesCACert`, `tokenReviewerJWTSecretRef` and `disableLocalCAJWT`, and bind OpenBao's
ServiceAccount to `system:auth-delegator`. The [quickstart](../get-started/quickstart.md) works
that way.

```bash
kubectl apply -f examples/policies/templated/00-kubernetes-auth.yaml
kubectl -n openbao-config wait authmount/kubernetes kubernetesauthconfig/kubernetes \
  --for=condition=Ready --timeout=2m
```

## 2. Create a policy and a role

The role binds ServiceAccount names and namespaces to policies. Name `Policy` resources in
`policyRefs` instead of repeating policy names: the role then waits until each policy exists in
OpenBao. A minimal role:

```yaml title="examples/quickstart/kubernetesauthrole.yaml"
--8<-- "examples/quickstart/kubernetesauthrole.yaml"
```

Every field of the kind, including token limits and bound CIDRs:

??? example "examples/auth/kubernetesauthrole.yaml"

    ```yaml
    --8<-- "examples/auth/kubernetesauthrole.yaml"
    ```

Set `audience` and have the workload request a token for it. A role without an audience accepts
any token the API server signed for the ServiceAccount, including the automounted one, which is
also a Kubernetes API credential.

Bind one ServiceAccount name per role where you can. `"*"` in `boundServiceAccountNames` or
`boundServiceAccountNamespaces` lets every matching ServiceAccount use the role's policies.

!!! tip "Tenants writing their own roles"
    A tenant connection that may write roles could bind ServiceAccounts of another Kubernetes
    namespace. Pin `bound_service_account_namespaces` with `allowed_parameters` in the tenant's
    OpenBao policy: [OpenBao policy: tenant operator](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace).

## 3. Check that it worked

The commands use the minimal role of step 2: `quickstart-app` in the namespace `quickstart`,
which binds the ServiceAccount `app` of `quickstart`.

```bash
kubectl -n quickstart wait kubernetesauthrole/quickstart-app --for=condition=Ready --timeout=2m
bao read auth/kubernetes/role/quickstart-app
```

Log in as the workload with a token for the role's audience:

```bash
bao write auth/kubernetes/login role=quickstart-app \
  jwt="$(kubectl -n quickstart create token app --audience openbao)"
```

The response lists the token's policies.

## Troubleshooting

| symptom | check |
|---------|-------|
| `AuthMount` reports `PermissionDenied` | enabling and tuning an auth method needs `sudo` on `sys/auth/<mount>` |
| role reports `ReferenceNotReady` | a `Policy` in `policyRefs` is missing, not `Ready`, or uses another `connectionRef` or `spec.namespace` than the role |
| login fails with `permission denied` and `service account name not authorized` | `boundServiceAccountNames` and `boundServiceAccountNamespaces` |
| login fails with `invalid audience` | the token was requested without `--audience`, or for another audience than the role's `audience` |
| login fails with a TokenReview error | the reviewer ServiceAccount's `system:auth-delegator` binding, `kubernetesHost` and the CA certificate |

## Related

- API reference: [AuthMount](../reference/api/sys.md#authmount),
  [KubernetesAuthConfig](../reference/api/auth.md#kubernetesauthconfig),
  [KubernetesAuthRole](../reference/api/auth.md#kubernetesauthrole),
  [Policy](../reference/api/sys.md#policy).
- [Access model](access-model.md): `AccessGrant` writes the policy and one role per subject for
  you.
- [Templated policies](templated-policies.md): one policy for many ServiceAccounts.
