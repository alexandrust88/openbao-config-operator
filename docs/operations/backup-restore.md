# Backup and restore

The custom resources in Git are the source of truth for OpenBao configuration. The operator
rebuilds configuration from them. It cannot rebuild data, generated values or server-side IDs,
so a complete recovery needs both the resources and an OpenBao snapshot.

## What lives where

| state | lives in | recovered by |
|-------|----------|--------------|
| desired configuration (policies, roles, mounts, auth config, identities) | custom resources in Git | re-applying the resources |
| secret inputs (passwords, JWTs, client secrets) | Kubernetes Secrets referenced by `<field>SecretRef` | your Secret source (ESO, sealed secrets, Velero) |
| one-time outputs: AppRole secret IDs, OIDC client IDs and secrets, exported PKI private keys, `RandomSecret` copies | Kubernetes Secrets written through `writeSecretRef`, owned by the resource | Velero, or generating new ones |
| server-generated IDs and accessors (entities, groups, aliases, mounts) | OpenBao, copied into `status.id` and `status` fields | an OpenBao snapshot; recreating gives new IDs |
| data: KV values not set by a spec, `RandomSecret` values, transit, PKI and SSH key material, database root passwords after `rotate-root`, tokens and leases | OpenBao only | an OpenBao snapshot only |
| ownership and progress: `status.acquired`, `status.path`, `status.namespace`, `status.writeOnlyHash` | resource status | not needed; see [status is not restored](#status-is-not-restored) |
| members a `GroupMembership` added, children of an `AccessGrant` or `AccessLayout` | resource status | lost with the status; see below |
| the key of `status.writeOnlyHash` | the hash-key Secret | Velero or a manual copy |

## What to back up

1. The custom resources: Git, or `kubectl get openbao -A -o yaml` for a point-in-time copy.
2. The Secrets the resources and connections reference.
3. The output Secrets written through `writeSecretRef`.
4. The hash-key Secret, `openbao-config-operator-hash-key` in the operator namespace.
5. OpenBao itself, with its own snapshot tooling (`bao operator raft snapshot save` for integrated
   storage).

## The hash-key Secret

The Secret holds the HMAC key of `status.writeOnlyHash`. Losing it costs one write: each resource
with write-only inputs finds that its recorded hash no longer matches and writes once. It matters
only when the resources and their status survive, for example when the operator namespace is
deleted and recreated. When the status is gone as well, every such resource writes once anyway.

```sh
kubectl get secret openbao-config-operator-hash-key -n openbao-config-operator -o yaml > hash-key.yaml
```

Restore it before the manager starts. The manager creates a new key only when the Secret is
absent.

## Restore scenarios

### OpenBao restored from a snapshot, resources intact

Restore the snapshot, unseal, and wait for the connections to become `Ready`. The resources keep
their status. On the next reconcile each resource reads its object and:

- writes it again if the object differs from the spec (changes made after the snapshot was taken);
- creates it again if it is missing and the resource had acquired it (`status.acquired` set), even
  with `creationPolicy: Adopt`.

Objects created out of band after the snapshot are lost. One-time outputs generated after the
snapshot, such as AppRole secret IDs, exist in Kubernetes Secrets but no longer in OpenBao:
generate them again.

### OpenBao rebuilt empty, resources intact

Re-initialise OpenBao and restore the connection identities first (auth method, role, policy), by
hand or with a bootstrap token. The operator then creates every object again. Server-generated
IDs change; resources that reference another resource (`entityRef`, `mountRef`) resolve the new
ID from the referent's status once it is `Ready`. Data is lost.

### Kubernetes rebuilt, OpenBao intact

Install the operator, restore the hash-key Secret and the referenced Secrets, then apply the
resources from Git. The new resources have no status, so the operator meets every object as an
existing object it does not own yet:

| `creationPolicy` | result |
|------------------|--------|
| `CreateOrAdopt` (default) | adopts the object, emits an `Adopted` event, sets `status.acquired: Adopted` |
| `Adopt` | adopts it; reports `NotFound` if it does not exist |
| `Create` | `Conflict`, unless the object already matches the spec and the spec has no write-only inputs |

Use `CreateOrAdopt` or `Adopt` for a restore. Each resource with write-only inputs writes once,
because its status has no hash yet.

### Status is not restored

Velero and GitOps tools restore spec and metadata but not status by default. The operator does not
need the status for most kinds: it re-acquires objects by adoption as above. Three kinds keep
state in status that cannot be rebuilt:

- `GroupMembership` records the members it added (`status.memberEntityIDs`,
  `status.memberGroupIDs`) and removes only those. After a restore it adds the members in its
  spec again, but it no longer removes members it added earlier and that the spec no longer lists.
- `AccessGrant` and `AccessLayout` record the objects they wrote in `status.children`. After a
  restore they cannot remove children that their spec no longer produces. Remove those by hand.
- `RandomSecret` adopts the existing value when its status is empty and does not regenerate it,
  even if `spec.refreshNonce` is set.

## Velero

Back up the operator namespace (for the hash-key Secret) and every namespace that holds custom
resources, with their Secrets:

```sh
velero backup create openbao-config \
  --include-namespaces openbao-config-operator,team-a,team-b \
  --include-cluster-resources=true
```

Points to check:

- Restore the CRDs, through the chart or `crds.yaml`, before the resources.
- Restore the hash-key Secret before the operator starts, or the operator creates a new key.
- Output Secrets written through `writeSecretRef` carry an owner reference to the resource's UID.
  A restored resource has a new UID. Check that the garbage collector has not removed the output
  Secrets. If an output Secret survives with the old owner, the operator refuses to overwrite it
  ("the Secret exists and is not controlled by this resource"); delete it so the operator writes it
  again. A missing `AppRoleSecretID` Secret makes the operator generate a new secret ID.
- Restored resources keep their finalizers. That is expected: the operator handles them.

## Deletion policy and orphaned objects

`deletionPolicy` decides what deleting a resource does in OpenBao. The default depends on the
kind:

| default | kinds |
|---------|-------|
| `Orphan` | objects that hold data or identities: `OpenBaoNamespace`, `AuthMount`, `SecretsMount`, `Entity`, `Group`, `KVSecret`, `KVConfig`, `RandomSecret`, `TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`, `MFAMethod`; singletons and engine or auth method config: every `*AuthConfig` and `*SecretsConfig`, `RabbitMQConfig`, `PKIConfig`, `PKIACMEConfig`, `PKIAutoTidy`, `OIDCConfig`, `CORSConfig`, `KeyRotationConfig`, `Logger`, `QuotaConfig`, `RaftAutopilot`, `Plugin`; and `OpenBaoEndpoint` |
| `Delete` | leaf configuration: `Policy`, `PasswordPolicy`, every role kind, `AppRoleSecretID`, `TokenRole`, `UserpassUser`, the auth group, team, user and certificate kinds, the GCP roleset and account kinds, `EntityAlias`, `GroupAlias`, `GroupMembership`, the OIDC key, role, provider, client, scope and assignment kinds, `MFALoginEnforcement`, `DatabaseConnection`, `RateLimitQuota`, `AuditRequestHeader`, `UIHeader`, `Workflow`, `AccessGrant`, `AccessLayout` |

The operator adds the finalizer `openbaoconfig.io/finalizer` only when the effective policy is
`Delete`, and removes it when the policy changes to `Orphan`. On deletion with `Delete` it deletes
`status.path` in `status.namespace`. If the connection or a credential is missing, the finalizer
stays and `Synced` reports `CleanupBlocked` until the delete succeeds.

An orphaned object stays in OpenBao, unmanaged. Re-creating the resource adopts it again
(`CreateOrAdopt` or `Adopt`).

## Uninstall and CRD deletion

`helm uninstall` with the default `crds.keep=true` removes the manager but keeps the CRDs, the
custom resources and every OpenBao object. Resources with the finalizer cannot be deleted until a
manager runs again.

With `crds.keep=false`, `helm uninstall` deletes the CRDs, and Kubernetes deletes every custom
resource with them. What happens in OpenBao then depends on timing: while a manager still runs, it
deletes every object whose policy is `Delete`, such as policies and roles. Once the manager is
gone, the finalizers block the deletion and the CRDs stay `Terminating`. Neither outcome is safe,
so keep `crds.keep=true`.

To remove the operator and leave OpenBao untouched:

1. Set `deletionPolicy: Orphan` on every resource, in Git.
2. Wait until no resource carries the finalizer:

   ```sh
   kubectl get openbao -A -o json \
     | jq -r '.items[] | select(.metadata.finalizers // [] | index("openbaoconfig.io/finalizer")) | "\(.kind) \(.metadata.namespace)/\(.metadata.name)"'
   ```

3. Run `helm uninstall`, then delete the CRDs.

To remove the operator and its configuration, delete the resources while the manager runs, wait
until they are gone, then uninstall.
