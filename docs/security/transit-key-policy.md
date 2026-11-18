---
title: Keep OpenBao transit keys non-exportable
description: Enforce non-exportable TransitKey resources with Kubernetes admission policy and separate workload cryptographic access from key administration.
---

# Keep OpenBao transit keys non-exportable

Use this policy when a Kubernetes namespace must only declare transit keys whose
material stays inside OpenBao. It rejects `exportable: true` and
`allowPlaintextBackup: true` on the `TransitKey` resources of the OpenBao Config
Operator, on create and update. Omitted fields and explicit `false` remain allowed.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- A `TransitKey` in that Kubernetes namespace
  ([Manage transit encryption keys](../guides/transit.md)).
- Kubernetes 1.30 or newer with ValidatingAdmissionPolicy enabled, and permission to manage
  admission policies and Kubernetes namespace labels.

## 1. Apply the policy

The policy denies the two flags; its binding applies it only to Kubernetes namespaces with the
label `obco.dev/non-exportable-transit: 'true'`. Save this as `transit-key-policy.yaml`:

```yaml title="examples/security/transit-key-policy.yaml"
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: openbao-transit-non-exportable
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: [secrets.obco.dev]
        apiVersions: [v1alpha1]
        operations: [CREATE, UPDATE]
        resources: [transitkeys]
  validations:
    - expression: '!has(object.spec.exportable) || !object.spec.exportable'
      reason: Forbidden
      message: Transit keys must not be exportable in this namespace
    - expression: '!has(object.spec.allowPlaintextBackup) || !object.spec.allowPlaintextBackup'
      reason: Forbidden
      message: Transit keys must not allow plaintext backup in this namespace
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: openbao-transit-non-exportable
spec:
  policyName: openbao-transit-non-exportable
  validationActions: [Deny]
  matchResources:
    namespaceSelector:
      matchLabels:
        obco.dev/non-exportable-transit: 'true'
```

Apply it and label the Kubernetes namespace:

```bash
kubectl apply -f transit-key-policy.yaml
kubectl label namespace openbao-config obco.dev/non-exportable-transit=true
```

Replace `openbao-config` with the Kubernetes namespace that holds your
`TransitKey` resources. The label selects the Kubernetes namespace, not an
OpenBao namespace. Only a platform administrator should be able to change this
label or the policy.

Enabling the policy does not repair an existing exportable key. OpenBao does not
allow reversing these flags once enabled. Migrate its ciphertext to a new,
non-exportable key before retiring the old key. The policy also rejects future
specification updates to an already-exportable CR in a selected namespace;
plan that migration before adding the label.

To stop enforcing this policy in one Kubernetes namespace:

```bash
kubectl label namespace openbao-config obco.dev/non-exportable-transit-
```

## 2. Close the other write paths

This policy controls `TransitKey` specifications only. It cannot inspect existing
OpenBao key material, prevent direct API calls, or constrain `BaoEndpoint`.
Keep those capabilities away from application identities:

- Give workloads only the required paths for their key, such as
  `transit/encrypt/app`, `transit/decrypt/app` and `transit/rewrap/app` for the key `app` from
  [Manage transit encryption keys](../guides/transit.md).
- Reserve key configuration, rotation, export, backup and deletion paths for
  administrators. Workloads should not have wildcard transit permissions.
- Restrict who can create `BaoEndpoint` resources and who can access an
  administrative connection or its credentials.

## 3. Check that it worked

Try enabling either flag with a server-side dry run:

```bash
kubectl patch transitkey <transit-key> -n openbao-config --type=merge \
  --patch '{"spec":{"exportable":true}}' --dry-run=server
```

Replace `<transit-key>` with the name of your `TransitKey`. Admission denies the request with
`Transit keys must not be exportable in this namespace`.

## Troubleshooting

| symptom | check |
|---------|-------|
| the patch is admitted | the Kubernetes namespace carries the label `obco.dev/non-exportable-transit=true` |
| the patch fails with `NotFound` | the name and Kubernetes namespace of the `TransitKey` |

## Related

- API reference: [TransitKey](../reference/api/secrets.md#transitkey)
- [Hardening](hardening.md)
- [Kubernetes ValidatingAdmissionPolicy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/)
- [OpenBao transit API](https://openbao.org/api-docs/secret/transit/)
