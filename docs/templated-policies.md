# Templated policies

OpenBao fills in identity templates in an ACL policy for each request, from the entity behind the
token. One policy can then give every workload its own path. The operator stores the policy text
as you write it: `Policy` passes `{{...}}` through unchanged. The access kinds write the templates
for you.

Every pattern on this page is tested end to end by `test/integration/templated_acl_test.go`. The
test runs the operator against an envtest API server and `bao server -dev`. Real ServiceAccounts
from two Kubernetes namespaces log in with TokenRequest tokens, then the test reads and writes
secrets with the tokens they get. The YAML below is in
[examples/policies/templated/](../examples/policies/templated/), and the same test applies those
files with a server-side dry run.

```bash
BAO_BIN=$(command -v bao) go test -tags integration -run TestTemplatedACL ./test/integration/
```

## What the test proves

- A ServiceAccount can read and write only its own templated path. It gets `403` on the path of
  the other ServiceAccount, on another name in its own namespace, and on paths without a template.
- `{{identity.entity.name}}` and `{{identity.entity.id}}` work both for a named entity (bound with
  `Entity` and `EntityAlias`) and for the `entity_<random>` entity that a login creates.
- A `{{identity.groups.names.<group>.id}}` path works only for members of the group. Membership
  comes from `Group` and `GroupMembership`.
- An `AccessGrant` with `{sa.namespace}/{sa.name}` gives the same isolation, for data and for
  metadata `list`.
- A capability added to a `Policy` applies to the next token of that ServiceAccount.
- In a child OpenBao namespace, a policy written through a connection based in that namespace
  works, and so does one written with `spec.namespace` through the root connection. Neither policy
  appears in the root namespace.

## The auth method

OpenBao checks each login with the Kubernetes TokenReview API. The token it uses for that review
belongs to a ServiceAccount bound to `system:auth-delegator`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: openbao-token-reviewer
  namespace: openbao-config
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: openbao-token-reviewer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - kind: ServiceAccount
    name: openbao-token-reviewer
    namespace: openbao-config
---
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: AuthMount
metadata:
  name: kubernetes
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  type: kubernetes
---
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: KubernetesAuthConfig
metadata:
  name: kubernetes
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  kubernetesHost: https://kubernetes.default.svc
  kubernetesCACert: |
    -----BEGIN CERTIFICATE-----
    REPLACE-WITH-THE-CLUSTER-CA
    -----END CERTIFICATE-----
  tokenReviewerJWTSecretRef:
    name: openbao-token-reviewer
    key: token
  disableLocalCAJWT: true
```

[00-kubernetes-auth.yaml](../examples/policies/templated/00-kubernetes-auth.yaml) also has the
reviewer token Secret and the KV v2 engine `apps`.

## A path per ServiceAccount

At login, the Kubernetes auth method writes the ServiceAccount's namespace and name to the entity
alias metadata as `service_account_namespace` and `service_account_name`. A template reads alias
metadata through the accessor of the auth mount. The `AuthMount` shows that accessor in
`status.id`, and `Policy` can put it into the text for you: name the mount in `accessorRefs` and
write `${accessor:<name>}` where the accessor goes. OpenBao's own `{{identity...}}` templates are
sent unchanged.

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: sa-own-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  accessorRefs:
    k8s:
      authMountRef:
        name: kubernetes
  policy: |
    path "apps/data/sa/{{identity.entity.aliases.${accessor:k8s}.metadata.service_account_namespace}}/{{identity.entity.aliases.${accessor:k8s}.metadata.service_account_name}}/*" {
      capabilities = ["create", "read", "update"]
    }
---
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: KubernetesAuthRole
metadata:
  name: apps
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  boundServiceAccountNames:
    - app
  boundServiceAccountNamespaces:
    - team-a
    - team-b
  tokenTTL: 15m
  tokenPolicies:
    - sa-own-folder
```

`team-a/app` can now use `apps/data/sa/team-a/app/*`, and `team-b/app` can use
`apps/data/sa/team-b/app/*`. Neither can use the other's path.

The accessor changes when the auth method is disabled and enabled again. The `Policy` follows the
`AuthMount` and rewrites the policy with the new accessor. Use `mountPath: kubernetes` instead of
`authMountRef` for a mount that no `AuthMount` manages; the operator then reads the accessor from
`sys/auth` at every drift check. A name in `${accessor:...}` that `accessorRefs` does not list is an
invalid spec, and the policy waits while the mount does not exist. `AccessGrant` resolves the
accessor too (see [below](#accessgrant-renders-the-templates)).

## Entity and group templates

A Kubernetes login creates an entity named `entity_<random>`, unless an alias already links the
ServiceAccount to a named entity. With the default alias name source (`serviceaccount_uid`), the
alias name is the ServiceAccount UID:

```bash
kubectl get serviceaccount app -n team-a -o jsonpath='{.metadata.uid}'
```

```yaml
apiVersion: identity.openbaoconfig.io/v1alpha1
kind: Entity
metadata:
  name: billing
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
---
apiVersion: identity.openbaoconfig.io/v1alpha1
kind: EntityAlias
metadata:
  name: billing-kubernetes
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  aliasName: 6f1e3c2a-0000-4000-8000-000000000001
  mountRef:
    name: kubernetes
  entityRef:
    name: billing
---
apiVersion: identity.openbaoconfig.io/v1alpha1
kind: Group
metadata:
  name: developers
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
---
apiVersion: identity.openbaoconfig.io/v1alpha1
kind: GroupMembership
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
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: entity-own-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  policy: |
    path "apps/data/entity/{{identity.entity.name}}/*" {
      capabilities = ["create", "read", "update"]
    }
    path "apps/data/entity-id/{{identity.entity.id}}/*" {
      capabilities = ["create", "read", "update"]
    }
---
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: group-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  policy: |
    path "apps/data/group/{{identity.groups.names.developers.id}}/*" {
      capabilities = ["create", "read", "update"]
    }
```

- Apply the `EntityAlias` before the ServiceAccount logs in for the first time. Otherwise the
  first login creates its own alias with the same name, and the `EntityAlias` conflicts with it.
- `{{identity.groups.names.developers.id}}` resolves only for members, so a non-member gets `403`.
  The group ID is in the `Group`'s `status.id`.

## AccessGrant renders the templates

In an `AccessGrant` path, `{sa.namespace}` and `{sa.name}` become the alias-metadata templates of
the auth mount the grant uses. The grant looks the accessor up on every reconcile, so you never
copy it by hand:

```yaml
apiVersion: access.openbaoconfig.io/v1alpha1
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
apiVersion: access.openbaoconfig.io/v1alpha1
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
      namespace: team-*
  rules:
    - mount: apps
      path: grant/{sa.namespace}/{sa.name}/*
      metadataPath: grant/{sa.namespace}/{sa.name}/*
      level: read-write
  tokenTTL: 15m
```

The grant writes the policy `grant_<namespace>_<name>` and one Kubernetes auth role per subject.
A workload logs in with the role in `status.roles`:

```bash
kubectl get accessgrant team-apps -n openbao-config -o jsonpath='{.status.roles[0]}'
```

## Child namespaces

Each OpenBao namespace has its own auth mounts, so the accessor in a template has to be the one
of the auth mount inside that namespace. An object reaches a child namespace in one of two ways:

- through a connection whose `spec.namespace` is the child namespace (`openbao-team` below), or
- through the root connection, with `spec.namespace` on the object.

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata:
  name: team
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
---
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: team-sa-own-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao-team
  name: sa-own-folder
  policy: |
    path "apps/data/sa/{{identity.entity.aliases.auth_kubernetes_5e6f7a8b.metadata.service_account_namespace}}/{{identity.entity.aliases.auth_kubernetes_5e6f7a8b.metadata.service_account_name}}/*" {
      capabilities = ["create", "read", "update"]
    }
---
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: team-entity-id-folder
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  namespace: team
  name: entity-id-folder
  policy: |
    path "apps/data/entity-id/{{identity.entity.id}}/*" {
      capabilities = ["create", "read", "update"]
    }
```

[40-child-namespace.yaml](../examples/policies/templated/40-child-namespace.yaml) has the full
set: the connection, the auth mount, the KV engine, the auth config and the role in `team`.
Workloads log in to the child namespace with the `X-Vault-Namespace: team` header
(`BAO_NAMESPACE=team`).

## Changing a policy

OpenBao looks a policy up by name on every request. Edit the `Policy`, wait until it is `Ready`,
and the next token uses the new text. The test adds `delete` to a policy, waits for the write,
logs in again, and the new token can delete.

## Ordering

`EntityAlias` and `GroupMembership` resolve their references (`mountRef`, `entityRef`, `groupRef`)
when they reconcile. If a reference is not `Ready` yet,
the object reports `ReferenceNotReady` and tries again 30 seconds later. Nothing triggers an earlier
retry. If you apply everything at once, expect up to 30 seconds per level of references before
everything is `Ready`.
