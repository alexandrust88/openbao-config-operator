---
title: Write OpenBao templated policies with Kubernetes
description: Scope ACL paths with identity templates, accessorRefs and AccessGrant placeholders for each ServiceAccount.
---

# Write OpenBao templated policies with Kubernetes

OpenBao fills in identity templates in an ACL policy for each request, from the entity behind the
token. One policy can then give every workload its own path. The OpenBao Config Operator stores
the policy text as you write it: `ACLPolicy` passes `{{...}}` through unchanged. The access kinds
write the templates for you.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/auth"                   { capabilities = ["read"] }
    path "sys/policies/acl/*"         { capabilities = ["create", "read", "update", "delete"] }
    path "auth/kubernetes/role/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "identity/entity/name/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "identity/group/id/*"        { capabilities = ["read", "update"] }
    path "identity/entity-alias"      { capabilities = ["create", "update"] }
    path "identity/entity-alias/id/*" { capabilities = ["read", "update", "delete"] }
    path "identity/lookup/entity"     { capabilities = ["update"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The Kubernetes auth method at `kubernetes`, managed by the `AuthMount` named `kubernetes`, and
  the ServiceAccount `app` in the Kubernetes namespace `apps`
  ([Configure Kubernetes authentication](kubernetes-auth.md), steps 1 to 3).
- The KV version 2 mount `secret` ([Manage KV secrets from Kubernetes](kv-secrets.md)).
- The `IdentityGroup` named `developers` from step 1 of
  [Manage OpenBao identity with Kubernetes](identity.md) is `Ready`.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Scope a path per ServiceAccount

At login, the Kubernetes auth method writes the ServiceAccount's Kubernetes namespace and name to
the entity alias metadata, which a template reads through the accessor of that auth mount. Name the
mount in `accessorRefs` and write `${accessor:<name>}` where the accessor goes. Save this as
`sa-own-folder.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: sa-own-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: sa-own-folder
  accessorRefs:
    kubernetes:
      authMountRef:
        name: kubernetes
  policy: |
    path "secret/data/sa/{{identity.entity.aliases.${accessor:kubernetes}.metadata.service_account_namespace}}/{{identity.entity.aliases.${accessor:kubernetes}.metadata.service_account_name}}/*" {
      capabilities = ["create", "read", "update"]
    }
    path "secret/metadata/sa/{{identity.entity.aliases.${accessor:kubernetes}.metadata.service_account_namespace}}/{{identity.entity.aliases.${accessor:kubernetes}.metadata.service_account_name}}/*" {
      capabilities = ["read", "list"]
    }
---
apiVersion: auth.obco.dev/v1alpha1
kind: KubernetesAuthRole
metadata:
  name: sa-own-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  name: sa-own-folder
  boundServiceAccountNames:
    - app
    - billing
  boundServiceAccountNamespaces:
    - apps
  audience: openbao
  tokenTTL: 15m
  policyRefs:
    - name: sa-own-folder
```

```bash
kubectl apply -f sa-own-folder.yaml
kubectl -n openbao-config wait aclpolicies.sys.obco.dev/sa-own-folder --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait kubernetesauthrole/sa-own-folder --for=condition=Ready --timeout=2m
```

The ServiceAccount `app` in `apps` can use `secret/data/sa/apps/app/*`; a ServiceAccount in another
Kubernetes namespace cannot use that path. The operator replaces `${accessor:kubernetes}` with the
accessor that the `AuthMount` shows in `status.id`, and rewrites the policy when the auth method is
disabled and enabled again with a new accessor. In `accessorRefs`, use `mountPath: kubernetes`
instead of `authMountRef` for a mount that no `AuthMount` manages; the operator then reads the
accessor from `sys/auth` at every reconcile.

## 2. Use entity and group templates

A Kubernetes login creates an entity named `entity_<random>`, unless an alias already links the
ServiceAccount to a named entity. With the default alias name source (`serviceaccount_uid`), the
alias name is the ServiceAccount UID. Create the ServiceAccount `billing` and print its UID:

```bash
kubectl -n apps create serviceaccount billing --dry-run=client -o yaml | kubectl apply -f -
kubectl -n apps get serviceaccount billing -o jsonpath='{.metadata.uid}'
```

Save this as `entity-templates.yaml`:

```yaml
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityEntity
metadata:
  name: billing
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: billing
  policies:
    - entity-own-folder
---
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityEntityAlias
metadata:
  name: billing-kubernetes
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  aliasName: <service-account-uid>
  mountRef:
    name: kubernetes
  entityRef:
    name: billing
---
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityGroupMembership
metadata:
  name: developers-billing
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  groupRef:
    name: developers
  entityRef:
    name: billing
---
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: entity-own-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: entity-own-folder
  policy: |
    path "secret/data/entity/{{identity.entity.name}}/*" {
      capabilities = ["create", "read", "update"]
    }
    path "secret/data/group/{{identity.groups.names.developers.id}}/*" {
      capabilities = ["create", "read", "update"]
    }
```

Replace `<service-account-uid>` with the UID the last command printed, then apply the file:

```bash
kubectl apply -f entity-templates.yaml
kubectl -n openbao-config wait identityentityalias/billing-kubernetes --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait identitygroupmembership/developers-billing --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait aclpolicies.sys.obco.dev/entity-own-folder --for=condition=Ready --timeout=2m
```

Apply the `IdentityEntityAlias` before `billing` logs in for the first time. Otherwise the first login
creates its own alias with the same name, and the `IdentityEntityAlias` conflicts with it. `aliasName` is
immutable, so a wrong UID means deleting and recreating the resource.
`{{identity.groups.names.developers.id}}` resolves only for members, so a non-member gets `403`.

## 3. Let AccessGrant render the templates { #accessgrant-renders-the-templates }

In an `AccessGrant` path, `{sa.namespace}` and `{sa.name}` become the alias-metadata templates of
the auth mount the grant uses. The grant looks the accessor up on every reconcile, so you never
copy it by hand. Save this as `templated-grant.yaml`:

```yaml
apiVersion: access.obco.dev/v1alpha1
kind: AccessLevel
metadata:
  name: read-write
  namespace: openbao-config
spec:
  data:
    - create
    - read
    - update
  metadata:
    - read
    - list
---
apiVersion: access.obco.dev/v1alpha1
kind: AccessGrant
metadata:
  name: team-apps
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  authMountRef:
    name: kubernetes
  subjects:
    - name: app
      namespace: apps
  rules:
    - mount: secret
      path: grant/{sa.namespace}/{sa.name}/*
      metadataPath: grant/{sa.namespace}/{sa.name}/*
      level: read-write
  tokenTTL: 15m
```

```bash
kubectl apply -f templated-grant.yaml
kubectl -n openbao-config wait accessgrant/team-apps --for=condition=Ready --timeout=2m
kubectl -n openbao-config get accessgrant team-apps -o jsonpath='{.status.roles}'
```

The grant writes the policy `grant_<Kubernetes namespace>_<name>`, here
`grant_openbao-config_team-apps`, and one Kubernetes auth role per subject.

## 4. Write templates in a child OpenBao namespace

Each OpenBao namespace has its own auth mounts, so the accessor in a template has to be the one of
the auth mount inside that OpenBao namespace. This step uses the tenant from
[Organise OpenBao namespaces and tenants with Kubernetes](namespaces-and-tenants.md): the connection
`openbao` in the Kubernetes namespace `team-a` is based in the OpenBao namespace `tenants/team-a`,
which has its own Kubernetes auth method at `kubernetes`. Save this as
`child-namespace-policy.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: ACLPolicy
metadata:
  name: team-a-sa-own-folder
  namespace: team-a
spec:
  connectionRef:
    name: openbao
  name: team-a-sa-own-folder
  accessorRefs:
    kubernetes:
      mountPath: kubernetes
  policy: |
    path "secret/data/sa/{{identity.entity.aliases.${accessor:kubernetes}.metadata.service_account_namespace}}/{{identity.entity.aliases.${accessor:kubernetes}.metadata.service_account_name}}/*" {
      capabilities = ["create", "read", "update"]
    }
```

The policy name starts with `team-a-`, as the tenant policy in
[OpenBao policy for the operator](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace)
requires. Because `mountPath` resolves the accessor from the mount table, the tenant connection's
OpenBao policy also needs `read` on `sys/auth` in `tenants/team-a`.

```bash
kubectl apply -f child-namespace-policy.yaml
kubectl -n team-a wait aclpolicies.sys.obco.dev/team-a-sa-own-folder --for=condition=Ready --timeout=2m
```

Workloads log in to the child OpenBao namespace with the `X-Vault-Namespace: tenants/team-a`
header (`BAO_NAMESPACE=tenants/team-a`).

## 5. Check that it worked

Confirm the policies are ready, then that OpenBao holds the rendered text:

```bash
kubectl -n openbao-config get aclpolicies.sys.obco.dev sa-own-folder entity-own-folder
bao policy read sa-own-folder
```

Expect `Ready` and `Synced` to be `True`. In OpenBao, every `${accessor:<name>}` is replaced by
the mount accessor and the `{{identity...}}` templates are unchanged. After an edit, check that
`Synced` is `True` again, because `Ready` keeps its last value while a write fails. OpenBao
applies the new text to every token that carries the policy on its next request.

## Troubleshooting

| symptom | check |
|---------|-------|
| the policy reports `InvalidSpec` | every `${accessor:...}` name in the policy appears in `accessorRefs` |
| the policy reports `ReferenceNotReady` | the `AuthMount` in `accessorRefs` exists in the policy's Kubernetes namespace and is `Ready`, or, with `mountPath`, the auth method exists in the connection's OpenBao namespace |
| an `IdentityEntityAlias` or `IdentityGroupMembership` reports `ReferenceNotReady` | the entity, group or mount it names; it retries with a backoff from 2 seconds up to 30 seconds, and a referent that becomes `Ready` does not start an earlier retry |
| a ServiceAccount gets `403` on its own path | the accessor matches the auth mount that issued the token; after a remount, wait for `accessorRefs` to rewrite the policy |
| `IdentityEntityAlias` reports `Conflict` | a login created the alias first: delete that alias in OpenBao, or adopt it with `creationPolicy: CreateOrAdopt` |
| a group-templated path returns `403` | the entity is a member of the group (`IdentityGroupMembership` is `Ready`); the group ID is the `IdentityGroup`'s `status.id` |

## Related

- API reference: [ACLPolicy](../reference/api/sys.md#aclpolicy),
  [KubernetesAuthRole](../reference/api/auth.md#kubernetesauthrole),
  [IdentityEntity](../reference/api/identity.md#identityentity),
  [IdentityEntityAlias](../reference/api/identity.md#identityentityalias),
  [IdentityGroupMembership](../reference/api/identity.md#identitygroupmembership),
  [AccessLevel](../reference/api/access.md#accesslevel),
  [AccessGrant](../reference/api/access.md#accessgrant).
- [Manage OpenBao policies with Kubernetes](policies.md)
- [Manage OpenBao identity with Kubernetes](identity.md)
- [Grant KV access with access levels, grants and layouts](access-model.md)
- [Organise OpenBao namespaces and tenants with Kubernetes](namespaces-and-tenants.md)
