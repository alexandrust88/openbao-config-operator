---
description: Apply OpenBao configuration with Flux and wait for current-generation operator readiness before deploying dependent workloads.
---

# Reconcile OpenBao configuration with Flux

Use Flux to deliver custom resources and the operator to reconcile their OpenBao objects.
This example waits for a policy's current-generation `Ready` and `Synced` conditions before
allowing a dependent Flux Kustomization to proceed.

## Prerequisites

- Flux is installed, with a `GitRepository` named `platform` in `flux-system` pointing at your
  configuration repository. Use your existing Flux installation or the
  [Flux Operator](https://fluxoperator.dev/docs/) to manage one.
- A Flux Kustomization named `openbao-platform` installs the operator and its CRDs, and provisions
  the server, connection and input Secrets. Its own health checks must wait for these resources.
- A Kubernetes namespace `team-a` and a ready `OpenBaoConnection` named `openbao` exist there.
  Its OpenBao identity can manage the example policy within the tenant's namespace.
- Your kustomize-controller supports `spec.healthCheckExprs`.

Replace these repository, namespace and connection names with your deployment's values.
Keep credentials in your existing encrypted-secret or secret-delivery workflow, not plain Git YAML.

## Declare the policy

Put this file at `clusters/production/openbao/team-a/policy.yaml` in the configuration repository:

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: app-reader
  namespace: team-a
spec:
  connectionRef:
    kind: OpenBaoConnection
    name: openbao
  creationPolicy: Create
  deletionPolicy: Orphan
  name: app-reader
  policy: |
    path "kv/data/app/*" {
      capabilities = ["read"]
    }
```

The path is relative to the connection's OpenBao namespace. It does not create the KV mount or
assign the policy to users. Follow the [access model](access-model.md) to bind permissions.
`Create` refuses an unrelated existing policy; use the [migration guide](migration.md) for adoption.

## Wait for reconciliation

Add this Flux resource to the repository path your Flux installation already reconciles:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: team-a-openbao
  namespace: flux-system
spec:
  interval: 5m
  timeout: 5m
  path: ./clusters/production/openbao/team-a
  sourceRef:
    kind: GitRepository
    name: platform
  dependsOn:
    - name: openbao-platform
  prune: true
  healthChecks:
    - apiVersion: sys.openbaoconfig.io/v1alpha1
      kind: Policy
      name: app-reader
      namespace: team-a
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

`exists` requires both conditions to be present; an empty list cannot pass. A missing `status`
can produce a CEL evaluation error while the controller has not reconciled the object; it does
not count as healthy and Flux waits until the timeout. Old conditions cannot approve a new spec.
No `failed` expression is used, so transient connection failures can recover within the timeout.
See [Flux health-check expressions](https://fluxcd.io/flux/components/kustomize/kustomizations/#health-check-expressions).

For additional kinds, add their actual API group, kind and object name to `healthChecks` and a
corresponding expression. The [status reference](../reference/status-and-events.md) describes
managed-kind conditions; do not assume the same contract for arbitrary third-party resources.

A workload Kustomization can then declare:

```yaml
# Add to its existing spec; keep its sourceRef, path and other settings.
dependsOn:
  - name: team-a-openbao
```

Inspect delivery and OpenBao reconciliation separately:

```sh
flux get kustomizations -n flux-system
kubectl -n team-a get policies.sys.openbaoconfig.io app-reader -o yaml
```

## Deletion and operational limits

Flux `prune: true` removes Kubernetes objects absent from Git. The example's `deletionPolicy:
Orphan` leaves the OpenBao policy behind. Opting into `Delete` changes that behaviour; destructive
kinds also require the operator's deletion annotation. See [deletion and orphaned objects](../operate/backup-restore.md#deletion-policy-and-orphaned-objects).

Readiness proves that the operator reconciled the declared configuration. It does not prove a
workload can log in, that every ACL is correct, or that a deleted Kubernetes Secret remains
available. Include application login and allowed/denied access tests in your deployment checks.
