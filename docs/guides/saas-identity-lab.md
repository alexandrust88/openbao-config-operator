---
title: Test OpenBao access with Okta and Auth0
description: "Verify Okta and Auth0 OIDC login, JWT validation and tenant permissions in OpenBao using operator-managed configuration."
---

# Test OpenBao access with Okta and Auth0

Verify hosted login and group-based access against an OpenBao cluster configured by the
operator. The operator manages OpenBao through its CRDs. The provider scripts create and
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
sign in without an interactive challenge. The browser test does not bypass MFA, CAPTCHA,
enrollment or consent, and a blocked journey fails the test. Use an interactive login to test
those flows separately; a passing automated run does not establish MFA coverage.

A successful run exercises `admin`, `read` and `ops` in the root namespace and in two tenant
namespaces. None of those users receives the OpenBao `root` policy. The journeys and the expected
permissions are in the [OIDC permissions showcase](../tutorials/oidc-showcase.md).

## Run the lab

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

## Clean up

Run the provider guide's `--cleanup` command with the original prefix,
credentials and state. Then remove the disposable cluster:

```sh
bin/kind delete cluster --name openbao-config-operator-lab
```

The lab CRDs use `Orphan` deletion policy. Deleting only the Kubernetes resources leaves their
configuration in a retained OpenBao server. Keep the lab disposable or review those objects
before reusing the server.

Keep the state file until cleanup finishes. If a provider request is interrupted, the resource
can exist even though the script did not record its ID. Check that provider before you delete
the state file or create the same names again. Retire the provisioning credential when you stop
using the lab.
