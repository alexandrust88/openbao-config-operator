# Identity

This guide declares OpenBao identities in Kubernetes: entities and groups, the aliases that link
logins to them, group membership, and OpenBao as an OIDC provider for other applications.

## Before you begin

A `Ready` connection whose OpenBao policy allows the `identity/` paths of the kinds you use
([OpenBao policy: platform operator](../operate/openbao-policy.md#platform-operator-in-the-root-namespace)
lists them). Aliases also need the auth mounts they refer to.

## 1. Create entities and groups

An `Entity` is a person or a service; a `Group` gives policies to its members. An internal group
gets members through `GroupMembership`; an external group gets them from an identity provider
through a `GroupAlias`:

=== "Entity"

    ```yaml
    --8<-- "examples/identity/entity.yaml"
    ```

=== "Group"

    ```yaml
    --8<-- "examples/identity/group.yaml"
    ```

=== "GroupMembership"

    ```yaml
    --8<-- "examples/identity/groupmembership.yaml"
    ```

Entities and groups default to `deletionPolicy: Orphan`, so deleting the resource keeps the
identity, its ID and everything that refers to it.

## 2. Link logins with aliases

An alias ties a login on one auth mount to an entity or a group. Each alias names its mount and its
target in one of four ways: a resource in the same namespace (`mountRef`, `entityRef`, `groupRef`),
a name or path, a label selector, or an OpenBao ID:

=== "EntityAlias"

    ```yaml
    --8<-- "examples/identity/entityalias.yaml"
    ```

=== "GroupAlias"

    ```yaml
    --8<-- "examples/identity/groupalias.yaml"
    ```

Apply an `EntityAlias` before the first login of that identity. Otherwise the first login creates
its own alias with the same name, and the `EntityAlias` reports `Conflict`. For an OIDC group
alias, `aliasName` is the group name exactly as the identity provider sends it in the groups claim.

## 3. Run OpenBao as an OIDC provider

Six kinds make OpenBao an OIDC provider for other applications: a signing key, scopes, clients,
assignments that say who may use a client, the provider, and the issuer configuration.

=== "OIDCKey"

    ```yaml
    --8<-- "examples/identity/oidckey.yaml"
    ```

=== "OIDCScope"

    ```yaml
    --8<-- "examples/identity/oidcscope.yaml"
    ```

=== "OIDCAssignment"

    ```yaml
    --8<-- "examples/identity/oidcassignment.yaml"
    ```

=== "OIDCClient"

    ```yaml
    --8<-- "examples/identity/oidcclient.yaml"
    ```

=== "OIDCProvider"

    ```yaml
    --8<-- "examples/identity/oidcprovider.yaml"
    ```

=== "OIDCConfig"

    ```yaml
    --8<-- "examples/identity/oidcconfig.yaml"
    ```

`OIDCClient` writes the generated client ID and secret to the Secret in `writeSecretRef`. Give it
to the application that logs its users in through OpenBao. `OIDCRole`
([examples/identity/oidcrole.yaml](../../examples/identity/oidcrole.yaml)) issues identity tokens to
OpenBao clients instead.

## 4. Check that it worked

```bash
kubectl -n openbao-config get entities,entityaliases,groups,groupaliases,groupmemberships
bao read identity/entity/name/obco-alice
```

Every resource shows `Ready` `True`. The entity lists its aliases and the groups it belongs to.

## Troubleshooting

| symptom | check |
|---------|-------|
| an alias or membership reports `ReferenceNotReady` for up to about 30 seconds | it waits for the mount, entity or group it names and retries with a backoff from 2 seconds up to 30 seconds; each level of references can add up to 30 seconds when you apply everything at once |
| `EntityAlias` reports `Conflict` | a login created the alias first: delete that alias in OpenBao, or adopt it with `creationPolicy: CreateOrAdopt` |
| an OIDC user is not in the external group | the `GroupAlias` `aliasName` against the groups claim, and the role's `groupsClaim` |
| `OIDCClient` reports `SecretNotOwned` | a Secret named in `writeSecretRef` exists and was not created by this resource |

## Related

- API reference: [identity.openbaoconfig.io](../reference/api/identity.md), with
  [Entity](../reference/api/identity.md#entity), [EntityAlias](../reference/api/identity.md#entityalias),
  [Group](../reference/api/identity.md#group), [GroupAlias](../reference/api/identity.md#groupalias),
  [GroupMembership](../reference/api/identity.md#groupmembership) and the OIDC kinds.
- [Templated policies](templated-policies.md): paths built from entity and group names.
- [Access model](access-model.md): `AccessLayout` writes external groups and aliases for you.
