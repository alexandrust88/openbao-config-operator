---
title: Run the Argo CD OpenBao lab
description: "Run the kind lab that deploys OpenBao and the configuration operator through Argo CD app-of-apps from a clone."
---

# Run the Argo CD lab

For contributors working in a clone, this page runs `make lab-argocd` to deploy OpenBao, the operator and root/tenant configuration through Argo CD from the checked-in `gitops/argocd/` examples.

## What it covers

- A fresh kind cluster (`obco-gitops-argocd` by default) that refuses to reuse an existing cluster of that name
- Upstream Argo CD Helm install, then AppProjects and the root Application from public Git
- Wave-ordered children: OpenBao chart, published operator chart, then `gitops/config`
- Smoke checks of Application health, Git revision and real OpenBao behaviour in root and tenant namespaces
- Shared PostgreSQL configuration Applications under `gitops/databases/argocd/`

## Prerequisites

- A clone of this repository; run commands from the repository root
- Docker, Go, Python 3, kubectl, OpenSSL and Make (the target installs `bin/kind`, `bin/helm` and `bin/yq`)
- Outbound access to GitHub, Helm repositories and container registries

## Run

```bash
make lab-argocd
```

That invokes `hack/lab/gitops/run.sh argocd`. The runner installs Argo CD, bootstraps private TLS and credentials outside Git, applies Projects/root Application, and runs the smoke test. It does not pre-install OpenBao or the operator. The Git sources follow `main` by default.

Pin an already-pushed commit:

```bash
GITOPS_REVISION="$(git rev-parse HEAD)" make lab-argocd
```

Keep the cluster after the run:

```bash
KEEP_GITOPS_LAB=1 make lab-argocd
export KUBECONFIG="$PWD/.tmp/lab/obco-gitops-argocd/kubeconfig"
kubectl -n argocd port-forward service/argocd-server 8443:443
```

Retrieve the generated admin password privately (do not log or commit it):

```bash
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath='{.data.password}' | base64 --decode
```

Open `https://localhost:8443` (self-signed certificate) and sign in as `admin`. No ingress is enabled. The values are a small local example, not a production hardening baseline.

## Inspect

| Application | Source | Order |
| --- | --- | --- |
| `gitops-root` | Public Git: `gitops/argocd/applications` | Creates the children |
| `gitops-openbao` | Official OpenBao chart and `gitops/openbao/values.yaml` | Wave 0 |
| `gitops-operator` | Published operator chart and `gitops/argocd/values/operator.yaml` | Wave 10 |
| `gitops-config` | Public Git: `gitops/config` | Wave 20 |

Chart pins live in `hack/lab/gitops/argocd.py` (Argo CD chart, OpenBao chart, published operator chart). Public configuration holds references, not credentials. The smoke test reads the public repository; it does not push. It does not claim Git-commit mutation or prune coverage.

Each chart Application has a second Git source named `values`, and its Helm source reads `valueFiles` such as `$values/gitops/openbao/values.yaml`. To adapt the example, copy it into your own repository, change every Git source URL and revision, and update the matching AppProject `sourceRepos`. The root repository is an administrator trust boundary: whoever can change its Applications can change what Argo CD deploys within those Projects.

Separate AppProjects constrain sources, destinations and managed kinds. The configuration Project cannot manage Secrets, Deployments or RBAC objects. These are sync restrictions, not a restriction on the Argo CD controller's Kubernetes read permissions.

The same run delivers the shared PostgreSQL configuration chart through its own Applications:

| File | Purpose |
| --- | --- |
| `gitops/databases/argocd/project.yaml` | AppProject `database-lab`: database and workload-auth kinds only in `obco-gitops` |
| `gitops/databases/argocd/postgresql-root.yaml` | Application for the root OpenBao namespace |
| `gitops/databases/argocd/postgresql-alpha.yaml` | Application for the child OpenBao namespace `lab-alpha` |

Their values reference the pre-existing `gitops-openbao` connection; the PostgreSQL server, bootstrap password and CA are prepared by the runner, not by the Applications. Argo CD must report ownership of the rendered resources and be Synced and Healthy at the selected commit before the SQL credential checks run. There is no database regression for Flux. Pruning the CRs does not remove the database: their `deletionPolicy` is `Orphan`.

To run the same setup step by step on your own cluster, follow [Deliver OpenBao configuration with Argo CD](../guides/argocd.md). Alternative controller lab: [Run the Flux lab](flux-lab.md).

## Clean up

Without `KEEP_GITOPS_LAB=1` the runner deletes its cluster on exit. To remove a retained lab:

```bash
bin/kind delete cluster --name obco-gitops-argocd
```

Applications omit cascading-deletion finalizers; cluster deletion is the lab cleanup boundary. Before you enable prune in production, read [ADR 0004](../adr/0004-creation-and-deletion-policy-defaults.md) on creation and deletion policies.
