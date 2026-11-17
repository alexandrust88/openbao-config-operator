---
description: "Configure and test OpenBao dynamic MySQL credentials with tenant-scoped permissions, verified TLS and lease revocation."
---

# Test dynamic MySQL credentials

Use this recipe after [starting the disposable database lab](../../tutorials/dynamic-database-credentials.md#1-start-a-separate-lab).
It selects the pinned MySQL 8.4 fixture; exact image tags and digests live in the
[provider source](../../../hack/lab/databases/mysql.py) and [manifest](../../../hack/lab/databases/mysql.yaml).

## Run

```sh
make lab-database DATABASE_PROFILE=mysql
```

The runner configures root and `lab-alpha`, then performs real authentication, privilege,
renewal and revocation checks. See the [shared assertions and boundaries](../../tutorials/dynamic-database-credentials.md#4-understand-the-assertions).

## Connection settings

| Setting | Lab value |
|---------|-----------|
| `DatabaseConnection.spec.pluginName` | `mysql-database-plugin` |
| Typed plugin settings | `spec.mysql` |
| Database service | `mysql.obco-db-mysql.svc` |
| Administrator password | `passwordSecretRef` to `database-mysql-admin`, key `password`, in `obco-lab` |
| Database mount | `lab-db-mysql` in each OpenBao namespace |
| Role | `reader`, the only entry in `allowedRoles` |

This plugin is built into the pinned OpenBao server. No `Plugin` registration or `pluginRef`
is required. The administrator Secret is generated outside Git; do not put its value in Helm values.

## Permissions and lease lifecycle

The fixture creates databases `lab_root` and `lab_alpha`, each with a `probe` table.
The role creates a fresh user with `REQUIRE SSL` and grants only `SELECT` on the matching
table. `DROP USER` implements revocation. The test requires MySQL error 1142 for forbidden
operations and 1045 for a new login after revocation.

The workload's OpenBao token can issue only this role. The test administrator performs renewal
and revocation; the suite does not grant those capabilities to the workload. Static-role
rotation, root-password rotation and termination of existing sessions are not tested here.

## TLS and engine-specific limits

The `mysql` connection sets `tlsCA`, `tlsServerName` and `tlsSkipVerify: false`.
Probe clients use `--ssl-mode=VERIFY_IDENTITY` and verify that the session has an SSL cipher.
The fixture administrator is the database `root` user; it is not the identity issued to workloads.

## Adapt or clean up

Keep the database administrator capable of the configured creation and revocation statements,
but scope generated users to their actual application data. A successful connection check is
not proof that those statements work: rerun the lifecycle after changing them.
The [database configuration chart](../../../gitops/databases/chart/Chart.yaml) supplies the shared
mount, connection, role and workload-auth resources; provider-specific settings and statements
come from the fixture source linked above.

Follow the [shared cleanup](../../tutorials/dynamic-database-credentials.md#remove-the-lab) when finished.
