---
title: Harden an OpenBao Config Operator deployment
description: Configure tenant isolation, restricted credentials, network access and release verification using the operator security checklist.
---

# Hardening

This page turns the [threat model](threat-model.md) into a checklist for running
openbao-config-operator in production. It follows the layout of the ControlPlane cert-manager
hardening guide: each recommendation has an id `R-<CATEGORY>-nn`, a priority and the threats it
mitigates (`T-nn`, `GAP-nn`, `RR-nn` in the threat model).

Priority: **High** closes a path to the platform identity, to cluster-wide credentials or to data
loss; **Medium** limits blast radius or improves detection; **Low** is defence in depth.

The commands assume the chart defaults: release and namespace `openbao-config-operator`,
hash-key Secret `openbao-config-operator-hash-key`. The cosign and `gh attestation` release verification commands live
only on this page, in [verify the release](#verify-the-release); other pages link here.

## Checklist

| id | priority | recommendation | mitigates |
|----|----------|----------------|-----------|
| [R-RBAC-01](#r-rbac-01-install-namespace-scoped) | High | install namespace-scoped with `watchNamespaces` | T-19, RR-01 |
| [R-RBAC-02](#r-rbac-02-keep-platform-kinds-for-platform-admins) | High | keep servers, connections, Namespace labels and status for platform admins | T-01, T-15, T-17, T-12 |
| [R-RBAC-03](#r-rbac-03-bind-the-tenant-role-deliberately) | Medium | bind `<release>-tenant-editor` only to subjects that may read the namespace's Secrets | T-04, RR-04 |
| [R-RBAC-04](#r-rbac-04-isolate-the-operator-namespace) | High | isolate the operator namespace | T-19, T-22, T-27 |
| [R-NETWORK-01](#r-network-01-enable-the-networkpolicy) | High | keep the NetworkPolicy on and name its peers | T-19, T-29, GAP-07 |
| [R-NETWORK-02](#r-network-02-restrict-openbao-egress) | Medium | restrict OpenBao's own egress | T-13, RR-08 |
| [R-POD-01](#r-pod-01-enforce-pod-security-restricted) | High | enforce Pod Security `restricted` on the operator namespace | T-19 |
| [R-POD-02](#r-pod-02-keep-the-security-context) | High | keep seccomp, read-only root filesystem, non-root, no capabilities | T-19 |
| [R-POD-03](#r-pod-03-resources-and-availability) | Medium | resource limits, two replicas, leader election, PDB | T-23 |
| [R-POD-04](#r-pod-04-no-sidecar-injection) | Medium | no sidecar injection into the operator pod | T-22 |
| [R-SUPPLY-01](#r-supply-01-verify-and-enforce-signatures) | High | verify image and chart ([verify the release](#verify-the-release)); enforce at admission; pin by digest | T-25, RR-11 |
| [R-CONFIG-01](#r-config-01-openbaoserver) | High | `OpenBaoServer`: https, CA bundle, allow-lists, rate limit | T-14, T-17, T-18, T-28, N1 |
| [R-CONFIG-02](#r-config-02-clusteropenbaoconnection) | High | `ClusterOpenBaoConnection`: `allowedNamespaces`, projected token, explicit audiences | T-01, T-15, T-16, N5 |
| [R-CONFIG-03](#r-config-03-forbid-the-escape-hatches-in-production) | Medium | forbid `allowInsecureHTTP` and `{}` selectors in production | T-17, T-18, N6 |
| [R-CONFIG-04](#r-config-04-manager-flags) | Medium | `apiServerAudiences`, `--cluster-id`, plugin kinds off, metrics TLS | T-03, T-24, T-35 |
| [R-OPENBAO-01](#r-openbao-01-least-privilege-policies) | High | least-privilege policies; split the platform identity | T-33, T-34, RR-02 |
| [R-OPENBAO-02](#r-openbao-02-auth-roles-and-token-ttls) | High | auth roles pinned to one ServiceAccount and audience, short token TTLs | T-36, T-37 |
| [R-OPENBAO-03](#r-openbao-03-audit-device-and-attribution-header) | High | audit device plus `X-Openbao-Config-Resource` recorded in clear | T-11, T-32 |
| [R-OPENBAO-04](#r-openbao-04-self-init-and-no-root-token) | High | self-init the operator's role; no root token | design rule 9 |
| [R-TENANCY-01](#r-tenancy-01-server-allow-lists) | High | `allowedNamespaces` on a protected label; one dedicated audience | T-01, T-14, T-18 |
| [R-TENANCY-02](#r-tenancy-02-quotas) | Medium | `ResourceQuota` object counts per tenant namespace; a server `rateLimit` | T-10, N1, RR-07 |
| [R-TENANCY-03](#r-tenancy-03-one-openbao-namespace-per-tenant) | High | one OpenBao namespace, auth role and connection per tenant | T-08, T-34 |
| [R-KEYS-01](#r-keys-01-hash-key) | Medium | protect, back up and, when needed, rotate the hash key | T-27, RR-12 |
| [R-KEYS-02](#r-keys-02-connection-credentials) | Medium | prefer TokenRequest; rotate static credentials | A4 |
| [R-STORAGE-01](#r-storage-01-encrypt-secrets-at-rest) | High | etcd encryption at rest and encrypted backups | T-26, RR-05 |
| [R-MONITORING-01](#r-monitoring-01-alerts-and-audit) | Medium | alerts, Kubernetes audit of the manager, OpenBao detections | T-19, T-32, T-33 |
| [R-OPS-01](#r-ops-01-upgrades) | Medium | upgrade with verification, watch writes, pause to stop | T-25, T-30 |
| [R-OPS-02](#r-ops-02-incidents) | High | incident procedures: pause, observe, revoke | T-01, T-16, T-19 |

## RBAC

### R-RBAC-01: Install namespace-scoped

By default the manager's ClusterRole grants Secrets `get`, `list`, `watch`, `create`, `update`,
`patch` and `serviceaccounts/token` `create` in every namespace
(`templates/rbac/_manager-rules.tpl:60-76`). A compromised manager can then read every Secret
and mint a token for any ServiceAccount, cluster-admin-bound ones included.

List the namespaces that hold resources, connections and the Secrets or ServiceAccounts they
reference, including those a `ClusterOpenBaoConnection` names:

```yaml
watchNamespaces:
  - openbao-platform
  - team-a
  - team-b
```

The chart then moves the Secret, ConfigMap, token and Event rules, and every rule on a namespaced
managed kind with its `status` and `finalizers`, into a Role per listed namespace
(`templates/rbac/manager.yaml:30-70`). An unlisted release namespace gets only `get` on the
hash-key Secret and `create` on Secrets. The ClusterRole keeps only the cluster-scoped
kinds (`openbaoservers`, `clusteropenbaoconnections`), Namespace reads and the OIDC discovery URL.
The manager caches only those namespaces and lists live only inside them (`cmd/main.go:232-244`,
`internal/engine/ownership.go:326-333`). Resources in other namespaces are not reconciled, and the
manager cannot read or patch them. Adding a tenant means a chart upgrade, which is the point:
enrolment becomes a reviewed change.

Check that the manager can no longer mint tokens or patch managed resources outside the list; both
should print `no`:

```sh
kubectl auth can-i create serviceaccounts/token -n kube-system \
  --as=system:serviceaccount:openbao-config-operator:openbao-config-operator
kubectl auth can-i patch policies.sys.openbaoconfig.io -n kube-system \
  --as=system:serviceaccount:openbao-config-operator:openbao-config-operator
```

### R-RBAC-02: Keep platform kinds for platform admins

Grant these only to the platform team or its GitOps identity:

| right | why |
|-------|-----|
| `openbaoservers`, `clusteropenbaoconnections` | choose where credentials go and which identity is used ([privilege equivalences](threat-model.md#privilege-equivalences)) |
| `openbaoconnections` | choose which ServiceAccount or Secret of the namespace logs in; the chart's tenant role already leaves it out (`templates/rbac/user-roles.yaml:14-16`) |
| `patch` on Namespaces (labels, annotations) | labels enrol a namespace in a server; `openbaoconfig.io/paused` on a Namespace stops drift repair for it |
| `update`, `patch` on `*/status` of managed kinds | `status.path` steers deletes; the chart's roles grant only `get` (`templates/_helpers.tpl:85-91`) |

RBAC cannot restrict annotations. The engine requires `openbaoconfig.io/allow-destructive-delete`
before deleting the objects of every kind that reports `Destructive()`, listed in the
[kind catalogue](../kinds.md) (for example `internal/controller/core/openbaoendpoint.go:60`). If tenants must not delete data at all, add an
admission rule that allows that annotation only for platform subjects, in the same way as
[R-CONFIG-03](#r-config-03-forbid-the-escape-hatches-in-production).

### R-RBAC-03: Bind the tenant role deliberately

Every managed kind with a `<field>SecretRef` reads the Secret with the manager's rights, and the
value can be written to an OpenBao path the author can read back. The chart aggregates
`<release>-tenant-editor` into `admin` and `edit` (`values.yaml:77-81`), which already read
Secrets, so nothing is gained there. Do not bind `<release>-tenant-editor` on its own to a subject
that must not read the namespace's Secrets. To keep plain `edit` users away from OpenBao entirely,
set `rbac.aggregate.edit=false` and bind the role explicitly. To keep a single kind, such as the raw-path `OpenBaoEndpoint`
([ADR 0006](../adr/0006-endpoint-escape-hatch.md)), for platform admins, list it in
`rbac.tenantEditor.excludeKinds`.

### R-RBAC-04: Isolate the operator namespace

- Run nothing else in the operator namespace.
- Allow `get` on Secrets there (the hash key), `pods/exec`, `pods/attach`, `pods/portforward`
  and Lease writes only to cluster admins.
- Do not grant workload `create` (pods, deployments, jobs, cronjobs) or `impersonate` on ServiceAccounts
  there to anyone else: a pod that names the manager ServiceAccount gets its token and its cluster-wide rights.
- Keep `pprof.enabled=false`; when needed, it binds to 127.0.0.1 only
  (`templates/deployment.yaml:63-65`).

## Network

### R-NETWORK-01: Enable the NetworkPolicy

The chart's NetworkPolicy is on by default. It admits metrics scrapes only from the release
namespace and `networkPolicy.metrics.monitoringNamespace` (`monitoring`), and allows DNS to
kube-dns. With empty `apiServer.cidrs` or `openbao.to` it still allows egress to any host on 443,
6443 and 8200 (GAP-07), and the install notes (`NOTES.txt`) print a warning. Name every peer:

```yaml
networkPolicy:
  enabled: true
  metrics:
    from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: monitoring
  apiServer:
    cidrs:
      - 10.0.0.10/32
      - 10.0.0.11/32
      - 10.0.0.12/32
  openbao:
    to:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: openbao
        podSelector:
          matchLabels:
            app.kubernetes.io/name: openbao
```

Take the API server addresses from
`kubectl get endpointslices -n default -l kubernetes.io/service-name=kubernetes`. For an OpenBao
outside the cluster, use an `ipBlock` in `openbao.to`. The client already refuses redirects
(`internal/openbao/client.go:276-281,298-299`); the policy is the second line. With NodeLocal
DNSCache, set `dns.nodeLocalDNS.enabled=true`.

On Cilium (RKE2), an `ipBlock` does not match the API server's identity. Set
`networkPolicy.flavor=cilium`: the chart then renders a `CiliumNetworkPolicy` that reaches the API
server through `toEntities: [kube-apiserver]` and OpenBao through `toFQDNs`, and ignores
`metrics.from`, `dns.to`, `apiServer.cidrs` and `openbao.to`:

```yaml
networkPolicy:
  flavor: cilium
  openbao:
    fqdns:
      - openbao.example.com
```

`toFQDNs` needs the Cilium DNS proxy; the chart adds the DNS rule it learns the addresses from.
With empty `openbao.fqdns`, the Cilium policy allows egress to any host on the OpenBao ports, and
the install notes print a warning.

### R-NETWORK-02: Restrict OpenBao egress

Typed kinds such as `KubernetesAuthConfig`, `JWTAuthConfig` and `DatabaseConnection` make the
OpenBao server connect to the host in the spec. Give the OpenBao pods an egress policy that allows
only the API server, the IdPs and the backends you use, and keep those configuration paths out of
tenant policies ([R-OPENBAO-01](#r-openbao-01-least-privilege-policies)).

## Pod security

### R-POD-01: Enforce Pod Security restricted

The chart's defaults meet the `restricted` profile. Enforce it on the namespace so an override
cannot weaken it:

```sh
kubectl label namespace openbao-config-operator \
  pod-security.kubernetes.io/enforce=restricted \
  pod-security.kubernetes.io/enforce-version=latest
```

`kubectl label --dry-run=server` with the same labels prints a warning for any running pod that
would violate the profile.

### R-POD-02: Keep the security context

Do not override these values (`values.yaml:152-168`):

| setting | value |
|---------|-------|
| `runAsNonRoot`, `runAsUser`, `runAsGroup` | `true`, `65532`, `65532` |
| `seccompProfile.type` (pod and container) | `RuntimeDefault` |
| `readOnlyRootFilesystem` | `true`; the pod has no writable volume, since controller-runtime keeps the self-signed metrics certificate in memory |
| `allowPrivilegeEscalation`, `privileged` | `false` |
| `capabilities.drop` | `[ALL]` |

The image is distroless (no shell). Do not mount extra volumes into the directory of the projected
token; the chart refuses the automount directory (`templates/deployment.yaml:4-7`).

### R-POD-03: Resources and availability

- Keep the memory limit (`512Mi` by default) and size it from the object count; the chart sets a
  CPU request and no CPU limit (`values.yaml:145-150`).
- Run two replicas with leader election (`values.yaml:36-40`); the chart refuses more than one
  replica without it (`templates/deployment.yaml:1-3`). Only the leader reconciles.
- Keep the PodDisruptionBudget (`unhealthyPodEvictionPolicy: AlwaysAllow`, so a crash-looping
  pod does not block a node drain) and the topology spread (`values.yaml:177-188`); set a
  `priorityClassName` so the operator is not the first pod evicted.

### R-POD-04: No sidecar injection

The pod needs its ServiceAccount token for the API server, so `automountServiceAccountToken` stays
`true` (`templates/deployment.yaml:43`). Any injected container would share it. Disable mesh and
agent injection for the operator namespace, for example `istio-injection=disabled` on the
namespace, or per pod through `podAnnotations` (`sidecar.istio.io/inject: "false"`,
`linkerd.io/inject: disabled`); the chart does not set them, because a mesh-only OpenBao or an
agent token sink can need a sidecar. Check that only `manager` runs:

```sh
kubectl get pod -n openbao-config-operator -o jsonpath='{.items[*].spec.containers[*].name}'
```

## Supply chain

### R-SUPPLY-01: Verify and enforce signatures

1. Verify the image and the chart before every install or upgrade
   ([verify the release](#verify-the-release)).
2. Install by digest: the released chart pins the image index digest in `image.digest`
   (`hack/release/package-chart.sh`); keep it set when you mirror the image.
3. Enforce the signature at admission, so a replaced image never starts. With Kyverno 1.18 or
   later (API `policies.kyverno.io/v1`):

```yaml
apiVersion: policies.kyverno.io/v1
kind: ImageValidatingPolicy
metadata:
  name: verify-openbao-config-operator
spec:
  validationActions: [Deny]
  matchConstraints:
    resourceRules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["pods"]
  matchImageReferences:
    - glob: "ghcr.io/alexandrust88/openbao-config-operator*"
  attestors:
    - name: release
      cosign:
        keyless:
          identities:
            - issuer: "https://token.actions.githubusercontent.com"
              subjectRegExp: "^https://github.com/alexandrust88/openbao-config-operator/.github/workflows/release.yml@refs/tags/v"
        ctlog:
          url: https://rekor.sigstore.dev
  validations:
    - message: "the image is not signed by the openbao-config-operator release workflow"
      expression: >-
        images.containers.map(image, verifyImageSignatures(image, [attestors.release])).all(e, e > 0)
```

When you mirror the image into a private registry, add the mirror to `matchImageReferences`; the
signature travels with the digest.

#### Verify the release

`.github/workflows/release.yml` runs only on a `v*` tag that is a SemVer 2.0 version, after the
full CI and e2e suites, and signs with keyless cosign (GitHub OIDC, logged in Rekor), so no cosign
key exists. The only signing key is the GPG key of the chart's Helm provenance, described in
[Helm provenance](#helm-provenance-gpg). A release refuses to overwrite an existing GitHub release, image tag or chart version, and
stops when it cannot tell whether one exists. It pushes the image and the chart by digest and tags
them only after the signatures and attestations are in place (the image also after the
vulnerability gate, `hack/release/scan.sh`, high and critical).

Provenance comes in two forms: GitHub artifact attestations (`actions/attest-build-provenance`,
SLSA Build L2) and a cosign `slsaprovenance1` statement written by the release job
(`hack/release/provenance.sh`, no SLSA level). Nothing reaches SLSA Build L3. The cosign statement
lists the source commit and the Dockerfile base images as `resolvedDependencies`;
`verify-release.sh` checks that it is signed by the release run and names the release tag, not the
base images.

| Subject | Signed | Attestations |
| --- | --- | --- |
| image index | cosign | SPDX and CycloneDX SBOM per platform, provenance (cosign `slsaprovenance1` and GitHub), OpenVEX when `hack/release/vex.openvex.json` has statements |
| each platform image (amd64, arm64) | cosign | SPDX and CycloneDX SBOM, provenance (cosign `slsaprovenance1`) |
| chart (OCI) | cosign (Sigstore bundle and legacy `sha256-<digest>.sig`); from v0.3.0 also GPG (Helm provenance, `.tgz.prov` layer) | SPDX and CycloneDX SBOM (the chart and the image it pins), provenance (cosign `slsaprovenance1` and GitHub) |
| chart release asset (`.tgz`), from v0.3.0 | GPG (`.tgz.prov` next to it) | covered by `sha256sums.txt` |
| release assets | `sha256sums.txt.sigstore.json` | GitHub provenance for every file in `sha256sums.txt`, also attached as `openbao-config-operator.intoto.jsonl` |
| OCM component descriptor ([below](#verify-and-transfer-the-ocm-component)) | cosign | provenance (cosign `slsaprovenance1`) |

The release notes list the image index digest, each platform digest and the chart digest. One
script checks every row above, and that the verified chart pins the verified image. Run it from a
checkout of the repository with cosign v3, crane, jq, yq, curl and Helm v4 on `PATH`:

```sh
hack/release/verify-release.sh @TAG@
```

The script prints one `ok` line per check and ends with `release @TAG@ verified`. Optional parts:

- Helm v4 is required to verify the Helm provenance, because the script passes the armored public
  key. If `helm` on `PATH` is Helm v3, set `HELM` to a Helm v4 binary; otherwise the check fails
  with `needs Helm v4 to read the armored key`.
- With `ASSETS_DIR` set to a directory of downloaded release assets, it also checks the signed
  `sha256sums.txt`, the assets against it and their provenance.
- With the `gh` CLI on `PATH`, it also checks the GitHub artifact attestations; with the ocm CLI, the
  [OCM component](#verify-and-transfer-the-ocm-component).

Every signature must come from the release run of exactly the version you install. A pattern that
accepts any `v*` tag also accepts an older, signed release whose tags were moved in a registry or a
mirror, so pin the full identity. By hand, with cosign v3 and crane (`IMAGE_DIGEST` and
`CHART_DIGEST` from the release notes):

```sh
REPO=alexandrust88/openbao-config-operator
IMAGE=ghcr.io/alexandrust88/openbao-config-operator
CHART=ghcr.io/alexandrust88/charts/openbao-config-operator
VERSION=@VERSION@
IMAGE_DIGEST=sha256:...
CHART_DIGEST=sha256:...
PIN=(--certificate-oidc-issuer https://token.actions.githubusercontent.com
  --certificate-identity "https://github.com/$REPO/.github/workflows/release.yml@refs/tags/v$VERSION"
  --certificate-github-workflow-trigger push
  --certificate-github-workflow-repository "$REPO"
  --certificate-github-workflow-ref "refs/tags/v$VERSION")

cosign verify "${PIN[@]}" "$IMAGE@$IMAGE_DIGEST"
cosign verify-attestation "${PIN[@]}" --type spdxjson "$IMAGE@$IMAGE_DIGEST"
cosign verify-attestation "${PIN[@]}" --type cyclonedx "$IMAGE@$IMAGE_DIGEST"
cosign verify-attestation "${PIN[@]}" --type slsaprovenance1 "$IMAGE@$IMAGE_DIGEST"

cosign verify "${PIN[@]}" "$CHART@$CHART_DIGEST"
cosign verify "${PIN[@]}" --new-bundle-format=false "$CHART@$CHART_DIGEST"
cosign verify-attestation "${PIN[@]}" --type spdxjson "$CHART@$CHART_DIGEST"
cosign verify-attestation "${PIN[@]}" --type cyclonedx "$CHART@$CHART_DIGEST"
cosign verify-attestation "${PIN[@]}" --type slsaprovenance1 "$CHART@$CHART_DIGEST"
```

The second `cosign verify` checks the legacy `sha256-<digest>.sig` signature, the one Artifact Hub
looks for.

The platform digests verify the same way (`crane manifest "$IMAGE@$IMAGE_DIGEST"` lists them).
To read an SBOM, decode the verified payload:

```sh
cosign verify-attestation "${PIN[@]}" --type cyclonedx "$CHART@$CHART_DIGEST" |
  jq -r .payload | base64 -d | jq '.predicate.components'
```

With the GitHub CLI, the GitHub attestations of the image, the chart and the release assets:

```sh
GH_PIN=(--repo "$REPO" --signer-workflow "$REPO/.github/workflows/release.yml" --source-ref "refs/tags/v$VERSION")
gh attestation verify "oci://$IMAGE@$IMAGE_DIGEST" "${GH_PIN[@]}"
gh attestation verify "oci://$CHART@$CHART_DIGEST" "${GH_PIN[@]}"
gh release download "v$VERSION" --repo "$REPO" --dir assets
gh attestation verify assets/install.yaml "${GH_PIN[@]}"
cosign verify-blob-attestation "${PIN[@]}" --type slsaprovenance1 \
  --bundle assets/openbao-config-operator.intoto.jsonl assets/install.yaml
cosign verify-blob "${PIN[@]}" --bundle assets/sha256sums.txt.sigstore.json assets/sha256sums.txt
(cd assets && shasum -a 256 -c sha256sums.txt)
```

`helm pull oci://$CHART --version $VERSION` prints the digest it pulled; compare it with
`$CHART_DIGEST` before you install. The classic Helm repository on GitHub Pages
(`/charts/index.yaml`) lists only release tags, and only charts that match their release's signed
`sha256sums.txt` (`hack/docs/chart-index.sh`); `helm install` from it checks no signature unless you
pass `--verify`.

#### Helm provenance (GPG)

From v0.3.0 the chart is also signed with the project's GPG chart signing key (Helm provenance).
v0.2.0 and earlier have no provenance file.

| item | value |
|------|-------|
| provenance file | `openbao-config-operator-<version>.tgz.prov`, next to the chart in the GitHub release and as a layer of the OCI chart |
| key fingerprint | `FECBF5840F506776191497D4561068F2CF22E472`, also in the chart's `artifacthub.io/signKey` annotation |
| public key | `https://alexandrust88.github.io/openbao-config-operator/charts/pgp-public-key.asc` |
| Helm v3 | needs the key dearmored (`gpg --dearmor`) |
| Helm v4 | reads the armored key |

Artifact Hub shows the chart as signed because of the `.prov` file. `verify-release.sh` checks it.
To verify during `helm install` with `--verify`, follow
[Install: verify the chart signature](../get-started/install.md#verify-the-chart-signature). The GPG
signature proves who packaged the chart; the cosign commands in this section also prove which
workflow run built it.

#### Verify and transfer the OCM component

Each release also publishes an [OCM](https://ocm.software) component version,
`github.com/alexandrust88/openbao-config-operator:<version>`, to
`ghcr.io/alexandrust88/ocm`. It references the image and the chart by digest and carries
`install.yaml`, `crds.yaml` and the SPDX and CycloneDX SBOMs as local blobs
(`release/component-constructor.yaml`). The release pushes it after the image and the chart are
signed, never overwrites a published version, and signs the component descriptor manifest with
keyless cosign (the identity above, pinned to the release tag) plus a `slsaprovenance1` attestation.

Verify it at the source, with the ocm CLI v0.54.0, cosign, crane and yq:

```sh
hack/ocm/verify.sh @TAG@
```

It checks the descriptor signature and provenance, that the descriptor is this component and
version, that `operator-image` and `helm-chart` are the signed release digests, and runs
`ocm verify componentversions`, which re-digests every resource. `verify-release.sh` runs it when
the ocm CLI is on `PATH` and skips it, with a message, for a release that has no component.
Then transfer it through a CTF directory into the air-gapped registry:

```sh
COMPONENT=github.com/alexandrust88/openbao-config-operator:@VERSION@
ocm transfer componentversions --copy-resources "ghcr.io/alexandrust88/ocm//$COMPONENT" ./ocm-ctf
ocm transfer ctf ./ocm-ctf registry.example.internal/ocm
ocm verify componentversions "registry.example.internal/ocm//$COMPONENT"
```

The image and the chart keep their manifest digests in the target registry, so the `image.digest`
the chart pins stays valid. OCM does not copy cosign signatures: copy them with `cosign copy` when
admission control inside the air gap enforces them.

The descriptor is signed with cosign, not with an OCM `sigstore-v2` signature, because the ocm
v0.54.0 sigstore verifier checks the Rekor entry but not the certificate identity, so it cannot
tell the release workflow from any other signer. The cosign signature stays at the source:
`--copy-resources` rewrites the resource locations and so the descriptor manifest. Verify before
the transfer; inside the air gap `ocm verify componentversions` still checks every resource against
the digests the descriptor records.

## Configuration

### R-CONFIG-01: OpenBaoServer

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoServer
metadata:
  name: openbao
spec:
  address: https://openbao.openbao.svc:8200
  caBundle:
    configMapRef:
      namespace: openbao
      name: openbao-ca
  allowedNamespaces:
    matchLabels:
      openbaoconfig.io/tenant: "true"
  allowedAudiences: ["openbao"]
  rateLimit:
    qps: 50
    burst: 100
  requestTimeout: 30s
```

- Always `https://`. CEL and the resolver refuse `http://` unless `allowInsecureHTTP: true`
  (`api/core/v1alpha1/openbaoserver_types.go:8`, `internal/connection/resolver.go:332-334`); keep
  that flag for local test servers and forbid it in production with
  [R-CONFIG-03](#r-config-03-forbid-the-escape-hatches-in-production). The import CLI sets it for an
  `http://` address and warns; review its output before committing it.
- Set `caBundle` instead of trusting the system roots for an internal CA. There is no option to
  skip verification (`internal/openbao/client.go:314-316`). Without `caBundle`, `SSL_CERT_FILE` and
  `SSL_CERT_DIR` in the operator pod (chart `extraEnv`) choose the system roots: review them as trust
  configuration.
- The operator never uses a proxy for OpenBao: `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are
  ignored, also when a cluster-wide injector sets them (`internal/openbao/client.go:313`). Allow
  direct egress to the address ([R-NETWORK-01](#r-network-01-enable-the-networkpolicy)).
- The address carries no credentials: the CRD refuses `user:password@`.
- Never use an empty selector (`{}`) for `allowedNamespaces`: it admits every namespace.
- Size `rateLimit` from the OpenBao capacity you can give the operator; it is shared by every
  connection to the server.

### R-CONFIG-02: ClusterOpenBaoConnection

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: ClusterOpenBaoConnection
metadata:
  name: openbao
spec:
  serverRef:
    name: openbao
  allowedNamespaces:
    matchLabels:
      kubernetes.io/metadata.name: openbao-platform
  auth:
    kubernetes:
      mountPath: kubernetes-operator
      role: openbao-config-operator
      injectedIdentity:
        tokenPath: /var/run/secrets/openbao/token
```

with the chart default:

```yaml
injectedIdentity:
  projectedToken:
    enabled: true
    audience: openbao-config-operator
```

- Always set `allowedNamespaces` to a selector of platform namespaces; unset admits none, and `{}`
  hands the platform identity to every tenant.
- Keep the projected token on. The default `injectedIdentity.tokenPath` is the chart's projected
  token; CEL and the resolver refuse the automounted token by path and by file identity, and refuse
  a token file whose `aud` is an API server audience or cannot be read (`internal/connection/resolver.go:527-576`).
- A `token.filesystem` file that holds a JWT gets the same API server audience check; an OpenBao token
  passes unchanged.
- With `serviceAccountRef`, `audiences` is required, and the resolver refuses API server audiences
  for cluster connections as for tenants (`internal/connection/resolver.go:378-391`).
- CEL and the resolver refuse `auth.token.filesystem.path` under
  `/var/run/secrets/kubernetes.io/serviceaccount/`.
- `mountPath` is a clean relative path (CRD pattern, and the login path is checked again before the
  request), so a login cannot reach `sys/` or another mount through `..` or empty segments.
- Resources that use the connection see only "ClusterOpenBaoConnection <name> is not ready (see its
  status)"; read the connection's own status for the Secret, ServiceAccount, file or login error.

### R-CONFIG-03: Forbid the escape hatches in production

The CRDs and the resolver now refuse the unsafe defaults the first threat model found (open cluster
connections, plaintext addresses, the automounted token, API server audiences). Two deliberate
escape hatches remain: `allowInsecureHTTP: true` on an `OpenBaoServer`, and an empty selector
(`{}`) in `allowedNamespaces`, which admits every namespace. In production clusters, forbid both
with a native `ValidatingAdmissionPolicy` (Kubernetes 1.30 or later):

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: openbao-config-operator-production
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["core.openbaoconfig.io"]
        apiVersions: ["v1alpha1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["openbaoservers", "clusteropenbaoconnections"]
  validations:
    - expression: "!has(object.spec.allowInsecureHTTP) || !object.spec.allowInsecureHTTP"
      message: "allowInsecureHTTP is not allowed in this cluster"
    - expression: >-
        !has(object.spec.allowedNamespaces) ||
        (has(object.spec.allowedNamespaces.matchLabels) && size(object.spec.allowedNamespaces.matchLabels) > 0) ||
        (has(object.spec.allowedNamespaces.matchExpressions) && size(object.spec.allowedNamespaces.matchExpressions) > 0)
      message: "allowedNamespaces must not be the empty selector, which admits every namespace"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: openbao-config-operator-production
spec:
  policyName: openbao-config-operator-production
  validationActions: [Deny]
```

### R-CONFIG-04: Manager flags

| setting | value | why |
|---------|-------|-----|
| `apiServerAudiences` | every audience in the API server's `--api-audiences` | the operator always refuses the built-in audiences (including `rke2` and `k3s`) and the discovered issuer (`internal/connection/audiences.go:17-23,29-54`), but not other custom audiences |
| `extraArgs: ["--cluster-id=<name>"]` | a stable name per cluster | the ownership marker carries it, so a second cluster writing the same OpenBao namespace gets `Conflict`; the default is the `kube-system` UID, which changes when a cluster is rebuilt (`cmd/main.go:178-180`) |
| `pluginKinds.enabled` | `false` unless you use those plugins | fewer CRDs, fewer controllers, smaller RBAC |
| `metrics.certSecret` and `serviceMonitor.tlsConfig` | a certificate from your CA, and that CA and `serverName` in the ServiceMonitor | the default ServiceMonitor skips TLS verification of the self-signed certificate (`values.yaml:104-105`); with `metrics.certSecret` the chart refuses to render without `ca` (or `caFile`) and `serverName`, or with `insecureSkipVerify` |
| `--write-budget-per-minute` | default 60; lower for a small OpenBao | caps drift repairs and default-change rewrites per server during upgrades; changed write-only inputs (Secret or hash-key rotation) are exempt and paced only by `rateLimit` |

## OpenBao side

### R-OPENBAO-01: Least-privilege policies

[openbao-policy.md](../operate/openbao-policy.md) derives the paths each kind needs. On top of
it:

- **Split the platform identity.** One identity that can write `sys/policies/acl/*` and auth roles
  can grant itself anything (T-33); OpenBao has no Sentinel or EGP to stop it. Use separate
  `ClusterOpenBaoConnection`s, each with its own role and policy: one for policies and roles, one
  for mounts and engines, one for identity. Keep the policy that grants the operator itself out of
  every identity's reach: name it outside the paths the policy-writing identity may write, for
  example by granting `sys/policies/acl/tenant-*` and `sys/policies/acl/app-*` instead of
  `sys/policies/acl/*`.
- **Never grant** `auth/*`, `+/*`, `sys/raw`, `sys/rotate/*`, `auth/token/create*` or the lease
  endpoints. Grant `sys/audit/*` (with `sudo`) only to an identity that manages `Audit` resources.
- **Tenant identities** are written inside the tenant's OpenBao namespace or prefixed with it, use
  name prefixes (`team-a-*`) and pin `bound_service_account_namespaces` with `allowed_parameters`,
  as in the tenant example of openbao-policy.md. Do not grant tenants `auth/<mount>/config` or
  engine configuration paths (T-13).
- Leave out `delete` when every resource of a kind uses `deletionPolicy: Orphan`.

Check each identity with `bao token capabilities` on the paths it must not reach.

### R-OPENBAO-02: Auth roles and token TTLs

For every connection's role:

| setting | value |
|---------|-------|
| auth mount | a dedicated mount for the operator (for example `kubernetes-operator`), so revoking by prefix in an incident does not log out workloads |
| `bound_service_account_names`, `bound_service_account_namespaces` | exactly one ServiceAccount and namespace, no globs |
| `audience` | the dedicated audience: for the operator's own role the chart's projected-token audience (`openbao-config-operator` by default), for a tenant role an entry of the server's `allowedAudiences` |
| `token_policies` | one policy for this connection; `token_no_default_policy=true` only when that policy grants `lookup-self` and `renew-self` itself |
| `token_ttl`, `token_max_ttl` | `1h` and `24h`, as in `examples/self-init/openbao.hcl:68-69`; the operator renews at a third of the TTL and logs in again before the max |
| `token_bound_cidrs` | the pod CIDR of the cluster, where it is stable |

Avoid `auth.token` (a long-lived token in a Secret). If AppRole is unavoidable, set
`secret_id_bound_cidrs` and `token_bound_cidrs`, and rotate the secret ID by updating its Secret;
the operator reads credentials at every login (`internal/connection/resolver.go:489-497`).

### R-OPENBAO-03: Audit device and attribution header

Enable at least one audit device before any connection is used, ideally in self-init so it exists
from the first request; the `Audit` kind can manage it afterwards (root namespace only, guarded as
destructive). Keep the defaults that HMAC sensitive values; never set `log_raw`.

The operator sends `X-Openbao-Config-Resource: <group>/<kind>/<namespace>/<name>` on every request
(`internal/openbao/pool.go:284`). OpenBao records it only when the header is listed; apply
[examples/sys/auditrequestheader-attribution.yaml](../../examples/sys/auditrequestheader-attribution.yaml),
or the same with a platform connection:

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: AuditRequestHeader
metadata:
  name: x-openbao-config-resource
  namespace: openbao-platform
spec:
  connectionRef:
    kind: ClusterOpenBaoConnection
    name: openbao
  name: X-Openbao-Config-Resource
  hmac: false
```

`hmac: false` keeps the value readable; it names a Kubernetes object, not a secret. Any client can
send the same header, so detections must join it with `auth.display_name` or the entity of the
operator's role before trusting it.

### R-OPENBAO-04: Self-init and no root token

- Create the operator's auth method, role and policy with self-init
  ([self-init.md](../guides/self-init.md)); it revokes the root token it used.
- Keep recovery keys under split custody, and treat `sys/generate-root` as an incident.
- Check now and then that no token with the `root` policy exists: list
  `bao list auth/token/accessors` and look each one up with `bao token lookup -accessor`.

## Tenancy

### R-TENANCY-01: Server allow-lists

- Select tenant namespaces in `OpenBaoServer.spec.allowedNamespaces` by a label only platform
  admins may set. Anyone who can label a Namespace enrols it; protect the label with an admission
  rule if namespace owners can edit labels.
- Use one dedicated audience per server in `allowedAudiences`, such as `openbao`. The API server's
  audiences are refused for tenants whatever the list says (`internal/connection/resolver.go:364-368`).
- Every `ClusterOpenBaoConnection` selects only platform namespaces
  ([R-CONFIG-02](#r-config-02-clusteropenbaoconnection)).

### R-TENANCY-02: Quotas

Cap object counts per tenant namespace with a `ResourceQuota`
([tenant-quotas.md](../operate/tenant-quotas.md)). The request budget and the write budget are
per server, not per tenant, so one namespace can otherwise slow every other one (T-10). Set a
`rateLimit` on every `OpenBaoServer`: without it, nothing paces the requests of one tenant. A
resource cannot poll faster than every 30 s: CEL and the engine floor `spec.driftInterval`
(`api/common/v1alpha1/types.go:120`, `internal/engine/drift.go:42-47`).

### R-TENANCY-03: One OpenBao namespace per tenant

- One OpenBao namespace per tenant, created by the platform with `OpenBaoNamespace`.
- One `OpenBaoConnection` per tenant namespace, written by the platform, with `namespace` set to the
  tenant's OpenBao namespace and a role whose policy reaches only that subtree.
- Shared namespaces (several tenants in one OpenBao namespace) depend entirely on name prefixes in
  policies; avoid them, because `Adopt` lets a tenant take over any object its policy can write
  (T-08).

## Keys and credentials

### R-KEYS-01: Hash key

The Secret `openbao-config-operator-hash-key` holds the HMAC key of `status.writeOnlyHash`
(`internal/engine/hashkey.go:21-50`). Whoever reads it can test guesses of weak write-only inputs
offline against the status of every resource.

- Restrict `get` on Secrets in the operator namespace
  ([R-RBAC-04](#r-rbac-04-isolate-the-operator-namespace)).
- Back it up with the namespace
  ([backup-restore.md](../operate/backup-restore.md#the-hash-key-secret)).

Rotate it only when it may have leaked. Effect: every resource with write-only inputs sees a new
hash with an unchanged spec, so it writes once and re-sends the **current** values of its
write-only inputs from their Secrets; write-once and create-only fields are not re-sent. A changed
write-only hash counts as a changed input, so these rewrites do not wait for the write budget: they
go out as fast as each server's `rateLimit` allows. Set `rateLimit` before you rotate.

1. Make sure every referenced Secret holds the value that should be in OpenBao; a stale Secret will
   overwrite the server.
2. Count the resources that will write:

   ```sh
   kubectl get openbao -A -o json | jq '[.items[] | select((.status.writeOnlyHash // "") != "")] | length'
   ```

3. Keep the old key for a rollback, then delete it and restart every replica, so no pod keeps the
   old key in memory:

   ```sh
   kubectl -n openbao-config-operator get secret openbao-config-operator-hash-key -o yaml > hash-key-old.yaml
   kubectl -n openbao-config-operator delete secret openbao-config-operator-hash-key
   kubectl -n openbao-config-operator rollout restart deployment/openbao-config-operator
   kubectl -n openbao-config-operator rollout status deployment/openbao-config-operator
   ```

4. Watch `openbao_writes_total` rise and return to zero, and the `Updated` events that name
   `write-only inputs`. The new Secret is created immutable on first start.
5. Delete `hash-key-old.yaml` once the wave has finished.

If the key leaked, also rotate the weak inputs it protected (userpass passwords and the like): the
old statuses were readable by anyone with `get` on those resources.

### R-KEYS-02: Connection credentials

- Prefer Kubernetes or JWT auth with a ServiceAccount (TokenRequest, 10 minutes, one login,
  `internal/connection/resolver.go:57`) over Secrets.
- Credentials in Secrets are read at every login, so rotation needs no restart: update the Secret,
  and the pooled client is replaced on its next use.
- One identity per connection and per environment; a development cluster never holds a connection
  to a production server.

## Storage

### R-STORAGE-01: Encrypt Secrets at rest

Connection credentials, one-time outputs and the hash key are Kubernetes Secrets. Enable an
`EncryptionConfiguration` with a KMS provider for `secrets`, encrypt etcd backups, and restrict
who can restore them (T-26).

## Monitoring

### R-MONITORING-01: Alerts and audit

- Enable `prometheusRule.enabled` and `serviceMonitor.enabled`; the alerts and their runbooks are
  in [runbooks.md](../operate/runbooks.md). `OpenBaoConfigOperatorSteadyStateWrites` catches a
  rewrite loop; `OpenBaoConfigOperatorOpenBaoDenied` catches a policy that is too narrow or an
  identity being probed.
- Kubernetes audit policy: log at `Metadata` level the manager ServiceAccount's
  `create` on `serviceaccounts/token` and its `create`, `update`, `patch` on Secrets. Alert on
  TokenRequests for ServiceAccounts that no connection references, and on any API request made
  with the manager's identity from outside its pod IPs.
- OpenBao audit: alert when an operator identity writes the policy bound to an operator role, when
  a write (create, update, delete) from an operator identity carries no attribution header, and
  on logins to the operator's role from outside the cluster's pod CIDR.

## Operations

### R-OPS-01: Upgrades

Follow [upgrade.md](../operate/upgrade.md), and in addition:

1. Verify the new image and chart ([R-SUPPLY-01](#r-supply-01-verify-and-enforce-signatures)).
2. Read the release notes for changed defaults: a changed default can rewrite many objects. The
   write budget caps the rate, not the total.
3. After the upgrade, watch `openbao_writes_total` by kind and the `Updated` events. Steady state
   writes nothing.
4. To stop the operator touching a server while you investigate, annotate the server; nothing is
   read, written or deleted for resources that use it, and deletions keep their finalizers
   (`internal/engine/reconciler.go:258-264,1115-1148`):

   ```sh
   kubectl annotate openbaoservers.core.openbaoconfig.io openbao openbaoconfig.io/paused=true
   ```

   Remove the annotation (`openbaoconfig.io/paused-`) to resume. To roll back while paused, pin the
   earlier image as in [Upgrade: rolling back](../operate/upgrade.md#rolling-back), not with
   `helm rollback`, which also replaces the CRDs; keep the hash-key Secret.

### R-OPS-02: Incidents

The tools, from narrow to wide:

| scope | tool | effect |
|-------|------|--------|
| one resource | `managementPolicy: Observe` in Git | reads and reports drift (`Drifted` with the differing keys), never writes or deletes, and removes the finalizer (`internal/engine/reconciler.go:295-297,1090-1098`) |
| one resource | `openbaoconfig.io/paused: "true"` on it | no reads, writes or deletes |
| one tenant | the annotation on its Namespace | the same for every resource in it |
| one server | the annotation on the `OpenBaoServer` | the same for every resource that uses it |
| everything | scale the Deployment to 0 | the only option when the manager itself is not trusted |

A pause also holds every finalizer, including those in a terminating Namespace, so remove a server
or Namespace pause before deleting the Namespace (N7).

**A tenant abuses its connection** (unexpected writes in OpenBao, T-08, T-10, T-13):

1. Pause its Namespace: `kubectl annotate namespace team-a openbaoconfig.io/paused=true`.
2. Find its requests in the OpenBao audit log by `X-Openbao-Config-Resource` and the role's
   `display_name`.
3. Narrow the tenant's OpenBao policy, then remove the annotation. Deleting the connection does not
   remove OpenBao objects; resources with `deletionPolicy: Delete` keep their finalizer with reason
   `CleanupBlocked`.

**The platform identity is used from a tenant namespace** (T-01): set `allowedNamespaces` on the
cluster connection at once, review the OpenBao audit log for writes attributed to tenant
resources, and revert them.

**The manager or its token is compromised** (T-16, T-19):

1. Scale to 0; deleting the pods also invalidates their pod-bound ServiceAccount tokens:

   ```sh
   kubectl -n openbao-config-operator scale deployment openbao-config-operator --replicas=0
   ```

2. Revoke the operator's OpenBao tokens: with a dedicated auth mount,
   `bao lease revoke -prefix auth/kubernetes-operator/login`; otherwise revoke the accessors found
   in the audit log.
3. In the Kubernetes audit log, list the TokenRequests made with the manager's identity. Tokens it
   minted are not bound to a pod and stay valid until they expire; delete and recreate the affected
   ServiceAccounts (a new UID invalidates them) when the expiry is too far away.
4. Rotate every credential the manager could read: connection Secrets, one-time outputs (for
   example AppRole secret IDs), the hash key
   ([R-KEYS-01](#r-keys-01-hash-key)), and any Secret in the namespaces it covered.
5. Verify and redeploy a known-good digest; resume with the server annotation set, then remove it
   per server.

**A one-time output leaked** (a secret ID or client secret in a Secret): revoke the value in
OpenBao (for a secret ID, `auth/<mount>/role/<role>/secret-id-accessor/destroy` with its
accessor), then recreate the resource so it issues a new one into its Secret.
