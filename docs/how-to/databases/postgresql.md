---
description: "Configure and test OpenBao dynamic PostgreSQL credentials with tenant-scoped permissions, verified TLS and lease revocation."
---

# Test dynamic PostgreSQL credentials

Use this recipe after [starting the disposable database lab](../../tutorials/dynamic-database-credentials.md#1-start-a-separate-lab).
It selects the pinned PostgreSQL 16 fixture; exact image tags and digests live in the
[provider source](../../../hack/lab/databases/postgresql.py) and [manifest](../../../hack/lab/databases/postgresql.yaml).

## Run

```sh
make lab-database DATABASE_PROFILE=postgresql
```

The runner configures root and `lab-alpha`, then performs real authentication, privilege,
renewal and revocation checks. See the [shared assertions and boundaries](../../tutorials/dynamic-database-credentials.md#4-understand-the-assertions).

## Connection settings

| Setting | Lab value |
|---------|-----------|
| `DatabaseConnection.spec.pluginName` | `postgresql-database-plugin` |
| Typed plugin settings | `spec.postgresql` |
| Database service | `postgresql.obco-db-postgresql.svc` |
| Administrator password | `passwordSecretRef` to `database-postgresql-admin`, key `password`, in `obco-lab` |
| Database mount | `lab-db-postgresql` in each OpenBao namespace |
| Role | `reader`, the only entry in `allowedRoles` |

This plugin is built into the pinned OpenBao server. No `Plugin` registration or `pluginRef`
is required. The administrator Secret is generated outside Git; do not put its value in Helm values.

## Permissions and lease lifecycle

The fixture creates database `app` and separate schemas `lab_root` and `lab_alpha`, each with
one `probe` table. Reader roles receive `USAGE` on their schema and `SELECT` on its table;
they receive no insert grant and no grant on the other schema. Creation uses `VALID UNTIL`,
renewal updates it, and revocation removes privileges and drops the role.

The workload's OpenBao token can issue only this role. The test administrator performs renewal
and revocation; the suite does not grant those capabilities to the workload.

The PostgreSQL lifecycle extension also uses separate, per-run administrators to exercise
`rotateRootOnCreate` and scheduled `DatabaseStaticRole` rotation in root and `lab-alpha`.
Those administrators have `CREATEROLE`, grant options only on their tenant's schema/table,
and administration rights on a dedicated static reader. The shared bootstrap account is
not rotated. The test requires the old administrator password to fail while issuance
through the rotated connection still works. Static rotation keeps the username, changes
the password and timestamp, rejects a fresh login with the old password and accepts the
new password. It starts with a one-hour period for initial login checks, changes the CR to
60 seconds for bounded scheduled-rotation polling, then restores one hour and reads the
current credential before checking old/new logins. This avoids rotating a valid credential
mid-probe. It does not test termination of existing sessions. These additional checks need a passing current-commit CI result.

## TLS and engine-specific limits

The connection uses `sslmode=verify-full` and SCRAM-SHA-256 password authentication. Its
`sslrootcert` points to `/database-ca/postgresql/ca.crt` **inside OpenBao**, not the operator
pod. The installer mounts the disposable CA into the owned OpenBao fixture and waits for
its rollout. Probe Jobs independently verify the CA and assert that their session uses TLS.

## Adapt or clean up

Keep the database administrator capable of the configured creation and revocation statements,
but scope generated users to their actual application data. A successful connection check is
not proof that those statements work: rerun the lifecycle after changing them.
The [database configuration chart](../../../gitops/databases/chart/Chart.yaml) supplies the shared
mount, connection, role and workload-auth resources; provider-specific settings and statements
come from the fixture source linked above.

Follow the [shared cleanup](../../tutorials/dynamic-database-credentials.md#remove-the-lab) when finished.
