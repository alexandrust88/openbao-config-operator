---
title: Track OpenBao configuration health in Argo CD
description: Add an Argo CD health check that waits for OpenBao custom resources to reconcile and reports configuration failures without changing deletion behaviour.
---

# Track OpenBao configuration health in Argo CD

Install a custom health check so Argo CD evaluates the operator's reconciliation status, rather
than relying on the presence of its custom resources. The check covers every
`*.openbaoconfig.io` group and reports configuration errors in the Application resource tree.

## Before you begin

- Argo CD manages an Application containing the operator's custom resources.
- The operator is installed and has a [Ready connection](../get-started/connect.md).
- You can update the configuration source that owns Argo CD's `argocd-cm` ConfigMap: its Helm
  values, Git manifest or operator configuration. Preserve unrelated settings and health checks.
- You have the `argocd` CLI configured for that installation, or access to its UI.

## Install the health check

The complete configuration is in [argocd-cm-health.yaml](argocd-cm-health.yaml):

```yaml title="docs/guides/argocd-cm-health.yaml"
# Merge "*.openbaoconfig.io/*" into the existing resource.customizations value.
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  resource.customizations: |
    "*.openbaoconfig.io/*":
      health.lua: |
        local hs = {status = "Progressing", message = "Waiting for the operator to reconcile"}
        if obj.status == nil or obj.status.conditions == nil then
          return hs
        end
        local generation = obj.metadata.generation
        local observed = obj.status.observedGeneration
        local ready, synced
        for _, c in ipairs(obj.status.conditions) do
          if c.type == "Ready" then
            ready = c
          elseif c.type == "Synced" then
            synced = c
          end
          if observed == nil and c.type == "Ready" then
            observed = c.observedGeneration
          end
        end
        local waiting = {
          ReconcileInProgress = true,
          ReferenceNotReady = true,
          ConnectionNotReady = true,
          PluginNotRegistered = true,
          ServerNotReady = true,
          WriteBudgetExceeded = true,
        }
        if generation ~= nil and synced ~= nil and synced.observedGeneration == generation and synced.status == "False"
          and synced.reason ~= "Paused" and not waiting[synced.reason] then
          hs.status = "Degraded"
          hs.message = (synced.reason or "") .. ": " .. (synced.message or "")
          return hs
        end
        if generation ~= nil and ((observed == nil or observed < generation)
          or (ready ~= nil and ready.observedGeneration ~= nil and ready.observedGeneration < generation)
          or (synced ~= nil and synced.observedGeneration ~= nil and synced.observedGeneration < generation)) then
          hs.message = "Waiting for the operator to observe generation " .. generation
          return hs
        end
        for _, c in pairs({ready = ready, synced = synced}) do
          if c.reason == "Paused" then
            hs.status = "Suspended"
            hs.message = c.message or "Paused"
            return hs
          end
        end
        if ready == nil then
          return hs
        end
        local message = ready.message or ""
        if ready.status == "True" then
          if synced ~= nil and synced.status == "False" then
            if not waiting[synced.reason] then
              hs.status = "Degraded"
            end
            hs.message = (synced.reason or "") .. ": " .. (synced.message or "")
            return hs
          end
          hs.status = "Healthy"
          hs.message = message
          return hs
        end
        if ready.status == "False" and not waiting[ready.reason] then
          hs.status = "Degraded"
          hs.message = (ready.reason or "") .. ": " .. message
          return hs
        end
        hs.message = (ready.reason or "") .. ": " .. message
        return hs
```

Merge the `"*.openbaoconfig.io/*"` entry into the YAML string stored under
`data.resource.customizations` of your existing `argocd-cm`. If Argo CD is installed with Helm,
put that same string under `configs.cm.resource.customizations`. Keep any existing customizations
in the string, and use your installation's namespace instead of `argocd` where needed.

Wildcards belong inside `resource.customizations`; they cannot appear in a ConfigMap data key
such as `resource.customizations.health.*.openbaoconfig.io_*`. This is the format documented by
[Argo CD's custom health-check guide](https://argo-cd.readthedocs.io/en/stable/operator-manual/health/#way-1-define-a-custom-health-check-in-argocd-cm-configmap).

Commit the configuration change and deploy it through the mechanism that manages Argo CD.
The customization changes health reporting; it does not install the operator or grant access to OpenBao.

## Verify the result

Sync an Application that contains a [Policy](policies.md), then refresh its resource tree. Replace
`openbao-config` below with your Application name:

```bash
argocd app get openbao-config --refresh
argocd app wait openbao-config --sync --health --timeout 180
```

While the operator has not observed the current generation, expect `Progressing`. A successfully
reconciled policy becomes `Healthy`. Compare the resource's Argo CD health with its Kubernetes
conditions:

```bash
kubectl -n openbao-config describe policy guide-app-read
```

When you use sync waves, this health check lets the wave wait for reconciliation. References such
as `policyRefs` still express dependencies within the operator; a health check does not replace them.

## Mapping

The check reads `Ready`, `Synced` and `observedGeneration` (the top-level one, or the one on the
`Ready` condition).

| status | when |
|--------|------|
| Degraded | A current-generation `Synced=False` reports a terminal error, even if `Ready` still describes the previous successful generation |
| Progressing | no status yet, no observed generation, or the observed/Ready generation is older than `metadata.generation`, without a current terminal sync error |
| Suspended | `Ready` or `Synced` has the reason `Paused` (annotation `openbaoconfig.io/paused`) |
| Healthy | `Ready=True` for the current generation, and `Synced` is `True` or absent (connections/server) |
| Progressing | `Ready=True` but `Synced=False` reports a waiting reason |
| Degraded | `Ready=True` but `Synced=False` with a reason that is not waiting, for example `ReconcileError`, `PermissionDenied` or `Drifted` |
| Degraded | `Ready=False` with a reason that is not waiting, for example `Conflict`, `InvalidSpec`, `AuthenticationFailed` |
| Progressing | `Ready=False` with a waiting reason, or `Ready=Unknown` |

A failed update can retain `Ready=True` from the last successful generation while setting
`Synced=False` for the new generation. The health check examines that current sync failure before
waiting for Ready to advance; otherwise an invalid Git change would remain `Progressing` forever.
An error from an older generation does not override pending reconciliation of a newer spec.

Waiting reasons are the ones that resolve without a change to the object: `ReconcileInProgress`,
`ReferenceNotReady`, `ConnectionNotReady`, `PluginNotRegistered`, `ServerNotReady` and
`WriteBudgetExceeded`. A `Ready=True` object whose connection is down reports `Progressing` while `Synced=False`
records that waiting state. A dependency gate must not advance while reconciliation is known to be pending.


## Troubleshooting

| Symptom | Check |
| --- | --- |
| Kubernetes rejects the ConfigMap key | Use `resource.customizations` with the quoted wildcard inside its YAML value |
| The resource has no custom health result | Confirm the entry reached the live `argocd-cm` and was not overwritten by its Helm release or managing operator |
| The resource remains `Progressing` | Inspect `observedGeneration`, connection readiness and referenced resources in Kubernetes |
| The Application is `Degraded` | Read the affected resource's conditions and events; fix its OpenBao permissions or spec rather than suppressing the health result |
| Argo CD is `Synced` but configuration is not ready | Sync describes the Kubernetes manifest; health describes the operator's reported reconciliation result |

See [status and events](../reference/status-and-events.md) for condition meanings and
[operator troubleshooting](../operate/troubleshooting.md) for reconciliation failures.

## Remove the customization

Remove only the `"*.openbaoconfig.io/*"` entry from the configuration source that manages Argo CD,
then deploy that change. Keep other customizations. This removes the custom health check; it does
not delete Kubernetes resources or OpenBao configuration.

Before enabling GitOps pruning, review each resource's `deletionPolicy` and the
[destructive-delete safeguards](../operate/backup-restore.md#deletion-policy-and-orphaned-objects).
Continue with [importing existing configuration](import.md) or [tenant onboarding](../tutorials/tenant-onboarding.md).
