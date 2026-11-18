---
title: Upgrade OpenBao Config Operator
description: "Upgrade OpenBao Config Operator, its Helm chart and CRDs while reviewing compatibility and breaking changes."
---

# Upgrade

Upgrade the chart and manager together from 1.0.0 or later, then confirm every managed resource is still `Ready`.
1.0.0 is the first release: there is no upgrade from, and no rollback to, the withdrawn 0.x pre-releases.

## Before you begin

- The operator 1.0.0 or later is installed from the Helm chart ([Install](../get-started/install.md)).
- You have the target chart version and its release notes.
- You can run `kubectl` and `helm` against the release Kubernetes namespace (default `openbao-config-operator`).
- `jq`, for the health checks in step 1.

Installed from `install.yaml` instead: apply the new release's `install.yaml` with
`kubectl apply --server-side`, after reading the release notes from your current version up to the target.

## CRD upgrades

The CRDs are chart templates. `helm upgrade` applies the new CRDs together with the new manager,
so the schema and the code always match. CRD changes within `v1alpha1` are additive.

If you install the CRDs separately (`crds.install=false`, see
[Install options](install-options.md#crds)), apply the new `crds.yaml` before you upgrade the
chart:

```bash
kubectl apply --server-side \
  -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/crds.yaml
```

## 1. Upgrade the release

1. Read the release notes from the current version up to the target, for deprecations, changed
   defaults and new required permissions in OpenBao.
2. Verify the new image and chart ([Hardening: verify the release](../security/hardening.md#verify-the-release)).
3. Check that every resource is healthy before you start, so new failures stand out. The command
   lists every resource with a condition that is not `True`, and prints nothing when all are healthy:

   ```bash
   kubectl get obco -A -o json | jq -r '.items[] | select(any(.status.conditions[]?; .status != "True")) | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
   ```

4. Upgrade with your values file:

   ```bash
   helm upgrade openbao-config-operator \
     oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
     --version @VERSION@ --namespace openbao-config-operator --values values.yaml
   ```

   `--reuse-values` keeps the old values of keys whose defaults the new chart changed; use it only
   if you have no values file.

5. Watch the rollout and the resources:

   ```bash
   kubectl rollout status deploy/openbao-config-operator -n openbao-config-operator
   kubectl get obco -A -o json | jq -r '.items[] | select(any(.status.conditions[]?; .status != "True")) | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
   ```

   A short rise in `obco_writes_total` is expected when a release starts sending a field it did
   not send before. A rate that stays up after the first drift interval is not; see
   [the runbook](runbooks.md#openbaoconfigoperatorsteadystatewrites).

## Rolling back

Roll back the manager image, not the CRDs. The earliest release you can roll back to is 1.0.0.

`helm rollback` re-applies every template of the earlier revision, including the earlier CRDs.
`crds.keep` only stops Helm from deleting a CRD; it does not stop a rollback from replacing the
schema. The API server prunes fields that the older schema does not know, so specs that used new
fields lose them.

Instead, keep the new chart and CRDs and pin the earlier image:

```bash
helm upgrade openbao-config-operator \
  oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version <new-version> --namespace openbao-config-operator --values values.yaml \
  --set image.digest=<earlier-digest>
```

`<new-version>` is the chart version you upgraded to, and `<earlier-digest>` is the manager image
digest of the release you roll back to, from its release notes.

The earlier manager ignores fields it does not know. A resource that depends on a new field is
reconciled as if the field were absent, so check those resources after the rollback.

If you must roll back the chart as well, roll back with `crds.install=false` set in the earlier
revision's values, or re-apply the new `crds.yaml` immediately afterwards.

## Hash-key continuity

`status.writeOnlyHash` is keyed by the hash-key Secret (`openbao-config-operator-hash-key` by
default). Upgrades and rollbacks keep the Secret, so they cause no extra writes. Do not delete the
Secret during an upgrade: a new key makes every resource with write-only inputs write once ([Install options](install-options.md#hash-key-secret)).

## API compatibility

Every group is at `v1alpha1`. The product version is 1.0.0 or later, but the API is not yet stable,
and these rules apply until a `v1beta1` is published
([deprecation policy](../reference/compatibility.md#deprecation-policy)):

- New optional fields can be added in any minor release. An older manager ignores fields it does
  not know.
- A field is not removed or renamed in a patch release.
- A field to be removed is deprecated first, and kept for at least one minor release. The release
  notes list deprecations and removals.
- A default can change only in a minor release, and the release notes say so. Defaults that
  decide what happens in OpenBao, such as `creationPolicy` and the per-kind `deletionPolicy`, are
  not changed lightly.
- Validation can become stricter in a minor release when the old rule let through specs that
  OpenBao rejects anyway.

Immutable fields are enforced with CEL rules (`self == oldSelf`), for example `connectionRef`,
`openbaoNamespace`, the mount `type`, `local` and `sealWrap`, and a KV secret's `mountPath` and `path`.
Making a mutable field immutable is a breaking change and follows the deprecation rule. Changing
an immutable field always means deleting and re-creating the resource; check its `deletionPolicy`
first.

### Moving to v1beta1

When a `v1beta1` version is added:

- both versions are served, and `v1alpha1` stays the storage version for at least one minor
  release;
- if the schemas differ, a conversion webhook converts between them; if they are the same, no
  webhook is needed;
- the storage version then moves to `v1beta1`, and `v1alpha1` is deprecated and later no longer
  served. Before `v1alpha1` stops being served, stored objects must be rewritten in the new
  storage version (a storage version migration), or the API server cannot read them.

No such version exists yet.

## Troubleshooting

| symptom | check |
|---------|-------|
| `helm upgrade` rejects values | the error names the value; compare it with the [Helm values](../reference/helm-values.md) of the target release |
| resources stay not `Ready` after rollout | [Troubleshooting](troubleshooting.md), then the matching [runbook](runbooks.md) |
| steady writes after upgrade | [OpenBaoConfigOperatorSteadyStateWrites](runbooks.md#openbaoconfigoperatorsteadystatewrites) |

## Related

- [Helm values](../reference/helm-values.md)
- [Install](../get-started/install.md)
- [Hardening](../security/hardening.md)
- [OpenBao policy for the operator](openbao-policy.md)
