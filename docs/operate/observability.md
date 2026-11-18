---
title: Observe OpenBao Config Operator
description: "Monitor OpenBao Config Operator with metrics, logs, alerts and reconciliation status."
---

# Observability

What the operator exposes for monitoring: Prometheus metrics, the chart's scrape and alert
options, Kubernetes events and conditions, and logs. Conditions and events are listed in
[status and events](../reference/status-and-events.md).

## Metrics endpoint

| property | value |
|----------|-------|
| port | `metrics.port`, default `8443`, container port `metrics` |
| protocol | HTTPS (`--metrics-secure=true`); HTTP/2 off unless `--enable-http2` |
| authentication | Kubernetes TokenReview and SubjectAccessReview: the scraper needs `get` on the non-resource URL `/metrics` |
| role to bind | ClusterRole `<fullname>-metrics-reader` |
| Service | `<fullname>-metrics`, type `ClusterIP`, port name `https` |
| certificate | self-signed by the manager, or the Secret in `metrics.certSecret` (`tls.crt`, `tls.key`) |
| NetworkPolicy | with `networkPolicy.enabled` (default), ingress to the metrics port from the release namespace and `networkPolicy.metrics.monitoringNamespace` (default `monitoring`), or the peers in `networkPolicy.metrics.from` (`metrics.from` applies to `networkPolicy.flavor: kubernetes` only) |

Only the leader replica runs the controllers. The operator series below (`obco_*`) therefore
come from the leader pod; the standby replica serves the Go and process metrics only. Use
`max by (...)` or `sum by (...)` without the `pod` label when you aggregate.

## Prometheus Operator integration

The chart renders a `ServiceMonitor` and a `PrometheusRule` for the
[Prometheus Operator](https://prometheus-operator.dev/) when you enable them. Both need
`metrics.enabled: true`.

| value | effect |
|-------|--------|
| `serviceMonitor.enabled` | render a `ServiceMonitor` that scrapes `/metrics` on port `https` with the scraper's ServiceAccount token |
| `serviceMonitor.interval`, `serviceMonitor.scrapeTimeout` | scrape timing |
| `serviceMonitor.tlsConfig` | the default `{insecureSkipVerify: true}` fits the self-signed certificate only; with `metrics.certSecret` the chart requires `ca` (or `caFile`), `serverName` and `insecureSkipVerify: false`, because Helm merges your `tlsConfig` with the default |
| `serviceMonitor.labels`, `.relabelings`, `.metricRelabelings` | labels for your Prometheus selector, relabelling |
| `prometheusRule.enabled` | render the alerts in [runbooks](runbooks.md) |
| `prometheusRule.labels` | labels for your Prometheus rule selector |
| `prometheusRule.selector` | series selector of the alerts; empty means `job="<fullname>-metrics", namespace="<release namespace>"` |
| `prometheusRule.reconcileErrorRatio`, `.openbaoErrorRatio`, `.steadyStateWritesFor` | thresholds of the built-in alerts |
| `prometheusRule.alertLabels`, `.disabledAlerts`, `.runbookBaseURL` | routing labels, alerts to leave out, base of each alert's `runbook_url` |

The [Helm values reference](../reference/helm-values.md) lists every value with its default.
`<fullname>` is the release name when it contains `openbao-config-operator` (as in the install
guide), otherwise `<release>-openbao-config-operator`, unless you set `fullnameOverride` or
`nameOverride`.

=== "Self-signed certificate"

    ```yaml
    serviceMonitor:
      enabled: true
      labels:
        release: prometheus
    prometheusRule:
      enabled: true
      labels:
        release: prometheus
    ```

=== "Certificate from a Secret"

    The Secret name and `serverName` are examples: `serverName` must match a name in the certificate.

    ```yaml
    metrics:
      certSecret: openbao-config-operator-metrics-tls
    serviceMonitor:
      enabled: true
      tlsConfig:
        ca:
          secret:
            name: openbao-config-operator-metrics-tls
            key: ca.crt
        serverName: openbao-config-operator-metrics.openbao-config-operator.svc
        insecureSkipVerify: false
    ```

Bind the scraper's ServiceAccount to the `<fullname>-metrics-reader` ClusterRole, or the scrape
answers 403:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: openbao-config-operator-metrics-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: openbao-config-operator-metrics-reader
subjects:
  - kind: ServiceAccount
    name: prometheus
    namespace: monitoring
```

Set the subject to the ServiceAccount your Prometheus runs as (`prometheus` above) and its
Kubernetes namespace (`monitoring` above).

## Operator metrics

| metric | type | labels | meaning |
|--------|------|--------|---------|
| `obco_requests_total` | counter | `connection`, `method`, `code` | HTTP requests sent to OpenBao, including retries |
| `obco_request_duration_seconds` | histogram (default buckets) | `connection`, `method` | duration of HTTP requests sent to OpenBao |
| `obco_logins_total` | counter | `connection`, `result` | logins to OpenBao; steady state adds none |
| `obco_token_renewals_total` | counter | `connection`, `result` | token renewals (`renew-self`) |
| `obco_writes_total` | counter | `kind` | writes the engine sent to OpenBao; zero at rest |
| `obco_drift_repairs_total` | counter | `kind` | writes that repaired an out-of-band change of a settled resource |
| `obco_conflicts_total` | counter | `kind` | passes refused because another resource or cluster owns the path |
| `obco_drift_lag_seconds` | gauge | `connection` | how far the last drift read of a connection ran behind its schedule |
| `obco_resource_ready` | gauge | `kind`, `namespace`, `name`, `reason` | 1 when a managed resource is `Ready`, else 0; `reason` is the reason of its `Ready` condition |
| `obco_write_budget_exhausted_total` | counter | `server` | writes held back because the server's write budget was spent |
| `obco_connection_ready` | gauge | `kind`, `namespace`, `name` | 1 when the connection is `Ready`, else 0 |

Label values:

| label | values |
|-------|--------|
| `connection` on `obco_requests_total`, `obco_request_duration_seconds`, `obco_logins_total`, `obco_token_renewals_total` | the connection's UID. The series of a connection are deleted when its client is dropped |
| `connection` on `obco_drift_lag_seconds` | `BaoConnection/<namespace>/<name>` or `ClusterBaoConnection/<name>` |
| `method` | `GET`, `PUT`, `POST`, `DELETE`, or `LIST` for a `GET` with `list=true` |
| `code` | the HTTP status code, or `error` for a transport failure (DNS, TCP, TLS) |
| `result` | `success`, `failure` |
| `kind` on engine metrics | the CRD kind, for example `ACLPolicy` |
| `kind` on `obco_connection_ready` | `BaoConnection` or `ClusterBaoConnection`; `namespace` is empty for the cluster kind |
| `server` | the `BaoServer` name |
| `reason` | a `Ready` reason from [status and events](../reference/status-and-events.md#reasons); a resource has one series, and a reason change replaces it |

Map a connection UID to its name:

```bash
kubectl get baoconnections,clusterbaoconnections -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,UID:.metadata.uid
```

The manager also serves the standard controller-runtime and Go metrics. The ones the chart's
alerts use are `controller_runtime_reconcile_total{controller,result}` and
`controller_runtime_reconcile_errors_total{controller}`, plus Prometheus's own `up` series for the
scrape target; `workqueue_depth{name}` shows the backlog
per controller. Controller names are
`<kind>.<group>` in lower case for managed kinds (for example `aclpolicy.sys.obco.dev`), and
`baoserver`, `baoconnection` and `clusterbaoconnection` for the core controllers.

## Example queries

Resources that are not `Ready`, by kind and reason:

```promql
sum by (kind, reason) (max without (pod, instance) (obco_resource_ready == 0))
```

Connections that are not `Ready`:

```promql
max without (pod, instance) (obco_connection_ready) == 0
```

Kinds the operator keeps writing (should be empty at rest):

```promql
topk(5, sum by (kind) (rate(obco_writes_total[30m])))
```

OpenBao error ratio per connection (5xx and transport errors):

```promql
sum by (connection) (rate(obco_requests_total{code=~"5..|error"}[5m]))
  / sum by (connection) (rate(obco_requests_total[5m]))
```

95th percentile OpenBao latency per connection and method:

```promql
histogram_quantile(0.95, sum by (connection, method, le) (rate(obco_request_duration_seconds_bucket[5m])))
```

Failed logins and renewals:

```promql
sum by (connection, result) (increase(obco_logins_total[1h]))
sum by (connection) (increase(obco_token_renewals_total{result="failure"}[1h]))
```

Drift repairs, ownership conflicts and held-back writes:

```promql
sum by (kind) (increase(obco_drift_repairs_total[1d]))
sum by (kind) (increase(obco_conflicts_total[1h]))
sum by (server) (increase(obco_write_budget_exhausted_total[1h]))
```

## Alerting rules

The chart's `PrometheusRule` (`prometheusRule.enabled: true`) holds five alerts, each with a
runbook: `OpenBaoConfigOperatorDown`, `OpenBaoConfigOperatorReconcileErrors`,
`OpenBaoConfigOperatorSteadyStateWrites`, `OpenBaoConfigOperatorOpenBaoServerErrors` and
`OpenBaoConfigOperatorOpenBaoDenied` ([runbooks](runbooks.md)).

The rules below add alerts on readiness, conflicts and the write budget. They use only the metrics
in the [operator metrics](#operator-metrics) table. The `for` durations and thresholds are starting
points; tune them to your drift intervals. Add them to your own rule file or `PrometheusRule`:

```yaml
groups:
  - name: openbao-config-operator-extra
    rules:
      - alert: OpenBaoConfigConnectionNotReady
        expr: max without (pod, instance) (obco_connection_ready) == 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.kind }} {{ $labels.namespace }}/{{ $labels.name }} is not Ready."
          description: "Resources that use this connection stop converging. Check the connection's Ready reason."
      - alert: OpenBaoConfigResourceNeedsAction
        expr: max without (pod, instance) (obco_resource_ready{reason=~"Conflict|NamespaceChanged|ServerChanged|SecretNotOwned|OutputNotDelivered|RootOnlyKind|InvalidSpec"}) == 0
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.kind }} {{ $labels.namespace }}/{{ $labels.name }} is not Ready: {{ $labels.reason }}."
          description: "This reason does not clear without a change to the spec, the connection or OpenBao."
      - alert: OpenBaoConfigResourcesNotReady
        expr: sum by (kind, reason) (max without (pod, instance) (obco_resource_ready == 0)) > 0
        for: 30m
        labels:
          severity: info
        annotations:
          summary: "{{ $value }} {{ $labels.kind }} resources are not Ready: {{ $labels.reason }}."
      - alert: OpenBaoConfigOwnershipConflicts
        expr: sum by (kind) (increase(obco_conflicts_total[30m])) > 0
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.kind }} resources keep hitting ownership conflicts."
          description: "Two resources, or two clusters, claim the same OpenBao path."
      - alert: OpenBaoConfigWriteBudgetExhausted
        expr: sum by (server) (increase(obco_write_budget_exhausted_total[15m])) > 0
        for: 1h
        labels:
          severity: warning
        annotations:
          summary: "The write budget of BaoServer {{ $labels.server }} keeps running out."
          description: "Something rewrites owned objects with an unchanged spec. Find the kind with obco_writes_total."
      - alert: OpenBaoConfigLoginFailures
        expr: sum by (connection) (increase(obco_logins_total{result="failure"}[15m])) > 0
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Logins of connection {{ $labels.connection }} fail."
          description: "The label is the connection UID. Check the connection's AuthenticationFailed message."
```

`obco_resource_ready` and `obco_connection_ready` are rebuilt from the conditions after a
manager restart or a leader change, one series at a time as resources are reconciled, so a `for`
of at least 10 minutes avoids alerts on an empty or partial series set.

## What the metrics do not show

- `Paused`, `CleanupBlocked`, `DeleteGuarded` (on a resource being deleted), `WriteBudgetExceeded`
  and `ReconcileInProgress` set only the `Synced` condition of a resource that already has `Ready`.
  `obco_resource_ready` follows `Ready`, so these states keep the previous `Ready` series. Find
  them with `kubectl get obco -A` and the `Synced` column.
- There is no gauge for `BaoServer`. A sealed or unreachable server shows as
  `obco_connection_ready == 0` on every connection that uses it, with the reason
  `ServerNotReady` in the connection's `Ready` condition.
- A failed pass with an error (`ReconcileError`, `PermissionDenied`) leaves an existing `Ready`
  condition, and so the gauge, as it was. Watch `controller_runtime_reconcile_errors_total` and
  the chart's `OpenBaoConfigOperatorReconcileErrors` alert for these.
- Drift in write-only fields (passwords and other inputs read from Secrets) cannot be detected:
  OpenBao does not return those values.

## Events and conditions

Every write, adoption, recreate and delete is recorded as a `Normal` event, and every stop that
needs attention as a `Warning` event; [status and events](../reference/status-and-events.md#events)
lists them. Argo CD and Flux read the `Ready` condition; the [Argo CD guide](../guides/argocd.md)
ships a health check.

```bash
kubectl get events -A --field-selector type=Warning
kubectl get obco -A
```

## Logs

The manager logs with controller-runtime's zap logger. Defaults:

| property | default | flag |
|----------|---------|------|
| encoding | JSON | `--zap-encoder=json` or `console` |
| level | `info` | `--zap-log-level=debug`, `info`, `error`, or an integer for more verbose debug levels |
| stack traces | at `error` and above | `--zap-stacktrace-level` |
| time format | RFC 3339 | `--zap-time-encoding` (`epoch`, `millis`, `nano`, `iso8601`, `rfc3339`, `rfc3339nano`) |
| development mode | off | `--zap-devel` (console encoding, `debug` level, stack traces from `warn`) |
| output | standard error | |

Pass the flags through the chart's `extraArgs`:

```yaml
extraArgs:
  - --zap-log-level=debug
```

Logs name kinds, paths and the keys that differed. They never contain tokens, secret values or
OpenBao response bodies. Only the leader reconciles, so read the leader's logs; the
[troubleshooting guide](troubleshooting.md#first-look) shows how to find it.

## Related

- [Runbooks](runbooks.md)
- [Status and events](../reference/status-and-events.md)
- [Helm values](../reference/helm-values.md)
- [Troubleshooting](troubleshooting.md)
