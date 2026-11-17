---
description: "Configure and test OpenBao dynamic InfluxDB credentials with tenant-scoped permissions, verified TLS and lease revocation."
---

# Test dynamic InfluxDB credentials

Use this recipe after [starting the disposable database lab](../../tutorials/dynamic-database-credentials.md#1-start-a-separate-lab).
It selects the pinned InfluxDB 1.x fixture; exact image tags and digests live in the
[provider source](../../../hack/lab/databases/influxdb.py).

## Run

```sh
make lab-database DATABASE_PROFILE=influxdb
```

The runner configures root and `lab-alpha`, then performs real authentication, privilege,
renewal and revocation checks. See the [shared assertions and boundaries](../../tutorials/dynamic-database-credentials.md#4-understand-the-assertions).

## Connection settings

| Setting | Lab value |
|---------|-----------|
| `DatabaseConnection.spec.pluginName` | `influxdb-database-plugin` |
| Typed plugin settings | `spec.influxdb` |
| Database service | `influxdb.obco-db-influxdb.svc` |
| Administrator password | `passwordSecretRef` to `database-influxdb-admin`, key `password`, in `obco-lab` |
| Database mount | `lab-db-influxdb` in each OpenBao namespace |
| Role | `reader`, the only entry in `allowedRoles` |

This plugin is built into the pinned OpenBao server. No `Plugin` registration or `pluginRef`
is required. The administrator Secret is generated outside Git; do not put its value in Helm values.

## Permissions and lease lifecycle

The fixture creates `lab_root` and `lab_alpha` databases and a `probe` measurement.
Generated users get `READ` only on their own database; revocation runs `DROP USER`.
The probe requires a real query result, rejects writes and cross-database queries, and
requires HTTP 401 after revocation. It recognises InfluxDB 1.x query permission errors
inside an HTTP 200 response instead of treating every 200 as success.

The workload's OpenBao token can issue only this role. The test administrator performs renewal
and revocation; the suite does not grant those capabilities to the workload. Static-role
rotation, root-password rotation and termination of existing sessions are not tested here.

## TLS and engine-specific limits

HTTP authentication and HTTPS are enabled, and the connection sets `tls: true`.
OpenBao 2.7.2 has a CA-conversion mismatch: the
[InfluxDB connection code](https://github.com/openbao/openbao/blob/v2.7.2/internal/builtin/database/influxdb/connection_producer.go#L87)
reads `IssuingCA` after conversion, while
[`ToCertBundle`](https://github.com/openbao/openbao/blob/v2.7.2/sdk/helper/certutil/types.go#L325)
populates `CAChain`. This loses the custom CA supplied through the PEM settings.

The fixture therefore mounts its public CA under `/database-ca/influxdb` in OpenBao and adds
that directory to `SSL_CERT_DIR`, preserving existing trust directories. Certificate and
hostname verification stay enabled; the probe independently validates the service certificate.
This adds trust to the **OpenBao process**, not just one tenant connection. It is a workaround
for the owned disposable lab, not a production design for tenant-specific trust. The lab does
not claim that `pemJSONSecretRef` custom-CA handling works on this version.

This is the InfluxDB 1.x username/password and InfluxQL model. It does not validate InfluxDB
2.x tokens, organisations or buckets.

## Adapt or clean up

Keep the database administrator capable of the configured creation and revocation statements,
but scope generated users to their actual application data. A successful connection check is
not proof that those statements work: rerun the lifecycle after changing them.
The [database configuration chart](../../../gitops/databases/chart/Chart.yaml) supplies the shared
mount, connection, role and workload-auth resources; provider-specific settings and statements
come from the fixture source linked above.

Follow the [shared cleanup](../../tutorials/dynamic-database-credentials.md#remove-the-lab) when finished.
