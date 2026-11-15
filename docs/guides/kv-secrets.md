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
`update`) and `<mount>/metadata/*` (`read`, `delete`) for the secrets
([OpenBao policy for the operator](../operate/openbao-policy.md)).

## 1. Enable the engine

The examples on this page write to the KV version 2 mount `secret`, which a `bao server -dev`
server enables by default. To use a mount you enable here instead, set `mountPath` in the
`KVConfig`, `KVSecret` and `RandomSecret` to that mount's path (`quickstart-kv` or `team-a/kv`
below).

A `SecretsMount` with `type: kv` and `options.version: "2"` enables KV version 2. The minimal
form:

```yaml title="examples/quickstart/secretsmount.yaml"
--8<-- "examples/quickstart/secretsmount.yaml"
```

Every mount field, including lease TTLs and audit settings:

??? example "examples/sys/secretsmount.yaml"

    ```yaml
    --8<-- "examples/sys/secretsmount.yaml"
    ```

A `KVConfig` sets the engine's version limits:

```yaml title="examples/secrets/kvconfig.yaml"
--8<-- "examples/secrets/kvconfig.yaml"
```

## 2. Write a secret from Kubernetes Secrets

A `KVSecret` writes the keys of one or more Kubernetes Secrets to one KV path. `dataFrom` copies
every key of a Secret; `data` maps single keys. The values are hashed into `status.writeOnlyHash`,
never stored in status, and a change of a referenced Secret writes a new version:

```yaml title="examples/secrets/kvsecret.yaml"
--8<-- "examples/secrets/kvsecret.yaml"
```

The KV data is removed with the resource only when `deletionPolicy: Delete` is set and the
resource carries `openbaoconfig.io/allow-destructive-delete: "true"`, as in the example.

## 3. Generate a value

A `RandomSecret` generates a value once and stores it under one key of a KV secret. Change
`refreshNonce` to generate a new value. `writeSecretRef` also writes the value to a Kubernetes
Secret that the resource owns; `passwordPolicyName` generates it with an OpenBao password policy
instead of `generator`:

```yaml title="examples/secrets/randomsecret.yaml"
--8<-- "examples/secrets/randomsecret.yaml"
```

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
