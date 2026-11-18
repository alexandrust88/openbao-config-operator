---
title: Deliver OpenBao configuration with Flux
description: Install Flux, apply ordered Kustomizations, and let Flux deploy OpenBao, the operator and its configuration from Git with readiness gates.
---

# Deliver OpenBao configuration with Flux

Install Flux, then apply three ordered Flux Kustomizations. Flux deploys OpenBao, the OpenBao
Config Operator and the OpenBao configuration from Git, waits until the operator reports each
resource `Ready` and `Synced`, and keeps OpenBao matching Git. Every manifest on this page is a
file from the public [gitops/](../../gitops/README.md) example, which the project's GitOps tests
deploy on every change.

The example runs OpenBao with a static unseal key and connects the operator with an administrator
token, to keep the lab short. Use a cluster you can delete;
[step 9](#9-adapt-the-example-for-production) replaces both for production.

## Already running the operator?

If you followed [Connect to OpenBao](../get-started/connect.md), you do not need the lab: point a
Flux `Kustomization` at the folder in Git that holds your manifests, with the health expressions
from [step 4](#4-apply-the-ordered-kustomizations) so dependants wait for the operator:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: openbao-config
  namespace: flux-system
spec:
  interval: 5m
  path: <folder>
  prune: true
  sourceRef:
    kind: GitRepository
    name: <git-repository>
  wait: true
  timeout: 5m
  healthCheckExprs:
    - apiVersion: sys.obco.dev/v1alpha1
      kind: ACLPolicy
      current: has(status.conditions) && status.conditions.exists(c, c.type == 'Ready' && c.status == 'True' && c.observedGeneration == metadata.generation) && status.conditions.exists(c, c.type == 'Synced' && c.status == 'True' && c.observedGeneration == metadata.generation)
      failed: has(status.conditions) && status.conditions.exists(c, (c.type == 'Ready' || c.type == 'Synced') && c.status == 'False' && c.observedGeneration == metadata.generation && c.reason == 'ReconcileError')
```

`<git-repository>` is your Flux `GitRepository` and `<folder>` the path of your manifests in it,
for example the `ACLPolicy` `app-read` from [Manage ACL policies](policies.md). Add one expression per
kind you deliver. Save the file under a name other than `kustomization.yaml`, which
kustomize-controller reads as a kustomize configuration. The rest of this page builds the whole
stack from scratch.

## Before you begin

- A Kubernetes cluster you can delete, with a StorageClass named `standard` (the OpenBao values
  request a 1 GiB volume from it). With [kind](https://kind.sigs.k8s.io/): `kind create cluster --name openbao-gitops`.
- `kubectl`, Helm 3.8 or later, `openssl`, and the `bao` CLI for the checks.
- The cluster can pull from `github.com`, `ghcr.io` and `openbao.github.io`.

## 1. Create the material that stays out of Git

The TLS certificate, the unseal key and the first administrator token never go to Git. Create
the two Kubernetes namespaces that hold them:

```bash
kubectl create namespace openbao
kubectl create namespace obco-gitops
```

Create a private CA and the OpenBao server certificate in a new working directory. The names
cover the Services the OpenBao chart creates in the Kubernetes namespace `openbao`:

```bash
mkdir openbao-tls && cd openbao-tls
sans=DNS:localhost,IP:127.0.0.1
for name in openbao openbao-active openbao-internal '*.openbao-internal'; do
  sans+=",DNS:$name,DNS:$name.openbao.svc,DNS:$name.openbao.svc.cluster.local"
done
openssl ecparam -genkey -name prime256v1 -noout -out ca.key
openssl ecparam -genkey -name prime256v1 -noout -out tls.key
openssl req -new -key ca.key -subj /CN=openbao-ca -out ca.csr
openssl x509 -req -sha256 -in ca.csr -signkey ca.key -days 365 -out ca.crt \
  -extfile <(printf 'basicConstraints=critical,CA:TRUE\nsubjectKeyIdentifier=hash\nkeyUsage=critical,keyCertSign,cRLSign\n')
openssl req -new -key tls.key -subj /CN=openbao -out tls.csr
openssl x509 -req -sha256 -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 365 -out tls.crt \
  -extfile <(printf 'subjectAltName=%s\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid:always\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\n' "$sans")
rm ca.key ca.csr tls.csr ca.srl
```

Store the certificate, a random static unseal key and a random first administrator token. The
OpenBao values in Git read them from the Secrets `openbao-tls` and `openbao-e2e`:

```bash
kubectl -n openbao create configmap openbao-ca --from-file=ca.crt=ca.crt
kubectl -n openbao create secret generic openbao-tls --type=kubernetes.io/tls \
  --from-file=tls.crt=tls.crt --from-file=tls.key=tls.key --from-file=ca.crt=ca.crt
kubectl -n openbao create secret generic openbao-e2e \
  --from-literal=seal-key="$(openssl rand -hex 32)" \
  --from-literal=token="$(openssl rand -hex 24)"
```

On its first start, OpenBao initialises itself and creates that token with an administrator
policy ([gitops/openbao/bootstrap/init.yaml](../../gitops/openbao/bootstrap/init.yaml)). Give the
operator the same token and the CA, in the Kubernetes namespace where its configuration lives:

```bash
kubectl -n obco-gitops create secret generic gitops-bootstrap \
  --from-literal=token="$(kubectl -n openbao get secret openbao-e2e -o jsonpath='{.data.token}' | base64 -d)"
kubectl -n obco-gitops create configmap openbao-ca --from-file=ca.crt=ca.crt
```

Delete the server key from your disk; the Secret holds it now. Keep this shell in the
`openbao-tls` directory: the last steps use `ca.crt`.

```bash
rm tls.key
```

!!! warning "Lab material only"
    The Secret `openbao-e2e` holds the static unseal key next to a token that can do everything in
    OpenBao. Anyone who can read Secrets in `openbao` or `obco-gitops` controls OpenBao, and the
    unseal key also decrypts its storage and every Raft snapshot. Use this only on a cluster you
    will delete.

## 2. Install Flux

Install the [Flux Operator](https://fluxoperator.dev/docs/) chart and image the example is tested
with (chart 0.62.1 has the digest
`sha256:1135514a01cc11e206f5ff0b568ac30d9defd675022b7a9da88d4a3474fbdf13`):

```bash
helm upgrade --install flux-operator oci://ghcr.io/controlplaneio-fluxcd/charts/flux-operator --version 0.62.1 \
  --set-string image.tag=v0.62.1@sha256:1d09a3f96524d0895fb11e8072d84a3dd029cecdec0f869efa371be6b001469c \
  --namespace flux-system --create-namespace --wait --timeout 3m
```

A `FluxInstance` tells the Flux Operator which Flux version and controllers to run. Save this as
`flux-instance.yaml`:

```yaml title="gitops/flux/flux-instance.yaml"
apiVersion: fluxcd.controlplane.io/v1
kind: FluxInstance
metadata:
  name: flux
  namespace: flux-system
spec:
  distribution:
    # renovate: datasource=github-releases depName=fluxcd/flux2
    version: 2.10.0
    registry: ghcr.io/fluxcd
  components:
    - source-controller
    - kustomize-controller
    - helm-controller
  cluster:
    type: kubernetes
    size: small
    networkPolicy: true
```

```bash
kubectl apply -f flux-instance.yaml
kubectl -n flux-system wait fluxinstance/flux --for=condition=Ready --timeout=5m
```

## 3. Point Flux at the repository

The `GitRepository` makes the repository available to the Kustomizations of the next step:

```yaml title="gitops/flux/bootstrap/source.yaml"
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: openbao-config-operator
  namespace: flux-system
spec:
  interval: 1m
  url: https://github.com/alexandrust88/openbao-config-operator
  ref:
    branch: main
```

## 4. Apply the ordered Kustomizations

Three Flux Kustomizations deploy the layers. Each one waits for its resources (`wait: true`), and
`config` starts only after the other two are ready:

| Kustomization | path | deploys | runs as |
|---------------|------|---------|---------|
| `openbao` | [gitops/flux/openbao](../../gitops/flux/openbao/kustomization.yaml) | OpenBao 2.7.2, three Raft replicas, from the OpenBao Helm chart | `platform-reconciler` |
| `operator` | [gitops/flux/operator](../../gitops/flux/operator/kustomization.yaml) | the operator, from its published OCI chart | `platform-reconciler` |
| `config` | [gitops/config](../../gitops/config/kustomization.yaml) | the `BaoServer`, the connection and the OpenBao configuration | `config-reconciler` |

The operator is a `HelmRelease` of the published chart, pinned by tag and digest:

```yaml title="gitops/flux/operator/source.yaml"
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: operator-chart
  namespace: flux-system
spec:
  interval: 1h
  url: oci://ghcr.io/alexandrust88/charts/openbao-config-operator
  ref:
    tag: 1.0.0
    digest: sha256:d2d6b7a0a0c2ac5f45f38ba15fabaa5e1d8caa30de122874e3a1fa98448f1442
  layerSelector:
    mediaType: application/vnd.cncf.helm.chart.content.v1.tar+gzip
    operation: copy
```

```yaml title="gitops/flux/operator/release.yaml"
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: openbao-config-operator
  namespace: flux-system
spec:
  interval: 1m
  releaseName: openbao-config-operator
  targetNamespace: openbao-config-operator-system
  serviceAccountName: platform-reconciler
  install:
    createNamespace: true
    strategy:
      name: RetryOnFailure
      retryInterval: 30s
  upgrade:
    strategy:
      name: RetryOnFailure
      retryInterval: 30s
  driftDetection:
    mode: enabled
  chartRef:
    kind: OCIRepository
    name: operator-chart
  # Keep the published chart's signed release image and CRD defaults.
  values:
    resources:
      requests:
        cpu: 50m
        memory: 128Mi
      limits:
        memory: 512Mi
```

The `config` Kustomization gates on the operator's result. For each kind it lists, a CEL
expression counts the resource healthy only when `Ready` and `Synced` are `True` for the current
generation, and failed when the operator reports `ReconcileError`. This excerpt shows the
dependency and the expression for `ACLPolicy`; the full file has one per kind
([kustomizations.yaml](../../gitops/flux/bootstrap/kustomizations.yaml)):

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: config
  namespace: flux-system
spec:
  interval: 1m
  retryInterval: 10s
  path: ./gitops/config
  prune: true
  sourceRef:
    kind: GitRepository
    name: openbao-config-operator
  wait: true
  timeout: 5m
  serviceAccountName: config-reconciler
  dependsOn:
    - name: openbao
    - name: operator
  healthCheckExprs:
    - apiVersion: sys.obco.dev/v1alpha1
      kind: ACLPolicy
      current: has(status.conditions) && status.conditions.exists(c, c.type == 'Ready' && c.status == 'True' && c.observedGeneration == metadata.generation) && status.conditions.exists(c, c.type == 'Synced' && c.status == 'True' && c.observedGeneration == metadata.generation)
      failed: has(status.conditions) && status.conditions.exists(c, (c.type == 'Ready' || c.type == 'Synced') && c.status == 'False' && c.observedGeneration == metadata.generation && c.reason == 'ReconcileError')
```

`exists` needs both conditions, so an empty list cannot pass, and old conditions cannot approve a
new spec. The two ServiceAccounts come from [rbac.yaml](../../gitops/flux/bootstrap/rbac.yaml):
`platform-reconciler` is `cluster-admin`, because it installs charts and CRDs;
`config-reconciler` may only manage the operator's kinds and ConfigMaps in `obco-gitops` and
`BaoServer` objects, and cannot read Secrets.

Apply the ServiceAccounts, the `GitRepository` and the Kustomizations from the repository:

```bash
kubectl apply -k 'https://github.com/alexandrust88/openbao-config-operator//gitops/flux/bootstrap?ref=main'
```

## 5. Wait for the layers

```bash
kubectl -n flux-system wait kustomization/openbao kustomization/operator --for=condition=Ready --timeout=15m
kubectl -n openbao wait statefulset/openbao --for=jsonpath='{.status.readyReplicas}'=3 --timeout=10m
kubectl -n flux-system wait kustomization/config --for=condition=Ready --timeout=10m
kubectl -n flux-system get kustomizations,helmreleases
```

All three Kustomizations and both `HelmRelease`s show `Ready` `True`. `config` turns ready only
after the operator has written every object to OpenBao.

## 6. Check that it worked

Every operator resource reports `Ready` and `Synced`:

```bash
kubectl -n obco-gitops get baoconnections,baonamespaces,aclpolicies.sys.obco.dev,secretsmounts,authmounts,approles,transitkeys
```

Read what OpenBao holds. Forward the active OpenBao Service to local port 18200 in a second
terminal (18200, so it does not collide with a local OpenBao or Vault on 8200):

```bash
kubectl -n openbao port-forward svc/openbao-active 18200:8200
```

In the first terminal, still in the `openbao-tls` directory, log in with the bootstrap token and
read the policy in the root and in the child OpenBao namespace `gitops-team`:

```bash
export BAO_ADDR=https://127.0.0.1:18200 BAO_CACERT="$PWD/ca.crt"
export BAO_TOKEN="$(kubectl -n openbao get secret openbao-e2e -o jsonpath='{.data.token}' | base64 -d)"
bao policy read gitops-reader
bao policy read -namespace=gitops-team gitops-reader
bao write gitops-transit/encrypt/app plaintext="$(printf hello | base64)"
```

Both policies print the two `gitops-kv` paths from Git, and the transit key returns a ciphertext.

## 7. See drift repair

The `ACLPolicy` resource `root-reader` in `obco-gitops` ([gitops/config/root.yaml](../../gitops/config/root.yaml))
writes the OpenBao policy `gitops-reader` in the root OpenBao namespace. Change that policy in
OpenBao behind the operator's back:

```bash
printf 'path "gitops-kv/data/app/*" { capabilities = ["read"] }\n' | bao policy write gitops-reader -
```

The resources in Git set `driftInterval: 30s`. Within about 40 seconds (the 30-second interval plus
up to 20% jitter) the operator writes the policy from Git again:

```bash
bao policy read gitops-reader
```

The `gitops-kv/metadata/app/*` line is back. A change to the Kubernetes resource itself is
reverted from Git in the same way:

```bash
kubectl -n obco-gitops patch aclpolicies.sys.obco.dev root-reader --type=merge \
  -p '{"spec":{"policy":"path \"gitops-kv/data/app/*\" { capabilities = [\"read\"] }"}}'
```

Flux applies the manifest from Git again at its next reconciliation, about a minute later
(`interval: 1m`), and the operator then writes it to OpenBao.

## 8. Gate your workloads on the configuration

In the Flux `Kustomization` that deploys an application which needs this configuration, add under
`spec`:

```yaml
spec:
  dependsOn:
    - name: config
      namespace: flux-system
```

Keep its other settings; `namespace` is needed when that Kustomization lives outside `flux-system`. Flux then reconciles the application only after `config` is ready, which
means the operator has reconciled every resource the health expressions cover. When you add a kind
to `gitops/config`, add a matching entry to `healthCheckExprs`; the
[status reference](../reference/status-and-events.md) describes the conditions every operator kind
reports.

## 9. Adapt the example for production

- Fork the repository. Change `url` and `ref` of the `GitRepository`, and pin `ref` to a tag or
  commit rather than the branch `main`.
- Run OpenBao your way: remove the `openbao` Kustomization and its entry in `dependsOn`, then set
  `address` and `caBundle` of the `BaoServer` in `gitops/config/connection.yaml` to your OpenBao.
- If you keep the example OpenBao, replace the static seal with a KMS, transit or HSM seal (seal
  migration), and stop loading `init.hcl` after the first start: self-initialisation runs once, and
  an `openbao-0` that loses its volume while the file is still loaded initialises a new cluster
  instead of joining the existing one.
- Replace the administrator token with a Kubernetes login and a least-privilege policy
  ([Connect to OpenBao](../get-started/connect.md),
  [OpenBao policy for the operator](../operate/openbao-policy.md)). The bootstrap token can do
  everything in OpenBao and expires after the system maximum TTL (32 days by default).
  Self-initialisation creates no recovery keys: create them with the authenticated recovery key
  rotation (`sys/rotate/recovery`) before you revoke the bootstrap token, or no root token can be
  generated again.
- The example installs the operator into the Kubernetes namespace
  `openbao-config-operator-system`; [Install](../get-started/install.md) uses
  `openbao-config-operator`. Either works, but an OpenBao role that binds the operator's
  ServiceAccount, such as the one in [OpenBao self-init](self-init.md), must name the namespace
  you chose.
- Keep credentials in your encrypted-secret or secret-delivery workflow, not in plain Git YAML.

!!! warning "Trust boundary"
    The repository Flux reconciles from is an administrator trust boundary: whoever can change the
    `openbao` and `operator` paths acts as `cluster-admin`. The `config` path runs with the narrow
    `config-reconciler` identity. Before you keep `prune: true` in production, review each
    resource's `creationPolicy` and `deletionPolicy`
    ([ADR 0004](../adr/0004-creation-and-deletion-policy-defaults.md)). The example uses
    `deletionPolicy: Orphan`, so pruning a resource leaves the OpenBao object.

## Troubleshooting

| symptom | check |
|---------|-------|
| `openbao` stays not ready | `kubectl -n openbao get pods,pvc`: the pods need the Secrets `openbao-tls` and `openbao-e2e` from step 1 and a volume from the StorageClass `standard` |
| `operator` reports a chart pull error | the cluster can reach `ghcr.io`; `kubectl -n flux-system describe ocirepository operator-chart` |
| `config` times out on health | `kubectl -n obco-gitops get obco`: find the resource that is not `Ready`, then read its conditions |
| `config` resources report `AuthenticationFailed` | the Secret `gitops-bootstrap` in `obco-gitops` holds the same token as `openbao-e2e` in `openbao` |
| The connection reports a certificate error | the ConfigMap `openbao-ca` in `obco-gitops` holds the CA that signed `openbao-tls` |
| `config` reports forbidden | the `config-reconciler` Role covers the kind; add new API groups to [rbac.yaml](../../gitops/flux/bootstrap/rbac.yaml) |
| A workload deploys before the configuration is ready | its Kustomization's `dependsOn` names `config` |

## Clean up

On kind, delete the cluster:

```bash
kind delete cluster --name openbao-gitops
```

On another cluster, delete the Kustomizations in reverse order. With `prune: true`, Flux deletes
what each one applied, but it does not wait for those objects to go, so wait before removing what
reconciles them:

```bash
kubectl -n flux-system delete kustomization config --timeout=10m
kubectl wait --for=delete baoservers.core.obco.dev/gitops-openbao --timeout=5m
for k in operator openbao; do
  kubectl -n flux-system delete kustomization "$k" --timeout=10m
done
kubectl -n flux-system wait --for=delete helmreleases/openbao-config-operator helmreleases/openbao --timeout=10m
kubectl -n flux-system delete fluxinstance flux --timeout=5m
helm uninstall flux-operator --namespace flux-system
kubectl delete clusterrolebinding gitops-platform gitops-server
kubectl delete clusterrole gitops-server
kubectl delete namespace flux-system obco-gitops openbao-config-operator-system
kubectl delete namespace openbao --ignore-not-found
```

Deleting the `openbao` Kustomization deletes the namespace `openbao`, OpenBao's volumes and every
secret in it. The two cluster role bindings come from step 4, not from Flux, so they are removed by
hand; the first one grants `cluster-admin`.

## Related

- [API reference](../reference/api/index.md)
- [Status and events](../reference/status-and-events.md)
- [Deliver OpenBao configuration with Argo CD](argocd.md)
- [Flux health-check expressions](https://fluxcd.io/flux/components/kustomize/kustomizations/#health-check-expressions)
- [ADR 0004: creation and deletion policy defaults](../adr/0004-creation-and-deletion-policy-defaults.md)
