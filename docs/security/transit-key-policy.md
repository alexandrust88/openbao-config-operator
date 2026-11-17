---
title: Keep OpenBao transit keys non-exportable
description: Enforce non-exportable TransitKey resources with Kubernetes admission policy and separate workload cryptographic access from key administration.
---

# Keep OpenBao transit keys non-exportable

Use this policy when a Kubernetes namespace must only declare transit keys whose
material stays inside OpenBao. It rejects `exportable: true` and
`allowPlaintextBackup: true` on `TransitKey` creates and updates. Omitted fields
and explicit `false` remain allowed.

## Apply the policy

Prerequisites: Kubernetes 1.30 or newer with ValidatingAdmissionPolicy enabled,
the operator CRDs installed, and permission to manage admission policies and
namespace labels. Run these commands from the repository checkout.

```sh
kubectl apply -f examples/security/transit-key-policy.yaml
kubectl label namespace openbao-config openbaoconfig.io/non-exportable-transit=true
```

Replace `openbao-config` with the namespace holding your `TransitKey` resources.
The label selects the **Kubernetes namespace**, not an OpenBao namespace.
Only a platform administrator should be able to change this label or the policy.

Try enabling either flag using a server-side dry run:

```sh
kubectl patch transitkey orders -n openbao-config --type=merge \
  --patch '{"spec":{"exportable":true}}' --dry-run=server
```

Expected result: admission denies the request with
`Transit keys must not be exportable in this namespace`.
The [example admission test](https://github.com/alexandrust88/openbao-config-operator/blob/main/test/examples/transit_admission_test.go)
checks creates, updates, omitted fields, explicit false values and namespaces
outside the binding against a real API server in CI.

## Close the other write paths

This policy controls `TransitKey` specifications only. It cannot inspect existing
OpenBao key material, prevent direct API calls, or constrain `OpenBaoEndpoint`.
Keep those capabilities away from application identities:

- Give workloads only the required paths for their key, such as
  `transit/encrypt/orders`, `transit/decrypt/orders` and `transit/rewrap/orders`.
- Reserve key configuration, rotation, export, backup and deletion paths for
  administrators. Workloads should not have wildcard transit permissions.
- Restrict who can create `OpenBaoEndpoint` resources and who can access an
  administrative connection or its credentials.

Enabling the policy does not repair an existing exportable key. OpenBao does not
allow reversing these flags once enabled. Migrate its ciphertext to a new,
non-exportable key before retiring the old key. The policy also rejects future
specification updates to an already-exportable CR in a selected namespace;
plan that migration before adding the label.

To stop enforcing this policy in one namespace:

```sh
kubectl label namespace openbao-config openbaoconfig.io/non-exportable-transit-
```

See [Kubernetes ValidatingAdmissionPolicy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/)
for policy evaluation and binding semantics, and the
[OpenBao transit API](https://openbao.org/api-docs/secret/transit/) for key settings.
