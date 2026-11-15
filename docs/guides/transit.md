# Transit

This guide declares encryption keys of OpenBao's transit secrets engine in Kubernetes, with
scheduled rotation and an on-demand rotation from Git.

## Before you begin

A `Ready` connection whose OpenBao policy allows `sys/mounts/<mount>` to enable the engine, and
`<mount>/keys/*` for the keys ([OpenBao policy for the operator](../operate/openbao-policy.md)).

## 1. Enable the engine

Create a `SecretsMount` with `type: transit`, as in this fragment of a spec:

```yaml
spec:
  connectionRef:
    name: openbao
  path: transit
  type: transit
```

## 2. Create the keys

A `TransitKey` creates a key and keeps its settings. This file has an AES-256-GCM encryption key
that rotates every 30 days and an HMAC key:

```yaml title="examples/secrets/transitkey.yaml"
--8<-- "examples/secrets/transitkey.yaml"
```

- `autoRotatePeriod` makes OpenBao rotate the key on a schedule.
- `rotateNonce` rotates the key once each time you change it to a new value, for example after
  an incident. The first value the operator sees does not rotate.
- `minDecryptionVersion` keeps old key versions from decrypting.
- `type`, `derived`, `convergentEncryption` and `keySize` are immutable, and `exportable` and
  `allowPlaintextBackup` cannot be turned off once on: the API server refuses such a change.

Both keys keep the default `deletionPolicy: Orphan`, so deleting a resource leaves its key in OpenBao.
`TransitKey` is a destructive kind ([ADR 0013](../adr/0013-destructive-delete-guard.md)): to destroy the key
when its resource is deleted, set `deletionPolicy: Delete` and the annotation
`openbaoconfig.io/allow-destructive-delete: "true"`. With both, the operator sets `deletionAllowed` just before
it deletes the key. The deletion is irreversible: data encrypted with a deleted key cannot be decrypted again.

## 3. Check that it worked

```bash
kubectl -n openbao-config get transitkeys
bao read transit/keys/obco-app
```

Both keys show `Ready` `True`, and OpenBao lists the key's versions and settings.

## Troubleshooting

| symptom | check |
|---------|-------|
| `kubectl apply` is refused with `type is immutable` or a similar message | the field is fixed at creation: create a new key under another name instead |
| `rotateNonce` changed but the version did not | a value seen first at creation or adoption does not rotate; change it again |
| encryption fails with a context error | a `derived` key needs a `context` on every request |

## Related

- API reference: [TransitKey](../reference/api/secrets.md#transitkey),
  [SecretsMount](../reference/api/sys.md#secretsmount).
