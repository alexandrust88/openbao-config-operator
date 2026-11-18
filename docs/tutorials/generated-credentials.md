---
title: Generate an OpenBao credential and deliver it with ESO
description: "Generate a password in OpenBao with RandomSecret and project one key into a Kubernetes Secret with External Secrets Operator."
---

# Generate an application credential and deliver it with ESO

Generate a password in OpenBao with OpenBao Config Operator and copy only that key into a
Kubernetes Secret with External Secrets Operator (ESO). The operator owns the value in OpenBao;
ESO owns the projected Secret.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/policies/password/application-passwords"          { capabilities = ["create", "read", "update"] }
    path "sys/policies/password/application-passwords/generate" { capabilities = ["read"] }
    path "secret/data/*"                                        { capabilities = ["create", "read", "update"] }
    path "secret/metadata/*"                                    { capabilities = ["read"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- A KV version 2 mount at `secret` is `Ready` ([Manage KV secrets](../guides/kv-secrets.md)).
- External Secrets Operator is installed.
- The `ACLPolicy` `app-read` ([Manage OpenBao policies](../guides/policies.md)) and the
  `KubernetesAuthRole` `app` with the ServiceAccount `app` in the Kubernetes namespace `apps`
  ([Configure Kubernetes authentication](../guides/kubernetes-auth.md)) are `Ready`. `app-read`
  grants `read` on `secret/data/app/*`, which covers the path this page generates.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Define the password policy

A `PasswordPolicy` stores the length and character rules OpenBao uses when generating. Save this
as `password-policy.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: PasswordPolicy
metadata:
  name: application-passwords
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: application-passwords
  policy: |
    length = 32
    rule "charset" {
      charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
      min-chars = 32
    }
```

```bash
kubectl apply -f password-policy.yaml
kubectl -n openbao-config wait passwordpolicy/application-passwords \
  --for=condition=Ready --timeout=2m
```

## 2. Generate the credential in OpenBao

A `RandomSecret` generates the value once and stores it under one key. Omit `writeSecretRef` so
ESO alone owns the Kubernetes Secret; do not point both controllers at the same destination. Save
this as `random-secret.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: RandomSecret
metadata:
  name: application-password
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  creationPolicy: CreateOrAdopt
  mountPath: secret
  path: app/generated
  kvVersion: 2
  key: password
  passwordPolicyName: application-passwords
  refreshNonce: initial
```

```bash
kubectl apply -f random-secret.yaml
kubectl -n openbao-config wait randomsecret/application-password \
  --for=condition=Ready --timeout=2m
```

Changing generator settings does not regenerate an existing value. Change `refreshNonce` to a new
string when you want a new password (step 5). Selecting one property in ESO limits the
projection, not OpenBao access to other keys at the same path: put independently authorised
values at separate paths. `deletionPolicy: Orphan` keeps the OpenBao value when you delete the
`RandomSecret`.

## 3. Create the secret store

ESO reads OpenBao through a `SecretStore` in the workload's Kubernetes namespace. This one logs in
with the Kubernetes auth role `app` as the ServiceAccount `app` and reads the KV version 2 mount
`secret` in the root OpenBao namespace. Copy the CA certificate you used in
[Connect to OpenBao](../get-started/connect.md) into `apps`:

```bash
kubectl -n apps create configmap openbao-ca --from-file=ca.crt=ca.crt
```

Save this as `secret-store.yaml`:

```yaml
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: openbao
  namespace: apps
spec:
  provider:
    vault:
      server: https://<openbao-host>:8200
      path: secret
      version: v2
      caProvider:
        type: ConfigMap
        name: openbao-ca
        key: ca.crt
      auth:
        kubernetes:
          mountPath: kubernetes
          role: app
          serviceAccountRef:
            name: app
            audiences:
              - openbao
```

Replace `<openbao-host>` with the host name of the OpenBao API as ESO's pods reach it, for example
`openbao.openbao.svc`. Leave out `caProvider` and the ConfigMap when a public CA signed the server
certificate. The [ESO Vault provider](https://external-secrets.io/latest/provider/hashicorp-vault/)
documentation lists the other login methods and a `ClusterSecretStore` variant.

```bash
kubectl apply -f secret-store.yaml
kubectl -n apps wait secretstore/openbao --for=condition=Ready --timeout=2m
```

## 4. Project the key with External Secrets Operator

Save this as `external-secret.yaml`. The store's `path: secret` names the mount, so
`remoteRef.key` is the path inside it:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: generated-application
  namespace: apps
spec:
  refreshInterval: 1m
  secretStoreRef:
    name: openbao
    kind: SecretStore
  target:
    name: generated-application
    creationPolicy: Owner
  data:
    - secretKey: password
      remoteRef:
        key: app/generated
        property: password
```

`creationPolicy: Owner` on the target removes the Kubernetes Secret when you delete the `ExternalSecret`.

```bash
kubectl apply -f external-secret.yaml
kubectl -n apps wait externalsecret/generated-application \
  --for=condition=Ready --timeout=2m
```

!!! warning "Projected secrets remain sensitive"
    No credential value appears in these manifests, but the projected Kubernetes Secret still
    holds the password and may be stored in etcd and backups. Keep Secret read permissions narrow
    and configure Kubernetes encryption at rest.

## 5. Rotate the credential

Set a new nonce, then wait for the operator to write the new value:

```bash
kubectl -n openbao-config patch randomsecret application-password --type=merge \
  -p '{"spec":{"refreshNonce":"rotation-2"}}'
kubectl -n openbao-config wait randomsecret/application-password \
  --for=condition=Ready --timeout=2m
```

ESO picks up the new KV version at its next refresh (`refreshInterval: 1m`). To deliver it now, ask
ESO to refresh, then print the time of its last refresh:

```bash
kubectl -n apps annotate externalsecret generated-application force-sync="$(date +%s)" --overwrite
kubectl -n apps get externalsecret generated-application -o jsonpath='{.status.refreshTime}{"\n"}'
```

The refresh time is later than the patch above once the Kubernetes Secret holds the new password.

A ready Kubernetes Secret does not mean that the application has reloaded it: processes that read
environment variables need a restart; mounted-file consumers must reload changed files. This
pattern creates an application credential; it does not update a database user, renew a lease, or
coordinate cutover. For database credentials, see
[Issue dynamic database credentials](dynamic-database-credentials.md).

## 6. Check that it worked

The operator resource and the projection are `Ready`:

```bash
kubectl -n openbao-config wait randomsecret/application-password \
  --for=condition=Ready --timeout=2m
kubectl -n apps wait externalsecret/generated-application \
  --for=condition=Ready --timeout=2m
kubectl -n apps get secret generated-application
```

OpenBao holds the generated key (do not print the value in shared logs):

```bash
bao kv metadata get secret/app/generated
```

The metadata shows a current version, which grows by one with each rotation. The Kubernetes
Secret `generated-application` in `apps` contains the key `password`.

## Troubleshooting

| symptom | check |
|---------|-------|
| `RandomSecret` reports `ReferenceNotReady` | the KV mount or `PasswordPolicy` is missing or not `Ready` |
| `RandomSecret` reports `PermissionDenied` | the connection's policy lacks password-policy or KV write paths |
| `SecretStore` is not `Ready` | `kubectl -n apps describe secretstore openbao`: the server address and CA, and that the role `app` admits the ServiceAccount `app` with the audience `openbao` |
| `ExternalSecret` is not `Ready` | the store is `Ready`, and `remoteRef.key` is the path inside the mount `secret` |
| rotation left the old password in the Secret | `status.refreshTime` of the `ExternalSecret` is older than the rotation: annotate it with `force-sync` or wait for `refreshInterval` |

## Related

- API reference: [PasswordPolicy](../reference/api/sys.md#passwordpolicy),
  [RandomSecret](../reference/api/secrets.md#randomsecret).
- [Manage KV secrets from Kubernetes](../guides/kv-secrets.md).
- [ADR 0007](../adr/0007-no-secret-sync.md): why the operator does not sync secrets out of OpenBao.
