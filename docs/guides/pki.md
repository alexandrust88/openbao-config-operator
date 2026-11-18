---
title: Configure OpenBao PKI from Kubernetes
description: "Build a root and intermediate CA in OpenBao, configure URLs, and create a role that issues certificates."
---

# Configure OpenBao PKI

Build a two-tier certificate authority in OpenBao from Kubernetes resources with OpenBao Config
Operator: a root CA, an intermediate CA signed by it, and a role that issues certificates from the
intermediate.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/mounts"                      { capabilities = ["read"] }
    path "sys/mounts/pki-root"             { capabilities = ["create", "read", "update"] }
    path "sys/mounts/pki-root/tune"        { capabilities = ["create", "read", "update"] }
    path "sys/mounts/pki-int"              { capabilities = ["create", "read", "update"] }
    path "sys/mounts/pki-int/tune"         { capabilities = ["create", "read", "update"] }
    path "pki-root/root/generate/*"        { capabilities = ["create", "update"] }
    path "pki-root/issuer/*"               { capabilities = ["read", "update"] }
    path "pki-int/intermediate/generate/*" { capabilities = ["create", "update"] }
    path "pki-int/intermediate/set-signed" { capabilities = ["create", "update"] }
    path "pki-int/issuer/*"                { capabilities = ["read", "update"] }
    path "pki-int/config/*"                { capabilities = ["read", "update"] }
    path "pki-int/roles/*"                 { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

    Signing the intermediate writes `pki-root/issuer/<root issuer ID>/sign-intermediate`, with the
    generated ID of the root issuer: `pki-root/issuer/*` covers it, and a narrower policy grants
    `pki-root/issuer/+/sign-intermediate`. Add `delete` on `<mount>/issuer/*` and `<mount>/key/*`
    only for issuers with `deletionPolicy: Delete`.

- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable two PKI mounts

Create one `SecretsMount` with `type: pki` for the root and one for the intermediate. Raise
`maxLeaseTTL` to at least the lifetime of the certificates each mount signs. Save this as
`pki-mounts.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: SecretsMount
metadata:
  name: pki-root
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: pki-root
  type: pki
  maxLeaseTTL: 87600h
---
apiVersion: sys.obco.dev/v1alpha1
kind: SecretsMount
metadata:
  name: pki-int
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: pki-int
  type: pki
  maxLeaseTTL: 43800h
```

```bash
kubectl apply -f pki-mounts.yaml
kubectl -n openbao-config wait secretsmount/pki-root secretsmount/pki-int \
  --for=condition=Ready --timeout=2m
```

## 2. Create the issuers

A `PKIIssuer` with `type: root` generates a self-signed CA. One with `type: intermediate` and a
`signer` is signed by another issuer, so the chain is built without copying a CSR by hand. Save
this as `pki-issuers.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: PKIIssuer
metadata:
  name: root-2026
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  mountPath: pki-root
  name: root-2026
  type: root
  key:
    source: internal
    name: root-2026-key
    type: ec
    bits: 384
  certificate:
    commonName: Example Root CA 2026
    organization:
      - Example
    ttl: 87600h
    maxPathLength: 1
---
apiVersion: secrets.obco.dev/v1alpha1
kind: PKIIssuer
metadata:
  name: int-2026
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  mountPath: pki-int
  name: int-2026
  type: intermediate
  key:
    source: internal
    name: int-2026-key
    type: ec
    bits: 256
  certificate:
    commonName: Example Intermediate CA 2026
    organization:
      - Example
    ttl: 43800h
    maxPathLength: 0
  signer:
    issuerRef:
      name: root-2026
```

```bash
kubectl apply -f pki-issuers.yaml
kubectl -n openbao-config wait pkiissuer/root-2026 pkiissuer/int-2026 \
  --for=condition=Ready --timeout=5m
```

`key.source` chooses where the key comes from: `internal` (the default) keeps it in OpenBao,
`exported` also writes it once to the Secret in `writeSecretRef`, `existing` reuses a key already
stored in the mount, and `kms` uses an OpenBao external key. With the default
`deletionPolicy: Orphan`, deleting the resource keeps the CA. `Delete` removes it only while the
resource carries `obco.dev/allow-destructive-delete: "true"`. Every field:
[PKIIssuer](../reference/examples/secrets.md#pkiissuer),
[PKIKey](../reference/examples/secrets.md#pkikey).

## 3. Configure the intermediate mount

A `PKIConfig` sets the URLs written into certificates, the CRL and OCSP settings and the default
issuer of a mount. Save this as `pki-config.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: PKIConfig
metadata:
  name: pki-int
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: pki-int
  urls:
    issuingCertificates:
      - https://<openbao-addr>/v1/pki-int/ca
    crlDistributionPoints:
      - https://<openbao-addr>/v1/pki-int/crl
  issuers:
    default:
      issuerRef:
        name: int-2026
```

Replace `<openbao-addr>` with the address clients use to reach OpenBao.

```bash
kubectl apply -f pki-config.yaml
kubectl -n openbao-config wait pkiconfig/pki-int --for=condition=Ready --timeout=2m
```

`PKIAutoTidy` and `PKIACMEConfig` configure tidying and ACME on a mount (every field:
[PKIAutoTidy](../reference/examples/secrets.md#pkiautotidy),
[PKIACMEConfig](../reference/examples/secrets.md#pkiacmeconfig)).

## 4. Create a role

A `PKIRole` says which certificates a mount may issue. `issuer.issuerRef` names the `PKIIssuer`
resource. Save this as `pki-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: PKIRole
metadata:
  name: web
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: pki-int
  name: web
  issuer:
    issuerRef:
      name: int-2026
  ttl: 24h
  maxTTL: 720h
  allowedDomains:
    - example.com
  allowSubdomains: true
  allowBareDomains: false
  serverFlag: true
  clientFlag: false
  keyType: ec
  keyBits: 256
```

```bash
kubectl apply -f pki-role.yaml
kubectl -n openbao-config wait pkirole/web --for=condition=Ready --timeout=2m
```

Replace `example.com` with the domain you issue for. Every field:
[PKIRole](../reference/examples/secrets.md#pkirole).

## 5. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait pkiissuer/root-2026 pkiissuer/int-2026 pkiconfig/pki-int \
  pkirole/web --for=condition=Ready --timeout=2m
```

OpenBao issues a certificate from the role:

```bash
bao write pki-int/issue/web common_name=app.example.com ttl=1h
```

OpenBao returns a certificate signed by the intermediate, with the root in its chain.

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
- [SecretsMount](../reference/api/sys.md#secretsmount).
