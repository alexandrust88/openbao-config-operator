# 0007. No OpenBao-to-Kubernetes secret sync

Status: accepted, still accurate for v2 (2026-11-14). Date: 2026-11-13.

## Context

A configuration operator could also read OpenBao paths and render Kubernetes Secrets with
templates. External Secrets Operator (ESO) and other secret sync operators already do this, with refresh, templating and many providers. Copying secret values into Kubernetes
spreads them to etcd, backups and anyone with Secret read access.

## Decision

The operator configures OpenBao. It does not sync secret values into Kubernetes Secrets. It
writes a Kubernetes Secret only for a value it generates and that OpenBao returns once, such as
an AppRole secret-id or the key shares of a sealable namespace, through `writeSecretRef`. The
operator owns that Secret.

## Consequences

- Users combine this operator with ESO: this operator writes the policy and the Kubernetes auth
  role, and ESO reads the secret with them.
- No secret sync kind, and no secret values in CR status or logs.
- One-time outputs must be written before status is updated, so a crash cannot lose them without
  a trace; adapters for these kinds need care and tests.
