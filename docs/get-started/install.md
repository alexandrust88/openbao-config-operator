# Install

This guide installs the operator on a Kubernetes cluster and chooses its scope, tenancy model,
availability and monitoring. The operator configures an OpenBao server that already runs; it does
not install one. To try it on a laptop first, follow the [Quickstart on kind](quickstart.md).

## Before you begin

- Kubernetes 1.31 or later (the chart's `kubeVersion`) and `kubectl` with cluster-admin rights for
  the install, which creates CRDs and cluster roles.
- Helm 3.8 or later, for OCI charts.
- An initialised, unsealed OpenBao server that the operator pods can reach.
- An OpenBao identity for every connection, with a policy from
  [OpenBao policy for the operator](../operate/openbao-policy.md).

## 1. Verify the release

Each release publishes a signed image, a signed chart and signed release assets, with SBOM and
provenance attestations. The released chart pins the image by digest (`image.digest`), so a
verified chart pulls the verified image. Verify before you install:
[Hardening: verify the release](../security/hardening.md#verify-the-release) has the `cosign` and
`gh attestation` commands. From a clone of the repository, `hack/release/verify-release.sh @TAG@`
runs all of them. The script needs Helm 4; if `helm` on your `PATH` is Helm 3, set `HELM` to a
Helm 4 binary.

### Verify the chart signature

Since v0.3.0, each release also signs the chart with the project's GPG key (Helm provenance). The
file `openbao-config-operator-<version>.tgz.prov` sits next to the chart on the release page and is
a layer of the OCI chart. v0.2.0 and earlier have no provenance file, so `--verify` fails for them.

1. Download the public key and show its fingerprint:

    ```bash
    curl -fsSLo openbao-config-operator.asc https://alexandrust88.github.io/openbao-config-operator/charts/pgp-public-key.asc
    gpg --show-keys openbao-config-operator.asc
    ```

    The fingerprint must be `FECBF5840F506776191497D4561068F2CF22E472`, the key Artifact Hub
    shows for the chart.

2. Helm 4 reads the armoured key file `openbao-config-operator.asc` as it is. Helm 3 reads only a
   binary keyring, so convert the key for Helm 3:

    ```bash
    gpg --dearmor < openbao-config-operator.asc > openbao-config-operator.gpg
    ```

3. Check the chart without installing it. With Helm 4, pass
   `--keyring openbao-config-operator.asc` instead:

    ```bash
    helm pull oci://ghcr.io/alexandrust88/charts/openbao-config-operator --version @VERSION@ \
      --verify --keyring openbao-config-operator.gpg
    ```

    Helm prints `Signed by:`, `Using Key With Fingerprint:` and `Chart Hash Verified:` lines.

4. Add `--verify --keyring <keyring file>` to the `helm install` command in [2. Install](#2-install),
   with either Helm method. Helm stops before it installs anything when the signature does not verify.

## 2. Install

Choose one method. The chart is the full installation; `install.yaml` is a fixed, single-replica
manifest without values.

=== "Helm (OCI)"

    ```bash
    helm install openbao-config-operator \
      oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
      --version @VERSION@ \
      --namespace openbao-config-operator --create-namespace \
      --values values.yaml
    ```

    Pin the chart by digest in GitOps tools that support it; the release notes list the digest.

=== "Helm repository"

    The same chart is served from a classic Helm repository on this site, for tools without OCI
    support. The repository lists only charts that match their release's signed checksums, but
    `helm install` from it checks no signature unless you add `--verify` (see
    [Verify the chart signature](#verify-the-chart-signature)).

    ```bash
    helm repo add openbao-config-operator https://alexandrust88.github.io/openbao-config-operator/charts
    helm repo update openbao-config-operator
    helm install openbao-config-operator openbao-config-operator/openbao-config-operator \
      --version @VERSION@ \
      --namespace openbao-config-operator --create-namespace \
      --values values.yaml
    ```

=== "install.yaml"

    Each GitHub release has an `install.yaml` asset: the CRDs, the RBAC and a single-replica
    manager Deployment in the namespace `openbao-config-operator-system`, watching every
    namespace. Use the chart when you need high availability, a narrower scope, the
    PodDisruptionBudget, a NetworkPolicy or monitoring objects.

    ```bash
    kubectl apply --server-side \
      -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/install.yaml
    ```

`values.yaml` holds your settings; the sections below name the values that matter most, and
[Helm values](../reference/helm-values.md) lists all of them. Leave `--values` out to install the
defaults: two replicas with leader election, cluster-wide scope, the NetworkPolicy and HTTPS
metrics.

## 3. Check the installation

Wait for the manager:

=== "Helm"

    ```bash
    kubectl -n openbao-config-operator rollout status deployment/openbao-config-operator --timeout=5m
    ```

    ```text title="Expected output"
    deployment "openbao-config-operator" successfully rolled out
    ```

=== "install.yaml"

    ```bash
    kubectl -n openbao-config-operator-system rollout status \
      deployment/openbao-config-operator-controller-manager --timeout=5m
    ```

    ```text title="Expected output"
    deployment "openbao-config-operator-controller-manager" successfully rolled out
    ```

Then check that the CRDs exist:

```bash
kubectl get crd -o name | grep -c openbaoconfig.io
```

The count is the number of kinds the release installs. The kinds that need an external OpenBao
plugin are left out unless `pluginKinds.enabled` is `true`.

Confirm:

- [ ] the manager Deployment is available;
- [ ] `kubectl get crd openbaoservers.core.openbaoconfig.io` finds the CRD.

Next, [connect the operator to OpenBao](connect.md).

## Uninstall

Follow the steps in order, and keep the manager running until step 6. These finalizers are
released only by a running manager:

| finalizer | on | released when |
|-----------|----|---------------|
| `openbaoconfig.io/finalizer` | a resource with `deletionPolicy: Delete` | the manager has deleted its OpenBao object, or left it on purpose |
| `openbaoconfig.io/in-use` | an `AccessLevel` | no `AccessGrant` or `AccessLayout` cites it |
| `openbaoconfig.io/connection-in-use` | an `OpenBaoConnection` or `ClusterOpenBaoConnection` | no resource that still carries `openbaoconfig.io/finalizer` uses it |
| `openbaoconfig.io/server-in-use` | an `OpenBaoServer` | no connection references it |

Without a manager, the objects that carry them stay `Terminating`, and so do their CRDs.

Before you start:

- The commands need `kubectl` with cluster-admin rights and `jq`. Every kind of the operator is in
  the category `openbao`, so `kubectl get openbao --all-namespaces` lists all of its objects.
- If a GitOps tool applies the resources, make the changes in Git and remove the resources from
  Git, or suspend its sync, so that it does not undo the steps.
- Remove the annotation `openbaoconfig.io/paused` from resources, Namespaces and `OpenBaoServer`s.
  The manager does not delete a paused resource and keeps its finalizer.

### 1. Choose what stays in OpenBao

`deletionPolicy` decides what happens to the OpenBao object of a resource when you delete the
resource.

=== "Keep the OpenBao configuration"

    Set `deletionPolicy: Orphan` on every resource, in Git or with this command, which patches
    every resource that has `Delete`:

    ```bash
    --8<-- "hack/docs/uninstall.sh:orphan"
    ```

    Every OpenBao object stays when you delete the resources.

=== "Remove the OpenBao configuration"

    Keep `deletionPolicy: Delete` on the resources whose OpenBao objects should go. The
    [destructive kinds](../kinds.md), such as `SecretsMount`, `AuthMount`, `OpenBaoNamespace` and
    `KVSecret`, delete only with the annotation `openbaoconfig.io/allow-destructive-delete: "true"`;
    without it, their deletion waits with the reason `DeleteGuarded`. This command annotates every
    resource that has `Delete`; the annotation has no effect on the other kinds:

    ```bash
    --8<-- "hack/docs/uninstall.sh:destructive"
    ```

    Kinds whose OpenBao object cannot be deleted, such as the auth method configurations, reject
    `Delete`; their objects go with the mount or namespace that holds them, or stay.

To remove some configuration and keep the rest, run the keep command, set `Delete` again on the
resources whose objects should go, and then run the remove command. A resource you change to
`Delete` is protected only once it carries the finalizer `openbaoconfig.io/finalizer`, which the
manager adds within seconds; if you delete it before that, its object stays in OpenBao.

Deleting the Kubernetes Namespaces is no shortcut: in a terminating Namespace, a destructive
resource without the annotation, and a resource whose connection is already gone, leave their
objects in OpenBao.

### 2. Delete the resources

Delete every resource except the connections and the servers. The command waits until they are
gone, for up to 10 minutes:

```bash
--8<-- "hack/docs/uninstall.sh:delete-resources"
```

If it times out, `kubectl get openbao --all-namespaces` shows what is left, and the `Synced`
condition of each one says why ([Stuck finalizers](../operate/troubleshooting.md#stuck-finalizers)).

### 3. Delete the connections

A connection that a resource still needs keeps its finalizer, so delete the connections only
after step 2:

```bash
--8<-- "hack/docs/uninstall.sh:delete-connections"
```

### 4. Delete the servers

```bash
--8<-- "hack/docs/uninstall.sh:delete-servers"
```

### 5. Check that nothing is left

```bash
--8<-- "hack/docs/uninstall.sh:check"
```

```text title="Expected output"
No resources found
```

### 6. Remove the manager

=== "Helm"

    ```bash
    --8<-- "hack/docs/uninstall.sh:helm-uninstall"
    ```

    With the default `crds.keep=true`, this removes the manager and keeps the CRDs and the
    hash-key Secret.

=== "install.yaml"

    ```bash
    kubectl delete \
      -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/install.yaml
    ```

    This also deletes the CRDs, so step 7 is done.

### 7. Delete the CRDs

```bash
--8<-- "hack/docs/uninstall.sh:crds"
```

[Backup and restore](../operate/backup-restore.md#uninstall-and-crd-deletion) explains what deleting the CRDs
does in OpenBao.

!!! tip "Tested in CI"
    `hack/docs/uninstall.sh` runs these steps at the end of the [tutorials](../tutorials/index.md):
    it removes the OpenBao configuration of `tenant-b`, keeps the rest, and checks both in OpenBao
    after the manager and the CRDs are gone.

## CRDs

The CRDs are chart templates, not files in the chart's `crds/` directory. Helm therefore
creates them on `helm install` and updates them on `helm upgrade`. Four values control them:

| value | default | effect |
|-------|---------|--------|
| `crds.install` | `true` | render the CRDs with the release |
| `crds.keep` | `true` | add `helm.sh/resource-policy: keep`, so `helm uninstall` does not delete the CRDs |
| `crds.annotations` | `{}` | extra annotations on every CRD |
| `crds.labels` | `{}` | extra labels on every CRD; the chart's own label keys are refused |

Keep `crds.keep=true`. Deleting a CRD deletes every custom resource of that kind. The chart refuses
`crds.install=false` together with `crds.keep=false`, because that upgrade would delete the CRDs an earlier
release installed. See
[Backup and restore](../operate/backup-restore.md#uninstall-and-crd-deletion) for what happens in OpenBao
when that occurs. The guard only sees the values of the current upgrade: Helm keeps a CRD that a release stops
rendering only if the live CRD already carries `helm.sh/resource-policy: keep`. After any revision with
`crds.keep=false`, set `crds.keep=true` in one upgrade before you switch to `crds.install=false` or turn
`pluginKinds.enabled` off.

`crds.keep=true` wins over a `helm.sh/resource-policy` in `crds.annotations`, so the annotation
appears once. With `crds.keep=false`, a `helm.sh/resource-policy` in `crds.annotations` is used as
given. A GitOps tool can read its options from `crds.annotations`, for example Argo CD:

```yaml
crds:
  annotations:
    argocd.argoproj.io/sync-options: ServerSideApply=true,Prune=false
```

### CRDs installed outside the release

To manage the CRDs outside the release, for example with `kubectl`, in an earlier GitOps wave or
from another release, apply the `crds.yaml` release asset and install the chart with
`crds.install=false`:

```sh
kubectl apply --server-side \
  -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/crds.yaml
helm install ... --set crds.install=false
```

Apply the `crds.yaml` of the new version before each `helm upgrade`; with `crds.install=false`
the chart notes link it. `crds.yaml` holds no plugin-kind CRDs. With `pluginKinds.enabled=true`,
build them from the release tag with
`kustomize build --load-restrictor LoadRestrictionsNone config/crd/plugin-kinds`.
[Upgrade](../operate/upgrade.md) describes the CRD compatibility rules.

If the CRDs already exist without Helm ownership metadata and you install with
`crds.install=true`, Helm refuses to take them over. After
`Error: INSTALLATION FAILED: unable to continue with install:` it prints, for the first CRD it finds:

```text
CustomResourceDefinition "accessgrants.access.openbaoconfig.io" in namespace "" exists and cannot be
imported into the current release: invalid ownership metadata; label validation error: missing key
"app.kubernetes.io/managed-by": must be set to "Helm"; annotation validation error: missing key
"meta.helm.sh/release-name": must be set to "openbao-config-operator"; annotation validation error:
missing key "meta.helm.sh/release-namespace": must be set to "openbao-config-operator"
```

Fix it in one of two ways:

- Leave the CRDs where they are managed now and install with `crds.install=false`.
- Hand the CRDs to this release. Stop the current owner from managing them first (an old Helm
  release deletes them on uninstall unless they carry `helm.sh/resource-policy: keep`), then label
  and annotate them for the release name and namespace you install with:

    ```sh
    kubectl get crd -o name | grep openbaoconfig.io | xargs -I{} kubectl label {} \
      app.kubernetes.io/managed-by=Helm --overwrite
    kubectl get crd -o name | grep openbaoconfig.io | xargs -I{} kubectl annotate {} \
      meta.helm.sh/release-name=openbao-config-operator \
      meta.helm.sh/release-namespace=openbao-config-operator --overwrite
    ```

The chart does not detect existing CRDs with `lookup`: `helm template` and GitOps tools render
without cluster access, so detection would drop the CRDs from the manifest and an upgrade could
delete them.

## Hash-key Secret

At start-up the manager reads the Secret `openbao-config-operator-hash-key` in its own namespace.
If the Secret is absent, the manager creates it with a random 32-byte key under the data key
`key`. Change the name with the `--hash-key-secret` flag through `extraArgs`.

The key signs `status.writeOnlyHash`, which records the write-only inputs of a resource, such as
passwords and client secrets (see [security.md](../security.md#write-only-inputs)). The Secret is
immutable, and the chart does not manage it, so `helm uninstall` leaves it in place.

If the Secret is lost, the manager creates a new key. Every resource that has write-only inputs
then writes to OpenBao once, because its recorded hash no longer matches. Nothing breaks, but
back the Secret up with the custom resources to avoid that burst
([Backup and restore](../operate/backup-restore.md#the-hash-key-secret)).

## Scope: watchNamespaces

By default the manager watches every namespace, and its ClusterRole includes access to Secrets,
ConfigMaps, ServiceAccount tokens and Events in every namespace.

Set `watchNamespaces` to limit it:

```yaml
watchNamespaces:
  - platform-openbao
  - team-a
  - team-b
```

This changes three things:

- The manager gets `--watch-namespaces=<list>` and caches objects only from the listed namespaces
  and its own namespace.
- Access to Secrets, ConfigMaps, `serviceaccounts/token`, Events and every namespaced kind of the
  operator moves from the ClusterRole to a Role and RoleBinding in each listed namespace and in
  the release namespace.
- The ClusterRole keeps only the cluster-wide rules: `OpenBaoServer` and
  `ClusterOpenBaoConnection`, read access to Namespaces (for `allowedNamespaces`), and `GET` on
  `/.well-known/openid-configuration`.

List every namespace that holds a credential, a CA bundle or a ServiceAccount that a
`ClusterOpenBaoConnection` references. A reference to an unlisted namespace fails: the connection
reports `InvalidSpec` for a Secret or ConfigMap outside the cache, and `AuthenticationFailed`
when the TokenRequest is forbidden.

## Tenancy models

Choose one model per cluster, or combine them. Connections decide which OpenBao identity a
request uses, so the user-facing `<release>-tenant-editor` role excludes both connection kinds.
Only the platform team creates connections. [Connect to OpenBao](connect.md) has the manifests.

| model | connection | OpenBao identity | use when |
|-------|------------|------------------|----------|
| Platform only | one `ClusterOpenBaoConnection`, `allowedNamespaces` limited to platform namespaces | the operator's own ServiceAccount (injected identity), a token or an AppRole | only the platform team writes configuration |
| Tenant connections | one `OpenBaoConnection` per tenant namespace | a ServiceAccount in that namespace, through the TokenRequest API | tenants configure their own OpenBao namespace with their own policy |
| Shared cluster connection | one `ClusterOpenBaoConnection` with an `allowedNamespaces` selector | one shared identity | a group of trusted namespaces shares one policy |

In the tenant model, the platform team creates the namespace's `OpenBaoConnection`, its
ServiceAccount and the matching OpenBao role. Tenants then create resources with the
`tenant-editor` role, which is aggregated into the built-in `admin` and `edit` roles. A namespaced
connection can never log in as the operator. The limit of what a tenant can do is the OpenBao
policy of its own ServiceAccount ([OpenBao policy for the operator](../operate/openbao-policy.md#tenant-operator-in-a-child-namespace)).

A shared cluster connection gives every allowed namespace the same OpenBao identity. Use it only
for namespaces that trust each other. Without `allowedNamespaces`, no namespace can use it; `{}` admits every namespace.

## Injected identity token

A `ClusterOpenBaoConnection` with Kubernetes or JWT auth and no `serviceAccountRef` logs in as
the operator's own ServiceAccount. It reads a projected token with a dedicated audience, which
the chart mounts by default; the operator refuses the automounted token, whose audience is the
Kubernetes API server:

```yaml
injectedIdentity:
  projectedToken:
    enabled: true
    audience: openbao-config-operator
    expirationSeconds: 3600
    path: /var/run/secrets/openbao/token
```

The connection's default `injectedIdentity.tokenPath` is this path; set it when you change `path`.
Make the OpenBao role accept the audience.

## High availability

The chart defaults are suitable for production:

| value | default | effect |
|-------|---------|--------|
| `replicas` | `2` | one active manager and one standby |
| `leaderElection.enabled` | `true` | passes `--leader-elect=true` or `=false`; the chart fails to render with more than one replica without it |
| `podDisruptionBudget.enabled` | `true` | rendered only when `replicas > 1`; `minAvailable: 1` by default, `maxUnavailable` wins when set |
| `topologySpreadConstraints` | spread by `kubernetes.io/hostname`, `ScheduleAnyway` | a constraint without `labelSelector` gets the pod selector labels |

Only the leader reconciles. A standby takes over when the leader's lease expires. Add a zone
constraint for clusters that span zones:

```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: ScheduleAnyway
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: ScheduleAnyway
```

Three manager flags tune throughput, through `extraArgs`:

| flag | default | meaning |
|------|---------|---------|
| `--drift-interval-critical` | `15m` | drift check interval for kinds that grant access (policies, auth mounts, roles), with jitter |
| `--drift-interval-standard` | `1h` | drift check interval for all other kinds, with jitter |
| `--max-concurrent-reconciles` | `4` | concurrent reconciles per kind |

## Monitoring

| value | default | effect |
|-------|---------|--------|
| `metrics.enabled` | `true` | HTTPS metrics on port 8443, protected by Kubernetes authentication and authorisation |
| `metrics.certSecret` | `""` | Secret with `tls.crt` and `tls.key`; empty uses a self-signed certificate |
| `serviceMonitor.enabled` | `false` | prometheus-operator ServiceMonitor |
| `prometheusRule.enabled` | `false` | prometheus-operator PrometheusRule with the operator's alerts |

A scraper needs permission to `GET /metrics`. Bind the `<fullname>-metrics-reader` ClusterRole to
the scraper's ServiceAccount:

```sh
kubectl create clusterrolebinding prometheus-openbao-config-operator-metrics \
  --clusterrole=openbao-config-operator-metrics-reader \
  --serviceaccount=monitoring:prometheus-k8s
```

The ServiceMonitor skips TLS verification by default because of the self-signed certificate. When
you set `metrics.certSecret` with `serviceMonitor.enabled=true`, set `serviceMonitor.tlsConfig.ca` (or `caFile`) and `serverName`, and
set `insecureSkipVerify: false`; otherwise the chart fails to render.

The alerts and what to do about each one are in [Runbooks](../operate/runbooks.md). The metrics are
described in [Observability](../operate/observability.md).

## NetworkPolicy

`networkPolicy.enabled=true` renders a NetworkPolicy for the manager pods:

- ingress only to the metrics port, from `networkPolicy.metrics.from`; when that list is empty,
  from the release namespace and `networkPolicy.metrics.monitoringNamespace` (`monitoring` by
  default);
- egress to DNS (`networkPolicy.dns.to`, kube-dns in `kube-system` by default);
- egress to the API server on `networkPolicy.apiServer.ports` (443 and 6443), limited to
  `networkPolicy.apiServer.cidrs` when set;
- egress to OpenBao on `networkPolicy.openbao.ports` (8200), limited to `networkPolicy.openbao.to`
  when set;
- `networkPolicy.extraEgress` for anything else, such as an external OIDC issuer.

Set the CIDRs and peers in production; the empty defaults allow any destination on those ports.

## CA bundles and extra volumes

An `OpenBaoServer` reads the OpenBao CA from a Secret or ConfigMap through `spec.caBundle`
([Connections reference](../reference/connections.md)), for example a ConfigMap that trust-manager
distributes. No volume is needed for that, and a server cannot read a CA bundle from a file.

Mount other files into the manager with `extraVolumes` and `extraVolumeMounts`. The main use is a
token file for a `ClusterOpenBaoConnection` with `auth.token.filesystem.path`, such as a Secret
that another controller keeps renewed:

```yaml
extraVolumes:
  - name: openbao-token
    secret:
      secretName: platform-openbao-token
extraVolumeMounts:
  - name: openbao-token
    mountPath: /var/run/secrets/openbao-token
    readOnly: true
```

The connection then sets `auth.token.filesystem.path: /var/run/secrets/openbao-token/token`. The
root filesystem is read-only, so every file the manager reads that is not in the image must come
from a volume.

A server without a CA bundle is verified against the system roots. To add a corporate CA to them,
mount it with `extraVolumes` and point `SSL_CERT_DIR` at the mount through `extraEnv`.

## Next steps

1. Write the OpenBao policy for each connection identity:
   [OpenBao policy for the operator](../operate/openbao-policy.md).
2. Create the connections: [Connect to OpenBao](connect.md).
3. Check `kubectl get openbao -A`, and read [Troubleshooting](../operate/troubleshooting.md) if a
   resource is not `Ready`.
