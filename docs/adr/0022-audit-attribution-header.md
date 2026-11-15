# 0022. Audit attribution header

Status: accepted. Date: 2026-11-14.

## Context

All operator writes show in the OpenBao audit log under the operator's (or a tenant's) token. An
auditor cannot tell which Git object caused a change.

## Decision

Every request carries the header `X-Openbao-Config-Resource: <group>/<kind>/<k8s-namespace>/<name>`.
An `AuditRequestHeader` example configures OpenBao to log it.

## Consequences

- Each audit entry identifies a CR, which operators can correlate with their GitOps history.
- The header names a resource, never a value, so it adds no secret to the audit log.
- Status: implemented by the OpenBao client; attribution header handling and audit-log output are
  covered by tests.
