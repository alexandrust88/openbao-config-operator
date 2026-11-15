# Access model

This guide gives workloads and people access to KV version 2 secrets without writing ACL policies
by hand. Three kinds work together:

| kind | for | writes in OpenBao |
|------|-----|-------------------|
| `AccessLevel` | a named set of capabilities, such as read, write or owner | nothing; grants and layouts cite it |
| `AccessGrant` | Kubernetes ServiceAccounts (machine access) | one policy `grant_<namespace>_<name>` and one Kubernetes auth role per subject |
| `AccessLayout` | identity provider groups (human access) | one policy per role, OpenBao namespace and organisation, and per auth mount an external group with a group alias |

## Before you begin

- A KV version 2 mount ([KV secrets](kv-secrets.md)).
- For grants, a Kubernetes auth method ([Kubernetes auth](kubernetes-auth.md)); for layouts, an
  OIDC or JWT auth method that sends a groups claim ([JWT and OIDC auth](jwt-oidc-auth.md)).
- A `Ready` connection whose OpenBao policy allows the policy, role and identity group paths the
  access kinds write; [OpenBao policy for the operator](../operate/openbao-policy.md#platform-operator-in-the-root-namespace)
  lists them.

## 1. Define the levels

Each `AccessLevel` lists capabilities on KV data and on metadata. `versions: true` also grants the
version paths (delete, undelete, destroy):

```yaml title="examples/access/accesslevel.yaml"
--8<-- "examples/access/accesslevel.yaml"
```

A level cannot be deleted while a grant or layout cites it: it reports `InUse` and lists the
citing resources in `status.citedBy`.

## 2. Grant access to workloads

An `AccessGrant` lists subjects (ServiceAccounts, by name and namespace or by a namespace
selector) and rules of the form mount, path, level. In a path, `{sa.namespace}` and `{sa.name}`
become OpenBao identity templates, so each ServiceAccount reaches only its own path through one
shared policy. A broad subject, a glob or a selector, must scope every path with
`{sa.namespace}`; the CRD refuses it otherwise.

```yaml title="examples/access/accessgrant.yaml"
--8<-- "examples/access/accessgrant.yaml"
```

The three grants name the Kubernetes auth method in the three supported ways: `mountPath`,
`authMountRef` (an `AuthMount` resource) and `mountAccessor`. The grant looks the accessor up on
every reconcile. A workload logs in with the role in `status.roles`:

```bash
kubectl -n openbao-config get accessgrant payments-workloads -o jsonpath='{.status.roles}'
```

## 3. Give people access by group

An `AccessLayout` is a matrix: for each role of people, its level in each OpenBao namespace,
optionally repeated per organisation. `groupAliasTemplate` builds the name of the identity provider
group that each generated external group matches:

```yaml title="examples/access/accesslayout.yaml"
--8<-- "examples/access/accesslayout.yaml"
```

Here the identity provider group `acme_developer` gets `write` on `orgs/acme/` in the base
namespace and `read` in `prod`.

Narrowing a level or a path rewrites the policies at once. An update that would delete a policy or
group the layout created before, because a role, namespace, organisation or auth mount left the
spec, waits until the resource carries `openbaoconfig.io/allow-disruptive-update: "true"`. This
keeps a typo from removing people's access.

## 4. Check that it worked

```bash
kubectl -n openbao-config get accesslevels,accessgrants,accesslayouts
bao policy read grant_openbao-config_payments-workloads
```

The grants and layouts show `Ready` `True`, and OpenBao holds the rendered policy. Log in as a
subject ServiceAccount with the role from `status.roles` and read its path to prove the access.

## Troubleshooting

| symptom | check |
|---------|-------|
| `kubectl apply` refuses a grant with a message about `{sa.namespace}` | a glob or selector subject needs `{sa.namespace}` in every path |
| a grant or layout reports `ReferenceNotReady` | the `AccessLevel` it cites exists in the same Kubernetes namespace, and the auth mount exists |
| deleting an `AccessLevel` hangs with reason `InUse` | change or delete the resources in `status.citedBy` first |
| a layout change does not remove a group | removing access needs `openbaoconfig.io/allow-disruptive-update: "true"`; see [Troubleshooting](../operate/troubleshooting.md#accesslayout-refuses-to-remove-access) |
| a person gets no access | the identity provider group name against `groupAliasTemplate`, and the groups claim of the auth role |

## Related

- API reference: [AccessLevel](../reference/api/access.md#accesslevel),
  [AccessGrant](../reference/api/access.md#accessgrant),
  [AccessLayout](../reference/api/access.md#accesslayout).
- [Templated policies](templated-policies.md#accessgrant-renders-the-templates): how
  `{sa.namespace}` becomes an identity template.
- [Architecture: access models](../architecture.md#access-models).
