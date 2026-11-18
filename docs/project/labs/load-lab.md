---
title: Run the load OpenBao lab
description: "Run the load harness from a clone: build the manager, drive envtest and a raft OpenBao, and collect the numbers behind the load testing page."
---

# Run the load lab

This page is for contributors working in a clone of this repository. The harness produces the
numbers on [Load testing](../../operate/load-testing.md).

## What it covers

`hack/load/run.sh` builds the manager binary once, then runs `go test -tags load ./test/load/` for
each object count and kind set (the `tree` set runs once per shape). Each run starts envtest
(kube-apiserver and etcd with every CRD from `config/crd/bases`), one `bao server` on raft, and the
manager from `./cmd/` with `--watch-namespaces=load`, `--leader-elect=false`, both drift intervals
at 1 minute, `--max-concurrent-reconciles=4` and the default
`--max-concurrent-reconciles-high-volume=8` unless a run says otherwise. The manager authenticates
with its own AppRole, not a root token.

The harness creates objects with 16 parallel workers, follows `Ready` and `Synced` through
informers, scrapes `/metrics` every 2 s and reads manager and OpenBao RSS with `ps`. Goroutine
counts come from `go_goroutines`; pprof stays off. After each phase it lists
`auth/token/accessors`, `identity/entity/id` and `identity/entity-alias/id`.

The scenarios run in this order:

| scenario | what it does |
|----------|--------------|
| `ramp` | creates the objects and times each to `Ready` and `Synced` |
| `steady` | waits three drift intervals; OpenBao and Kubernetes writes must be zero |
| `drift` | changes N/10 `ACLPolicy` and `KVSecret` objects in OpenBao (at most 50 in the tree set) and times their repair; a few other `KVSecret` paths get a key the operator does not own |
| `outage` | stops OpenBao for `LOAD_OUTAGE` (default 1 minute), starts and unseals it, and times recovery |
| `delete` | deletes every object and times each finalizer |

`.github/workflows/load.yml` runs the tree set every Sunday and on demand (`workflow_dispatch`
takes the shape, scenarios, drift interval and `--max-concurrent-reconciles`). It defaults to
`20x10x20` with `ramp,steady,drift,delete`, uploads the run directory as the `load-tree` artifact
and writes the main numbers to the job summary. A scheduled failure opens an issue through
`report-failure.yml`.

## Prerequisites

Work at the repository root. Install Go and the `bao` binary (on `PATH`, or set `BAO_BIN`). Run
`make setup-envtest` once, or set `KUBEBUILDER_ASSETS` to existing envtest binaries. The `tree`
set at `20x10x20` needs several hundred MiB for OpenBao and runs for well over ten minutes per
shape.

## Run

```bash
hack/load/run.sh -n "200 1000" -k "simple mixed"
hack/load/run.sh -n 1000 -k simple -b -1
hack/load/run.sh -n 201 -k members -s ramp,steady,delete
hack/load/run.sh -k tree -t "5x3x4 20x10x20" -s ramp,steady,drift,delete
```

Flags: `-n` object counts, `-k` kind sets, `-t` tree shapes, `-c`
`--max-concurrent-reconciles`, `-s` scenarios (an unknown name fails before anything starts), `-d`
drift interval, `-b` `--write-budget-per-minute` (`-1` disables it), `-o` output directory (default
`.tmp/night/loadtest`). `LOAD_MANAGER_BIN` and `LOAD_CRD_DIR` reuse a prebuilt binary and its CRDs.

A run fails when a scenario's check fails, for example a tampered object that is not repaired or a
write during the steady phase.

## Inspect

Each run writes a directory `<kinds>-<n or shape>-mcr<C>` under the output directory, with
`results.json`, `timeseries.csv`, `objects.csv`, `events.txt`, `manager.log`, `bao.log` and
`test.log`.

Compare a run against these earlier results. They were measured with N=1000, a 60 s outage and
the default write budget (60) unless the row says otherwise, before and after three engine changes:
creates and spec changes stopped spending the write budget, `IdentityEntityAlias`, `IdentityGroupAlias` and
`IdentityGroupMembership` stopped recording a shared collection path (97 of 100 aliases had stayed in
`Conflict` at N=200), and connection recovery replaced a fixed 30 s requeue with a jittered backoff,
a connection watch and a per-server ramp.

| Run | All ready (ramp) | Reconciles per object (ramp) | PATCHes per object (ramp) | Ready after restart | Peak OpenBao req/s in recovery | Reconciles in recovery |
| --- | --- | --- | --- | --- | --- | --- |
| simple, before, wb 60 | at least 17 min (N/60); N=200 took 190.9 s | 3.8 at N=200 | 4.2 at N=200 | not run | not run | not run |
| simple, before, wb off | 25.8 s | 1.0 | 3.0 | 68.3 s | 162 | 2734 |
| simple, after, wb 60 | 12.1 s | 1.0 | 3.0 | 28.1 s | 100 | 1326 |
| mixed, before, wb off | 7.7 s | 1.0 | 3.0 | 50.1 s | 86 | 1713 |
| mixed, after, wb 60 | 9.6 s | 1.0 | 3.0 | 33.4 s | 114 | 1599 |

The peak is the highest 2 s sample between the restart and the last object ready. Before the
connection watch, the backlog was released at once: 500 (simple) and 550 (mixed) req/s for about
2 s.

A 10x5x10 tree (1,150 objects, 660 mounts) before and after three tree changes: a mount create
sends its config with the enable, namespace-not-found became a `ReferenceNotReady` wait instead of
waiting for the next probe, and a write that gets 404 "no handler for route" became a
`ReferenceNotReady` wait instead of an error:

| | Before | After |
| --- | --- | --- |
| Ramp, all ready | 75.7 s (15.2 objects/s) | 40.2 s (28.6 objects/s) |
| Mounts per second | 8.7 | 19.1 |
| Connection ready p95 | 71.3 s | 3.6 s |
| OpenBao PUTs in the ramp | 3,403 | 1,857 |
| Failed reconciles | 1,121 | 0 |
| Manager error lines | 998 | 3 (startup cache warnings for kube-system namespaces) |
| Delete, all gone | 45.8 s | 46.1 s |

## Clean up

Each run stops its envtest, OpenBao and manager processes and removes the build directory when it
ends. Remove the results when you no longer need them:

```bash
rm -rf .tmp/night/loadtest
```
