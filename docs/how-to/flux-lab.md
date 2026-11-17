---
title: Test OpenBao GitOps with Flux
description: Deploy OpenBao and its configuration operator through Flux, then verify Git updates, drift correction and deletion behaviour against the live API.
---

# Test OpenBao GitOps with Flux

This disposable kind profile installs Flux Operator and a FluxInstance, then lets Flux deploy
OpenBao, the operator chart from the current checkout, and root and child namespace configuration.
It uses a real in-cluster Git HTTP server. No GitHub account or repository credential is required.

## Run the regression

From the repository root, with Docker running:

```sh
make lab-flux
```

The runner builds the operator image from the checkout and loads it into the fresh kind cluster.
It generates TLS and bootstrap credentials outside Git. OpenBao and the configuration operator
are deployed by Flux; neither is installed imperatively before the test.

The cluster is removed on exit. To retain a failed run for inspection:

```sh
KEEP_GITOPS_LAB=1 make lab-flux
```

The fixture uses `kind-obco-gitops-flux`. Do not reuse that context for other workloads.
Delete a retained cluster with `bin/kind delete cluster --name obco-gitops-flux`.

## Follow the reconciliation order

The generated Git repository separates infrastructure from configuration:

```text
FluxInstance → GitRepository → root Kustomization
                                 ├─ openbao Kustomization → TLS Raft server
                                 ├─ operator Kustomization → HelmRelease → operator and CRDs
                                 └─ config Kustomization (depends on both)
                                      → connection, root and child configuration
```

The bootstrap uses this FluxInstance shape; the fixture supplies its internal Git URL and pins
the distribution to `2.10.0`:

```yaml
apiVersion: fluxcd.controlplane.io/v1
kind: FluxInstance
metadata:
  name: flux
  namespace: flux-system
spec:
  distribution:
    version: "2.10.0"
    registry: ghcr.io/fluxcd
  components: [source-controller, kustomize-controller, helm-controller]
  cluster:
    type: kubernetes
    size: small
    networkPolicy: true
  sync:
    kind: GitRepository
    url: http://git.obco-gitops.svc:8080/repo.git
    ref: refs/heads/main
    path: ./flux/root
    interval: 10s
```

The internal HTTP source is only for this disposable lab. Use authenticated HTTPS or SSH for a
shared repository. The Flux Operator chart is pinned to `0.62.1` and its verified OCI digest; the operator image
is pinned by digest too. Both pins are recorded in
[hack/lab/gitops/flux.py](https://github.com/alexandrust88/openbao-config-operator/blob/main/hack/lab/gitops/flux.py).

The operator HelmRelease reads the chart from the same Git revision under `operator/`:

```yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: openbao-config-operator
  namespace: flux-system
spec:
  interval: 10s
  releaseName: openbao-config-operator
  targetNamespace: openbao-config-operator-system
  serviceAccountName: platform-reconciler
  chart:
    spec:
      chart: ./operator
      sourceRef:
        kind: GitRepository
        name: flux-system
      reconcileStrategy: Revision
      interval: 10s
  install:
    createNamespace: true
    strategy:
      name: RetryOnFailure
      retryInterval: 10s
  upgrade:
    strategy:
      name: RetryOnFailure
      retryInterval: 10s
  driftDetection:
    mode: enabled
```

The runner adds image values identifying the locally built image and clears `image.digest`, so
an existing chart image pin cannot override the tested build. `reconcileStrategy: Revision`
ensures a chart change is consumed even when its chart version is unchanged.

The configuration dependency gate is a separate Kustomization:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: config
  namespace: flux-system
spec:
  interval: 10s
  retryInterval: 5s
  sourceRef:
    kind: GitRepository
    name: flux-system
  path: ./config
  serviceAccountName: config-reconciler
  dependsOn:
    - name: openbao
    - name: operator
  prune: true
  wait: true
  timeout: 5m
  healthCheckExprs:
    - apiVersion: sys.openbaoconfig.io/v1alpha1
      kind: Policy
      current: >-
        has(status.conditions) &&
        status.conditions.exists(c, c.type == 'Ready' && c.status == 'True' &&
          c.observedGeneration == metadata.generation) &&
        status.conditions.exists(c, c.type == 'Synced' && c.status == 'True' &&
          c.observedGeneration == metadata.generation)
```

The executable fixture adds equivalent checks for every managed kind and Ready-only checks for
OpenBaoServer and OpenBaoConnection. It also recognises a current-generation reconciliation error
as failed health. A stale Ready condition does not satisfy the gate.

## Interpret the evidence

The regression waits for the exact Git revision and healthy Flux resources, then reads the live
OpenBao API to verify root and child namespace policies, mounts, auth roles and transit keys.
It tests Git policy updates, Kubernetes and OpenBao drift repair, failed configuration health and
recovery, and removal of two policies with different deletion policies.

Flux `prune: true` deletes the Kubernetes object. The operator's `deletionPolicy` independently
determines whether its OpenBao object is removed: `Delete` removes the test policy; `Orphan`
retains it. Flux inventory alone does not prove either OpenBao outcome.

## Understand the trust boundary

The platform reconciler has cluster-admin permission because it installs the operator's CRDs,
cluster RBAC and server resources. That account and the Flux bootstrap are platform-admin
boundaries; this fixture does not prove hostile multi-tenant Git isolation.

The configuration reconciler has namespaced permissions only for the tested operator API groups
and ConfigMaps in `obco-gitops`, plus cluster permission for OpenBaoServer resources. It cannot
read Kubernetes Secrets or create arbitrary cluster resources. Runtime credentials stay in
Kubernetes Secrets and are never committed to the Git fixture.

For production patterns, see [the Flux guide](../guides/flux.md), the official
[Flux Operator installation guide](https://fluxoperator.dev/docs/installation/),
[Kustomization health checks](https://fluxcd.io/flux/components/kustomize/kustomizations/#health-check-expressions)
and [HelmRelease chart reconciliation](https://fluxcd.io/flux/components/helm/helmreleases/#chart-template).
