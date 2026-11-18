---
title: Grant OpenBao KV access with levels, grants and layouts
description: Grant KV access to workloads and identity-provider groups with AccessLevel, AccessGrant and AccessLayout resources.
---

# Grant KV access with access levels, grants and layouts

Give workloads and people access to KV version 2 secrets without writing ACL policies by hand: the
OpenBao Config Operator writes the policies, roles and groups from three kinds:

| kind | for | writes in OpenBao |
|------|-----|-------------------|
| `AccessLevel` | a named set of capabilities, such as read, write or owner | nothing; grants and layouts cite it |
| `AccessGrant` | Kubernetes ServiceAccounts (machine access) | one policy `grant_<Kubernetes namespace>_<name>` and one Kubernetes auth role per subject |
| `AccessLayout` | identity provider groups (human access) | one policy per role, OpenBao namespace and organisation, and per auth mount an external group with a group alias |

A tenant is a boundary in Kubernetes and in OpenBao. How those namespaces relate is in
[Concepts: servers, connections and the tenant boundary](../concepts/index.md#servers-connections-and-the-tenant-boundary).

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/auth"                  { capabilities = ["read"] }
    path "sys/policies/acl/*"        { capabilities = ["create", "read", "update", "delete"] }
    path "auth/kubernetes/role/*"    { capabilities = ["create", "read", "update", "delete"] }
    path "identity/group/name/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "identity/group-alias"      { capabilities = ["create", "update"] }
    path "identity/group-alias/id/*" { capabilities = ["read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The KV version 2 mount `secret` exists
  ([Manage KV secrets from Kubernetes](kv-secrets.md)).
- For grants, the Kubernetes auth method at `kubernetes` and the ServiceAccount `app` in the
  Kubernetes namespace `apps` ([Configure Kubernetes authentication](kubernetes-auth.md), steps 1
  to 3); for layouts, the OIDC auth method at `oidc` with a role that sends a groups claim
  ([Configure JWT and OIDC authentication](jwt-oidc-auth.md)).
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Define an access level

Each `AccessLevel` lists capabilities on KV data and on metadata. `versions: true` also grants the
version paths (delete, undelete, destroy). Save this as `access-level.yaml`:

```yaml
apiVersion: access.obco.dev/v1alpha1
kind: AccessLevel
metadata:
  name: write
  namespace: openbao-config
spec:
  data:
    - create
    - read
    - update
    - patch
  metadata:
    - read
    - list
```

```bash
kubectl apply -f access-level.yaml
kubectl -n openbao-config wait accesslevel/write --for=condition=Ready --timeout=2m
```

A level cannot be deleted while a grant or layout cites it: it reports `InUse` and lists the
citing resources in `status.citedBy`. The example with
[every field](../reference/examples/access.md#accesslevel) shows the other options.

## 2. Grant access to workloads

An `AccessGrant` lists subjects (ServiceAccounts, by name and Kubernetes namespace or by a Kubernetes
namespace selector) and rules of the form mount, path, level. In a path, `{sa.namespace}` and
`{sa.name}` become OpenBao identity templates, so each ServiceAccount reaches only its own path
through one shared policy. A broad subject, a glob or a selector, must scope every path with
`{sa.namespace}`; the CRD refuses it otherwise. Save this as `access-grant.yaml`:

```yaml
apiVersion: access.obco.dev/v1alpha1
kind: AccessGrant
metadata:
  name: team-workloads
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: kubernetes
  subjects:
    - name: app
      namespace: apps
  rules:
    - mount: secret
      path: "{sa.namespace}/{sa.name}/*"
      metadataPath: "{sa.namespace}/{sa.name}/*"
      level: write
  tokenTTL: 15m
```

```bash
kubectl apply -f access-grant.yaml
kubectl -n openbao-config wait accessgrant/team-workloads --for=condition=Ready --timeout=2m
```

The ServiceAccount `app` gets `write` on `secret/data/apps/app/*`. A workload logs in with the
role in `status.roles`:

```bash
kubectl -n openbao-config get accessgrant team-workloads -o jsonpath='{.status.roles}'
```

## 3. Give people access by group

An `AccessLayout` is a matrix: for each role of people, its level in each OpenBao namespace,
optionally repeated per organisation. `groupAliasTemplate` builds the name of the identity
provider group that each generated external group matches. Save this as `access-layout.yaml`:

```yaml
apiVersion: access.obco.dev/v1alpha1
kind: AccessLayout
metadata:
  name: platform-teams
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  authMounts:
    - oidc
  organizations:
    - acme
  groupAliasTemplate: "{org}_{role}"
  paths:
    - mount: secret
      path: "{org}/+/*"
      metadataPath: "{org}/*"
  roles:
    - name: developer
      levels:
        - openbaoNamespace: ""
          level: write
```

```bash
kubectl apply -f access-layout.yaml
kubectl -n openbao-config wait accesslayout/platform-teams --for=condition=Ready --timeout=2m
```

Here the identity provider group `acme_developer` gets `write` on `secret/data/acme/<team>/*` in
the base OpenBao namespace, where `+` matches any one path segment. Narrowing a level or a path
rewrites the policies at once. An update that would delete a policy or group the layout created
before waits until the resource carries `obco.dev/allow-disruptive-update: "true"`. This
keeps a typo from removing people's access.

## 4. Check that it worked

Confirm the Kubernetes resources are ready, then that OpenBao holds the rendered policy:

```bash
kubectl -n openbao-config get accesslevels,accessgrants,accesslayouts
bao policy read grant_openbao-config_team-workloads
```

The grants and layouts show `Ready` `True`, and OpenBao holds the rendered policy. After a later
edit, check `Synced` as well, because `Ready` keeps its last value while a write fails. Log in as
the subject ServiceAccount with the role from `status.roles` and read its path to prove the access.

## Troubleshooting

| symptom | check |
|---------|-------|
| `kubectl apply` refuses a grant with a message about `{sa.namespace}` | a glob or selector subject needs `{sa.namespace}` in every path |
| a grant or layout reports `ReferenceNotReady` | the `AccessLevel` it cites exists in the same Kubernetes namespace, and the auth mount exists |
| deleting an `AccessLevel` hangs with reason `InUse` | change or delete the resources in `status.citedBy` first |
| a layout change does not remove a group | removing access needs `obco.dev/allow-disruptive-update: "true"`; see [Troubleshooting](../operate/troubleshooting.md#accesslayout-refuses-to-remove-access) |
| a person gets no access | the identity provider group name against `groupAliasTemplate`, and the groups claim of the auth role |

## Related

- API reference: [AccessLevel](../reference/api/access.md#accesslevel),
  [AccessGrant](../reference/api/access.md#accessgrant),
  [AccessLayout](../reference/api/access.md#accesslayout).
- [Write OpenBao templated policies with Kubernetes](templated-policies.md#accessgrant-renders-the-templates)
- [Architecture: access models](../architecture.md#access-models)
