---
description: "Run live OpenBao Config Operator checks and interpret test coverage, readiness results and declared exclusions."
---

# Live testing

[`examples/live`](../../examples/live/README.md) is a kustomize kit that applies every built-in kind to a running
OpenBao. It has one overlay per connection mode (`root`, `root-to-child`, `child-connection-root-login`, `child`,
plus `optional`, `observe` and `all`), each in a `minimal` and a `full` variant, and every overlay prefixes its
names so several can share a cluster.

## Run it against your OpenBao

1. Override the `live-params` literals of `examples/live/params/kustomization.yaml` (OpenBao address, CA ConfigMap,
   Kubernetes namespace, the Kubernetes auth roles and ServiceAccounts, and the tenant OpenBao namespace) in your
   own params kustomization: list `examples/live/params` under `resources` and add a `live-params`
   `configMapGenerator` with `behavior: merge` and the literals you change.
2. Create the input Secrets: `examples/live/create-secrets.sh --namespace <namespace>`.
3. Apply one overlay: `bin/kustomize build examples/live/overlays/root/full | kubectl apply -f -`.
4. Check: `kubectl get openbao -n <namespace> -l live.openbaoconfig.io/overlay=root-full`.

The [kit README](../../examples/live/README.md) has the prerequisites, the naming rules and how to clean up.

## Checks without a cluster

| command | what it does |
|---------|--------------|
| `make live-render` | renders every overlay to `.tmp/live-render/` |
| `make test-livekit` | renders and checks every overlay, checks that each built-in kind has a minimal and a full example and that the full examples set every spec field, validates every object against the CRDs in envtest, and applies the root and child overlays to `bao server -dev` with the manager built from `./cmd` |

`make test-livekit` reads `LIVEKIT_OVERLAYS` (overlays to apply, default `root/minimal,root/full,child/minimal,child/full`),
`LIVEKIT_COMPONENTS` (a regular expression over scenario names) and `LIVEKIT_TIMEOUT`. Objects that cannot be
Ready on a dev server are listed with the reason in `test/livekit/exceptions/`.
