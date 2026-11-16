---
description: "Design decision for OpenBao Config Operator: use the OpenBao api/v2 client. Rationale, constraints and consequences."
---

# 0001. Use the OpenBao api/v2 client

Status: accepted, still accurate for v2 (2026-11-14). Date: 2026-11-13.

## Context

The operator needs an HTTP client for OpenBao that handles TLS, namespaces, token renewal and
retries. A narrow hand-written client keeps paths, status codes and redaction explicit, but it
needs its own retry and backoff on 429 and 5xx, and every new endpoint needs new client code. Our
catalogue has more than a hundred kinds plus a raw `OpenBaoEndpoint` kind, so a per-endpoint
client does not scale.

`github.com/openbao/openbao/api/v2` is the public client module of OpenBao. It has in-memory CA
bundles (`TLSConfig.CACertBytes`), `WithNamespace` (a shallow copy with its own namespace
header), `Logical()` for any path, a `LifetimeWatcher` and configurable retries. It sends the
`X-Vault-*` headers that the OpenBao API expects.

## Decision

Use `github.com/openbao/openbao/api/v2` for all OpenBao traffic. Wrap it behind the narrow
`openbao.Client` interface in `internal/openbao/contract.go` (`Read`, `Write`, `Delete`, `List`,
`WithNamespace`, `Namespace`). Adapters and the engine use only that interface and generic
logical paths, not the typed `Sys()` helpers, because the typed structs follow OpenBao fields and
lag behind the server.

## Consequences

- One code path serves every kind and `OpenBaoEndpoint`; new kinds need no client code.
- Tests use a fake `openbao.Client`, and integration tests use a real `bao server -dev`.
- The module is a large dependency and is tagged separately (`api/v2.x.y`); the version is pinned
  in `go.mod` and bumped by Renovate.
- Error mapping (404 to `ErrNotFound`, 401/403 re-login, 429/5xx backoff) and redaction stay our
  responsibility inside `internal/openbao`.
