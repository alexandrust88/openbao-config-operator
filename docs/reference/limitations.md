---
description: "Review OpenBao Config Operator limitations, unsupported features and operational trade-offs before deployment."
---

# Known limitations

Limitations of the current release that affect users, with the workaround where one exists. The
sources are the [tech-debt register](../project/tech-debt.md), the [backlog](../project/backlog.md),
the [ADRs](../adr/README.md) and the [architecture](../architecture.md); each entry links to its source. An
entry leaves this page when its source is closed.

!!! warning "Alpha API"
    1.0.0, API `v1alpha1`: fields can still change in a minor release under the
    [deprecation policy](compatibility.md#deprecation-policy). The API groups are `<area>.obco.dev/v1alpha1`.

## Verified scope { #verification }

| limitation | effect | source |
|------------|--------|--------|
| Behaviour is verified on a single-node OpenBao without TLS and with a root token | storage, HA, TLS and least-privilege behaviour of your production server is not covered for every kind; test the kinds you use under your connection's policy | [TD-001](../project/tech-debt.md#td-001-live-coverage-uses-openbao-dev-mode) |
| Runtime verification targets OpenBao 2.7.2; 2.6 is covered by its OpenAPI snapshot | a behaviour difference on 2.6 is not caught automatically | [BL-014](../project/backlog.md#bl-014-detect-openbao-api-changes-automatically), [compatibility](compatibility.md) |
| Field-level API coverage is not checked | a request field added or renamed upstream on a typed path is noticed only by hand; use `BaoEndpoint` for it meanwhile | [TD-003](../project/tech-debt.md#td-003-no-openapi-field-coverage-check) |
| External-plugin kinds are not run against a server | they are verified from recorded payloads only, and paths marked `(u)` in the [kind catalogue](../kinds.md) come from documentation, not OpenBao source; they are disabled by default | [TD-004](../project/tech-debt.md#td-004-unconfirmed-paths-and-external-plugin-kinds) |
| A release can contain a kind whose declared defaults regressed | after an upgrade, check that settled resources stay `Ready` with no `Updated` events | [upgrade](../operate/upgrade.md) |

## Drift detection and comparison

| limitation | effect | source |
|------------|--------|--------|
| A field the operator does not send is not compared | removing it from the spec does not unset it in OpenBao; set the field to its default explicitly | [BL-012](../project/backlog.md#bl-012-remaining-v2-rule-gaps), [architecture](../architecture.md#not-yet-enforced) |
| Some fields cannot return to their default once set, such as a pinned mount `plugin_version` or a PKI default issuer | OpenBao keeps the stored value when the field is removed from the spec | [architecture](../architecture.md#not-yet-enforced) |
| `BaoEndpoint` compares only its `data` keys | a key removed from `data` is not unset on the server | [architecture](../architecture.md#field-classes) |
| Write-only fields cannot be compared | drift in a password or other value OpenBao does not return is invisible; the operator writes it again only when the input Secret or spec changes | [architecture](../architecture.md#security-model) |
| The read after a write is not pinned to the active node | on a cluster whose standbys serve reads, a read-back can return stale values and report `OutOfSync`, retried with backoff | [architecture](../architecture.md#reconcile-sequence) |
| Drift reads always go to the configured address | reading from standbys (horizontal read scaling) is planned | [architecture](../architecture.md#scale-and-drift) |
| The write budget is one window per `BaoServer`, shared by every namespace | one namespace whose objects keep drifting can delay other namespaces' drift repairs on the same server | [BL-016](../project/backlog.md#bl-016-per-namespace-fairness-for-the-write-budget) |

## Ownership and restore

| limitation | effect | source |
|------------|--------|--------|
| Only `IdentityEntity`, `IdentityGroup`, `BaoNamespace` and `KVSecretMetadata` carry a server-side ownership marker | after status loss, other kinds need a one-time `creationPolicy: Adopt` to be managed again | [ADR 0012](../adr/0012-ownership-path-index-and-marker.md) |
| `IdentityGroupMembership`, `AccessGrant` and `AccessLayout` keep the members and children they own only in status | a restore without status cannot remove members or children that the spec no longer lists; remove them by hand | [BL-011](../project/backlog.md#bl-011-restore-without-status-chart-attestation), [backup and restore](../operate/backup-restore.md) |
| Creates are serialised per process | with several replicas, leader election must stay on (the chart default) | [architecture](../architecture.md#scale-and-drift) |
| The hash key of `status.writeOnlyHash` cannot be rotated without rewriting every write-only field | if the hash-key Secret is deleted, the next manager start creates a new key and every object with write-only inputs is rewritten once | [architecture](../architecture.md#security-model), [roadmap](../maintainers/roadmap.md#later-plugins-and-the-road-to-v1beta1) |

## OpenBao behaviour

| limitation | effect | workaround | source |
|------------|--------|------------|--------|
| A standby that serves requests itself (the OpenBao 2.7 default) answers `failed to update audit table` (400) to an audit enable and does not forward it | `Audit` reports `ReconcileError` | point the `BaoServer` address at the active node only, or set `disable_standby_reads = true` in the OpenBao server configuration | [troubleshooting](../operate/troubleshooting.md#common-openbao-errors) |
| A proxy or a standby without request forwarding answers with a redirect | the operator never follows redirects: resource `ReconcileError`, connection `AuthenticationFailed` | point the address at the active node or a service that forwards | [troubleshooting](../operate/troubleshooting.md#common-openbao-errors) |
| Nine sys kinds exist only in the root namespace | `Audit`, `AuditRequestHeader`, `RateLimitQuota`, `QuotaConfig`, `Plugin`, `CORSConfig`, `UIHeader`, `Logger` and `RaftAutopilot` in a child namespace stop with `RootOnlyKind` | use a connection based in the root namespace | [architecture](../architecture.md#namespace-scope) |
| `KeyRotationConfig` needs the root or a sealable namespace | `Unsupported` elsewhere | | [architecture](../architecture.md#namespace-scope) |
| A KV version 2 history purge cannot be conditional | bounded version destruction preserves later writes but cannot distinguish metadata deleted and recreated concurrently | do not delete/recreate metadata while cleanup is pending | [TD-008](../project/tech-debt.md#td-008-a-kv-history-purge-can-destroy-a-version-written-right-before-it) |

## Kinds and features

| limitation | effect | source |
|------------|--------|--------|
| External-plugin kinds are disabled by default | enable them with `pluginKinds.enabled`; each reports `Unsupported` until its plugin is registered and mounted | [kind catalogue](../kinds.md), [BL-005](../project/backlog.md#bl-005-external-plugin-kinds) |
| No typed kind for some configuration paths, such as SSH zero-address configuration, the level of every logger at once (`sys/loggers`) and external keys | use `BaoEndpoint` | [API coverage](../api-coverage.md#endpoint-only-paths) |
| No kind for features OpenBao does not have (Sentinel, KMIP, Transform, managed keys, replication) or for one-off actions (a login, a PKI sign, creating a token) | out of scope | [architecture](../architecture.md#coverage-policy) |
| `DatabaseConnection.pluginConfig.connection_url` is not checked for a literal password | `connectionURL` refuses one; the same value in `pluginConfig` is accepted. | [BL-017](../project/backlog.md#bl-017-refuse-a-literal-password-in-databaseconnection-pluginconfigconnection_url) |
| Namespace sealing on `BaoNamespace` is planned | not available | [architecture](../architecture.md#openbao-native-features) |
| Plugin support end to end ([roadmap phase 5](../maintainers/roadmap.md#phases)) is planned | registering a plugin and mounting it through `pluginRef` is not verified end to end against a server with the plugin installed; test plugin mounts on your server before relying on them | [architecture](../architecture.md#plugin-support) |

## Security and install

| limitation | effect | workaround | source |
|------------|--------|------------|--------|
| The default install can read Secrets and ConfigMaps and request ServiceAccount tokens cluster-wide | the tenant boundary relies on each connection's OpenBao identity, not on Kubernetes RBAC | set `watchNamespaces` for a Role per namespace | [TD-005](../project/tech-debt.md#td-005-the-default-install-can-read-secrets-cluster-wide) |
| One-time outputs (secret IDs, client secrets, generated keys) live in Kubernetes Secrets | etcd encryption and RBAC protect them, not OpenBao | | [architecture](../architecture.md#security-model) |
| The NetworkPolicy is on by default but leaves egress open by destination while `apiServer.cidrs` and `openbao.to` are empty | the operator can reach any host on the API server and OpenBao ports | name the peers | [hardening](../security/hardening.md#r-network-01-enable-the-networkpolicy) |
