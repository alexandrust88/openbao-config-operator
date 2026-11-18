---
title: Deliver OpenBao configuration with Argo CD
description: Install Argo CD, apply one root Application, and let Argo CD deploy OpenBao, the operator and its configuration from Git in order.
---

# Deliver OpenBao configuration with Argo CD

Install Argo CD with a health check for the OpenBao Config Operator's resources, then apply one
root Application. Argo CD deploys OpenBao, the operator and the OpenBao configuration from Git, in
order, waits until each is healthy, and keeps OpenBao matching Git. Every manifest on this page is
a file from the public [gitops/](../../gitops/README.md) example, which the project's GitOps tests
deploy on every change.

The example runs OpenBao with a static unseal key and connects the operator with an administrator
token, to keep the lab short. Use a cluster you can delete;
[step 8](#8-adapt-the-example-for-production) replaces both for production.

## Already running the operator?

If you followed [Connect to OpenBao](../get-started/connect.md), you do not need the lab: add the
health check from [step 2](#2-install-argo-cd-with-the-health-check) to your Argo CD, then let one
Application deliver the folder in Git that holds your manifests, for example the `ACLPolicy`
`app-read` from [Manage ACL policies](policies.md):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: openbao-config
  namespace: argocd
spec:
  project: <project>
  source:
    repoURL: <repository-url>
    targetRevision: <revision>
    path: <folder>
  destination:
    server: https://kubernetes.default.svc
    namespace: openbao-config
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

`<project>` is an `AppProject` that allows the operator's kinds in the Kubernetes namespace
`openbao-config`, like `gitops-config` in [step 3](#3-create-the-argo-cd-projects).
`<repository-url>`, `<revision>` and `<folder>` locate your manifests. The rest of this page builds
the whole stack from scratch.

## Before you begin

- A Kubernetes cluster you can delete, with a StorageClass named `standard` (the OpenBao values
  request a 1 GiB volume from it). With [kind](https://kind.sigs.k8s.io/): `kind create cluster --name openbao-gitops`.
- `kubectl`, Helm 3.8 or later, `openssl`, and the `bao` CLI for the checks.
- The cluster can pull from `github.com`, `ghcr.io`, `quay.io`, `public.ecr.aws`,
  `argoproj.github.io`, `openbao.github.io` and `docs.obco.dev`.

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

## 2. Install Argo CD with the health check

These Helm values switch off the parts of Argo CD the example does not need, reconcile every 15
seconds, and add two health checks: one that reads the operator's `Ready` and `Synced` conditions
on every `*.obco.dev` kind, and one that reports a child `Application`'s health to its
parent, so each sync wave waits for the one before. Save this as `argocd-values.yaml`:

```yaml title="gitops/argocd/values/argocd.yaml"
global:
  image:
    repository: quay.io/argoproj/argocd
    tag: v3.6.1@sha256:296e3d74f449b2ce45265613fcb68c18ed7366488360c8ea32f5f2a93d2e5c54
dex:
  enabled: false
notifications:
  enabled: false
server:
  enabled: true
  service:
    type: ClusterIP
redis:
  image:
    repository: public.ecr.aws/docker/library/redis
    tag: 8.10.1-alpine@sha256:ef216d85ec44f4fdf7c0e6df2d1757a4734ed32fbb278cabd3181b5a27353b79
configs:
  cm:
    timeout.reconciliation: 15s
    timeout.reconciliation.jitter: 0s
    application.resourceTrackingMethod: annotation
    resource.customizations: |
      "*.obco.dev/*":
        health.lua: |
          local hs = {status = "Progressing", message = "Waiting for the operator to reconcile"}
          if obj.status == nil or obj.status.conditions == nil then
            return hs
          end
          local generation = obj.metadata.generation
          local observed = obj.status.observedGeneration
          local ready, synced
          for _, c in ipairs(obj.status.conditions) do
            if c.type == "Ready" then
              ready = c
            elseif c.type == "Synced" then
              synced = c
            end
            if observed == nil and c.type == "Ready" then
              observed = c.observedGeneration
            end
          end
          local waiting = {
            ReconcileInProgress = true,
            ReferenceNotReady = true,
            ConnectionNotReady = true,
            PluginNotRegistered = true,
            ServerNotReady = true,
            WriteBudgetExceeded = true,
          }
          if generation ~= nil and synced ~= nil and synced.observedGeneration == generation and synced.status == "False"
            and synced.reason ~= "Paused" and not waiting[synced.reason] then
            hs.status = "Degraded"
            hs.message = (synced.reason or "") .. ": " .. (synced.message or "")
            return hs
          end
          if generation ~= nil and ((observed == nil or observed < generation)
            or (ready ~= nil and ready.observedGeneration ~= nil and ready.observedGeneration < generation)
            or (synced ~= nil and synced.observedGeneration ~= nil and synced.observedGeneration < generation)) then
            hs.message = "Waiting for the operator to observe generation " .. generation
            return hs
          end
          for _, c in pairs({ready = ready, synced = synced}) do
            if c.reason == "Paused" then
              hs.status = "Suspended"
              hs.message = c.message or "Paused"
              return hs
            end
          end
          if ready == nil then
            return hs
          end
          local message = ready.message or ""
          if ready.status == "True" then
            if synced ~= nil and synced.status == "False" then
              if not waiting[synced.reason] then
                hs.status = "Degraded"
              end
              hs.message = (synced.reason or "") .. ": " .. (synced.message or "")
              return hs
            end
            hs.status = "Healthy"
            hs.message = message
            return hs
          end
          if ready.status == "False" and not waiting[ready.reason] then
            hs.status = "Degraded"
            hs.message = (ready.reason or "") .. ": " .. message
            return hs
          end
          hs.message = (ready.reason or "") .. ": " .. message
          return hs
    resource.customizations.health.argoproj.io_Application: |
      local hs = {status = "Progressing", message = "Waiting for child Application sync and health"}
      if obj.status == nil then return hs end
      if obj.status.operationState ~= nil and obj.status.operationState.phase == "Failed" then
        return {status = "Degraded", message = "Child sync failed"}
      end
      if obj.status.sync ~= nil and obj.status.sync.status == "Synced" and obj.status.health ~= nil then
        hs.status = obj.status.health.status or "Progressing"
        hs.message = obj.status.health.message or "Child Application health"
      end
      return hs
```

Install the Argo CD chart version the example is tested with:

```bash
helm repo add argo https://argoproj.github.io/argo-helm
helm repo update argo
helm upgrade --install argocd argo/argo-cd --version 10.18.0 \
  --namespace argocd --create-namespace --values argocd-values.yaml --wait --timeout 10m
kubectl wait --for=condition=Established crd/applications.argoproj.io crd/appprojects.argoproj.io --timeout=90s
```

### What the health check reports

The check reads `Ready`, `Synced` and `observedGeneration` (the top-level one, or the one on the
`Ready` condition).

| status | when |
|--------|------|
| Degraded | A current-generation `Synced=False` reports a terminal error, even if `Ready` still describes the previous successful generation |
| Progressing | no status yet, no observed generation, or the observed/Ready generation is older than `metadata.generation`, without a current terminal sync error |
| Suspended | `Ready` or `Synced` has the reason `Paused` (annotation `obco.dev/paused`) |
| Healthy | `Ready=True` for the current generation, and `Synced` is `True` or absent (connections/server) |
| Progressing | `Ready=True` but `Synced=False` reports a waiting reason |
| Degraded | `Ready=True` but `Synced=False` with a reason that is not waiting, for example `ReconcileError`, `PermissionDenied` or `Drifted` |
| Degraded | `Ready=False` with a reason that is not waiting, for example `Conflict`, `InvalidSpec`, `AuthenticationFailed` |
| Progressing | `Ready=False` with a waiting reason, or `Ready=Unknown` |

Waiting reasons resolve without a change to the object: `ReconcileInProgress`,
`ReferenceNotReady`, `ConnectionNotReady`, `PluginNotRegistered`, `ServerNotReady` and
`WriteBudgetExceeded`. A failed update can keep `Ready=True` from the last good generation while
`Synced=False` reports the new one; the check looks at that sync failure first, so an invalid Git
change shows as `Degraded` instead of `Progressing` forever.

If Argo CD is already installed, merge the `"*.obco.dev/*"` entry from
[argocd-cm-health.yaml](argocd-cm-health.yaml) into the `resource.customizations` of the source
that manages your `argocd-cm` (its Helm values, Git manifest or operator configuration). Also add
the `resource.customizations.health.argoproj.io_Application` key from the values above, unless your
Argo CD already has a health check for `Application`: without it the sync waves do not wait. Then
continue with step 3.

## 3. Create the Argo CD projects

Four `AppProject`s limit what each Application may deploy and where:

| project | Applications | may deploy |
|---------|--------------|------------|
| `gitops-root` | the root Application | `Application` objects in `argocd` |
| `gitops-openbao` | OpenBao | the OpenBao chart's objects in `openbao` |
| `gitops-operator` | the operator | the operator chart's objects in `openbao-config-operator-system`, its CRDs and cluster roles |
| `gitops-config` | the configuration | the operator's kinds in `obco-gitops` and the cluster-scoped `BaoServer`; no Secrets, Deployments or RBAC |

The configuration project is the narrowest. It shows how to keep the repository that holds OpenBao
configuration from deploying anything else:

```yaml title="gitops/argocd/projects/gitops-config.yaml"
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: gitops-config
  namespace: argocd
spec:
  sourceRepos:
    - https://github.com/alexandrust88/openbao-config-operator.git
  destinations:
    - server: https://kubernetes.default.svc
      namespace: obco-gitops
  namespaceResourceWhitelist:
    - group: ""
      kind: ConfigMap
    - group: core.obco.dev
      kind: BaoConnection
    - group: sys.obco.dev
      kind: ACLPolicy
    - group: sys.obco.dev
      kind: SecretsMount
    - group: sys.obco.dev
      kind: AuthMount
    - group: sys.obco.dev
      kind: BaoNamespace
    - group: auth.obco.dev
      kind: AppRole
    - group: secrets.obco.dev
      kind: TransitKey
  clusterResourceWhitelist:
    - group: core.obco.dev
      kind: BaoServer
  orphanedResources:
    warn: false
```

Apply the four projects from the repository:

```bash
kubectl apply -k 'https://github.com/alexandrust88/openbao-config-operator//gitops/argocd/projects?ref=main'
```

## 4. Apply the root Application

The root Application syncs the directory
[gitops/argocd/applications](../../gitops/argocd/applications/kustomization.yaml), which holds
one child Application per layer. Save this as `root.yaml`:

```yaml title="gitops/argocd/root.yaml"
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: gitops-root
  namespace: argocd
spec:
  project: gitops-root
  source:
    repoURL: https://github.com/alexandrust88/openbao-config-operator.git
    targetRevision: main
    path: gitops/argocd/applications
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    retry:
      limit: 8
      backoff:
        duration: 5s
        factor: 2
        maxDuration: 30s
    syncOptions:
      - CreateNamespace=true
      - ServerSideApply=true
      - PruneLast=true
```

```bash
kubectl apply -f root.yaml
```

Argo CD creates the three child Applications and syncs them in wave order:

| wave | Application | deploys | from |
|------|-------------|---------|------|
| 0 | `gitops-openbao` | OpenBao 2.7.2, three Raft replicas, from the OpenBao Helm chart | [gitops-openbao.yaml](../../gitops/argocd/applications/gitops-openbao.yaml), values in [gitops/openbao](../../gitops/openbao/values.yaml) |
| 10 | `gitops-operator` | the operator, from its published Helm chart | [gitops-operator.yaml](../../gitops/argocd/applications/gitops-operator.yaml) |
| 20 | `gitops-config` | the `BaoServer`, the connection and the OpenBao configuration | [gitops/config](../../gitops/config/kustomization.yaml) |

The operator's Application installs the chart from its Helm repository and takes its values from
Git, through a second source referenced as `$values`:

```yaml title="gitops/argocd/applications/gitops-operator.yaml"
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: gitops-operator
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "10"
spec:
  project: gitops-operator
  destination:
    server: https://kubernetes.default.svc
    namespace: openbao-config-operator-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    retry:
      limit: 8
      backoff:
        duration: 5s
        factor: 2
        maxDuration: 30s
    syncOptions:
      - CreateNamespace=true
      - ServerSideApply=true
      - PruneLast=true
  sources:
    - repoURL: https://docs.obco.dev/charts
      chart: openbao-config-operator
      targetRevision: 1.0.0
      helm:
        releaseName: openbao-config-operator
        valueFiles:
          - $values/gitops/argocd/values/operator.yaml
    - repoURL: https://github.com/alexandrust88/openbao-config-operator.git
      targetRevision: main
      ref: values
```

The configuration in [gitops/config](../../gitops/config/kustomization.yaml) declares, in the root
OpenBao namespace and in a child OpenBao namespace `gitops-team`: a KV version 2 mount
`gitops-kv`, a transit mount and key, an AppRole login and the policy `gitops-reader`. It connects
with the token from step 1:

```yaml title="gitops/config/connection.yaml"
apiVersion: core.obco.dev/v1alpha1
kind: BaoServer
metadata:
  name: gitops-openbao
spec:
  address: https://openbao-active.openbao.svc:8200
  caBundle:
    configMapRef:
      name: openbao-ca
      namespace: obco-gitops
      key: ca.crt
  allowedNamespaces:
    matchLabels:
      kubernetes.io/metadata.name: obco-gitops
  allowedAudiences:
    - openbao
---
apiVersion: core.obco.dev/v1alpha1
kind: BaoConnection
metadata:
  name: gitops-openbao
  namespace: obco-gitops
spec:
  serverRef:
    name: gitops-openbao
  auth:
    token:
      secretRef:
        name: gitops-bootstrap
        key: token
```

## 5. Wait for the waves

The root Application creates its children one wave at a time and reports `Synced` only when all
three exist, which is after OpenBao and the operator are healthy. Wait for that, then for the
configuration:

```bash
kubectl -n argocd wait application/gitops-root --for=jsonpath='{.status.sync.status}'=Synced --timeout=25m
kubectl -n openbao wait statefulset/openbao --for=jsonpath='{.status.readyReplicas}'=3 --timeout=10m
kubectl -n argocd wait application/gitops-openbao application/gitops-operator application/gitops-config \
  --for=jsonpath='{.status.health.status}'=Healthy --timeout=10m
kubectl -n argocd get applications
```

The three child Applications show `Synced` and `Healthy`; the root Application follows within a
reconciliation. The configuration Application turns `Healthy`
only after the operator has written every object to OpenBao, because the health check of step 2
reads the operator's conditions, not the presence of the resources.

To watch the waves and each operator resource's health in the Argo CD UI, forward its Service in a
second terminal:

```bash
kubectl -n argocd port-forward svc/argocd-server 8080:443
```

Open `https://localhost:8080`, accept the self-signed certificate and log in as `admin` with the
password the chart generated:

```bash
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d
```

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

Argo CD sees the change through its watch on the live object, and `selfHeal` restores the spec
from Git within seconds; the operator then writes it to OpenBao.

## 8. Adapt the example for production

- Fork the repository. Change `repoURL` and `targetRevision` in `root.yaml` and in every child
  Application (both Git sources of `gitops-openbao`, the values source of `gitops-operator`, the
  source of `gitops-config`), and the matching `sourceRepos` of each `AppProject`. Pin
  `targetRevision` to a tag or commit rather than `main`.
- Run OpenBao your way: remove `gitops-openbao.yaml` from the applications directory and the
  `gitops-openbao` project, then set `address` and `caBundle` of the `BaoServer` in
  `gitops/config/connection.yaml` to your OpenBao.
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
- Review the Argo CD values against your own Argo CD standards; they are not a hardening baseline.

!!! warning "Trust boundary"
    The repository Argo CD reconciles from is an administrator trust boundary: whoever can change
    its Applications changes what Argo CD applies within their projects, OpenBao configuration
    included. Before you keep `prune: true` in production, review each resource's
    `creationPolicy` and `deletionPolicy` ([ADR 0004](../adr/0004-creation-and-deletion-policy-defaults.md))
    and the [destructive-delete safeguards](../operate/backup-restore.md#deletion-policy-and-orphaned-objects).
    The example uses `deletionPolicy: Orphan`, so pruning a resource leaves the OpenBao object.

## Troubleshooting

| symptom | check |
|---------|-------|
| `gitops-openbao` stays `Progressing` | `kubectl -n openbao get pods,pvc`: the pods need the Secrets `openbao-tls` and `openbao-e2e` from step 1 and a volume from the StorageClass `standard` |
| An Application reports `ComparisonError` or a project denial | the resource's group, kind and Kubernetes namespace are in its `AppProject`, and `sourceRepos` lists the repository |
| `kubectl get obco` reports no such resource type right after the install | kubectl cached the API list before the CRDs existed: name the kinds, or run `kubectl api-resources --categories=obco` to refresh |
| `gitops-config` resources report `AuthenticationFailed` | the Secret `gitops-bootstrap` in `obco-gitops` holds the same token as `openbao-e2e` in `openbao` |
| The connection reports a certificate error | the ConfigMap `openbao-ca` in `obco-gitops` holds the CA that signed `openbao-tls` |
| A resource stays `Progressing` | its `observedGeneration`, the connection's `Ready` condition and the resources it references |
| The Application is `Synced` but not `Healthy` | sync describes the Kubernetes manifests; health describes the operator's result. Read the resource's conditions and events |
| The resource has no custom health | the entry reached the live `argocd-cm` and was not overwritten by the source that manages Argo CD |

## Clean up

On kind, delete the cluster:

```bash
kind delete cluster --name openbao-gitops
```

On another cluster, delete the root Application first, so it stops recreating its children, then
delete each child with its resources, in reverse wave order:

```bash
kubectl -n argocd delete application gitops-root
for app in gitops-config gitops-operator gitops-openbao; do
  kubectl -n argocd patch application "$app" --type=merge \
    -p '{"metadata":{"finalizers":["resources-finalizer.argocd.argoproj.io"]}}'
  kubectl -n argocd delete application "$app" --timeout=5m
done
helm uninstall argocd --namespace argocd
kubectl delete namespace argocd obco-gitops openbao openbao-config-operator-system --ignore-not-found
kubectl delete crd applications.argoproj.io applicationsets.argoproj.io appprojects.argoproj.io --ignore-not-found
```

The finalizer makes Argo CD delete the Application's resources. `gitops-openbao` manages the
namespace `openbao`, so deleting it removes OpenBao's volumes and every secret in it, including
those from step 1. The Argo CD chart keeps its CRDs on uninstall, so the last command removes them.

## Related

- [API reference](../reference/api/index.md)
- [Status and events](../reference/status-and-events.md)
- [Deliver OpenBao configuration with Flux](flux.md)
- [Operator troubleshooting](../operate/troubleshooting.md)
- [ADR 0004: creation and deletion policy defaults](../adr/0004-creation-and-deletion-policy-defaults.md)
