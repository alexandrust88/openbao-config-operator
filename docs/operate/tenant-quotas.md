---
title: Cap tenant object counts in OpenBao Config Operator
description: "Cap how many OpenBao Config Operator resources a tenant Kubernetes namespace can create, so one tenant cannot use up a shared OpenBao server."
---

# Cap tenant object counts

Cap the number of OpenBao Config Operator resources each tenant Kubernetes namespace can create,
so one tenant cannot use up the request budget of a shared OpenBao server for every other tenant.

## Before you begin

- The operator is installed ([Install](../get-started/install.md)).
- `kubectl` access that can create a `ResourceQuota` in the tenant Kubernetes namespace `team-a`.

## 1. Create the quota

Every managed object costs drift reads against its OpenBao server
([architecture](../architecture.md#scale-and-drift)). A standard `ResourceQuota` caps the object
count per Kubernetes namespace; object count quotas work on any custom resource, as
`count/<plural>.<group>`, so the operator needs nothing for this.

Quota the kinds a tenant creates in large numbers. The limits below are an example; size them from
the server's request budget (objects / drift interval = reads per second). Save this as
`quota.yaml`:

```yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: openbao-objects
  namespace: team-a
spec:
  hard:
    count/aclpolicies.sys.obco.dev: "200"
    count/kubernetesauthroles.auth.obco.dev: "200"
    count/jwtauthroles.auth.obco.dev: "100"
    count/approles.auth.obco.dev: "50"
    count/kvsecrets.secrets.obco.dev: "500"
    count/randomsecrets.secrets.obco.dev: "200"
    count/identityentities.identity.obco.dev: "200"
    count/identityentityaliases.identity.obco.dev: "400"
    count/identitygroups.identity.obco.dev: "100"
    count/identitygroupaliases.identity.obco.dev: "100"
    count/identitygroupmemberships.identity.obco.dev: "200"
    count/accessgrants.access.obco.dev: "50"
    count/baoendpoints.core.obco.dev: "50"
```

```bash
kubectl apply -f quota.yaml
```

## 2. Check that it worked

```bash
kubectl -n team-a describe resourcequota openbao-objects
```

The output lists each `count/...` resource with its `Used` and `Hard` values. When a tenant reaches
a limit, the API server rejects the next create with `exceeded quota`; the objects that already
exist keep reconciling.

A quota limits what a Kubernetes namespace can create. It does not limit what a tenant can do in
OpenBao; the OpenBao policy behind the tenant's connection does that.

## Related

- [OpenBao policy for the operator](openbao-policy.md)
- [Namespaces and tenants](../guides/namespaces-and-tenants.md)
- [Load testing](load-testing.md)
- [Architecture](../architecture.md)
