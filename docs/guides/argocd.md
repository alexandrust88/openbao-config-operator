# Argo CD health check

Argo CD has no built-in health check for these kinds, so it reports them `Healthy` as soon as they
exist. [argocd-cm-health.yaml](argocd-cm-health.yaml) adds one Lua check for every kind in every
`*.openbaoconfig.io` group. With it, sync waves wait until an object is really in OpenBao and an
application turns `Degraded` when OpenBao refuses a change.

## Install

Merge the `data` key of [argocd-cm-health.yaml](argocd-cm-health.yaml) into the `argocd-cm`
ConfigMap, or into `configs.cm` of the Argo CD Helm chart:

```yaml
configs:
  cm:
    resource.customizations.health.*.openbaoconfig.io_*: |
      <the Lua script from argocd-cm-health.yaml>
```

Argo CD 2.6 and later match the wildcard group and kind in the key. No restart is needed.

## Mapping

The check reads `Ready`, `Synced` and `observedGeneration` (the top-level one, or the one on the
`Ready` condition).

| status | when |
|--------|------|
| Progressing | no status yet, or `observedGeneration` is older than `metadata.generation` |
| Suspended | `Ready` or `Synced` has the reason `Paused` (annotation `openbaoconfig.io/paused`) |
| Healthy | `Ready=True`, and `Synced` is `True`, absent (connections) or waiting (see below) |
| Degraded | `Ready=True` but `Synced=False` with a reason that is not waiting, for example `ReconcileError`, `PermissionDenied` or `Drifted` |
| Degraded | `Ready=False` with a reason that is not waiting, for example `Conflict`, `InvalidSpec`, `AuthenticationFailed` |
| Progressing | `Ready=False` with a waiting reason, or `Ready=Unknown` |

Waiting reasons are the ones that resolve without a change to the object: `ReconcileInProgress`,
`ReferenceNotReady`, `ConnectionNotReady`, `PluginNotRegistered`, `ServerNotReady` and
`WriteBudgetExceeded`. A `Ready=True` object whose connection is down stays `Healthy`: what is in
OpenBao still matches the last check.
