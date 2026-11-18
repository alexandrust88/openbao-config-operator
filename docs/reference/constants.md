---
description: "Look up the operator's drift intervals, retry backoffs, rate limits, write budget, login and renewal timings and manager flag defaults."
---

# Constants and timings

The fixed values in the code that shape the operator's behaviour, and the defaults of the flags that
tune them. Each row names the package file that defines the value. The design behind these values
is in the [architecture](../architecture.md).

## Drift and requeue

| value | default | source | effect |
|-------|---------|--------|--------|
| `DefaultDriftIntervalCritical` | 15 min | `internal/engine/drift.go` | drift interval of the access-critical kinds listed in [scale and drift](../architecture.md#scale-and-drift); flag `--drift-interval-critical` (`cmd/main.go`) |
| `DefaultDriftIntervalStandard` | 1 h | `internal/engine/drift.go` | drift interval of every other kind; flag `--drift-interval-standard` (`cmd/main.go`) |
| `MinDriftInterval` | 30 s | `internal/engine/drift.go` | CEL rejects a shorter `spec.driftInterval` (`api/common/v1alpha1/types.go`); the engine raises one that bypassed validation to 30 s |
| `mountTableTTL` | 2 s | `internal/controller/sys/mounttable.go` | one read of `sys/mounts` or `sys/auth` serves every mount resource of the same connection and OpenBao namespace; a mount write or delete clears it, so an out-of-band mount change shows up to 2 s later |
| `liveSnapshotTTL` | 10 s | `internal/engine/ownership.go` | one live LIST of a kind serves the ownership rechecks before adoption; a path this process claimed after that LIST gets a fresh LIST |
| jitter | × 0.8 to × 1.2 | `internal/engine/reconciler.go` | applied to the drift requeue, to every backoff and to the pause recheck |
| `minReferenceRetry` | 2 s | `internal/engine/reconciler.go` | first delay of every backoff below |
| backoff growth | time since `Synced` turned `False` with the same reason, at least 2 s | `internal/engine/reconciler.go` | each retry waits about as long as the condition has lasted, so the delay roughly doubles |
| `retryInterval` | 30 s | `internal/engine/reconciler.go` | backoff cap for `ReferenceNotReady` and `OutputNotDelivered` |
| `maxConnectionRetry` | 1 min | `internal/engine/reconciler.go` | backoff cap for `ConnectionNotReady`; the connection watch re-queues sooner |
| `OutOfSync` cap | the resource's drift interval | `internal/engine/reconciler.go` | backoff cap after a write that did not read back as written |
| `inProgressInterval` | 10 s | `internal/engine/reconciler.go` | fixed requeue while OpenBao completes an asynchronous operation (`ReconcileInProgress`) |
| `pausedRecheck` | 1 min | `internal/engine/reconciler.go` | recheck of a resource paused through its Namespace or its `BaoServer`; a pause on the resource itself waits for the annotation change |

## Request rate and recovery

| value | default | source | effect |
|-------|---------|--------|--------|
| `driftShare` | 0.8 | `internal/openbao/ratelimit.go` | requests without priority (drift reads, the first pass after an outage) use at most 80 % of `BaoServer.spec.rateLimit` QPS and burst; spec changes may use all of it |
| rate limiter key | server address | `internal/openbao/pool.go` | one token bucket per address, shared by every connection to it; the most recent connection's `rateLimit` replaces the buckets |
| `recoveryPerSecond` | 100 per second | `internal/engine/budget.go` | rate of the first passes after `ConnectionNotReady`, per `BaoServer` |
| `recoveryBurst` | 10 | `internal/engine/budget.go` | burst of the recovery ramp |

## Write budget

| value | default | source | effect |
|-------|---------|--------|--------|
| `DefaultWriteBudgetPerMinute` | 60 | `internal/engine/budget.go`, flag `--write-budget-per-minute` (`cmd/main.go`) | rewrites per `BaoServer` per window; `-1` disables the cap |
| `budgetWindow` | 1 min, sliding | `internal/engine/budget.go` | window of the write budget |
| held-back retry | the wait for the first free slot (at least 1 s) plus 0 to 60 s at random | `internal/engine/budget.go` | spreads held-back rewrites over a window |
| budget state | in process memory | `internal/engine/budget.go` | a manager restart or a leader change starts with an empty window, so up to one full budget of rewrites per server can follow at once |

Creates, adoptions, spec changes and changed input Secrets are not charged; the
[architecture](../architecture.md#operational-safety) describes which writes count.

## OpenBao HTTP client

| value | default | source | effect |
|-------|---------|--------|--------|
| `maxRetries` | 2 | `internal/openbao/client.go` | retries per request after the first attempt, by the OpenBao API client's default retry policy; every attempt takes a rate-limit token |
| `minRetryWait` | 250 ms | `internal/openbao/client.go` | shortest wait between retries |
| `maxRetryWait` | 2 s | `internal/openbao/client.go` | longest wait between retries |
| redirects | refused | `internal/openbao/client.go` | a 3xx answer is an error and is never followed, so a token or body is never re-sent to another host |
| `maxAttributionLength` | 256 characters | `internal/openbao/ratelimit.go` | longest `X-Openbao-Config-Resource` header; non-printable characters are dropped |

## Login, renewal and tokens

| value | default | source | effect |
|-------|---------|--------|--------|
| renewal point, renewable token | a third of the TTL | `internal/openbao/pool.go` | the token is renewed for its login TTL |
| renewal floor | a third of the login TTL | `internal/openbao/pool.go` | a renewal that returns a shorter lease (max TTL near) leads to a new login |
| replacement point, non-renewable token | two thirds of the TTL | `internal/openbao/pool.go` | a new login replaces the token |
| `minReloginInterval` | 5 s | `internal/openbao/pool.go` | after a 401, or a 403 whose `auth/token/lookup-self` fails, at most one new login per 5 s per client; inside that window the request fails without a login |
| `sharedLoginTimeout` | 1 min | `internal/openbao/pool.go` | limit of a login that several callers wait for |
| `revokeTimeout` | 5 s | `internal/openbao/pool.go` | limit of the best-effort revocation of a retired token |
| `tokenExpirationSeconds` | 600 s | `internal/connection/resolver.go` | lifetime requested for a TokenRequest token; each one is used for a single login |

## Server identity and probes

| value | default | source | effect |
|-------|---------|--------|--------|
| `clusterIDTTL` | 1 min | `internal/openbao/clusterid.go` | how long a client trusts the cluster id from `sys/health`; it is read again past the cache right before a create, a recreate and a delete |
| `--cluster-id` | the UID of the `kube-system` Namespace | `cmd/main.go` | written into ownership markers. A rebuilt Kubernetes cluster has a new `kube-system` UID, so markers written before the rebuild name another cluster and marked kinds (`IdentityEntity`, `IdentityGroup`, `BaoNamespace`, `KVSecretMetadata`) report `Conflict`; a fixed `--cluster-id` survives the rebuild |
| `probeInterval` | 1 min plus up to 20 % | `internal/controller/core/connection_controller.go`; `internal/controller/core/server_controller.go` | re-check of every `BaoServer` and connection |
| namespace retry | 2 s to 30 s | `internal/controller/core/connection_controller.go` | retry while a connection's OpenBao namespace does not exist yet |
| `inUseRecheck` | 5 s | `internal/controller/core/inuse.go` | recheck of a connection or server held by its in-use finalizer |

## Hashing and redaction

| value | default | source | effect |
|-------|---------|--------|--------|
| `hashKeySize` | 32 bytes | `internal/engine/hashkey.go` | size of the key in the hash-key Secret (`--hash-key-secret`, default `openbao-config-operator-hash-key`, `cmd/main.go`) |
| `minRedactLength` | 4 characters | `internal/engine/reconciler.go` | write-only string values shorter than this are not redacted from OpenBao error messages |

## Concurrency and leader election

| flag | default | source | effect |
|------|---------|--------|--------|
| `--max-concurrent-reconciles` | 4 | `cmd/main.go` | workers per kind |
| `--max-concurrent-reconciles-high-volume` | 8 | `cmd/main.go` | workers of the kinds that usually have many objects (roles, policies, entities, groups) |
| `--leader-elect` | `true` | `cmd/main.go` | creates are serialised only within one process, so several replicas need it |
| `GracefulShutdownTimeout` | 5 s | `cmd/main.go` | after SIGTERM in-flight reconciles get 5 s to return, within the chart's 10 s `terminationGracePeriodSeconds`; the leader then releases its lease (`LeaderElectionReleaseOnCancel`), so the next pod takes over without waiting for the lease to expire |
