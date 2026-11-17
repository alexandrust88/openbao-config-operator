---
description: "Configure OpenBao database connections and credential roles declaratively using Kubernetes custom resources."
---

# Database secrets

This guide connects OpenBao's database secrets engine to a database and declares roles that issue
credentials: a dynamic role that creates a user per request, and a static role that rotates the
password of an existing user.

For a runnable local environment with real login and revocation checks, start with
[Issue and revoke dynamic database credentials](../tutorials/dynamic-database-credentials.md).
This guide describes configuration for a database you already operate.

## Before you begin

- A database that OpenBao can reach, and a user with the rights to create users (dynamic roles) or
  change passwords (static roles).
- A `Ready` connection whose OpenBao policy allows `sys/mounts/<mount>` to enable the engine, and
  `<mount>/config/*`, `<mount>/roles/*`, `<mount>/static-roles/*` and `<mount>/rotate-root/*`
  ([OpenBao policy for the operator](../operate/openbao-policy.md)).

## 1. Enable the engine

Create a `SecretsMount` with `type: database` at the path the roles use, as in this fragment of a
spec:

```yaml
spec:
  connectionRef:
    name: openbao
  path: database
  type: database
```

[examples/sys/secretsmount.yaml](../../examples/sys/secretsmount.yaml) shows a complete
`SecretsMount`.

## 2. Connect the database

A `DatabaseConnection` configures one database. The root password comes from a Secret; OpenBao
never returns it, so the operator records a hash and writes it again when the Secret changes.
`allowedRoles` lists the roles that may use the connection. The file holds one connection per
plugin section (PostgreSQL, MySQL, Cassandra, InfluxDB, Valkey, and Redis through the `valkey`
section) and a Snowflake connection set through `pluginConfig`; the PostgreSQL one comes first:

??? example "examples/secrets/databaseconnection.yaml"

    ```yaml
    --8<-- "examples/secrets/databaseconnection.yaml"
    ```

With `rotateRootOnCreate: true`, OpenBao rotates the root password once, right after it creates the
connection, so only OpenBao knows it. The password in the Secret is then stale and is sent again
only if the connection is created again. `verifyConnection` (default `true`) makes OpenBao connect
to the database on every write, so a wrong URL or password fails the write.

### Built-in and registered plugins

For the pinned OpenBao server, PostgreSQL, MySQL (also used by the MariaDB lab), Cassandra,
InfluxDB and Valkey use built-in plugins. Set `DatabaseConnection.spec.pluginName` to the
appropriate name and use its typed settings. No `Plugin` resource or `pluginRef` is needed.
The `valkey` settings also accept the Redis plugin alias, but the live profile tests Valkey,
not a Redis server.

A plugin that is not built in must be installed on OpenBao and registered separately. A
`Plugin` CR records the catalog name, executable and checksum; it does not download or
install a binary. `DatabaseConnection.pluginName` selects that registered name and
`pluginConfig` carries plugin-specific settings. See the
[custom-plugin prerequisites](../how-to/databases/custom.md). Elasticsearch is not a built-in
profile and has no live verification in this suite.

## 3. Create roles

A `DatabaseRole` creates a user per credential request and drops it when the lease ends. The file
also has a role that issues client certificates:

```yaml title="examples/secrets/databaserole.yaml"
--8<-- "examples/secrets/databaserole.yaml"
```

A `DatabaseStaticRole` manages the password of a user that already exists and rotates it every
`rotationPeriod`:

```yaml title="examples/secrets/databasestaticrole.yaml"
--8<-- "examples/secrets/databasestaticrole.yaml"
```

The role names must be in the connection's `allowedRoles`.

## 4. Check that it worked

```bash
kubectl -n openbao-config get databaseconnections,databaseroles,databasestaticroles
bao read database/creds/obco-app-ro
bao read database/static-creds/obco-app-static
```

Every resource shows `Ready` `True`, and OpenBao returns a username and password for each role.

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` reports `ReconcileError` with a connection error | OpenBao connects to the database, not the operator: OpenBao's network path, the URL, the root user and password |
| `DatabaseConnection` reports `ReferenceNotReady` | the password Secret and key exist in the resource's namespace |
| `bao read database/creds/<role>` fails with `is not an allowed role` | add the role to `allowedRoles` |
| the Secret's root password no longer works | expected after `rotateRootOnCreate` or a `rotate-root`: only OpenBao knows the password |

## Related

- API reference: [DatabaseConnection](../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../reference/api/secrets.md#databaserole),
  [DatabaseStaticRole](../reference/api/secrets.md#databasestaticrole),
  [SecretsMount](../reference/api/sys.md#secretsmount).
- [ADR 0017](../adr/0017-write-once-fields-for-rotated-root-credentials.md): write-once fields for
  rotated root credentials.
