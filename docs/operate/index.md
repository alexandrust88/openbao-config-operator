---
description: "Operate OpenBao Config Operator with guides for upgrades, observability, troubleshooting, backups and tenant quotas."
---

# Operations

Guides for running openbao-config-operator in production. The design is in
[architecture.md](../architecture.md) and the kinds are in [kinds.md](../kinds.md).

| page | covers |
|------|--------|
| [install.md](../get-started/install.md) | Helm and `install.yaml`, CRDs, the hash-key Secret, `watchNamespaces`, tenancy models, high availability, monitoring, NetworkPolicy, extra volumes |
| [connections.md](../reference/connections.md) | token, Kubernetes, JWT and AppRole auth, injected identity, `namespace` and `authNamespace`, CA bundles, status and reasons, the matching `bao` commands |
| [openbao-policy.md](openbao-policy.md) | least-privilege OpenBao policies for a platform and a tenant identity, `sudo` paths, the connection probe |
| [backup-restore.md](backup-restore.md) | what lives where, restore scenarios, Velero, deletion policies, uninstalling safely |
| [migration.md](../guides/migration.md) | adopting existing OpenBao configuration: `Observe`, `obc-import`, `creationPolicy: Adopt`, `deletionPolicy: Orphan` |
| [troubleshooting.md](troubleshooting.md) | conditions and every reason, debug logging, metrics, common OpenBao errors, stuck finalizers |
| [runbooks.md](runbooks.md) | one runbook per alert |
| [observability.md](observability.md) | metrics, events and the conditions to alert on |
| [tenant-quotas.md](tenant-quotas.md) | capping the object count per tenant namespace with a `ResourceQuota` |
| [upgrade.md](upgrade.md) | API compatibility, CRD upgrades, upgrading and rolling back |
| [load-testing.md](load-testing.md) | steady-state and drift numbers at scale |
| [live-testing.md](live-testing.md) | a kit that applies every built-in kind to a running OpenBao |
| [argocd.md](../guides/argocd.md) | Argo CD health check for every kind ([argocd-cm-health.yaml](../guides/argocd-cm-health.yaml)) |
| [self-init.md](../guides/self-init.md) | OpenBao self-init that creates the operator's auth role and policy, so no root token is needed |

Related pages:

- [security.md](../security.md): threat model, tenant boundary, supply chain and release
  verification.
