---
description: "Configure and test OpenBao dynamic MariaDB credentials with tenant-scoped permissions, verified TLS and lease revocation."
---

# Test dynamic MariaDB credentials

Use this recipe after [starting the disposable database lab](../../tutorials/dynamic-database-credentials.md#1-start-a-separate-lab).
It selects the pinned MariaDB 11.8 fixture; exact image tags and digests live in the
[provider source](../../../hack/lab/databases/mariadb.py) and [manifest](../../../hack/lab/databases/mariadb.yaml).

## Run

```sh
make lab-database DATABASE_PROFILE=mariadb
```

The runner configures root and `lab-alpha`, then performs real authentication, privilege,
renewal and revocation checks. See the [shared assertions and boundaries](../../tutorials/dynamic-database-credentials.md#4-understand-the-assertions).

## Connection settings

| Setting | Lab value |
|---------|-----------|
| `DatabaseConnection.spec.pluginName` | `mysql-database-plugin` |
| Typed plugin settings | `spec.mysql` |
| Database service | `mariadb.obco-db-mariadb.svc` |
| Administrator password | `passwordSecretRef` to `database-mariadb-admin`, key `password`, in `obco-lab` |
| Database mount | `lab-db-mariadb` in each OpenBao namespace |
| Role | `reader`, the only entry in `allowedRoles` |

This plugin is built into the pinned OpenBao server. No `Plugin` registration or `pluginRef`
is required. The administrator Secret is generated outside Git; do not put its value in Helm values.

## Permissions and lease lifecycle

MariaDB uses the MySQL database plugin and the CRD's `mysql` settings, not a separate
`mariadb` section. Separate `lab_root` and `lab_alpha` databases contain the fixture tables.
Generated users receive `SELECT` only on their own table and are removed with `DROP USER`.
The actual server and probe client are MariaDB, so this profile is distinct from the MySQL test.

The workload's OpenBao token can issue only this role. The test administrator performs renewal
and revocation; the suite does not grant those capabilities to the workload. Static-role
rotation, root-password rotation and termination of existing sessions are not tested here.

## TLS and engine-specific limits

The connection sets `tlsCA`, `tlsServerName` and `tlsSkipVerify: false`. The MariaDB CLI
uses `--ssl --ssl-verify-server-cert --ssl-ca=/ca/ca.crt`; the probe also checks the negotiated
cipher. The bootstrap `root` account remains a disposable administrator, not a workload credential.

## Adapt or clean up

Keep the database administrator capable of the configured creation and revocation statements,
but scope generated users to their actual application data. A successful connection check is
not proof that those statements work: rerun the lifecycle after changing them.
The [database configuration chart](../../../gitops/databases/chart/Chart.yaml) supplies the shared
mount, connection, role and workload-auth resources; provider-specific settings and statements
come from the fixture source linked above.

Follow the [shared cleanup](../../tutorials/dynamic-database-credentials.md#remove-the-lab) when finished.
