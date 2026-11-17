---
description: "Issue short-lived database credentials with OpenBao Config Operator and test real logins, tenant permissions, renewal and revocation in kind."
---

# Issue and revoke dynamic database credentials

Use the operator to configure OpenBao's database engine, then authenticate a workload and
use its temporary credentials against a real database. This tutorial starts with PostgreSQL.
The same runner supports the [engine recipes](#choose-an-engine) below.

The database is a secrets-engine target. OpenBao continues to store its own data in its
three-node TLS Raft cluster. The database fixtures are disposable single-instance services,
not production database installation examples.

## Before you begin

Work at the root of this repository with Docker running. Install Go, Python 3, kubectl and
OpenSSL. The Make targets obtain the repository's pinned kind, Helm and yq tools. The lab
needs enough Docker memory for OpenBao, the operator and the selected database; Cassandra
alone has a 2 GiB container memory limit. Run engine profiles one at a time on a small machine.
No external database account or hosted-provider secret is needed.

## 1. Start a separate lab

```sh
export LAB_CONTEXT=kind-obco-database-tutorial
make lab-up
export KUBECONFIG="$PWD/.tmp/lab/${LAB_CONTEXT#kind-}/kubeconfig"
```

`lab-up` installs OpenBao and the operator and creates the lab connection. Keep this shell
for the following commands. The explicit kubeconfig keeps inspection commands on this lab.

## 2. Configure PostgreSQL and run the lifecycle

```sh
make lab-database DATABASE_PROFILE=postgresql
```

The runner installs the database fixture, creates random bootstrap credentials and a disposable
CA, and applies the operator configuration from the
[database configuration chart](../../gitops/databases/chart/Chart.yaml). Its resources are:

| Resource | Responsibility |
|----------|----------------|
| `SecretsMount` | Enable `database` at `lab-db-postgresql` |
| `DatabaseConnection` | Connect to PostgreSQL using a same-namespace password Secret reference |
| `DatabaseRole` | Create a reader identity per request, with a 5-minute default and 15-minute maximum lease |
| `AuthMount`, `KubernetesAuthConfig`, `KubernetesAuthRole` | Authenticate the `db-root` or `db-alpha` ServiceAccount with audience `openbao` |
| `Policy` | Permit issuance only from `lab-db-postgresql/creds/reader` and session self-revocation |

There are separate configurations in OpenBao's root namespace and `lab-alpha`. Their Kubernetes
custom resources live in `obco-lab`; `spec.namespace: lab-alpha` selects the child OpenBao
namespace. The root workload has a restricted policy: it does **not** receive a root token.

You should see a configuration-Ready message followed by one successful lifecycle summary
for each namespace. A failed login, permission check or cleanup makes the command fail.

## 3. Inspect the configuration without exposing credentials

```sh
kubectl --context "$LAB_CONTEXT" -n obco-lab get \
  secretsmounts,databaseconnections,databaseroles,kubernetesauthroles \
  -l lab.openbaoconfig.io/profile=database
```

The Ready connection and role prove configuration convergence. The preceding runner goes
further: it opens actual database sessions with the generated identities.

To inspect the PostgreSQL root-namespace configuration before applying it elsewhere:

```sh
bin/helm template database-demo gitops/databases/chart \
  -f gitops/databases/values/postgresql.yaml
```

The rendered YAML contains Secret references, not passwords. The sample values assume the
lab's existing connection, database endpoint, credentials and OpenBao CA mount; rendering
them does not install those prerequisites. Keep statements in the values file specific to
your database privileges rather than granting blanket administrator rights to generated users.

## 4. Understand the assertions

For both root and `lab-alpha`, the runner:

1. Exchanges a short-lived ServiceAccount token for a narrowly scoped OpenBao token.
2. Refuses credential issuance in the other OpenBao namespace.
3. Requests two independent credentials and checks distinct users and renewable leases.
4. Reads the tenant's fixture data, rejects a write and rejects the other tenant's data.
5. Renews the first lease and verifies that its credentials still work.
6. Revokes that lease, requires a database authentication rejection on a **new connection**,
   and verifies that the second identity still works.
7. Revokes the remaining lease and workload token and deletes temporary credential Secrets.

Lease renewal and revocation use the lab's administrative identity. The workload policy grants
neither permission. These tests do not prove workload self-service lease administration or
termination of already-open database sessions after revocation.

Bootstrap database accounts have the privileges needed to create and drop users; generated
reader accounts have narrower grants. Temporary credentials are passed to probe Jobs through
Kubernetes Secrets and can be read by principals with appropriate Kubernetes access. Probe logs
contain only a fixed success marker, not database passwords or query output.

## Choose an engine

Run the matching command after step 1. Each recipe explains its privilege model and TLS settings.

| Recipe | Command | Connection plugin |
|--------|---------|-------------------|
| [PostgreSQL](../how-to/databases/postgresql.md) | `make lab-database DATABASE_PROFILE=postgresql` | `postgresql-database-plugin` |
| [MySQL](../how-to/databases/mysql.md) | `make lab-database DATABASE_PROFILE=mysql` | `mysql-database-plugin` |
| [MariaDB](../how-to/databases/mariadb.md) | `make lab-database DATABASE_PROFILE=mariadb` | `mysql-database-plugin` |
| [Cassandra](../how-to/databases/cassandra.md) | `make lab-database DATABASE_PROFILE=cassandra` | `cassandra-database-plugin` |
| [InfluxDB](../how-to/databases/influxdb.md) | `make lab-database DATABASE_PROFILE=influxdb` | `influxdb-database-plugin` |
| [Valkey](../how-to/databases/valkey.md) | `make lab-database DATABASE_PROFILE=valkey` | `valkey-database-plugin` |

All six profiles use plugins built into the pinned OpenBao server. Set `DatabaseConnection.spec.pluginName`;
there is no `pluginRef` field to set and no `Plugin` resource to register for those built-ins.
For other engines, review the [custom-plugin prerequisites](../how-to/databases/custom.md).
No external database binary is downloaded by this lab.
The Redis plugin alias is not a Redis-server test. Elasticsearch is not a built-in profile
and has no live coverage here.

## Deliver the configuration with Argo CD

The separate public-Git regression runs the PostgreSQL configuration through real Argo CD
Applications rather than applying the rendered database CRs directly:

```sh
make lab-argocd
```

It creates its own disposable kind cluster. The
[AppProject](../../gitops/databases/argocd/project.yaml) permits the operator's database and
workload-auth resources only in `obco-gitops`. The
[root Application](../../gitops/databases/argocd/postgresql-root.yaml) and
[child Application](../../gitops/databases/argocd/postgresql-alpha.yaml) use the shared chart
with separate PostgreSQL values files. Argo CD reports ownership of those resources and must
be Synced and Healthy at the selected public Git commit before the SQL lifecycle runs.

OpenBao and the operator are delivered through the existing Helm-based app hierarchy.
The PostgreSQL server, bootstrap password and public CA are prepared as test infrastructure;
the Applications reconcile the OpenBao configuration, not the database server. Their values
reference the pre-existing `gitops-openbao` connection and database password Secret. AppProject
restrictions limit what the Applications may manage; they are not Kubernetes Secret-read RBAC.

The checked-in Applications default to `main`. For an unmerged commit already pushed to the
public repository, select that revision as described in the
[Argo CD deployment guide](../how-to/argocd-lab.md). The workflow uses its tested commit.
There is no equivalent database-specific Flux regression yet, and pruning the CRs does not
remove the database: their `deletionPolicy` is `Orphan`.

## Troubleshooting

| Failure | Check |
|---------|-------|
| Database Deployment is not ready | Pod events, image availability and Docker memory in `obco-db-<provider>` |
| Connection is not Ready | Database administrator Secret, service DNS, trusted CA and database privileges; OpenBao makes the connection |
| Issuance fails | The connection's `allowedRoles`, role statements and the workload's exact credentials path |
| Probe Job fails | The fixed test stage and Job status; a wrong error category is a failure, not accepted as a denial |
| Cleanup reports an error | The remaining lease may still be active; inspect/revoke it with an administrator before reusing the fixture |
| A long-lived local lab stops connecting over TLS | Database fixture certificates expire after two days; recreate the disposable lab |

Do not dump Secrets or enable shell tracing to diagnose an authentication problem.

## Remove the lab

```sh
bin/kind delete cluster --name "${LAB_CONTEXT#kind-}"
```

This removes the databases and OpenBao volumes as well as their Kubernetes resources. The
runner's `finally` block normally removes issued credentials and probe Jobs, but it deliberately
keeps configuration and database fixtures for inspection. Database CRs use `deletionPolicy: Orphan`;
deleting their manifests alone is not a full database teardown.

## Coverage boundary

These profiles are wired into the `integration lab` workflow. Their addition is not evidence
that every engine has passed on the current commit: check each `db-*` job. Offline schema and
harness tests do not substitute for a live run.

| Behaviour | This dynamic-credential suite |
|-----------|-------------------------------|
| Issuance, real login, scoped read and denied writes/cross-tenant access | Executed for root and one child namespace |
| Distinct identities, renewal, revoked new-login denial | Executed for root and one child namespace |
| Static-role scheduled rotation and root-password rotation | PostgreSQL-only lifecycle extension; other profiles do not exercise these |
| Natural lease expiry and existing-session termination | Not exercised |
| Database HA, backups and production administration | Outside this disposable lab |

Continue with the [database configuration guide](../guides/database-secrets.md) for static roles
and the [verification guide](../project/verification.md#dynamic-database-profiles) for CI scope.
