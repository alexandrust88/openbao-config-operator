# OpenBao policy for the operator

The OpenBao policy behind a connection's identity is the only real limit on what the operator can
do through that connection. Give each identity only the paths of the kinds it manages. This page
derives those paths from the path column of [kinds.md](../kinds.md) and from the requests the
operator actually sends.

Paths in a policy are relative to the namespace the policy is written in. A policy in the root
namespace reaches a child namespace with the child's path as a prefix, for example
`tenants/team-a/sys/policies/acl/*`.

## How the operator uses capabilities

| request | capability |
|---------|------------|
| read an object (every reconcile, and drift checks) | `read` |
| write an object | `create` for a new object, `update` for an existing one |
| delete an object (only with `deletionPolicy: Delete`, or the kind's default) | `delete` |
| list MFA methods to find one by name | `list` |

Leave out `delete` on kinds whose resources all use `deletionPolicy: Orphan`. Leave out `create`
when every resource uses `creationPolicy: Adopt`.

### sudo

OpenBao requires `sudo` in addition to the normal capability on these system paths. Only the
platform identity needs them, and only for the kinds that use them.

| path | kind |
|------|------|
| `sys/auth/<path>`, `sys/auth/<path>/tune` | `AuthMount` (enable, tune, disable) |
| `sys/audit`, `sys/audit/<path>` | `Audit` (list, enable, disable; root namespace only) |
| `sys/config/auditing/request-headers/<name>` | `AuditRequestHeader` |
| `sys/config/cors` | `CORSConfig` |
| `sys/config/ui/headers/<name>` | `UIHeader` |
| `sys/plugins/catalog/<type>/<name>` | `Plugin` |

Listing `sys/auth`, `sys/mounts` and `sys/plugins/catalog` needs only `read`. The operator never
uses `sys/raw`, `sys/rotate/keyring`,
`sys/rotate/root`, the token creation endpoints or the lease endpoints, so do not grant them.

## Connection probe

Every identity needs these, whatever kinds it manages:

| path | namespace | capability | why |
|------|-----------|------------|-----|
| `sys/health`, `sys/seal-status` | root | none | unauthenticated; the probe sends no token |
| `auth/<b>/login` | `authNamespace` | none | the login itself |
| `auth/token/lookup-self` | `authNamespace` | `read` | a static token is looked up for its TTL |
| `auth/token/renew-self` | `authNamespace` | `update` | renewable tokens are renewed after a third of their TTL |
| `auth/token/lookup-self` | connection `namespace` | `read` | the status probe checks authentication there |

The built-in `default` policy grants `auth/token/lookup-self` and `auth/token/renew-self` in the
namespace where it applies. Add them explicitly when the role sets `token_no_default_policy`.

When `authNamespace` is the root and `namespace` is a child, the identity also needs
`<namespace>/auth/token/lookup-self` in its root-namespace policy, as in the tenant example below.

## Platform operator in the root namespace

This policy covers the `sys`, auth, identity and secrets kinds for a platform identity based in
the root namespace. Remove every block for a kind you do not manage. Replace the `<mount>`
placeholders with your mount paths; do not widen them to `auth/*` or `+/*`, because that would
also grant token creation (`auth/token/create`) and every secret in every mount.

```hcl
# connection probe and token lifecycle
path "auth/token/lookup-self" { capabilities = ["read"] }
path "auth/token/renew-self"  { capabilities = ["update"] }

# Policy, PasswordPolicy; RandomSecret with passwordPolicyName reads .../generate
path "sys/policies/acl/*"      { capabilities = ["create", "read", "update", "delete"] }
path "sys/policies/password/*" { capabilities = ["create", "read", "update", "delete"] }

# OpenBaoNamespace
path "sys/namespaces/*" { capabilities = ["create", "read", "update", "delete"] }

# AuthMount: the table is read to observe; enable, tune and disable need sudo
path "sys/auth"   { capabilities = ["read"] }
path "sys/auth/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }

# SecretsMount
path "sys/mounts"   { capabilities = ["read"] }
path "sys/mounts/*" { capabilities = ["create", "read", "update", "delete"] }

# RateLimitQuota, QuotaConfig
path "sys/quotas/rate-limit/*" { capabilities = ["create", "read", "update", "delete"] }
path "sys/quotas/config"       { capabilities = ["read", "update"] }

# Audit (root namespace only; sudo)
path "sys/audit"   { capabilities = ["read", "sudo"] }
path "sys/audit/*" { capabilities = ["create", "update", "delete", "sudo"] }

# AuditRequestHeader, CORSConfig, UIHeader (sudo)
path "sys/config/auditing/request-headers/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }
path "sys/config/cors"                       { capabilities = ["read", "update", "delete", "sudo"] }
path "sys/config/ui/headers/*"               { capabilities = ["create", "read", "update", "delete", "sudo"] }

# Plugin (root namespace only; sudo)
path "sys/plugins/catalog"   { capabilities = ["read"] }
path "sys/plugins/catalog/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }

# Logger, KeyRotationConfig, RaftAutopilot, Workflow
path "sys/loggers/*"                            { capabilities = ["create", "read", "update", "delete"] }
path "sys/rotate/config"                        { capabilities = ["read", "update"] }
path "sys/storage/raft/autopilot/configuration" { capabilities = ["read", "update"] }
path "sys/workflows/manage/*"                   { capabilities = ["create", "read", "update", "delete"] }

# auth method config and roles, one block per mount
path "auth/<kubernetes-mount>/config" { capabilities = ["create", "read", "update", "delete"] }
path "auth/<kubernetes-mount>/role/*" { capabilities = ["create", "read", "update", "delete"] }
path "auth/<jwt-mount>/config"        { capabilities = ["create", "read", "update", "delete"] }
path "auth/<jwt-mount>/role/*"        { capabilities = ["create", "read", "update", "delete"] }
path "auth/<approle-mount>/role/*"    { capabilities = ["create", "read", "update", "delete"] }
path "auth/<userpass-mount>/users/*"  { capabilities = ["create", "read", "update", "delete"] }

# TokenRole
path "auth/token/roles/*" { capabilities = ["create", "read", "update", "delete"] }

# Entity, Group, GroupMembership
path "identity/entity/name/*" { capabilities = ["create", "read", "update", "delete"] }
path "identity/group/name/*"  { capabilities = ["create", "read", "update", "delete"] }
path "identity/group/id/*"    { capabilities = ["read", "update"] }

# EntityAlias, GroupAlias: found by (mount accessor, name) through the lookup endpoints
path "identity/entity-alias"      { capabilities = ["create", "update"] }
path "identity/entity-alias/id/*" { capabilities = ["read", "update", "delete"] }
path "identity/group-alias"       { capabilities = ["create", "update"] }
path "identity/group-alias/id/*"  { capabilities = ["read", "update", "delete"] }
path "identity/lookup/entity"     { capabilities = ["update"] }
path "identity/lookup/group"      { capabilities = ["update"] }

# OIDC provider and token kinds
path "identity/oidc/config"         { capabilities = ["read", "update"] }
path "identity/oidc/key/*"          { capabilities = ["create", "read", "update", "delete"] }
path "identity/oidc/role/*"         { capabilities = ["create", "read", "update", "delete"] }
path "identity/oidc/provider/*"     { capabilities = ["create", "read", "update", "delete"] }
path "identity/oidc/client/*"       { capabilities = ["create", "read", "update", "delete"] }
path "identity/oidc/scope/*"        { capabilities = ["create", "read", "update", "delete"] }
path "identity/oidc/assignment/*"   { capabilities = ["create", "read", "update", "delete"] }

# MFAMethod (listed to find a method by name), MFALoginEnforcement
path "identity/mfa/method"                 { capabilities = ["list"] }
path "identity/mfa/method/*"               { capabilities = ["create", "read", "update", "delete", "list"] }
path "identity/mfa/login-enforcement/*"    { capabilities = ["create", "read", "update", "delete"] }

# KVSecret and RandomSecret on a KV version 2 mount: delete removes the metadata (every version)
path "<kv-mount>/data/*"     { capabilities = ["create", "read", "update"] }
path "<kv-mount>/metadata/*" { capabilities = ["delete"] }
path "<kv-mount>/config"     { capabilities = ["read", "update"] }

# secrets engine kinds, one block per mount, for example PKI and database
path "<pki-mount>/roles/*"      { capabilities = ["create", "read", "update", "delete"] }
path "<database-mount>/config/*" { capabilities = ["create", "read", "update", "delete"] }
path "<database-mount>/roles/*"  { capabilities = ["create", "read", "update", "delete"] }
```

For the other secrets engine and auth kinds, take the path from [kinds.md](../kinds.md) and
grant the same four capabilities on it. `AccessGrant` and `AccessLayout` need the paths of the
objects they write: `sys/policies/acl/*`, `auth/<b>/role/*`, `identity/group/name/*`,
`identity/group-alias` and `identity/group-alias/id/*`, plus `read` on `sys/auth`.

Write the policy and bind it to the operator's role:

```sh
bao policy write openbao-config-operator platform-operator.hcl
bao write auth/kubernetes/role/openbao-config-operator \
  bound_service_account_names=openbao-config-operator \
  bound_service_account_namespaces=openbao-config-operator \
  audience=openbao token_policies=openbao-config-operator token_ttl=1h
```

## Tenant operator in a child namespace

A tenant connection is based in the tenant's OpenBao namespace (`spec.namespace`), so every
resource path is inside it. This example lets team `team-a`, whose connection logs in at the root
(`authNamespace: ""`) and works in `tenants/team-a`, manage:

- ACL policies named `team-a-*`;
- Kubernetes auth roles named `team-a-*` that bind only ServiceAccounts of the Kubernetes
  namespace `team-a`;
- KV version 2 secrets in the mount `secret`.

Written in the root namespace:

```hcl
# probe: login and renewal in the root, the status lookup in the tenant namespace
path "auth/token/lookup-self"                { capabilities = ["read"] }
path "auth/token/renew-self"                 { capabilities = ["update"] }
path "tenants/team-a/auth/token/lookup-self" { capabilities = ["read"] }

# Policy
path "tenants/team-a/sys/policies/acl/team-a-*" {
  capabilities = ["create", "read", "update", "delete"]
}

# KubernetesAuthRole, pinned to the tenant's Kubernetes namespace
path "tenants/team-a/auth/kubernetes/role/team-a-*" {
  capabilities = ["create", "read", "update", "delete"]
  allowed_parameters = {
    "bound_service_account_namespaces" = ["team-a"]
    "*"                                = []
  }
}

# KVSecret, RandomSecret
path "tenants/team-a/secret/data/*"     { capabilities = ["create", "read", "update"] }
path "tenants/team-a/secret/metadata/*" { capabilities = ["delete"] }
```

If the tenant's auth mount lives inside `tenants/team-a` (connection `authNamespace` unset), write
the same policy inside that namespace with `bao policy write -namespace=tenants/team-a` and drop
the `tenants/team-a/` prefix from every path.

Notes:

- A policy written in a child namespace grants paths only in that namespace and its children. A
  tenant that can write its own policies therefore cannot reach the root or a sibling namespace.
  The name prefix keeps the tenant away from policies the platform team writes in the same
  namespace.
- Do not grant `sys/auth/*` or `sys/mounts/*` to tenants unless they own their mounts. Mounts hold
  data; disabling one destroys every secret in it.
- `KubernetesAuthRole` sends `bound_service_account_namespaces` as a list; `allowed_parameters`
  checks every element. Without the restriction, a tenant could bind ServiceAccounts of another
  Kubernetes namespace to its roles.

## Check a policy

Log in as the identity and ask OpenBao what it may do on a path:

```sh
bao token capabilities -namespace=tenants/team-a sys/policies/acl/team-a-app
bao token capabilities -namespace=tenants/team-a sys/policies/acl/platform-admin
```

A resource that hits a missing capability reports `Synced=False` with `ReconcileError` and a
403 in the message ([troubleshooting.md](troubleshooting.md#common-openbao-errors)).
