---
title: Encrypt application data with OpenBao transit
description: "Grant a workload encrypt and decrypt access to a transit key through Kubernetes authentication, then rotate the key and rewrap ciphertext."
---

# Encrypt application data with OpenBao transit

Give a workload encrypt and decrypt access to a transit key managed by OpenBao Config Operator,
then rotate the key and rewrap ciphertext. The application authenticates with its ServiceAccount;
it never receives an OpenBao administrator token or the key material.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/policies/acl/transit-app"     { capabilities = ["create", "read", "update", "delete"] }
    path "auth/kubernetes/role/transit-app" { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The `TransitKey` `app-encryption` (key `app` on the mount `transit`) from
  [Manage transit encryption keys](../guides/transit.md) is `Ready`.
- Kubernetes authentication is configured at the mount `kubernetes` (`AuthMount`,
  `KubernetesAuthConfig`) as in [Configure Kubernetes authentication](../guides/kubernetes-auth.md).
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Grant the workload encrypt and decrypt

Create a policy that allows only the operations the application needs on this key, then bind the
ServiceAccount `app` in the Kubernetes namespace `apps` to that policy. Save this as
`transit-access.yaml`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: apps
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: app
  namespace: apps
---
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: transit-app
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: transit-app
  policy: |
    path "transit/encrypt/app" { capabilities = ["update"] }
    path "transit/decrypt/app" { capabilities = ["update"] }
    path "transit/rewrap/app" { capabilities = ["update"] }
    path "auth/token/revoke-self" { capabilities = ["update"] }
---
apiVersion: auth.obco.dev/v1alpha1
kind: KubernetesAuthRole
metadata:
  name: transit-app
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  name: transit-app
  boundServiceAccountNames:
    - app
  boundServiceAccountNamespaces:
    - apps
  audience: openbao
  tokenTTL: 15m
  tokenNoDefaultPolicy: true
  policyRefs:
    - name: transit-app
```

```bash
kubectl apply -f transit-access.yaml
kubectl -n openbao-config wait aclpolicies.sys.obco.dev/transit-app \
  kubernetesauthrole/transit-app --for=condition=Ready --timeout=2m
```

Keep administration paths and other keys out of this policy. The workload requests a projected
ServiceAccount token with audience `openbao`, logs in at `auth/kubernetes/login` with role
`transit-app`, performs its operation, and revokes its token. A small Go sample that performs
these calls is in [examples/apps/transit](../../examples/apps/transit/).

## 2. Rotate the key and rewrap ciphertext

| operation | what changes | where plaintext appears |
| --- | --- | --- |
| Rotate a key, then rewrap | Ciphertext moves to a newer version of the same key. | Inside OpenBao; the rewrap response contains ciphertext. |
| Migrate to a different key | The application decrypts under the source key and encrypts under the destination key. | Inside OpenBao and the trusted application process. |

Changing `rotateNonce` on the `TransitKey` to a new value requests a new key version; it does not
rewrite application data. In Git, change `rotateNonce` in the `transit-key.yaml` of
[Manage transit encryption keys](../guides/transit.md); from the command line:

```bash
kubectl -n openbao-config patch transitkey app-encryption --type=merge \
  -p '{"spec":{"rotateNonce":"rotation-2"}}'
kubectl -n openbao-config wait transitkey/app-encryption --for=condition=Ready --timeout=2m
bao read transit/keys/app
```

OpenBao shows a higher `latest_version`. OpenBao also rotates the key automatically every
`autoRotatePeriod` (at least one hour). Rewrap each stored ciphertext to the latest version with
a token that holds the `transit-app` policy (step 3 shows how the workload logs in for one):

```bash
BAO_TOKEN=<token> bao write transit/rewrap/app ciphertext=<ciphertext>
```

`<token>` is that token and `<ciphertext>` is a value the application stored. Persist and verify
the replacement ciphertext before raising `minDecryptionVersion` or retiring a key. Rewrapping
cannot recover data after the key version required to decrypt it has been destroyed.

!!! warning "Key deletion destroys ciphertext"
    Keeping `deletionPolicy: Orphan` protects against accidental deletion of the custom resource. Destroying the
    OpenBao storage or deleting the key with the destructive-delete guard still loses data that
    depended on that key. See [Keep transit keys non-exportable](../security/transit-key-policy.md).

## 3. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait transitkey/app-encryption \
  aclpolicies.sys.obco.dev/transit-app kubernetesauthrole/transit-app \
  --for=condition=Ready --timeout=2m
```

Log in as the workload with a token for the role's audience:

```bash
bao write -field=token auth/kubernetes/login role=transit-app \
  jwt="$(kubectl -n apps create token app --audience openbao)"
```

With the token from that output, encrypt with the key `app`, decrypt the result, and try another
key name:

```bash
BAO_TOKEN=<token> bao write transit/encrypt/app plaintext="$(printf 'hello' | base64)"
BAO_TOKEN=<token> bao write transit/decrypt/app ciphertext=<ciphertext>
BAO_TOKEN=<token> bao write transit/encrypt/other plaintext="$(printf 'hello' | base64)"
```

`<token>` is the token from the login and `<ciphertext>` is the `ciphertext` the first command
returns. The first two commands succeed and the decrypt returns `aGVsbG8=`, the base64 of `hello`;
the third is refused with `permission denied`.

## Troubleshooting

| symptom | check |
|---------|-------|
| `KubernetesAuthRole` reports `ReferenceNotReady` | the `ACLPolicy` and the Kubernetes auth mount and config are `Ready` |
| login fails with audience or ServiceAccount errors | `audience` on the role matches the projected token; name and Kubernetes namespace match the bindings |
| encrypt fails with permission denied | the policy paths use the mount and key name (`transit/encrypt/app`) |
| `rotateNonce` changed but the version did not | the first value at creation does not rotate; change it again |

## Related

- API reference: [TransitKey](../reference/api/secrets.md#transitkey),
  [ACLPolicy](../reference/api/sys.md#aclpolicy),
  [KubernetesAuthRole](../reference/api/auth.md#kubernetesauthrole).
- [Manage transit encryption keys](../guides/transit.md).
- [Configure Kubernetes authentication](../guides/kubernetes-auth.md).
