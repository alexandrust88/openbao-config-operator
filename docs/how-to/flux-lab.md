---
title: Deploy OpenBao and its configuration with Flux
description: Use checked-in Flux resources, public chart releases and a public Git repository to deploy OpenBao and declarative root and tenant configuration.
---

# Deploy OpenBao and its configuration with Flux

The [Flux examples](https://github.com/alexandrust88/openbao-config-operator/tree/main/gitops/flux)
are ordinary checked-in Kubernetes manifests. They use this public Git repository, the official
OpenBao Helm repository and the published OpenBao Config Operator OCI chart. They need no custom
Git server, generated delivery repository, Git write credentials or locally built operator image.

Use these examples in a disposable cluster. The OpenBao values configure a small TLS-enabled
Raft deployment with self-initialisation, not a production availability or backup policy.

## Run the documented smoke test

From the repository root, with Docker running:

```sh
make lab-flux
```

The runner creates a fresh kind cluster, prepares random bootstrap credentials and TLS Secrets
outside Git, installs Flux Operator, and applies the checked-in Flux bootstrap resources.
Flux deploys both OpenBao and the published configuration operator chart. The public GitRepository
follows `main` by default.

To test a commit already pushed to the public repository:

```sh
GITOPS_REVISION="$(git rev-parse HEAD)" make lab-flux
```

The runner replaces the source's branch reference with that exact commit; it never updates the
public repository. CI supplies the pull request's public repository URL and exact head SHA. Local
uncommitted changes to the Git-managed manifests are therefore not part of this smoke test.
The published operator image is tested, not the manager binary in the checkout.

The cluster is removed on exit. For investigation, set `KEEP_GITOPS_LAB=1`; remove the retained
cluster with `bin/kind delete cluster --name obco-gitops-flux` when finished.

## Use the checked-in manifests

The reusable files are under `gitops/flux/`:

| Path | Purpose |
| --- | --- |
| `flux-instance.yaml` | Flux distribution and controllers, managed by Flux Operator |
| `bootstrap/source.yaml` | Public GitRepository, default branch `main` |
| `bootstrap/kustomizations.yaml` | Separate OpenBao, operator and configuration reconciliation |
| `bootstrap/rbac.yaml` | Platform and configuration reconciliation identities |
| `openbao/` | Official OpenBao HelmRepository and versioned HelmRelease |
| `operator/` | Digest-pinned published operator OCIRepository and HelmRelease with example resource values |
| `../openbao/values.yaml` | Shared OpenBao chart values; no secret values |
| `../config/` | Root and child namespace CRs consumed by either Flux or Argo CD |

The example GitRepository is directly editable:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: openbao-config-operator
  namespace: flux-system
spec:
  interval: 1m
  url: https://github.com/alexandrust88/openbao-config-operator
  ref:
    branch: main
```

For your own deployment, fork the repository and change this URL. Prepare the TLS and bootstrap
Secrets referenced by the shared OpenBao values before applying the infrastructure. The smoke
runner does this automatically; the YAML does not contain credentials.

Once Flux Operator is installed and those prerequisites exist, the same delivery resources can
be applied without the Python smoke harness:

```sh
kubectl apply -f gitops/flux/flux-instance.yaml
kubectl -n flux-system wait --for=condition=Ready fluxinstance/flux --timeout=300s
kubectl apply -k gitops/flux/bootstrap
```

Flux Operator installation itself is pinned in `hack/lab/gitops/flux.py` by chart digest and image
digest. The static FluxInstance pins the controller distribution to `2.10.0`; that distribution
renders its controller images with digests.

## Follow the dependency graph

```text
Public GitRepository
  ├─ openbao Kustomization → official OpenBao chart + shared values
  ├─ operator Kustomization → published operator OCI chart
  └─ config Kustomization, after both dependencies are healthy
       → OpenBaoServer, connection, root and child namespace CRs
```

The operator HelmRelease uses an OCI chart source instead of a chart path inside Git:

```yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: openbao-config-operator
  namespace: flux-system
spec:
  interval: 1m
  targetNamespace: openbao-config-operator-system
  serviceAccountName: platform-reconciler
  chartRef:
    kind: OCIRepository
    name: operator-chart
  install:
    createNamespace: true
    strategy:
      name: RetryOnFailure
      retryInterval: 30s
  driftDetection:
    mode: enabled
```

The OCIRepository pins the published `0.4.0` chart by digest. The HelmRelease retains its release
image pin. OpenBao uses official chart `0.30.7`; its `valuesFrom` reads the ConfigMap generated
from `gitops/openbao/values.yaml`.

The config Kustomization declares `dependsOn` for both infrastructure layers and `wait: true`.
Its custom health expressions require current-generation `Ready=True` and `Synced=True` for
managed configuration; OpenBaoServer and OpenBaoConnection require current-generation Ready.
A current-generation `Synced=False` reconciliation error is unhealthy even if an older Ready
condition remains True. The executable CEL tests evaluate those cases against the checked-in YAML.

## Understand what the smoke test proves

The test requires a healthy source and the selected Git revision at each Kustomization. It checks
root and child namespace objects through the real OpenBao API, AppRole permissions, transit
round trips, Kubernetes drift repair by Flux, and OpenBao drift repair by the configuration
operator.

It does not commit changes to the public repository. Git update, invalid-commit recovery and
Git removal/prune are not automated by this credential-free smoke test. To exercise those
workflows, use your own fork and commit changes there.

`prune: true` controls deletion of Kubernetes resources removed from Git. The operator's separate
`deletionPolicy` controls their OpenBao counterparts: `Delete` removes a supported remote object;
`Orphan` retains it. The checked-in fixtures illustrate both choices without claiming the smoke
test performs a public Git deletion.

## Review the trust boundary

The platform reconciler has cluster-admin permission because it installs CRDs, cluster RBAC and
server infrastructure. The bootstrap and platform repository are administrator trust boundaries.
The configuration reconciler has permissions for the tested operator API groups and ConfigMaps
only in `obco-gitops`, plus cluster permission for OpenBaoServer resources. It cannot read
Kubernetes Secrets or create arbitrary cluster resources. This example is not proof of hostile
multi-tenant Git isolation.

See the [Flux guide](../guides/flux.md), official
[Flux Operator installation guide](https://fluxoperator.dev/docs/installation/),
[Kustomization health expressions](https://fluxcd.io/flux/components/kustomize/kustomizations/#health-check-expressions)
and [HelmRelease chart references](https://fluxcd.io/flux/components/helm/helmreleases/#chart-reference).
