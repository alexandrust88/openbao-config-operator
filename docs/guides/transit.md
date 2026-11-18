---
title: Manage OpenBao transit encryption keys
description: "Enable the transit engine, declare encryption keys with scheduled and on-demand rotation, and protect destructive deletes."
---

# Manage transit encryption keys

Declare encryption keys of OpenBao's transit secrets engine in Kubernetes with OpenBao Config
Operator, with scheduled rotation and an on-demand rotation from Git.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/mounts"              { capabilities = ["read"] }
    path "sys/mounts/transit"      { capabilities = ["create", "read", "update"] }
    path "sys/mounts/transit/tune" { capabilities = ["create", "read", "update"] }
    path "transit/keys/*"          { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable the engine

Create a `SecretsMount` with `type: transit`. Save this as `transit-mount.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: SecretsMount
metadata:
  name: transit
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: transit
  type: transit
```

```bash
kubectl apply -f transit-mount.yaml
kubectl -n openbao-config wait secretsmount/transit --for=condition=Ready --timeout=2m
```

## 2. Create a key

A `TransitKey` creates a key and keeps its settings. This AES-256-GCM key rotates every 30 days.
Save this as `transit-key.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: TransitKey
metadata:
  name: app-encryption
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  mountPath: transit
  name: app
  type: aes256-gcm96
  autoRotatePeriod: 720h
  minDecryptionVersion: 1
  rotateNonce: "initial"
```

```bash
kubectl apply -f transit-key.yaml
kubectl -n openbao-config wait transitkey/app-encryption --for=condition=Ready --timeout=2m
```

- `autoRotatePeriod` makes OpenBao rotate the key on a schedule (minimum one hour).
- `rotateNonce` rotates the key once each time you change it to a new value, for example after
  an incident. The first value the operator sees does not rotate.
- `minDecryptionVersion` keeps old key versions from decrypting.
- `type`, `derived`, `convergentEncryption` and `keySize` are immutable, and `exportable` and
  `allowPlaintextBackup` cannot be turned off once on: the API server refuses such a change.

The default `deletionPolicy: Orphan` leaves the key in OpenBao when the resource is deleted.
`TransitKey` is a destructive kind ([ADR 0013](../adr/0013-destructive-delete-guard.md)): to
destroy the key when its resource is deleted, set `deletionPolicy: Delete` and the annotation
`obco.dev/allow-destructive-delete: "true"`. With both, the operator sets
`deletionAllowed` immediately before it deletes the key. The deletion is irreversible: data encrypted
with a deleted key cannot be decrypted again.

Every field: [TransitKey](../reference/examples/secrets.md#transitkey).

## 3. Check that it worked

The key is `Ready`:

```bash
kubectl -n openbao-config wait transitkey/app-encryption --for=condition=Ready --timeout=2m
```

OpenBao lists the key's versions and settings:

```bash
bao read transit/keys/app
```

To encrypt a value with this key from a token that may call `transit/encrypt/app`:

```bash
bao write transit/encrypt/app plaintext=$(printf 'hello' | base64)
```

## Troubleshooting

| symptom | check |
|---------|-------|
| `kubectl apply` is refused with `type is immutable` or a similar message | the field is fixed at creation: create a new key under another name instead |
| `rotateNonce` changed but the version did not | a value seen first at creation or adoption does not rotate; change it again |
| encryption fails with a context error | a `derived` key needs a `context` on every request |

## Related

- API reference: [TransitKey](../reference/api/secrets.md#transitkey),
  [SecretsMount](../reference/api/sys.md#secretsmount).
- [Encrypt application data](../tutorials/application-encryption.md): give a workload encrypt and
  decrypt access through Kubernetes authentication.
- [Keep transit keys non-exportable](../security/transit-key-policy.md).
