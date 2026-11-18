---
title: OpenBao configuration guides
description: Task guides for managing OpenBao policies, Kubernetes authentication, secrets engines, tenant namespaces and existing configuration with the operator.
---

# Guides

Each guide reaches one goal with the OpenBao Config Operator: the manifest to apply, the command
to run and how to check the result. They assume an installed operator and a `Ready` connection
([Install](../get-started/install.md), [Connect to OpenBao](../get-started/connect.md)).

The manifests use the Kubernetes namespace `openbao-config` and a connection named `openbao`;
change both to yours. Passwords, keys and certificates in them are fake placeholders. The check
steps read OpenBao with the `bao` CLI, so set `BAO_ADDR` and a token that can read the paths.

To learn the operator step by step on a test cluster first, follow the [tutorials](../tutorials/index.md).

## Let workloads and people log in

| goal | guide |
|------|-------|
| Let a workload log in with its Kubernetes ServiceAccount token | [Kubernetes auth](kubernetes-auth.md) |
| Let people log in through an identity provider, and CI jobs with a signed JWT | [JWT and OIDC auth](jwt-oidc-auth.md) |
| Give a machine outside Kubernetes, such as a CI runner or a VM, a login | [AppRole](approle.md) |

## Control who can access what

Pick how you write access rules:

- `ACLPolicy`: you write the ACL text yourself, for any path and capability OpenBao supports.
- A templated `ACLPolicy`: one policy whose paths OpenBao fills in from the identity of each token,
  so every workload gets its own path.
- `AccessGrant` and `AccessLayout`: you name subjects, KV version 2 paths and an access level,
  and the operator writes the policies, plus Kubernetes auth roles for a grant or identity groups
  for a layout.

| goal | guide |
|------|-------|
| Keep an ACL policy in Git and check what OpenBao holds | [Manage ACL policies](policies.md) |
| Give every workload its own path with one policy | [Templated policies](templated-policies.md) |
| Grant KV access to ServiceAccounts and to identity provider groups without writing policies | [Access model](access-model.md) |
| Declare entities, groups and aliases, or use OpenBao as an OIDC provider | [Identity](identity.md) |
| Give a tenant its own OpenBao namespace and its own connection | [Namespaces and tenants](namespaces-and-tenants.md) |

Before tenants get their own connections, read [the tenant boundary](../security.md#the-tenant-boundary).

## Store and issue secrets

| goal | guide |
|------|-------|
| Create a KV version 2 mount and fill it from Kubernetes Secrets or generated values | [KV secrets](kv-secrets.md) |
| Issue certificates from a root and an intermediate CA | [PKI](pki.md) |
| Declare encryption keys and rotate them on a schedule | [Transit](transit.md) |
| Issue dynamic and static database credentials | [Database secrets](database-secrets.md) |

The database recipes configure dynamic credentials for one database each:
[PostgreSQL](../how-to/databases/postgresql.md), [MySQL](../how-to/databases/mysql.md),
[MariaDB](../how-to/databases/mariadb.md), [Cassandra](../how-to/databases/cassandra.md),
[InfluxDB](../how-to/databases/influxdb.md) and [Valkey](../how-to/databases/valkey.md). For a
database whose plugin is not built into OpenBao, start with
[external database plugins](../how-to/databases/custom.md).

## Use OpenBao from an application

| goal | guide |
|------|-------|
| Generate a credential in OpenBao and deliver it to a workload with External Secrets Operator | [Generate credentials and deliver with ESO](../tutorials/generated-credentials.md) |
| Encrypt application data with a transit key | [Encrypt application data](../tutorials/application-encryption.md) |
| Give an application short-lived database credentials, from the connection to the revoked lease | [Issue dynamic database credentials](../tutorials/dynamic-database-credentials.md) |

## Start with a new or an existing OpenBao

| goal | guide |
|------|-------|
| Create the operator's login at OpenBao's first start, without a root token | [OpenBao self-init](self-init.md) |
| Generate resources from a running OpenBao and review them before the operator writes anything | [Import existing configuration](import.md) |
| Hand existing objects over from another tool or from manual changes, one at a time | [Migrate to the operator](migration.md) |

## Deliver configuration from Git

| goal | guide |
|------|-------|
| Install Argo CD and deploy OpenBao, the operator and its configuration from Git, with health checks | [Argo CD](argocd.md) |
| Install Flux and deploy OpenBao, the operator and its configuration from Git, with readiness gates | [Flux](flux.md) |
