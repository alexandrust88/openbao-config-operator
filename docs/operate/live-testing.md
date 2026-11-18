---
title: Run the live OpenBao kit lab
description: "Render and run the examples/live kit and make test-livekit checks from a repository clone."
---

# Run the live kit lab

For contributors working in a clone, this page renders and exercises [`examples/live`](../../examples/live/README.md)—the kustomize kit that applies every built-in kind to a running OpenBao—and runs the offline live-kit checks from the Makefile.

## What it covers

- Overlays per connection mode (`root`, `root-to-child`, `child-connection-root-login`, `child`, plus `optional`, `observe`, `all`), each in `minimal` and `full`
- Name prefixes so several overlays can share one cluster and one OpenBao
- `make live-render` for rendered YAML under `.tmp/live-render/`
- `make test-livekit` for render, field coverage, CRD validation in envtest, and root/child overlays against `bao server -dev`

## Prerequisites

- A clone of this repository; run commands from the repository root
- For render/check targets: Make and Go; the targets fetch `bin/kustomize` and the envtest assets
- The `bao` binary on the `PATH` or from `make bao`; without it the `bao server -dev` scenarios are skipped locally, so check the output for `SKIP`
- For a real cluster apply: a running operator, OpenBao reachable as in the [kit README](../../examples/live/README.md), kubectl and openssl

## Run

Render every overlay:

```bash
make live-render
```

Full live-kit suite (render, coverage, enums, one-of branches, CRD validation, root and child overlays on `bao server -dev` with the manager from `./cmd`):

```bash
make test-livekit
```

Optional environment overrides: `LIVEKIT_OVERLAYS` (default `root/minimal,root/full,child/minimal,child/full`), `LIVEKIT_COMPONENTS` (regex over scenario names), `LIVEKIT_TIMEOUT`.

Against your own OpenBao:

1. Write your own params kustomization: list `examples/live/params` under `resources` and add a `live-params` `configMapGenerator` with `behavior: merge` and the literals you change (OpenBao address, CA ConfigMap, Kubernetes namespace, the Kubernetes auth roles and ServiceAccounts, and the tenant OpenBao namespace).
2. Create input Secrets: `examples/live/create-secrets.sh --namespace <namespace>`.
3. Copy one overlay, point its `../../../params` entry at your params kustomization, and apply it with `bin/kustomize build <overlay> | kubectl apply -f -`, as the [kit README](../../examples/live/README.md) shows.

`<namespace>` is the `kubernetesNamespace` literal (default `openbao-live`), and `<overlay>` is the directory of your copied overlay.

## Inspect

```bash
kubectl get obco -n <namespace> -l live.obco.dev/overlay=root-full
```

Objects that cannot become Ready on a pure `bao server -dev` are listed with reasons under `test/livekit/exceptions/`. Naming rules, mode meaning and coexistence constraints are in the kit README.

## Clean up

Delete the applied live resources while the operator still runs, in reverse sync waves as the [kit README](../../examples/live/README.md) describes, or tear down the disposable OpenBao and cluster you pointed the kit at. The full examples set `deletionPolicy: Delete`; the minimal ones keep the default `Orphan`, so their OpenBao objects stay. Render output under `.tmp/live-render/` is local only; remove that directory when you no longer need the YAML.
