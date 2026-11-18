---
title: Size OpenBao Config Operator from load results
description: "Size OpenBao Config Operator from measured create, drift, outage and delete times, and the flags that change them."
---

# Load testing

Measured scale numbers for OpenBao Config Operator: how long creates, drift repair, OpenBao
outages and deletes take, and which flags matter when you size a fleet. Use the tables to pick
drift intervals, write budget and concurrent reconciles for your object count.

## Conditions of these numbers

One host (arm64, 10 CPUs) ran a single OpenBao node on raft, the manager and the Kubernetes API.
Drift intervals were 1 minute unless noted. Latencies are pessimistic: other work kept the host
load high on several runs. Counts of requests, writes, reconciles and logins are exact.

- No TLS between the manager and OpenBao, one OpenBao node, no network latency, no Kubernetes API
  priority and fairness limits.
- The 1 minute drift interval compresses time; production defaults are longer.
- `wb` in the tables is `--write-budget-per-minute`; `wb off` means `-1`, which disables the budget.
  The default is 60. The budget does not apply to creates ([write budget](#write-budget)).

Kind mixes used in the tables:

| set | kinds |
| --- | --- |
| `simple` | `ACLPolicy` and `KVSecret`, one Secret per `KVSecret` |
| `mixed` | `ACLPolicy`, `KVSecret`, `PasswordPolicy`, `AuditRequestHeader`, `TokenRole`, `UserpassUser`, `AppRole`, `IdentityEntity`, `IdentityGroup`, `TransitKey` |
| `refs` | `IdentityEntityAlias` with `entityRef` to an `IdentityEntity` |
| `members` | `IdentityGroupMembership` with `entityRef` and `groupRef` (N a multiple of 3) |
| `tree` | an OpenBao namespace hierarchy sized by shape `PARENTSxCHILDRENxKV[xROLESxPOLICIES]` |

Phases behind each row: create N objects (ramp); three quiet drift intervals (steady); change N/10
`ACLPolicy` and `KVSecret` objects in OpenBao and time their repair (drift); stop OpenBao for 60 s
then recover (outage); delete every object with `deletionPolicy: Delete` (delete).

## Results

### Ramp

| Run | All ready | Ready p50 / p95 / p99 | OpenBao requests | Reconciles | Peak RSS |
| --- | --- | --- | --- | --- | --- |
| simple, N=1000, wb off | 25.8 s | 12.8 / 23.9 / 24.7 s | 3500 (2500 GET, 1000 PUT) | 1000 | 153 MiB |
| mixed, N=200, wb off | 2.9 s | 0.9 / 2.5 / 2.7 s | 660 (440 GET, 220 PUT) | 200 | 125 MiB |
| mixed, N=1000, wb off | 7.7 s | 2.1 / 5.9 / 6.6 s | 3300 (2200 GET, 1100 PUT) | 1000 | 159 MiB |
| simple, N=5000, wb off | 91.6 s | 33.1 / 77.7 / 84.2 s | 19106 (14106 GET, 5000 PUT) | 6605 | 196 MiB |

The default write budget (60) does not hold creates back: in separate runs with it, `simple` at
N=1000 was ready in 12.1 s and `mixed` at N=1000 in 9.6 s.

A create costs one reconcile (1.3 at N=5000), about 2.5 OpenBao reads and 1 write (read, write,
read back; `KVSecret` also reads the current version for check-and-set) and 3 Kubernetes PATCHes
plus one event. The PATCHes are the claim recorded before the create, the finalizer and the final
status; with the default `deletionPolicy: Orphan`, which adds no finalizer, a create costs two.
Throughput is bounded by the workers per kind: `ACLPolicy` (8 workers) and `KVSecret` (4 workers)
together reached 39 objects/s at N=1000 and 55 objects/s at N=5000 on the loaded host; the ten
mixed kinds reached 130 objects/s.

### Steady state

| Run | Reconciles/s (expected N/60 s) | OpenBao requests/s | Requests per 10 s bucket min / p50 / p95 / max | OpenBao writes | Kubernetes writes |
| --- | --- | --- | --- | --- | --- |
| simple, N=200 | 3.23 (3.33) | 3.22 | 0.5 / 2.9 / 6.3 / 6.3 | 0 | 0 |
| simple, N=1000 | 15.8 (16.7) | 15.8 | 0 / 13.8 / 34.3 / 34.3 | 0 | 0 |
| mixed, N=200 | 2.99 (3.33) | 3.27 | 0 / 1.2 / 9.9 / 9.9 | 0 | 0 |
| mixed, N=1000 | 15.2 (16.7) | 16.7 | 0 / 5.1 / 44.1 / 44.1 | 0 | 0 |
| simple, N=5000 | 81.3 (83.3) | 81.3 | 52.6 / 76.9 / 107.1 / 107.1 | 0 | 0 |

Every drift check costs one OpenBao read and no Kubernetes write. The rate stays below N/interval
in the first interval after a ramp, because objects created together first come back between 0.8
and 1.2 intervals later. The ±20 % jitter spreads that cohort over 24 s; the 10 s buckets show the
spread, which widens with every interval. Logins stayed at one per connection, and token accessors
and entity aliases did not change in any run.

### Drift storm

| Run | Tampered | Repair p50 / p95 / max | Writes | Writes after repair |
| --- | --- | --- | --- | --- |
| simple, N=200 | 20 | 22.7 / 52.8 / 61.9 s | 20 | 0 |
| simple, N=1000 | 100 | 35.4 / 61.0 / 69.7 s | 100 | 0 |
| mixed, N=200 | 20 | 48.2 / 58.7 / 60.3 s | 20 | 0 |
| mixed, N=1000 | 100 | 42.1 / 63.1 / 72.7 s | 100 | 0 |
| simple, N=5000 | 500 | 39.9 / 66.2 / 75.7 s | 500 | 0 |

Repair time is the time to the object's next drift check, at most 1.2 drift intervals. Each
tampered object is written exactly once. A `KVSecret` path with a key the operator does not own was
never rewritten.

100 tampered objects exceed the default write budget (60). In N=1000 runs with it, 36 (`simple`)
and 29 (`mixed`) repairs waited, and the last repair landed after 122 s and 120 s instead of 70 s
and 73 s with the budget off. Each tampered object was still written exactly once.

### OpenBao outage (60 s)

| Run | All objects ready after restart | Peak OpenBao requests/s in recovery | Reconciles in recovery |
| --- | --- | --- | --- |
| simple, N=1000, wb 60 | 28.1 s | 100 | 1326 |
| mixed, N=1000, wb 60 | 33.4 s | 114 | 1599 |

The peak is the highest 2 s sample between the restart and the last object ready. The connection
probe (1 minute ±20 %) decides when the connection turns ready: 18 to 23 s after the restart in
these runs.

- While OpenBao is down, resources whose connection is not ready retry with a jittered backoff from
  2 s to 60 s: about 6 reconciles per object during the outage. They read only the cache: no
  OpenBao request and about one status PATCH per object for the whole outage.
- When the connection turns ready, the connection watch re-queues them. The first pass after that
  goes through a per-server ramp of 100 passes per second (burst 10), and its requests run without
  priority, so `BaoServer.spec.rateLimit` holds them to the drift share when it is set.
  Recovery ran at about 100 requests/s for 10 s, with one status PATCH and one OpenBao read per
  object.
- No object was rewritten after the restart, and the operator did not log in again: its token was
  still valid.

### Delete

| Run | All finalized | Finalize p50 / p95 |
| --- | --- | --- |
| simple, N=200 | 3.6 s | 1.3 / 3.3 s |
| simple, N=1000 | 16.0 s | 8.0 / 14.4 s |
| mixed, N=200 | 1.3 s | 0.3 / 1.0 s |
| mixed, N=1000 | 7.6 s | 1.4 / 6.2 s |
| simple, N=5000 | 66.6 s | 24.4 / 59.8 s |

### References

Referrers wait for their referents with a jittered backoff from 2 s to 30 s. With the default
write budget, `refs` at N=200 made all 100 aliases ready (OpenBao holds 101 entity aliases, the
operator's own included) and `members` at N=201 made all 67 memberships ready, with no `Conflict`
event in either run. Referrers were ready 4.8 s (p50) and 8.0 s (max) after their referents were
applied for `refs`, 5.8 s and 9.1 s for `members`.

### Resources

The idle manager used about 4,400 goroutines with 68 controllers running, each with its workers,
informers and queues; a default install starts 74 controllers, one per built-in kind. The count
stayed flat through every scenario and after deletion, so nothing leaks. RSS was about 55 MiB idle, 125 to 160 MiB with 1,000 objects and 232 MiB with 5,000. CPU peaked at
70 % of one core during the mixed N=1000 ramp; in steady state it was 7.5 % at N=1000 and 31 % at
N=5000 with the 1 minute interval (81 drift checks/s).

Errors use controller-runtime's per-item exponential backoff. The cache is scoped to
`--watch-namespaces`, the client reads Secrets live and the Secret cache drops their data.

### Write budget

Creates, adoptions and spec changes do not spend the write budget, so a first sync runs at full
speed and no create gets `WriteBudgetExceeded`. Only rewrites of objects the operator already holds
spend it: drift repairs, changed defaults and new write-only inputs. A held-back rewrite keeps a
fixed condition message and retries at a random point of the next window; a retry that is held
back again patches nothing.

## Namespace tree

The `tree` set builds the OpenBao namespace hierarchy of a multi-tenant server. Shape
`PARENTSxCHILDRENxKV[xROLESxPOLICIES]` (roles and policies default to 3); the default `100x10x20`
is 100 parent namespaces with 10 children each: 1,100 namespaces, 32,000 objects and 23,100 mounts.

- Each parent namespace and the objects in it go through the root connection, with `spec.openbaoNamespace`
  set to the parent.
- Each parent gets its own `BaoConnection` with `openbaoNamespace: <parent>` and `openbaoAuthNamespace: ""`.
  It creates the child namespaces and the objects in them, so a run with 100 parents has 101
  connections and 101 operator tokens.
- Every OpenBao namespace holds one `kubernetes` `AuthMount` with its `KubernetesAuthConfig`, KV
  `SecretsMount`s of type `kv-v2`, roles as `KubernetesAuthRole`s and policies as `ACLPolicy` objects.
- Objects are applied in wave order (parent namespaces, connections, parent mounts and policies,
  parent auth, child namespaces, child mounts and policies, child auth) without waiting between
  waves. Drift tampers with 50 `ACLPolicy` objects. Delete goes level by level in reverse. Mounts,
  policies and roles use `deletionPolicy: Delete` with the destructive-delete annotation;
  namespaces and `KubernetesAuthConfig` stay `Orphan`.

### Tree results

One arm64 host (10 CPUs, 32 GB), load average 2 to 6. OpenBao 2.7.2 on raft, drift intervals
1 minute (30 s for 5x3x4), default write budget. `-c` is `--max-concurrent-reconciles`; `ACLPolicy` and
`KubernetesAuthRole` use the high-volume default of 8. These runs predate the mount and namespace
handling described in [how the operator handles a tree](#how-the-operator-handles-a-tree), which
made the ramp of a 10x5x10 tree about twice as fast; treat the ramp times below as an upper bound.

| Shape | Objects | `-c` | All ready | Objects/s | Ready p50 / p95 / max | Manager peak RSS / goroutines / CPU | OpenBao peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 5x3x4 | 265 | 4 | 68.1 s | 3.9 | 3.5 / 67.0 / 68.0 s | 131 MiB / 3,982 / 11 % | 114 MiB |
| 20x10x20 | 6,400 | 4 | 393.2 s | 16.3 | 132.5 / 364.4 / 392.8 s | 206 MiB / 4,272 / 24 % | 457 MiB |
| 20x10x20 | 6,400 | 8 | 365.8 s | 17.5 | 123.4 / 338.7 / 365.8 s | 248 MiB / 4,538 / 24 % | 465 MiB |

Ready latency by level, 20x10x20 with `-c 4` (mounts include the policies of the same OpenBao
namespace):

| Level | Objects | p50 | p95 | max |
| --- | --- | --- | --- | --- |
| parent namespaces | 20 | 2.1 s | 3.6 s | 3.6 s |
| connections | 20 | 6.4 s | 71.8 s | 72.0 s |
| parent mounts | 480 | 37.8 s | 58.4 s | 375.8 s |
| parent auth | 80 | 8.1 s | 19.5 s | 20.7 s |
| child namespaces | 200 | 25.2 s | 76.0 s | 79.1 s |
| child mounts | 4,800 | 190.2 s | 369.4 s | 392.8 s |
| child auth | 800 | 37.9 s | 101.7 s | 118.0 s |

The other phases of 20x10x20 with `-c 4`:

- Steady: 104.7 reconciles/s (expected 106.3), 105.4 OpenBao requests/s, no OpenBao write and no
  Kubernetes write over three intervals. The manager used 29 % of a core and 228 MiB.
- Drift: 50 policies tampered, repaired after 24.4 s (p50), 58.3 s (p95), 64.8 s (max), each written
  once and none again in the two intervals after.
- Delete: all 6,400 objects gone after 330.7 s (370.1 s with `-c 8`); finalize p50 64.5 s, p95
  273.8 s. OpenBao then held the 220 orphaned namespaces with no KV mount, no kubernetes auth mount
  and no policy left in them; the auth configs went with their mounts.
- Token accessors went from 2 to 22 after the ramp (one per connection) and back to 2 after delete.
  Entities and entity aliases did not change, and the goroutine count went back to its idle level
  of about 3,900.

OpenBao grew from 76 MiB idle to about 460 MiB for 220 namespaces and 4,620 mounts, about 1.7 MiB
per namespace, and kept that memory after delete because the namespaces stay.

The full 100x10x20 shape was not run. Its ramp and delete are bound by OpenBao's mount writes. At
the 19 mounts per second of the 10x5x10 run below, 23,100 mounts take about 20 minutes to create;
that run deleted about 14 mounts per second, so delete takes about 27 minutes. OpenBao would need
about 2 GiB by the same per-namespace growth.

### How the operator handles a tree

- OpenBao serialises mount changes: every enable, tune and disable takes the server-wide mount
  table lock, whatever the OpenBao namespace. Mount creation is therefore the ramp's ceiling, and
  more workers gain little (doubling them gained 7 % in the 20x10x20 run). The operator sends a
  mount's config with the enable, one locked write instead of an enable and a tune; a tune follows
  only for user lockout, which enable does not accept.
- A tenant connection created before its OpenBao namespace reports `ReferenceNotReady` and retries
  after 2 to 30 s, so the objects behind it do not wait for the next connection probe.
- A write that gets 404 "no handler for route", because its mount does not exist yet, is a
  `ReferenceNotReady` wait with one Info line per retry. A 404 on read still means absent.

A 10x5x10 tree (1,150 objects, 660 mounts) on the same host:

| Measure | Result |
| --- | --- |
| Ramp, all ready | 40.2 s (28.6 objects/s) |
| Mounts per second | 19.1 |
| Connection ready p95 | 3.6 s |
| OpenBao PUTs in the ramp | 1,857 |
| Failed reconciles | 0 |
| Delete, all gone | 46.1 s |

## Tuning

- `--write-budget-per-minute`: caps, per `BaoServer`, the rewrites per minute of objects the
  operator already holds (drift repairs, changed defaults, new write-only inputs). Creates,
  adoptions and spec changes are not counted. With the default of 60, a drift repair or default
  change across N objects takes N/60 minutes.
- `--max-concurrent-reconciles` (default 4 per kind) and `--max-concurrent-reconciles-high-volume`
  (default 8, for kinds with many objects such as `ACLPolicy`, roles, entities and groups): creates
  scale with workers per kind until OpenBao or the Kubernetes API slows down. Raise them for a few
  kinds with thousands of objects. Every worker is a goroutine on each of the 74 built-in
  controllers, so do not raise them for everything.
- `--drift-interval-critical` and `--drift-interval-standard`: the steady OpenBao read rate is N
  divided by the interval, for example 5,000 objects at 10 minutes is 8.3 reads/s. Repair of
  out-of-band changes takes up to 1.2 intervals.
- `BaoServer.spec.rateLimit` (unset means no limit): a token bucket shared by every connection
  to one server, with a smaller share for drift reads. Set `qps` below what the OpenBao cluster can
  serve next to its other clients; at the default intervals the operator needs only a few requests
  per second.
- OpenBao availability: after an outage, resources wait for the next connection probe (1 minute
  ±20 %). Then the connection watch re-queues them and each server takes them back at 100 per
  second, so expect up to about 72 s plus N/100 s before every object is ready again.
- Namespace trees: OpenBao creates and removes mounts one at a time, so 1,000 mounts take about a
  minute to create and a little longer to delete on a single-node raft server like the one
  measured, whatever `--max-concurrent-reconciles` says.

## Related

- [Observability](observability.md)
- [Architecture](../architecture.md)
- [Tenant quotas](tenant-quotas.md)
- [Helm values](../reference/helm-values.md)
