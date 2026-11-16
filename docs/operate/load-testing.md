# Load testing

This page describes the load test in `test/load`, the numbers it produced, the bottlenecks it found and how to
tune the operator for large object counts.

## Method

`hack/load/run.sh` builds the manager binary once, then runs `go test -tags load ./test/load/` for each object
count and kind set. Each run starts a fresh environment:

- envtest: a real kube-apiserver and etcd with every CRD from `config/crd/bases`.
- OpenBao: one `bao server` on raft storage with a single unseal key, so its data survives the restart in the
  outage scenario. `bao server -dev` keeps data in memory and would come back empty.
- The manager binary from `./cmd/`, run against envtest with `--watch-namespaces=load`, `--leader-elect=false`
  (one replica; lease renewals would count as Kubernetes writes in the steady window), both drift intervals at 1 minute, `--max-concurrent-reconciles=4` and the default
  `--max-concurrent-reconciles-high-volume=8` unless a run says otherwise. It authenticates with
  its own AppRole (design rule 9), not a root token.

The harness creates the objects with 16 parallel workers, follows their `Ready` and `Synced` conditions through
informers, scrapes the manager's `/metrics` every 2 s and reads the manager's RSS and CPU time and OpenBao's RSS
with `ps`. Goroutine
counts come from `go_goroutines`; the harness leaves pprof off.

Kind sets:

| Set | Kinds |
| --- | --- |
| `simple` | `Policy` and `KVSecret`, one Secret per `KVSecret` |
| `mixed` | `Policy`, `KVSecret`, `PasswordPolicy`, `AuditRequestHeader`, `TokenRole`, `UserpassUser`, `AppRole`, `Entity`, `Group`, `TransitKey` |
| `refs` | `EntityAlias` with `entityRef` to an `Entity`; aliases are applied 5 s before their entities |
| `members` | `GroupMembership` with `entityRef` and `groupRef`; memberships are applied first. Use a multiple of 3 for N |
| `tree` | an OpenBao namespace hierarchy, sized by a shape instead of N; see [Namespace tree](#namespace-tree) |

Scenarios, in order:

1. **ramp**: create N objects; time from each create to `Ready=True`, `Synced=True` for the current generation.
2. **steady**: three drift intervals with no change. OpenBao writes and Kubernetes writes must be zero.
3. **drift**: change N/10 `Policy` and `KVSecret` objects in OpenBao and time their repair. Five other `KVSecret`
   paths get a key the operator does not own; their KV version counts the operator's writes to them exactly.
4. **outage**: stop OpenBao for 60 s, start and unseal it, and time until the connection and every object are
   healthy again.
5. **delete**: delete every object (`deletionPolicy: Delete`) and time until each is finalized; check OpenBao.

After each phase the harness lists `auth/token/accessors`, `identity/entity/id` and `identity/entity-alias/id`.
Only the managed `Entity` and `EntityAlias` objects may change them (design rule 2).

### Run it

```sh
hack/load/run.sh -n "200 1000" -k "simple mixed"
hack/load/run.sh -n 1000 -k simple -b -1          # write budget off
hack/load/run.sh -n 201 -k members -s ramp,steady,delete
hack/load/run.sh -k tree -t "5x3x4 20x10x20" -s ramp,steady,drift,delete
```

Flags: `-n` object counts, `-k` kind sets, `-t` tree shapes, `-c` `--max-concurrent-reconciles`, `-s` scenarios
(an unknown name fails the run before anything starts), `-d` drift
interval, `-b` `--write-budget-per-minute` (`-1` disables it), `-o` output directory (default
`.tmp/night/loadtest`). `LOAD_MANAGER_BIN` and `LOAD_CRD_DIR` reuse a prebuilt binary and its CRDs. Each run
writes `results.json`, `timeseries.csv`, `objects.csv`, `events.txt`, `manager.log` and `bao.log`.

`.github/workflows/load.yml` runs the tree set every Sunday and on demand (`workflow_dispatch` takes the shape,
scenarios, drift interval and `--max-concurrent-reconciles`). It defaults to `20x10x20` with
`ramp,steady,drift,delete`, uploads the run directory as the `load-tree` artifact and writes the main numbers to
the job summary. A scheduled failure opens an issue through `report-failure.yml`.

### Limits of these numbers

- One laptop (darwin/arm64, 10 CPUs) runs envtest, OpenBao, the manager and the harness. Other work kept the
  load average between 50 and 180 during these runs (recorded per run in `results.json`), so latencies are
  pessimistic and vary between runs. Counts of requests, writes, reconciles and logins are exact.
- No TLS, one OpenBao node, no network latency, no Kubernetes API priority and fairness limits.
- The 1 minute drift interval compresses time; production defaults are longer.

## Results

Manager build of 2026-11-14 ~01:50. `wb` is `--write-budget-per-minute`; `-1` disables the budget.

### Ramp

| Run | All ready | Ready p50 / p95 / p99 | OpenBao requests | Reconciles | Peak RSS |
| --- | --- | --- | --- | --- | --- |
| simple, N=200, wb 60 (default) | 190.9 s | 64.2 / 185.9 / 190.0 s | 1262 (1062 GET, 200 PUT) | 761 | 124 MiB |
| simple, N=1000, wb off | 25.8 s | 12.8 / 23.9 / 24.7 s | 3500 (2500 GET, 1000 PUT) | 1000 | 153 MiB |
| mixed, N=200, wb off | 2.9 s | 0.9 / 2.5 / 2.7 s | 660 (440 GET, 220 PUT) | 200 | 125 MiB |
| mixed, N=1000, wb off | 7.7 s | 2.1 / 5.9 / 6.6 s | 3300 (2200 GET, 1100 PUT) | 1000 | 159 MiB |
| simple, N=5000, wb off | 91.6 s | 33.1 / 77.7 / 84.2 s | 19106 (14106 GET, 5000 PUT) | 6605 | 196 MiB |

The same harness on the build of ~01:25, which had no write budget, made N=200 ready in 3.2 s.

With the budget off, a create costs one reconcile (1.3 at N=5000), about 2.5 OpenBao reads and 1 write (read,
write, read back; `KVSecret` also reads the current version for check-and-set) and 3 Kubernetes PATCHes plus one
event. Throughput is bounded by the workers per kind: `Policy` (8 workers) and `KVSecret` (4 workers) together
reached 39 objects/s at N=1000 and 55 objects/s at N=5000 on the loaded host; the ten mixed kinds reached 130
objects/s.

### Steady state

| Run | Reconciles/s (expected N/60 s) | OpenBao requests/s | Requests per 10 s bucket min / p50 / p95 / max | OpenBao writes | Kubernetes writes |
| --- | --- | --- | --- | --- | --- |
| simple, N=200 | 3.23 (3.33) | 3.22 | 0.5 / 2.9 / 6.3 / 6.3 | 0 | 0 |
| simple, N=1000 | 15.8 (16.7) | 15.8 | 0 / 13.8 / 34.3 / 34.3 | 0 | 0 |
| mixed, N=200 | 2.99 (3.33) | 3.27 | 0 / 1.2 / 9.9 / 9.9 | 0 | 0 |
| mixed, N=1000 | 15.2 (16.7) | 16.7 | 0 / 5.1 / 44.1 / 44.1 | 0 | 0 |
| simple, N=5000 | 81.3 (83.3) | 81.3 | 52.6 / 76.9 / 107.1 / 107.1 | 0 | 0 |

Every drift check costs one OpenBao read and no Kubernetes write. The rate stays below N/interval in the first
interval after a ramp, because objects created together first come back between 0.8 and 1.2 intervals later. The
±20 % jitter spreads that cohort over 24 s; the 10 s buckets show the spread, which widens with every interval.
Logins stayed at one per connection, and token accessors and entity aliases did not change in any run.

### Drift storm

| Run | Tampered | Repair p50 / p95 / max | Writes | Writes after repair |
| --- | --- | --- | --- | --- |
| simple, N=200 | 20 | 22.7 / 52.8 / 61.9 s | 20 | 0 |
| simple, N=1000 | 100 | 35.4 / 61.0 / 69.7 s | 100 | 0 |
| mixed, N=200 | 20 | 48.2 / 58.7 / 60.3 s | 20 | 0 |
| mixed, N=1000 | 100 | 42.1 / 63.1 / 72.7 s | 100 | 0 |
| simple, N=5000 | 500 | 39.9 / 66.2 / 75.7 s | 500 | 0 |

Repair time is the time to the object's next drift check, at most 1.2 drift intervals. Each tampered object is
written exactly once. A `KVSecret` path with a key the operator does not own was never rewritten.

### OpenBao outage (60 s)

| Run | Objects unhealthy | Connection ready after restart | All objects ready after restart | Peak requests/s in recovery | Reconciles in recovery |
| --- | --- | --- | --- | --- | --- |
| simple, N=200 | 200 | 11.5 s | 41.2 s | 14 | 261 |
| simple, N=1000 | 1000 | 38.2 s | 68.3 s | 157 | 2734 |
| mixed, N=200 | 200 | 18.8 s | 48.9 s | 21 | 310 |
| mixed, N=1000 | 1000 | 20.2 s | 50.1 s | 66 | 1713 |
| simple, N=5000 | 5000 | 30.7 s | 61.0 s | 513 | 10149 |

No object was rewritten after the restart, and the operator did not log in again: its token was still valid.

### Delete

| Run | All finalized | Finalize p50 / p95 |
| --- | --- | --- |
| simple, N=200 | 3.6 s | 1.3 / 3.3 s |
| simple, N=1000 | 16.0 s | 8.0 / 14.4 s |
| mixed, N=200 | 1.3 s | 0.3 / 1.0 s |
| mixed, N=1000 | 7.6 s | 1.4 / 6.2 s |
| simple, N=5000 | 66.6 s | 24.4 / 59.8 s |

### References

Referrers wait for their referents with a jittered backoff from 2 s to 30 s. On the build of ~01:25, 20
`EntityAlias` objects applied 5 s before their `Entity` objects were ready 0.4 to 6.6 s (p50 4.2 s) after the
entities were applied. On the ~01:50 build the `refs` run at N=200 could not measure this: 97 of 100 aliases stayed
in `Conflict` (bottleneck 2). The `members` set hits the same conflict.

On the build of ~04:00 (default write budget) both sets are clean: `refs` at N=200 made all 100 aliases ready
(OpenBao holds 101 entity aliases, the operator's own included), `members` at N=201 made all 67 memberships ready,
with no `Conflict` event in either run. Referrers were ready 4.8 s (p50) and 8.0 s (max) after their referents were
applied for `refs`, 5.8 s and 9.1 s for `members`.

### Resources

The idle manager on that run used about 4,400 goroutines: 68 controllers (plugin kinds off), each with its workers, informers and queues. A default install now starts 74 controllers, one per built-in kind. The count stayed flat through every scenario and after deletion, so nothing
leaks. RSS was about 55 MiB idle, 125 to
160 MiB with 1,000 objects and 232 MiB with 5,000. CPU peaked at 70 % of one core during the mixed N=1000 ramp;
in steady state it was 7.5 % at N=1000 and 31 % at N=5000 with the 1 minute interval (81 drift checks/s).

## Namespace tree

The `tree` set builds the OpenBao namespace hierarchy of a multi-tenant server. `-t` takes the shape
`PARENTSxCHILDRENxKV[xROLESxPOLICIES]` (roles and policies default to 3); the default `100x10x20` is 100 parent
namespaces with 10 children each: 1,100 namespaces, 32,000 objects and 23,100 mounts.

- Each parent namespace and the objects in it go through the root connection, with `spec.namespace` set to the
  parent (the `root-to-child` mode of `examples/live`).
- Each parent gets its own `OpenBaoConnection` with `namespace: <parent>` and `authNamespace: ""` (the
  `child-connection-root-login` mode). It creates the child namespaces and the objects in them, so a run with 100
  parents has 101 connections and 101 operator tokens.
- Every namespace holds one `kubernetes` `AuthMount` with its `KubernetesAuthConfig` (pinned to the envtest CA),
  KV `SecretsMount`s of type `kv-v2`, ROLES `KubernetesAuthRole`s and POLICIES `Policy` objects.
- The harness applies everything at once, in the order of the Argo CD sync waves but without waiting between
  them: parent namespaces, connections, parent mounts and policies, parent auth configs and roles, child
  namespaces, child mounts and policies, child auth configs and roles. The ramp reports latency per level.
- Drift tampers with 50 `Policy` objects, which stays under the default write budget of 60 rewrites a minute.
- Delete goes level by level in reverse, each level after the one before is gone. Mounts, policies and roles use
  `deletionPolicy: Delete` with the destructive-delete annotation. Namespaces keep the default `Orphan`, and
  `KubernetesAuthConfig` can only be `Orphan`.
- After the ramp and after delete, the harness walks every namespace with the root token and counts the KV mounts,
  the kubernetes auth mounts and the policies. Token accessors must grow by one per connection and drop back after
  the connections are deleted.

### Results

Apple M1 Max (darwin/arm64, 10 CPUs, 32 GB), load average 2 to 6, other envtest and OpenBao processes running
next to the harness. OpenBao 2.7.2 on raft, drift intervals 1 minute (30 s for 5x3x4), default write budget.
`-c` is `--max-concurrent-reconciles`; `Policy` and `KubernetesAuthRole` use the high-volume default of 8.

| Shape | Objects | `-c` | All ready | Objects/s | Ready p50 / p95 / max | Manager peak RSS / goroutines / CPU | OpenBao peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 5x3x4 | 265 | 4 | 68.1 s | 3.9 | 3.5 / 67.0 / 68.0 s | 131 MiB / 3,982 / 11 % | 114 MiB |
| 20x10x20 | 6,400 | 4 | 393.2 s | 16.3 | 132.5 / 364.4 / 392.8 s | 206 MiB / 4,272 / 24 % | 457 MiB |
| 20x10x20 | 6,400 | 8 | 365.8 s | 17.5 | 123.4 / 338.7 / 365.8 s | 248 MiB / 4,538 / 24 % | 465 MiB |

Ready latency by level, 20x10x20 with `-c 4` (mounts include the policies of the same namespace):

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

- Steady: 104.7 reconciles/s (expected 106.3), 105.4 OpenBao requests/s, no OpenBao write and no Kubernetes
  write over three intervals. The manager used 29 % of a core and 228 MiB.
- Drift: 50 policies tampered, repaired after 24.4 s (p50), 58.3 s (p95), 64.8 s (max), each written once and
  none again in the two intervals after.
- Delete: all 6,400 objects gone after 330.7 s (370.1 s with `-c 8`); finalize p50 64.5 s, p95 273.8 s. OpenBao
  then held the 220 orphaned namespaces with no KV mount, no kubernetes auth mount and no policy left in them;
  the auth configs went with their mounts.
- Token accessors went from 2 to 22 after the ramp (one per connection) and back to 2 after delete. Entities and
  entity aliases did not change, and the goroutine count went back to its idle level of about 3,900.

OpenBao grew from 76 MiB idle to about 460 MiB for 220 namespaces and 4,620 mounts, about 1.7 MiB per namespace,
and kept that memory after delete because the namespaces stay. The harness's OpenBao has no telemetry stanza.

The full 100x10x20 shape was not run on this laptop. Its ramp and delete are bound by OpenBao's mount writes
(below) at about 12 to 14 mounts per second, so 23,100 mounts would take about 30 minutes to create and as long to
delete, and OpenBao would need about 2 GiB by the same per-namespace growth.

### Bottlenecks

The numbers above predate the fixes below.

1. **OpenBao serialises mount changes.** Every enable, tune and disable takes the server-wide mount table lock
   (`Core.mountsLock`, `internal/vault/mount.go` in OpenBao), whatever the namespace, so mount creation is the
   ramp's ceiling and doubling the workers gained 7 %. Fixed in the operator's share: a mount create now sends its
   config with the enable, one locked write instead of an enable and a tune (a tune follows only for user lockout,
   which enable does not accept).
2. **A tenant connection created before its namespace waited for the next probe** (1 minute ±20 %), and every
   child object behind it waited too. Fixed: namespace-not-found now reports `ReferenceNotReady` and retries after
   2 to 30 s.
3. **Objects created before their mount failed with errors.** A write that gets 404 "no handler for route" (a role
   before its auth mount, objects in a namespace whose `sys/` is still mounting) logged an error with a stack trace
   on each retry, about 3,000 lines in the 20x10x20 ramp. Fixed: it is now a `ReferenceNotReady` wait with one
   Info line per retry. Reads are unchanged: a 404 on read still means absent.

10x5x10 tree (1,150 objects, 660 mounts), `-s ramp,delete`, same laptop, before (main at `a0a0b5b`) and after:

| | Before | After |
| --- | --- | --- |
| Ramp, all ready | 75.7 s (15.2 objects/s) | 40.2 s (28.6 objects/s) |
| Mounts per second | 8.7 | 19.1 |
| Connection ready p95 | 71.3 s | 3.6 s |
| OpenBao PUTs in the ramp | 3,403 | 1,857 |
| Failed reconciles | 1,121 | 0 |
| Manager error lines | 998 | 3 (startup cache warnings for kube-system namespaces) |
| Delete, all gone | 45.8 s | 46.1 s |

## Before and after the engine fixes

The build of ~04:00 fixes the three bottlenecks below. Same harness and host, N=1000, default write budget (60),
60 s outage. Re-run with `hack/load/run.sh` to reproduce the numbers.

| Run | All ready (ramp) | Reconciles per object (ramp) | PATCHes per object (ramp) | Ready after restart | Peak OpenBao req/s in recovery | Reconciles in recovery |
| --- | --- | --- | --- | --- | --- | --- |
| simple, before, wb 60 | at least 17 min (N/60); N=200 took 190.9 s | 3.8 at N=200 | 4.2 at N=200 | not run | not run | not run |
| simple, before, wb off | 25.8 s | 1.0 | 3.0 | 68.3 s | 162 | 2734 |
| simple, after, wb 60 | 12.1 s | 1.0 | 3.0 | 28.1 s | 100 | 1326 |
| mixed, before, wb off | 7.7 s | 1.0 | 3.0 | 50.1 s | 86 | 1713 |
| mixed, after, wb 60 | 9.6 s | 1.0 | 3.0 | 33.4 s | 114 | 1599 |

The three PATCHes per object are the claim recorded before the create, the finalizer and the final status. The
peak is the highest 2 s sample between the restart and the last object ready. The recovery window held one status
PATCH and one OpenBao read per object.

- Creates and spec changes no longer spend the write budget, so a first sync runs at full speed and no create
  got `WriteBudgetExceeded`. Only rewrites of objects the operator already holds spend it: drift repairs, changed
  defaults and new write-only inputs. The drift storm (100 tampered objects) now exceeds the budget: 36 (simple)
  and 29 (mixed) repairs waited, and the last repair landed after 122 s and 120 s instead of 70 s and 73 s. Each
  tampered object was still written exactly once.
- A held-back rewrite keeps a fixed condition message and retries at a random point of the next window. A retry
  that is held back again patches nothing.
- Resources whose connection is not ready retry with a jittered backoff from 2 s to 60 s, and the connection watch
  re-queues them when the connection turns ready. The first pass after that goes through a per-server ramp of 100
  passes per second (burst 10), and its requests run without priority, so `OpenBaoServer.spec.rateLimit`
  holds them to the drift share when it is set. Without the ramp, the watch released the whole backlog at once:
  500 (simple) and 550 (mixed) req/s for about 2 s. With it, recovery ran at about 100 req/s for 10 s. The
  connection probe (1 minute ±20 %) still decides when the connection turns ready: 18 to 23 s after the restart
  in these runs.
- The backoff makes more cheap retries while OpenBao is down: about 6 reconciles per object during the outage,
  against 1.1 to 1.8 before. They read only the cache: no OpenBao request and no status PATCH (about one PATCH
  per object for the whole outage, as before).

Steady state still wrote nothing to OpenBao or Kubernetes over three intervals, and delete took 12.9 s (simple)
and 6.1 s (mixed).

## Bottlenecks found

All three are fixed on the build of ~04:00 (see above).

1. **The write budget throttles creates.** `--write-budget-per-minute` (default 60) applies to every write,
   including creates and spec changes. A first sync of N objects therefore takes at least N/60 minutes: about
   17 minutes for 1,000 objects and 83 minutes for 5,000. While waiting, each object re-reads OpenBao and patches
   its status every minute, so at N=200 the ramp cost 3.8 reconciles, 5.3 reads and 4.2 status patches per
   object instead of 1, 2.5 and 3. All waiting objects wake in the same 12 s band.
2. **Shared collection paths conflict.** `EntityAlias` and `GroupAlias` record the collection path, so the
   ownership check lets only one of them own `identity/entity-alias`. At N=200, 97 of 100 aliases stayed in
   `Conflict`. `GroupMembership` has the same problem with `identity/group`.
3. **Connection recovery waits a fixed 30 s.** A resource whose connection is not ready requeues after exactly
   30 s and does not watch the connection. During an outage the work queue backs up behind slow failing
   reconciles; when the connection turns not ready the backlog stalls within seconds, so the 30 s timers line up.
   After the restart, recovery peaked at 10 times the steady request rate at N=1000 (157 against 15.8 req/s) and
   6 times at N=5000 (513 against 81 req/s), and every stalled object re-ran every 30 s until its connection was
   ready again: 10,149 reconciles for 5,000 objects. Objects were ready 50 to 68 s after OpenBao.

Steady state writes nothing (design rule 1), the operator logs in once per connection (design rule 2), and drift checks are
jittered. Errors use controller-runtime's per-item exponential backoff. On the ~01:50 build the connection-not-ready requeue was the one periodic wait without jitter. The
cache is scoped to `--watch-namespaces`, the client reads Secrets live and the Secret cache drops their data.

## Tuning

- `--write-budget-per-minute`: caps the rewrites per minute of objects the operator already holds (drift
  repairs, changed defaults, new write-only inputs). Creates and spec changes are not counted. With the default
  of 60, a drift repair or default change across N objects takes N/60 minutes.
- `--max-concurrent-reconciles` (default 4 per kind, 8 for high-volume kinds): creates scale with workers per
  kind until OpenBao or the Kubernetes API slows down. Raise it for a few kinds with thousands of objects. Every
  worker is a goroutine on each of the 74 built-in controllers, so do not raise it for everything.
- `--drift-interval-critical` and `--drift-interval-standard` (the old `--drift-interval` flag is removed): the steady
  OpenBao read rate is N divided by the interval, for example 5,000 objects at 10 minutes is 8.3 reads/s. Repair
  of out-of-band changes takes up to 1.2 intervals.
- `OpenBaoServer.spec.rateLimit` (unset means no limit): a token bucket shared by every connection to one server,
  with a smaller share for drift reads. Set `qps` below what the OpenBao cluster can serve next to its other
  clients; at the default intervals the operator needs only a few requests per second.
- OpenBao availability: after an outage, resources wait for the next connection probe (1 minute ±20 %). Then
  the connection watch re-queues them and each server takes them back at 100 per second, so expect up to about
  72 s plus N/100 s before every object is ready again.
- Namespace trees: OpenBao creates and removes mounts one at a time, so 1,000 mounts take a little over a minute
  on storage like this laptop's, whatever `--max-concurrent-reconciles` says. A tenant connection created before
  its OpenBao namespace exists reports `ReferenceNotReady` and retries after 2 to 30 s.
