---
description: "Write OpenBao ACL policy templates that use identity metadata to scope application and tenant access."
---

# Templated policies

OpenBao fills in identity templates in an ACL policy for each request, from the entity behind the
token. One policy can then give every workload its own path. The operator stores the policy text
as you write it: `Policy` passes `{{...}}` through unchanged. The access kinds write the templates
for you.

The YAML on this page is in
[examples/policies/templated/](../../examples/policies/templated/), and an integration test runs
every pattern end to end ([How these patterns are tested](#how-these-patterns-are-tested)).

## Before you begin

A `Ready` connection `openbao` in the Kubernetes namespace `openbao-config`, whose OpenBao policy
may manage auth and secrets mounts, policies, Kubernetes auth roles and identity objects
([OpenBao policy: platform operator](../operate/openbao-policy.md#platform-operator-in-the-root-namespace)).

## The auth method

OpenBao checks each login with the Kubernetes TokenReview API. The token it uses for that review
belongs to a ServiceAccount bound to `system:auth-delegator`:

```yaml title="examples/policies/templated/00-kubernetes-auth.yaml"
--8<-- "examples/policies/templated/00-kubernetes-auth.yaml"
```

The file also creates the reviewer token Secret and the KV v2 engine `apps` that the next sections use.

## A path per ServiceAccount

At login, the Kubernetes auth method writes the ServiceAccount's namespace and name to the entity
alias metadata as `service_account_namespace` and `service_account_name`. A template reads alias
metadata through the accessor of the auth mount. The `AuthMount` shows that accessor in
`status.id`. The example below writes the accessor literally, so replace
`auth_kubernetes_1a2b3c4d` with yours. `Policy` can also put it into the text for you: name the
mount in `accessorRefs` and write `${accessor:<name>}` where the accessor goes, as
[examples/sys/policy.yaml](../../examples/sys/policy.yaml) does. OpenBao's own `{{identity...}}`
templates are sent unchanged.

```yaml title="examples/policies/templated/10-serviceaccount-path.yaml"
--8<-- "examples/policies/templated/10-serviceaccount-path.yaml"
```

`team-a/app` can now use `apps/data/sa/team-a/app/*`, and `team-b/app` can use
`apps/data/sa/team-b/app/*`. Neither can use the other's path. The role also attaches the two
policies of the next section.

The accessor changes when the auth method is disabled and enabled again. A literal accessor, as in
the example, then no longer matches. A `Policy` that uses `accessorRefs` follows the `AuthMount`
and rewrites the policy with the new accessor. In `accessorRefs`, use `mountPath: kubernetes`
instead of `authMountRef` for a mount that no `AuthMount` manages; the operator then reads the
accessor from `sys/auth` at every drift check. A name in `${accessor:...}` that `accessorRefs` does not list is an
invalid spec, and the policy waits while the mount does not exist. `AccessGrant` resolves the
accessor too (see [below](#accessgrant-renders-the-templates)).

## Entity and group templates

A Kubernetes login creates an entity named `entity_<random>`, unless an alias already links the
ServiceAccount to a named entity. With the default alias name source (`serviceaccount_uid`), the
alias name is the ServiceAccount UID:

```bash
kubectl get serviceaccount app -n team-a -o jsonpath='{.metadata.uid}'
```

```yaml title="examples/policies/templated/20-entity-and-group.yaml"
--8<-- "examples/policies/templated/20-entity-and-group.yaml"
```

- Apply the `EntityAlias` before the ServiceAccount logs in for the first time. Otherwise the
  first login creates its own alias with the same name, and the `EntityAlias` conflicts with it.
- `{{identity.groups.names.developers.id}}` resolves only for members, so a non-member gets `403`.
  The group ID is in the `Group`'s `status.id`.

## AccessGrant renders the templates

In an `AccessGrant` path, `{sa.namespace}` and `{sa.name}` become the alias-metadata templates of
the auth mount the grant uses. The grant looks the accessor up on every reconcile, so you never
copy it by hand:

```yaml title="examples/policies/templated/30-accessgrant.yaml"
--8<-- "examples/policies/templated/30-accessgrant.yaml"
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

```yaml title="examples/policies/templated/40-child-namespace.yaml"
--8<-- "examples/policies/templated/40-child-namespace.yaml"
```

The file holds the full set: the namespace, the connection, the auth mount, the KV engine, the
auth config, both policies and the role in `team`. Workloads log in to the child namespace with the `X-Vault-Namespace: team` header
(`BAO_NAMESPACE=team`).

## Changing a policy

OpenBao looks a policy up by name on every request. Edit the `Policy`, wait until it is `Ready`,
and the next token uses the new text. The test adds `delete` to a policy, waits for the write,
logs in again, and the new token can delete.

## Ordering

`EntityAlias` and `GroupMembership` resolve their references (`mountRef`, `entityRef`, `groupRef`)
when they reconcile. If a reference is not `Ready` yet, the object reports `ReferenceNotReady` and
tries again with a backoff from 2 seconds up to 30 seconds; a change of the referent does not
trigger an earlier retry. If you apply everything at once, expect up to 30 seconds per level of
references before everything is `Ready`.

## How these patterns are tested

`test/integration/templated_acl_test.go` runs the operator against an envtest API server and
`bao server -dev`. Real ServiceAccounts from two Kubernetes namespaces log in with TokenRequest
tokens, then the test reads and writes secrets with the tokens they get. The same test applies the
files of `examples/policies/templated/` with a server-side dry run. It proves that:

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

CI runs it with the other integration tests. To run it alone from a clone of the repository, with
`bao` on your `PATH` or in `BAO_BIN`:

```bash
make test-integration INTEGRATION_RUN=TestTemplatedACL
```
