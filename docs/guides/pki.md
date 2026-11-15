# PKI

This guide builds a two-tier certificate authority in OpenBao from Kubernetes resources: a root
CA, an intermediate CA signed by it, and a role that issues certificates from the intermediate.

## Before you begin

A `Ready` connection whose OpenBao policy allows `sys/mounts/<mount>` for both mounts, and
`<mount>/*` paths for issuers, keys, configuration and roles
([OpenBao policy for the operator](../operate/openbao-policy.md)).

## 1. Enable two PKI mounts

Create one `SecretsMount` with `type: pki` for the root (`obco-pki-root`) and one for the
intermediate (`obco-pki-int`). Raise `maxLeaseTTL` to at least the lifetime of the certificates
each mount signs, as in this fragment of a spec:

```yaml
spec:
  connectionRef:
    name: openbao
  path: obco-pki-root
  type: pki
  maxLeaseTTL: 87600h
```

## 2. Create the issuers

A `PKIIssuer` with `type: root` generates a self-signed CA. One with `type: intermediate` and a
`signer` is signed by another issuer, so the chain is built without copying a CSR by hand. The
file holds a root, an intermediate signed by it through `signer.issuerRef`, and a second
intermediate that uses an existing key:

??? example "examples/secrets/pkiissuer.yaml"

    ```yaml
    --8<-- "examples/secrets/pkiissuer.yaml"
    ```

`key.source` chooses where the key comes from: `internal` keeps it in OpenBao, `exported` also
writes it to the Secret in `writeSecretRef`, and `existing` uses a key that a `PKIKey` created:

??? example "examples/secrets/pkikey.yaml"

    ```yaml
    --8<-- "examples/secrets/pkikey.yaml"
    ```

Issuers default to `deletionPolicy: Orphan`, so deleting the resource never deletes a CA.

## 3. Configure the mounts

A `PKIConfig` sets the URLs written into certificates, the CRL and OCSP settings and the default
issuer of a mount:

??? example "examples/secrets/pkiconfig.yaml"

    ```yaml
    --8<-- "examples/secrets/pkiconfig.yaml"
    ```

`PKIAutoTidy` and `PKIACMEConfig` configure tidying and ACME on a mount
([examples/secrets/pkiautotidy.yaml](../../examples/secrets/pkiautotidy.yaml),
[examples/secrets/pkiacmeconfig.yaml](../../examples/secrets/pkiacmeconfig.yaml)).

## 4. Create a role

A `PKIRole` says which certificates a mount may issue. `issuer.issuerRef` names the `PKIIssuer`
resource:

??? example "examples/secrets/pkirole.yaml"

    ```yaml
    --8<-- "examples/secrets/pkirole.yaml"
    ```

## 5. Check that it worked

```bash
kubectl -n openbao-config get pkiissuers,pkikeys,pkiconfigs,pkiroles
bao write obco-pki-int/issue/obco-web common_name=app.openbao-config.example.com ttl=1h
```

Every resource shows `Ready` `True`, and OpenBao returns a certificate signed by the
intermediate, with the root in its chain.

## Troubleshooting

| symptom | check |
|---------|-------|
| the intermediate reports `ReferenceNotReady` | the signing issuer in `signer` exists and is `Ready` |
| issuing fails with a TTL error | the mount's `maxLeaseTTL` and the role's `maxTTL` against the requested TTL; `leafNotAfterBehavior` of the issuer |
| issuing fails with `common name not allowed` | `allowedDomains`, `allowSubdomains` and `allowBareDomains` on the role |

## Related

- API reference: [PKIIssuer](../reference/api/secrets.md#pkiissuer),
  [PKIKey](../reference/api/secrets.md#pkikey),
  [PKIConfig](../reference/api/secrets.md#pkiconfig),
  [PKIRole](../reference/api/secrets.md#pkirole),
  [PKIAutoTidy](../reference/api/secrets.md#pkiautotidy),
  [PKIACMEConfig](../reference/api/secrets.md#pkiacmeconfig).
