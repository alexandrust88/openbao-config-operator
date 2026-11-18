---
title: Issue dynamic OpenBao Cassandra credentials
description: "Connect Cassandra to OpenBao over verified TLS and create a dynamic role that grants SELECT on a keyspace."
---

# Issue dynamic Cassandra credentials

Configure a `DatabaseConnection` and a `DatabaseRole` with OpenBao Config Operator so OpenBao
issues short-lived Cassandra roles. The Cassandra plugin is built into OpenBao.

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
- A Cassandra cluster OpenBao can reach over TLS, with the keyspace the application reads and an
  administrator that can `CREATE ROLE` and `GRANT`.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `cassandra-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cassandra-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f cassandra-root.yaml
```

## 2. Connect the database

Each `DatabaseConnection` holds one plugin and `pluginName` cannot change: use one connection per database
engine. The connection reads the CA that verifies the server from a second Secret. Save this as
`database-connection.yaml`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cassandra-ca
  namespace: openbao-config
type: Opaque
stringData:
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    <ca-certificate-pem>
    -----END CERTIFICATE-----
---
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: cassandra
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: cassandra
  pluginName: cassandra-database-plugin
  allowedRoles:
    - cassandra-reader
  passwordSecretRef:
    name: cassandra-root
    key: password
  cassandra:
    hosts:
      - <cassandra-host>
    port: 9042
    protocolVersion: 4
    username: openbao
    tls: true
    insecureTLS: false
    tlsServerName: <cassandra-host>
    tlsMinVersion: tls12
    connectTimeout: 15s
    pemBundleSecretRef:
      name: cassandra-ca
      key: ca.crt
```

Replace `<cassandra-host>` with a hostname OpenBao can resolve and `<ca-certificate-pem>` with the
PEM body of the CA that signed the server certificate. `pemBundleSecretRef` carries the CA that
verifies the server; add a client certificate and key to the same bundle only if the server
requires client authentication.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/cassandra --for=condition=Ready --timeout=2m
```

## 3. Create a dynamic role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: cassandra-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: cassandra-reader
  dbName: cassandra
  defaultTTL: 1h
  maxTTL: 24h
  creationStatements:
    - CREATE ROLE '{{username}}' WITH PASSWORD = '{{password}}' AND LOGIN = true;
    - GRANT SELECT ON KEYSPACE <keyspace> TO '{{username}}';
  revocationStatements:
    - DROP ROLE '{{username}}';
```

Replace `<keyspace>` with the keyspace the application reads. The Cassandra plugin fills
`{{username}}` and `{{password}}` in the statements.

```bash
kubectl apply -f database-role.yaml
kubectl -n openbao-config wait databaserole/cassandra-reader --for=condition=Ready --timeout=2m
```

Keep the administrator able to run the creation and revocation statements, and scope generated
users to the data the application needs. A `Ready` connection does not prove that the statements
work; the next step logs in to check them.

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait databaseconnection/cassandra databaserole/cassandra-reader \
  --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/cassandra-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to Cassandra with them and
confirm that the grants match the creation statements. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused once the server's credentials, roles and permissions caches expire
(`credentials_validity`, `roles_validity`, `permissions_validity` in `cassandra.yaml`).

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` is not `Ready` | the administrator Secret, a hostname OpenBao can resolve, the CA in `cassandra-ca` and `tlsServerName`, and the administrator's privileges |
| `bao read database/creds/cassandra-reader` fails with `is not an allowed role` | `allowedRoles` includes `cassandra-reader` |
| login works but grants are wrong | the creation statements and the keyspace name |

## Related

- API reference: [DatabaseConnection](../../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../../guides/database-secrets.md)
- [Issue dynamic database credentials](../../tutorials/dynamic-database-credentials.md)
