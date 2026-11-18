---
title: Issue dynamic OpenBao MySQL credentials
description: "Connect MySQL to OpenBao over verified TLS and create a dynamic reader role with SELECT grants and SSL-required users."
---

# Issue dynamic MySQL credentials

Configure a `DatabaseConnection` and a `DatabaseRole` with OpenBao Config Operator so OpenBao
issues short-lived MySQL users. The MySQL plugin is built into OpenBao.

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
- A MySQL server OpenBao can reach over TLS, with the database the application reads and an
  administrator that can `CREATE USER` and `GRANT`.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `mysql-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: mysql-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f mysql-root.yaml
```

## 2. Connect the database

Each `DatabaseConnection` holds one plugin and `pluginName` cannot change: use one connection per database
engine. Save this as `database-connection.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: mysql
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: mysql
  pluginName: mysql-database-plugin
  allowedRoles:
    - mysql-reader
  passwordSecretRef:
    name: mysql-root
    key: password
  mysql:
    connectionURL: '{{username}}:{{password}}@tcp(<mysql-host>:3306)/'
    username: openbao
    tlsCA: |
      -----BEGIN CERTIFICATE-----
      <ca-certificate-pem>
      -----END CERTIFICATE-----
    tlsServerName: <mysql-host>
    tlsSkipVerify: false
```

Replace `<mysql-host>` with a hostname OpenBao can resolve and `<ca-certificate-pem>` with the PEM
body of the CA that signed the server certificate. `tlsServerName` and `tlsSkipVerify: false`
make OpenBao check the server certificate and its hostname.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/mysql --for=condition=Ready --timeout=2m
```

## 3. Create a dynamic role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: mysql-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: mysql-reader
  dbName: mysql
  defaultTTL: 1h
  maxTTL: 24h
  creationStatements:
    - CREATE USER '{{name}}'@'%' IDENTIFIED BY '{{password}}' REQUIRE SSL;
    - GRANT SELECT ON <database>.* TO '{{name}}'@'%';
  revocationStatements:
    - DROP USER '{{name}}'@'%';
```

Replace `<database>` with the database the application reads. `REQUIRE SSL` refuses logins of
generated users that do not use TLS.

```bash
kubectl apply -f database-role.yaml
kubectl -n openbao-config wait databaserole/mysql-reader --for=condition=Ready --timeout=2m
```

Keep the administrator able to run the creation and revocation statements, and scope generated
users to the data the application needs. A `Ready` connection does not prove that the statements
work; the next step logs in to check them.

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait databaseconnection/mysql databaserole/mysql-reader \
  --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/mysql-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to MySQL with them over TLS
and confirm that the grants match the creation statements. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused.

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` is not `Ready` | the administrator Secret, a hostname OpenBao can resolve, `tlsCA` and `tlsServerName`, and the administrator's privileges |
| `bao read database/creds/mysql-reader` fails with `is not an allowed role` | `allowedRoles` includes `mysql-reader` |
| login works but grants are wrong | the creation statements and the object names in the database |

## Related

- API reference: [DatabaseConnection](../../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../../guides/database-secrets.md)
- [Issue dynamic database credentials](../../tutorials/dynamic-database-credentials.md)
