---
description: "Generate a credential in OpenBao and project only its selected key into Kubernetes with External Secrets Operator. Test rotation, recovery and tenant isolation."
---

# Generate an application credential and deliver it with ESO

In this tutorial, OpenBao Config Operator generates a password in a tenant's KV store.
External Secrets Operator (ESO) copies only that password into a Kubernetes Secret.
You will rotate the value explicitly and verify that deleting the projection does not
rotate or delete its source.

This is a disposable extension of the [local lab](local-lab.md). It requires Docker and
its kind cluster, the identity lab's tenant configuration, and ESO. The commands below
install those prerequisites from the repository root:

```bash
make lab-up
export KUBECONFIG="$PWD/.tmp/lab/openbao-config-operator-lab/kubeconfig"
hack/lab/identity-up.sh
make yq
hack/lab/eso-up.sh
```

## Apply the recipe

The checked-in [credential recipe](https://github.com/alexandrust88/openbao-config-operator/blob/main/examples/recipes/generated-credentials/credentials.yaml)
contains three resources:

| Resource | Responsibility |
| --- | --- |
| `PasswordPolicy` | Defines the generated password's length and character set. |
| `RandomSecret` | Generates and stores `password` at `kv/app/generated` in `lab-alpha`. |
| `ExternalSecret` | Projects that single key into `obco-lab-alpha/generated-application`. |

`hack/lab/eso-up.sh` applies this recipe for both `alpha` and `beta`, using the existing tenant
SecretStores and Kubernetes authentication roles. To apply the checked-in alpha recipe
again without the helper:

```bash
kubectl --context kind-openbao-config-operator-lab apply \
  -f examples/recipes/generated-credentials/credentials.yaml
```

The `RandomSecret` deliberately omits `writeSecretRef`. ESO is the sole owner of the
projected Kubernetes Secret. Do not point both controllers at the same destination.
The ESO role can read its tenant's application paths; it cannot write them or read the
other tenant. Selecting a property limits the projection, not OpenBao access to other
properties at the same KV path. Put independently authorised values at separate paths.

## Verify generation and rotation

Run the regression without printing credential values:

```bash
hack/lab/eso-test.sh
```

For each tenant, the test verifies that:

1. The generated password reaches the ESO-owned Secret.
2. Changing generation settings does not change the existing password or create a KV version.
3. Changing `refreshNonce` generates a different password and ESO delivers it.
4. An unrelated key at the same KV path survives rotation and stays out of the projected Secret.
5. Deleting the projected Secret causes ESO to recreate it with the same password; the KV version stays unchanged.
6. The reader cannot write credentials or read the other tenant's credential.
7. Deleting the ExternalSecret removes its owned projection; deleting the RandomSecret
   with `Orphan` retains the remote value. Recreating the recipe adopts that value.

The regression intentionally changes the generator settings and nonce. To request a
subsequent rotation yourself, change the nonce to a new value:

```bash
kubectl --context kind-openbao-config-operator-lab -n obco-lab \
  patch randomsecret alpha-application-password --type=merge \
  -p '{"spec":{"refreshNonce":"rotation-2"}}'
```

Wait for the current-generation `Ready` and `Synced` conditions and ESO's `Ready`
condition. A ready Kubernetes Secret does not prove that an application has reloaded it:
applications using environment variables need a restart; mounted-file consumers must
reload changed files.

## Understand the boundary

No credential values appear in these manifests. The projected Kubernetes Secret still
contains the value and may be stored in etcd and backups. Keep Secret read permissions
narrow and configure Kubernetes encryption at rest.

This recipe creates an application credential. It does **not** update a database user's
password, renew a lease, or coordinate application cutover. For database credentials,
configure `DatabaseConnection` and `DatabaseRole` or `DatabaseStaticRole`, then test the
consumer's database login and expiry or rotation behaviour separately.

`deletionPolicy: Orphan` retains the OpenBao configuration and generated value if the
operator resources are removed. The ESO target has `creationPolicy: Owner`, so deleting
its `ExternalSecret` removes the owned Kubernetes projection. Deleting the complete
lab removes its disposable OpenBao storage as well:

```bash
bin/kind delete cluster --name openbao-config-operator-lab
```

See [the ownership decision](../adr/0007-no-secret-sync.md) before adapting this pattern
for a shared cluster.
