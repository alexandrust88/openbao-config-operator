# OpenBao Config Operator

Manage the configuration of an existing OpenBao server through Kubernetes custom resources and GitOps.
The operator reconciles policies, authentication methods, secrets engines and other configuration
with the desired state in Kubernetes. This chart installs the operator; provision, initialise and
unseal your OpenBao server separately.

[Documentation](https://docs.obco.dev/) ·
[Quickstart](https://docs.obco.dev/get-started/quickstart/) ·
[Supported resources](https://docs.obco.dev/kinds/) ·
[Security model](https://docs.obco.dev/security/)

## Install

You need Kubernetes 1.31 or later, Helm 3.8 or later, and permission to create CRDs and cluster
roles. The operator pods must be able to reach your OpenBao server.

Set `CHART_VERSION` to the chart version you want to install. See the
[releases](https://github.com/alexandrust88/openbao-config-operator/releases) for available versions
and upgrade notes. Follow the [release verification instructions](https://docs.obco.dev/get-started/install/#1-verify-the-release)
before installing.

```bash
helm install openbao-config-operator oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version "${CHART_VERSION:?Set CHART_VERSION to the chart version to install}" \
  --namespace openbao-config-operator --create-namespace
kubectl -n openbao-config-operator rollout status deployment/openbao-config-operator
```

Then [connect the operator to OpenBao](https://docs.obco.dev/get-started/connect/).
For a local evaluation, follow the [kind quickstart](https://docs.obco.dev/get-started/quickstart/).

## What it installs

- CRDs as templates, so `helm upgrade` upgrades them. They carry `helm.sh/resource-policy: keep` by default, so
  `helm uninstall` leaves every custom resource in place.
- The manager Deployment (2 replicas with leader election, restricted pod security), its ServiceAccount, a
  PodDisruptionBudget and an HTTPS metrics Service protected by Kubernetes authentication and authorisation.
- RBAC for the manager, and two user-facing ClusterRoles:
  - `<release>-tenant-editor`: every namespaced kind except `BaoConnection`. A connection decides which
    OpenBao identity is used, so only the platform team manages it. Aggregated into `admin` and `edit`.
  - `<release>-viewer`: read-only access to every kind except `BaoServer` and `ClusterBaoConnection`.
    Aggregated into `view`.
- A NetworkPolicy that limits the manager's ingress to the metrics port and its egress to DNS, the API server and
  OpenBao. Set `networkPolicy.apiServer.cidrs` and `networkPolicy.openbao.to` (with `flavor: cilium`,
  `networkPolicy.openbao.fqdns`) in production; empty lists allow any
  destination on those ports.

## CRDs managed outside the release

`crds.annotations` and `crds.labels` add metadata to every CRD, for example
`argocd.argoproj.io/sync-options: ServerSideApply=true`. `crds.keep=true` wins over a `helm.sh/resource-policy` in
`crds.annotations`.

To install the CRDs separately (`kubectl apply --server-side -f crds.yaml` from the GitHub release, a GitOps wave or
another release), install the chart with `crds.install=false` and apply the new `crds.yaml` before each upgrade.
`crds.yaml` has no plugin-kind CRDs.

If another tool already manages the CRDs, keep that ownership explicit with `crds.install=false`.
To transfer ownership to Helm, follow the
[CRD ownership procedure](https://docs.obco.dev/operate/install-options/#crds)
and stop the previous owner managing those CRDs before installing the chart.

## Watching a subset of namespaces

With `watchNamespaces` set, the manager gets `--watch-namespaces=<list>`, and access to Secrets, ConfigMaps,
ServiceAccount tokens, Events and every namespaced managed kind is granted by a Role in each listed namespace instead
of cluster-wide. List every
namespace that holds credentials or CA bundles referenced by a `ClusterBaoConnection`.

## Metrics

Metrics are served on port 8443 over HTTPS. A scraper needs a token allowed to `GET /metrics`: bind the
`<fullname>-metrics-reader` ClusterRole to its ServiceAccount. Without `metrics.certSecret` the manager uses a
self-signed certificate, so the ServiceMonitor skips verification by default.

## Injected identity

A `ClusterBaoConnection` with `auth.kubernetes.injectedIdentity` or `auth.jwt.injectedIdentity` logs in as the
manager's own ServiceAccount. The chart mounts a projected token for `injectedIdentity.projectedToken.audience`
(default `openbao-config-operator`) at `/var/run/secrets/openbao/token`, the CRD's default `tokenPath`.
Choose a dedicated audience that your Kubernetes API server does not accept. The operator refuses
the automounted token. If you
change `injectedIdentity.projectedToken.path`, set the connection's `tokenPath` to the same file.

## Values

The most used values are listed here. The [Helm values reference](https://docs.obco.dev/reference/helm-values/)
lists every value with its type and constraints.

| key | default | description |
|-----|---------|-------------|
| `crds.install` | `true` | install and upgrade the CRDs with the release |
| `crds.keep` | `true` | annotate CRDs with `helm.sh/resource-policy: keep` |
| `crds.annotations` | `{}` | extra annotations on every CRD; with `crds.keep: true` a `helm.sh/resource-policy` here is replaced by `keep` |
| `crds.labels` | `{}` | extra labels on every CRD; the chart's own label keys are refused |
| `image.repository` | `ghcr.io/alexandrust88/openbao-config-operator` | manager image |
| `image.tag` | `""` | defaults to the chart `appVersion` |
| `image.digest` | `""` | `sha256:...`; pulls by digest when set |
| `image.pullPolicy` | `IfNotPresent` | when Kubernetes pulls the manager image |
| `imagePullSecrets` | `[]` | Secrets used to authenticate to the image registry |
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
| `rbac.tenantEditor.excludeKinds` | `[]` | plurals to leave out of the tenant-editor role, for example `baoendpoints` |
| `rbac.aggregate.admin` / `.edit` / `.view` | `true` | aggregation labels on the user roles |
| `metrics.enabled` | `true` | HTTPS metrics endpoint and Service |
| `metrics.port` | `8443` | HTTPS metrics port |
| `metrics.certSecret` | `""` | Secret with `tls.crt` and `tls.key` for the metrics endpoint |
| `metrics.service.type` / `.annotations` | `ClusterIP` / `{}` | only `ClusterIP` |
| `serviceMonitor.enabled` | `false` | prometheus-operator ServiceMonitor |
| `serviceMonitor.labels` / `.interval` / `.scrapeTimeout` | `{}` / `30s` / `10s` | monitor labels, scrape interval and timeout |
| `serviceMonitor.tlsConfig` | `{insecureSkipVerify: true}` | with `metrics.certSecret`, `ca` (or `caFile`) and `serverName` are required and `insecureSkipVerify` is refused |
| `serviceMonitor.relabelings` / `.metricRelabelings` | `[]` | Prometheus target and metric relabeling rules |
| `prometheusRule.enabled` | `false` | PrometheusRule with the alerts in [alert runbooks](https://docs.obco.dev/operate/runbooks/) |
| `prometheusRule.selector` | `""` | series selector; empty means `job="<fullname>-metrics"` in the release namespace |
| `prometheusRule.runbookBaseURL` | published alert runbooks | base URL used for alert runbook links |
| `prometheusRule.labels` / `.alertLabels` / `.disabledAlerts` | `{}` / `{}` / `[]` | rule object labels, labels on every alert, alerts to leave out |
| `prometheusRule.reconcileErrorRatio` / `.openbaoErrorRatio` / `.steadyStateWritesFor` | `0.1` / `0.05` / `30m` | alert thresholds |
| `health.port` | `8081` | `/healthz` and `/readyz` |
| `pprof.enabled` / `.port` | `false` / `8082` | Go pprof on `127.0.0.1` in the pod, no Service; use `kubectl port-forward` |
| `extraArgs` / `extraEnv` | `[]` | extra manager flags and environment; flags with a dedicated value are refused |
| `extraVolumes` / `extraVolumeMounts` | `[]` | extra volumes, for example a CA bundle trusted through `SSL_CERT_DIR` |
| `resources` | requests `50m`/`128Mi`, limit `512Mi` | manager CPU and memory requests and limits |
| `podSecurityContext` / `securityContext` | restricted | non-root 65532, read-only root, no capabilities, RuntimeDefault seccomp |
| `livenessProbe` / `readinessProbe` | delays and periods | merged into the httpGet probes |
| `podDisruptionBudget.enabled` | `true` | rendered only when `replicas > 1` |
| `podDisruptionBudget.minAvailable` / `.maxUnavailable` | `1` / `""` | `maxUnavailable` wins when set |
| `topologySpreadConstraints` | spread by hostname | a missing `labelSelector` defaults to the pod labels |
| `priorityClassName` / `nodeSelector` / `tolerations` / `affinity` | empty | pod priority and scheduling constraints |
| `terminationGracePeriodSeconds` | `10` | time allowed for graceful pod termination |
| `revisionHistoryLimit` | `10` | number of previous Deployment revisions retained |
| `annotations` / `podAnnotations` / `podLabels` | `{}` | Deployment annotations, pod annotations and pod labels |
| `networkPolicy.enabled` | `true` | ingress to metrics only, egress to DNS, the API server and OpenBao |
| `networkPolicy.flavor` | `kubernetes` | `cilium` renders a CiliumNetworkPolicy (`toEntities: [kube-apiserver]`, `toFQDNs`) |
| `networkPolicy.metrics.monitoringNamespace` / `.from` | `monitoring` / `[]` | empty `from` allows the release and monitoring namespaces |
| `networkPolicy.dns.to` | kube-dns in kube-system | DNS destination peers for the Kubernetes NetworkPolicy |
| `networkPolicy.dns.nodeLocalDNS.enabled` / `.address` | `false` / `169.254.20.10` | also allow NodeLocal DNSCache |
| `networkPolicy.apiServer.cidrs` / `.ports` | `[]` / `[443, 6443]` | empty CIDRs allow any destination on those ports |
| `networkPolicy.openbao.to` / `.fqdns` / `.ports` | `[]` / `[]` / `[8200]` | empty peers (`fqdns` with `cilium`) allow any destination on those ports |
| `networkPolicy.extraEgress` | `[]` | in the syntax of the selected flavor |

## Upgrade and uninstall

Read the [upgrade guide](https://docs.obco.dev/operate/upgrade/)
before changing chart versions. Helm upgrades the CRDs when `crds.install=true`.

By default, uninstalling the chart retains its CRDs and custom resources. Resources with
finalizers still need a running operator to complete deletion. Follow the
[uninstall procedure](https://docs.obco.dev/operate/uninstall/)
before removing the release or its CRDs.

## Support and contributions

Use the [troubleshooting guide](https://docs.obco.dev/operate/troubleshooting/)
to diagnose connection and reconciliation errors. Report bugs through
[GitHub issues](https://github.com/alexandrust88/openbao-config-operator/issues), without credentials or secret values.
See the [contributor guide](https://github.com/alexandrust88/openbao-config-operator/blob/main/CONTRIBUTING.md)
for development and chart maintenance instructions.
