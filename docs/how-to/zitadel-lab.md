---
title: Test OpenBao OIDC and JWT with ZITADEL
description: Test ZITADEL human OIDC and service-user JWT authentication against operator-managed OpenBao namespaces in a disposable kind lab.
---

# Test OpenBao OIDC and JWT with ZITADEL

This fixture runs ZITADEL and PostgreSQL inside kind. It creates nine human users and nine
separate service users: `admin`, `read` and `ops` in root, alpha and beta. The OpenBao Config
Operator creates the auth mounts, roles, policies, external groups and group aliases through
its CRDs. No ZITADEL Cloud account or external API credential is required.

## Start the fixture

Start the [local lab](../tutorials/local-lab.md) with Docker running, then install the locked
browser package and run the provider profile:

```sh
export KIND_CLUSTER=obco-zitadel-lab
export LAB_CONTEXT=kind-${KIND_CLUSTER}
hack/lab/up.sh raft
npm ci --ignore-scripts --prefix hack/lab/browser
hack/lab/provider-test.sh zitadel
```

The profile runs `python3 hack/lab/zitadel.py up`, `apply` and `test` in order. The `up` command
installs the official ZITADEL chart and creates disposable identities; `apply` waits for the
operator's resources; `test` exercises login and permissions. To repeat only the regression
against an existing fixture, load its isolated kubeconfig first:

```sh
source hack/lab/common.sh
python3 hack/lab/zitadel.py test
```

A partial provisioning run is refused on retry; inspect its resources
before deleting and rebuilding only this fixture's namespace.

## What is tested

- Human browser OIDC in the root namespace and both child namespaces, with role-specific KV
  and transit access, group-derived policies, forbidden operations and revoked-token rejection.
- Human ID tokens against separate JWT discovery and explicit JWKS auth mounts.
- Nine distinct ZITADEL service users authenticated with client credentials. Their signed access
  tokens exercise the same JWT/ACL matrix; these are machine logins, not renamed human tokens.
- Wrong-role, wrong-tenant, wrong-audience, wrong-issuer and corrupted-signature rejection.
- Separation between the human application's audience and the machine project's audience.

The shared test matrix requires the expected rejection reason; an unavailable server does not
count as a successful denial test. The fixture deliberately tests unattended password login. Before creating users, it removes
optional OTP/U2F enrollment offerings from this disposable instance through the Admin API; it
refuses a policy that already requires MFA. The browser does not click through or bypass an MFA
challenge. This is not MFA, passwordless, SAML or complete ZITADEL-policy coverage.

## How roles become OpenBao groups

ZITADEL's native project-role claim is an object, while OpenBao's `groupsClaim` expects a
string or a list of strings. The fixture creates real project roles and user grants, then uses
a [Complement Token Action](https://zitadel.com/docs/apis/actions/complement-token) to emit a
`groups` array. It selects only the dedicated project's nine known roles; unrelated project
roles and user-supplied metadata cannot add groups. The ID-token action additionally checks
the owned OIDC application's client ID.

Human and machine grants belong to separate projects. ZITADEL ID tokens can include their
project ID in the audience; separate projects prevent a human token satisfying the machine
audience check. Service-user subjects are checked against their recorded IDs before OpenBao
verifies the JWT signature.

The ID-token trigger is `4`; the access-token trigger is `5`, both in flow `2`. This uses the
Actions V1 API supported by the pinned ZITADEL version. It is separate from Actions V2's
external execution model. See the upstream [claims reference](https://zitadel.com/docs/apis/openidoauth/claims)
for the native roles shape.

## Credentials and deployment boundaries

The chart is pinned to version `10.5.0` and verified by its archive SHA-256. ZITADEL uses a
pinned `4.20.3` image, overriding the chart's older application default; PostgreSQL and helper
images are also pinned by digest. The fixture follows the official chart's
[referenced Secrets example](https://github.com/zitadel/zitadel-charts/tree/main/examples/3-referenced-secrets).
The master key and database password are generated at runtime and stored in Kubernetes Secrets,
not Helm values or Git. The bootstrap service-user PAT expires after one day.

The private `zitadel-lab-state` Secret in `obco-lab-zitadel` holds generated application,
human and machine credentials. Do not print or upload it. The bootstrap PAT remains in its
own Secret and is not copied into operator resources. A completed fixture is reused; partial
state is never silently adopted.

The built-in login interface is enabled for this fixture (`login.enabled: false` disables the
separate Login V2 deployment). It uses HTTP at the fixed internal Service address
`http://zitadel.obco-lab-zitadel.svc:8080`; the browser reaches it through a loopback port-forward.
OpenBao still uses HTTPS with the lab CA. The confidential application uses Basic client
authentication and the exact loopback callback `http://127.0.0.1:8250/oidc/callback`.
Its development mode permits that HTTP callback. These settings are for a disposable local
fixture, not an externally exposed deployment.

## Remove the fixture

Delete the disposable kind cluster using the [local lab cleanup](../tutorials/local-lab.md#remove-the-lab).
This removes ZITADEL, its ephemeral database, test identities and credentials. Deleting only
`obco-lab-zitadel` removes the provider but leaves the lab's OpenBao configuration; delete the
whole disposable cluster for a complete reset.
