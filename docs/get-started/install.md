---
title: Install OpenBao Config Operator
description: Install OpenBao Config Operator on Kubernetes with a signed Helm chart or manifests.
---

# Install

This guide installs OpenBao Config Operator on a Kubernetes cluster. The operator configures an
OpenBao server that already runs; it does not install one. To try it on a laptop first, follow the
[Quickstart on kind](quickstart.md). For CRDs, namespace scope, tenancy, HA and monitoring, see
[Install options](../operate/install-options.md); to remove the operator, see
[Uninstall](../operate/uninstall.md).

## Before you begin

- Kubernetes 1.31 or later (the chart's `kubeVersion`) and `kubectl` with cluster-admin rights for
  the install, which creates CRDs and cluster roles.
- Helm 3.8 or later. Helm 4.3 or later reads the chart's armoured signing key as it is; older versions need it
  converted ([Verify the chart signature](#verify-the-chart-signature)).
- An initialised, unsealed OpenBao server that the operator pods can reach.

## 1. Verify the release

Each release publishes a signed image, a signed chart and signed release assets, with SBOM and
provenance attestations. The released chart pins the image by digest (`image.digest`), so a
verified chart pulls the verified image. Verify before you install:
[Hardening: verify the release](../security/hardening.md#verify-the-release) has the `cosign` and
`gh attestation` commands.

### Verify the chart signature

Every release also signs the chart with the project's GPG key (Helm provenance). The
file `openbao-config-operator-<version>.tgz.prov` sits next to the chart on the release page and is
a layer of the OCI chart.

1. Download the public key and show its fingerprint:

    ```bash
    curl -fsSLo openbao-config-operator.asc https://docs.obco.dev/charts/pgp-public-key.asc
    gpg --show-keys openbao-config-operator.asc
    ```

    The fingerprint must be `FECBF5840F506776191497D4561068F2CF22E472`, the key Artifact Hub
    shows for the chart.

2. Helm 4.3 or later reads the armoured key file `openbao-config-operator.asc` as it is. Older versions read only a
   binary keyring, so convert the key for them:

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
manifest without values. The Helm commands below install the defaults and need no local
`values.yaml`. To customise the release, create that file first and add `--values values.yaml`
to the command. The values that matter most are in
[Install options](../operate/install-options.md); [Helm values](../reference/helm-values.md) lists
all of them.

=== "Helm (OCI)"

    ```bash
    helm install openbao-config-operator \
      oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
      --version @VERSION@ \
      --namespace openbao-config-operator --create-namespace
    ```

    Pin the chart by digest in GitOps tools that support it; the release notes list the digest.

=== "Helm repository"

    The same chart is served from a classic Helm repository on this site, for tools without OCI
    support. The repository lists only charts that match their release's signed checksums, but
    `helm install` from it checks no signature unless you add `--verify` (see
    [Verify the chart signature](#verify-the-chart-signature)).

    ```bash
    helm repo add openbao-config-operator https://docs.obco.dev/charts
    helm repo update openbao-config-operator
    helm install openbao-config-operator openbao-config-operator/openbao-config-operator \
      --version @VERSION@ \
      --namespace openbao-config-operator --create-namespace
    ```

=== "install.yaml"

    Each GitHub release has an `install.yaml` asset: the CRDs, the RBAC and a single-replica
    manager Deployment in the namespace `obco-system`, watching every
    namespace. Use the chart when you need high availability, a narrower scope, the
    PodDisruptionBudget, a NetworkPolicy or monitoring objects
    ([Install options](../operate/install-options.md)).

    ```bash
    kubectl apply --server-side \
      -f https://github.com/alexandrust88/openbao-config-operator/releases/download/@TAG@/install.yaml
    ```

The Helm commands install the chart defaults: two replicas with leader election, cluster-wide
scope, the PodDisruptionBudget, the NetworkPolicy and HTTPS metrics. `install.yaml` is fixed at one
replica with HTTPS metrics, without a NetworkPolicy or PodDisruptionBudget.

## 3. Check that it worked

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
    kubectl -n obco-system rollout status \
      deployment/obco-controller-manager --timeout=5m
    ```

    ```text title="Expected output"
    deployment "obco-controller-manager" successfully rolled out
    ```

Then check that the CRDs exist:

```bash
kubectl get crd baoservers.core.obco.dev
```

Kubernetes prints the CRD's name and creation time; `NotFound` means the CRDs are not installed.
The kinds that need an external OpenBao plugin get CRDs only from the chart, with
`pluginKinds.enabled: true`.

Confirm:

- [ ] the manager Deployment is available;
- [ ] `kubectl get crd baoservers.core.obco.dev` finds the CRD.

## Troubleshooting

| symptom | check |
|---------|-------|
| `helm install` fails with `invalid ownership metadata` on a CRD | the CRDs exist without Helm ownership: [Install options: CRDs installed outside the release](../operate/install-options.md#crds-installed-outside-the-release) |
| `helm pull --verify` fails | Helm 3 needs the converted `.gpg` keyring |
| the rollout does not finish | `kubectl -n openbao-config-operator describe pod -l app.kubernetes.io/name=openbao-config-operator` shows image pull or Pod Security errors |

## Related

- [Connect to OpenBao](connect.md): create the connection the guides use.
- <a id="crds"></a>[Install options](../operate/install-options.md): CRD ownership, scope, tenancy,
  HA and monitoring.
- <a id="uninstall"></a>[Uninstall](../operate/uninstall.md): remove the operator and, optionally,
  its CRDs.
- [Helm values](../reference/helm-values.md): every chart value and its default.
