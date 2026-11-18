---
title: Uninstall OpenBao Config Operator
description: Uninstall OpenBao Config Operator and its custom resources without leaving Terminating objects or orphaned OpenBao configuration.
---

# Uninstall

Remove OpenBao Config Operator from a cluster, with its custom resources, finalizers and CRDs, without
leaving objects stuck in `Terminating`.

Follow the steps in order, and keep the manager running until step 6. These finalizers are
released only by a running manager:

| finalizer | on | released when |
|-----------|----|---------------|
| `obco.dev/finalizer` | a resource with `deletionPolicy: Delete` | the manager has deleted its OpenBao object, or left it on purpose |
| `obco.dev/in-use` | an `AccessLevel` | no `AccessGrant` or `AccessLayout` cites it |
| `obco.dev/connection-in-use` | a `BaoConnection` or `ClusterBaoConnection` | no resource that still carries `obco.dev/finalizer` uses it |
| `obco.dev/server-in-use` | a `BaoServer` | no connection references it |

Without a manager, the objects that carry them stay `Terminating`, and so do their CRDs.

## Before you begin

- The commands need `kubectl` with cluster-admin rights and `jq`. Every kind of the operator is in
  the category `obco`, so `kubectl get obco --all-namespaces` lists all of its objects.
- If a GitOps tool applies the resources, make the changes in Git and remove the resources from
  Git, or suspend its sync, so that it does not undo the steps.
- Remove the annotation `obco.dev/paused` from resources, Namespaces and `BaoServer`s.
  The manager does not delete a paused resource and keeps its finalizer.

## 1. Choose what stays in OpenBao

`deletionPolicy` decides what happens to the OpenBao object of a resource when you delete the
resource.

=== "Keep the OpenBao configuration"

    Set `deletionPolicy: Orphan` on every resource, in Git or with this command, which patches
    every resource that has `Delete`:

    ```bash
    --8<-- "hack/docs/uninstall.sh:orphan"
    ```

    Every OpenBao object stays when you delete the resources.

=== "Remove the OpenBao configuration"

    Keep `deletionPolicy: Delete` on the resources whose OpenBao objects should go. The
    [destructive kinds](../kinds.md), such as `SecretsMount`, `AuthMount`, `BaoNamespace` and
    `KVSecret`, delete only with the annotation `obco.dev/allow-destructive-delete: "true"`;
    without it, their deletion waits with the reason `DeleteGuarded`. This command annotates every
    resource that has `Delete`; the annotation has no effect on the other kinds:

    ```bash
    --8<-- "hack/docs/uninstall.sh:destructive"
    ```

    Kinds whose OpenBao object cannot be deleted, such as the auth method configurations, reject
    `Delete`; their objects go with the mount or namespace that holds them, or stay.

To remove some configuration and keep the rest, run the keep command, set `Delete` again on the
resources whose objects should go, and then run the remove command. A resource you change to
`Delete` is protected only once it carries the finalizer `obco.dev/finalizer`, which the
manager adds within seconds; if you delete it before that, its object stays in OpenBao.

Deleting the Kubernetes namespaces is no shortcut: in a terminating Kubernetes namespace, a destructive
resource without the annotation, and a resource whose connection is already gone, leave their
objects in OpenBao.

## 2. Delete the resources

Delete every resource except the connections and the servers. The command waits until they are
gone, for up to 10 minutes:

```bash
--8<-- "hack/docs/uninstall.sh:delete-resources"
```

If it times out, `kubectl get obco --all-namespaces` shows what is left, and the `Synced`
condition of each one says why ([Stuck finalizers](troubleshooting.md#stuck-finalizers)).

## 3. Delete the connections

A connection that a resource still needs keeps its finalizer, so delete the connections only
after step 2:

```bash
--8<-- "hack/docs/uninstall.sh:delete-connections"
```

## 4. Delete the servers

```bash
--8<-- "hack/docs/uninstall.sh:delete-servers"
```

## 5. Check that nothing is left

```bash
--8<-- "hack/docs/uninstall.sh:check"
```

```text title="Expected output"
No resources found
```

## 6. Remove the manager

=== "Helm"

    ```bash
    --8<-- "hack/docs/uninstall.sh:helm-uninstall"
    ```

    With the default `crds.keep=true`, this removes the manager and keeps the CRDs and the
    hash-key Secret.

=== "install.yaml"

    ```bash
    kubectl delete \
      -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/install.yaml
    ```

    This also deletes the CRDs, so step 7 is done.

## 7. Delete the CRDs

```bash
--8<-- "hack/docs/uninstall.sh:crds"
```

[Backup and restore](backup-restore.md#uninstall-and-crd-deletion) explains what deleting the CRDs
does in OpenBao.

With Helm, to remove everything, delete the release namespace, which holds the hash-key Secret:

```bash
kubectl delete namespace openbao-config-operator
```

Keep it if you plan to reinstall: a new hash key makes every resource with write-only inputs
write to OpenBao once.

## Related

- [Backup and restore](backup-restore.md#uninstall-and-crd-deletion)
- [Troubleshooting: stuck finalizers](troubleshooting.md#stuck-finalizers)
- [Install options: CRDs](install-options.md#crds)
