---
title: Run the Authentik OpenBao lab
description: "Run the kind Authentik profile that exercises operator-managed OpenBao OIDC, JWT and tenant isolation from a clone."
---

# Run the Authentik lab

For contributors working in a clone, this page runs the in-cluster Authentik profile (`make lab-authentik` / `hack/lab/provider-test.sh authentik`) against a disposable kind OpenBao lab.

## What it covers

- Official Authentik Helm chart plus PostgreSQL in Kubernetes namespace `obco-lab-authentik`
- Nine identities (`admin`, `read`, `ops` × root/alpha/beta); tenants `saas-authentik-alpha` and `saas-authentik-beta`
- Browser OIDC plus JWT discovery and JWKS mounts configured through the operator
- Group-derived policies, allowed/denied KV and transit operations, tenant isolation and revocation

## Prerequisites

- A clone of this repository; run commands from the repository root
- The [local lab](../tutorials/local-lab.md#prerequisites) prerequisites
- Extra memory for Authentik server, worker and PostgreSQL beside OpenBao and the operator

Authentik uses HTTP inside kind; OpenBao keeps HTTPS with the lab CA. The browser reaches Authentik through a loopback port-forward while preserving the cluster issuer URL.

## Run

```bash
export KIND_CLUSTER=obco-authentik-lab
export LAB_CONTEXT="kind-${KIND_CLUSTER}"
hack/lab/up.sh raft
make lab-authentik
```

`make lab-authentik` runs `npm ci --ignore-scripts --prefix hack/lab/browser` then `hack/lab/provider-test.sh authentik` (`up`, `apply`, `test`). Credentials stay in Kubernetes Secrets in `obco-lab-authentik` and are never printed.

Repeat tests on an existing lab:

```bash
source hack/lab/common.sh
python3 hack/lab/authentik.py test
```

Provisioning refuses an existing Authentik namespace instead of replacing identities silently.

## Inspect

```bash
kubectl --context "$LAB_CONTEXT" -n obco-lab-authentik get pods
kubectl --context "$LAB_CONTEXT" -n obco-lab get jwtauthroles,jwtauthconfigs
```

Permission journeys match [Run the OIDC permissions showcase lab](../tutorials/oidc-showcase.md). On failure use the test's phase/category diagnostics; do not publish Secrets, tokens or raw provider logs. Non-secret chart settings: `hack/lab/authentik/values.yaml`.

## Clean up

```bash
bin/kind delete cluster --name "$KIND_CLUSTER"
```

Deleting the cluster removes generated credentials and ephemeral PostgreSQL data. No external Authentik tenant is modified.
