---
description: Prepare an isolated Auth0 tenant for OpenBao browser OIDC and JWT tests, including account permissions and cleanup.
---

# Prepare Auth0 for the OpenBao identity lab

Use a dedicated Auth0 development tenant to test human authentication and group-based access to
OpenBao. The provisioning script creates an application and test identities; the operator
configures OpenBao through custom resources.

Your tenant's quotas, Actions and login policies decide whether its users can complete the
journeys. Use a disposable tenant, not a production identity directory.

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

    Cleanup needs the delete permissions. A credential used only to create resources can omit
    them until you remove the lab.
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

For an unattended browser run, the application's login policy must let the test users sign in
without an interactive challenge. The suite does not bypass MFA, consent or CAPTCHA, and it does
not use the password grant.

## Preview or run provisioning

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

## Diagnose provisioning failures

Failures identify a fixed API stage, such as `POST users` or `POST connections`, and an
allowlisted error category when available. For example, `category=password-strength` means
Auth0 rejected the generated password. Provider response text, user emails, resource IDs
and credentials are never included in that diagnostic.

The lab keeps Auth0's `excellent` password policy. Its generated passwords satisfy the
length and character-class requirements and exclude three identical consecutive characters,
as required by [Auth0's password-policy rules](https://github.com/auth0/auth0-password-policies).
Randomness alone does not guarantee compliance with that repetition rule.

A rejected HTTP 400 is not retried blindly. Check the reported stage and category, preserve
the private state and run cleanup before a new attempt. Explicit HTTP 429 rejections have
bounded retries; ambiguous network failures preserve the pending-create record so cleanup
cannot silently claim to have removed an unrecorded resource.

## Clean up and recover

Use the same prefix and state file:

```sh
python3 hack/lab/saas/auth0.py \
  --credentials "$LAB_DIR/auth0/credentials.json" \
  --state "$LAB_DIR/auth0/state.json" \
  --prefix obco-auth0-demo --cleanup
```

Cleanup removes the resources recorded in the state file, including its own Action binding, and
leaves other Actions alone. It does not delete a resource only because the name shares the prefix.
Retry it after a partial failure.

Keep the state file until cleanup finishes. If a create request times out, the resource can exist
even though its ID was not saved. Look up the prefix in the Auth0 dashboard before you delete the
state or provision the same names again. Revoke the provisioning credential when you stop using
the lab.

## Check the script without an account

```sh
python3 -m unittest discover -s hack/lab/saas -p test_auth0.py -v
```

These tests do not contact Auth0. Browser login and OpenBao authorization need the lab in the
[SaaS identity guide](../guides/saas-identity-lab.md).
