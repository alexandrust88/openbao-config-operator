---
title: Issue dynamic OpenBao PostgreSQL credentials
description: "Connect PostgreSQL to OpenBao over verified TLS and create a dynamic reader role with creation, renew and revocation statements."
---

# Issue dynamic PostgreSQL credentials

Configure a `DatabaseConnection` and a `DatabaseRole` with OpenBao Config Operator so OpenBao
issues short-lived PostgreSQL users. The PostgreSQL plugin is built into OpenBao.

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
- A PostgreSQL server OpenBao can reach over TLS, with the database and schema the application
  reads, an administrator that can `CREATE ROLE` and `GRANT`, and the CA that signed the server
  certificate mounted as a file into the OpenBao pods.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `postgresql-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: postgresql-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f postgresql-root.yaml
```

## 2. Connect the database

Each `DatabaseConnection` holds one plugin and `pluginName` cannot change: use one connection per database
engine. Save this as `database-connection.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: postgresql
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: postgresql
  pluginName: postgresql-database-plugin
  allowedRoles:
    - postgresql-reader
  passwordSecretRef:
    name: postgresql-root
    key: password
  postgresql:
    connectionURL: postgresql://{{username}}:{{password}}@<postgres-host>:5432/<database>?sslmode=verify-full&sslrootcert=<ca-path-on-openbao-pods>
    username: openbao
    passwordAuthentication: scram-sha-256
```

Replace `<postgres-host>` with a hostname OpenBao can resolve, `<database>` with the database the
application reads, and `<ca-path-on-openbao-pods>` with the path of the CA file inside the
OpenBao pods, not the operator pod. `sslmode=verify-full` checks the server certificate and its
hostname. The URL keeps the `{{username}}` and `{{password}}` templates; the password stays in
the Secret.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/postgresql --for=condition=Ready --timeout=2m
```

## 3. Create a dynamic role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: postgresql-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: postgresql-reader
  dbName: postgresql
  defaultTTL: 1h
  maxTTL: 24h
  creationStatements:
    - CREATE ROLE "{{name}}" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';
    - GRANT USAGE ON SCHEMA <schema> TO "{{name}}";
    - GRANT SELECT ON ALL TABLES IN SCHEMA <schema> TO "{{name}}";
  renewStatements:
    - ALTER ROLE "{{name}}" VALID UNTIL '{{expiration}}';
  revocationStatements:
    - REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA <schema> FROM "{{name}}";
    - REVOKE USAGE ON SCHEMA <schema> FROM "{{name}}";
    - DROP ROLE IF EXISTS "{{name}}";
```

Replace `<schema>` with the schema the application reads.

```bash
kubectl apply -f database-role.yaml
kubectl -n openbao-config wait databaserole/postgresql-reader --for=condition=Ready --timeout=2m
```

Keep the administrator able to run the creation and revocation statements, and scope generated
users to the data the application needs. A `Ready` connection does not prove that the statements
work; the next step logs in to check them.

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait databaseconnection/postgresql databaserole/postgresql-reader \
  --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/postgresql-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to PostgreSQL with them and
confirm that the grants match the creation statements. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused.

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` is not `Ready` | the administrator Secret, a hostname OpenBao can resolve, the CA path on the OpenBao pods, and the administrator's privileges |
| `bao read database/creds/postgresql-reader` fails with `is not an allowed role` | `allowedRoles` includes `postgresql-reader` |
| login works but grants are wrong | the creation statements and the object names in the database |

## Related

- API reference: [DatabaseConnection](../../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../../guides/database-secrets.md)
- [Issue dynamic database credentials](../../tutorials/dynamic-database-credentials.md)
