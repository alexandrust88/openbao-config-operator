# Tenant quotas

Every managed object costs drift reads against its OpenBao server
([architecture](../architecture.md#scale-and-drift)). One tenant that creates thousands of objects
uses up the shared request budget of the server for every other tenant. Cap the object count per
Kubernetes namespace with a standard `ResourceQuota`. The operator needs nothing for this: object
count quotas work on any custom resource, as `count/<plural>.<group>`.

Quota the kinds a tenant creates in large numbers. The limits below are an example; size them from
the server's request budget (objects / drift interval = reads per second).

```yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: openbao-objects
  namespace: team-a
spec:
  hard:
    count/policies.sys.openbaoconfig.io: "200"
    count/kubernetesauthroles.auth.openbaoconfig.io: "200"
    count/jwtauthroles.auth.openbaoconfig.io: "100"
    count/approles.auth.openbaoconfig.io: "50"
    count/kvsecrets.secrets.openbaoconfig.io: "500"
    count/randomsecrets.secrets.openbaoconfig.io: "200"
    count/entities.identity.openbaoconfig.io: "200"
    count/entityaliases.identity.openbaoconfig.io: "400"
    count/groups.identity.openbaoconfig.io: "100"
    count/groupaliases.identity.openbaoconfig.io: "100"
    count/groupmemberships.identity.openbaoconfig.io: "200"
    count/accessgrants.access.openbaoconfig.io: "50"
    count/openbaoendpoints.core.openbaoconfig.io: "50"
```

You should see the quota usage with:

```sh
kubectl -n team-a describe resourcequota openbao-objects
```

When a tenant reaches a limit, the API server rejects the next create with `exceeded quota`; the
objects that already exist keep reconciling.

A quota limits what a namespace can create. It does not limit what a tenant can do in OpenBao; the
OpenBao policy behind the tenant's connection does that.
