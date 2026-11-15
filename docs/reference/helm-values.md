# Helm values

Every value of the `openbao-config-operator` chart, with its type, default and meaning.

!!! note "Source of truth"
    This page is derived from `charts/openbao-config-operator/values.yaml` (defaults and `# --`
    comments) and `charts/openbao-config-operator/values.schema.json` (types and constraints).
    Those two files are canonical; when they disagree with this page, they win. The chart's
    `README.md` has a shorter table in the same format.

The schema sets `additionalProperties: false` on the top level and on most sections, so Helm
refuses a misspelt or unknown key at install time. Besides the schema, the templates refuse these
combinations:

| combination | error |
|-------------|-------|
| `replicas` greater than 1 with `leaderElection.enabled: false` | `replicas > 1 requires leaderElection.enabled=true` |
| `injectedIdentity.projectedToken.path` under `/var/run/secrets/kubernetes.io/serviceaccount/` | the automounted token directory is refused |
| `crds.install: false` with `crds.keep: false` | an upgrade would delete the CRDs of an earlier release and every custom resource with them |
| `metrics.certSecret` set without `serviceMonitor.tlsConfig.ca` (or `caFile`) and `serverName`, or with `insecureSkipVerify` | the ServiceMonitor cannot verify the certificate |

## Names, CRDs and image

| key | type | default | description |
|-----|------|---------|-------------|
| `nameOverride` | string | `""` | override the chart name used in resource names |
| `fullnameOverride` | string | `""` | override the full resource name prefix |
| `crds.install` | boolean | `true` | install and upgrade the CRDs with the release |
| `crds.keep` | boolean | `true` | annotate the CRDs with `helm.sh/resource-policy: keep`, so uninstalling the release does not delete every custom resource |
| `crds.annotations` | map of strings, without `controller-gen.kubebuilder.io/version` | `{}` | extra annotations on every CRD, for example `argocd.argoproj.io/sync-options: ServerSideApply=true`. With `crds.keep: true`, a `helm.sh/resource-policy` here is replaced by `keep`; with `crds.keep: false` it is used as given |
| `crds.labels` | map of strings, without the chart's own label keys | `{}` | extra labels on every CRD. `helm.sh/chart`, `app.kubernetes.io/name`, `app.kubernetes.io/instance`, `app.kubernetes.io/version` and `app.kubernetes.io/managed-by` are refused |
| `pluginKinds.enabled` | boolean | `false` | install the CRDs of kinds that need an external OpenBao plugin (cloud auth and secrets, GitHub, Okta, LDAP, Kerberos, RADIUS, Consul, Nomad, Quay, Terraform Cloud, MongoDB Atlas) and pass `--enable-plugin-kinds` |
| `image.repository` | string, non-empty | `ghcr.io/alexandrust88/openbao-config-operator` | manager image repository |
| `image.tag` | string | `""` | image tag; empty uses the chart `appVersion` |
| `image.digest` | string, `sha256:` and 64 hex characters, or empty | `""` | when set, the image is pulled by digest. The released chart sets it |
| `image.pullPolicy` | `Always`, `IfNotPresent` or `Never` | `IfNotPresent` | |
| `imagePullSecrets` | list of objects | `[]` | image pull secrets, as a list of `{name: ...}` |

## Manager

| key | type | default | description |
|-----|------|---------|-------------|
| `replicas` | integer, at least 0 | `2` | manager replicas; more than one requires `leaderElection.enabled` |
| `leaderElection.enabled` | boolean | `true` | passes `--leader-elect`; disable only with `replicas: 1` |
| `watchNamespaces` | list of strings | `[]` | namespaces the manager watches (`--watch-namespaces`); empty means cluster-wide. When set, access to Secrets, ConfigMaps, ServiceAccount tokens, Events and every namespaced managed kind is granted through a Role in each listed namespace instead of a ClusterRole |
| `apiServerAudiences` | list of strings | `[]` | passes `--apiserver-audiences`: audiences the Kubernetes API server accepts besides the built-in ones (`https://kubernetes.default.svc`, `https://kubernetes.default.svc.cluster.local`, `kubernetes.default.svc`, `rke2`, `k3s`) and the discovered service account issuer. A tenant `OpenBaoConnection` is never given a token for them |
| `health.port` | integer, 1 to 65535 | `8081` | port of `/healthz` and `/readyz` |
| `pprof.enabled` | boolean | `false` | serve Go pprof profiles on `127.0.0.1` inside the pod (no Service); reach them with `kubectl port-forward` |
| `pprof.port` | integer, 1 to 65535 | `8082` | pprof port |
| `extraArgs` | list of strings | `[]` | extra manager arguments. Flags with a dedicated value (`--leader-elect`, `--metrics-secure`, `--metrics-bind-address`, `--pprof-bind-address`, `--health-probe-bind-address`) are refused |
| `extraEnv` | list of objects | `[]` | extra manager environment variables |
| `extraVolumes` | list of objects | `[]` | extra volumes, for example a CA bundle ConfigMap trusted through `SSL_CERT_DIR` in `extraEnv` |
| `extraVolumeMounts` | list of objects | `[]` | extra volume mounts of the manager container |

Manager flags without a chart value, such as `--drift-interval-critical`,
`--drift-interval-standard`, `--max-concurrent-reconciles`,
`--max-concurrent-reconciles-high-volume`, `--write-budget-per-minute`, `--cluster-id`,
`--hash-key-secret` and the `--zap-*` logging flags, go through `extraArgs`.

## Injected identity

| key | type | default | description |
|-----|------|---------|-------------|
| `injectedIdentity.projectedToken.enabled` | boolean | `true` | mount a projected ServiceAccount token with its own audience for `ClusterOpenBaoConnection` `injectedIdentity` logins. The operator never sends the automounted token, so a connection with `injectedIdentity` needs this or another projected token |
| `injectedIdentity.projectedToken.audience` | string, non-empty | `openbao-config-operator` | token audience; the OpenBao kubernetes or jwt role must accept it |
| `injectedIdentity.projectedToken.expirationSeconds` | integer, at least 600 | `3600` | token lifetime in seconds; the kubelet rotates the file before it expires |
| `injectedIdentity.projectedToken.path` | string, absolute path of at least two segments | `/var/run/secrets/openbao/token` | token file in the manager container; its directory must not hold other mounts. It is also the CRD's default `tokenPath`; if you change it, set the connection's `tokenPath` to the same file |

## ServiceAccount and RBAC

| key | type | default | description |
|-----|------|---------|-------------|
| `serviceAccount.create` | boolean | `true` | create the ServiceAccount |
| `serviceAccount.name` | string | `""` | ServiceAccount name; defaults to the full name; required when `create` is `false` |
| `serviceAccount.annotations` | map of strings | `{}` | |
| `rbac.create` | boolean | `true` | create the manager ClusterRole, Roles and bindings |
| `rbac.userRoles` | boolean | `true` | create the user-facing `<release>-tenant-editor` and `<release>-viewer` ClusterRoles |
| `rbac.aggregate.admin` | boolean | `true` | aggregate the tenant-editor role into the built-in `admin` role |
| `rbac.aggregate.edit` | boolean | `true` | aggregate the tenant-editor role into the built-in `edit` role |
| `rbac.aggregate.view` | boolean | `true` | aggregate the viewer role into the built-in `view` role |

## Metrics and monitoring

| key | type | default | description |
|-----|------|---------|-------------|
| `metrics.enabled` | boolean | `true` | serve metrics over HTTPS, protected by Kubernetes authentication and authorization, and create the metrics Service |
| `metrics.port` | integer, 1 to 65535 | `8443` | metrics port |
| `metrics.certSecret` | string | `""` | Secret (`tls.crt`, `tls.key`) with the metrics serving certificate; empty uses a self-signed certificate |
| `metrics.service.type` | `ClusterIP` | `ClusterIP` | only `ClusterIP`: the metrics endpoint is for in-cluster scrapers |
| `metrics.service.annotations` | map of strings | `{}` | |
| `serviceMonitor.enabled` | boolean | `false` | create a prometheus-operator `ServiceMonitor` |
| `serviceMonitor.labels` | map of strings | `{}` | labels for your Prometheus selector |
| `serviceMonitor.interval` | string | `30s` | scrape interval |
| `serviceMonitor.scrapeTimeout` | string | `10s` | scrape timeout |
| `serviceMonitor.tlsConfig` | object | `{insecureSkipVerify: true}` | TLS config of the scrape endpoint. `insecureSkipVerify` fits only the default self-signed certificate; with `metrics.certSecret` the chart requires `ca` (or `caFile`) and `serverName` and refuses `insecureSkipVerify` |
| `serviceMonitor.relabelings` | list of objects | `[]` | |
| `serviceMonitor.metricRelabelings` | list of objects | `[]` | |
| `prometheusRule.enabled` | boolean | `false` | create a prometheus-operator `PrometheusRule` with the alerts in [runbooks](../operate/runbooks.md) |
| `prometheusRule.labels` | map of strings | `{}` | labels of the `PrometheusRule` object |
| `prometheusRule.selector` | string | `""` | series selector of the operator's metrics; empty means `job="<fullname>-metrics"` in the release namespace |
| `prometheusRule.alertLabels` | map of strings | `{}` | labels added to every alert, for example for routing |
| `prometheusRule.disabledAlerts` | list of strings | `[]` | alert names to leave out |
| `prometheusRule.reconcileErrorRatio` | number, at most 1 | `0.1` | ratio of failed reconciles per controller above which `OpenBaoConfigOperatorReconcileErrors` fires |
| `prometheusRule.openbaoErrorRatio` | number, at most 1 | `0.05` | ratio of OpenBao 5xx and transport errors per connection above which `OpenBaoConfigOperatorOpenBaoServerErrors` fires |
| `prometheusRule.steadyStateWritesFor` | string, a number followed by `s`, `m` or `h` | `30m` | how long writes must continue before `OpenBaoConfigOperatorSteadyStateWrites` fires |
| `prometheusRule.runbookBaseURL` | string, `http://` or `https://` URL | the project's runbooks page on GitHub | base of each alert's `runbook_url` |

[Observability](../operate/observability.md) explains the metrics and the alerts.

## Pod settings

| key | type | default | description |
|-----|------|---------|-------------|
| `resources` | object | requests `cpu: 50m`, `memory: 128Mi`; limit `memory: 512Mi` | manager container resources |
| `podSecurityContext` | object | `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`, `seccompProfile.type: RuntimeDefault` | pod security context |
| `securityContext` | object | `allowPrivilegeEscalation: false`, `privileged: false`, `readOnlyRootFilesystem: true`, `runAsNonRoot: true`, `capabilities.drop: [ALL]`, `seccompProfile.type: RuntimeDefault` | container security context |
| `livenessProbe` | object | `initialDelaySeconds: 15`, `periodSeconds: 20` | merged into the `httpGet` liveness probe |
| `readinessProbe` | object | `initialDelaySeconds: 5`, `periodSeconds: 10` | merged into the `httpGet` readiness probe |
| `podDisruptionBudget.enabled` | boolean | `true` | create a PodDisruptionBudget when `replicas` is greater than 1 |
| `podDisruptionBudget.minAvailable` | integer or string | `1` | |
| `podDisruptionBudget.maxUnavailable` | integer or string | `""` | used instead of `minAvailable` when set |
| `topologySpreadConstraints` | list of objects | `maxSkew: 1`, `topologyKey: kubernetes.io/hostname`, `whenUnsatisfiable: ScheduleAnyway` | a missing `labelSelector` defaults to the pod selector labels |
| `priorityClassName` | string | `""` | |
| `nodeSelector` | map of strings | `{}` | |
| `tolerations` | list of objects | `[]` | |
| `affinity` | object | `{}` | |
| `terminationGracePeriodSeconds` | integer, at least 0 | `10` | |
| `revisionHistoryLimit` | integer, at least 0 | `10` | |
| `podAnnotations` | map of strings | `{}` | |
| `podLabels` | map of strings | `{}` | |
| `annotations` | map of strings | `{}` | annotations of the Deployment |

## Network policy

| key | type | default | description |
|-----|------|---------|-------------|
| `networkPolicy.enabled` | boolean | `true` | restrict ingress to the metrics port and egress to DNS, the API server and OpenBao |
| `networkPolicy.flavor` | `kubernetes` or `cilium` | `kubernetes` | `kubernetes` renders a NetworkPolicy; `cilium` renders a CiliumNetworkPolicy that reaches the API server through the `kube-apiserver` entity, which an `ipBlock` does not match on Cilium. `metrics.from`, `dns.to`, `apiServer.cidrs` and `openbao.to` apply to the `kubernetes` flavour only |
| `networkPolicy.metrics.monitoringNamespace` | string, a namespace name or empty | `monitoring` | namespace of the metrics scraper; with an empty `from`, the release namespace and this namespace may scrape |
| `networkPolicy.metrics.from` | list of objects | `[]` | NetworkPolicy peers allowed to scrape metrics, replacing the release and monitoring namespaces |
| `networkPolicy.dns.to` | list of objects | kube-dns pods in `kube-system` | DNS peers |
| `networkPolicy.dns.nodeLocalDNS.enabled` | boolean | `false` | also allow DNS to NodeLocal DNSCache |
| `networkPolicy.dns.nodeLocalDNS.address` | string, IPv4 | `169.254.20.10` | NodeLocal DNSCache address |
| `networkPolicy.apiServer.cidrs` | list | `[]` | API server CIDRs; empty allows any destination on the API server ports |
| `networkPolicy.apiServer.ports` | list | `[443, 6443]` | API server ports |
| `networkPolicy.openbao.to` | list of objects | `[]` | OpenBao peers; empty allows any destination on the OpenBao ports |
| `networkPolicy.openbao.fqdns` | list | `[]` | OpenBao host names for the `cilium` flavour (`toFQDNs`, needs the Cilium DNS proxy); empty allows any destination on the OpenBao ports |
| `networkPolicy.openbao.ports` | list | `[8200]` | OpenBao ports |
| `networkPolicy.extraEgress` | list of objects | `[]` | extra egress rules, in the syntax of the selected flavour |

The [hardening guide](../security/hardening.md#r-network-01-enable-the-networkpolicy) explains why
empty `apiServer.cidrs` and `openbao.to` leave egress open by destination.
