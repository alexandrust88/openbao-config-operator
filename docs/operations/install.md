# Install

This page installs the operator and chooses its scope, tenancy model, availability and monitoring.
The operator configures an OpenBao server that already runs; it does not install one.

## Requirements

- Kubernetes 1.31 or later (the chart's `kubeVersion`).
- Helm 3.8 or later, for OCI charts.
- An initialised, unsealed OpenBao server that the operator pods can reach.
- An OpenBao identity for every connection, with a policy from [openbao-policy.md](openbao-policy.md).

## Verify the release first

Each release publishes a signed image and a signed chart, with an SBOM attestation on the image
and build provenance on both (GitHub artifact attestations: SLSA Build L2; cosign `slsaprovenance1`
statement written by the release job: no SLSA level). Verify them before you install. The commands are in
[security.md](../security.md#verifying-releases). The released chart pins the image by digest
(`image.digest`), so a verified chart pulls the verified image.

## Install with Helm

```sh
helm install openbao-config-operator \
  oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version <version> \
  --namespace openbao-config-operator --create-namespace \
  --values values.yaml
```

Pin the chart by digest in GitOps tools that support it. The chart values are listed in the
[chart README](../../charts/openbao-config-operator/README.md).

## Install with install.yaml

Each GitHub release also has an `install.yaml` asset. It holds the CRDs, the RBAC and a
single-replica manager Deployment in the namespace `openbao-config-operator-system`, and it
watches every namespace. It has no values. Use the chart when you need high availability, a
narrower scope, the PodDisruptionBudget, a NetworkPolicy or monitoring objects.

```sh
kubectl apply --server-side -f install.yaml
```

## CRDs

The CRDs are chart templates, not files in the chart's `crds/` directory. Helm therefore
creates them on `helm install` and updates them on `helm upgrade`. Two values control them:

| value | default | effect |
|-------|---------|--------|
| `crds.install` | `true` | render the CRDs with the release |
| `crds.keep` | `true` | add `helm.sh/resource-policy: keep`, so `helm uninstall` does not delete the CRDs |

Keep `crds.keep=true`. Deleting a CRD deletes every custom resource of that kind. The chart refuses
`crds.install=false` together with `crds.keep=false`, because that upgrade would delete the CRDs an earlier
release installed. See
[backup-restore.md](backup-restore.md#uninstall-and-crd-deletion) for what happens in OpenBao
when that occurs.

To manage the CRDs outside the release, for example in an earlier GitOps wave, apply the
`crds.yaml` release asset and install the chart with `crds.install=false`:

```sh
kubectl apply --server-side -f crds.yaml
helm install ... --set crds.install=false
```

Apply the `crds.yaml` of the new version before each `helm upgrade`. [upgrade.md](../upgrade.md)
describes the CRD compatibility rules.

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
([backup-restore.md](backup-restore.md#the-hash-key-secret)).

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
- Access to Secrets, ConfigMaps, `serviceaccounts/token` and Events moves from the ClusterRole to
  a Role and RoleBinding in each listed namespace and in the release namespace.
- The ClusterRole keeps the cluster-wide rules: the custom resources, and read access to
  Namespaces (for `allowedNamespaces`).

List every namespace that holds a credential, a CA bundle or a ServiceAccount that a
`ClusterOpenBaoConnection` references. A reference to an unlisted namespace fails: the connection
reports `InvalidSpec` for a Secret or ConfigMap outside the cache, and `AuthenticationFailed`
when the TokenRequest is forbidden.

## Tenancy models

Choose one model per cluster, or combine them. Connections decide which OpenBao identity a
request uses, so the user-facing `<release>-tenant-editor` role excludes both connection kinds.
Only the platform team creates connections. [connections.md](connections.md) has the manifests.

| model | connection | OpenBao identity | use when |
|-------|------------|------------------|----------|
| Platform only | one `ClusterOpenBaoConnection`, `allowedNamespaces` limited to platform namespaces | the operator's own ServiceAccount (injected identity), a token or an AppRole | only the platform team writes configuration |
| Tenant connections | one `OpenBaoConnection` per tenant namespace | a ServiceAccount in that namespace, through the TokenRequest API | tenants configure their own OpenBao namespace with their own policy |
| Shared cluster connection | one `ClusterOpenBaoConnection` with an `allowedNamespaces` selector | one shared identity | a group of trusted namespaces shares one policy |

In the tenant model, the platform team creates the namespace's `OpenBaoConnection`, its
ServiceAccount and the matching OpenBao role. Tenants then create resources with the
`tenant-editor` role, which is aggregated into the built-in `admin` and `edit` roles. A namespaced
connection can never log in as the operator. The limit of what a tenant can do is the OpenBao
policy of its own ServiceAccount ([openbao-policy.md](openbao-policy.md#tenant-operator-in-a-child-namespace)).

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

The ServiceMonitor skips TLS verification by default because of the self-signed certificate. Set
`serviceMonitor.tlsConfig` to a CA when you set `metrics.certSecret`.

The alerts and what to do about each one are in [runbooks.md](runbooks.md). The metrics are
described in [troubleshooting.md](troubleshooting.md#metrics).

## NetworkPolicy

`networkPolicy.enabled=true` renders a NetworkPolicy for the manager pods:

- ingress only to the metrics port, from `networkPolicy.metrics.from` (empty allows any source);
- egress to DNS (`networkPolicy.dns.to`, kube-dns in `kube-system` by default);
- egress to the API server on `networkPolicy.apiServer.ports` (443 and 6443), limited to
  `networkPolicy.apiServer.cidrs` when set;
- egress to OpenBao on `networkPolicy.openbao.ports` (8200), limited to `networkPolicy.openbao.to`
  when set;
- `networkPolicy.extraEgress` for anything else, such as an external OIDC issuer.

Set the CIDRs and peers in production; the empty defaults allow any destination on those ports.

## CA bundles and extra volumes

An `OpenBaoServer` reads the OpenBao CA from a Secret or ConfigMap through `spec.caBundle`
([connections.md](connections.md#openbaoserver)), for example a ConfigMap that trust-manager
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

1. Write the OpenBao policy for each connection identity: [openbao-policy.md](openbao-policy.md).
2. Create the connections: [connections.md](connections.md).
3. Check `kubectl get openbao -A` and the [troubleshooting](troubleshooting.md) page if a
   resource is not `Ready`.
