---
title: Run the SaaS identity OpenBao lab
description: "Run Auth0 or Okta OIDC, JWT and tenant permission checks against OpenBao Config Operator from a repository clone."
---

# Run the SaaS identity lab

For contributors working in a clone, this page runs hosted Auth0 or Okta login and group-based access against OpenBao configured by the operator in a disposable kind lab.

## What it covers

- OIDC browser login for `admin`, `read` and `ops` in root and two tenant namespaces
- JWT validation through discovery and an explicit JWKS endpoint (authorization-code + PKCE; no password grant)
- Operator-managed mounts, groups and policies in separate Auth0/Okta tenant namespaces
- The permission matrix and policy-change journey in [Run the OIDC permissions showcase lab](../tutorials/oidc-showcase.md)

## Prerequisites

- A clone of this repository; run commands from the repository root
- Docker, kubectl, Go, Python 3, Node.js, npm and the [local lab](../tutorials/local-lab.md) tooling
- A dedicated Auth0 development tenant or Okta organisation with permission to create and delete lab applications, users and authorisation configuration
- Provider state from [Provision Auth0 for the SaaS identity lab](../how-to/auth0-lab.md) or [Provision Okta for the SaaS identity lab](../how-to/okta-lab.md)

!!! warning
    The browser runner does not bypass MFA, CAPTCHA, enrollment or consent. For unattended runs the application's policy must allow the disposable users to sign in without an interactive challenge. A passing automated run does not establish MFA coverage.

None of the lab users receives the OpenBao `root` policy.

## Run

Prepare a private workspace (default path is git-ignored):

```bash
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
umask 077
install -d -m 700 "$LAB_DIR"
```

Provision the provider with `--apply` per the Auth0 or Okta page, then:

```bash
export PROVIDER=auth0
export STATE="$LAB_DIR/$PROVIDER/state.json"
export LAB_CONTEXT=kind-openbao-config-operator-lab

hack/lab/up.sh raft
npm ci --ignore-scripts --prefix hack/lab/browser
hack/lab/saas/run.sh apply --state "$STATE"
hack/lab/saas/run.sh jwt --state "$STATE"
```

For the browser permission matrix, continue with [Run the OIDC permissions showcase lab](../tutorials/oidc-showcase.md).

Use `PROVIDER=okta` when that state was prepared. Only the OIDC client secret is passed into Kubernetes; the provider-management credential stays with the provisioner. Auth0 and Okta mounts can coexist in the same lab.

Interactive single-role login:

```bash
hack/lab/saas/run.sh oidc --state "$STATE" --tenant alpha --role read
```

Expect denial (sign in as the tenant's read user while requesting admin):

```bash
hack/lab/saas/run.sh oidc --state "$STATE" --tenant alpha --role admin --expect-denied
```

## Inspect

`apply` waits for current-generation readiness of the SaaS identity resources. The JWT command prints results by namespace and role and exits non-zero on failure. Keep `$STATE` until cleanup succeeds: it holds login credentials and ownership records for deletion. Do not commit it or paste it into an issue.

## Clean up

Run the provider page's `--cleanup` with the original prefix, credentials and state. Then:

```bash
bin/kind delete cluster --name openbao-config-operator-lab
```

Lab CRDs use `Orphan` deletion policy: deleting only Kubernetes resources leaves objects in a retained OpenBao. If a provider request was interrupted, confirm resources in the provider console before deleting the state file. Revoke the provisioning credential when you stop using the lab.
