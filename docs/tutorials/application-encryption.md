---
title: Encrypt application data with OpenBao and Kubernetes workload identity
description: Run an application against operator-managed transit keys, rotate and rewrap ciphertext, and test key access in root and tenant namespaces.
---

# Encrypt application data with OpenBao

Run a small Go application that authenticates with its own Kubernetes
ServiceAccount and encrypts data with operator-managed transit keys. The lab
runs the application in OpenBao's root namespace and a child namespace, then
checks rotation, ciphertext migration and access denials.

## Start the lab

From the repository checkout, follow the [local lab prerequisites](local-lab.md),
then run:

```sh
make lab-up
make lab-transit
```

The first command creates the disposable kind cluster, a TLS-enabled OpenBao
raft cluster and an operator built from this checkout. The second builds the
[Go application](https://github.com/alexandrust88/openbao-config-operator/tree/main/examples/apps/transit),
loads its image into kind, applies operator resources and runs the regressions.
It needs no external identity provider, cloud account or stored application token.

The lab uses these resources:

| Resource | Purpose |
| --- | --- |
| `OpenBaoNamespace` | Creates the child namespace used by the test. |
| `SecretsMount` and `TransitKey` | Configure keys and their lifecycle settings. |
| `AuthMount`, `KubernetesAuthConfig`, `KubernetesAuthRole` | Authenticate the application's projected ServiceAccount token. |
| `Policy` | Grant operations on named keys; keep administration and other keys out of the workload's permissions. |
| Kubernetes ServiceAccount and Job | Run the actual application with its own identity and mounted CA. |

The workload receives neither an OpenBao administrator token nor transit key
material. Each invocation logs in, performs its operation and revokes its session.
The smoke Job emits only a success result. Failed operations report a status or
fixed error message rather than an OpenBao response body.

## Inspect the configuration

The lab keeps its kubeconfig separate from your normal contexts:

```sh
export KUBECONFIG="$PWD/.tmp/lab/openbao-config-operator-lab/kubeconfig"
kubectl -n obco-lab get transitkeys
kubectl -n obco-lab get kubernetesauthroles,policies
```

The regression creates and deletes dedicated test keys to exercise lifecycle
behaviour. An absent deletion-test CR after a successful run is expected.

## Understand the two migration operations

| Operation | What changes | Where plaintext appears |
| --- | --- | --- |
| Rotate a key, then rewrap | Ciphertext moves to a newer version of the same key. | Inside OpenBao; the rewrap response contains ciphertext. |
| Migrate to a different key | The application decrypts under the source key and encrypts under the destination key. | Inside OpenBao and the trusted application process. |

Changing `TransitKey.spec.rotateNonce` requests a new key version; it does not
rewrite application data. Persist and verify replacement ciphertext before
raising `minDecryptionVersion` or retiring a key. Rewrapping cannot recover data
after the key version required to decrypt it has been destroyed.

The application's [command reference](https://github.com/alexandrust88/openbao-config-operator/tree/main/examples/apps/transit#encrypt-and-decrypt)
shows encrypt, decrypt, rewrap and cross-key migration with JSON over standard
input. A decrypt result is sensitive output, not a safe log message. The example
uses direct transit encryption, not envelope encryption or an atomic database
migration.

## Regression scope

The `integration lab` workflow runs the transit profile on matching pull
requests, weekly and on demand. It checks the application and the selected
cryptographic and lifecycle cases against real OpenBao, rather than mocking
successful login or encryption responses. The
[regression source](https://github.com/alexandrust88/openbao-config-operator/blob/main/hack/lab/transit.py)
is the executable case inventory.

External-key providers require additional infrastructure and are outside this
lab. Automatic rotation has a minimum one-hour period; a short CI run can verify
its configuration but cannot prove that an hour has elapsed and rotation fired.
These tests do not establish cryptographic certification, production throughput
or recovery from loss of OpenBao storage.

## Clean up

```sh
bin/kind delete cluster --name openbao-config-operator-lab
```

Deleting this lab also removes its OpenBao storage. Keeping a `TransitKey` with
`deletionPolicy: Orphan` protects against CR deletion, not destruction of the
server's storage. For namespace-level controls, see
[keep transit keys non-exportable](../security/transit-key-policy.md).
