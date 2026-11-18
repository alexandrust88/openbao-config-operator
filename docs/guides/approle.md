---
title: Configure OpenBao AppRole authentication
description: "Give a CI runner or VM an OpenBao AppRole login whose secret ID the operator issues into a Kubernetes Secret and rotates."
---

# Configure AppRole authentication

A machine outside Kubernetes, such as a CI runner or a VM, gets an AppRole login. You declare the
auth method and the role as Kubernetes resources, and the OpenBao Config Operator issues the secret
ID into a Kubernetes Secret and replaces it before it expires.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/auth"                 { capabilities = ["read"] }
    path "sys/auth/approle-ci"      { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "sys/auth/approle-ci/tune" { capabilities = ["create", "read", "update", "delete", "sudo"] }
    # the role, its role ID, secret IDs and the secret ID accessor lookup and destroy
    path "auth/approle-ci/role/*"   { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The `ACLPolicy` named `app-read` from [Manage OpenBao policies with Kubernetes](policies.md) is
  `Ready`.
- A way to deliver the Secret's values to the machine, for example a CI variable that a pipeline
  updates from the Secret.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable the auth method

An `AuthMount` enables the AppRole auth method at `auth/approle-ci`. Save this as
`approle-mount.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: AuthMount
metadata:
  name: approle-ci
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: approle-ci
  type: approle
```

```bash
kubectl apply -f approle-mount.yaml
kubectl -n openbao-config wait authmount/approle-ci --for=condition=Ready --timeout=2m
```

## 2. Create the role

An `AppRole` sets how long secret IDs and tokens live and which policies the token gets.
`policyRefs` makes the role wait until the `ACLPolicy` is `Ready`. Save this as `approle.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: AppRole
metadata:
  name: ci-deployer
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: approle-ci
  name: ci-deployer
  roleID: ci-deployer
  secretIDTTL: 24h
  tokenTTL: 20m
  tokenMaxTTL: 1h
  policyRefs:
    - name: app-read
```

```bash
kubectl apply -f approle.yaml
kubectl -n openbao-config wait approle/ci-deployer --for=condition=Ready --timeout=2m
```

`roleID` fixes the role ID that clients send at login; without it, OpenBao generates a UUID. The
role ID is not a secret, and OpenBao also names the login's entity alias after it
([Manage OpenBao identity with Kubernetes](identity.md)). `secretIDBoundCIDRs` and `tokenBoundCIDRs`
limit the addresses the secret ID and the tokens may be used from. These and the other fields are
in the example with [every field](../reference/examples/auth.md#approle).

## 3. Issue a secret ID

An `AppRoleSecretID` creates a secret ID for the role and writes it to the Secret named in
`writeSecretRef`, with the keys `role_id`, `secret_id` and `secret_id_accessor`. Status holds only
the accessor, never the secret ID. Save this as `approle-secret-id.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: AppRoleSecretID
metadata:
  name: ci-deployer-login
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  roleRef:
    name: ci-deployer
  ttl: 12h
  writeSecretRef:
    name: ci-deployer-approle
```

```bash
kubectl apply -f approle-secret-id.yaml
kubectl -n openbao-config wait approlesecretid/ci-deployer-login --for=condition=Ready --timeout=2m
```

Rotation is on by default for a secret ID with a TTL: the operator creates a new secret ID
`rotation.renewBefore` the expiry (a duration or a percentage of the TTL, by default a third of
it), writes it to the Secret and then destroys the old one. Expiry is checked at every drift
interval, so keep `rotation.renewBefore` longer than that interval. Turn rotation off with
`rotation.enabled: false`.

`Adopt` is not valid for an `AppRoleSecretID`, and `CreateOrAdopt` always creates a new secret ID.
`cidrList`, `tokenBoundCIDRs`, `ttl`, `numUses`, `metadata` and `writeSecretRef` are immutable, so
set them before you apply the resource ([every field](../reference/examples/auth.md#approlesecretid)).

## 4. Check that it worked

The role and the secret ID are `Ready`:

```bash
kubectl -n openbao-config wait approle/ci-deployer approlesecretid/ci-deployer-login \
  --for=condition=Ready --timeout=2m
```

After a later edit, check `Synced` as well, because `Ready` keeps its last value while a write
fails:

```bash
kubectl -n openbao-config get approles,approlesecretids
```

The Secret holds the three keys. `kubectl describe` shows their names and sizes, not the values:

```bash
kubectl -n openbao-config describe secret ci-deployer-approle
```

OpenBao holds the role. Set the CLI to the same OpenBao namespace as the connection:

```bash
bao read auth/approle-ci/role/ci-deployer
```

Log in with the role ID and secret ID from the Secret:

```bash
bao write auth/approle-ci/login \
  role_id="$(kubectl -n openbao-config get secret ci-deployer-approle -o jsonpath='{.data.role_id}' | base64 -d)" \
  secret_id="$(kubectl -n openbao-config get secret ci-deployer-approle -o jsonpath='{.data.secret_id}' | base64 -d)"
```

The response lists `app-read` in the token's policies. If you bound the role or the secret ID to
CIDRs, run the login from an address inside them.

## Troubleshooting

| symptom | check |
|---------|-------|
| `AuthMount` reports `PermissionDenied` | enabling and tuning an auth method needs `sudo` on `sys/auth/approle-ci` and `sys/auth/approle-ci/tune` |
| `AppRole` reports `ReferenceNotReady` | the `ACLPolicy` in `policyRefs` exists in the role's Kubernetes namespace and is `Ready` |
| `AppRoleSecretID` reports `ReferenceNotReady` | the `AppRole` in `roleRef` exists and is `Ready` |
| `AppRoleSecretID` reports `SecretNotOwned` | a Secret of that name exists and was not created by this resource: rename `writeSecretRef` or delete the Secret |
| `AppRoleSecretID` reports `OutputNotDelivered` | OpenBao returned the secret ID but the Secret write failed; the message says how to rotate |
| login fails with `invalid secret id` | the secret ID expired or was rotated: read the Secret again |
| login fails with a CIDR error | `secretIDBoundCIDRs` and `tokenBoundCIDRs` on the role, and `cidrList` and `tokenBoundCIDRs` on the secret ID, against the machine's address |

## Related

- API reference: [AppRole](../reference/api/auth.md#approle),
  [AppRoleSecretID](../reference/api/auth.md#approlesecretid),
  [AuthMount](../reference/api/sys.md#authmount),
  [ACLPolicy](../reference/api/sys.md#aclpolicy).
- [Manage OpenBao policies with Kubernetes](policies.md): declare the ACL the role attaches.
- [Manage OpenBao identity with Kubernetes](identity.md): link the AppRole login to a named entity.
- [Connect to OpenBao](../get-started/connect.md): AppRole for the operator's own login.
