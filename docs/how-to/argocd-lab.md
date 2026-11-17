---
title: Deploy OpenBao with Argo CD and Helm
description: Deploy OpenBao and the configuration operator with published Helm charts, public Git Applications and the Argo CD UI.
---

# Deploy OpenBao with Argo CD and Helm

Use this disposable kind lab to see Argo CD deploy OpenBao, the configuration
operator and root/tenant configuration from public Git and published Helm charts.
The checked-in examples are in [`gitops/argocd/`](https://github.com/alexandrust88/openbao-config-operator/tree/main/gitops/argocd).

## Prerequisites

Clone this repository and install Docker, Go, Python 3, kubectl and make. Run the
commands from the repository root. Allow outbound access to GitHub, the Helm
repositories and container registries. The lab uses a fresh kind cluster named
`obco-gitops-argocd`; it refuses to reuse an existing cluster with that name.

## Deploy and test

```bash
make lab-argocd
```

The command installs the upstream `argo-cd` Helm chart, including the API server
and UI. It then applies the checked-in AppProjects and root Application. Argo CD
installs both workloads; the test runner does not install OpenBao or the operator
first. The controller bootstrap uses chart version **10.18.0**, OpenBao uses its
official chart **0.30.7**, and the operator uses published chart **0.4.0**.

The runner uses the standard Helm installation commands:

```bash
helm repo add argo https://argoproj.github.io/argo-helm --force-update
helm repo update argo
helm upgrade --install argocd argo/argo-cd --version 10.18.0 \
  --namespace argocd --create-namespace \
  --values gitops/argocd/values/argocd.yaml --wait --timeout 10m
```

These commands install Argo CD only; `make lab-argocd` also performs the private
OpenBao bootstrap, applies the Projects/root Application, and runs the smoke test.

The source examples follow `main`. CI overrides Git sources with the commit being
tested, including the values-file sources; chart versions remain pinned. This
smoke test reads the public repository and never pushes test changes to GitHub.

| Application | Source | Order |
| --- | --- | --- |
| `gitops-root` | Public Git: `gitops/argocd/applications` | Creates the children |
| `gitops-openbao` | Official OpenBao chart and `gitops/openbao/values.yaml` | Wave 0 |
| `gitops-operator` | Published operator chart and `gitops/argocd/values/operator.yaml` | Wave 10 |
| `gitops-config` | Public Git: `gitops/config` | Wave 20 |

The root's child-Application health check waits for sync and health before
advancing to the next wave. Operator health checks distinguish pending
reconciliation from a current-generation error. The test verifies the expected
Git revision, healthy Applications and real OpenBao behaviour in root and tenant
namespaces. Kubernetes edits and OpenBao drift are repaired by their respective
controllers. This public smoke test does not claim Git-commit mutation or prune
coverage: those require changes to the source repository.

## Inspect the checked-in configuration

The root Application's source is a normal public Git repository:

```yaml
source:
  repoURL: https://github.com/alexandrust88/openbao-config-operator.git
  targetRevision: main
  path: gitops/argocd/applications
```

Each chart Application has a second Git source named `values`. Its Helm source
uses `valueFiles` such as `$values/gitops/openbao/values.yaml`. To adapt the example,
copy it into your own repository, change **all** Git source URLs and revisions,
and update the matching AppProject `sourceRepos` permissions. The root repository
is an administrator trust boundary: someone who can change its Applications can
change what Argo CD deploys within those Projects.

Separate AppProjects constrain sources, destinations and managed resource kinds.
The configuration Project cannot manage Secrets, Deployments or RBAC objects.
These are sync restrictions, not a restriction on the Argo controller's Kubernetes
read permissions. Review Kubernetes RBAC separately for a production deployment.

## Open the Argo CD UI

Keep the disposable cluster after the test:

```bash
KEEP_GITOPS_LAB=1 make lab-argocd
export KUBECONFIG="$PWD/.tmp/lab/obco-gitops-argocd/kubeconfig"
kubectl -n argocd port-forward service/argocd-server 8443:443
```

Open `https://localhost:8443`. The lab server uses a self-signed certificate.
In another terminal, retrieve its generated administrator password privately:

```bash
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath='{.data.password}' | base64 --decode
```

Sign in as `admin` and inspect `gitops-root` and its three children. Do not paste
that password into logs or commit it. No ingress or public administrative endpoint
is enabled. These values are a small local example, not a production hardening
baseline; configure trusted TLS, SSO, access policies, backups and availability
for an actual deployment.

## Secrets and cleanup

The runner creates a private lab CA, TLS Secret and random bootstrap credentials
outside Git. Public configuration contains references, not credentials. It copies
the generated connection credential only after the OpenBao and operator
Applications are healthy; configuration can then reconcile.

The command deletes its cluster when it finishes unless `KEEP_GITOPS_LAB=1` is set.
To remove a retained lab:

```bash
bin/kind delete cluster --name obco-gitops-argocd
```

The Applications intentionally omit cascading-deletion finalizers. Deleting an
Application alone does not prove that its workloads or OpenBao data were removed.
Cluster deletion is the lab cleanup boundary. Before enabling production prune,
review the operator's [creation and deletion policies](../adr/0004-creation-and-deletion-policy-defaults.md).

For reusable health integration, see [Use Argo CD](../guides/argocd.md). For the
alternative controller, see [the Flux lab](flux-lab.md).
