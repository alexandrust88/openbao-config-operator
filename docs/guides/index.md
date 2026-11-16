# Guides

Each guide reaches one goal with the operator. They assume an installed operator and a `Ready`
connection ([Install](../get-started/install.md), [Connect to OpenBao](../get-started/connect.md)).

The manifests come from `examples/`, where CI checks every file against the CRDs. They use the
Kubernetes namespace `openbao-config` and a connection named `openbao`; change both to yours. Values
such as passwords, keys and certificates in them are fake placeholders. The check steps read
OpenBao with the `bao` CLI, so set `BAO_ADDR` and a token that may read the paths.

To learn the operator step by step on a test cluster first, follow the [tutorials](../tutorials/index.md).

## Authentication

<div class="grid cards" markdown>

-   :material-kubernetes: **[Kubernetes auth](kubernetes-auth.md)**

    ---

    Let workloads log in with their ServiceAccount token.

-   :material-badge-account: **[JWT and OIDC auth](jwt-oidc-auth.md)**

    ---

    Browser logins through an identity provider, and CI jobs with their job JWT.

-   :material-robot: **[AppRole](approle.md)**

    ---

    Machine logins outside Kubernetes, with secret IDs the operator rotates.

</div>

## Secrets engines

<div class="grid cards" markdown>

-   :material-key-variant: **[KV secrets](kv-secrets.md)**

    ---

    KV version 2 mounts, secrets from Kubernetes Secrets, and generated values.

-   :material-database: **[Database secrets](database-secrets.md)**

    ---

    Dynamic and static database credentials.

-   :material-certificate: **[PKI](pki.md)**

    ---

    A root and an intermediate CA, and a role that issues certificates.

-   :material-lock: **[Transit](transit.md)**

    ---

    Encryption keys with scheduled rotation.

</div>

## Identity and access

<div class="grid cards" markdown>

-   :material-account-group: **[Identity](identity.md)**

    ---

    Entities, groups, aliases, and OpenBao as an OIDC provider.

-   :material-shield-account: **[Access model](access-model.md)**

    ---

    Grant KV access to ServiceAccounts and to identity provider groups without writing policies.

-   :material-code-braces: **[Templated policies](templated-policies.md)**

    ---

    One policy that gives every workload its own path.

-   :material-domain: **[Namespaces and tenants](namespaces-and-tenants.md)**

    ---

    One OpenBao namespace and one connection per team.

</div>

## Adopt and deliver

<div class="grid cards" markdown>

-   :material-import: **[Import existing configuration](import.md)**

    ---

    Generate resources from a running OpenBao server.

-   :material-swap-horizontal: **[Migrate to the operator](migration.md)**

    ---

    Hand objects over from another tool, one at a time.

-   :material-sync: **[Argo CD](argocd.md)**

    ---

    A health check so sync waves wait for OpenBao.

-   :material-power: **[OpenBao self-init](self-init.md)**

    ---

    Create the operator's login at OpenBao's first boot, without a root token.

</div>
