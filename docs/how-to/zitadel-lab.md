---
title: Run the ZITADEL OpenBao lab
description: "Run the kind ZITADEL profile that exercises operator-managed OpenBao human OIDC and service-user JWT from a clone."
---

# Run the ZITADEL lab

For contributors working in a clone, this page runs the in-cluster ZITADEL profile (`make lab-zitadel` / `hack/lab/provider-test.sh zitadel`) against a disposable kind OpenBao lab.

## What it covers

- ZITADEL and PostgreSQL in Kubernetes namespace `obco-lab-zitadel`
- Nine human users and nine service users (`admin`, `read`, `ops` × root/alpha/beta)
- Browser OIDC; human ID tokens on JWT discovery and JWKS mounts; machine client-credentials JWTs
- Complement Token Action emitting a `groups` array OpenBao can consume
- Wrong-role, wrong-tenant, wrong-audience, wrong-issuer and bad-signature rejection

## Prerequisites

- A clone of this repository; run commands from the repository root
- The [local lab](../tutorials/local-lab.md#prerequisites) prerequisites
- Extra memory for ZITADEL and PostgreSQL beside OpenBao and the operator

## Run

```bash
export KIND_CLUSTER=obco-zitadel-lab
export LAB_CONTEXT="kind-${KIND_CLUSTER}"
hack/lab/up.sh raft
make lab-zitadel
```

`make lab-zitadel` installs the browser package and runs `hack/lab/provider-test.sh zitadel` (`python3 hack/lab/zitadel.py` `up`, `apply`, `test`). Equivalent direct call:

```bash
npm ci --ignore-scripts --prefix hack/lab/browser
hack/lab/provider-test.sh zitadel
```

Repeat only the tests:

```bash
source hack/lab/common.sh
python3 hack/lab/zitadel.py test
```

A partial provisioning run is refused on retry; inspect then delete and rebuild rather than adopting partial state.

## Inspect

Human and machine grants use separate projects so audiences do not overlap. The Action selects only the dedicated project's nine known roles. Chart/image pins and referenced Secrets live in `hack/lab/zitadel.py` and its values; the bootstrap PAT expires after one day and stays out of operator resources. Private state Secret: `zitadel-lab-state` in `obco-lab-zitadel`—do not print or upload it.

Issuer is `http://zitadel.obco-lab-zitadel.svc:8080`; callback is `http://127.0.0.1:8250/oidc/callback`. The fixture removes optional OTP/U2F enrollment offerings before creating users and refuses a policy that already requires MFA; it is not MFA or SAML coverage. Permission matrix detail: [Run the OIDC permissions showcase lab](../tutorials/oidc-showcase.md).

## Clean up

```bash
bin/kind delete cluster --name "$KIND_CLUSTER"
```

Deleting only `obco-lab-zitadel` leaves OpenBao configuration in the lab; delete the whole kind cluster for a full reset ([local lab cleanup](../tutorials/local-lab.md#remove-the-lab)).
