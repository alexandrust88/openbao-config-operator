---
description: "Design decision for OpenBao Config Operator: write-once fields for rotated root credentials. Rationale, constraints and consequences."
---

# 0017. Write-once fields for rotated root credentials

Status: accepted. Date: 2026-11-14.

## Context

After `rotate-root`, only OpenBao knows a database or LDAP root credential. A write-only field is
re-sent on every write, so the next write restores the stale password from the Kubernetes Secret
and breaks the connection.

## Decision

Add the field class write-once: sent on create, and again only when the user changes a rotation
nonce on purpose; never re-sent otherwise and never compared. Used by `DatabaseConnection`
`password` and `private_key`, and LDAP `bindpass` when rotation is enabled.

## Consequences

- A rotation inside OpenBao is not undone by the operator.
- Changing the Secret alone does nothing; the user bumps the nonce to push a new root credential.
- Status: in progress. `DatabaseConnection` with `rotateRootOnCreate` already sends these fields
  on create only (create-only class), without a nonce.
