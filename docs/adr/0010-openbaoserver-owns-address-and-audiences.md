# 0010. OpenBaoServer owns the address and audiences

Status: accepted. Date: 2026-11-14.

## Context

In v1 a tenant sets `address` on its `OpenBaoConnection`, so a tenant can make the operator send
requests, including TokenRequest-minted ServiceAccount tokens, to any URL it chooses. A tenant can
also request any audience for its ServiceAccount token, including the API server audience, which
turns the operator into a minter of general Kubernetes tokens.

## Decision

Add a cluster-scoped `OpenBaoServer`, owned by the platform. It holds `address`, the CA bundle,
`allowedNamespaces`, `allowedAudiences`, `rateLimit` and `requestTimeout`. Connections name it
with `serverRef` and have no address field. A namespaced connection may reference a server only if
`allowedNamespaces` selects its namespace, and every requested audience must be in
`allowedAudiences`; the API server audience is always rejected.

## Consequences

- A tenant cannot choose the network target or mint a general token.
- The per-server rate limit and write budget have a natural home.
- Breaking change for v1 connection manifests (pre-alpha, accepted).
- Status: implemented. `OpenBaoServer` owns `address`; both connection kinds refer to it through
  `serverRef`.
