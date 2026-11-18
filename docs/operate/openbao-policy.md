---
title: OpenBao policy for the operator
description: "Grant the OpenBao permissions required by operator-managed resources using scoped ACL policies."
---

# OpenBao policy for the operator

The OpenBao policy behind a connection's identity is the only real limit on what the operator can
do through that connection. Give each identity only the paths of the kinds it manages. This page
derives those paths from the path column of the [Kind catalogue](../kinds.md) and from the requests the
operator actually sends.

Paths in a policy are relative to the OpenBao namespace the policy is written in. A policy in the root
OpenBao namespace reaches a child with the child's path as a prefix, for example
`tenants/team-a/sys/policies/acl/*`.

## How the operator uses capabilities

| request | capability |
|---------|------------|
| read an object (every reconcile, and drift checks) | `read` |
| write an object | `create` for a new object, `update` for an existing one |
| delete an object (only with `deletionPolicy: Delete`) | `delete` |
| list MFA methods to find one by name | `list` |

Leave out `delete` on kinds whose resources all use `deletionPolicy: Orphan`. Leave out `create`
when every resource uses `creationPolicy: Adopt`.

### sudo

OpenBao requires `sudo` in addition to the normal capability on these system paths. Only the
platform identity needs them, and only for the kinds that use them.

| path | kind |
|------|------|
| `sys/auth/<path>`, `sys/auth/<path>/tune` | `AuthMount` (enable, tune, disable) |
| `sys/audit`, `sys/audit/<path>` | `Audit` (list, enable, disable; root OpenBao namespace only) |
| `sys/config/auditing/request-headers/<name>` | `AuditRequestHeader` |
| `sys/config/cors` | `CORSConfig` |
| `sys/config/ui/headers/<name>` | `UIHeader` |
| `sys/plugins/catalog/<type>/<name>` | `Plugin` |

Listing `sys/auth`, `sys/mounts` and `sys/plugins/catalog` needs only `read`. The operator never
uses `sys/raw`, `sys/rotate/keyring`,
`sys/rotate/root`, the token creation endpoints or the lease endpoints, so do not grant them.

## Connection probe

Every identity needs these, whatever kinds it manages:

| path | OpenBao namespace | capability | why |
|------|------------------|------------|-----|
| `sys/health`, `sys/seal-status` | root | none | unauthenticated; the probe sends no token |
| `auth/<b>/login` | `openbaoAuthNamespace` | none | the login itself |
| `auth/token/lookup-self` | `openbaoAuthNamespace` | `read` | a static token is looked up for its TTL |
| `auth/token/renew-self` | `openbaoAuthNamespace` | `update` | renewable tokens are renewed after a third of their TTL |
| `auth/token/revoke-self` | `openbaoAuthNamespace` | `update` | optional: the token of a dropped or replaced session is revoked; without it the token expires at its TTL |
| `auth/token/lookup-self` | connection `openbaoNamespace` | `read` | the status probe checks authentication there |

The built-in `default` policy grants `auth/token/lookup-self`, `auth/token/renew-self` and
`auth/token/revoke-self` in the OpenBao namespace where it applies. Add them explicitly when the
role sets `token_no_default_policy`.

When `openbaoAuthNamespace` is the root and `openbaoNamespace` is a child, the identity also needs
`<openbao-namespace>/auth/token/lookup-self` in its root-namespace policy, where `<openbao-namespace>` is the
connection's `openbaoNamespace`, as in the tenant example below.

## Platform operator in the root OpenBao namespace { #platform-operator-in-the-root-namespace }

This policy covers the `sys`, auth, identity and secrets kinds for a platform identity based in
the root OpenBao namespace. Remove every block for a kind you do not manage. Replace the `<mount>`
placeholders with your mount paths; do not widen them to `auth/*` or `+/*`, because that would
also grant token creation (`auth/token/create`) and every secret in every mount.

A resource with `spec.openbaoNamespace` sends its requests inside that OpenBao namespace, so this policy
needs the same path with `<spec.openbaoNamespace>/` in front. The `tenants/sys/namespaces/*` line shows
the rule for a `BaoNamespace` nested under `tenants`; repeat it for each parent you use.

```hcl
# connection probe and token lifecycle
path "auth/token/lookup-self" { capabilities = ["read"] }
path "auth/token/renew-self"  { capabilities = ["update"] }
path "auth/token/revoke-self" { capabilities = ["update"] }

# ACLPolicy, PasswordPolicy; RandomSecret with passwordPolicyName reads .../generate
path "sys/policies/acl/*"      { capabilities = ["create", "read", "update", "delete"] }
path "sys/policies/password/*" { capabilities = ["create", "read", "update", "delete"] }

# BaoNamespace; a nested one (spec.openbaoNamespace: tenants) writes under its parent
path "sys/namespaces/*"         { capabilities = ["create", "read", "update", "delete"] }
path "tenants/sys/namespaces/*" { capabilities = ["create", "read", "update", "delete"] }

# the auth table: AuthMount observes it; IdentityEntityAlias, IdentityGroupAlias, MFALoginEnforcement, AccessGrant,
# AccessLayout and ACLPolicy accessorRefs read it to resolve a mountPath to its accessor
path "sys/auth" { capabilities = ["read"] }

# AuthMount: enable, tune and disable need sudo
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

# Logger, KeyRotationConfig, RaftAutopilot, BaoWorkflow
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

# IdentityEntity, IdentityGroup, IdentityGroupMembership
path "identity/entity/name/*" { capabilities = ["create", "read", "update", "delete"] }
path "identity/group/name/*"  { capabilities = ["create", "read", "update", "delete"] }
path "identity/group/id/*"    { capabilities = ["read", "update"] }

# IdentityEntityAlias, IdentityGroupAlias: found by (mount accessor, name) through the lookup endpoints
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

# KVSecret and RandomSecret: destructive cleanup retains metadata and destroys bounded versions
path "<kv-mount>/data/*"     { capabilities = ["create", "read", "update"] }
path "<kv-mount>/metadata/*" { capabilities = ["read"] }
path "<kv-mount>/destroy/*"  { capabilities = ["update"] }
path "<kv-mount>/config"     { capabilities = ["read", "update"] }

# PKIRole, PKICELRole, and the PKIConfig, PKIKeyConfig, PKIACMEConfig, PKIAutoTidy sections
path "<pki-mount>/roles/*"     { capabilities = ["create", "read", "update", "delete"] }
path "<pki-mount>/cel/roles/*" { capabilities = ["create", "read", "update", "delete"] }
path "<pki-mount>/config/*"    { capabilities = ["read", "update"] }

# PKIKey: generate or import, then read, rename and delete by name or ID
path "<pki-mount>/keys/generate/*" { capabilities = ["update"] }
path "<pki-mount>/keys/import"     { capabilities = ["update"] }
path "<pki-mount>/key/*"           { capabilities = ["read", "update", "delete"] }

# PKIIssuer: generate a root, or an intermediate CSR that the signer signs and this mount imports
path "<pki-mount>/root/generate/*"         { capabilities = ["update"] }
path "<pki-mount>/intermediate/generate/*" { capabilities = ["update"] }
path "<pki-mount>/intermediate/set-signed" { capabilities = ["update"] }
path "<pki-mount>/issuer/*"                { capabilities = ["read", "update", "delete"] }
path "<signer-mount>/issuer/+/sign-intermediate" { capabilities = ["update"] }

# TransitKey (create, config, rotate, delete), TransitKeyConfig, TransitCacheConfig
path "<transit-mount>/keys/*"      { capabilities = ["create", "read", "update", "delete"] }
path "<transit-mount>/config/keys" { capabilities = ["read", "update"] }
path "<transit-mount>/cache-config" { capabilities = ["read", "update"] }

# DatabaseConnection, DatabaseRole, DatabaseStaticRole; rotate-root only with rotateRootOnCreate
path "<database-mount>/config/*"       { capabilities = ["create", "read", "update", "delete"] }
path "<database-mount>/roles/*"        { capabilities = ["create", "read", "update", "delete"] }
path "<database-mount>/static-roles/*" { capabilities = ["create", "read", "update", "delete"] }
path "<database-mount>/rotate-root/*"  { capabilities = ["update"] }
```

A `PKIIssuer` deletes its key with the issuer unless it used an existing key, so `delete` on
`<pki-mount>/key/*` is needed for issuers too. A `TransitKey` is deleted only with the
`obco.dev/allow-destructive-delete` annotation; the operator first sets
`deletion_allowed` through `<transit-mount>/keys/<name>/config`.

For the other secrets engine and auth kinds, take the path from the [Kind catalogue](../kinds.md) and
grant the same four capabilities on it. `AccessGrant` and `AccessLayout` need the paths of the
objects they write: `sys/policies/acl/*`, `auth/<b>/role/*`, `identity/group/name/*`,
`identity/group-alias` and `identity/group-alias/id/*`, plus `read` on `sys/auth`.

Save the HCL above as `platform-operator.hcl` and write the policy:

```bash
bao policy write openbao-config-operator platform-operator.hcl
```

Then bind it to the operator's login role as in
[Connect to OpenBao](../get-started/connect.md#log-in-as-the-operator), with
`token_policies=openbao-config-operator`.

## Tenant operator in a child OpenBao namespace { #tenant-operator-in-a-child-namespace }

A tenant connection is based in the tenant's OpenBao namespace (`spec.openbaoNamespace`), so every
resource path is inside it. This example lets the tenant `team-a`, whose connection logs in at the root
(`openbaoAuthNamespace: ""`) and works in `tenants/team-a`, manage:

- ACL policies named `team-a-*`;
- Kubernetes auth roles named `team-a-*` that bind only ServiceAccounts of the Kubernetes
  namespace `team-a`;
- KV version 2 secrets in the mount `secret`.

Written in the root OpenBao namespace:

```hcl
# probe: login and renewal in the root, the status lookup in the tenant namespace
path "auth/token/lookup-self"                { capabilities = ["read"] }
path "auth/token/renew-self"                 { capabilities = ["update"] }
path "auth/token/revoke-self"                { capabilities = ["update"] }
path "tenants/team-a/auth/token/lookup-self" { capabilities = ["read"] }

# ACLPolicy; the auth table resolves accessorRefs with a mountPath
path "tenants/team-a/sys/auth" { capabilities = ["read"] }
path "tenants/team-a/sys/policies/acl/team-a-*" {
  capabilities = ["create", "read", "update", "delete"]
}

# KubernetesAuthRole, pinned to the tenant's Kubernetes namespace
path "tenants/team-a/auth/kubernetes/role/team-a-*" {
  capabilities = ["create", "read", "update", "delete"]
  allowed_parameters = {
    "bound_service_account_namespaces"         = [["team-a"]]
    "bound_service_account_namespace_selector" = [""]
    "*"                                        = []
  }
}

# KVSecret, RandomSecret
path "tenants/team-a/secret/data/*"     { capabilities = ["create", "read", "update"] }
path "tenants/team-a/secret/metadata/*" { capabilities = ["read"] }
path "tenants/team-a/secret/destroy/*"  { capabilities = ["update"] }
```

If the tenant's auth mount lives inside `tenants/team-a` (connection `openbaoAuthNamespace` unset), write
the same policy inside that OpenBao namespace with `bao policy write -namespace=tenants/team-a` and drop
the `tenants/team-a/` prefix from every path. Login, renewal and the status lookup then all happen in
`tenants/team-a`, so delete the last line of the probe block: without the prefix it repeats
`auth/token/lookup-self`.

Notes:

- A policy written in a child OpenBao namespace grants paths only in that namespace and its children. A
  tenant that can write its own policies therefore cannot reach the root or a sibling OpenBao namespace.
  The name prefix keeps the tenant away from policies the platform team writes in the same
  OpenBao namespace. Name the tenant's own login policy and role outside the prefix (for example
  `configurer-team-a`): a name inside it lets the tenant rewrite its own permissions.
- Do not grant `sys/auth/*` or `sys/mounts/*` to tenants unless they own their mounts. Mounts hold
  data; disabling one destroys every secret in it.
- `KubernetesAuthRole` sends `bound_service_account_namespaces` as a list, and `allowed_parameters`
  compares that whole list with each allowed value. `[["team-a"]]` therefore allows exactly the list
  `["team-a"]`; the flat form `["team-a"]` would refuse every write. The operator always sends
  `bound_service_account_namespace_selector`, empty when unset, and OpenBao admits a ServiceAccount
  that matches either the list or the selector, so the selector is pinned to empty. Without these
  restrictions, a tenant could bind ServiceAccounts of another Kubernetes namespace to its roles.

## Check a policy

Log in as the identity and ask OpenBao what it may do on a path:

```bash
bao token capabilities -namespace=tenants/team-a sys/policies/acl/team-a-app
bao token capabilities -namespace=tenants/team-a sys/policies/acl/platform-admin
```

A resource that hits a missing capability reports `Synced=False` with `PermissionDenied` and a
403 in the message ([Troubleshooting](troubleshooting.md#common-openbao-errors)).

## Related

- [Kind catalogue](../kinds.md)
- [Connect to OpenBao](../get-started/connect.md)
- [Troubleshooting](troubleshooting.md)
- [Organise OpenBao namespaces and tenants with Kubernetes](../guides/namespaces-and-tenants.md)
