---
description: "Configure and test OpenBao dynamic Valkey credentials with tenant-scoped permissions, verified TLS and lease revocation."
---

# Test dynamic Valkey credentials

Use this recipe after [starting the disposable database lab](../../tutorials/dynamic-database-credentials.md#1-start-a-separate-lab).
It selects the pinned Valkey 8 fixture; exact image tags and digests live in the
[provider source](../../../hack/lab/databases/valkey.py).

## Run

```sh
make lab-database DATABASE_PROFILE=valkey
```

The runner configures root and `lab-alpha`, then performs real authentication, privilege,
renewal and revocation checks. See the [shared assertions and boundaries](../../tutorials/dynamic-database-credentials.md#4-understand-the-assertions).

## Connection settings

| Setting | Lab value |
|---------|-----------|
| `DatabaseConnection.spec.pluginName` | `valkey-database-plugin` |
| Typed plugin settings | `spec.valkey` |
| Database service | `valkey.obco-db-valkey.svc` |
| Administrator password | `passwordSecretRef` to `database-valkey-admin`, key `password`, in `obco-lab` |
| Database mount | `lab-db-valkey` in each OpenBao namespace |
| Role | `reader`, the only entry in `allowedRoles` |

This plugin is built into the pinned OpenBao server. No `Plugin` registration or `pluginRef`
is required. The administrator Secret is generated outside Git; do not put its value in Helm values.

## Permissions and lease lifecycle

Each issued ACL user receives only `+get` and a key pattern matching its tenant:
`~lab:root:*` or `~lab:alpha:*`. The runner reads the seeded `probe` key, requires `NOPERM`
for `SET` and the other tenant's key, and requires `WRONGPASS` for a fresh login after
lease revocation. No custom revocation statement is supplied; revocation exercises the
plugin's user-removal behaviour.

The workload's OpenBao token can issue only this role. The test administrator performs renewal
and revocation; the suite does not grant those capabilities to the workload. Static-role
rotation, root-password rotation and termination of existing sessions are not tested here.

## TLS and engine-specific limits

The server disables the plaintext port and listens on its TLS port. The connection uses
`tls: true` and `caCert`; the RESP probe verifies the certificate and service hostname.
A random password protects the bootstrap `default` administrator. The API also accepts
`redis-database-plugin` through the `valkey` section, but this profile tests a Valkey server
with `valkey-database-plugin`, not a Redis server or every Redis-compatible service.

## Adapt or clean up

Keep the database administrator capable of the configured creation and revocation statements,
but scope generated users to their actual application data. A successful connection check is
not proof that those statements work: rerun the lifecycle after changing them.
The [database configuration chart](../../../gitops/databases/chart/Chart.yaml) supplies the shared
mount, connection, role and workload-auth resources; provider-specific settings and statements
come from the fixture source linked above.

Follow the [shared cleanup](../../tutorials/dynamic-database-credentials.md#remove-the-lab) when finished.
