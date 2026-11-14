# 0020. CRD versioning without webhooks

Status: accepted. Date: 2026-11-14.

## Context

[ADR 0005](0005-cel-validation-without-webhooks.md) avoids admission webhooks. Serving a new CRD
version normally needs a conversion webhook, which brings back a serving certificate and a
fail-closed dependency on the operator.

## Decision

Version bumps stay schema-compatible: additive changes only, so the API server converts with
`conversion: None`. Stored objects move to the new storage version with
kube-storage-version-migrator. A conversion webhook is accepted later only if a breaking change is
unavoidable.

## Consequences

- No cert-manager dependency and no webhook outage mode.
- Renames and removals need a deprecation period with both fields, not a conversion.
- Status: decided; applies from the first version after `v1alpha1`.
