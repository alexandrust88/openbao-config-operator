---
title: Migrate existing OpenBao configuration to the operator
description: Hand existing OpenBao objects to the operator with Observe, Adopt and staged Manage, without two writers on the same path.
---

# Migrate existing OpenBao configuration to the operator

Bring objects that already exist in OpenBao under the operator. The objects stay where they are;
only the manager changes. Observe first, hand each object over, then retire the old manager. At no
point should two tools write the same object.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows `read`, `create`, `update` and `delete` on the paths you
  hand over ([OpenBao policy for the operator](../operate/openbao-policy.md)).
- You can stop the previous manager from writing or deleting those objects. If it deletes an
  OpenBao object when its own record is removed, switch that off first so retiring it later leaves
  the object in place.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Generate or write the resources

Generate the resources with [`obc-import`](import.md), or write them by hand. Each resource needs
`managementPolicy: Observe`, `creationPolicy: Adopt` and `deletionPolicy: Orphan`; `obc-import`
sets the first two. `Adopt` reports `NotFound` instead of creating an object when a name or path
is wrong. The [kind catalogue](../kinds.md) lists every typed kind with its OpenBao path, and the
fields every kind shares are under [common spec](../architecture.md#common-spec). Manage a path
without a typed kind with `BaoEndpoint` ([ADR 0006](../adr/0006-endpoint-escape-hatch.md));
features with no typed kind, and what to use instead, are under [not typed](../kinds.md#not-typed).

Save this as `policy.yaml` for one policy you already hold in OpenBao (replace
`<policy-hcl>` with the live policy text):

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: app-read
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  managementPolicy: Observe
  creationPolicy: Adopt
  deletionPolicy: Orphan
  name: app-read
  policy: |
    <policy-hcl>
```

Create the Secrets for write-only fields with their real values before you leave `Observe`. In
`Observe` a missing write-only Secret resolves to an empty value, so the resource still reports
`Observed`; OpenBao never returns these values, so `Observe` cannot compare them. The first write
in `Manage` sends whatever the Secret holds, empty included
([Import](import.md#5-create-the-secrets-for-write-only-inputs)).

Check the fields of each kind against its CRD before you write the resource, for example for
`ACLPolicy`:

```bash
kubectl explain aclpolicies.spec --api-version=sys.obco.dev/v1alpha1
```

## 2. Observe until every resource matches

Apply the resources and read their `Synced` condition. `Observed` means OpenBao matches the spec;
`Drifted` names the keys that differ. Fix the spec until every resource is `Observed`.

```bash
kubectl apply -f policy.yaml
kubectl -n openbao-config wait aclpolicies.sys.obco.dev/app-read --for=condition=Ready --timeout=2m
kubectl -n openbao-config get aclpolicies.sys.obco.dev app-read -o jsonpath='{.status.conditions[?(@.type=="Synced")].reason}{"\n"}'
```

A field the spec leaves unset is not compared, so an adopted object keeps its other settings until
the spec names them.

## 3. Switch to Manage one step at a time

In the `ACLPolicy` above, set:

```yaml
spec:
  managementPolicy: Manage
```

Apply that change one Kubernetes namespace or one kind at a time. Check that each resource is
`Ready` and that its events show `Adopted`, not `Created`:

```bash
kubectl apply -f policy.yaml
kubectl -n openbao-config wait aclpolicies.sys.obco.dev/app-read --for=condition=Ready --timeout=2m
kubectl -n openbao-config get events --field-selector reason=Adopted
```

An `Updated` event names the keys that differed from the spec. Fix the spec if the difference was
not intended.

## 4. Retire the previous manager

Remove the previous manager's records. With its deletes switched off in Before you begin, the
OpenBao objects stay. Once the operator is the only manager, set `deletionPolicy: Delete` where
you want deleting the resource to remove the object; leave `Orphan` everywhere else. Destructive
kinds, such as mounts, OpenBao namespaces, KV data, audit devices, key material, MFA methods,
entities and `BaoEndpoint`, delete their object only with the annotation
`obco.dev/allow-destructive-delete: "true"`; the [kind catalogue](../kinds.md) lists them
([backup and restore](../operate/backup-restore.md#deletion-policy-and-orphaned-objects)).

## 5. Check that it worked

Confirm the Kubernetes resource and the object in OpenBao:

```bash
kubectl -n openbao-config get aclpolicies.sys.obco.dev app-read
bao read sys/policies/acl/app-read
```

Expect `Ready=True` and `Synced=True` on the resource, and the policy body you declared.

## Troubleshooting

| symptom | check |
|---------|-------|
| `Synced` reason `NotFound` with `creationPolicy: Adopt` | The name or path in the spec does not match OpenBao; fix the spec or create the object first |
| `Synced` reason `Drifted` while observing | Align the spec with OpenBao, or accept the keys named in the condition message |
| Write-only field wiped on first `Manage` | Put the real Secret values in place before leaving `Observe` |
| Previous tool deletes the OpenBao object | Disable its delete-on-remove behaviour before you remove its records |

## Related

- [ACLPolicy](../reference/api/sys.md#aclpolicy)
- [Import existing configuration](import.md)
- [Backup and restore](../operate/backup-restore.md)
- [Status and events](../reference/status-and-events.md)
