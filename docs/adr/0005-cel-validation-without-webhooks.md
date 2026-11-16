---
description: "Design decision for OpenBao Config Operator: cEL validation without admission webhooks. Rationale, constraints and consequences."
---

# 0005. CEL validation without admission webhooks

Status: accepted, still accurate for v2 (2026-11-14). Date: 2026-11-13.

## Context

A webhook-based design needs hand-written checks per kind (immutability alone is one check per
field) and a serving certificate (cert-manager or OLM), makes envtest setup heavier, and fails
closed when the operator is down. Kubernetes 1.25+ supports CEL `x-kubernetes-validations`, including transition rules
(`self == oldSelf`) and optional fields (`self.?field.orValue(...)`).

## Decision

Validation lives in the CRD schema: OpenAPI markers for formats, enums, patterns and defaults,
and CEL rules for immutability, one-of choices and cross-field checks. No admission webhook is
shipped in v1alpha1. A webhook is added only for a check that needs other objects, and none is
planned.

## Consequences

- No cert-manager dependency; the CRDs validate even when the operator is not running.
- Validation is tested with envtest against the real API server.
- Checks that need other objects (for example a referenced connection's `allowedNamespaces`)
  happen at reconcile time and surface as conditions, not admission errors.
- CEL cost limits constrain rules on large lists; schemas need `maxItems` and `maxLength`.
