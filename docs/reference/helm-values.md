---
description: "Configure the OpenBao Config Operator Helm chart, including deployment, RBAC, networking, metrics and resource settings."
---

# Helm values

Every value of the `openbao-config-operator` chart, with its type, default, constraints and meaning.

!!! note "Source of truth"
    Defaults and comments come from
    [`charts/openbao-config-operator/values.yaml`](../../charts/openbao-config-operator/values.yaml);
    types and constraints from
    [`charts/openbao-config-operator/values.schema.json`](../../charts/openbao-config-operator/values.schema.json).
    Those files win when they disagree with this page.

The schema sets `additionalProperties: false` on the top level and on most sections, so Helm
refuses a misspelt or unknown key at install time. Besides the schema, the templates refuse these
combinations:

| combination | error |
|-------------|-------|
| `replicas` greater than 1 with `leaderElection.enabled: false` | `replicas > 1 requires leaderElection.enabled=true` |
| `injectedIdentity.projectedToken.enabled` with `path` directly in `/var/run/secrets/kubernetes.io/serviceaccount/` | the automounted token directory is refused |
| `crds.install: false` with `crds.keep: false` | an upgrade would delete the CRDs of an earlier release and every custom resource with them |
| `metrics.enabled` and `metrics.certSecret` with `serviceMonitor.enabled`, without `serviceMonitor.tlsConfig.ca` (or `caFile`) and `serverName`, or with `insecureSkipVerify: true` | the ServiceMonitor cannot verify the certificate |

Helm merges your `serviceMonitor.tlsConfig` with the default `insecureSkipVerify: true`, so when you set `ca`
also set `insecureSkipVerify: false`.

String values that land in a manifest (names, image tag, audiences, CIDRs, durations, the token path) must match a pattern without whitespace, so a value cannot add YAML keys to the rendered objects.

Manager flags without a chart value, such as `--drift-interval-critical`,
`--drift-interval-standard`, `--max-concurrent-reconciles`,
`--max-concurrent-reconciles-high-volume`, `--write-budget-per-minute`, `--cluster-id` and the
`--zap-*` logging flags, go through `extraArgs`. `--hash-key-secret` cannot be changed through the
chart: the namespace-scoped Role pins the Secret name.

[Observability](../operate/observability.md) explains the metrics and the alerts. The
[hardening guide](../security/hardening.md#r-network-01-enable-the-networkpolicy) explains why
empty `networkPolicy.apiServer.cidrs` and `networkPolicy.openbao.to` leave egress open by destination.

## Values

<div class="helm-values" markdown="1">

| Key | Type | Default | Constraints | Description |
| --- | --- | --- | --- | --- |
| `nameOverride` | string | `""` |  | Override the chart name used in resource names. |
| `fullnameOverride` | string | `""` |  | Override the full resource name prefix. |
| `crds` | object | see below |  |  |
| `crds.install` | bool | `true` |  | Install and upgrade the CRDs with the release. |
| `crds.keep` | bool | `true` |  | Annotate the CRDs with helm.sh/resource-policy=keep so uninstalling the release does not delete every custom resource. |
| `crds.annotations` | object | `{}` |  | Extra annotations on every CRD, for example argocd.argoproj.io/sync-options: ServerSideApply=true. With keep true, helm.sh/resource-policy here is replaced by keep. controller-gen.kubebuilder.io/version is refused. |
| `crds.labels` | object | `{}` |  | Extra labels on every CRD. The chart's own label keys (helm.sh/chart, app.kubernetes.io/name, instance, version and managed-by) are refused. |
| `pluginKinds` | object | see below |  |  |
| `pluginKinds.enabled` | bool | `false` |  | Install the CRDs of kinds that need an external OpenBao plugin (cloud auth and secrets, GitHub, Okta, LDAP, Kerberos, RADIUS, Consul, Nomad, Quay, Terraform Cloud, MongoDB Atlas) and pass --enable-plugin-kinds. |
| `image` | object | see below |  |  |
| `image.repository` | string | `ghcr.io/alexandrust88/openbao-config-operator` | non-empty | Manager image repository. |
| `image.tag` | string | `""` | pattern `^([\w][\w.-]{0,127})?$` | Image tag. Defaults to the chart appVersion. |
| `image.digest` | string | `""` | pattern `^(sha256:[a-f0-9]{64})?$` | Image digest (sha256:...). When set, the image is pulled by digest. The released chart sets it. |
| `image.pullPolicy` | string | `IfNotPresent` | one of `Always`, `IfNotPresent`, `Never` | Image pull policy. |
| `imagePullSecrets` | list | `[]` |  | Image pull secrets, as a list of {name: ...}. |
| `replicas` | int | `2` | at least 0 | Replicas. More than one requires leaderElection.enabled. |
| `leaderElection` | object | see below |  |  |
| `leaderElection.enabled` | bool | `true` |  | Run leader election (--leader-elect). Disable only with replicas: 1. |
| `watchNamespaces` | list | `[]` | each item: at most 63 characters; each item: pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` | Namespaces the manager watches. Empty means cluster-wide. When set, access to Secrets, ConfigMaps, ServiceAccount tokens, Events and every namespaced managed kind is granted through a Role in each listed namespace instead of a ClusterRole. The release namespace, unless listed, gets only the hash-key Secret. |
| `apiServerAudiences` | list | `[]` | each item: pattern `^[^,\s]+$` | Audiences the Kubernetes API server accepts besides the built-in ones (https://kubernetes.default.svc, https://kubernetes.default.svc.cluster.local, kubernetes.default.svc, rke2, k3s) and the service account issuer the operator discovers. A tenant BaoConnection is never given a token for them. |
| `injectedIdentity` | object | see below |  |  |
| `injectedIdentity.projectedToken` | object | see below |  |  |
| `injectedIdentity.projectedToken.enabled` | bool | `true` |  | Mount a projected ServiceAccount token with its own audience for ClusterBaoConnection injectedIdentity logins. The CRD's default tokenPath is the default path; the operator never sends the automounted token, so a connection with injectedIdentity needs this or another projected token. |
| `injectedIdentity.projectedToken.audience` | string | `openbao-config-operator` | non-empty | Token audience. The OpenBao kubernetes or jwt role must accept it. |
| `injectedIdentity.projectedToken.expirationSeconds` | int | `3600` | at least 600 | Token lifetime in seconds. The kubelet rotates the file before it expires. |
| `injectedIdentity.projectedToken.path` | string | `/var/run/secrets/openbao/token` | at most 512 characters; pattern `^(/[^/\s]+){2,}$` | Token file in the manager container. Its directory must not hold other mounts. It is also the CRD's default tokenPath: if you change it, set the connection's tokenPath to the same file. |
| `serviceAccount` | object | see below |  |  |
| `serviceAccount.create` | bool | `true` |  | Create the ServiceAccount. |
| `serviceAccount.name` | string | `""` | at most 253 characters; pattern `^([a-z0-9]([-.a-z0-9]*[a-z0-9])?)?$`; not `default` | ServiceAccount name. Defaults to the full name; required when create is false. The name default is refused, so the manager rights never land on the namespace's default ServiceAccount. |
| `serviceAccount.annotations` | object | `{}` |  | Annotations of the ServiceAccount. |
| `rbac` | object | see below |  |  |
| `rbac.create` | bool | `true` |  | Create the manager ClusterRole, Roles and bindings. |
| `rbac.userRoles` | bool | `true` |  | Create the user-facing `<release>-tenant-editor` and `<release>-viewer` ClusterRoles. The viewer never includes BaoServer or ClusterBaoConnection. |
| `rbac.tenantEditor` | object | see below |  |  |
| `rbac.tenantEditor.excludeKinds` | list | `[]` | each item: pattern `^[a-z0-9]+$` | Plurals to leave out of the tenant-editor role, for example baoendpoints to keep the raw-path kind for platform admins (ADR 0006). |
| `rbac.aggregate` | object | see below |  |  |
| `rbac.aggregate.admin` | bool | `true` |  | Aggregate the tenant-editor role into the built-in admin role. |
| `rbac.aggregate.edit` | bool | `true` |  | Aggregate the tenant-editor role into the built-in edit role. |
| `rbac.aggregate.view` | bool | `true` |  | Aggregate the viewer role into the built-in view role. |
| `metrics` | object | see below |  |  |
| `metrics.enabled` | bool | `true` |  | Serve metrics over HTTPS on metrics.port, protected by Kubernetes authentication and authorization. |
| `metrics.port` | int | `8443` | 1 to 65535 | Metrics port. |
| `metrics.certSecret` | string | `""` | at most 253 characters; pattern `^([a-z0-9]([-.a-z0-9]*[a-z0-9])?)?$` | Secret (tls.crt, tls.key) with the metrics serving certificate. Empty uses a self-signed certificate. |
| `metrics.service` | object | see below |  |  |
| `metrics.service.type` | string | `ClusterIP` | only `ClusterIP` | Only ClusterIP: the metrics endpoint is for in-cluster scrapers. |
| `metrics.service.annotations` | object | `{}` |  | Annotations of the metrics Service. |
| `serviceMonitor` | object | see below |  |  |
| `serviceMonitor.enabled` | bool | `false` |  | Create a prometheus-operator ServiceMonitor. Needs metrics.enabled. |
| `serviceMonitor.labels` | object | `{}` |  | Labels of the ServiceMonitor, for your Prometheus selector. |
| `serviceMonitor.interval` | string | `30s` | pattern `^([0-9]+(ms|s|m|h|d|w|y))+$` | Scrape interval. |
| `serviceMonitor.scrapeTimeout` | string | `10s` | pattern `^([0-9]+(ms|s|m|h|d|w|y))+$` | Scrape timeout. |
| `serviceMonitor.tlsConfig` | object | see below |  | TLS configuration for scraping metrics. The default skips verification of the generated self-signed certificate. With metrics.certSecret, provide ca (or caFile) and serverName; insecureSkipVerify must be false. |
| `serviceMonitor.tlsConfig.insecureSkipVerify` | bool | `true` |  | Fits only the self-signed certificate. Values are merged with this default, so set it to false explicitly when you provide ca. |
| `serviceMonitor.relabelings` | list | `[]` |  | Relabelings of the scrape endpoint. |
| `serviceMonitor.metricRelabelings` | list | `[]` |  | Metric relabelings of the scrape endpoint. |
| `prometheusRule` | object | see below |  |  |
| `prometheusRule.enabled` | bool | `false` |  | Create a prometheus-operator PrometheusRule with the alerts described at runbookBaseURL. Needs metrics.enabled. |
| `prometheusRule.labels` | object | `{}` |  | Labels of the PrometheusRule object. |
| `prometheusRule.selector` | string | `""` |  | Series selector of the operator's metrics. Empty means `job="<fullname>-metrics"` in the release namespace. |
| `prometheusRule.alertLabels` | object | `{}` |  | Labels added to every alert, for example for routing. |
| `prometheusRule.disabledAlerts` | list | `[]` | each item: one of `OpenBaoConfigOperatorDown`, `OpenBaoConfigOperatorReconcileErrors`, `OpenBaoConfigOperatorSteadyStateWrites`, `OpenBaoConfigOperatorOpenBaoServerErrors`, `OpenBaoConfigOperatorOpenBaoDenied` | Alert names to leave out. |
| `prometheusRule.reconcileErrorRatio` | number | `0.1` | greater than 0, at most 1 | Ratio of failed reconciles per controller above which OpenBaoConfigOperatorReconcileErrors fires. |
| `prometheusRule.openbaoErrorRatio` | number | `0.05` | greater than 0, at most 1 | Ratio of OpenBao 5xx and transport errors per connection above which OpenBaoConfigOperatorOpenBaoServerErrors fires. |
| `prometheusRule.steadyStateWritesFor` | string | `30m` | pattern `^[0-9]+(s|m|h)$` | How long writes must continue before OpenBaoConfigOperatorSteadyStateWrites fires. |
| `prometheusRule.runbookBaseURL` | string | `https://docs.obco.dev/operate/runbooks/` | pattern `^https?://` | Base URL of each alert's runbook_url; the lower-case alert name is appended as the anchor. |
| `health` | object | see below |  |  |
| `health.port` | int | `8081` | 1 to 65535 | Port of the /healthz and /readyz endpoints that the liveness and readiness probes call. |
| `pprof` | object | see below |  |  |
| `pprof.enabled` | bool | `false` |  | Serve Go pprof profiles on 127.0.0.1 inside the pod (no Service); reach them with kubectl port-forward. A heap profile holds OpenBao tokens, so whoever may port-forward to the pod can then read them. |
| `pprof.port` | int | `8082` | 1 to 65535 | pprof port. |
| `extraArgs` | list | `[]` |  | Extra manager arguments. Flags the chart sets from a value (`--leader-elect`, `--metrics-secure`, `--metrics-bind-address`, `--metrics-cert-path`, `--pprof-bind-address`, `--health-probe-bind-address`, `--watch-namespaces`, `--apiserver-audiences`, `--enable-plugin-kinds`) and `--hash-key-secret`, whose name the namespace-scoped Role pins, are refused here. |
| `extraEnv` | list | `[]` |  | Extra manager environment variables. POD_NAMESPACE is refused: the chart sets it. |
| `extraVolumes` | list | `[]` |  | Extra volumes, for example a CA bundle ConfigMap trusted through SSL_CERT_DIR in extraEnv. hostPath is refused. |
| `extraVolumeMounts` | list | `[]` |  | Extra volume mounts of the manager container. |
| `resources` | object | see below |  | Manager container resources. |
| `resources.requests` | object | see below |  |  |
| `resources.requests.cpu` | string | `50m` |  | CPU request. |
| `resources.requests.memory` | string | `128Mi` |  | Memory request. |
| `resources.limits` | object | see below |  |  |
| `resources.limits.memory` | string | `512Mi` |  | Memory limit. |
| `podSecurityContext` | object | see below |  | Pod security context. |
| `podSecurityContext.runAsNonRoot` | bool | `true` | must be `true` | Run as a non-root user. |
| `podSecurityContext.runAsUser` | int | `65532` |  | User ID. Set it to null where the platform assigns UIDs (OpenShift). |
| `podSecurityContext.runAsGroup` | int | `65532` |  | Group ID. Set it to null where the platform assigns UIDs (OpenShift). |
| `podSecurityContext.seccompProfile` | object | see below |  |  |
| `podSecurityContext.seccompProfile.type` | string | `RuntimeDefault` |  | Seccomp profile of the pod. |
| `securityContext` | object | see below |  | Manager container security context. allowPrivilegeEscalation, readOnlyRootFilesystem and runAsNonRoot are required; added capabilities are refused. |
| `securityContext.allowPrivilegeEscalation` | bool | `false` | must be `false` | Block privilege escalation. |
| `securityContext.privileged` | bool | `false` | must be `false` | Run unprivileged. |
| `securityContext.readOnlyRootFilesystem` | bool | `true` | must be `true` | Mount the root file system read-only. |
| `securityContext.runAsNonRoot` | bool | `true` | must be `true` | Run as a non-root user. |
| `securityContext.capabilities` | object | see below |  |  |
| `securityContext.capabilities.drop` | list | `["ALL"]` |  | Capabilities to drop. |
| `securityContext.seccompProfile` | object | see below |  |  |
| `securityContext.seccompProfile.type` | string | `RuntimeDefault` |  | Seccomp profile of the container. |
| `livenessProbe` | object | see below |  | Merged into the httpGet liveness probe on /healthz. |
| `livenessProbe.initialDelaySeconds` | int | `15` |  | Seconds before the first liveness check. |
| `livenessProbe.periodSeconds` | int | `20` |  | Seconds between liveness checks. |
| `readinessProbe` | object | see below |  | Merged into the httpGet readiness probe on /readyz. |
| `readinessProbe.initialDelaySeconds` | int | `5` |  | Seconds before the first readiness check. |
| `readinessProbe.periodSeconds` | int | `10` |  | Seconds between readiness checks. |
| `podDisruptionBudget` | object | see below |  |  |
| `podDisruptionBudget.enabled` | bool | `true` |  | Create a PodDisruptionBudget when replicas > 1. |
| `podDisruptionBudget.minAvailable` | int or string | `1` | pattern `^([0-9]+%)?$` | Minimum available pods, as a number or a percentage. |
| `podDisruptionBudget.maxUnavailable` | int or string | `""` | pattern `^([0-9]+%)?$` | Used instead of minAvailable when set. |
| `topologySpreadConstraints` | list | `[{"maxSkew":1,"topologyKey":"kubernetes.io/hostname","whenUnsatisfiable":"ScheduleAnyway"}]` |  | Topology spread constraints. A missing labelSelector defaults to the pod selector labels. |
| `priorityClassName` | string | `""` | at most 253 characters; pattern `^([a-z0-9]([-.a-z0-9]*[a-z0-9])?)?$` | Pod priority class. |
| `nodeSelector` | object | `{}` |  | Pod node selector. |
| `tolerations` | list | `[]` |  | Pod tolerations. |
| `affinity` | object | `{}` |  | Pod affinity. |
| `terminationGracePeriodSeconds` | int | `10` | at least 0 | Seconds the pod gets to shut down. |
| `revisionHistoryLimit` | int | `10` | at least 0 | Old ReplicaSets the Deployment keeps. |
| `podAnnotations` | object | `{}` |  | Annotations of the pod. |
| `podLabels` | object | `{}` |  | Labels of the pod. |
| `annotations` | object | `{}` |  | Annotations of the Deployment. |
| `networkPolicy` | object | see below |  |  |
| `networkPolicy.enabled` | bool | `true` |  | Restrict ingress to the metrics port and egress to DNS, the API server and OpenBao. |
| `networkPolicy.flavor` | string | `kubernetes` | one of `kubernetes`, `cilium` | kubernetes renders a NetworkPolicy; cilium renders a CiliumNetworkPolicy that reaches the API server through the kube-apiserver entity, which an ipBlock does not match on Cilium. metrics.from, dns.to, apiServer.cidrs and openbao.to apply to the kubernetes flavour only. |
| `networkPolicy.metrics` | object | see below |  |  |
| `networkPolicy.metrics.monitoringNamespace` | string | `monitoring` | at most 63 characters; pattern `^([a-z0-9]([-a-z0-9]*[a-z0-9])?)?$` | Namespace of the metrics scraper. With an empty from, the release namespace and this namespace may scrape. |
| `networkPolicy.metrics.from` | list | `[]` |  | NetworkPolicy peers allowed to scrape metrics, replacing the release and monitoring namespaces. |
| `networkPolicy.dns` | object | see below |  |  |
| `networkPolicy.dns.to` | list | `[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"kube-system"}},"podSelector":{"matchLabels":{"k8s-app":"kube-dns"}}}]` |  | DNS peers. The default is the kube-dns pods in kube-system. |
| `networkPolicy.dns.nodeLocalDNS` | object | see below |  |  |
| `networkPolicy.dns.nodeLocalDNS.enabled` | bool | `false` |  | Also allow DNS to NodeLocal DNSCache. |
| `networkPolicy.dns.nodeLocalDNS.address` | string | `169.254.20.10` | format `ipv4` | NodeLocal DNSCache address. |
| `networkPolicy.apiServer` | object | see below |  |  |
| `networkPolicy.apiServer.cidrs` | list | `[]` | each item: pattern `^[0-9a-fA-F.:]+/[0-9]{1,3}$` | API server CIDRs. Empty allows any destination on the API server ports. |
| `networkPolicy.apiServer.ports` | list | `[443,6443]` | each item: 1 to 65535 | API server ports. |
| `networkPolicy.openbao` | object | see below |  |  |
| `networkPolicy.openbao.to` | list | `[]` |  | OpenBao peers. Empty allows any destination on the OpenBao ports. |
| `networkPolicy.openbao.fqdns` | list | `[]` | each item: non-empty | OpenBao host names for the cilium flavour (toFQDNs, needs the Cilium DNS proxy). Empty allows any destination on the OpenBao ports. |
| `networkPolicy.openbao.ports` | list | `[8200]` | each item: 1 to 65535 | OpenBao ports. |
| `networkPolicy.extraEgress` | list | `[]` |  | Extra egress rules, in the syntax of the selected flavour. |

</div>
