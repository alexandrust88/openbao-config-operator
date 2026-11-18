---
title: Issue dynamic OpenBao database credentials
description: "Mount the database engine, connect PostgreSQL, create a reader role, and issue and revoke short-lived credentials with the operator."
---

# Issue dynamic database credentials

Enable OpenBao's database engine with OpenBao Config Operator, connect a PostgreSQL database,
declare a reader role, then issue a short-lived username and password and revoke it. Other engines
use the same kinds with different plugin settings: see the
[database recipes](../how-to/databases/postgresql.md) (PostgreSQL first, then the other engines in
the navigation).

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/mounts"               { capabilities = ["read"] }
    path "sys/mounts/database"      { capabilities = ["create", "read", "update"] }
    path "sys/mounts/database/tune" { capabilities = ["create", "read", "update"] }
    path "database/config/*"        { capabilities = ["create", "read", "update", "delete"] }
    path "database/roles/*"         { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- A PostgreSQL server OpenBao can reach over TLS, with the database and schema the application
  reads, an administrator that can `CREATE ROLE` and `GRANT`, and the CA that signed the server
  certificate mounted as a file into the OpenBao pods.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Enable the database engine

Save this as `database-mount.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: SecretsMount
metadata:
  name: database
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  creationPolicy: CreateOrAdopt
  deletionPolicy: Orphan
  path: database
  type: database
```

```bash
kubectl apply -f database-mount.yaml
kubectl -n openbao-config wait secretsmount/database --for=condition=Ready --timeout=2m
```

This is the same resource as in [Configure OpenBao database secrets](../guides/database-secrets.md);
if you created it there, applying it again changes nothing. If another operator resource already
owns `database`, reuse that resource.

## 2. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `db-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: db-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f db-root.yaml
```

## 3. Connect PostgreSQL

Save this as `database-connection.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: app-db
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: app-db
  pluginName: postgresql-database-plugin
  allowedRoles:
    - app-reader
    - app-static
  passwordSecretRef:
    name: db-root
    key: password
  postgresql:
    connectionURL: postgresql://{{username}}:{{password}}@<postgres-host>:5432/<database>?sslmode=verify-full&sslrootcert=<ca-path-on-openbao-pods>
    username: openbao
    passwordAuthentication: scram-sha-256
```

Replace `<postgres-host>` with a hostname OpenBao can resolve, `<database>` with the database the
application reads, and `<ca-path-on-openbao-pods>` with the path of the CA file inside the
OpenBao pods, not the operator pod. The URL keeps the `{{username}}` and `{{password}}`
templates; the password stays in the Secret. This is the same `DatabaseConnection` as in
[Configure OpenBao database secrets](../guides/database-secrets.md), so applying either page
leaves it unchanged; `app-static` is the static role that guide adds.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/app-db --for=condition=Ready --timeout=2m
```

## 4. Create a reader role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: app-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: app-reader
  dbName: app-db
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
kubectl -n openbao-config wait databaserole/app-reader --for=condition=Ready --timeout=2m
```

To deliver the manifests of steps 1, 3 and 4 from Git with Argo CD, keep the database server and
the `db-root` Secret outside the Application: they must exist before it syncs, and the
Application reconciles only the OpenBao configuration. Restrict its AppProject to the kinds it
manages (`SecretsMount`, `DatabaseConnection`, `DatabaseRole`) and no cluster-scoped kinds; this
limits what Argo CD applies, not who can read the Secret. With `deletionPolicy: Orphan` (the
default), pruning a resource removes only the Kubernetes object; the mount, connection and role
stay in OpenBao.

## 5. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait secretsmount/database databaseconnection/app-db \
  databaserole/app-reader --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/app-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to PostgreSQL with them and
confirm that `SELECT` works on the granted schema and that a write fails. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused.

## Troubleshooting

| symptom | check |
|---------|-------|
| `SecretsMount` reports `Conflict` | another operator resource already owns `database`; reuse it |
| `DatabaseConnection` is not `Ready` | the administrator Secret, a hostname OpenBao can resolve, the CA path on the OpenBao pods, and that the administrator can connect |
| `bao read database/creds/app-reader` fails with `is not an allowed role` | `allowedRoles` on the connection includes `app-reader` |
| login works but grants are wrong | the creation statements and the schema name in the database |

## Related

- API reference: [SecretsMount](../reference/api/sys.md#secretsmount),
  [DatabaseConnection](../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../guides/database-secrets.md)
- Database recipes: [PostgreSQL](../how-to/databases/postgresql.md),
  [MySQL](../how-to/databases/mysql.md),
  [MariaDB](../how-to/databases/mariadb.md),
  [Cassandra](../how-to/databases/cassandra.md),
  [InfluxDB](../how-to/databases/influxdb.md),
  [Valkey](../how-to/databases/valkey.md),
  [custom plugin](../how-to/databases/custom.md)
- [Deliver OpenBao configuration with Argo CD](../guides/argocd.md)
