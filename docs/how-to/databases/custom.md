---
title: Issue dynamic OpenBao credentials with a custom plugin
description: "Register an external database plugin with a Plugin resource, connect it through pluginConfig, and create a dynamic role."
---

# Issue dynamic credentials with a custom database plugin

Register an external database plugin with OpenBao Config Operator, point a `DatabaseConnection` at
it and create a `DatabaseRole`, so OpenBao issues short-lived users for a database that has no
built-in plugin. For PostgreSQL, MySQL, MariaDB, Cassandra, InfluxDB and Valkey, use the
[database recipes](postgresql.md) instead (PostgreSQL first, then the other engines in the
navigation).

The operator does not download or install plugin binaries. A checksum establishes which file
OpenBao runs; it does not establish that the file is safe. The
[OpenBao MongoDB plugin](https://github.com/openbao/openbao-plugins/tree/main/database/mongodb)
is one external implementation. A Vault plugin or a third-party example does not establish
OpenBao compatibility by itself.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../../get-started/install.md),
  [Connect to OpenBao](../../get-started/connect.md)). Plugin registration works only in the
  OpenBao root namespace, so the connection must not set `spec.openbaoNamespace`.
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../../operate/openbao-policy.md)):

    ```hcl
    path "sys/plugins/catalog"   { capabilities = ["read"] }
    path "sys/plugins/catalog/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "database/config/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "database/roles/*"      { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- A `SecretsMount` with `type: database` at path `database` is `Ready`
  ([Configure OpenBao database secrets](../../guides/database-secrets.md#1-enable-the-engine)).
- A maintained plugin release for your OpenBao version whose publisher, licence and vulnerability
  scan (including its Go runtime and dependencies) you have reviewed, installed in the plugin
  directory of every OpenBao node, with its SHA-256.
- A database OpenBao can reach, with an administrator that can create and drop users.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `custom-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: custom-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f custom-root.yaml
```

## 2. Connect the database

Each `DatabaseConnection` holds one plugin and `pluginName` cannot change: use one connection per database
engine. Register the plugin in OpenBao's catalogue first. Save this as `database-plugin.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: Plugin
metadata:
  name: custom-plugin
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  creationPolicy: CreateOrAdopt
  deletionPolicy: Orphan
  type: database
  name: my-db-plugin
  command: openbao-plugin-database-example
  sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

Replace `my-db-plugin` with the catalogue name you choose, `openbao-plugin-database-example` with
the file name of the binary in the plugin directory, and the fake checksum with the SHA-256 of
that file.

```bash
kubectl apply -f database-plugin.yaml
kubectl -n openbao-config wait plugin/custom-plugin --for=condition=Ready --timeout=2m
```

`DatabaseConnection` has no reference to the `Plugin` resource: its `pluginName` must equal the
catalogue `name`. Plugin-specific settings go in `pluginConfig`, under the OpenBao API parameter
names the plugin documents. Save this as `database-connection.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: custom
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: custom
  pluginName: my-db-plugin
  allowedRoles:
    - custom-reader
  passwordSecretRef:
    name: custom-root
    key: password
  pluginConfig:
    host: <db-host>
    username: openbao
```

Replace `<db-host>` with a hostname OpenBao can resolve, and the keys with the ones your plugin
documents. The API server rejects `password`, `private_key` and the top-level connection fields
(such as `name`, `plugin_name`, `allowed_roles`) in `pluginConfig`, and a typed section such as
`postgresql` with an external `pluginName`. The password reaches the plugin only from
`passwordSecretRef`, through the `{{password}}` template where the plugin uses one.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/custom --for=condition=Ready --timeout=2m
```

## 3. Create a dynamic role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: custom-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: custom-reader
  dbName: custom
  defaultTTL: 1h
  maxTTL: 24h
  creationStatements:
    - CREATE USER '{{name}}'@'%' IDENTIFIED BY '{{password}}';
  revocationStatements:
    - DROP USER IF EXISTS '{{name}}'@'%';
```

The statements use MySQL syntax as an example; write the statements your plugin and database
accept.

```bash
kubectl apply -f database-role.yaml
kubectl -n openbao-config wait databaserole/custom-reader --for=condition=Ready --timeout=2m
```

Keep the administrator able to run the creation and revocation statements, and scope generated
users to the data the application needs. A `Ready` connection does not prove that the statements
work; the next step logs in to check them.

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait plugin/custom-plugin databaseconnection/custom \
  databaserole/custom-reader --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/custom-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to the database with them and
confirm TLS, the grants and that writes outside the grants are refused. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused.

## Troubleshooting

| symptom | check |
|---------|-------|
| `Plugin` is not `Ready` | the binary is present on every OpenBao node, `command` and `sha256` match the file, catalogue `type` is `database` |
| `DatabaseConnection` reports an unknown plugin | `pluginName` equals the registered catalogue `name`, and the `Plugin` is `Ready` |
| `kubectl apply` of the connection is refused | `pluginConfig` holds no password, private key or top-level connection field, and no typed section is set |

## Related

- API reference: [Plugin](../../reference/api/sys.md#plugin),
  [DatabaseConnection](../../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../../guides/database-secrets.md)
- [Issue dynamic database credentials](../../tutorials/dynamic-database-credentials.md)
