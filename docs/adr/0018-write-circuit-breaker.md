# 0018. Write circuit breaker

Status: accepted. Date: 2026-11-14.

## Context

An operator upgrade that changes a declared default, or a bad shared template, can make thousands
of objects differ at once. The engine would then rewrite all of them within minutes against one
server.

## Decision

Writes per minute per `OpenBaoServer` are capped by an operator flag (default 60). At the cap the
engine stops writing to that server, sets `Synced=False` reason `WriteBudgetExceeded` on the
affected objects, emits an event, and resumes when the window allows. Reads are not affected.

## Consequences

- A mass rewrite becomes a slow, visible one that an operator can stop.
- Large legitimate changes take longer; the flag can be raised for planned migrations.
- Only drift repairs and changed defaults spend the budget. Creates, adoptions, spec changes and
  changed input Secrets (a new write-only hash) are user-intended and exempt, so one tenant rotating
  a Secret cannot starve another tenant's drift repairs. A rotated hash key also changes every hash
  and is therefore not budgeted; the server's `rateLimit` still paces it.
- Status: implemented as a per-server write budget in the engine.
