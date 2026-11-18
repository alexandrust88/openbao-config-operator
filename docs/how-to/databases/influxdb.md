---
title: Issue dynamic OpenBao InfluxDB credentials
description: "Connect InfluxDB 1.x to OpenBao over verified TLS and create a dynamic user with READ on one database."
---

# Issue dynamic InfluxDB credentials

Configure a `DatabaseConnection` and a `DatabaseRole` with OpenBao Config Operator so OpenBao
issues short-lived InfluxDB 1.x users. The InfluxDB plugin is built into OpenBao. This page does
not cover InfluxDB 2.x tokens, organisations or buckets.

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
- An InfluxDB 1.x server OpenBao can reach over TLS (username and password authentication,
  InfluxQL), with the database the application reads, an administrator that can `CREATE USER`
  and `GRANT`, and a server certificate the OpenBao process trusts (see the warning in step 2).
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step and revoke
  leases (`sys/leases/revoke`, `update`).

## 1. Store the administrator password

OpenBao never returns the administrator password, so it comes from a Secret in the same
Kubernetes namespace as the `DatabaseConnection`. Save this as `influxdb-root.yaml`; the password value
is fake, replace it before you apply:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: influxdb-root
  namespace: openbao-config
type: Opaque
stringData:
  password: fake-root-password-not-real
```

```bash
kubectl apply -f influxdb-root.yaml
```

## 2. Connect the database

Each `DatabaseConnection` holds one plugin and `pluginName` cannot change: use one connection per database
engine. Save this as `database-connection.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseConnection
metadata:
  name: influxdb
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: influxdb
  pluginName: influxdb-database-plugin
  allowedRoles:
    - influxdb-reader
  passwordSecretRef:
    name: influxdb-root
    key: password
  influxdb:
    host: <influxdb-host>
    port: 8086
    username: openbao
    tls: true
    insecureTLS: false
```

Replace `<influxdb-host>` with a hostname OpenBao can resolve.

```bash
kubectl apply -f database-connection.yaml
kubectl -n openbao-config wait databaseconnection/influxdb --for=condition=Ready --timeout=2m
```

!!! warning "The CA is trusted by the whole OpenBao process"
    On OpenBao 2.7.2 the InfluxDB plugin ignores a custom CA supplied through
    `pemBundleSecretRef` or `pemJSONSecretRef`. Use a CA the OpenBao process already trusts, for
    example through `SSL_CERT_DIR`, and keep `insecureTLS: false`. This trusts the CA for every
    connection on that OpenBao server, in every OpenBao namespace.

## 3. Create a dynamic role

Save this as `database-role.yaml`:

```yaml
apiVersion: secrets.obco.dev/v1alpha1
kind: DatabaseRole
metadata:
  name: influxdb-reader
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: database
  name: influxdb-reader
  dbName: influxdb
  defaultTTL: 1h
  maxTTL: 24h
  creationStatements:
    - CREATE USER "{{username}}" WITH PASSWORD '{{password}}';
    - GRANT READ ON "<database>" TO "{{username}}";
  revocationStatements:
    - DROP USER "{{username}}";
```

Replace `<database>` with the database the application reads. The InfluxDB plugin fills
`{{username}}` and `{{password}}` in the statements.

```bash
kubectl apply -f database-role.yaml
kubectl -n openbao-config wait databaserole/influxdb-reader --for=condition=Ready --timeout=2m
```

Keep the administrator able to run the creation and revocation statements, and scope generated
users to the data the application needs. A `Ready` connection does not prove that the statements
work; the next step logs in to check them.

## 4. Check that it worked

The resources are `Ready`:

```bash
kubectl -n openbao-config wait databaseconnection/influxdb databaserole/influxdb-reader \
  --for=condition=Ready --timeout=2m
```

OpenBao issues credentials:

```bash
bao read database/creds/influxdb-reader
```

OpenBao returns a `username`, a `password` and a `lease_id`. Log in to InfluxDB with them over
TLS and confirm that the grants match the creation statements, which also confirms that
certificate verification works. Then revoke the lease:

```bash
bao lease revoke <lease_id>
```

`<lease_id>` is the `lease_id` from the previous output. A new login with the same password is
refused.

## Troubleshooting

| symptom | check |
|---------|-------|
| `DatabaseConnection` is not `Ready` | the administrator Secret, a hostname OpenBao can resolve, that the OpenBao process trusts the server's CA, and the administrator's privileges |
| `bao read database/creds/influxdb-reader` fails with `is not an allowed role` | `allowedRoles` includes `influxdb-reader` |
| login works but grants are wrong | the creation statements and the database name |

## Related

- API reference: [DatabaseConnection](../../reference/api/secrets.md#databaseconnection),
  [DatabaseRole](../../reference/api/secrets.md#databaserole)
- [Configure OpenBao database secrets](../../guides/database-secrets.md)
- [Issue dynamic database credentials](../../tutorials/dynamic-database-credentials.md)
