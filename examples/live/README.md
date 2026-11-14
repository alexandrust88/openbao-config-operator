# Live test kit

A kustomize kit that applies every built-in kind to a running OpenBao, in each way a connection can reach an
OpenBao namespace. Each kind has two examples: `minimal` (required fields only) and `full` (every spec field, with
realistic values). [coverage.md](coverage.md) lists the components that hold each kind.

## Layout

| path | what it is |
|------|------------|
| `params/` | the only environment-specific values, as `live-params` literals: address, CA ConfigMap, Kubernetes namespace, login roles |
| `base/` | the `OpenBaoServer` and `nameref.yaml`, which makes a `namePrefix` reach every reference and OpenBao name |
| `modes/<mode>/` | the `OpenBaoConnection` and `ClusterOpenBaoConnection` of one connection mode |
| `components/<scenario>/{minimal,full}/` | one dependency chain each, for example mount, config, role, policy |
| `sets/` | the scenario components grouped into namespaced, root-only and optional sets |
| `finalize/` | copies the params into the server and connections, and adds Argo CD sync waves by layer |
| `overlays/<mode>/<variant>/` | what you apply; `<variant>` is `minimal` or `full` |
| `create-secrets.sh` | creates the input Secrets with generated values |

## Overlays

| overlay | login | objects land in | sets |
|---------|-------|-----------------|------|
| `root` | root namespace | root namespace | namespaced, root-only |
| `root-to-child` | root namespace | `spec.namespace: <tenantNamespace>` | namespaced |
| `child-connection-root-login` | root namespace, `authNamespace: ""` | connection namespace `<tenantNamespace>` | namespaced |
| `child` | inside `<tenantNamespace>` | `<tenantNamespace>` and below | namespaced |
| `optional` | root namespace | root namespace | database, RabbitMQ and alternative connection logins |
| `observe` | as `root` | as `root` | the objects of `root`, switched to `managementPolicy: Observe` |
| `all` | | | `root`, `root-to-child` and `optional` together |

Every overlay prefixes its Kubernetes names and OpenBao names (`root-min-`, `r2c-full-`, ...), so overlays coexist
in one Kubernetes namespace and one OpenBao server. Objects that some scenarios keep in a namespace of their own
carry the label `live.openbaoconfig.io/nested: "true"`. Exceptions, which are shared by every root overlay:

- Server-wide settings: `CORSConfig`, `QuotaConfig`, `RaftAutopilot`, `KeyRotationConfig`, the loggers, the UI
  header and the audit request header. Apply one root overlay at a time.
- `observe/<variant>` reuses the names of `root/<variant>`. Applying it switches those objects to Observe;
  applying `root/<variant>` again switches them back.

## Before you start

- The operator runs and watches the Kubernetes namespace from `kubernetesNamespace`.
- A ConfigMap with the CA of the OpenBao listener exists in that namespace (`caConfigMap`, key `caKey`).
- The ServiceAccounts named in the `live-params` literals exist, and OpenBao has Kubernetes auth roles for them:
  - `rootAuthRole` at `rootAuthMountPath` in the root namespace, with a policy over the root namespace and below
    (`root`, `root-to-child`, `optional`).
  - `tenantAuthRole` at the same mount, with a policy that only reaches `tenantNamespace`
    (`child-connection-root-login`).
  - `childAuthRole` at `childAuthMountPath` inside `tenantNamespace` (`child`).
- The OpenBao namespace `tenantNamespace` exists.

To use your own values, write a params kustomization that lists `params/` under `resources` and merges the
literals you change, then copy an overlay and point its `../../../params` entry at your params kustomization:

```yaml
resources:
  - <path to>/examples/live/params
configMapGenerator:
  - name: live-params
    behavior: merge
    literals:
      - kubernetesNamespace=my-namespace
      - openbaoAddress=https://openbao.my-domain:8200
```

## Input Secrets

The components read passwords, keys and tokens from Secrets that the kit never contains. Each component lists
them in `input-secrets.txt` as `<secret> <key> <generator>`. Create them all in one go:

```bash
examples/live/create-secrets.sh --namespace openbao-live
```

It skips Secrets that already exist, never prints a value, and needs `kubectl` and `openssl` (`ssh-keygen` and
`htpasswd` for two of the generators). `--list` shows what it would create.

## Apply, check, clean up

```bash
make kustomize
examples/live/create-secrets.sh --namespace openbao-live
bin/kustomize build examples/live/overlays/root/full | kubectl apply -f -
kubectl get openbao -n openbao-live -l live.openbaoconfig.io/overlay=root-full
```

One apply is enough: objects wait with the reason `ReferenceNotReady` until what they reference exists. Argo CD
users get the order from the `argocd.argoproj.io/sync-wave` annotations: 0 connections, 1 namespaces and plugins,
2 mounts, policies and server settings, 3 engine and auth configs, 4 roles and identities, 5 outputs and access.

Clean up in reverse waves while the operator still runs. The full examples set `deletionPolicy: Delete`; the
minimal ones keep the default `Orphan`, so their OpenBao objects stay unless you switch them to `Delete` first.
Objects the kit adopts (`creationPolicy: Adopt` or `CreateOrAdopt` of an existing setting) always use `Orphan`.

Some objects do not reach Ready everywhere, for example the database kinds without a database, `RaftAutopilot`
without raft storage, `Audit` where the server refuses audit devices from the API, and `Plugin` without a plugin
binary. `test/livekit/exceptions/` lists each one with the reason.

## Checks

`make test-livekit` renders every overlay, checks that every reference resolves and every OpenBao name carries the
overlay prefix, that every built-in kind has both variants and every spec field is set by a full example, validates
every object against the CRDs in envtest, and applies the root and child overlays to `bao server -dev`.
