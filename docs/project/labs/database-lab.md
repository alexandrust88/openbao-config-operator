---
title: Run the database OpenBao lab
description: "Run the repository database lab: install fixtures, apply operator configuration, and assert login, grants, renewal and revocation."
---

# Run the database lab

This page is for contributors working in a clone of this repository. It runs disposable database
fixtures against a kind lab, applies operator resources, and checks real logins, tenant grants,
lease renewal and revocation.

## What it covers

- `make lab-up` with a dedicated kind context for database work
- `make lab-database DATABASE_PROFILE=<provider>` for PostgreSQL, MySQL, MariaDB, Cassandra,
  InfluxDB and Valkey
- Shared lifecycle assertions for OpenBao root and the `lab-alpha` child namespace
- Cleanup of the kind cluster
- Through `make lab-argocd`, not this target: the PostgreSQL configuration delivered by the Argo CD
  Applications in `gitops/databases/argocd/` (AppProject `database-lab`, which allows only
  operator namespace, mount, policy, Kubernetes auth and database kinds in `obco-gitops`; root and
  `lab-alpha` Applications on the pre-existing `gitops-openbao` connection), followed by the same
  SQL lifecycle. There is no Flux equivalent.

Profiles use built-in OpenBao plugins. External plugins (including MongoDB) are outside this lab;
see [Issue dynamic credentials with a custom database plugin](../../how-to/databases/custom.md).

## Prerequisites

Work at the repository root with Docker running. Install Go, Python 3, kubectl and OpenSSL. The
Make targets obtain the repository's pinned kind, Helm and yq tools. The lab needs enough Docker
memory for OpenBao, the operator and the selected database; Cassandra alone has a 2 GiB container
memory limit. Run engine profiles one at a time on a small machine. No external database account
is required.

## Run

Start a separate lab and keep the shell for the following commands:

```bash
export LAB_CONTEXT=kind-obco-database-tutorial
make lab-up
export KUBECONFIG="$PWD/.tmp/lab/${LAB_CONTEXT#kind-}/kubeconfig"
```

`lab-up` installs OpenBao and the operator and creates the lab connection. The explicit kubeconfig
keeps inspection commands on this lab.

Configure a provider and run the lifecycle (PostgreSQL shown):

```bash
make lab-database DATABASE_PROFILE=postgresql
```

Replace `postgresql` with `mysql`, `mariadb`, `cassandra`, `influxdb` or `valkey`. The runner
installs the fixture under `hack/lab/databases/`, creates bootstrap credentials and a disposable
CA, and applies operator configuration from `gitops/databases/chart/`. Resources include:

| Resource | Responsibility |
|----------|----------------|
| `SecretsMount` | Enable `database` at `lab-db-<provider>` |
| `DatabaseConnection` | Connect using a same-namespace password Secret |
| `DatabaseRole` | Create a `reader` identity per request (5m default, 15m maximum lease) |
| `AuthMount`, `KubernetesAuthConfig`, `KubernetesAuthRole` | Authenticate the `db-root` or `db-alpha` ServiceAccount with audience `openbao` |
| `ACLPolicy` | Permit issuance only from `lab-db-<provider>/creds/reader` and session self-revocation |

Configurations exist in OpenBao's root namespace and `lab-alpha`. Their Kubernetes custom resources
live in `obco-lab`; `spec.openbaoNamespace: lab-alpha` selects the child OpenBao namespace. The root
workload has a restricted policy and does not receive a root token.

You should see a configuration-Ready message followed by one successful lifecycle summary for each
OpenBao namespace. A failed login, permission check or cleanup fails the command.

Inspect without exposing credentials:

```bash
kubectl --context "$LAB_CONTEXT" -n obco-lab get \
  secretsmounts,databaseconnections,databaseroles,kubernetesauthroles \
  -l lab.obco.dev/profile=database
```

Render the chart for inspection (Secret references, not passwords):

```bash
bin/helm template database-demo gitops/databases/chart \
  -f gitops/databases/values/postgresql.yaml
```

## Inspect

For both root and `lab-alpha`, the runner:

1. Exchanges a short-lived ServiceAccount token for a narrowly scoped OpenBao token.
2. Refuses credential issuance in the other OpenBao namespace.
3. Requests two independent credentials and checks distinct users and renewable leases.
4. Reads the tenant's fixture data, rejects a write and rejects the other tenant's data.
5. Renews the first lease and verifies that its credentials still work.
6. Revokes that lease, requires a database authentication rejection on a **new connection**, and
   verifies that the second identity still works.
7. Revokes the remaining lease and workload token and deletes temporary credential Secrets.

Lease renewal and revocation use the lab's administrative identity. The workload policy grants
neither permission. These checks do not prove workload self-service lease administration or
termination of already-open database sessions after revocation.

Provider sources and manifests: `hack/lab/databases/<provider>.py` and, where present,
`hack/lab/databases/<provider>.yaml`. Consumer recipes for each engine:
[docs/how-to/databases/](../../how-to/databases/postgresql.md).

| Failure | Check |
|---------|-------|
| Database Deployment is not ready | Pod events, image availability and Docker memory in `obco-db-<provider>` |
| Connection is not Ready | Administrator Secret, service DNS, trusted CA and database privileges; OpenBao makes the connection |
| Issuance fails | The connection's `allowedRoles`, role statements and the workload's credentials path |
| Probe Job fails | The fixed test stage and Job status; a wrong error category is a failure |
| Cleanup reports an error | A remaining lease may still be active; inspect or revoke it with an administrator |
| A long-lived local lab stops connecting over TLS | Fixture certificates expire after two days; recreate the lab |

Do not dump Secrets or enable shell tracing to diagnose an authentication problem.

## Clean up

```bash
bin/kind delete cluster --name "${LAB_CONTEXT#kind-}"
```

This removes the databases and OpenBao volumes as well as their Kubernetes resources. The runner's
`finally` block normally removes issued credentials and probe Jobs, but it keeps configuration and
database fixtures for inspection. Database CRs use `deletionPolicy: Orphan`; deleting their
manifests alone is not a full database teardown.

## Coverage boundary

| Behaviour | This lab |
|-----------|----------|
| Issuance, real login, scoped read and denied writes/cross-tenant access | Executed for root and one child namespace |
| Distinct identities, renewal, revoked new-login denial | Executed for root and one child namespace |
| Static-role scheduled rotation and root-password rotation | PostgreSQL-only lifecycle extension; other profiles do not exercise these |
| Natural lease expiry and existing-session termination | Not exercised |
| Database HA, backups and production administration | Outside this disposable lab |

CI scope for these profiles: [verification](../verification.md#dynamic-database-profiles).
