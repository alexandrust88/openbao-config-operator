---
description: "Browse supported OpenBao custom resources, their managed API paths, implementation status and plugin requirements."
---

# Kind catalogue

Every typed kind, with links to its fields, YAML examples and test evidence. The design is in the
[architecture](architecture.md). Built-in kinds are implemented; their evidence identifies the tests and
runtime each kind needs. A dev server alone cannot exercise cluster-specific features such as raft autopilot.
The remaining built-in gaps are in [BL-009](project/backlog.md). The external-plugin kinds stay in the code but
are disabled by default behind `--enable-plugin-kinds` (chart `pluginKinds.enabled`) until plugin
support ([phase 5](maintainers/roadmap.md#phases)) works end to end.

!!! warning "Plugin kinds are experimental"

    The 52 external-plugin kinds are experimental. They are not run against a server, and their paths marked
    `(u)` come from documentation, not OpenBao source. No coverage claim counts on them: the
    [API coverage](api-coverage.md) share for OpenBao 2.7.2, the figure this documentation cites, contains no
    plugin-kind path.

Counts: 126 rows, one per CRD in `config/crd/bases`: 74 built-in kinds, all integration-tested, and 52
plugin-gated kinds (the CRDs listed in `hack/chart/plugin-crds.txt`). The built-in kinds are `OpenBaoServer`, the two
connection kinds, `OpenBaoEndpoint` and 70 typed OpenBao kinds. `test/apidocs/kinds_catalogue_test.go` checks these
counts and the evidence paths. Features without a typed kind are listed under [not typed](#not-typed).

The API group domain `openbaoconfig.io` is final ([ADR 0008](adr/0008-provisional-api-group-domain.md)).

Destructive kinds delete only with the annotation `openbaoconfig.io/allow-destructive-delete: "true"`
([ADR 0013](adr/0013-destructive-delete-guard.md)): `SecretsMount`, `AuthMount`, `OpenBaoNamespace`, `KVSecret`,
`RandomSecret`, `Audit`, `OpenBaoEndpoint`, `TransitKey`, `PKIIssuer`, `PKIKey`, `SSHCA`, `TOTPKey`, `MFAMethod`,
`Entity` and `KVSecretMetadata`.

## Columns

- **kind**: links to the complete API reference; **example** opens the YAML gallery for that kind.
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

The engine applies the path index, the destructive guard and the drift tiers to every kind. Every built-in
adapter returns its full payload with declared defaults, and the no-write and default conformance tests measure
each built-in kind, except the cases in `test/conformance/exceptions.yaml`
([not yet enforced](architecture.md#not-yet-enforced)). Write-only fields (absent from reads) are hashed into
`status.writeOnlyHash` (HMAC), never compared.

## core

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| [`OpenBaoServer`](reference/api/core-openbaoserver.md) · [example](reference/examples/core.md#openbaoserver) | none (holds address, CA bundle, allowed namespaces and audiences, rate limit) | builtin | 1 | integration-tested | `internal/controller/core/server_bao_test.go`, `test/e2e` | cluster-scoped, platform-owned; connections name it with `serverRef` ([ADR 0010](adr/0010-openbaoserver-owns-address-and-audiences.md)) |
| [`OpenBaoConnection`](reference/api/core-openbaoconnection.md) · [example](reference/examples/core.md#openbaoconnection) | login: `auth/<b>/login`; status: `sys/health`, `sys/seal-status`, `auth/token/lookup-self` | builtin | 1 | integration-tested | `internal/openbao/integration_test.go`, `internal/controller/core/connection_bao_test.go`, live | namespaced; Kubernetes/JWT auth uses TokenRequest for a ServiceAccount in the same namespace |
| [`ClusterOpenBaoConnection`](reference/api/core-clusteropenbaoconnection.md) · [example](reference/examples/core.md#clusteropenbaoconnection) | same | builtin | 1 | integration-tested | `internal/openbao/integration_test.go`, `internal/controller/core/connection_bao_test.go` | cluster-scoped; `allowedNamespaces` selector |
| [`OpenBaoEndpoint`](reference/api/core-openbaoendpoint.md) · [example](reference/examples/core.md#openbaoendpoint) | any | builtin | 1 | integration-tested | `integration_test.go`, live | escape hatch, see [ADR 0006](adr/0006-endpoint-escape-hatch.md); destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |

## sys

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| [`Policy`](reference/api/sys-policy.md) · [example](reference/examples/sys.md#policy) | `sys/policies/acl/<name>` | builtin | 1 | integration-tested | `integration_test.go`, `sys_core_test.go`, `templated_acl_test.go`, live | ACL only; OpenBao has no EGP/RGP |
| [`PasswordPolicy`](reference/api/sys-passwordpolicy.md) · [example](reference/examples/sys.md#passwordpolicy) | `sys/policies/password/<name>` | builtin | 4 | integration-tested | `sys_core_test.go`, live |  |
| [`OpenBaoNamespace`](reference/api/sys-openbaonamespace.md) · [example](reference/examples/sys.md#openbaonamespace) | `sys/namespaces/<path>` | builtin | 2 | integration-tested | `sys_core_test.go`, `templated_acl_test.go`, live | namespace scope: any (creates a child of its OpenBao namespace); delete leaves it `tainted` until purged (`ReconcileInProgress`); namespace sealing is not supported yet; default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`AuthMount`](reference/api/sys-authmount.md) · [example](reference/examples/sys.md#authmount) | `sys/auth/<path>`, `sys/auth/<path>/tune` | builtin | 2 | integration-tested | `identity_test.go`, `sys_core_test.go`, `templated_acl_test.go`, live | `type`, `local`, `seal_wrap` immutable; accessor in status; default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`SecretsMount`](reference/api/sys-secretsmount.md) · [example](reference/examples/sys.md#secretsmount) | `sys/mounts/<path>`, `sys/mounts/<path>/tune` | builtin | 2 | integration-tested | `sys_core_test.go`, `templated_acl_test.go`, live | as `AuthMount`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`Audit`](reference/api/sys-audit.md) · [example](reference/examples/sys.md#audit) | `sys/audit/<path>` | builtin | 4 | integration-tested | `sys_audit_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); types file, socket, syslog, http; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`AuditRequestHeader`](reference/api/sys-auditrequestheader.md) · [example](reference/examples/sys.md#auditrequestheader) | `sys/config/auditing/request-headers/<name>` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); `hmac` flag |
| [`RateLimitQuota`](reference/api/sys-ratelimitquota.md) · [example](reference/examples/sys.md#ratelimitquota) | `sys/quotas/rate-limit/<name>` | builtin | 4 | integration-tested | `sys_core_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); enforced per node; `group_by` and `secondary_rate` are not in OpenBao |
| [`QuotaConfig`](reference/api/sys-quotaconfig.md) · [example](reference/examples/sys.md#quotaconfig) | `sys/quotas/config` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); singleton |
| [`Plugin`](reference/api/sys-plugin.md) · [example](reference/examples/sys.md#plugin) | `sys/plugins/catalog/<auth\|database\|secret>/<name>` | builtin | 5 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere) |
| [`CORSConfig`](reference/api/sys-corsconfig.md) · [example](reference/examples/sys.md#corsconfig) | `sys/config/cors` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); singleton |
| [`UIHeader`](reference/api/sys-uiheader.md) · [example](reference/examples/sys.md#uiheader) | `sys/config/ui/headers/<name>` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere) |
| [`Logger`](reference/api/sys-logger.md) · [example](reference/examples/sys.md#logger) | `sys/loggers/<name>` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); runtime log level; not persisted across a server restart (u), so drift repair re-applies it |
| [`KeyRotationConfig`](reference/api/sys-keyrotationconfig.md) · [example](reference/examples/sys.md#keyrotationconfig) | `sys/rotate/config` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root or a sealable namespace (`Unsupported` elsewhere); singleton; barrier key rotation, not secret rotation |
| [`RaftAutopilot`](reference/api/sys-raftautopilot.md) · [example](reference/examples/sys.md#raftautopilot) | `sys/storage/raft/autopilot/configuration` | builtin | 4 | integration-tested | `sys_config_test.go` | namespace scope: root only (`RootOnlyKind` elsewhere); singleton; raft storage only |
| [`Workflow`](reference/api/sys-workflow.md) · [example](reference/examples/sys.md#workflow) | `sys/workflows/manage/<path>` | builtin | 4 | integration-tested | `sys_config_test.go`, live | namespace scope: any; OpenBao only (profiles, 2.6+); extra |

## auth

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| [`KubernetesAuthConfig`](reference/api/auth-kubernetesauthconfig.md) · [example](reference/examples/auth.md#kubernetesauthconfig) | `auth/<b>/config` | builtin | 2 | integration-tested | `auth_builtin_test.go`, `templated_acl_test.go`, live | `token_reviewer_jwt` write-only (read returns `token_reviewer_jwt_set`) |
| [`KubernetesAuthRole`](reference/api/auth-kubernetesauthrole.md) · [example](reference/examples/auth.md#kubernetesauthrole) | `auth/<b>/role/<name>` | builtin | 2 | integration-tested | `auth_builtin_test.go`, `templated_acl_test.go`, live |  |
| [`JWTAuthConfig`](reference/api/auth-jwtauthconfig.md) · [example](reference/examples/auth.md#jwtauthconfig) | `auth/<b>/config` | builtin | 2 | integration-tested | `auth_builtin_test.go`, live | `oidc_client_secret` write-only; sensitive `provider_config` keys stripped on read |
| [`JWTAuthRole`](reference/api/auth-jwtauthrole.md) · [example](reference/examples/auth.md#jwtauthrole) | `auth/<b>/role/<name>` | builtin | 2 | integration-tested | `auth_builtin_test.go`, live |  |
| [`JWTCELRole`](reference/api/auth-jwtcelrole.md) · [example](reference/examples/auth.md#jwtcelrole) | `auth/<b>/cel/role/<name>` | builtin | 4 | integration-tested | `auth_builtin_test.go`, live | OpenBao only; `cel_program` kept as opaque text; extra |
| [`AppRole`](reference/api/auth-approle.md) · [example](reference/examples/auth.md#approle) | `auth/<b>/role/<name>` (+ `role/<name>/role-id`) | builtin | 2 | integration-tested | `auth_credentials_test.go`, live | `local_secret_ids` create-only |
| [`AppRoleSecretID`](reference/api/auth-approlesecretid.md) · [example](reference/examples/auth.md#approlesecretid) | `auth/<b>/role/<name>/secret-id` | builtin | 2 | integration-tested | `auth_credentials_test.go`, live | generated `secret_id` returned once, written via `writeSecretRef`; create-only |
| [`TokenRole`](reference/api/auth-tokenrole.md) · [example](reference/examples/auth.md#tokenrole) | `auth/token/roles/<name>` | builtin | 2 | integration-tested | `auth_credentials_test.go`, live |  |
| [`CertAuthConfig`](reference/api/auth-certauthconfig.md) · [example](reference/examples/auth.md#certauthconfig) | `auth/<b>/config` | builtin | 7 | integration-tested | `auth_credentials_test.go`, live |  |
| [`CertAuthRole`](reference/api/auth-certauthrole.md) · [example](reference/examples/auth.md#certauthrole) | `auth/<b>/certs/<name>` | builtin | 7 | integration-tested | `auth_credentials_test.go`, live |  |
| [`CertAuthCRL`](reference/api/auth-certauthcrl.md) · [example](reference/examples/auth.md#certauthcrl) | `auth/<b>/crls/<name>` | builtin | 7 | integration-tested | `secrets_gaps_test.go`, live | exactly one of `crl` (write-only PEM) or `url`; a missing CRL is HTTP 400 |
| [`UserpassUser`](reference/api/auth-userpassuser.md) · [example](reference/examples/auth.md#userpassuser) | `auth/<b>/users/<name>` | builtin | 7 | integration-tested | `auth_credentials_test.go`, live | `password` write-only, from a Secret ref |
| [`LDAPAuthConfig`](reference/api/auth-ldapauthconfig.md) · [example](reference/examples/auth.md#ldapauthconfig) | `auth/<b>/config` | external-plugin | 6 | plugin-gated | - | `bindpass` write-only (u) |
| [`LDAPAuthGroup`](reference/api/auth-ldapauthgroup.md) · [example](reference/examples/auth.md#ldapauthgroup) | `auth/<b>/groups/<name>` | external-plugin | 6 | plugin-gated | - |  |
| [`LDAPAuthUser`](reference/api/auth-ldapauthuser.md) · [example](reference/examples/auth.md#ldapauthuser) | `auth/<b>/users/<name>` | external-plugin | 6 | plugin-gated | - |  |
| [`KerberosAuthConfig`](reference/api/auth-kerberosauthconfig.md) · [example](reference/examples/auth.md#kerberosauthconfig) | `auth/<b>/config` | external-plugin | 6 | plugin-gated | - | keytab write-only (u) |
| [`KerberosAuthLDAPConfig`](reference/api/auth-kerberosauthldapconfig.md) · [example](reference/examples/auth.md#kerberosauthldapconfig) | `auth/<b>/config/ldap` | external-plugin | 6 | plugin-gated | - |  |
| [`KerberosAuthGroup`](reference/api/auth-kerberosauthgroup.md) · [example](reference/examples/auth.md#kerberosauthgroup) | `auth/<b>/groups/<name>` | external-plugin | 6 | plugin-gated | - |  |
| [`RADIUSAuthConfig`](reference/api/auth-radiusauthconfig.md) · [example](reference/examples/auth.md#radiusauthconfig) | `auth/<b>/config` | external-plugin | 6 | plugin-gated | - | shared secret write-only (u) |
| [`RADIUSAuthUser`](reference/api/auth-radiusauthuser.md) · [example](reference/examples/auth.md#radiusauthuser) | `auth/<b>/users/<name>` | external-plugin | 6 | plugin-gated | - |  |
| [`AWSAuthConfig`](reference/api/auth-awsauthconfig.md) · [example](reference/examples/auth.md#awsauthconfig) | `auth/<b>/config/client` | external-plugin (u) | after 6 | plugin-gated | - | secret key write-only |
| [`AWSAuthIdentityConfig`](reference/api/auth-awsauthidentityconfig.md) · [example](reference/examples/auth.md#awsauthidentityconfig) | `auth/<b>/config/identity` | external-plugin (u) | after 6 | plugin-gated | - | singleton |
| [`AWSAuthRole`](reference/api/auth-awsauthrole.md) · [example](reference/examples/auth.md#awsauthrole) | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - | `auth_type` immutable |
| [`AWSAuthCertificate`](reference/api/auth-awsauthcertificate.md) · [example](reference/examples/auth.md#awsauthcertificate) | `auth/<b>/config/certificate/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`AWSAuthSTSRole`](reference/api/auth-awsauthstsrole.md) · [example](reference/examples/auth.md#awsauthstsrole) | `auth/<b>/config/sts/<account>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`AWSAuthTidyConfig`](reference/api/auth-awsauthtidyconfig.md) · [example](reference/examples/auth.md#awsauthtidyconfig) | `auth/<b>/config/tidy/identity-whitelist`, `.../roletag-blacklist` (u) | external-plugin (u) | after 6 | plugin-gated | - | one kind, two singletons |
| [`AzureAuthConfig`](reference/api/auth-azureauthconfig.md) · [example](reference/examples/auth.md#azureauthconfig) | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | client secret write-only |
| [`AzureAuthRole`](reference/api/auth-azureauthrole.md) · [example](reference/examples/auth.md#azureauthrole) | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GCPAuthConfig`](reference/api/auth-gcpauthconfig.md) · [example](reference/examples/auth.md#gcpauthconfig) | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | credentials write-only |
| [`GCPAuthRole`](reference/api/auth-gcpauthrole.md) · [example](reference/examples/auth.md#gcpauthrole) | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - | `type` immutable |
| [`GitHubAuthConfig`](reference/api/auth-githubauthconfig.md) · [example](reference/examples/auth.md#githubauthconfig) | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GitHubAuthTeam`](reference/api/auth-githubauthteam.md) · [example](reference/examples/auth.md#githubauthteam) | `auth/<b>/map/teams/<team>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GitHubAuthUser`](reference/api/auth-githubauthuser.md) · [example](reference/examples/auth.md#githubauthuser) | `auth/<b>/map/users/<user>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`OktaAuthConfig`](reference/api/auth-oktaauthconfig.md) · [example](reference/examples/auth.md#oktaauthconfig) | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | API token write-only |
| [`OktaAuthGroup`](reference/api/auth-oktaauthgroup.md) · [example](reference/examples/auth.md#oktaauthgroup) | `auth/<b>/groups/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`OktaAuthUser`](reference/api/auth-oktaauthuser.md) · [example](reference/examples/auth.md#oktaauthuser) | `auth/<b>/users/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`OCIAuthConfig`](reference/api/auth-ociauthconfig.md) · [example](reference/examples/auth.md#ociauthconfig) | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`OCIAuthRole`](reference/api/auth-ociauthrole.md) · [example](reference/examples/auth.md#ociauthrole) | `auth/<b>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`CFAuthConfig`](reference/api/auth-cfauthconfig.md) · [example](reference/examples/auth.md#cfauthconfig) | `auth/<b>/config` | external-plugin (u) | after 6 | plugin-gated | - | Cloud Foundry |
| [`CFAuthRole`](reference/api/auth-cfauthrole.md) · [example](reference/examples/auth.md#cfauthrole) | `auth/<b>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |

## secrets

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| [`KVSecret`](reference/api/secrets-kvsecret.md) · [example](reference/examples/secrets.md#kvsecret) | v2: `<m>/data/<path>` (+ `<m>/metadata/<path>`); v1: `<m>/<path>` | builtin | 4 | integration-tested | `secrets_kv_test.go`, live | values from Secret refs; delete via metadata (`Deleter`); default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`KVConfig`](reference/api/secrets-kvconfig.md) · [example](reference/examples/secrets.md#kvconfig) | `<m>/config` | builtin | 4 | integration-tested | `secrets_kv_test.go`, live | singleton per mount: `max_versions`, `cas_required`, `delete_version_after` |
| [`KVSecretMetadata`](reference/api/secrets-kvsecretmetadata.md) · [example](reference/examples/secrets.md#kvsecretmetadata) | `<m>/metadata/<path>` | builtin | 4 | integration-tested | `secrets_gaps_test.go`, live | per-secret retention and `custom_metadata`; an omitted field is left stored, and a set map replaces the stored keys; delete removes every version, so it is destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`RandomSecret`](reference/api/secrets-randomsecret.md) · [example](reference/examples/secrets.md#randomsecret) | `<m>/data/<path>`; value from `sys/policies/password/<name>/generate` | builtin | 4 | integration-tested | `secrets_kv_test.go`, live | OpenBao generates the value from the password policy; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`DatabaseConnection`](reference/api/secrets-databaseconnection.md) · [example](reference/examples/secrets.md#databaseconnection) | `<m>/config/<name>` | builtin | 4 | integration-tested | `secrets_credentials_test.go`, live | `password`, `private_key` write-only; `connection_url` redacted on read; never re-send the password after `rotate-root`; builtin plugins: MySQL family, PostgreSQL, Cassandra, InfluxDB, Redis, Valkey |
| [`DatabaseRole`](reference/api/secrets-databaserole.md) · [example](reference/examples/secrets.md#databaserole) | `<m>/roles/<name>` | builtin | 4 | integration-tested | `secrets_credentials_test.go`, live |  |
| [`DatabaseStaticRole`](reference/api/secrets-databasestaticrole.md) · [example](reference/examples/secrets.md#databasestaticrole) | `<m>/static-roles/<name>` | builtin | 4 | integration-tested | `secrets_credentials_test.go` | password not returned |
| [`PKIIssuer`](reference/api/secrets-pkiissuer.md) · [example](reference/examples/secrets.md#pkiissuer) | generate: `<m>/root/generate/<type>`, `<m>/intermediate/generate/<type>`; import: `<m>/config/ca`, `<m>/intermediate/set-signed`; config: `<m>/issuer/<ref>` | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | multi-step and non-idempotent; exported private key returned once (`writeSecretRef`); CSR in status; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`PKIKey`](reference/api/secrets-pkikey.md) · [example](reference/examples/secrets.md#pkikey) | `<m>/key/<ref>`, `<m>/keys/generate/<type>`, `<m>/keys/import` | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | extra; OpenBao external keys (`sys/external-keys`) are an alternative; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`PKIRole`](reference/api/secrets-pkirole.md) · [example](reference/examples/secrets.md#pkirole) | `<m>/roles/<name>` | builtin | 4 | integration-tested | `secrets_pki_test.go`, live |  |
| [`PKICELRole`](reference/api/secrets-pkicelrole.md) · [example](reference/examples/secrets.md#pkicelrole) | `<m>/cel/roles/<name>` | builtin | 4 | integration-tested | `secrets_gaps_test.go`, live | `cel_program` is the expression and its variables |
| [`PKIKeyConfig`](reference/api/secrets-pkikeyconfig.md) · [example](reference/examples/secrets.md#pkikeyconfig) | `<m>/config/keys` | builtin | 4 | integration-tested | `secrets_gaps_test.go`, live | singleton; `default` may be a key name, which OpenBao stores as the key id; the name `default` is reserved; no delete, so deletionPolicy is Orphan |
| [`PKIConfig`](reference/api/secrets-pkiconfig.md) · [example](reference/examples/secrets.md#pkiconfig) | `<m>/config/urls`, `<m>/config/crl`, `<m>/config/cluster`, `<m>/config/issuers` | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | one kind for the per-mount singletons |
| [`PKIACMEConfig`](reference/api/secrets-pkiacmeconfig.md) · [example](reference/examples/secrets.md#pkiacmeconfig) | `<m>/config/acme` | builtin (u) | 4 | integration-tested | `secrets_pki_test.go`, live |  |
| [`PKIAutoTidy`](reference/api/secrets-pkiautotidy.md) · [example](reference/examples/secrets.md#pkiautotidy) | `<m>/config/auto-tidy` | builtin | 4 | integration-tested | `secrets_pki_test.go`, live | extra |
| [`TransitKey`](reference/api/secrets-transitkey.md) · [example](reference/examples/secrets.md#transitkey) | `<m>/keys/<name>`, `<m>/keys/<name>/config` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live | `type`, `derived`, `convergent_encryption` immutable; key material never read; delete needs `deletion_allowed`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`TransitCacheConfig`](reference/api/secrets-transitcacheconfig.md) · [example](reference/examples/secrets.md#transitcacheconfig) | `<m>/cache-config` | builtin | 4 | integration-tested | `secrets_gaps_test.go`, live | singleton; `size` is 0 or at least 10; 0 is an unlimited cache; no delete, so deletionPolicy is Orphan |
| [`TransitKeyConfig`](reference/api/secrets-transitkeyconfig.md) · [example](reference/examples/secrets.md#transitkeyconfig) | `<m>/config/keys` | builtin | 4 | integration-tested | `secrets_gaps_test.go`, live | singleton; `disable_upsert`; no delete, so deletionPolicy is Orphan |
| [`SSHCA`](reference/api/secrets-sshca.md) · [example](reference/examples/secrets.md#sshca) | `<m>/config/ca` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live | `private_key` write-only (u); generated key cannot be replaced without delete; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`SSHRole`](reference/api/secrets-sshrole.md) · [example](reference/examples/secrets.md#sshrole) | `<m>/roles/<name>` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live |  |
| [`KubernetesSecretsConfig`](reference/api/secrets-kubernetessecretsconfig.md) · [example](reference/examples/secrets.md#kubernetessecretsconfig) | `<m>/config` | builtin | 2 | integration-tested | `secrets_credentials_test.go`, live | `service_account_jwt` write-only |
| [`KubernetesSecretsRole`](reference/api/secrets-kubernetessecretsrole.md) · [example](reference/examples/secrets.md#kubernetessecretsrole) | `<m>/roles/<name>` | builtin | 2 | integration-tested | `secrets_credentials_test.go`, live |  |
| [`RabbitMQConfig`](reference/api/secrets-rabbitmqconfig.md) · [example](reference/examples/secrets.md#rabbitmqconfig) | `<m>/config/connection`, `<m>/config/lease` | builtin | 6 | integration-tested | `secrets_credentials_test.go`, live | `password` write-only; two writes (`Writer`); built into 2.7.2; listed under phase 6 pending an [open question](maintainers/roadmap.md#open-questions) |
| [`RabbitMQRole`](reference/api/secrets-rabbitmqrole.md) · [example](reference/examples/secrets.md#rabbitmqrole) | `<m>/roles/<name>` | builtin | 6 | integration-tested | `secrets_credentials_test.go`, live |  |
| [`TOTPKey`](reference/api/secrets-totpkey.md) · [example](reference/examples/secrets.md#totpkey) | `<m>/keys/<name>` | builtin | 4 | integration-tested | `secrets_engines_test.go`, live | with `generate=true` the barcode and URL are returned once; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`LDAPSecretsConfig`](reference/api/secrets-ldapsecretsconfig.md) · [example](reference/examples/secrets.md#ldapsecretsconfig) | `<m>/config` | external-plugin | 6 | plugin-gated | - | `bindpass` write-only |
| [`LDAPSecretsDynamicRole`](reference/api/secrets-ldapsecretsdynamicrole.md) · [example](reference/examples/secrets.md#ldapsecretsdynamicrole) | `<m>/role/<name>` | external-plugin | 6 | plugin-gated | - |  |
| [`LDAPSecretsStaticRole`](reference/api/secrets-ldapsecretsstaticrole.md) · [example](reference/examples/secrets.md#ldapsecretsstaticrole) | `<m>/static-role/<name>` | external-plugin | 6 | plugin-gated | - |  |
| [`AWSSecretsConfig`](reference/api/secrets-awssecretsconfig.md) · [example](reference/examples/secrets.md#awssecretsconfig) | `<m>/config/root`, `<m>/config/lease` | external-plugin (u) | after 6 | plugin-gated | - | secret key write-only |
| [`AWSSecretsRole`](reference/api/secrets-awssecretsrole.md) · [example](reference/examples/secrets.md#awssecretsrole) | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`AzureSecretsConfig`](reference/api/secrets-azuresecretsconfig.md) · [example](reference/examples/secrets.md#azuresecretsconfig) | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`AzureSecretsRole`](reference/api/secrets-azuresecretsrole.md) · [example](reference/examples/secrets.md#azuresecretsrole) | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GCPSecretsConfig`](reference/api/secrets-gcpsecretsconfig.md) · [example](reference/examples/secrets.md#gcpsecretsconfig) | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GCPRoleset`](reference/api/secrets-gcproleset.md) · [example](reference/examples/secrets.md#gcproleset) | `<m>/roleset/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GCPStaticAccount`](reference/api/secrets-gcpstaticaccount.md) · [example](reference/examples/secrets.md#gcpstaticaccount) | `<m>/static-account/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GCPImpersonatedAccount`](reference/api/secrets-gcpimpersonatedaccount.md) · [example](reference/examples/secrets.md#gcpimpersonatedaccount) | `<m>/impersonated-account/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`ConsulSecretsConfig`](reference/api/secrets-consulsecretsconfig.md) · [example](reference/examples/secrets.md#consulsecretsconfig) | `<m>/config/access` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`ConsulSecretsRole`](reference/api/secrets-consulsecretsrole.md) · [example](reference/examples/secrets.md#consulsecretsrole) | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`NomadSecretsConfig`](reference/api/secrets-nomadsecretsconfig.md) · [example](reference/examples/secrets.md#nomadsecretsconfig) | `<m>/config/access`, `<m>/config/lease` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`NomadSecretsRole`](reference/api/secrets-nomadsecretsrole.md) · [example](reference/examples/secrets.md#nomadsecretsrole) | `<m>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`TerraformCloudSecretsConfig`](reference/api/secrets-terraformcloudsecretsconfig.md) · [example](reference/examples/secrets.md#terraformcloudsecretsconfig) | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`TerraformCloudSecretsRole`](reference/api/secrets-terraformcloudsecretsrole.md) · [example](reference/examples/secrets.md#terraformcloudsecretsrole) | `<m>/role/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`MongoDBAtlasSecretsConfig`](reference/api/secrets-mongodbatlassecretsconfig.md) · [example](reference/examples/secrets.md#mongodbatlassecretsconfig) | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`MongoDBAtlasSecretsRole`](reference/api/secrets-mongodbatlassecretsrole.md) · [example](reference/examples/secrets.md#mongodbatlassecretsrole) | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - |  |
| [`GitHubSecretsConfig`](reference/api/secrets-githubsecretsconfig.md) · [example](reference/examples/secrets.md#githubsecretsconfig) | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin; app private key write-only |
| [`GitHubSecretsRole`](reference/api/secrets-githubsecretsrole.md) · [example](reference/examples/secrets.md#githubsecretsrole) | `<m>/permissionset/<name>` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |
| [`QuaySecretsConfig`](reference/api/secrets-quaysecretsconfig.md) · [example](reference/examples/secrets.md#quaysecretsconfig) | `<m>/config` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |
| [`QuaySecretsRole`](reference/api/secrets-quaysecretsrole.md) · [example](reference/examples/secrets.md#quaysecretsrole) | `<m>/roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |
| [`QuaySecretsStaticRole`](reference/api/secrets-quaysecretsstaticrole.md) · [example](reference/examples/secrets.md#quaysecretsstaticrole) | `<m>/static-roles/<name>` | external-plugin (u) | after 6 | plugin-gated | - | third-party plugin |

## identity

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| [`Entity`](reference/api/identity-entity.md) · [example](reference/examples/identity.md#entity) | `identity/entity/name/<name>` | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | ID server-generated, kept in `status.id`; default `Orphan`; destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`EntityAlias`](reference/api/identity-entityalias.md) · [example](reference/examples/identity.md#entityalias) | `identity/entity-alias/id/<id>` | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | no lookup by name; identity is (mount accessor, name), adopt by list and read |
| [`Group`](reference/api/identity-group.md) · [example](reference/examples/identity.md#group) | `identity/group/name/<name>` | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | `type` internal/external immutable; default `Orphan` |
| [`GroupAlias`](reference/api/identity-groupalias.md) · [example](reference/examples/identity.md#groupalias) | `identity/group-alias/id/<id>` | builtin | 3 | integration-tested | `identity_test.go`, live | external groups only |
| [`GroupMembership`](reference/api/identity-groupmembership.md) · [example](reference/examples/identity.md#groupmembership) | `identity/group/id/<id>` (`member_entity_ids`, `member_group_ids`) | builtin | 3 | integration-tested | `identity_test.go`, `templated_acl_test.go`, live | no endpoint of its own: read-modify-write of the group, removing only members it added |
| [`OIDCConfig`](reference/api/identity-oidcconfig.md) · [example](reference/examples/identity.md#oidcconfig) | `identity/oidc/config` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live | singleton (`issuer`) |
| [`OIDCKey`](reference/api/identity-oidckey.md) · [example](reference/examples/identity.md#oidckey) | `identity/oidc/key/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| [`OIDCRole`](reference/api/identity-oidcrole.md) · [example](reference/examples/identity.md#oidcrole) | `identity/oidc/role/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| [`OIDCProvider`](reference/api/identity-oidcprovider.md) · [example](reference/examples/identity.md#oidcprovider) | `identity/oidc/provider/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| [`OIDCClient`](reference/api/identity-oidcclient.md) · [example](reference/examples/identity.md#oidcclient) | `identity/oidc/client/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live | `client_id`, `client_secret` server-generated; secret written via `writeSecretRef` |
| [`OIDCScope`](reference/api/identity-oidcscope.md) · [example](reference/examples/identity.md#oidcscope) | `identity/oidc/scope/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| [`OIDCAssignment`](reference/api/identity-oidcassignment.md) · [example](reference/examples/identity.md#oidcassignment) | `identity/oidc/assignment/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |
| [`MFAMethod`](reference/api/identity-mfamethod.md) · [example](reference/examples/identity.md#mfamethod) | `identity/mfa/method/<totp\|okta\|duo\|pingid>/<id>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live | destructive ([ADR 0013](adr/0013-destructive-delete-guard.md)) |
| [`MFALoginEnforcement`](reference/api/identity-mfaloginenforcement.md) · [example](reference/examples/identity.md#mfaloginenforcement) | `identity/mfa/login-enforcement/<name>` | builtin | 4 | integration-tested | `identity_oidc_test.go`, live |  |

## access

| kind | path | availability | phase | status | evidence | notes |
|------|------|--------------|-------|--------|----------|-------|
| [`AccessLevel`](reference/api/access-accesslevel.md) · [example](reference/examples/access.md#accesslevel) | none (cited by grants and layouts) | builtin | 7 | integration-tested | `access_test.go`, `templated_acl_test.go`, live | `in-use` finalizer |
| [`AccessGrant`](reference/api/access-accessgrant.md) · [example](reference/examples/access.md#accessgrant) | `sys/policies/acl/<name>`, `auth/<b>/role/<name>` | builtin | 7 | integration-tested | `access_test.go`, `templated_acl_test.go`, live | one Kubernetes auth role per subject |
| [`AccessLayout`](reference/api/access-accesslayout.md) · [example](reference/examples/access.md#accesslayout) | `sys/policies/acl/<name>`, `identity/group/name/<name>`, `identity/group-alias` | builtin | 7 | integration-tested | `access_test.go`, live |  |

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

Reachable with `OpenBaoEndpoint` and candidates for later typed kinds: OpenBao
sealable-namespace operations, user lockout config (tune `user_lockout_config` on `AuthMount`), plugin reload,
control groups, and the namespace API lock.
