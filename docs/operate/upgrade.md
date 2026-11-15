# Upgrade

This page covers the compatibility rules of the API and the chart, and how to upgrade and roll
back the operator.

## API compatibility

Every group is at `v1alpha1`. The project is pre-1.0, and these rules apply until a `v1beta1` is
published:

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
`namespace`, the mount `type`, `local` and `sealWrap`, and a KV secret's `mountPath` and `path`.
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

## CRD upgrades

The CRDs are chart templates. `helm upgrade` applies the new CRDs together with the new manager,
so the schema and the code always match. CRD changes within `v1alpha1` are additive.

If you install the CRDs separately (`crds.install=false`, see
[install.md](../get-started/install.md#crds)), apply the new `crds.yaml` before you upgrade the
chart:

```sh
kubectl apply --server-side -f crds.yaml
```

## Upgrade procedure

1. Read the release notes from the current version up to the target, for deprecations, changed
   defaults and new required permissions in OpenBao.
2. Verify the new image and chart ([security.md](../security.md#verifying-releases)).
3. Check that every resource is healthy before you start, so new failures stand out:

   ```sh
   kubectl get openbao -A | grep -v True
   ```

4. Upgrade:

   ```sh
   helm upgrade openbao-config-operator \
     oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
     --version <version> --namespace openbao-config-operator --reuse-values
   ```

   Prefer an explicit values file over `--reuse-values` in GitOps; `--reuse-values` keeps old
   defaults of keys the new chart changed.

5. Watch the rollout and the resources:

   ```sh
   kubectl rollout status deploy/openbao-config-operator -n openbao-config-operator
   kubectl get openbao -A | grep -v True
   ```

   A short rise in `openbao_writes_total` is expected when a release starts sending a field it did
   not send before. A rate that stays up after the first drift interval is not; see
   [the runbook](runbooks.md#openbaoconfigoperatorsteadystatewrites).

## Upgrading to 0.2.0

### Breaking change: more destructive kinds

`TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`, `MFAMethod` and `Entity` are now destructive kinds
([ADR 0013](../adr/0013-destructive-delete-guard.md)). Deleting their OpenBao objects destroys key material or
enrolled MFA secrets, so a resource of these kinds with `deletionPolicy: Delete` now deletes its object only while
it carries the annotation `openbaoconfig.io/allow-destructive-delete: "true"`. Without the annotation:

- in a live Kubernetes Namespace, the resource stays `Terminating` with reason `DeleteGuarded`;
- in a terminating Kubernetes Namespace, the operator leaves the object in OpenBao, records a Warning event with
  reason `Orphaned` and removes the finalizer.

Resources with the default `deletionPolicy: Orphan` are not affected.

Before you upgrade, list the affected resources. Each command prints the kind, namespace and name of every
resource of one kind that has `deletionPolicy: Delete` and lacks the annotation:

```sh
kubectl get transitkeys.secrets.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
kubectl get pkiissuers.secrets.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
kubectl get pkikeys.secrets.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
kubectl get sshcas.secrets.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
kubectl get totpkeys.secrets.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
kubectl get mfamethods.identity.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
kubectl get entities.identity.openbaoconfig.io -A -o json | jq -r '.items[] | select(.spec.deletionPolicy == "Delete" and .metadata.annotations["openbaoconfig.io/allow-destructive-delete"] != "true") | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
```

For each resource listed, choose one of two ways out:

1. If deleting the resource must still destroy the OpenBao object, add the annotation:

   ```sh
   kubectl annotate transitkey <name> -n <namespace> openbaoconfig.io/allow-destructive-delete=true
   ```

2. If the OpenBao object should stay when the resource is deleted, switch to `Orphan`:

   ```sh
   kubectl patch transitkey <name> -n <namespace> --type=merge -p '{"spec":{"deletionPolicy":"Orphan"}}'
   ```

Replace `transitkey` with the kind that the command listed. In GitOps, make the change in Git, or the next sync
reverts it.

### Other behaviour changes

- A change to only the labels or annotations of a connection's credential Secret no longer makes the operator log
  in again. A new login follows a change of the credential value.
- Mount drift checks share one read of the mount table per connection identity for up to 2 seconds. An
  out-of-band change to a mount can show up to 2 seconds later than before.

## Rolling back

Roll back the manager image, not the CRDs.

`helm rollback` re-applies every template of the earlier revision, including the earlier CRDs.
`crds.keep` only stops Helm from deleting a CRD; it does not stop a rollback from replacing the
schema. The API server prunes fields that the older schema does not know, so specs that used new
fields lose them.

Instead, keep the new chart and CRDs and pin the earlier image:

```sh
helm upgrade openbao-config-operator \
  oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version <new version> --namespace openbao-config-operator --reuse-values \
  --set image.digest=<digest of the earlier release>
```

The earlier manager ignores fields it does not know. A resource that depends on a new field is
reconciled as if the field were absent, so check those resources after the rollback.

If you must roll back the chart as well, roll back with `crds.install=false` set in the earlier
revision's values, or re-apply the new `crds.yaml` immediately afterwards.

## Hash-key continuity

`status.writeOnlyHash` is keyed by the hash-key Secret (`openbao-config-operator-hash-key` by
default). Upgrades and rollbacks keep the Secret, so they cause no extra writes. Do not delete the
Secret or change `--hash-key-secret` during an upgrade: a new key makes every resource with
write-only inputs write once ([install.md](../get-started/install.md#hash-key-secret)).
