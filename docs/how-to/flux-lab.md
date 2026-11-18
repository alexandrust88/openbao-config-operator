---
title: Run the Flux OpenBao lab
description: "Run the kind lab that deploys OpenBao and the configuration operator through Flux from checked-in GitOps manifests."
---

# Run the Flux lab

For contributors working in a clone, this page runs `make lab-flux` to deploy OpenBao and the operator through Flux Operator and the checked-in `gitops/flux/` manifests against a disposable kind cluster.

## What it covers

- A fresh kind cluster (`obco-gitops-flux` by default) that refuses an existing cluster of that name
- Flux Operator install, then GitRepository, Kustomizations and HelmReleases from `gitops/flux/`
- Public Git + official OpenBao chart + digest-pinned published operator OCI chart
- Checks of source revision, root/child OpenBao objects, AppRole permissions, transit round trips and drift repair
- Platform vs configuration reconciler RBAC boundaries in the example

## Prerequisites

- A clone of this repository; run commands from the repository root
- Docker, Go, Python 3, kubectl, OpenSSL and Make (the target installs `bin/kind`, `bin/helm` and `bin/yq`)
- Outbound access to GitHub, chart registries and OCI

## Run

```bash
make lab-flux
```

That invokes `hack/lab/gitops/run.sh flux`. The runner creates the cluster, prepares bootstrap Secrets outside Git, installs Flux Operator and applies the checked-in bootstrap. The public GitRepository follows `main` by default.

Pin an already-pushed commit:

```bash
GITOPS_REVISION="$(git rev-parse HEAD)" make lab-flux
```

Local uncommitted changes to Git-managed manifests are not part of this smoke test. The published operator image is tested, not the manager binary in the checkout.

Keep the cluster:

```bash
KEEP_GITOPS_LAB=1 make lab-flux
```

## Inspect

| Path | Purpose |
| --- | --- |
| `gitops/flux/flux-instance.yaml` | Flux distribution via Flux Operator |
| `gitops/flux/bootstrap/source.yaml` | Public GitRepository (default branch `main`) |
| `gitops/flux/bootstrap/kustomizations.yaml` | Separate OpenBao, operator and config reconciliation |
| `gitops/flux/bootstrap/rbac.yaml` | Platform and configuration reconciler identities |
| `gitops/flux/openbao/` | Official OpenBao HelmRepository and HelmRelease |
| `gitops/flux/operator/` | Digest-pinned operator OCIRepository and HelmRelease |
| `gitops/config/` | Root and child CRs shared with Argo CD |

Dependency graph: GitRepository → openbao and operator Kustomizations → config Kustomization (`dependsOn`, `wait: true`) with current-generation Ready/Synced health. Chart and image pins live in `hack/lab/gitops/flux.py` and the checked-in YAML.

To use the example in your own deployment, fork the repository and change the GitRepository URL in `gitops/flux/bootstrap/source.yaml`. Prepare the TLS and bootstrap Secrets referenced by `gitops/openbao/values.yaml` first; the runner does this automatically and the YAML contains no credentials. With Flux Operator installed and those Secrets in place, apply the delivery resources without the runner:

```bash
kubectl apply -f gitops/flux/flux-instance.yaml
kubectl -n flux-system wait --for=condition=Ready fluxinstance/flux --timeout=300s
kubectl apply -k gitops/flux/bootstrap
```

The platform reconciler has cluster-admin permission because it installs CRDs, cluster RBAC and server infrastructure. The bootstrap and platform repository are administrator trust boundaries. The configuration reconciler has permissions for the tested operator API groups and ConfigMaps only in `obco-gitops`, plus cluster permission for `BaoServer` resources. It cannot read Kubernetes Secrets or create arbitrary cluster resources. This example is not proof of hostile multi-tenant Git isolation.

The smoke test does not commit to the public repository; Git update, invalid-commit recovery and prune-from-Git are out of scope. `prune: true` deletes Kubernetes objects removed from Git; the operator's `deletionPolicy` controls OpenBao counterparts. To run the same setup step by step on your own cluster, follow [Deliver OpenBao configuration with Flux](../guides/flux.md). Argo CD lab: [Run the Argo CD lab](argocd-lab.md).

## Clean up

Without `KEEP_GITOPS_LAB=1` the runner deletes the cluster on exit. Retained cluster:

```bash
bin/kind delete cluster --name obco-gitops-flux
```
