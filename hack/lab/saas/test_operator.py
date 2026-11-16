"""Offline contract and isolation checks for SaaS lab manifests and token handling."""
import io
import copy
import urllib.error
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import operator_lab as lab


def fixture(provider="auth0"):
    return {"status": "complete", "complete": True, "provider": provider, "issuer": "https://tenant.example.test/",
            "connection_name": "fixture-users", "groups_claim": "https://openbaoconfig.io/groups", "oidc_client_id": "fake-client", "oidc_client_secret": "fake-secret",
            "users": [{"namespace": t, "role": r, "username": f"{t}-{r}", "password": "fake-password", "group": f"{t}-{r}"}
                      for t in lab.TENANTS for r in lab.ROLES]}


def auth(role="read"):
    policies = ["saas-auth0-" + role, "saas-auth0-session"]
    return {"client_token": "fake-token", "entity_id": "fake-entity", "identity_policies": policies,
            "policies": policies, "token_policies": []}


class LocalProviderTests(unittest.TestCase):
    def test_exact_local_issuers_render_isolated_roles(self):
        for provider, issuer in lab.LOCAL_ISSUERS.items():
            state = {**fixture(provider), "issuer": issuer, "oidc_scopes": ["profile", "email", "groups"]}
            objects = lab.render(state)
            roles = [obj["spec"] for obj in objects if obj["kind"] == "JWTAuthRole"]
            self.assertEqual(len(roles), 9)
            self.assertTrue(all(role["oidcScopes"] == ["profile", "email", "groups"] for role in roles))
            self.assertEqual({obj["spec"]["name"] for obj in objects if obj["kind"] == "OpenBaoNamespace"}, {f"saas-{provider}-alpha", f"saas-{provider}-beta"})
            for changes in ({"issuer": issuer + "wrong"}, {"provider": "okta"}, {"provider": "auth0"}, {"issuer": "http://untrusted.test"}, {"oidc_scopes": "profile"}, {"oidc_scopes": ["profile email"]}, {"token_endpoint_auth_method": "none"}):
                with self.subTest(provider=provider, changes=changes), self.assertRaises(ValueError):
                    lab.validate_state({**state, **changes})


class RenderTests(unittest.TestCase):
    def test_all_tenants_have_oidc_group_bindings_only(self):
        state = fixture()
        objects = lab.render(state)
        roles = [x for x in objects if x["kind"] == "JWTAuthRole"]
        self.assertEqual(len(roles), 9)
        for item in roles:
            spec = item["spec"]
            self.assertEqual(spec["tokenNoDefaultPolicy"], True)
            self.assertEqual(spec["roleType"], "oidc")
            self.assertEqual(spec["boundAudiences"], [state["oidc_client_id"]])
            self.assertEqual(spec["allowedRedirectURIs"], [lab.CALLBACK])
            self.assertNotIn("tokenPolicies", spec)
            self.assertEqual(len(spec["boundClaims"][state["groups_claim"]]), 1)
        self.assertEqual({x["spec"]["namespace"] for x in roles if "namespace" in x["spec"]}, {"saas-auth0-alpha", "saas-auth0-beta"})
        self.assertTrue(all(x["spec"]["type"] == "oidc" for x in objects if x["kind"] == "AuthMount"))

    def test_provider_resources_do_not_overlap(self):
        a = lab.render(fixture("auth0"))
        b = lab.render(fixture("okta"))
        self.assertFalse({x["metadata"]["name"] for x in a} & {x["metadata"]["name"] for x in b})

    def test_policy_rights_are_narrow(self):
        for role in lab.ROLES:
            policy = lab.policy(fixture(), role)
            self.assertNotIn('path "*"', policy)
            self.assertNotIn("sudo", policy)
            self.assertNotIn("sys/", policy)
            self.assertNotIn("destroy", policy)
            self.assertNotIn("export", policy)
        self.assertNotIn("transit", lab.policy(fixture(), "read"))
        self.assertNotIn('"create"', lab.policy(fixture(), "ops"))
        self.assertIn('"delete"', lab.policy(fixture(), "admin"))
        self.assertIn("transit/encrypt/app", lab.policy(fixture(), "ops"))

    def test_secrets_are_only_in_secret_objects(self):
        objects = lab.render(fixture())
        public = json.dumps([x for x in objects if x["kind"] != "Secret"])
        self.assertNotIn("fake-secret", public)
        self.assertNotIn("fake-password", json.dumps(objects))
        self.assertEqual(sum(x["kind"] == "GroupMembership" for x in objects), 9)

    def test_ambiguous_groups_and_missing_roles_rejected(self):
        state = fixture()
        state["users"][1]["group"] = state["users"][0]["group"]
        with self.assertRaises(ValueError):
            lab.render(state)
        state = fixture()
        state["users"].pop()
        with self.assertRaises(ValueError):
            lab.render(state)

    def test_private_output_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "private.json"
            lab.write_private(path, fixture())
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(lab.load_state(path)["provider"], "auth0")
            with self.assertRaises(FileExistsError):
                lab.write_private(path, {})
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                lab.load_state(path)


    def test_incomplete_state_rejected(self):
        for provider, key in (("auth0", "status"), ("okta", "complete")):
            state = fixture(provider)
            state.pop(key)
            with self.assertRaises(ValueError):
                lab.render(state)

    def test_fifo_and_symlink_state_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "pipe"
            os.mkfifo(path, 0o600)
            with self.assertRaises(ValueError):
                lab.load_state(path)
            link = Path(directory) / "link"
            link.symlink_to(path)
            with self.assertRaises(OSError):
                lab.load_state(link)

    def test_no_non_kind_context(self):
        with self.assertRaises(ValueError):
            lab.Kube("production")


class TokenTests(unittest.TestCase):



    def test_http_denial_discards_body_and_requires_binding_error(self):
        class Opener:
            def __init__(self, message):
                self.message = message
            def open(self, *args, **kwargs):
                raise urllib.error.HTTPError("https://example.test", 400, "bad request", {},
                    io.BytesIO(json.dumps({"errors": [self.message], "token": "fake-secret"}).encode()))
        for message, expected in (("client secret invalid", False),
                                  ('claim "groups" does not match any associated bound claim values', True)):
            status, body = lab.request_json(Opener(message), "https://example.test")
            self.assertEqual(status, 400)
            self.assertEqual(body, {"binding_denied": expected, "rejection": "binding" if expected else "unknown"})


    def test_http_error_diagnostics_are_categorical_and_fail_closed(self):
        for message, category in (("error exchanging code: secret=private", "exchange"),
                                  ("rate limit exceeded private", "rate-limit"),
                                  ("unknown private", "unknown")):
            opener = unittest.mock.Mock()
            opener.open.side_effect = urllib.error.HTTPError("https://example.test", 400, "bad request", {},
                io.BytesIO(json.dumps({"errors": [message]}).encode()))
            status, body = lab.request_json(opener, "https://example.test")
            self.assertEqual(body, {"binding_denied": False, "rejection": category})
            bao = unittest.mock.Mock()
            with patch.object(lab, "browser_auth", return_value=(status, body)):
                with self.assertRaisesRegex(RuntimeError, "category=" + category) as failure:
                    lab.browser_login(bao, fixture(), "beta", "read", {}, expect_denied=True)
                self.assertNotIn("private", str(failure.exception))

    def test_unexpected_success_is_revoked_and_diagnostics_do_not_leak(self):
        bao = unittest.mock.Mock()
        body = {"auth": {"client_token": "private"}, "rejection": "private"}
        with patch.object(lab, "browser_auth", return_value=(200, body)):
            with self.assertRaisesRegex(RuntimeError, "HTTP 200, token=True, category=unknown") as failure:
                lab.browser_login(bao, fixture(), "beta", "read", {}, expect_denied=True)
            self.assertNotIn("private", str(failure.exception))
        bao.request.assert_called_once_with("beta", "POST", "auth/token/revoke-self", token="private")

    def test_auth0_selects_owned_connection_preserving_oauth_parameters(self):
        state = fixture()
        original = state["issuer"] + "authorize?state=state%2Bnonce&nonce=nonce%2Fvalue&client_id=client&connection=default"
        rewritten = lab.authorization_url(state, original)
        query = urllib.parse.parse_qs(urllib.parse.urlsplit(rewritten).query)
        self.assertEqual(query, {"state": ["state+nonce"], "nonce": ["nonce/value"], "client_id": ["client"], "connection": ["fixture-users"]})
        self.assertEqual(lab.authorization_url(fixture("okta"), original), original)
        with self.assertRaises(RuntimeError):
            lab.authorization_url(state, "https://untrusted.test/authorize?state=nonce")


    def test_no_http_redirects(self):
        self.assertIsNone(lab.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.test"))

    def test_failure_revokes_token(self):
        class Bao:
            calls = []
            def request(self, tenant, method, path, data=None, token=None):
                self.calls.append(path)
                return (204, {}) if path == "auth/token/revoke-self" else (500, {})
        bao = Bao()
        with self.assertRaises(RuntimeError):
            lab.test_access(bao, fixture(), "alpha", "read", auth())
        self.assertEqual(bao.calls[-2:], ["auth/token/revoke-self", "saas-auth0-kv/data/app/config"])


    def test_group_policy_checks_and_revoked_token_rejection(self):
        class Bao:
            def __init__(self, revoked_status=403):
                self.revoked = False
                self.revoked_status = revoked_status
            def request(self, tenant, method, path, data=None, token=None):
                if path == "auth/token/revoke-self":
                    self.revoked = True
                    return 204, {}
                if self.revoked:
                    return self.revoked_status, {}
                if tenant == "alpha" and method == "GET" and path == "saas-auth0-kv/data/app/config":
                    return 200, {"data": {"data": {"value": "saas-lab-marker"}}}
                return 403, {}
        bao = Bao()
        lab.test_access(bao, fixture(), "alpha", "read", auth())
        self.assertTrue(bao.revoked)
        with self.assertRaisesRegex(RuntimeError, "revoked token cannot read"):
            lab.test_access(Bao(200), fixture(), "alpha", "read", auth())
        for changed in ({"identity_policies": ["saas-auth0-read"]}, {"identity_policies": ["saas-auth0-session"]},
                        {"token_policies": ["saas-auth0-read"]}, {"policies": ["root"]}, {"entity_id": ""}):
            bao = Bao()
            with self.assertRaises(RuntimeError):
                lab.test_access(bao, fixture(), "alpha", "read", {**auth(), **changed})
            self.assertTrue(bao.revoked)

    def test_admin_checks_kv_versions_and_protects_transit_keys(self):
        class Bao:
            def __init__(self, corrupt_history=False):
                self.revoked = False
                self.calls = []
                self.corrupt_history = corrupt_history
            def request(self, tenant, method, path, data=None, token=None):
                self.calls.append((method, path))
                if path == "auth/token/revoke-self":
                    self.revoked = True
                    return 204, {}
                if self.revoked or tenant != "alpha":
                    return 403, {}
                if method == "GET" and path.endswith("/data/app/config"):
                    return 200, {"data": {"data": {"value": "saas-lab-marker"}}}
                if path == "saas-auth0-kv/data/app/probe-fixed":
                    if method == "POST":
                        return 200, {"data": {"version": 1 if data["data"]["value"] == "first" else 2}}
                    if method == "DELETE":
                        return 204, {}
                    return 404, {}
                if path.endswith("probe-fixed?version=1"):
                    return 200, {"data": {"data": {"value": "CORRUPT" if self.corrupt_history else "first"}}}
                if path.endswith("probe-fixed?version=2"):
                    return 200, {"data": {"data": {"value": "second"}}}
                if path.endswith("/metadata/app/probe-fixed"):
                    return 200, {"data": {"current_version": 2}}
                if method == "LIST" and path.endswith("/metadata/app"):
                    return 200, {"data": {"keys": ["probe-fixed"]}}
                if path.endswith("/encrypt/app"):
                    return 200, {"data": {"ciphertext": "fake-ciphertext"}}
                if path.endswith("/decrypt/app"):
                    return 200, {"data": {"plaintext": "c2Fhcy1sYWItcHJvYmU="}}
                return 403, {}
        with patch.object(lab.secrets, "token_hex", return_value="fixed"):
            bao = Bao()
            lab.test_access(bao, fixture(), "alpha", "admin", auth("admin"))
            self.assertIn(("LIST", "saas-auth0-kv/metadata/app"), bao.calls)
            self.assertIn(("GET", "saas-auth0-transit/export/encryption-key/app"), bao.calls)
            self.assertIn(("POST", "saas-auth0-transit/keys/app/rotate"), bao.calls)
            self.assertEqual(bao.calls.count(("GET", "saas-auth0-kv/data/app/probe-fixed?version=1")), 2)
            with self.assertRaisesRegex(RuntimeError, "history did not preserve"):
                lab.test_access(Bao(True), fixture(), "alpha", "admin", auth("admin"))


class GroupChangeTests(unittest.TestCase):
    class Kube:
        def __init__(self):
            self.current = next(obj for obj in lab.render(fixture()) if obj["kind"] == "Group" and obj["metadata"]["name"] == "saas-auth0-alpha-read")
            self.current["metadata"].update(resourceVersion="1", uid="fake-group-uid")
            self.original = copy.deepcopy(self.current)
            self.applied = []
            self.waited = []
        def run(self, *args):
            return json.dumps(self.current)
        def apply(self, objects):
            obj = copy.deepcopy(objects[0])
            self.applied.append(obj)
            self.current["spec"] = obj["spec"]
            self.current["metadata"]["resourceVersion"] = str(int(self.current["metadata"]["resourceVersion"]) + 1)
        def wait(self, objects):
            self.waited.append(copy.deepcopy(objects[0]))

    class Bao:
        def __init__(self):
            self.calls = []
        def request(self, tenant, method, path, data=None, token=None):
            self.calls.append((tenant, method, path))
            return (204, {}) if path == "auth/token/revoke-self" else (403, {})

    def test_removal_revokes_data_grant_then_restores_original_spec(self):
        kube, bao = self.Kube(), self.Bao()
        response = {**auth(), "identity_policies": ["saas-auth0-session"], "policies": ["saas-auth0-session"]}
        with patch.object(lab, "browser_auth", return_value=(200, {"auth": response})), patch.object(lab, "browser_login") as login, patch("builtins.print"):
            lab.test_group_policy_change(bao, fixture(), kube)
        self.assertEqual(kube.applied[0]["spec"]["policies"], [])
        self.assertEqual(kube.current["spec"], kube.original["spec"])
        self.assertEqual(kube.applied[1]["metadata"]["resourceVersion"], "2")
        self.assertEqual(len(kube.waited), 2)
        self.assertEqual(login.call_args.args[2:4], ("alpha", "read"))
        self.assertIn(("alpha", "GET", "saas-auth0-kv/data/app/config"), bao.calls)

    def test_api_omitted_empty_policies_still_restores_group(self):
        class OmittingKube(self.Kube):
            def apply(self, objects):
                super().apply(objects)
                if self.current["spec"].get("policies") == []:
                    self.current["spec"].pop("policies")
        kube = OmittingKube()
        response = {**auth(), "identity_policies": ["saas-auth0-session"], "policies": ["saas-auth0-session"]}
        with patch.object(lab, "browser_auth", return_value=(200, {"auth": response})), patch.object(lab, "browser_login"), patch("builtins.print"):
            lab.test_group_policy_change(self.Bao(), fixture(), kube)
        self.assertEqual(kube.current["spec"], kube.original["spec"])
        self.assertEqual(len(kube.applied), 2)

    def test_browser_failure_still_restores_group(self):
        kube = self.Kube()
        with patch.object(lab, "browser_auth", side_effect=RuntimeError("browser unavailable")):
            with self.assertRaisesRegex(RuntimeError, "browser unavailable"):
                lab.test_group_policy_change(self.Bao(), fixture(), kube)
        self.assertEqual(kube.current["spec"], kube.original["spec"])
        self.assertEqual(len(kube.waited), 2)

    def test_direct_policy_cannot_hide_removal_failure(self):
        kube, bao = self.Kube(), self.Bao()
        with patch.object(lab, "browser_auth", return_value=(200, {"auth": auth()})):
            with self.assertRaisesRegex(RuntimeError, "did not change the identity grant"):
                lab.test_group_policy_change(bao, fixture(), kube)
        self.assertEqual(kube.current["spec"], kube.original["spec"])
        self.assertIn(("alpha", "POST", "auth/token/revoke-self"), bao.calls)

    def test_apply_failure_after_update_still_restores_group(self):
        class UncertainApply(self.Kube):
            def apply(self, objects):
                super().apply(objects)
                if len(self.applied) == 1:
                    raise RuntimeError("apply response lost")
        kube = UncertainApply()
        with self.assertRaisesRegex(RuntimeError, "apply response lost"):
            lab.test_group_policy_change(self.Bao(), fixture(), kube)
        self.assertEqual(kube.current["spec"], kube.original["spec"])

    def test_concurrent_edit_is_not_overwritten(self):
        kube = self.Kube()
        def changed(*args):
            kube.current["spec"]["policies"] = ["another-owner"]
            raise RuntimeError("browser failed")
        with patch.object(lab, "browser_auth", side_effect=changed):
            with self.assertRaisesRegex(RuntimeError, "Group changed concurrently"):
                lab.test_group_policy_change(self.Bao(), fixture(), kube)
        self.assertEqual(kube.current["spec"]["policies"], ["another-owner"])
        self.assertEqual(len(kube.applied), 1)


class BrowserTests(unittest.TestCase):
    def test_matrix_runs_all_roles_wrong_groups_and_wrong_tenants(self):
        state = fixture()
        with patch.object(lab, "browser_login") as login, patch("builtins.print"):
            lab.test_browser(object(), state)
            self.assertEqual(login.call_count, 15)
            positives = [call for call in login.call_args_list if not call.kwargs.get("expect_denied")]
            negatives = [call for call in login.call_args_list if call.kwargs.get("expect_denied")]
            self.assertEqual({(call.args[2], call.args[3]) for call in positives}, {(t, r) for t in lab.TENANTS for r in lab.ROLES})
            self.assertEqual(len(negatives), 6)
            wrong_groups = [call for call in negatives if call.args[3] == "admin"]
            wrong_tenants = [call for call in negatives if call.args[3] == "read"]
            self.assertEqual(len(wrong_groups), 3)
            self.assertEqual(len(wrong_tenants), 3)
            for call in wrong_groups:
                self.assertEqual(call.args[4]["role"], "read")
                self.assertEqual(call.args[4]["namespace"], call.args[2])
            self.assertEqual({(call.args[2], call.args[4]["namespace"]) for call in wrong_tenants},
                             {("root", "alpha"), ("alpha", "beta"), ("beta", "root")})
            for call in wrong_tenants:
                self.assertEqual(call.args[4]["role"], "read")

    def test_browser_refuses_state_without_passwords(self):
        state = fixture()
        state["users"][0].pop("password")
        with self.assertRaises(ValueError):
            lab.test_browser(object(), state)

    def test_browser_credentials_only_pass_over_stdin(self):
        state = fixture()
        class Bao:
            calls = []
            def request(self, tenant, method, path, data=None, token=None):
                self.calls.append((path, data))
                if path.endswith("/auth_url"):
                    return 200, {"data": {"auth_url": state["issuer"] + "authorize?state=fake-state"}}
                return 200, {"auth": auth("admin")}
        bao = Bao()
        result = type("Result", (), {"returncode": 0, "stdout": json.dumps({"state": "fake-state", "code": "fake-code"})})()
        with patch.object(lab, "browser_command", return_value=["node", "browser.mjs"]), patch.object(lab.subprocess, "run", return_value=result) as run, patch.object(lab, "test_access") as access:
            lab.browser_login(bao, state, "root", "admin", state["users"][0])
            self.assertEqual(run.call_args.args[0], ["node", "browser.mjs"])
            self.assertEqual(json.loads(run.call_args.kwargs["input"])["password"], "fake-password")
            self.assertEqual(access.call_args.args[-1], auth("admin"))
            nonce = bao.calls[0][1]["client_nonce"]
            self.assertIn("client_nonce=" + nonce, bao.calls[1][0])

    def test_browser_state_mismatch_never_exchanged(self):
        state = fixture()
        class Bao:
            count = 0
            def request(self, *args, **kwargs):
                self.count += 1
                return 200, {"data": {"auth_url": state["issuer"] + "authorize?state=fake-state"}}
        bao = Bao()
        result = type("Result", (), {"returncode": 0, "stdout": json.dumps({"state": "wrong-state", "code": "fake-code"})})()
        with patch.object(lab, "browser_command", return_value=["node", "browser.mjs"]), patch.object(lab.subprocess, "run", return_value=result):
            with self.assertRaises(RuntimeError):
                lab.browser_login(bao, state, "root", "admin", state["users"][0])
        self.assertEqual(bao.count, 1)

    def test_only_allowlisted_browser_phase_diagnostics_are_visible(self):
        valid = json.dumps({"phase": "submit password", "reason": "timeout"})
        self.assertIn("submit password (timeout)", lab.browser_failure(valid))
        for stderr in ("fake-secret", '{"phase":"fake-secret","reason":"timeout"}',
                       '{"phase":"submit password","reason":"fake-secret"}',
                       '{"phase":"submit password","reason":"timeout","token":"fake-secret"}'):
            self.assertNotIn("fake-secret", lab.browser_failure(stderr))

    def test_browser_challenge_failure_is_not_a_skip_or_pass(self):
        state = fixture()
        class Bao:
            def request(self, *args, **kwargs):
                return 200, {"data": {"auth_url": state["issuer"] + "authorize?state=fake-state"}}
        result = type("Result", (), {"returncode": 1, "stderr": "untrusted fake-secret text"})()
        with patch.object(lab, "browser_command", return_value=["node", "browser.mjs"]), patch.object(lab.subprocess, "run", return_value=result):
            with self.assertRaises(RuntimeError) as error:
                lab.browser_login(Bao(), state, "root", "admin", state["users"][0])
            self.assertNotIn("fake-secret", str(error.exception))
            self.assertIn("MFA/CAPTCHA", str(error.exception))


if __name__ == "__main__":
    unittest.main()
