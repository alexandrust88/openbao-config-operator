# Kind catalogue

Every typed kind, with its implementation status. The design is in the
[architecture](architecture.md). The v2 typed catalogue is built-ins first: the
kinds compiled into OpenBao 2.7 are implemented and tested against `bao server -dev`, including
built-in kinds beyond the core set of the v2 spec. The external-plugin kinds stay in the code but
are disabled by default behind `--enable-plugin-kinds` (chart `pluginKinds.enabled`) until plugin
support ([phase 5](maintainers/roadmap.md#phases)) works end to end.

!!! warning "Plugin kinds are experimental"

    The 52 external-plugin kinds are experimental. They are not run against a server, and their paths marked
    `(u)` come from documentation, not OpenBao source. No coverage claim counts on them: the
    [API coverage](api-coverage.md) share for OpenBao 2.7.2, the figure this documentation cites, contains no
    plugin-kind path.

Counts: 120 rows, one per CRD in `config/crd/bases`: 68 built-in kinds, all integration-tested, and 52
plugin-gated kinds (the CRDs listed in `hack/chart/plugin-crds.txt`). The built-in kinds are `OpenBaoServer`, the two
connection kinds, `OpenBaoEndpoint` and 64 typed OpenBao kinds. `test/apidocs/kinds_catalogue_test.go` checks these
counts and the evidence paths. Features without a typed kind are listed under [not typed](#not-typed).

The API group domain `openbaoconfig.io` is final ([ADR 0008](adr/0008-provisional-api-group-domain.md)).

Destructive kinds delete only with the annotation `openbaoconfig.io/allow-destructive-delete: "true"`
([ADR 0013](adr/0013-destructive-delete-guard.md)): `SecretsMount`, `AuthMount`, `OpenBaoNamespace`, `KVSecret`,
`RandomSecret`, `Audit`, `OpenBaoEndpoint`, `TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`, `MFAMethod`
and `Entity`.

## Columns

- **group**: the section; the full group is `<group>.openbaoconfig.io`.
- **path**: OpenBao API path, relative to the resource's OpenBao namespace. `<m>` is a secrets
  mount path, `<b>` an auth mount path. `(u)` marks a path not yet confirmed in OpenBao source.
- **availability**:
  - `builtin`: compiled into OpenBao 2.7 (`internal/helper/builtinplugins/registry.go` or core).
  - `external-plugin`: needs a plugin from openbao-plugins (or a third party) registered with
    `Plugin`. Only LDAP, Kerberos and RADIUS are confirmed by OpenBao docs; the rest are `(u)`.
    The kind reports `Unsupported` until the plugin is mounted.
- **phase**: the [roadmap phase](maintainers/roadmap.md#phases) whose exit test covers the kind; the roadmap
  defines the phases and their releases. `after 6`: plugin kinds outside the core plan; they stay gated until
  each has a plugin and backend in CI.
- **status**:
  - `implemented`: types, adapter, unit and envtest tests; wired in `cmd/controllers.go`.
  - `integration-tested`: implemented, and reconciled against a real `bao server -dev` in the
    file named under evidence.
  - `plugin-gated`: experimental; implemented with unit tests and recorded reads, but not tested against a
    server, because OpenBao 2.7.2 does not ship the plugin; disabled by default.
  - `in progress`: being implemented now; not in the code yet.
  - `planned`: a later phase.
- **evidence**: test files under `test/integration/` (build tag `integration`), other bao-backed
  tests by full path, and `live` when the kind ran `Ready` on a live OpenBao 2.7.2 cluster
  (create, update, steady state with zero rewrites, and delete where `Delete` is allowed).
- **namespace scope** (in notes): every built-in kind works in the root and in any child namespace unless its
  notes say otherwise; root-only kinds are refused with `RootOnlyKind` before any OpenBao call. Measured per kind
  and connection mode by `test/nsmatrix` ([architecture](architecture.md#namespace-scope)).

The v2 engine rules (full-field comparison, path index, destructive guard, drift tiers) are being
retrofitted to every kind; until that lands, `integration-tested` means the v1 engine rules hold.
Write-only fields (absent from reads) are hashed into `status.writeOnlyHash` (HMAC), never
compared.

## core

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| `OpenBaoServer` | none (holds address, CA bundle, allowed namespaces and audiences, rate limit) | builtin | 1 | integration-tested | `internal/controller/core/server_bao_test.go`, `test/e2e` | cluster-scoped, platform-owned; connections name it with `serverRef` ([ADR 0010](adr/0010-openbaoserver-owns-address-and-audiences.md)) |
| `OpenBaoConnection` | login: `auth/<b>/login`; status: `sys/health`, `sys/seal-status`, `auth/token/lookup-self` | builtin | 1 | integration-tested | `internal/openbao/integration_test.go`, `internal/controller/core/connection_bao_test.go`, live | namespaced; Kubernetes/JWT auth uses TokenRequest for a ServiceAccount in the same namespace |
| `ClusterOpenBaoConnection` | same | builtin | 1 | integration-tested | `internal/openbao/integration_test.go`, `internal/controller/core/connection_bao_test.go` | cluster-scoped; `allowedNamespaces` selector |
| `OpenBaoEndpoint` | any | builtin | 1 | integration-tested | `integration_test.go`, live | escape hatch, see [ADR 0006](adr/0006-endpoint-escape-hatch.md); destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |

## sys

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| `Policy` | `sys/policies/acl/<name>` | builtin | 1 | integration-tested | `integration_test.go`, `sys_core_test.go`, `templated_acl_test.go`, live | ACL only; OpenBao has no EGP/RGP |
| `PasswordPolicy` | `sys/policies/password/<name>` | builtin | 4 | integration-tested | `sys_core_test.go`, live |  |
| `OpenBaoNamespace` | `sys/namespaces/<path>` | builtin | 2 | integration-tested | `sys_core_test.go`, `templated_acl_test.go`, live | namespace scope: any (creates a child of its OpenBao namespace); delete leaves it `tainted` until purged; sealable namespaces return key shares once (`writeSecretRef`); default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `AuthMount` | `sys/auth/<path>`, `sys/auth/<path>/tune` | builtin | 2 | integration-tested | `identity_test.go`, `sys_core_test.go`, `templated_acl_test.go`, live | `type`, `local`, `seal_wrap` immutable; accessor in status; default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `SecretsMount` | `sys/mounts/<path>`, `sys/mounts/<path>/tune` | builtin | 2 | integration-tested | `sys_core_test.go`, `templated_acl_test.go`, live | as `AuthMount`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `Audit` | `sys/audit/<path>` | builtin | 4 | integration-tested | `sys_audit_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); types file, socket, syslog, http; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `AuditRequestHeader` | `sys/config/auditing/request-headers/<name>` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); `hmac` flag |
| `RateLimitQuota` | `sys/quotas/rate-limit/<name>` | builtin | 4 | integration-tested | `sys_core_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); enforced per node; `group_by` and `secondary_rate` are not in OpenBao |
| `QuotaConfig` | `sys/quotas/config` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); singleton |
| `Plugin` | `sys/plugins/catalog/<auth\|database\|secret>/<name>` | builtin | 5 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere) |
| `CORSConfig` | `sys/config/cors` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); singleton |
| `UIHeader` | `sys/config/ui/headers/<name>` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere) |
| `Logger` | `sys/loggers/<name>` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); runtime log level; not persisted across a server restart (u), so drift repair re-applies it |
| `KeyRotationConfig` | `sys/rotate/config` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root or a sealable namespace (`Unsupported` elsewhere); singleton; barrier key rotation, not secret rotation |
| `RaftAutopilot` | `sys/storage/raft/autopilot/configuration` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); singleton; raft storage only |
| `Workflow` | `sys/workflows/manage/<path>` | builtin | 4 | integration-tested | `sys_config_test.go`, live | namespace scope: any; OpenBao only (profiles, 2.6+); extra |

## auth

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| `KubernetesAuthConfig` | `auth/<b>/config` | builtin | 2 | integration-tested | `auth_builtin_test.go`, `templated_acl_test.go`, live | `token_reviewer_jwt` write-only (read returns `token_reviewer_jwt_set`) |
| `KubernetesAuthRole` | `auth/<b>/role/<name>` | builtin | 2 | integration-tested | `auth_builtin_test.go`, `templated_acl_test.go`, live |  |
| `JWTAuthConfig` | `auth/<b>/config` | builtin | 2 | integration-tested | `auth_builtin_test.go`, live | `oidc_client_secret` write-only; sensitive `provider_config` keys stripped on read |
| `JWTAuthRole` | `auth/<b>/role/<name>` | builtin | 2 | integration-tested | `auth_builtin_test.go`, live |  |
| `JWTCELRole` | `auth/<b>/cel/role/<name>` | builtin | 4 | integration-tested | `auth_builtin_test.go`, live | OpenBao only; `cel_program` kept as opaque text; extra |
| `AppRole` | `auth/<b>/role/<name>` (+ `role/<name>/role-id`) | builtin | 2 | integration-tested | `auth_credentials_test.go`, live | `local_secret_ids` create-only |
| `AppRoleSecretID` | `auth/<b>/role/<name>/secret-id` | builtin | 2 | integration-tested | `auth_credentials_test.go`, live | generated `secret_id` returned once, written via `writeSecretRef`; create-only |
| `TokenRole` | `auth/token/roles/<name>` | builtin | 2 | integration-tested | `auth_credentials_test.go`, live |  |
| `CertAuthConfig` | `auth/<b>/config` | builtin | 7 | integration-tested | `auth_credentials_test.go`, live |  |
| `CertAuthRole` | `auth/<b>/certs/<name>` | builtin | 7 | integration-tested | `auth_credentials_test.go`, live |  |
| `UserpassUser` | `auth/<b>/users/<name>` | builtin | 7 | integration-tested | `auth_credentials_test.go`, live | `password` write-only, from a Secret ref |
| `LDAPAuthConfig` | `auth/<b>/config` | external-plugin | 6 | plugin-gated | - | `bindpass` write-only (u) |
| `LDAPAuthGroup` | `auth/<b>/groups/<name>` | external-plugin | 6 | plugin-gated | - |  |
| `LDAPAuthUser` | `auth/<b>/users/<name>` | external-plugin | 6 | plugin-gated | - |  |
| `KerberosAuthConfig` | `auth/<b>/config` | external-plugin | 6 | plugin-gated | - | keytab write-only (u) |
| `KerberosAuthLDAPConfig` | `auth/<b>/config/ldap` | external-plugin | 6 | plugin-gated | - |  |
| `KerberosAuthGroup` | `auth/<b>/groups/<name>` | external-plugin | 6 | plugin-gated | - |  |
| `RADIUSAuthConfig` | `auth/<b>/config` | external-plugin | 6 | plugin-gated | - | shared secret write-only (u) |
| `RADIUSAuthUser` | `auth/<b>/users/<name>` | external-plugin | 6 | plugin-gated | - |  |
| `AWSAuthConfig` | `auth/<b>/config/client` | external-plugin (u) | after 6 | plugin-gated | - | secret key write-only |
| `AWSAuthIdentityConfig` | `auth/<b>/config/identity` | external-plugin (u) | after 6 | plugin-gated | - | singleton |
| `AWSAuthRole` | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - | `auth_type` immutable |
| `AWSAuthCertificate` | `auth/<b>/config/certificate/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `AWSAuthSTSRole` | `auth/<b>/config/sts/<account>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `AWSAuthTidyConfig` | `auth/<b>/config/tidy/identity-whitelist`, `.../roletag-blacklist` (u) | external-plugin (u) | after 6 | plugin-gated | - | one kind, two singletons |
| `AzureAuthConfig` | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | client secret write-only |
| `AzureAuthRole` | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GCPAuthConfig` | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | credentials write-only |
| `GCPAuthRole` | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - | `type` immutable |
| `GitHubAuthConfig` | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GitHubAuthTeam` | `auth/<b>/map/teams/<team>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GitHubAuthUser` | `auth/<b>/map/users/<user>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `OktaAuthConfig` | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | API token write-only |
| `OktaAuthGroup` | `auth/<b>/groups/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `OktaAuthUser` | `auth/<b>/users/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `OCIAuthConfig` | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `OCIAuthRole` | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `CFAuthConfig` | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | Cloud Foundry |
| `CFAuthRole` | `auth/<b>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |

## secrets

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| `KVSecret` | v2: `<m>/data/<path>` (+ `<m>/metadata/<path>`); v1: `<m>/<path>` | builtin | 4 | integration-tested | `secrets_kv_test.go`, live | values from Secret refs; delete via metadata (`Deleter`); default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `KVConfig` | `<m>/config` | builtin | 4 | integration-tested | `secrets_kv_test.go`, live | singleton per mount: `max_versions`, `cas_required`, `delete_version_after` |
| `RandomSecret` | `<m>/data/<path>`; value from `sys/policies/password/<name>/generate` | builtin | 4 | integration-tested | `secrets_kv_test.go`, live | OpenBao generates the value from the password policy; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `DatabaseConnection` | `<m>/config/<name>` | builtin | 4 | integration-tested | `secrets_credentials_test.go`, live | `password`, `private_key` write-only; `connection_url` redacted on read; never re-send the password after `rotate-root`; builtin plugins: MySQL family, PostgreSQL, Cassandra, InfluxDB, Redis, Valkey |
| `DatabaseRole` | `<m>/roles/<name>` | builtin | 4 | integration-tested | `secrets_credentials_test.go`, live |  |
| `DatabaseStaticRole` | `<m>/static-roles/<name>` | builtin | 4 | integration-tested | `secrets_credentials_test.go` | password not returned |
| `PKIIssuer` | generate: `<m>/root/generate/<type>`, `<m>/intermediate/generate/<type>`; import: `<m>/config/ca`, `<m>/intermediate/set-signed`; config: `<m>/issuer/<ref>` (u) | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | multi-step and non-idempotent; exported private key returned once (`writeSecretRef`); CSR in status; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `PKIKey` | `<m>/key/<ref>`, `<m>/keys/generate/<type>`, `<m>/keys/import` (u) | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | extra; OpenBao external keys (`sys/external-keys`) are an alternative; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `PKIRole` | `<m>/roles/<name>` | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | OpenBao CEL policies not modelled |
| `PKIConfig` | `<m>/config/urls`, `<m>/config/crl`, `<m>/config/cluster`, `<m>/config/issuers` (u) | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | one kind for the per-mount singletons; shape to be decided |
| `PKIACMEConfig` | `<m>/config/acme` (u) | builtin (u) | 4 | integration-tested | `secrets_pki_test.go`, live |  |
| `PKIAutoTidy` | `<m>/config/auto-tidy` (u) | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | extra |
| `TransitKey` | `<m>/keys/<name>`, `<m>/keys/<name>/config` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live | `type`, `derived`, `convergent_encryption` immutable; key material never read; delete needs `deletion_allowed`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `SSHCA` | `<m>/config/ca` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live | `private_key` write-only (u); generated key cannot be replaced without delete; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `SSHRole` | `<m>/roles/<name>` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live |  |
| `KubernetesSecretsConfig` | `<m>/config` | builtin | 2 | integration-tested | `secrets_credentials_test.go`, live | `service_account_jwt` write-only |
| `KubernetesSecretsRole` | `<m>/roles/<name>` | builtin | 2 | integration-tested | `secrets_credentials_test.go`, live |  |
| `RabbitMQConfig` | `<m>/config/connection`, `<m>/config/lease` | builtin | 6 | integration-tested | `secrets_credentials_test.go`, live | `password` write-only; two writes (`Writer`); built into 2.7.2, listed under phase 6 in the v2 spec pending an open question |
| `RabbitMQRole` | `<m>/roles/<name>` | builtin | 6 | integration-tested | `secrets_credentials_test.go`, live |  |
| `TOTPKey` | `<m>/keys/<name>` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live | with `generate=true` the barcode and URL are returned once (u); destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `LDAPSecretsConfig` | `<m>/config` | external-plugin | 6 | plugin-gated | - | `bindpass` write-only |
| `LDAPSecretsDynamicRole` | `<m>/role/<name>` | external-plugin | 6 | plugin-gated | - |  |
| `LDAPSecretsStaticRole` | `<m>/static-role/<name>` | external-plugin | 6 | plugin-gated | - |  |
| `AWSSecretsConfig` | `<m>/config/root`, `<m>/config/lease` | external-plugin (u) | after 6 | plugin-gated | - | secret key write-only |
| `AWSSecretsRole` | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `AzureSecretsConfig` | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `AzureSecretsRole` | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GCPSecretsConfig` | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GCPRoleset` | `<m>/roleset/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GCPStaticAccount` | `<m>/static-account/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GCPImpersonatedAccount` | `<m>/impersonated-account/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `ConsulSecretsConfig` | `<m>/config/access` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `ConsulSecretsRole` | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `NomadSecretsConfig` | `<m>/config/access`, `<m>/config/lease` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `NomadSecretsRole` | `<m>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `TerraformCloudSecretsConfig` | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `TerraformCloudSecretsRole` | `<m>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `MongoDBAtlasSecretsConfig` | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `MongoDBAtlasSecretsRole` | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| `GitHubSecretsConfig` | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin; app private key write-only |
| `GitHubSecretsRole` | `<m>/permissionset/<name>` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |
| `QuaySecretsConfig` | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |
| `QuaySecretsRole` | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |
| `QuaySecretsStaticRole` | `<m>/static-roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |

## identity

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| `Entity` | `identity/entity/name/<name>` | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | ID server-generated, kept in `status.id`; default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `EntityAlias` | `identity/entity-alias/id/<id>` | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | no lookup by name; identity is (mount accessor, name), adopt by list and read |
| `Group` | `identity/group/name/<name>` | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | `type` internal/external immutable; default `Orphan` |
| `GroupAlias` | `identity/group-alias/id/<id>` | builtin | 3 | integration-tested | `identity_test.go`, live | external groups only |
| `GroupMembership` | `identity/group/id/<id>` (`member_entity_ids`, `member_group_ids`) | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | no endpoint of its own: read-modify-write of the group, removing only members it added |
| `OIDCConfig` | `identity/oidc/config` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live | singleton (`issuer`) |
| `OIDCKey` | `identity/oidc/key/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| `OIDCRole` | `identity/oidc/role/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| `OIDCProvider` | `identity/oidc/provider/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| `OIDCClient` | `identity/oidc/client/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live | `client_id`, `client_secret` server-generated; secret written via `writeSecretRef` |
| `OIDCScope` | `identity/oidc/scope/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| `OIDCAssignment` | `identity/oidc/assignment/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| `MFAMethod` | `identity/mfa/method/<totp\|okta\|duo\|pingid>/<id>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live | destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| `MFALoginEnforcement` | `identity/mfa/login-enforcement/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |

## access

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| `AccessLevel` | none (cited by grants and layouts) | builtin | 7 | integration-tested | `access_test.go`, `templated_acl_test.go`, live | `in-use` finalizer |
| `AccessGrant` | `sys/policies/acl/<name>`, `auth/<b>/role/<name>` | builtin | 7 | integration-tested | `access_test.go`, `templated_acl_test.go`, live | one Kubernetes auth role per subject |
| `AccessLayout` | `sys/policies/acl/<name>`, `identity/group/name/<name>`, `identity/group-alias` | builtin | 7 | integration-tested | `access_test.go`, live |  |

## Not typed

These features have no typed kind: OpenBao does not have them, or they are actions rather than configuration
([ADR 0009](adr/0009-openbao-only-target.md)).

| feature | availability | why not typed | reach it with |
|---------|--------------|---------------|---------------|
| Sentinel EGP and RGP policies | not-in-openbao | OpenBao has ACL policies only | - |
| KMIP secrets engine | not-in-openbao | no KMIP engine in OpenBao | - |
| Transform secrets engine | not-in-openbao | no Transform engine in OpenBao | - |
| managed keys | not-in-openbao | OpenBao has `sys/external-keys` instead, with a different API | `OpenBaoEndpoint`; a typed external-keys kind may follow |
| legacy step-up MFA (`sys/mfa/method/...`) | not-in-openbao | login MFA covers it | `MFAMethod` |
| lease count quotas | not-in-openbao | OpenBao has rate-limit quotas only | - |
| raft snapshot agent configuration | not-in-openbao | OpenBao uses a separate snapshot agent | - |
| secret rotation policies (`sys/policies/rotation/<name>`) | not-in-openbao (u) | no secret rotation manager in OpenBao | - |
| container plugin runtimes | not-in-openbao (u) | not found in OpenBao | `OpenBaoEndpoint` |
| pinned plugin versions | not-in-openbao (u) | OpenBao 2.7 pins plugins differently; to verify | `OpenBaoEndpoint` |
| Active Directory secrets engine | not-in-openbao | deprecated upstream; the LDAP engine replaces it | `LDAPSecretsConfig` |
| AliCloud auth | not-in-openbao (u) | not built in; no OpenBao plugin confirmed | `OpenBaoEndpoint` |
| token creation | action-not-config | creating a token is an action; a token in a Secret is a credential to rotate, not config | `TokenRole` + workload login |
| logins (AppRole, AWS) | action-not-config | a login | - |
| AWS role tags | action-not-config | creates a role tag value each call | - |
| PKI certificate issuing and signing | action-not-config | issuing or signing creates a new certificate each time | cert-manager with an OpenBao issuer; `PKIIssuer` for the CA chain |
| reading dynamic credentials | action-not-config | each read creates new credentials | External Secrets Operator |
| copying secret values into Kubernetes Secrets | builtin | out of scope ([ADR 0007](adr/0007-no-secret-sync.md)) | External Secrets Operator |

## Not catalogued

Reachable with `OpenBaoEndpoint` and candidates for later typed kinds: transit cache config, OpenBao
sealable-namespace operations, user lockout config (tune `user_lockout_config` on `AuthMount`), plugin reload,
control groups, and the namespace API lock.
