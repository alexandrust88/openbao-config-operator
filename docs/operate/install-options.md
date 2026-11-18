---
title: Install options for OpenBao Config Operator
description: Advanced OpenBao Config Operator install options — CRDs, hash-key Secret, watchNamespaces, tenancy, HA, monitoring, NetworkPolicy and volumes.
---

# Install options

Set CRD ownership, namespace scope, tenancy, availability, monitoring and extra volumes for the
operator's Helm release. Every value, with its default, is listed in [Helm values](../reference/helm-values.md).

## CRDs

The CRDs are chart templates, not files in the chart's `crds/` directory. Helm therefore
creates them on `helm install` and updates them on `helm upgrade`. Four values control them:

| value | effect |
|-------|--------|
| `crds.install` | render the CRDs with the release |
| `crds.keep` | add `helm.sh/resource-policy: keep`, so `helm uninstall` does not delete the CRDs |
| `crds.annotations` | extra annotations on every CRD |
| `crds.labels` | extra labels on every CRD; the chart's own label keys are refused |

Defaults are in [Helm values](../reference/helm-values.md).

Keep `crds.keep=true`. Deleting a CRD deletes every custom resource of that kind. The chart refuses
`crds.install=false` together with `crds.keep=false`, because that upgrade would delete the CRDs an earlier
release installed. See
[Backup and restore](backup-restore.md#uninstall-and-crd-deletion) for what happens in OpenBao
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

```bash
kubectl apply --server-side \
  -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/crds.yaml
helm install openbao-config-operator \
  oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version @VERSION@ \
  --namespace openbao-config-operator --create-namespace \
  --set crds.install=false
```

Apply the `crds.yaml` of the new version before each `helm upgrade`; with `crds.install=false`
the chart notes link it. `crds.yaml` holds no plugin-kind CRDs, and no release asset does. With
`pluginKinds.enabled=true`, build them from a clone of the repository at the release tag, with
kustomize installed:
`kustomize build --load-restrictor LoadRestrictionsNone config/crd/plugin-kinds`.
[Upgrade](upgrade.md) describes the CRD compatibility rules.

If the CRDs already exist without Helm ownership metadata and you install with
`crds.install=true`, Helm refuses to take them over. After
`Error: INSTALLATION FAILED: unable to continue with install:` it prints, for the first CRD it finds:

```text
CustomResourceDefinition "accessgrants.access.obco.dev" in namespace "" exists and cannot be
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

    ```bash
    kubectl get crd -o name | grep obco.dev | xargs -I{} kubectl label {} \
      app.kubernetes.io/managed-by=Helm --overwrite
    kubectl get crd -o name | grep obco.dev | xargs -I{} kubectl annotate {} \
      meta.helm.sh/release-name=openbao-config-operator \
      meta.helm.sh/release-namespace=openbao-config-operator --overwrite
    ```

The chart does not detect existing CRDs with `lookup`: `helm template` and GitOps tools render
without cluster access, so detection would drop the CRDs from the manifest and an upgrade could
delete them.

## Hash-key Secret

At start-up the manager reads the Secret `openbao-config-operator-hash-key` in its own namespace.
If the Secret is absent, the manager creates it with a random 32-byte key under the data key
`key`. The chart always uses this name: it refuses `--hash-key-secret` in `extraArgs`, because the
namespace-scoped Role grants access to this Secret name only.

The key signs `status.writeOnlyHash`, which records the write-only inputs of a resource, such as
passwords and client secrets (see [Write-only inputs](../security.md#write-only-inputs)). The Secret is
immutable, and the chart does not manage it, so `helm uninstall` leaves it in place.

If the Secret is lost, the manager creates a new key. Every resource that has write-only inputs
then writes to OpenBao once, because its recorded hash no longer matches. Nothing breaks, but
back the Secret up with the custom resources to avoid that burst
([Backup and restore](backup-restore.md#the-hash-key-secret)).

## Scope: watchNamespaces

By default the manager watches every namespace, and its ClusterRole includes access to Secrets,
ConfigMaps, ServiceAccount tokens and Events in every namespace.

Set `watchNamespaces` to limit it:

```yaml
watchNamespaces:
  - openbao-config
  - team-a
  - team-b
```

List the release namespace (`openbao-config-operator`) as well only if it holds a Secret,
ConfigMap or ServiceAccount that a `BaoServer` or `ClusterBaoConnection` references.

This changes three things:

- The manager gets `--watch-namespaces=<list>` and caches objects only from the listed namespaces.
- Access to Secrets, ConfigMaps, `serviceaccounts/token`, Events and every namespaced kind of the
  operator moves from the ClusterRole to a Role and RoleBinding in each listed namespace. Unless
  listed, the release namespace gets only `get` on the hash-key Secret and `create` on Secrets.
- The ClusterRole keeps only the cluster-wide rules: `BaoServer` and
  `ClusterBaoConnection`, read access to Namespaces (for `allowedNamespaces`), and `GET` on
  `/.well-known/openid-configuration`.

List every namespace that holds a credential, a CA bundle or a ServiceAccount that a
`ClusterBaoConnection` references. A reference to an unlisted namespace fails: the connection
reports `InvalidSpec` for a Secret or ConfigMap outside the cache, and `AuthenticationFailed`
when the TokenRequest is forbidden.

## Tenancy models

Choose one model per cluster, or combine them. Connections decide which OpenBao identity a
request uses, so the user-facing `<release>-tenant-editor` role excludes both connection kinds.
Only the platform team creates connections. [Connect to OpenBao](../get-started/connect.md) has the manifests.

| model | connection | OpenBao identity | use when |
|-------|------------|------------------|----------|
| Platform only | one `ClusterBaoConnection`, `allowedNamespaces` limited to platform namespaces | the operator's own ServiceAccount (injected identity), a token or an AppRole | only the platform team writes configuration |
| Tenant connections | one `BaoConnection` per tenant namespace | a ServiceAccount in that namespace, through the TokenRequest API | tenants configure their own OpenBao namespace with their own policy |
| Shared cluster connection | one `ClusterBaoConnection` with an `allowedNamespaces` selector | one shared identity | a group of trusted namespaces shares one policy |

In the tenant model, the platform team creates the namespace's `BaoConnection`, its
ServiceAccount and the matching OpenBao role. Tenants then create resources with the
`tenant-editor` role, which is aggregated into the built-in `admin` and `edit` roles. A namespaced
connection can never log in as the operator. The limit of what a tenant can do is the OpenBao
policy of its own ServiceAccount ([OpenBao policy for the operator](openbao-policy.md#tenant-operator-in-a-child-namespace)).

A shared cluster connection gives every allowed namespace the same OpenBao identity. Use it only
for namespaces that trust each other. Without `allowedNamespaces`, no namespace can use it; `{}` admits every namespace.

## Injected identity token

A `ClusterBaoConnection` with Kubernetes or JWT auth and no `serviceAccountRef` logs in as
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

| value | effect |
|-------|--------|
| `replicas` | number of manager pods: one leader reconciles, the others stand by |
| `leaderElection.enabled` | passes `--leader-elect=true` or `=false`; the chart fails to render with more than one replica without it |
| `podDisruptionBudget.enabled` | rendered only when `replicas > 1`, with `podDisruptionBudget.minAvailable`; `maxUnavailable` wins when set |
| `topologySpreadConstraints` | a constraint without `labelSelector` gets the pod selector labels |

Defaults are in [Helm values](../reference/helm-values.md).

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

Five manager flags tune throughput, through `extraArgs`:

| flag | meaning |
|------|---------|
| `--drift-interval-critical` | drift check interval for kinds that grant access (policies, auth mounts, roles), with jitter |
| `--drift-interval-standard` | drift check interval for all other kinds, with jitter |
| `--max-concurrent-reconciles` | concurrent reconciles per kind, except the high-volume kinds |
| `--max-concurrent-reconciles-high-volume` | concurrent reconciles of the kinds that usually have many objects: roles, policies, entities and groups |
| `--write-budget-per-minute` | rewrites per minute per `BaoServer` of objects whose spec did not change; creates, adoptions and spec changes are exempt, `-1` disables the cap |

Defaults are in [Constants and timings](../reference/constants.md).

## Monitoring

| value | effect |
|-------|--------|
| `metrics.enabled` | HTTPS metrics on port 8443, protected by Kubernetes authentication and authorisation |
| `metrics.certSecret` | Secret with `tls.crt` and `tls.key`; empty uses a self-signed certificate |
| `serviceMonitor.enabled` | render a prometheus-operator ServiceMonitor |
| `prometheusRule.enabled` | render a prometheus-operator PrometheusRule with the operator's alerts |

Defaults are in [Helm values](../reference/helm-values.md).

A scraper needs permission to `GET /metrics`. Bind the `<fullname>-metrics-reader` ClusterRole to
the scraper's ServiceAccount:

```bash
kubectl create clusterrolebinding prometheus-openbao-config-operator-metrics \
  --clusterrole=openbao-config-operator-metrics-reader \
  --serviceaccount=monitoring:prometheus-k8s
```

The ServiceMonitor skips TLS verification by default because of the self-signed certificate. When
you set `metrics.certSecret` with `serviceMonitor.enabled=true`, set `serviceMonitor.tlsConfig.ca` (or `caFile`) and `serverName`, and
set `insecureSkipVerify: false`; otherwise the chart fails to render.

The alerts and what to do about each one are in [Runbooks](runbooks.md). The metrics are
described in [Observability](observability.md).

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

On Cilium, set `networkPolicy.flavor: cilium`: the chart renders a CiliumNetworkPolicy that reaches
the API server through the `kube-apiserver` entity, and limits OpenBao egress with
`networkPolicy.openbao.fqdns`. `metrics.from`, `dns.to`, `apiServer.cidrs` and `openbao.to` apply
to the `kubernetes` flavour only.

## CA bundles and extra volumes

A `BaoServer` reads the OpenBao CA from a Secret or ConfigMap through `spec.caBundle`
([Connections reference](../reference/connections.md)), for example a ConfigMap that trust-manager
distributes. No volume is needed for that, and a server cannot read a CA bundle from a file.

Mount other files into the manager with `extraVolumes` and `extraVolumeMounts`. The main use is a
token file for a `ClusterBaoConnection` with `auth.token.filesystem.path`, such as a Secret
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

## Related

- [Helm values](../reference/helm-values.md)
- [Install](../get-started/install.md)
- [Upgrade](upgrade.md)
- [Uninstall](uninstall.md)
- [Connect to OpenBao](../get-started/connect.md)
- [Backup and restore](backup-restore.md)
