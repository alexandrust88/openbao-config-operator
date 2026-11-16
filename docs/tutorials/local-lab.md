# Test user and workload access in a local lab

Run the configuration operator against a disposable, three-node OpenBao cluster in kind. Add
Keycloak for human and machine authentication, External Secrets Operator (ESO) for workload
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
```

The identity profile creates a root namespace scenario and two tenant scenarios, `lab-alpha`
and `lab-beta`. A root-namespace user is an ordinary policy-restricted identity, not an OpenBao
root-token holder. Each scenario has editors, viewers and a machine client.

Keycloak group claims map through `GroupAlias` resources to external OpenBao groups. Internal
group membership grants shared access. The tests check the resulting permissions, including
allowed reads, viewer write rejection, inherited shared reads, forbidden paths and cross-tenant
rejection. JWT and OIDC authentication are exercised separately.

The browser profile uses a digest-pinned public Playwright image. It submits the Keycloak login
form and completes an OpenBao OIDC authorization-code flow, then tests a user whose group does
not match the requested role. This checks the browser authentication journey; it does not claim
coverage of every OpenBao UI screen. The non-browser suite additionally tests machine clients.

Keycloak runs with HTTP inside this isolated development setup. OpenBao API connections use
HTTPS with the lab CA. Do not expose this lab or reuse its credentials in another environment.

## Deliver secrets to workloads with ESO

```sh
hack/lab/eso-up.sh
hack/lab/eso-test.sh
```

ESO uses its `vault` provider to connect to OpenBao. Each tenant's SecretStore authenticates
with a tenant ServiceAccount. The checks cover initial delivery, source-value rotation and
cross-tenant rejection. The configuration operator manages the auth mounts, roles and policies;
ESO manages the application Secrets.

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

The **integration lab** GitHub Actions workflow runs the identity, ESO, PKI and LDAP profiles
in separate jobs when manually dispatched. Each job creates and deletes its own kind cluster.
It uploads test logs and pod status, never the lab state directory, kubeconfig or credentials.

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
