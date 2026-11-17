---
description: "Prepare an isolated Okta application, users, groups and authorization server for OpenBao OIDC and JWT regression tests."
---

# Prepare Okta for human OIDC regression

Use a dedicated Okta test org to provision identities for the operator's root, alpha and beta
OpenBao namespaces. The script creates provider resources; the operator manages OpenBao through
custom resources. It does not configure an existing workforce org.

The setup has one browser application, nine groups and nine users:
`admin`, `read` and `ops` in each namespace. These role names describe OpenBao ACL access, not Okta
administrator privileges. No test user receives an Okta administrator role.

## Account requirements

Create an Okta test org and confirm that it supports a **new custom authorization server**. The
script deliberately leaves the org server and the `default` server unchanged. Custom servers
require API Access Management entitlement in production; availability and limits depend on the
account. An Integrator Free Plan account can have a preconfigured `default` server, but this alone
is not proof that the account can create another one. Check the account's entitlement and user/app
limits before applying. The full matrix needs nine new users in addition to your administrator.
[Okta authorization servers](https://developer.okta.com/docs/concepts/auth-servers/)

Use one mailbox you control that accepts plus addressing, or a domain with separate test addresses.
Users receive generated initial passwords,
but Okta enrollment, verification and MFA requirements still apply. Complete those interactively;
the scripts do not weaken global or application sign-in policies. Do not use password grant or
attempt to bypass CAPTCHA/MFA to automate a browser test.

Create an API token under **Security → API → Tokens** using an administrator allowed to manage
users, groups, applications and authorization servers, including deactivation and deletion of those lab resources. This implementation accepts an SSWS token,
whose privileges follow its creator, rather than an OAuth service-app credential. Use a short-lived
dedicated lab administrator/token, restrict its network access as appropriate and revoke it when
provisioning is finished. The script expects an SSWS API token in `api_token`, not an OAuth Bearer token.
[Okta API token guidance](https://developer.okta.com/docs/guides/create-an-api-token/main/)

## Prepare a private workspace

Run from the repository root. The default workspace is ignored by Git; set `LAB_DIR` to another
private location outside version control if needed. Enter credentials in an editor:

```sh
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
umask 077
install -d -m 700 "$LAB_DIR/okta"
${EDITOR:-vi} "$LAB_DIR/okta/credentials.json"
chmod 600 "$LAB_DIR/okta/credentials.json"
```

The JSON shape is:

```json
{
  "domain": "YOUR-ORG.okta.com",
  "api_token": "REPLACE-WITH-LAB-API-TOKEN",
  "email_address": "tester@example.com",
  "app_sign_on_policy_id": "YOUR-DEDICATED-TEST-APP-POLICY-ID"
}
```

`email_address` is the preferred option for one inbox. The script creates distinct addresses such
as `tester+obco-demo-alpha-admin@example.com`, so all nine users can receive mail in the same inbox.
Supply the base address without an existing `+tag`; confirm your mail provider supports plus
addressing. An optional `email_domain` remains supported for separate addresses and is used only
when `email_address` is absent. Every user has a different generated password. Users are created
with assigned passwords and activation enabled; the script does not request one-time activation
links. Provider enrollment and verification rules can still require interaction.

For an unattended browser run, create a dedicated application authentication policy once
in the test org and set its ID in optional `app_sign_on_policy_id`. Configure it deliberately for
the disposable test users and application; it must permit the login the browser can
complete. The script first verifies that the supplied policy is an active `ACCESS_POLICY`,
then assigns it only to the newly created OIDC web application. It never modifies the policy and
never deletes it during cleanup. Omit the field to retain the app's default authentication policy.
The SSWS token creator needs permission to read the selected policy.

Org-wide authentication, enrollment and risk rules still apply. The script does not disable MFA,
add a bypass rule or change global policy. An unattended browser test will fail if these rules
require interaction it cannot perform; use interactive login to test MFA separately.
[Okta application sign-on policy assignment](https://developer.okta.com/docs/guides/configure-signon-policy/main/)

`domain` is the canonical org hostname, without an `/oauth2` path. The file stays local and is not
a Kubernetes Secret. Do not attach the credential file or generated state to an issue or a screenshot.

## Review and apply

First inspect the offline plan. It does not read credentials or contact Okta:

```sh
python3 hack/lab/saas/okta.py \
  --prefix obco-demo \
  --state "$LAB_DIR/okta/state.json"
```

Then provision the dedicated lab:

```sh
python3 hack/lab/saas/okta.py \
  --prefix obco-demo \
  --credentials "$LAB_DIR/okta/credentials.json" \
  --state "$LAB_DIR/okta/state.json" \
  --apply
```

The only registered browser callback is `http://127.0.0.1:8250/oidc/callback`. The OpenBao OIDC role
and browser runner must use that exact callback. This is a loopback CLI callback, not an OpenBao UI
callback; UI login requires a separate explicit callback registration.

Provisioning creates a dedicated server with audience `api://obco-demo`. Human access is limited to
the browser client and the nine lab groups. Its ID token contains a `groups` claim filtered to the
lab prefix. The only enabled grant is authorization code, with `openid`, `profile` and `email`
scopes. OpenBao verifies the OIDC ID token's issuer and browser-client audience, then maps its
groups to namespace-local access through the operator's identity resources. The custom server's
API audience remains required by Okta, but this suite does not create machine clients or exercise
client-credentials JWT login.
[Okta group claims](https://developer.okta.com/docs/guides/customize-tokens-groups-claim/)

Keep the generated state private and retain it until cleanup finishes. The operator receives the
OIDC application's client secret; the administrative token stays with provisioning and cleanup.
The [SaaS identity guide](../guides/saas-identity-lab.md) explains how to run the operator scenarios.

```sh
hack/lab/saas/run.sh apply --state "$LAB_DIR/okta/state.json"
hack/lab/saas/run.sh showcase --state "$LAB_DIR/okta/state.json"
hack/lab/saas/run.sh jwt --state "$LAB_DIR/okta/state.json"
```

## Clean up

```sh
python3 hack/lab/saas/okta.py \
  --prefix obco-demo \
  --credentials "$LAB_DIR/okta/credentials.json" \
  --state "$LAB_DIR/okta/state.json" \
  --cleanup
```

Cleanup deletes only the resource IDs recorded in the state file. It does not search the org
or delete an object because its name shares the prefix. Keep the state file until the command
finishes. If a create request times out, the object can exist in Okta without an ID in that
file. Check the Okta System Log for the prefix before you delete the state or run apply again.

Revoke the API token when you stop using the lab.
[Okta application lifecycle](https://developer.okta.com/docs/api/openapi/okta-management/management/tags/application)

## Check the script without an account

```sh
python3 -m unittest discover -s hack/lab/saas -p test_okta.py -v
```

These tests do not contact Okta. Provisioning, enrollment and browser login need your test account.
