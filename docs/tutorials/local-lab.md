---
description: "Run a kind integration lab for OpenBao, Keycloak, Authentik and ZITADEL authentication, External Secrets Operator, cert-manager PKI and LDAP."
---

# Test user and workload access in a local lab

Run the configuration operator against a disposable, three-node OpenBao cluster in kind. Add
Keycloak, Authentik or ZITADEL for human and machine authentication, External Secrets Operator (ESO) for workload
secret delivery, or cert-manager for certificate issuance. The operator configures OpenBao
through its own CRDs; the other controllers consume that configuration.

You need Docker, kubectl, Go, Python 3, OpenSSL and the repository's Helm and kind tools. The
browser profile also needs Node.js and npm to install the locked Playwright package. Allow
space for the browser image and sufficient memory for OpenBao, the operator and the selected
controllers. These commands use generated test credentials and disposable storage.

## Start the cluster

From the repository root:

```sh
hack/lab/up.sh raft
export LAB_CONTEXT=kind-openbao-config-operator-lab
```

The base profile uses TLS, three raft replicas, persistent volumes and active-node routing.
The `postgres` profile adds PostgreSQL as a target for the database secrets engine; OpenBao
still stores its own data in raft. Operator settings can be overridden with Helm values:

```sh
hack/lab/up.sh postgres -f hack/lab/profiles/operator-single.yaml
```

The scripts keep the lab kubeconfig under `.tmp/lab/` and do not change your default context.
To use an existing lab cluster created by the e2e suite, set `LAB_CONTEXT` to that kind context
and run `hack/lab/bootstrap.sh`. Bootstrap credentials configure the lab only; the access tests
use user tokens, machine tokens or Kubernetes ServiceAccounts.

## Test Keycloak users and machines

```sh
hack/lab/identity-up.sh
hack/lab/identity-test.sh
npm ci --ignore-scripts --prefix hack/lab/browser
hack/lab/identity-browser.sh
hack/lab/keycloak-test.sh
```

The identity profile creates a root namespace scenario and two tenant scenarios, `lab-alpha`
and `lab-beta`. A root-namespace user is an ordinary policy-restricted identity, not an OpenBao
root-token holder. Each scenario has editors, viewers and a machine client.

Keycloak group claims map through `GroupAlias` resources to external OpenBao groups. The tests
check allowed reads, viewer write rejection, inherited shared reads, forbidden paths and
cross-tenant rejection. `keycloak-test.sh` repeats that for `admin`, `read` and `ops` in root,
alpha and beta, on one auth mount per mode:

| Mode | Operator configuration | Login |
| --- | --- | --- |
| Browser OIDC | `JWTAuthConfig.oidcDiscoveryURL`, `oidcClientID` and `oidcClientSecretSecretRef` | Authorization code through the Keycloak login form |
| JWT with discovery | `JWTAuthConfig.oidcDiscoveryURL` and `boundIssuer` | Keycloak-issued JWT submitted to OpenBao |
| JWT with JWKS | `JWTAuthConfig.jwksURL` and `boundIssuer` | The same signed JWT verified through the realm's key endpoint |

JWKS supplies public signing keys; it does not replace the OIDC client or browser redirect flow.
JWT denial cases distinguish group, issuer, audience and signature failures.

Keycloak runs with HTTP inside this isolated development setup. OpenBao API connections use
HTTPS with the lab CA. Do not expose this lab or reuse its credentials in another environment.

## Test Authentik or ZITADEL

Each provider runs in its own Kubernetes namespace with PostgreSQL. After starting the base lab,
choose one profile:

```sh
make lab-authentik
# Or:
make lab-zitadel
```

Both profiles provision disposable identities and applications, then configure OpenBao through
the operator. The test matrix covers root and two child namespaces, admin/read/ops roles, browser
OIDC, and JWT validation through discovery and direct JWKS. Tests check group-derived policies,
allowed and denied KV/transit operations, tenant isolation and token revocation.

Use the [Authentik guide](../how-to/authentik-lab.md) or [ZITADEL guide](../how-to/zitadel-lab.md)
for provider setup, claim mapping and machine-token scenarios. These local providers use fixed
cluster-internal HTTP endpoints; OpenBao uses the lab CA and HTTPS. The setup is for disposable
regression tests, not a production identity-provider deployment.

To test hosted identity providers, use the [Okta and Auth0 lab guide](../guides/saas-identity-lab.md).
It provisions dedicated provider resources and uses separate CRD-managed auth mounts and tenant
namespaces. Human OIDC login, JWT discovery/JWKS validation, groups and access regressions
run in CI.

## Deliver secrets to workloads with ESO

```sh
hack/lab/identity-up.sh
make lab-eso
```

ESO uses its `vault` provider to connect to OpenBao. Each tenant's SecretStore authenticates
with a tenant ServiceAccount. The checks cover initial delivery, source-value rotation and
cross-tenant rejection. The configuration operator manages the auth mounts, roles and policies;
ESO manages the application Secrets. The [generated-credential tutorial](generated-credentials.md)
adds explicit password rotation, selected-key projection, recovery and Orphan retention.

## Encrypt application data with transit keys

```sh
make lab-transit
```

The [application-encryption tutorial](application-encryption.md) runs a Go workload using its
own projected ServiceAccount token in root and a child OpenBao namespace. It tests encryption,
same-key rewrap, cross-key migration, all 15 software key algorithms and key lifecycle guards.
It requires only the base lab, not an identity-provider deployment.

## Issue a localhost certificate with cert-manager

```sh
hack/lab/pki-up.sh
hack/lab/pki-test.sh
```

The operator creates a PKI root, an intermediate issuer, a restricted signing role and
Kubernetes authentication in the `lab-pki` OpenBao namespace. cert-manager authenticates with
a short-lived ServiceAccount token and writes `localhost-tls` in Kubernetes namespace
`obco-lab-pki`.

The test verifies the certificate against the root and intermediate fetched independently from
the operator-managed OpenBao issuers, checks the `localhost` and `app.localhost` DNS names, and
checks that the private key matches. A certificate from another CA or intermediate fails verification. It proves that the issuer identity can sign a CSR but cannot change policies.
It then deletes this lab TLS Secret and checks that cert-manager recreates it with a new
certificate and key. This tests recovery and key rotation; it does not wait for scheduled
renewal. No public DNS name or ACME service is needed, and the lab CA is not installed in your
machine's trust store.

## Add a real external plugin

The [LDAP profile](../../hack/lab/plugins/README.md) builds a test OpenBao image containing the
published OpenBao LDAP auth plugin and deploys an LDAP directory. It exercises the plugin
catalogue, auth mount, LDAP configuration and group/user mappings through operator CRDs, then
checks actual LDAP authentication. It is separate from the tiny synthetic plugin used to test
registration and process recovery in the live kit.

## Run the profiles in CI

The **integration lab** GitHub Actions workflow runs Keycloak (`identity`), Authentik, ZITADEL,
transit and ESO in separate jobs on relevant pull requests and weekly. Manual runs also include
PKI and LDAP.
The identity-provider jobs exercise browser OIDC and JWT discovery/JWKS. Providers run inside
the cluster, so these tests need no external identity-provider account or GitHub secret.
Each job creates and deletes its own kind cluster.
It uploads test logs and pod status, never the lab state directory, kubeconfig or credentials.

## Test dynamic database credentials

After starting the lab, select one provider:

```sh
make lab-database DATABASE_PROFILE=postgresql
```

Use the [database tutorial](dynamic-database-credentials.md) for the root/child lease lifecycle
and recipes for MySQL, MariaDB, Cassandra, InfluxDB and Valkey.
[Custom database plugins](../how-to/databases/custom.md) are documented separately and are not installed by the lab.
These are actual login, permission and revocation tests. PostgreSQL adds isolated static-role
and root-password rotation checks; the other profiles do not cover those rotations.

## Remove the lab

Delete only the cluster you created for this tutorial:

```sh
bin/kind delete cluster --name openbao-config-operator-lab
```

Deleting the cluster removes its Kubernetes resources and raft volumes. Local reports and the
kubeconfig remain under the git-ignored `.tmp/lab/` directory.

## Sources and limits

The user-access scenarios draw on the public
[Stakater integration lab](https://github.com/stakater/mto-extension-vault-enterprise): pair each
denial with an allowed operation, exercise OIDC in a browser, and fail when required results
are absent. This lab uses our operator and OpenBao; it requires neither MTO nor Vault Enterprise.
Stakater's browser-runner image was inaccessible during implementation, so its private build
is not a dependency.

See [cert-manager's issuer documentation](https://cert-manager.io/docs/configuration/vault/)
for ServiceAccount authentication and [Playwright's Docker documentation](https://playwright.dev/docs/docker)
for the browser image. These lab profiles supplement the
[verification suites](../project/verification.md); they do not establish live coverage for
cloud plugins that require external accounts or credentials.
