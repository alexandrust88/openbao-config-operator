---
description: Prepare an isolated Auth0 tenant for OpenBao browser OIDC and JWT regression tests, including account permissions, CI credentials and cleanup.
---

# Prepare Auth0 for the OpenBao identity lab

Use a dedicated Auth0 development tenant to test human authentication and group-based access to
OpenBao. The provisioning script creates an application and test identities; the operator
configures OpenBao through custom resources.

The hosted regression passed for the project's test tenants in
[this CI run](https://github.com/alexandrust88/openbao-config-operator/actions/runs/43950421921).
Your tenant's quotas, Actions and login policies still determine whether its users can complete
the same journeys. Use a disposable tenant, not a production identity directory.

## Create a provisioning application

1. Create an Auth0 development tenant and record its native hostname, such as
    `example.us.auth0.com`, without `https://` or a trailing slash.
2. Create a **Machine to Machine** application named `openbao-lab-provisioner`.
3. Authorise it for the **Auth0 Management API** with these scopes:

    ```text
    read:clients create:clients read:client_keys
    read:connections create:connections
    read:users create:users
    read:actions create:actions update:actions
    delete:clients delete:connections delete:users delete:actions
    ```

    Deletion permissions are required for CI cleanup. A credential used only for local
    provisioning can omit them, but cleanup will need a credential with the delete permissions.
4. Save the application's client ID and client secret. This administrative application is separate
    from the web application used for human login.

The Management API credential provisions and removes resources; it is not a machine-login test
against OpenBao. See [Auth0 Management API access tokens](https://auth0.com/docs/secure/tokens/access-tokens/management-api-access-tokens).

## Prepare a private workspace

Run from the repository root. The default workspace is ignored by Git; you can set `LAB_DIR`
to another private location outside version control.

```sh
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
umask 077
install -d -m 700 "$LAB_DIR/auth0"
${EDITOR:-vi} "$LAB_DIR/auth0/credentials.json"
chmod 600 "$LAB_DIR/auth0/credentials.json"
```

Enter the credential JSON in your editor:

```json
{
  "domain": "YOUR_TENANT.us.auth0.com",
  "client_id": "YOUR_PROVISIONER_CLIENT_ID",
  "client_secret": "YOUR_PROVISIONER_CLIENT_SECRET",
  "email_address": "tester@example.com"
}
```

Use the lowercase tenant hostname. Replace `email_address` with a mailbox you control that accepts
plus addressing, without an existing `+tag`. Nine test addresses can then share that inbox.
Alternatively, set `email_domain` for individual addresses in a test domain. Without either field,
the script uses `example.invalid`, which cannot receive verification, password-reset or email MFA
messages. Users are not marked email-verified; complete verification and enrollment normally.

Keep the credential file and generated state private. The provisioning credential never enters
Kubernetes; the operator receives only the generated OIDC application's client secret.

## Configure GitHub Actions

Create the GitHub environment `auth0-lab`, restrict it to trusted branches, and store the credential
JSON as its `SAAS_CREDENTIALS_JSON` secret:

```sh
gh secret set SAAS_CREDENTIALS_JSON --env auth0-lab < "$LAB_DIR/auth0/credentials.json"
```

Follow [Run the SaaS identity regression](../guides/saas-identity-lab.md) to dispatch the workflow.
It provisions disposable resources, runs the operator's OIDC and JWT scenarios and attempts cleanup
when tests finish or fail.

For unattended tests, the dedicated application must permit a login journey the browser runner can
complete. Existing MFA, consent, CAPTCHA and login restrictions remain in force. The suite does not
bypass those controls or use the password grant. Use interactive login when testing MFA itself.

## Preview or run provisioning locally

Choose a unique prefix of 3–31 lowercase letters, digits and hyphens, starting with a letter.
The preview makes no network requests and needs no credential:

```sh
python3 hack/lab/saas/auth0.py \
  --state "$LAB_DIR/auth0/state.json" --prefix obco-auth0-demo
```

To create the resources:

```sh
python3 hack/lab/saas/auth0.py \
  --credentials "$LAB_DIR/auth0/credentials.json" \
  --state "$LAB_DIR/auth0/state.json" \
  --prefix obco-auth0-demo --apply
```

Expect one confidential web application, one database connection, nine users and an application-scoped
post-login Action. The users represent `root`, `alpha` and `beta`, each with `admin`, `read` and `ops`
roles. These are OpenBao application-data roles, not Auth0 administrator privileges.

The application uses the authorization code flow and the callback
`http://127.0.0.1:8250/oidc/callback`. The Action places administrator-managed group membership in the
ID token's `https://openbaoconfig.io/groups` claim. Existing Actions are preserved. Avoid concurrent
changes to tenant Actions while provisioning runs. The lab creates no custom API or machine-JWT client.

Pass the generated state to the operator lab:

```sh
hack/lab/saas/run.sh apply --state "$LAB_DIR/auth0/state.json"
hack/lab/saas/run.sh showcase --state "$LAB_DIR/auth0/state.json"
hack/lab/saas/run.sh jwt --state "$LAB_DIR/auth0/state.json"
```

These commands need the cluster described in the [SaaS identity lab guide](../guides/saas-identity-lab.md).
State includes generated login credentials as well as the records needed for cleanup; keep it until
the lab has been removed. Do not upload it as an artifact or include it in a support report.

## Clean up and recover

Use the same prefix and state file:

```sh
python3 hack/lab/saas/auth0.py \
  --credentials "$LAB_DIR/auth0/credentials.json" \
  --state "$LAB_DIR/auth0/state.json" \
  --prefix obco-auth0-demo --cleanup
```

Cleanup removes recorded lab resources and its own Action binding, preserving unrelated Actions.
It can be retried after a partial failure. A provisioning rerun checks recorded resources; it does
not reset passwords, rotate secrets or repair configuration drift.

If a create request times out, the resource may exist even if its ID was not returned. Keep the state,
inspect the run's prefix in the Auth0 dashboard and resolve residual resources before starting a new
run. Do not delete state and blindly provision the same names again. The cleanup command deliberately
does not delete resources merely because their names share a prefix.

If an older lab state contains machine grants or a custom API, cleanup also needs
`delete:client_grants` or `delete:resource_servers` for those recorded resources. New human-authentication
runs do not require those permissions. Revoke the provisioning credential when retiring the lab.

## Check the script without an account

```sh
python3 -m unittest discover -s hack/lab/saas -p test_auth0.py -v
```

Offline tests check provisioning requests, ownership, credential handling and recovery. Hosted browser
login and OpenBao authorization are verified by the credentialed CI workflow, not by these unit tests.
