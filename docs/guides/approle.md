# AppRole

This guide gives a machine outside Kubernetes, such as a CI runner or a VM, an AppRole login: the
role is declared in Kubernetes, and the operator issues its secret ID into a Kubernetes Secret and
replaces it before it expires.

## Before you begin

- A `Ready` connection whose OpenBao policy allows `auth/<mount>/role/*` on the approle mount
  ([OpenBao policy for the operator](../operate/openbao-policy.md)), plus `sys/auth/<mount>` and
  `sys/auth/<mount>/tune` with `sudo` if the operator also enables the mount.
- A way to deliver the Secret's values to the machine, for example a CI variable that a pipeline
  updates from the Secret.

## 1. Enable the auth method

Create an `AuthMount` with `type: approle`. The first object in this file enables it at
`approle-ci`:

??? example "examples/sys/authmount.yaml"

    ```yaml
    --8<-- "examples/sys/authmount.yaml"
    ```

## 2. Create the role

```yaml title="examples/auth/approle.yaml"
--8<-- "examples/auth/approle.yaml"
```

`roleID` fixes the role ID; leave it out and OpenBao generates one. `secretIDBoundCIDRs` and
`tokenBoundCIDRs` limit where the secret ID and the tokens may be used. `mountPath` is the path of
the auth method: `approle` here, so change it to `approle-ci` to use the mount of step 1.

## 3. Issue a secret ID

An `AppRoleSecretID` creates a secret ID for the role and writes it to the Secret named in
`writeSecretRef`, with the keys `role_id`, `secret_id` and `secret_id_accessor`. Status holds only
the accessor, never the secret ID.

```yaml title="examples/auth/approlesecretid.yaml"
--8<-- "examples/auth/approlesecretid.yaml"
```

With `rotation` enabled (the default for a secret ID with a TTL), the operator creates a new
secret ID `renewBefore` the expiry, a duration or a percentage of the TTL, writes it to the Secret
and then destroys the old one. Expiry is checked at every drift interval, so keep `renewBefore`
longer than that interval. A secret ID cannot be adopted: use `creationPolicy: Create` or
`CreateOrAdopt`.

## 4. Check that it worked

```bash
kubectl -n openbao-config wait approle/ci-deployer approlesecretid/ci-deployer-login \
  --for=condition=Ready --timeout=2m
kubectl -n openbao-config get secret ci-deployer-approle -o jsonpath='{.data}'
```

The Secret holds the three keys. The examples bind the secret ID and its tokens to
`10.42.0.0/16`, so the login works only from an address inside that range. Set
`secretIDBoundCIDRs`, `tokenBoundCIDRs` and the secret ID's `cidrList` and `tokenBoundCIDRs` to the
machine's range, or leave them out to allow any address. The secret ID's lists are immutable, so
set them before you apply it. Log in from the machine:

```bash
bao write auth/approle/login \
  role_id="$(kubectl -n openbao-config get secret ci-deployer-approle -o jsonpath='{.data.role_id}' | base64 -d)" \
  secret_id="$(kubectl -n openbao-config get secret ci-deployer-approle -o jsonpath='{.data.secret_id}' | base64 -d)"
```

## Troubleshooting

| symptom | check |
|---------|-------|
| `AppRoleSecretID` reports `ReferenceNotReady` | the `AppRole` in `roleRef` exists and is `Ready` |
| `AppRoleSecretID` reports `SecretNotOwned` | a Secret of that name exists and was not created by this resource: rename `writeSecretRef` or delete the Secret |
| `AppRoleSecretID` reports `OutputNotDelivered` | OpenBao returned the secret ID but the Secret write failed; the message says how to rotate |
| login fails with `invalid secret id` | the secret ID expired or was rotated: read the Secret again |
| login fails with a CIDR error | `secretIDBoundCIDRs` and `tokenBoundCIDRs` against the machine's address |

## Related

- API reference: [AppRole](../reference/api/auth.md#approle),
  [AppRoleSecretID](../reference/api/auth.md#approlesecretid),
  [AuthMount](../reference/api/sys.md#authmount).
- [Connect to OpenBao](../get-started/connect.md): AppRole for the operator's own login.
