# Deploy OpenBao and its configuration operator with GitOps

These examples use public Helm repositories and this public Git repository. No repository
credentials are required. Choose Argo CD or Flux for a cluster; both manage the same OpenBao
and operator resources.

| Directory | Contents |
| --- | --- |
| [argocd/](argocd/) | Argo CD Helm values, AppProjects, root Application and three child Applications |
| [flux/](flux/) | Flux bootstrap, Git and Helm sources, HelmReleases and ordered Kustomizations |
| [openbao/](openbao/) | Shared OpenBao Helm values |
| [config/](config/) | Operator connection and root/tenant policies, auth methods and secrets engines |

The charts have explicit release versions. Application configuration lives in ordinary YAML,
so you can review changes, fork the repository and substitute your own values. The operator
chart is pulled from its published Helm repository; it is not built from a copied chart directory.

Follow the [Argo CD guide](../docs/how-to/argocd-lab.md) for Helm installation and app-of-apps,
or the [Flux guide](../docs/how-to/flux-lab.md) for Helm sources and dependency ordering.
Those guides cover the bootstrap Secrets, readiness checks, access and cleanup.

To exercise either example in a fresh disposable kind cluster:

```sh
make lab-argocd
# Or, on a separate cluster:
make lab-flux
```

CI runs these commands against the public pull-request commit. They verify released-chart
installation, root and child namespace access, transit operations and drift correction. They do
not push Git commits. The operator's separate e2e suite tests an image built from current source.

The sample storage and locally generated TLS/bootstrap credentials are for a disposable cluster.
For a persistent deployment, set your storage class and capacity, provide managed TLS and sealing
material, and configure authentication for your organisation before granting access.
