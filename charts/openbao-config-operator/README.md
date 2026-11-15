# openbao-config-operator

Helm chart for openbao-config-operator, which manages the configuration of an existing OpenBao
server through Kubernetes custom resources. See [docs/architecture.md](../../docs/architecture.md).

```sh
helm install openbao-config-operator oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version 0.1.0 --namespace openbao-config-operator --create-namespace
```

## What it installs

- CRDs as templates, so `helm upgrade` upgrades them. They carry `helm.sh/resource-policy: keep` by default, so
  `helm uninstall` leaves every custom resource in place.
- The manager Deployment (2 replicas with leader election, restricted pod security), its ServiceAccount, a
  PodDisruptionBudget and an HTTPS metrics Service protected by Kubernetes authentication and authorization.
- RBAC for the manager, and two user-facing ClusterRoles:
  - `<release>-tenant-editor`: every namespaced kind except `OpenBaoConnection`. A connection decides which
    OpenBao identity is used, so only the platform team manages it. Aggregated into `admin` and `edit`.
  - `<release>-viewer`: read-only access to every kind. Aggregated into `view`.

## CRDs managed outside the release

`crds.annotations` and `crds.labels` add metadata to every CRD, for example
`argocd.argoproj.io/sync-options: ServerSideApply=true`. `crds.keep=true` wins over a `helm.sh/resource-policy` in
`crds.annotations`.

To install the CRDs separately (`kubectl apply --server-side -f crds.yaml` from the GitHub release, a GitOps wave or
another release), install the chart with `crds.install=false` and apply the new `crds.yaml` before each upgrade.
`crds.yaml` has no plugin-kind CRDs.

CRDs that exist without Helm ownership metadata make an install with `crds.install=true` fail:

```text
CustomResourceDefinition "accessgrants.access.openbaoconfig.io" in namespace "" exists and cannot be imported into
the current release: invalid ownership metadata; label validation error: missing key "app.kubernetes.io/managed-by":
must be set to "Helm"; annotation validation error: missing key "meta.helm.sh/release-name": must be set to
"openbao-config-operator"; annotation validation error: missing key "meta.helm.sh/release-namespace": must be set to
"openbao-config-operator"
```

Either install with `crds.install=false`, or stop the current owner managing them and adopt them into the release:

```sh
kubectl get crd -o name | grep openbaoconfig.io | xargs -I{} kubectl label {} app.kubernetes.io/managed-by=Helm --overwrite
kubectl get crd -o name | grep openbaoconfig.io | xargs -I{} kubectl annotate {} \
  meta.helm.sh/release-name=openbao-config-operator meta.helm.sh/release-namespace=openbao-config-operator --overwrite
```

The chart does not detect existing CRDs with `lookup`: `helm template` and GitOps render without a cluster, so the
CRDs would drop out of the manifest and an upgrade could delete them.

## Watching a subset of namespaces

With `watchNamespaces` set, the manager gets `--watch-namespaces=<list>`, and access to Secrets, ConfigMaps,
ServiceAccount tokens, Events and every namespaced managed kind is granted by a Role in each listed namespace instead
of cluster-wide. List every
namespace that holds credentials or CA bundles referenced by a `ClusterOpenBaoConnection`.

## Metrics

Metrics are served on port 8443 over HTTPS. A scraper needs a token allowed to `GET /metrics`: bind the
`<fullname>-metrics-reader` ClusterRole to its ServiceAccount. Without `metrics.certSecret` the manager uses a
self-signed certificate, so the ServiceMonitor skips verification by default.

## Injected identity

A `ClusterOpenBaoConnection` with `auth.kubernetes.injectedIdentity` or `auth.jwt.injectedIdentity` logs in as the
manager's own ServiceAccount. The chart mounts a projected token for `injectedIdentity.projectedToken.audience`
(default `openbao-config-operator`) at `/var/run/secrets/openbao/token`, the CRD's default `tokenPath`, so OpenBao
never receives a token that is valid against the API server. The operator refuses the automounted token. If you
change `injectedIdentity.projectedToken.path`, set the connection's `tokenPath` to the same file.

## Values

| key | default | description |
|-----|---------|-------------|
| `crds.install` | `true` | install and upgrade the CRDs with the release |
| `crds.keep` | `true` | annotate CRDs with `helm.sh/resource-policy: keep` |
| `crds.annotations` | `{}` | extra annotations on every CRD; with `crds.keep: true` a `helm.sh/resource-policy` here is replaced by `keep` |
| `crds.labels` | `{}` | extra labels on every CRD; the chart's own label keys are refused |
| `image.repository` | `ghcr.io/alexandrust88/openbao-config-operator` | manager image |
| `image.tag` | `""` | defaults to the chart `appVersion` |
| `image.digest` | `""` | `sha256:...`; pulls by digest when set |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | |
| `replicas` | `2` | more than one requires `leaderElection.enabled` |
| `leaderElection.enabled` | `true` | passes `--leader-elect=true` or `=false` |
| `watchNamespaces` | `[]` | empty means cluster-wide |
| `pluginKinds.enabled` | `false` | installs the CRDs of kinds that need an external OpenBao plugin and passes `--enable-plugin-kinds` |
| `apiServerAudiences` | `[]` | passes `--apiserver-audiences`; API server audiences a tenant connection may never request, besides the built-in ones (`https://kubernetes.default.svc[.cluster.local]`, `kubernetes.default.svc`, `rke2`, `k3s`) and the discovered issuer |
| `serviceAccount.create` / `.name` / `.annotations` | `true` / `""` / `{}` | `name` is required when `create` is false |
| `injectedIdentity.projectedToken.enabled` | `true` | mount a projected token for injected identity logins |
| `injectedIdentity.projectedToken.audience` / `.expirationSeconds` / `.path` | `openbao-config-operator` / `3600` / `/var/run/secrets/openbao/token` | token audience, lifetime (minimum 600) and file |
| `rbac.create` | `true` | manager ClusterRole, Roles and bindings |
| `rbac.userRoles` | `true` | tenant-editor and viewer ClusterRoles |
| `rbac.aggregate.admin` / `.edit` / `.view` | `true` | aggregation labels on the user roles |
| `metrics.enabled` | `true` | HTTPS metrics endpoint and Service |
| `metrics.port` | `8443` | |
| `metrics.certSecret` | `""` | Secret with `tls.crt` and `tls.key` for the metrics endpoint |
| `metrics.service.type` / `.annotations` | `ClusterIP` / `{}` | only `ClusterIP` |
| `serviceMonitor.enabled` | `false` | prometheus-operator ServiceMonitor |
| `serviceMonitor.labels` / `.interval` / `.scrapeTimeout` | `{}` / `30s` / `10s` | |
| `serviceMonitor.tlsConfig` | `{insecureSkipVerify: true}` | with `metrics.certSecret`, `ca` (or `caFile`) and `serverName` are required and `insecureSkipVerify` is refused |
| `serviceMonitor.relabelings` / `.metricRelabelings` | `[]` | |
| `prometheusRule.enabled` | `false` | PrometheusRule with the alerts in [runbooks.md](../../docs/operate/runbooks.md) |
| `prometheusRule.selector` | `""` | series selector; empty means `job="<fullname>-metrics"` in the release namespace |
| `prometheusRule.labels` / `.alertLabels` / `.disabledAlerts` | `{}` / `{}` / `[]` | rule object labels, labels on every alert, alerts to leave out |
| `prometheusRule.reconcileErrorRatio` / `.openbaoErrorRatio` / `.steadyStateWritesFor` | `0.1` / `0.05` / `30m` | alert thresholds |
| `health.port` | `8081` | `/healthz` and `/readyz` |
| `pprof.enabled` / `.port` | `false` / `8082` | Go pprof on `127.0.0.1` in the pod, no Service; use `kubectl port-forward` |
| `extraArgs` / `extraEnv` | `[]` | extra manager flags and environment; flags with a dedicated value are refused |
| `extraVolumes` / `extraVolumeMounts` | `[]` | extra volumes, for example a CA bundle trusted through `SSL_CERT_DIR` |
| `resources` | requests `50m`/`128Mi`, limit `512Mi` | |
| `podSecurityContext` / `securityContext` | restricted | non-root 65532, read-only root, no capabilities, RuntimeDefault seccomp |
| `livenessProbe` / `readinessProbe` | delays and periods | merged into the httpGet probes |
| `podDisruptionBudget.enabled` | `true` | rendered only when `replicas > 1` |
| `podDisruptionBudget.minAvailable` / `.maxUnavailable` | `1` / `""` | `maxUnavailable` wins when set |
| `topologySpreadConstraints` | spread by hostname | a missing `labelSelector` defaults to the pod labels |
| `priorityClassName` / `nodeSelector` / `tolerations` / `affinity` | empty | |
| `terminationGracePeriodSeconds` | `10` | |
| `revisionHistoryLimit` | `10` | |
| `annotations` / `podAnnotations` / `podLabels` | `{}` | |
| `networkPolicy.enabled` | `true` | ingress to metrics only, egress to DNS, the API server and OpenBao |
| `networkPolicy.flavor` | `kubernetes` | `cilium` renders a CiliumNetworkPolicy (`toEntities: [kube-apiserver]`, `toFQDNs`) |
| `networkPolicy.metrics.monitoringNamespace` / `.from` | `monitoring` / `[]` | empty `from` allows the release and monitoring namespaces |
| `networkPolicy.dns.to` | kube-dns in kube-system | |
| `networkPolicy.dns.nodeLocalDNS.enabled` / `.address` | `false` / `169.254.20.10` | also allow NodeLocal DNSCache |
| `networkPolicy.apiServer.cidrs` / `.ports` | `[]` / `[443, 6443]` | empty CIDRs allow any destination on those ports |
| `networkPolicy.openbao.to` / `.fqdns` / `.ports` | `[]` / `[]` / `[8200]` | empty peers (`fqdns` with `cilium`) allow any destination on those ports |
| `networkPolicy.extraEgress` | `[]` | in the syntax of the selected flavor |

## Development

The CRD templates and the manager rules are generated. Do not edit them by hand:

```sh
hack/chart/sync-crds.sh   # config/crd/bases -> templates/crds/ and templates/_crd-resources.tpl
hack/chart/sync-rbac.sh   # config/rbac/role.yaml + hack/chart/rbac-required.yaml -> templates/rbac/_manager-rules.tpl
helm lint charts/openbao-config-operator
helm unittest charts/openbao-config-operator
```

Both scripts need mikefarah `yq` v4; `sync-rbac.sh` also needs `jq`.
