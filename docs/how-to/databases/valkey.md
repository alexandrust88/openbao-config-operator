---
title: Issue dynamic OpenBao Valkey credentials
description: "Connect Valkey to OpenBao over verified TLS and create a dynamic ACL user limited to GET on a key pattern."
---

# Issue dynamic Valkey credentials

Configure a `DatabaseConnection` and a `DatabaseRole` with OpenBao Config Operator so OpenBao
issues short-lived Valkey ACL users. The Valkey plugin is built into OpenBao.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../../get-started/install.md),
  [Connect to OpenBao](../../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../../operate/openbao-policy.md)):

    ```hcl
    path "database/config/*" { capabilities = ["create", "read", "update", "delete"] }
    path "database/roles/*"  { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- A `SecretsMount` with `type: database` at path `database` is `Ready`
  ([Configure OpenBao database secrets](../../guides/database-secrets.md#1-enable-the-engine)).
- A Valkey server OpenBao can reach over TLS, with an administrator user that can create and delete
  ACL users.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `valkey-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: valkey-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f valkey-root.yaml
```

## 2. Connect the database

Each `DatabaseConnection` holds one plugin and `pluginName` cannot change: use one connection per database
engine. Save this as `database-connection.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: valkey
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: valkey
  pluginName: valkey-database-plugin
  allowedRoles:
    - valkey-reader
  passwordSecretRef:
    name: valkey-root
    key: password
  valkey:
    host: <valkey-host>
    port: 6379
    username: default
    tls: true
    caCert: |
      -----BEGIN CERTIFICATE-----
      <ca-certificate-pem>
      -----END CERTIFICATE-----
```

Replace `<valkey-host>` with a hostname OpenBao can resolve and `<ca-certificate-pem>` with the PEM
body of the CA that signed the server certificate; `tls: true` requires `caCert`. `default` is
Valkey's built-in administrator user. The `valkey` section also accepts
`pluginName: redis-database-plugin`; use a server that matches the plugin you set.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/valkey --for=condition=Ready --timeout=2m
```

## 3. Create a dynamic role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: valkey-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: valkey-reader
  dbName: valkey
  defaultTTL: 1h
  maxTTL: 24h
  creationStatements:
    - '["~<key-prefix>:*", "+get"]'
```

Replace `<key-prefix>` with the key prefix the application reads. Creation statements are ACL
rules as a JSON list. The plugin removes the user when the lease ends, so the role needs no
revocation statements.

```bash
kubectl apply -f database-role.yaml
kubectl -n openbao-config wait databaserole/valkey-reader --for=condition=Ready --timeout=2m
```

Keep the administrator able to create and delete ACL users, and scope generated users to the keys
the application needs. A `Ready` connection does not prove that the ACL rules work; the next step
logs in to check them.

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait databaseconnection/valkey databaserole/valkey-reader \
  --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/valkey-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to Valkey with them over TLS
and confirm that the ACL rules match the creation statements. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused.

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` is not `Ready` | the administrator Secret, a hostname OpenBao can resolve, `caCert`, and the administrator's ACL permissions |
| `bao read database/creds/valkey-reader` fails with `is not an allowed role` | `allowedRoles` includes `valkey-reader` |
| login works but commands are refused or allowed wrongly | the ACL rules in the creation statements |

## Related

- API reference: [DatabaseConnection](../../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../../guides/database-secrets.md)
- [Issue dynamic database credentials](../../tutorials/dynamic-database-credentials.md)
