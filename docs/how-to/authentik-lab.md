---
title: Test OpenBao OIDC and JWT with Authentik
description: Run an isolated Authentik lab to verify operator-managed OpenBao login, group policies and tenant isolation with browser and service identities.
---

# Test OpenBao OIDC and JWT with Authentik

Use this disposable kind lab to test Authentik identities against OpenBao configuration managed by
this operator. It creates its own Authentik instance; no hosted account or credentials are required.

The lab installs the official Authentik Helm chart with a pinned server image and a separate
PostgreSQL instance. Both the database and identities are disposable. This is a test deployment,
not a production Authentik installation.

## Before you start

Install the [local lab prerequisites](../tutorials/local-lab.md), including Docker, kind, Helm,
kubectl and the browser runner dependencies. Run the commands from the repository root. Allow
additional memory for Authentik's server, worker and PostgreSQL alongside OpenBao and the operator.

The identity provider uses HTTP only inside this disposable kind network. OpenBao connections
continue to use the lab's TLS certificate and CA verification. The browser reaches Authentik through
a loopback port-forward while preserving its cluster issuer URL.

## Run the regression

Create a dedicated cluster and bootstrap the operator's OpenBao connection:

```bash
export KIND_CLUSTER=obco-authentik
export LAB_CONTEXT=kind-obco-authentik
hack/lab/up.sh raft
make lab-authentik
```

Provisioning generates independent passwords, a bootstrap token, an OIDC client secret and service
account credentials. It stores them in Kubernetes Secrets in `obco-lab-authentik`; they are never
printed or committed. The provisioning command refuses an existing Authentik namespace instead of
silently replacing identities. To repeat the regression on an existing lab:

```bash
source hack/lab/common.sh
python3 hack/lab/authentik.py test
```

## What the tests prove

Nine human identities cover `root`, `alpha` and `beta`, each with `admin`, `read` and `ops` roles.
The tenant namespaces are `saas-authentik-alpha` and `saas-authentik-beta`; the root
scenario uses restricted policies, not an OpenBao root token. The operator creates auth mounts, JWT/OIDC configurations, roles, ACL policies,
external groups, group aliases and internal group memberships.

| Flow | Checks |
| --- | --- |
| Browser OIDC | Real Authentik login, group-derived permissions, KV versioning and transit operations, tenant isolation, and operator-managed grant removal and restoration |
| Human JWT | Authorization-code login with PKCE produces ID tokens; OpenBao validates each token through discovery and direct JWKS configurations |
| Service JWT | Nine separate Authentik service accounts obtain signed access tokens through the native `client_credentials` grant using app-password tokens |
| JWT denial cases | Wrong group, wrong tenant, wrong audience, wrong issuer and corrupted signature must fail for the expected reason in both validation modes |

The human JWT matrix has 18 positive sessions and 30 classified denial cases. Service identities
repeat that matrix using access tokens acquired without a human browser session. Human ID-token
checks are not counted as machine authentication.

Authentik's `groups` scope mapping supplies each account's group names in tokens. The provider uses
a generated RSA signing key and a confidential client; the test does not disable signature or
audience validation. Authentik groups are not OpenBao administrator privileges by themselves:
the operator's policies determine each group's capabilities within its OpenBao namespace.

## Troubleshoot a failure

Inspect readiness without exposing Secret contents:

```bash
kubectl --context "$LAB_CONTEXT" -n obco-lab-authentik get pods
kubectl --context "$LAB_CONTEXT" -n obco-lab get jwtauthroles,jwtauthconfigs
```

If installation fails, check image pulls and available memory. If a CRD is not Ready, inspect its
conditions and references. For login failures, use the test's safe phase/category diagnostics;
do not publish raw provider logs, callback URLs, tokens or Kubernetes Secrets.

An interrupted provisioning run may leave a partial disposable instance. Delete the dedicated
cluster and start again rather than adopting or overwriting those resources.

## Clean up

```bash
kind delete cluster --name "$KIND_CLUSTER"
```

Deleting the lab removes the generated credentials and ephemeral PostgreSQL data. No external
Authentik tenant is modified.

## Next steps

- [OIDC permissions showcase](../tutorials/oidc-showcase.md) explains the access journeys.
- [JWT and OIDC authentication](../guides/jwt-oidc-auth.md) covers operator configuration.
- Authentik documents [automated bootstrap](https://docs.goauthentik.io/install-config/automated-install/),
  [OAuth2/OIDC providers](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/), and
  [native machine authentication](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/machine_to_machine).
