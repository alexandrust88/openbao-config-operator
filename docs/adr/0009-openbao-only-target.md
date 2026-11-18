---
description: "Design decision for OpenBao Config Operator: openBao-only target. Rationale, constraints and consequences."
---

# 0009. OpenBao-only target

Status: accepted; implemented. Date: 2026-11-14.

Update 2026-11-18: no released CRD has a connection `status.flavor` field. The probe in `internal/openbao/probe.go` still computes a flavour, but nothing stores it; that code is the remaining leftover.

## Context

v1 targeted OpenBao and, where the APIs match, HashiCorp Vault. Every kind then needed a flavour
check, the test matrix doubled, and design choices were held back by Vault behaviour (no
namespaces in Vault CE, no self-init, no sys/workflows). No Vault deployment is known to need the
operator.

## Decision

Target OpenBao only, minimum 2.6, current 2.7.2. No flavour detection, no Vault Enterprise kinds.
Vault is neither tested nor promised. The design relies on OpenBao features: self-init,
namespaces, horizontal read scaling, OCI plugins.

## Consequences

- One test matrix: every adapter runs against `bao server -dev` on each pinned minor.
- `OpenBaoEndpoint` still reaches any path, but Vault-only paths are not supported.
- The connection `status.flavor` field and the probe code that sets it are leftovers to remove.
- Supersedes the compatibility parts of [ADR 0001](0001-openbao-api-client.md); the client choice
  stands.
