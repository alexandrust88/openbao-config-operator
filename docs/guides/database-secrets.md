---
title: Configure OpenBao database secrets
description: "Enable the database engine, connect a database, and declare dynamic and static credential roles with the operator."
---

# Configure OpenBao database secrets

Connect OpenBao's database secrets engine to a database you operate with OpenBao Config Operator,
then declare roles that issue short-lived users or rotate an existing user's password.

Per-engine connection settings and statements:
[PostgreSQL](../how-to/databases/postgresql.md),
[MySQL](../how-to/databases/mysql.md),
[MariaDB](../how-to/databases/mariadb.md),
[Cassandra](../how-to/databases/cassandra.md),
[InfluxDB](../how-to/databases/influxdb.md),
[Valkey](../how-to/databases/valkey.md),
[custom plugin](../how-to/databases/custom.md).
An end-to-end walk-through of one dynamic role:
[Issue dynamic database credentials](../tutorials/dynamic-database-credentials.md).

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
    path "database/static-roles/*"  { capabilities = ["create", "read", "update", "delete"] }
    # only when a DatabaseConnection sets rotateRootOnCreate
    path "database/rotate-root/*"   { capabilities = ["update"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- A PostgreSQL server OpenBao can reach over TLS, the CA that signed its certificate mounted as a
  file into the OpenBao pods, and an administrator user that can create users (dynamic roles) and
  change passwords (static roles). Other engines: the per-engine pages above.
- An existing database user for each static role (`app_user` below).
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable the engine

Every resource on this page uses the database mount `database`, connection `openbao` and
Kubernetes namespace `openbao-config`. Save this as `database-mount.yaml`:

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

If another operator resource already owns `database`, reuse that resource. For a different mount,
change `path` here and every `mountPath` below together.
Every mount field: [every field](../reference/examples/sys.md#secretsmount).

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

## 3. Connect the database

A `DatabaseConnection` configures one plugin and lists the roles allowed to use it. This example
uses PostgreSQL; each per-engine page above shows the typed section of its engine. Save this as
`database-connection.yaml`:

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

Replace `<postgres-host>` with a hostname OpenBao can resolve, `<database>` with the database
name, and `<ca-path-on-openbao-pods>` with the path of the CA file inside the OpenBao pods, not
the operator pod.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/app-db --for=condition=Ready --timeout=2m
```

- `rotateRootOnCreate: true` makes OpenBao rotate the administrator password once after
  creation, so only OpenBao knows it and the Secret holds a stale value
  ([ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md)).
- `verifyConnection` (default `true`) makes OpenBao connect to the database on every write.
- Removing a setting from `spec` does not reset it in OpenBao; recreate the connection to clear it.

Every connection field: [every field](../reference/examples/secrets.md#databaseconnection).

## 4. Create a dynamic role

A `DatabaseRole` creates a user per request and drops it when the lease ends. The role name must
appear in the connection's `allowedRoles`. Save this as `database-role.yaml`:

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

Scope the statements to the schemas and tables the application needs. A `Ready` connection does
not prove that the statements work; issue credentials and log in to check them.

Every role field: [every field](../reference/examples/secrets.md#databaserole).

## 5. Create a static role

A `DatabaseStaticRole` manages the password of a user that already exists and rotates it every
`rotationPeriod`. Create the database user `app_user` before you apply this role: OpenBao rotates
its password as soon as the role is created. Save this as `database-static-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseStaticRole
metadata:
  name: app-static
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: app-static
  dbName: app-db
  username: app_user
  rotationPeriod: 24h
  rotationStatements:
    - ALTER USER "{{name}}" WITH PASSWORD '{{password}}';
```

```bash
kubectl apply -f database-static-role.yaml
kubectl -n openbao-config wait databasestaticrole/app-static --for=condition=Ready --timeout=2m
```

Every static-role field: [every field](../reference/examples/secrets.md#databasestaticrole).

## 6. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait databaseconnection/app-db databaserole/app-reader \
  databasestaticrole/app-static --for=condition=Ready --timeout=2m
```

OpenBao issues credentials for both roles:

```bash
bao read database/creds/app-reader
bao read database/static-creds/app-static
```

OpenBao returns a username and password for each role.

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` reports `ReconcileError` with a connection error | OpenBao connects to the database, not the operator: OpenBao's network path, the URL, the root user and password |
| `DatabaseConnection` reports `ReferenceNotReady` | the password Secret and key exist in the resource's Kubernetes namespace |
| `bao read database/creds/<role>` fails with `is not an allowed role` | add the role to `allowedRoles` |
| `DatabaseStaticRole` is not `Ready` | the user in `username` exists in the database and the administrator can change its password |
| the Secret's root password no longer works | expected after `rotateRootOnCreate` or a `rotate-root`: only OpenBao knows the password |

## Related

- API reference: [DatabaseConnection](../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../reference/api/secrets.md#databaserole),
  [DatabaseStaticRole](../reference/api/secrets.md#databasestaticrole),
  [SecretsMount](../reference/api/sys.md#secretsmount)
- [Issue dynamic database credentials](../tutorials/dynamic-database-credentials.md)
- [ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md): write-once fields for
  rotated root credentials
