---
title: Manage OpenBao identity with Kubernetes
description: Declare OpenBao entities, aliases, groups and membership as Kubernetes resources, then verify them in OpenBao.
---

# Manage OpenBao identity with Kubernetes

Declare people and services as OpenBao entities and groups from Git. The OpenBao Config Operator
writes entities, aliases that link logins to them, group membership, and optionally the kinds that
make OpenBao an OIDC provider for other applications.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/auth"                   { capabilities = ["read"] }
    path "identity/entity/name/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "identity/group/name/*"      { capabilities = ["create", "read", "update", "delete"] }
    path "identity/group/id/*"        { capabilities = ["read", "update"] }
    path "identity/entity-alias"      { capabilities = ["create", "update"] }
    path "identity/entity-alias/id/*" { capabilities = ["read", "update", "delete"] }
    path "identity/group-alias"       { capabilities = ["create", "update"] }
    path "identity/group-alias/id/*"  { capabilities = ["read", "update", "delete"] }
    path "identity/lookup/entity"     { capabilities = ["update"] }
    path "identity/lookup/group"      { capabilities = ["update"] }
    # step 4: OpenBao as an OIDC provider
    path "identity/oidc/key/*"        { capabilities = ["create", "read", "update", "delete"] }
    path "identity/oidc/scope/*"      { capabilities = ["create", "read", "update", "delete"] }
    path "identity/oidc/assignment/*" { capabilities = ["create", "read", "update", "delete"] }
    path "identity/oidc/client/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "identity/oidc/provider/*"   { capabilities = ["create", "read", "update", "delete"] }
    path "identity/oidc/config"       { capabilities = ["read", "update"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The `ACLPolicy` named `app-read` from [Manage OpenBao policies with Kubernetes](policies.md) is
  `Ready`.
- For aliases, the auth mounts they link exist: `approle-ci` from
  [Configure AppRole authentication](approle.md) and `oidc` from
  [Configure JWT and OIDC authentication](jwt-oidc-auth.md).
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Create an entity and an internal group

An `IdentityEntity` is a person or a service. An `IdentityGroup` of type `internal` gets members through
`IdentityGroupMembership`. Save this as `identity-core.yaml`:

```yaml
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityEntity
metadata:
  name: alice
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: alice
  policies:
    - app-read
---
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityGroup
metadata:
  name: developers
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: developers
  type: internal
  policies:
    - app-read
---
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityGroupMembership
metadata:
  name: developers-alice
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  groupRef:
    name: developers
  entityRef:
    name: alice
```

```bash
kubectl apply -f identity-core.yaml
kubectl -n openbao-config wait identityentity/alice --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait identitygroup/developers --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait identitygroupmembership/developers-alice --for=condition=Ready --timeout=2m
```

Entities and groups default to `deletionPolicy: Orphan`, so deleting the resource keeps the
identity, its ID and everything that refers to it. The example with
[every field](../reference/examples/identity.md#identityentity) shows the other options.

## 2. Link a login with an entity alias

An `IdentityEntityAlias` ties a login name on one auth mount to an entity. Apply it before the first login
of that identity; otherwise the first login creates its own alias with the same name and the
resource reports `Conflict`. If you already logged in with the role (step 4 of
[Configure AppRole authentication](approle.md)), add `creationPolicy: CreateOrAdopt` under `spec` to
take over the alias that login created. Save this as `entity-alias.yaml`:

```yaml
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityEntityAlias
metadata:
  name: alice-approle
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  aliasName: ci-deployer
  mountPath: approle-ci
  entityRef:
    name: alice
```

`aliasName` is the name the auth method reports at login. For AppRole that is the role ID, not the
role name: here the `roleID` `ci-deployer` of the `AppRole` from
[Configure AppRole authentication](approle.md). For a role without `roleID`, use the generated ID
that `bao read -field=role_id auth/approle-ci/role/<role>/role-id` prints. Replace `approle-ci`
with the path of the auth mount that issues the login, or point at an `AuthMount` with `mountRef`
instead of `mountPath`.

```bash
kubectl apply -f entity-alias.yaml
kubectl -n openbao-config wait identityentityalias/alice-approle --for=condition=Ready --timeout=2m
```

## 3. Map an identity-provider group with a group alias

An external `IdentityGroup` plus an `IdentityGroupAlias` maps a groups claim from OIDC or JWT auth onto OpenBao
policies. `aliasName` must match the group name exactly as the identity provider sends it. Save
this as `group-alias.yaml`:

```yaml
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityGroup
metadata:
  name: idp-admins
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  deletionPolicy: Orphan
  name: idp-admins
  type: external
  policies:
    - app-read
---
apiVersion: identity.obco.dev/v1alpha1
kind: IdentityGroupAlias
metadata:
  name: idp-admins
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  aliasName: /platform/admins
  mountPath: oidc
  groupRef:
    name: idp-admins
```

```bash
kubectl apply -f group-alias.yaml
kubectl -n openbao-config wait identitygroup/idp-admins --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait identitygroupalias/idp-admins --for=condition=Ready --timeout=2m
```

For human access without writing these objects by hand, use an `AccessLayout`
([Grant KV access with access levels, grants and layouts](access-model.md)).

## 4. Run OpenBao as an OIDC provider

Six kinds make OpenBao an OIDC provider for other applications: a signing key, a scope, an
assignment of who may use a client, the client, the provider, and the issuer configuration. Save
this as `oidc-provider.yaml`:

```yaml
apiVersion: identity.obco.dev/v1alpha1
kind: OIDCKey
metadata:
  name: portal-signing-key
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: portal-signing-key
  algorithm: ES256
  allowedClientIDs:
    - "*"
---
apiVersion: identity.obco.dev/v1alpha1
kind: OIDCScope
metadata:
  name: portal-groups
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: portal-groups
  template: '{"groups": {{identity.entity.groups.names}}}'
---
apiVersion: identity.obco.dev/v1alpha1
kind: OIDCAssignment
metadata:
  name: portal-users
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: portal-users
  entities:
    - entityRef:
        name: alice
  groups:
    - groupRef:
        name: developers
---
apiVersion: identity.obco.dev/v1alpha1
kind: OIDCClient
metadata:
  name: portal
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: portal
  key: portal-signing-key
  clientType: confidential
  redirectURIs:
    - https://portal.example.com/oidc/callback
  assignments:
    - portal-users
  writeSecretRef:
    name: portal-oidc-credentials
---
apiVersion: identity.obco.dev/v1alpha1
kind: OIDCProvider
metadata:
  name: portal-provider
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  name: portal-provider
  issuer: https://openbao.example.com:8200
  allowedClients:
    - clientRef:
        name: portal
  scopesSupported:
    - portal-groups
---
apiVersion: identity.obco.dev/v1alpha1
kind: OIDCConfig
metadata:
  name: portal-oidc-config
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  issuer: https://openbao.example.com:8200
```

Replace `https://openbao.example.com:8200` and the redirect URI with your issuer and application
URLs. `OIDCClient` writes the generated client ID and secret to the Secret in `writeSecretRef`;
give them to the application that logs its users in through OpenBao. `OIDCRole`
([example](../reference/examples/identity.md#oidcrole)) is for the other direction: OpenBao issues
signed identity tokens to its own logged-in identities at `identity/oidc/token/<name>`.

```bash
kubectl apply -f oidc-provider.yaml
kubectl -n openbao-config wait oidcclient/portal --for=condition=Ready --timeout=2m
kubectl -n openbao-config wait oidcprovider/portal-provider --for=condition=Ready --timeout=2m
```

## 5. Check that it worked

Confirm the Kubernetes resources are ready, then that OpenBao holds the entity:

```bash
kubectl -n openbao-config get identityentities,identityentityaliases,identitygroups,identitygroupaliases,identitygroupmemberships
bao read identity/entity/name/alice
```

Every resource shows `Ready` `True`. After a later edit, check `Synced` as well, because `Ready`
keeps its last value while a write fails. The response lists the entity's `aliases` and, in
`group_ids`, the ID of the group `developers` (the `IdentityGroup`'s `status.id`).

## Troubleshooting

| symptom | check |
|---------|-------|
| an alias or membership reports `ReferenceNotReady` for up to about 30 seconds | it waits for the mount, entity or group it names and retries with a backoff from 2 seconds up to 30 seconds; each level of references can add up to 30 seconds when you apply everything at once |
| `IdentityEntityAlias` reports `Conflict` | a login created the alias first: delete that alias in OpenBao, or adopt it with `creationPolicy: CreateOrAdopt` |
| an OIDC user is not in the external group | the `IdentityGroupAlias` `aliasName` against the groups claim, and the role's `groupsClaim` |
| `OIDCClient` reports `SecretNotOwned` | a Secret named in `writeSecretRef` exists and was not created by this resource |

## Related

- API reference: [IdentityEntity](../reference/api/identity.md#identityentity),
  [IdentityEntityAlias](../reference/api/identity.md#identityentityalias),
  [IdentityGroup](../reference/api/identity.md#identitygroup),
  [IdentityGroupAlias](../reference/api/identity.md#identitygroupalias),
  [IdentityGroupMembership](../reference/api/identity.md#identitygroupmembership),
  [OIDCKey](../reference/api/identity.md#oidckey),
  [OIDCScope](../reference/api/identity.md#oidcscope),
  [OIDCAssignment](../reference/api/identity.md#oidcassignment),
  [OIDCClient](../reference/api/identity.md#oidcclient),
  [OIDCProvider](../reference/api/identity.md#oidcprovider),
  [OIDCConfig](../reference/api/identity.md#oidcconfig).
- [Write OpenBao templated policies with Kubernetes](templated-policies.md)
- [Grant KV access with access levels, grants and layouts](access-model.md)
