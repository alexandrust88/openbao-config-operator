---
title: Run the application OpenBao labs
description: "Run the ESO credential-delivery and transit application labs from a clone of this repository."
---

# Run the application labs

These labs are for contributors working in a clone. They start the disposable kind cluster,
apply operator resources, and assert generation, rotation, encryption and access denials against
real OpenBao. Consumer how-tos without the lab harness:
[Generate an application credential and deliver it with ESO](../../tutorials/generated-credentials.md),
[Encrypt application data with OpenBao transit](../../tutorials/application-encryption.md).

## What it covers

| Lab | Command | What it asserts |
| --- | --- | --- |
| ESO credential delivery | `make lab-eso` (after identity) | password reaches the ESO-owned Secret; generator-setting changes do not rotate; `refreshNonce` rotates and ESO delivers; unrelated keys stay out of the projection; deleting the Secret recreates the same password; tenant isolation; `Orphan` retains the OpenBao value |
| Transit application | `make lab-transit` | workload Kubernetes auth encrypts and decrypts; rotation and rewrap; cross-key migration; access denials; destructive-delete guard |

## Prerequisites

- Docker, and the tools the [local lab](../../tutorials/local-lab.md) lists.
- From the repository root, a disposable kind cluster with TLS OpenBao and the operator:

```bash
make lab-up
export KUBECONFIG="$PWD/.tmp/lab/openbao-config-operator-lab/kubeconfig"
```

The ESO lab also needs the identity tenants and External Secrets Operator:

```bash
hack/lab/identity-up.sh
make yq
```

## Run

ESO credential delivery (installs ESO, applies
`examples/recipes/generated-credentials/credentials.yaml` for the lab tenants, then runs the
assertions):

```bash
make lab-eso
```

Equivalent steps:

```bash
hack/lab/eso-up.sh
hack/lab/eso-test.sh
```

Transit application (builds `examples/apps/transit`, loads the image into kind, applies mounts,
keys, policies and Kubernetes auth roles, runs the workload Jobs and lifecycle checks):

```bash
make lab-transit
```

That target runs `hack/lab/transit-test.sh`.

## Inspect

```bash
export KUBECONFIG="$PWD/.tmp/lab/openbao-config-operator-lab/kubeconfig"
kubectl -n obco-lab get randomsecrets,passwordpolicies
kubectl -n obco-lab-alpha get externalsecrets,secrets
kubectl -n obco-lab get transitkeys,kubernetesauthroles,aclpolicies
```

The transit profile creates and deletes dedicated test keys. An absent deletion-test CR after a
successful run is expected. Automatic rotation has a minimum one-hour period; a short run can
verify configuration but cannot prove that an hour elapsed. External-key providers and
cryptographic certification are outside these labs.

To request a further ESO rotation by hand after the assertions:

```bash
kubectl --context kind-openbao-config-operator-lab -n obco-lab \
  patch randomsecret alpha-application-password --type=merge \
  -p '{"spec":{"refreshNonce":"rotation-2"}}'
```

## Clean up

```bash
bin/kind delete cluster --name openbao-config-operator-lab
```

Deleting the lab removes its OpenBao storage. `deletionPolicy: Orphan` on a `TransitKey` or
`RandomSecret` does not survive destruction of that storage.
