---
description: "Design decision for OpenBao Config Operator: drift tiers and per-server request budget. Rationale, constraints and consequences."
---

# 0014. Drift tiers and per-server request budget

Status: accepted. Date: 2026-11-14.

## Context

OpenBao does not notify clients of out-of-band changes, so drift is found by re-reading. With one
interval, 30k objects at 10 minutes is about 50 reads per second against one server, and every
connection to that server competes without coordination.

## Decision

Two tiers, both operator flags: access-critical kinds (policies, auth mounts, roles and configs,
audit, groups and memberships) every 15 minutes, everything else every hour, each with ±20% jitter.
`spec.driftInterval` overrides per object and `spec.driftDetection: Disabled` turns it off.
`OpenBaoServer.spec.rateLimit` is one token bucket shared by every connection to that server;
drift reads wait and spec-change reconciles go first. `openbao_drift_lag_seconds` shows the lag.
Drift reads may go to standbys. The read after the operator's own write is not pinned to a node; point the server address at the active node (see the troubleshooting page) when standbys serve reads.

## Consequences

- Load on a server is bounded and predictable: objects / interval.
- Drift in standard kinds can stay for up to an hour.
- Tenants share one budget per server; ResourceQuota object counts limit one tenant's share.
- Status: implemented: `--drift-interval-critical` (default 15m) and `--drift-interval-standard` (default 1h).
