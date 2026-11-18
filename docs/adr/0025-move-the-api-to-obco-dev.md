---
description: "Design decision for OpenBao Config Operator: move every API group to the owned domain obco.dev and fix the names that clash in kubectl, before the first release, 1.0.0."
---

# 0025. Move the API to obco.dev

Status: accepted; implemented in 1.0.0, the first release. Date: 2026-11-18.

Supersedes [ADR 0008](0008-provisional-api-group-domain.md).

## Context

ADR 0008 kept `openbaoconfig.io`, a domain the project never owned. An unowned API group can be claimed by
anyone. Three other projects named openbao-config-operator exist (Stakater's, on `security.mto.stakater.com`, and
a copy of it), and their default kustomize install shares the namespace and name prefix of our kustomize
install (`install.yaml`); the Helm chart's default namespace does not clash. Several names are
ambiguous in kubectl: the plural `policies` (Kyverno and others), `openbaonamespaces` (Stakater), and the generic
category `openbao`. `spec.namespace` sits three lines below `metadata.namespace` but means an OpenBao
namespace, which every review of the documentation flagged. The project is `v1alpha1`, four days public, with no
known adopters: the cheapest moment to rename. The maintainer registered `obco.dev` on 2026-11-18.

## Decision

In one release, the first release 1.0.0, driven by one rename map:

- API groups `<area>.openbaoconfig.io` become `<area>.obco.dev` for core, sys, auth, secrets, identity and access.
  Annotation and finalizer keys move from `openbaoconfig.io/` to `obco.dev/`.
- Kinds that repeat the product name or clash in kubectl are renamed: `OpenBaoServer`, `OpenBaoConnection`,
  `ClusterOpenBaoConnection`, `OpenBaoEndpoint`, `OpenBaoNamespace` become `BaoServer`, `BaoConnection`,
  `ClusterBaoConnection`, `BaoEndpoint`, `BaoNamespace`; `Policy` becomes `ACLPolicy` and `Workflow` becomes
  `BaoWorkflow`; the identity kinds take OpenBao's `identity/` prefix (`IdentityEntity`, `IdentityEntityAlias`,
  `IdentityGroup`, `IdentityGroupAlias`, `IdentityGroupMembership`). Kind names used as values
  (`connectionRef.kind`, access child kinds) follow.
- `namespace` always means a Kubernetes namespace and `openbaoNamespace` an OpenBao one: `spec.namespace`,
  `spec.authNamespace`, `status.namespace` and the nested access fields become `openbaoNamespace`,
  `openbaoAuthNamespace` and `openbaoNamespace`.
- The kustomize install uses the prefix `obco-` and the namespace `obco-system`; the Helm chart keeps its name and
  default namespace by choice. A CI test checks plurals and short names against a checked-in list of names used by
  Kyverno, Argo, OpenShift, cert-manager, Flux, Crossplane provider-vault and vault-config-operator, and against
  Stakater's `openbao*` plurals and `ob*` short names; it allow-lists the known overlaps.
- Every kind is in category `obco` and `obco-<area>`; the most used kinds get `bao*` short names.
- Operator metrics move from `openbao_*` to `obco_*`.
- Unchanged: the ownership marker keys the operator writes into OpenBao, the leader election ID, the audit attribution header, the Go module, `v1alpha1`, condition reasons, the
  chart name and every other kind and field.

## Consequences

- No migration and no upgrade path: the maintainer is the only user. The releases v0.1.x to v0.5.0 are withdrawn,
  and the renamed API ships as the first release, 1.0.0. Nothing converts resources or manifests from
  `openbaoconfig.io`.
- The ownership marker keys inside OpenBao stay `openbaoconfig-`. OpenBao objects that the withdrawn releases
  managed can be adopted by path with `creationPolicy: CreateOrAdopt`.
- `obco.dev` must be renewed (registered until 2030-11-18, auto-renew on).
