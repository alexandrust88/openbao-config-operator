---
description: "Operate OpenBao Config Operator: upgrades, observability, troubleshooting, backups, quotas and sizing."
---

# Operations

Day-two guides for running the operator against your OpenBao: upgrades, monitoring, failures, backups and sizing.

| page | covers |
|------|--------|
| [Upgrade](upgrade.md) | API compatibility, CRD upgrades, version notes, rollback |
| [Install options](install-options.md) | CRDs, watchNamespaces, tenancy, HA, monitoring, NetworkPolicy |
| [Uninstall](uninstall.md) | remove resources, finalizers, manager and CRDs safely |
| [Observability](observability.md) | metrics, ServiceMonitor, alerts, logs, conditions |
| [Troubleshooting](troubleshooting.md) | readiness, auth, TLS, ownership, stuck finalizers |
| [Runbooks](runbooks.md) | one procedure per chart alert |
| [OpenBao policy for the operator](openbao-policy.md) | least-privilege ACL paths for platform and tenant identities |
| [Tenant quotas](tenant-quotas.md) | `ResourceQuota` caps on managed objects per Kubernetes namespace |
| [Backup and restore](backup-restore.md) | what Git recovers, what needs an OpenBao snapshot |
| [Load testing](load-testing.md) | measured scale numbers and tuning flags |

Install and connect once before these guides: [Install](../get-started/install.md), [Connect to OpenBao](../get-started/connect.md). Adopting existing configuration is in [Migrate to the operator](../guides/migration.md). GitOps health checks are in [Argo CD](../guides/argocd.md).

## Related

- [Architecture](../architecture.md)
- [Kind catalogue](../kinds.md)
- [Security](../security.md)
