# Adopt existing OpenBao configuration

This page brings objects that already exist in OpenBao under openbao-config-operator: objects created by
hand, by scripts or by another configuration tool. The objects in OpenBao stay where they are; only the
manager changes.

## Approach

Observe first, hand each object over, then retire the old manager. At no point should two tools write the
same object.

1. Install openbao-config-operator ([Install](../get-started/install.md)). Create the connections and their OpenBao
   identities ([Connect to OpenBao](../get-started/connect.md), [OpenBao policy for the operator](../operate/openbao-policy.md)).
2. Stop the previous manager from writing or deleting the objects you hand over. If it deletes an OpenBao
   object when its own record of that object is removed, switch that off first, so that retiring it in
   step 7 leaves the object in place.
3. Generate the resources with [`obc-import`](import.md), or write them by hand. Each resource needs
   `managementPolicy: Observe`, `creationPolicy: Adopt` and `deletionPolicy: Orphan`; `obc-import` sets the
   first two. `Adopt` reports `NotFound` instead of creating an object when a name or path is wrong.
4. Apply the resources and read their `Synced` condition. `Observed` means OpenBao matches the spec;
   `Drifted` names the keys that differ. Fix the spec until every resource is `Observed`:

   ```sh
   kubectl get openbao -n team-a
   kubectl describe policy.sys.openbaoconfig.io app-read -n team-a
   ```

5. Switch resources to `managementPolicy: Manage`, one namespace or one kind at a time. Check that each
   resource is `Ready` and that its events show `Adopted`, not `Created`:

   ```sh
   kubectl get events -n team-a --field-selector reason=Adopted
   ```

   An `Updated` event names the keys that differed from the spec. Fix the spec if the difference was not
   intended.
6. Create the Secrets for write-only fields before you switch a resource that has them. OpenBao never
   returns these values, so `Observe` cannot compare them, and the first write in `Manage` sends whatever
   the Secret holds ([Import](import.md#what-you-must-add)).
7. Retire the previous manager's records. With step 2 done, the OpenBao objects stay.
8. Once the operator is the only manager, set `deletionPolicy: Delete` where you want deleting the
   resource to remove the object; leave `Orphan` everywhere else.

A field the spec leaves unset is not compared, so an adopted object keeps its other settings until the spec
names them.

## Fields to set

| topic | openbao-config-operator |
|-------|-------------------------|
| connection and login | `spec.connectionRef: {kind, name}` to an `OpenBaoConnection` (same namespace) or `ClusterOpenBaoConnection` |
| OpenBao namespace | the connection's `namespace` plus the resource's relative `spec.namespace` (immutable) |
| mount path | `spec.mountPath` (auth kinds without the `auth/` prefix) |
| object name | `spec.name`, defaulting to `metadata.name` |
| fields | camelCase OpenBao keys under `spec`, such as `boundServiceAccountNames` and `tokenPolicies` |
| secret inputs | `<field>SecretRef: {name, key}` in the resource's namespace; the value is hashed into `status.writeOnlyHash`, never compared |
| generated outputs | `writeSecretRef: {name}`, only for values OpenBao returns once; no general secret sync ([ADR 0007](../adr/0007-no-secret-sync.md)) |
| existing objects | `creationPolicy`: `Create` (default), `Adopt` or `CreateOrAdopt` |
| deletion | `deletionPolicy`: `Orphan` (default) or `Delete`; destructive kinds (mounts, namespaces, KV data, audit devices, key material, MFA methods, entities and `OpenBaoEndpoint`) are deleted only with the annotation `openbaoconfig.io/allow-destructive-delete: "true"` ([backup and restore](../operate/backup-restore.md#deletion-policy-and-orphaned-objects)) |
| drift | compared key by key; written only on a difference; `driftDetection: Disabled` stops re-reads |

Check each field against the CRD before you write the resource:

```sh
kubectl explain kubernetesauthroles.spec --api-version=auth.openbaoconfig.io/v1alpha1
```

## Which kind

The [kind catalogue](../kinds.md) lists every typed kind with its OpenBao path. Manage a path without a
typed kind with `OpenBaoEndpoint` ([ADR 0006](../adr/0006-endpoint-escape-hatch.md)). Features with no
typed kind, and what to use instead, are under [not typed](../kinds.md#not-typed).
