---
title: Manage OpenBao KV secrets from Kubernetes
description: "Enable a KV v2 mount, copy values from Kubernetes Secrets, and generate one-time values with the operator."
---

# Manage KV secrets from Kubernetes

Create a KV version 2 secrets engine with OpenBao Config Operator and fill it from Kubernetes:
secrets copied from Kubernetes Secrets, and values the operator generates.

The operator writes into OpenBao; it does not copy secrets out of OpenBao into Kubernetes
([ADR 0007](../adr/0007-no-secret-sync.md)). Workloads read OpenBao directly, or through a tool such
as the External Secrets Operator. On this page, only `RandomSecret` can also write its value to a
Kubernetes Secret (`writeSecretRef`).

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/mounts"             { capabilities = ["read"] }
    path "sys/mounts/secret"      { capabilities = ["create", "read", "update"] }
    path "sys/mounts/secret/tune" { capabilities = ["create", "read", "update"] }
    path "secret/data/*"          { capabilities = ["create", "read", "update"] }
    path "secret/metadata/*"      { capabilities = ["read"] }
    path "secret/destroy/*"       { capabilities = ["update"] }
    path "secret/config"          { capabilities = ["read", "update"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable the engine

Every resource on this page uses the KV v2 mount `secret`, connection `openbao` and Kubernetes
namespace `openbao-config`. Save this as `kv-mount.yaml`; it creates that mount or adopts the
existing KV v2 mount on a dev server:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: SecretsMount
metadata:
  name: secret
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  creationPolicy: CreateOrAdopt
  deletionPolicy: Orphan
  path: secret
  type: kv
  options:
    version: "2"
```

```bash
kubectl apply -f kv-mount.yaml
kubectl -n openbao-config wait secretsmount/secret --for=condition=Ready --timeout=2m
```

If another operator resource already owns `secret`, reuse that resource instead of creating a
second owner. For a different mount, change `path` here and every `mountPath` below together.
Every mount field, including lease TTLs and audit settings:
[every field](../reference/examples/sys.md#secretsmount).

A `KVConfig` sets the engine's version limits. Save this as `kv-config.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: KVConfig
metadata:
  name: secret-config
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: secret
  maxVersions: 10
  deleteVersionAfter: 768h
```

```bash
kubectl apply -f kv-config.yaml
kubectl -n openbao-config wait kvconfig/secret-config --for=condition=Ready --timeout=2m
```

## 2. Write a secret from Kubernetes Secrets

A `KVSecret` writes the keys of one or more Kubernetes Secrets to one KV path. `dataFrom` copies
every key of a Secret; `data` maps single keys. The values are hashed into `status.writeOnlyHash`,
never stored in status, and a change of a referenced Secret writes a new version. Save this as
`kv-secret.yaml` (the password and token values are fake):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: app-db
  namespace: openbao-config
type: Opaque
stringData:
  username: app
  password: fake-db-password-not-real
---
apiVersion: v1
kind: Secret
metadata:
  name: app-api
  namespace: openbao-config
type: Opaque
stringData:
  token: fake-api-token-not-real
---
apiVersion: secrets.obco.dev/v1alpha1
kind: KVSecret
metadata:
  name: app
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: secret
  path: app/config
  kvVersion: 2
  dataFrom:
    - secretRef:
        name: app-db
  data:
    api-token:
      secretKeyRef:
        name: app-api
        key: token
```

```bash
kubectl apply -f kv-secret.yaml
kubectl -n openbao-config wait kvsecret/app --for=condition=Ready --timeout=2m
```

Deleting this resource keeps the KV data (the default `deletionPolicy: Orphan`). To remove the
data with the resource, set `deletionPolicy: Delete` and the annotation
`obco.dev/allow-destructive-delete: "true"`; without the annotation, deletion stops with
`DeleteGuarded`.

## 3. Generate a value

A `RandomSecret` generates a value once and stores it under one key of a KV secret. Change
`refreshNonce` to generate a new value. `writeSecretRef` also writes the value to a Kubernetes
Secret that the resource owns; `passwordPolicyName` generates it with an OpenBao password policy
instead of `generator`. Save this as `session-key.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: RandomSecret
metadata:
  name: app-session-key
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: secret
  path: app/random
  kvVersion: 2
  key: session-key
  generator:
    length: 48
    charset: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_
  writeSecretRef:
    name: app-session-key
```

```bash
kubectl apply -f session-key.yaml
kubectl -n openbao-config wait randomsecret/app-session-key --for=condition=Ready --timeout=2m
```

For password-policy generation, see
[Generate an application credential and deliver it with ESO](../tutorials/generated-credentials.md). Every field:
[RandomSecret](../reference/examples/secrets.md#randomsecret).

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait kvsecret/app randomsecret/app-session-key \
  --for=condition=Ready --timeout=2m
```

OpenBao holds the secret metadata and data:

```bash
bao kv metadata get secret/app/config
bao kv get secret/app/config
```

The metadata shows the current version. `bao kv get` shows the keys written from the Kubernetes
Secrets.

## Troubleshooting

| symptom | check |
|---------|-------|
| `KVSecret` or `RandomSecret` reports `ReferenceNotReady` | the mount does not exist yet, or a referenced Secret or key is missing |
| the resource reports `PermissionDenied` | the connection's policy lacks `secret/data/*` |
| deleting the resource reports `DeleteGuarded` | the annotation `obco.dev/allow-destructive-delete: "true"` is missing |
| `RandomSecret` reports `SecretNotOwned` | a Secret named in `writeSecretRef` already exists and was not created by this resource |

## Related

- API reference: [SecretsMount](../reference/api/sys.md#secretsmount),
  [KVConfig](../reference/api/secrets.md#kvconfig),
  [KVSecret](../reference/api/secrets.md#kvsecret),
  [RandomSecret](../reference/api/secrets.md#randomsecret).
- [Access model](access-model.md): give workloads and people access to KV paths.
- [Generate an application credential and deliver it with ESO](../tutorials/generated-credentials.md): project a generated
  value with External Secrets Operator.
- [Quickstart on kind](../get-started/quickstart.md): a `RandomSecret` read back by a workload.
