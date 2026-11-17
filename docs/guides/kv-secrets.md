---
description: "Manage OpenBao KV configuration and secret resources with Kubernetes manifests and referenced Secret inputs."
---

# KV secrets

This guide creates a KV version 2 secrets engine and fills it from Kubernetes: secrets copied from
Kubernetes Secrets, and values the operator generates.

The operator writes into OpenBao; it does not copy secrets out of OpenBao into Kubernetes
([ADR 0007](../adr/0007-no-secret-sync.md)). Workloads read OpenBao directly, or through a tool such
as the External Secrets Operator. Only values the operator generates can also be written to a
Kubernetes Secret (`writeSecretRef`).

## Before you begin

A `Ready` connection whose OpenBao policy allows `sys/mounts` (`read`), `sys/mounts/<mount>` and
`sys/mounts/<mount>/tune` to manage the engine, and `<mount>/data/*` (`create`, `read`,
`update`), `<mount>/metadata/*` (`read`) and `<mount>/destroy/*` (`update`) for destructive history cleanup, and `<mount>/config`
(`read`, `update`) for the engine configuration
([OpenBao policy for the operator](../operate/openbao-policy.md)).

## 1. Enable the engine

Every resource on this page uses the KV v2 mount `secret`, connection `openbao` and Kubernetes
namespace `openbao-config`. Save this as `kv-mount.yaml`; it creates that mount or adopts the
existing KV v2 mount on a dev server:

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: SecretsMount
metadata:
  name: guide-secret
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
kubectl -n openbao-config wait secretsmount/guide-secret --for=condition=Ready --timeout=2m
```

If another operator resource already owns `secret`, reuse that resource instead of creating a
second owner. For a different mount, change `path` here and every `mountPath` below together.

Every mount field, including lease TTLs and audit settings:

??? example "examples/sys/secretsmount.yaml"

    ```yaml
    --8<-- "examples/sys/secretsmount.yaml"
    ```

A `KVConfig` sets the engine's version limits:

```yaml title="examples/secrets/kvconfig.yaml"
--8<-- "examples/secrets/kvconfig.yaml"
```

```bash
kubectl apply -f examples/secrets/kvconfig.yaml
```

## 2. Write a secret from Kubernetes Secrets

A `KVSecret` writes the keys of one or more Kubernetes Secrets to one KV path. `dataFrom` copies
every key of a Secret; `data` maps single keys. The values are hashed into `status.writeOnlyHash`,
never stored in status, and a change of a referenced Secret writes a new version:

```yaml title="examples/secrets/kvsecret.yaml"
--8<-- "examples/secrets/kvsecret.yaml"
```

```bash
kubectl apply -f examples/secrets/kvsecret.yaml
```

The KV data is removed with the resource only when `deletionPolicy: Delete` is set and the
resource carries `openbaoconfig.io/allow-destructive-delete: "true"`, as in the example.

## 3. Generate a value

A `RandomSecret` generates a value once and stores it under one key of a KV secret. Change
`refreshNonce` to generate a new value. `writeSecretRef` also writes the value to a Kubernetes
Secret that the resource owns; `passwordPolicyName` generates it with an OpenBao password policy
instead of `generator`:

Save this minimal example as `session-key.yaml`:

```yaml
apiVersion: secrets.openbaoconfig.io/v1alpha1
kind: RandomSecret
metadata:
  name: obco-app-session-key
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: secret
  path: obco/app-random
  kvVersion: 2
  key: session-key
  generator:
    length: 48
    charset: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_
  writeSecretRef:
    name: obco-app-session-key
```

```bash
kubectl apply -f session-key.yaml
```

For password-policy generation, see [the complete RandomSecret examples](../../examples/secrets/randomsecret.yaml).
Their second resource requires the `db-passwords` policy from
[passwordpolicy.yaml](../../examples/sys/passwordpolicy.yaml) before it can become Ready.

## 4. Check that it worked

```bash
kubectl -n openbao-config wait kvsecret/obco-app randomsecret/obco-app-session-key \
  --for=condition=Ready --timeout=2m
bao kv metadata get secret/obco/app
```

The metadata shows the current version. `bao kv get secret/obco/app` shows the data.

## Troubleshooting

| symptom | check |
|---------|-------|
| `KVSecret` or `RandomSecret` reports `ReferenceNotReady` | the mount does not exist yet, or a referenced Secret or key is missing |
| the resource reports `PermissionDenied` | the connection's policy lacks `<mount>/data/*` |
| deleting the resource reports `DeleteGuarded` | the annotation `openbaoconfig.io/allow-destructive-delete: "true"` is missing |
| `RandomSecret` reports `SecretNotOwned` | a Secret named in `writeSecretRef` already exists and was not created by this resource |

## Related

- API reference: [SecretsMount](../reference/api/sys.md#secretsmount),
  [KVConfig](../reference/api/secrets.md#kvconfig),
  [KVSecret](../reference/api/secrets.md#kvsecret),
  [RandomSecret](../reference/api/secrets.md#randomsecret).
- [Access model](access-model.md): give workloads and people access to KV paths.
- [Quickstart on kind](../get-started/quickstart.md): a `RandomSecret` read back by a workload.
