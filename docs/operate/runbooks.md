---
title: OpenBao Config Operator runbooks
description: "Respond to the five alerts the OpenBao Config Operator chart renders, with checks and fixes for each."
---

# Runbooks

The chart renders these alerts with `prometheusRule.enabled=true`. They use Prometheus's `up`
series and only metrics the manager exposes: `controller_runtime_reconcile_total`, `controller_runtime_reconcile_errors_total`,
`obco_writes_total{kind}` and `obco_requests_total{connection,method,code}`. The `connection`
label is the connection UID; [observability](observability.md#operator-metrics) shows how to map it to a
connection.

The shipped rules do not alert on objects or connections that are not `Ready`. The manager exports
`obco_resource_ready` and `obco_connection_ready` for that; example rules are in
[observability](observability.md).

## OpenBaoConfigOperatorDown

No manager replica has been scraped as up for 5 minutes. Nothing is reconciled, so drift stays
and new resources stay pending. OpenBao itself is not affected.

1. List the manager pods:
   `kubectl -n <kubernetes-namespace> get pods -l app.kubernetes.io/name=openbao-config-operator`.
2. For a crash loop, read the previous container's log:
   `kubectl -n <kubernetes-namespace> logs <pod> --previous`. A failure to load the hash-key Secret or a missing
   RBAC rule shows at startup. Replace `<kubernetes-namespace>` with the operator's Kubernetes namespace
   (`openbao-config-operator` in the install guide) and `<pod>` with a crash-looping pod from step 1.
3. If the pods run but are not scraped, check the ServiceMonitor labels and that the scraper is
   bound to the `<fullname>-metrics-reader` ClusterRole; without the binding the scrape answers 403.

The alert resolves when Prometheus scrapes at least one replica as `up == 1`.

## OpenBaoConfigOperatorReconcileErrors

More than `prometheusRule.reconcileErrorRatio` (10%) of the reconciles of one kind have failed
for 15 minutes. The alert's `controller` label names the kind, for example
`aclpolicy.sys.obco.dev`.

1. `kubectl get <kind> -A` and look for `Synced=False`; `kubectl describe` shows the reason and
   message. [Status and events](../reference/status-and-events.md#reasons) explains each reason.
2. Many kinds failing at once usually means OpenBao or a connection is down: check
   [OpenBaoConfigOperatorOpenBaoServerErrors](#openbaoconfigoperatoropenbaoservererrors) and
   [OpenBaoConfigOperatorOpenBaoDenied](#openbaoconfigoperatoropenbaodenied).

The alert resolves when the failed share of the kind's reconciles drops below the threshold.

## OpenBaoConfigOperatorSteadyStateWrites

The operator has written objects of one kind to OpenBao in every 15-minute window for 30 minutes.
At rest the operator only reads, so this is a fight.

1. Find the objects: `kubectl get events -A --field-selector reason=Updated` names the path and
   the keys that differed (never their values).
2. Another writer (Terraform, a script, a person, a second cluster) changes the same path: give the
   path one owner. Set `spec.managementPolicy: Observe` on the resource, or pause it with the annotation
   `obco.dev/paused: "true"`, while you sort it out.
3. The same key differs on every read with no other writer: the server normalises the value
   differently from the spec. Write the value as the server returns it and report the kind.

The alert resolves when `obco_writes_total` for the kind stops increasing for 15 minutes.

## OpenBaoConfigOperatorOpenBaoServerErrors

More than `prometheusRule.openbaoErrorRatio` (5%) of the requests of one connection have returned
5xx or failed in transport for 10 minutes.

1. Check OpenBao: `bao status` (sealed, standby, leader), its logs and its load balancer.
2. `code="error"` is a transport failure: DNS, TLS (wrong CA bundle), a NetworkPolicy
   (`networkPolicy.openbao`) or OpenBao being down.
3. The client retries 5xx with backoff, so a short blip does not fire this alert.

The alert resolves when the share of 5xx and `error` responses of the connection drops below the
threshold.

## OpenBaoConfigOperatorOpenBaoDenied

OpenBao has answered 403 to one connection for 15 minutes.

1. Find the denied paths in the OpenBao audit log (filter on the connection's token accessor or
   role), or in the `Synced` messages of the resources that use the connection.
2. Extend the OpenBao policy of the connection identity ([OpenBao policy for the operator](openbao-policy.md)),
   or move the resource to a connection that may manage that path.
3. A 403 on login means the auth role no longer accepts the identity (bound ServiceAccount,
   audience or OpenBao namespace).

The alert resolves when the connection gets no 403 for 5 minutes.

## Related

- [Observability](observability.md)
- [Troubleshooting](troubleshooting.md)
- [OpenBao policy for the operator](openbao-policy.md)
