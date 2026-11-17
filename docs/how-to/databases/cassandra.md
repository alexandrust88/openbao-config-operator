---
description: "Configure and test OpenBao dynamic Cassandra credentials with tenant-scoped permissions, verified TLS and lease revocation."
---

# Test dynamic Cassandra credentials

Use this recipe after [starting the disposable database lab](../../tutorials/dynamic-database-credentials.md#1-start-a-separate-lab).
It selects the pinned Cassandra 5 fixture; exact image tags and digests live in the
[provider source](../../../hack/lab/databases/cassandra.py).

## Run

```sh
make lab-database DATABASE_PROFILE=cassandra
```

The runner configures root and `lab-alpha`, then performs real authentication, privilege,
renewal and revocation checks. See the [shared assertions and boundaries](../../tutorials/dynamic-database-credentials.md#4-understand-the-assertions).

## Connection settings

| Setting | Lab value |
|---------|-----------|
| `DatabaseConnection.spec.pluginName` | `cassandra-database-plugin` |
| Typed plugin settings | `spec.cassandra` |
| Database service | `cassandra.obco-db-cassandra.svc` |
| Administrator password | `passwordSecretRef` to `database-cassandra-admin`, key `password`, in `obco-lab` |
| Database mount | `lab-db-cassandra` in each OpenBao namespace |
| Role | `reader`, the only entry in `allowedRoles` |

This plugin is built into the pinned OpenBao server. No `Plugin` registration or `pluginRef`
is required. The administrator Secret is generated outside Git; do not put its value in Helm values.

## Permissions and lease lifecycle

The fixture enables `PasswordAuthenticator` and `CassandraAuthorizer`, creates a random
`obco` administrator and disables login for Cassandra's initial bootstrap account. Separate
`lab_root` and `lab_alpha` keyspaces hold the fixture tables. Generated roles have `LOGIN`
and `SELECT` on their keyspace; `DROP ROLE` revokes them. Probes require Cassandra's
`Unauthorized` response for writes and access to the other keyspace.

The workload's OpenBao token can issue only this role. The test administrator performs renewal
and revocation; the suite does not grant those capabilities to the workload. Static-role
rotation, root-password rotation and termination of existing sessions are not tested here.

## TLS and engine-specific limits

Client encryption is mandatory. The server uses a generated PKCS#12 keystore; the
connection sets `tls: true`, the service `tlsServerName`, and `pemBundleSecretRef` for the
CA. The CQL probe uses certificate validation. Role, permission and credential caches are
set to zero for this fixture so revocation checks do not wait for cached authentication;
that is a test choice, not a production tuning recommendation.

## Adapt or clean up

Keep the database administrator capable of the configured creation and revocation statements,
but scope generated users to their actual application data. A successful connection check is
not proof that those statements work: rerun the lifecycle after changing them.
The [database configuration chart](../../../gitops/databases/chart/Chart.yaml) supplies the shared
mount, connection, role and workload-auth resources; provider-specific settings and statements
come from the fixture source linked above.

Follow the [shared cleanup](../../tutorials/dynamic-database-credentials.md#remove-the-lab) when finished.
