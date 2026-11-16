---
title: Test OpenBao access with Okta and Auth0
description: "Verify Okta and Auth0 OIDC login, JWT validation and tenant permissions in OpenBao using operator-managed configuration."
---

# Test OpenBao access with Okta and Auth0

Verify real hosted login and group-based access against an OpenBao cluster configured by the
operator. Run the regression in GitHub Actions, or use the same scripts locally to investigate
an integration. The operator manages OpenBao through its CRDs; provider scripts create and
remove the disposable identity-provider resources.

The tests cover OIDC browser login and JWT validation through both discovery and an explicit
JWKS endpoint. JWT tests obtain ID tokens through the authorization-code flow with PKCE;
no password grant or additional machine application is required.

## Before you begin

Use a dedicated development Auth0 tenant or Okta organisation. You need permission to create
and delete the test applications, users and authorization configuration. Provider subscriptions,
quotas and sign-in policies must support the setup described in the provider guide:

- [Prepare Auth0](../how-to/auth0-lab.md): create a Management API provisioning application.
- [Prepare Okta](../how-to/okta-lab.md): prepare the lab API token and custom authorization server prerequisites.

For unattended tests, the dedicated application's policy must allow the disposable users to
sign in without an interactive challenge. The runner does not bypass MFA, CAPTCHA, enrollment
or consent, and a blocked journey fails the test. Use an interactive login to test those flows
separately; a passing automated regression does not establish MFA coverage.

## What a successful run proves

Each provider is tested in the OpenBao root namespace and two isolated tenant namespaces,
with `admin`, `read` and `ops` identities in each. These are application-data roles: even in the
root namespace, no user receives the OpenBao `root` policy.

| Role | Access to the lab's resources |
|------|-------------------------------|
| `read` | Read application data from KV |
| `ops` | Read KV data and encrypt/decrypt with the transit key |
| `admin` | Create, read, update and delete KV data; encrypt/decrypt with the transit key |

All roles must be denied system administration, destruction of KV versions, transit key
rotation/export and access to other tenants. Group claims must produce the expected identity
policies through external groups, aliases and inherited internal-group membership.

The OIDC suite runs 17 browser journeys: nine successful role logins, six rejected role/tenant
combinations, and two logins proving that an operator-managed group policy can remove and restore
access. The JWT suite checks the same role permissions with discovery and JWKS validation,
including rejection of wrong groups, tenants, audiences, issuers and signatures. Successful
sessions also check KV history where permitted and rejection after token revocation.

See the [OIDC permissions showcase](../tutorials/oidc-showcase.md) for the executable scenario
catalogue, CRDs involved and expected results.

## Run in GitHub Actions

### Configure provider credentials

Create GitHub environments named **`auth0-lab`** and **`okta-lab`**. Add a secret named
**`SAAS_CREDENTIALS_JSON`** to each, using the credential JSON described in the corresponding
provider guide. Include deletion permissions so the workflow can clean up after a failed test.
Restrict access to trusted branches and use required reviewers where appropriate.

You can enter the JSON in GitHub's environment settings. If you prepared local credential
files using the provider guides, upload them without placing their contents in command arguments:

```sh
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
gh secret set SAAS_CREDENTIALS_JSON --env auth0-lab < "$LAB_DIR/auth0/credentials.json"
gh secret set SAAS_CREDENTIALS_JSON --env okta-lab < "$LAB_DIR/okta/credentials.json"
```

### Start and verify the workflow

Run **SaaS OIDC regression** from the repository's Actions tab once the workflow is available
on the default branch, or use the CLI:

```sh
gh workflow run saas-lab.yml
```

Both provider jobs run independently. Each provisions an isolated application and nine users,
deploys the TLS raft lab, applies the operator resources, runs the OIDC and JWT suites, then
removes its recorded provider resources and kind cluster. A unique run prefix separates CI
resources from local tests.

A successful job requires provisioning, both authentication suites and cleanup to pass. Missing
credentials or a blocked login is a failure. The ordinary CI login fixtures run without provider
accounts; they test the browser harness, not the hosted integration.

After both providers pass, set repository variable **`SAAS_REGRESSION_ENABLED=true`** to enable
weekly runs and relevant pull requests from branches in this repository. Fork pull requests do
not receive the credentials. Manual runs remain available before recurring runs are enabled.

## Run locally

Run commands from the repository root. You need the [local lab prerequisites](../tutorials/local-lab.md),
Node.js, npm and a running Docker daemon. The browser uses the repository's pinned Playwright image.

### Prepare a private workspace

Choose a workspace for generated credentials and state. The default below is ignored by Git;
if you override it, use a private directory outside version control.

```sh
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
umask 077
install -d -m 700 "$LAB_DIR"
```

Follow the [Auth0](../how-to/auth0-lab.md) or [Okta](../how-to/okta-lab.md) guide to provision the
provider resources in that workspace. Provisioning previews by default; `--apply` creates the
resources. Keep the generated state until cleanup succeeds: it contains login credentials and
the ownership information needed to remove the resources. Do not commit it, upload it as an
artifact or paste it into an issue.

### Apply configuration and run the regression

Choose the provider you prepared:

```sh
export PROVIDER=auth0  # or okta
export STATE="$LAB_DIR/$PROVIDER/state.json"
export LAB_CONTEXT=kind-openbao-config-operator-lab

hack/lab/up.sh raft
npm ci --ignore-scripts --prefix hack/lab/browser
hack/lab/saas/run.sh apply --state "$STATE"
hack/lab/saas/run.sh showcase --state "$STATE"
hack/lab/saas/run.sh jwt --state "$STATE"
```

`apply` waits for the operator resources to report current-generation readiness. The login
commands print results by namespace and role and return a non-zero status on failure. Only the
OIDC client secret is passed to Kubernetes; the provider-management credential stays with the
provisioner. Auth0 and Okta use separate tenant namespaces and can coexist in the same lab.

### Investigate one login interactively

To inspect a specific role, use your normal browser:

```sh
hack/lab/saas/run.sh oidc --state "$STATE" --tenant alpha --role read
```

Sign in as the matching test user from your private state file. The command listens on
`http://127.0.0.1:8250/oidc/callback`, exchanges the callback through OpenBao, checks the role's
permissions and revokes its token. Complete MFA or consent normally when requested.

Use a separate browser profile or sign out between users. To check a group denial, request an
admin role but sign in as the same tenant's read user:

```sh
hack/lab/saas/run.sh oidc --state "$STATE" --tenant alpha --role admin --expect-denied
```

## Clean up and recover

For local tests, run the provider guide's `--cleanup` command with the original prefix,
credentials and state. Then remove the disposable cluster:

```sh
bin/kind delete cluster --name openbao-config-operator-lab
```

The lab CRDs use `Orphan` deletion policy. Deleting only the Kubernetes resources leaves their
configuration in a retained OpenBao server. Keep the lab disposable or review those objects
before reusing the server.

CI attempts cleanup even when a test fails. If provider cleanup fails, the job publishes a
`saas-<provider>-recovery` artifact containing only the provider identity, run prefix and owned
resource IDs. Download it into a private directory, set the JSON file to mode `0600`, and follow
the provider guide's recovery procedure using a fresh management credential. Full state,
passwords, browser traces and screenshots are not published.

An interrupted provider request or a forcibly stopped runner can leave resources behind. Inspect
the run prefix and provider audit log before retrying; do not discard incomplete state or delete
shared provider configuration. Retire the provisioning credentials and GitHub environment
secrets when you stop using the lab.
