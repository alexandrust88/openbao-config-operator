"""Offline contracts for the Okta provisioning plan and secret boundaries."""

import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

import okta


class FakeAPI:
    domain = "https://test.okta.com"

    def __init__(self):
        self.calls = []
        self.next_id = 0
        self.fail_path = None
        self.policy = {"type": "ACCESS_POLICY", "status": "ACTIVE"}

    def call(self, method, path, data=None):
        self.calls.append((method, path, data))
        if self.fail_path and self.fail_path in path:
            raise okta.LabError("Simulated failure")
        if method == "GET":
            if path.startswith("/api/v1/policies/"):
                return self.policy
            return []
        if method == "PUT":
            return {}
        self.next_id += 1
        result = {"id": "id" + str(self.next_id)}
        if path == "/api/v1/apps":
            result["credentials"] = {"oauthClient": {
                "client_id": result["id"], "client_secret": "private-app-secret"}}
        return result


class OktaTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "state.json"
        self.api = FakeAPI()

    def apply(self):
        return okta.provision(self.api, {"email_domain": "example.test"}, "obco-test", self.path)

    def test_plan_needs_no_credentials_or_network(self):
        with patch("sys.argv", ["okta.py", "--state", str(self.path), "--prefix", "obco-test"]):
            with patch.object(okta.API, "call", side_effect=AssertionError("network")):
                with contextlib.redirect_stdout(io.StringIO()) as output:
                    okta.main()
        self.assertIn("nine users", output.getvalue())
        self.assertFalse(self.path.exists())

    def test_assigns_existing_app_policy_only_to_new_web_app(self):
        state = okta.provision(self.api, {"email_domain": "example.test", "app_sign_on_policy_id": "policy123"},
                               "obco-test", self.path)
        assignments = [(path, data) for method, path, data in self.api.calls
                       if method == "PUT" and "/policies/" in path]
        self.assertEqual(len(assignments), 1)
        self.assertEqual(assignments[0], ("/api/v1/apps/" + state["oidc_client_id"] + "/policies/policy123", None))
        self.assertFalse(any(item["path"].startswith("/api/v1/policies/") for item in state["resources"]))
        self.assertFalse(any(method != "GET" and path == "/api/v1/policies/policy123"
                             for method, path, _ in self.api.calls))

    def test_rejects_wrong_or_inactive_app_policy_before_creating_resources(self):
        for policy in ({"type": "OKTA_SIGN_ON", "status": "ACTIVE"},
                       {"type": "ACCESS_POLICY", "status": "INACTIVE"}):
            with self.subTest(policy=policy):
                self.api.policy = policy
                with self.assertRaisesRegex(okta.LabError, "ACTIVE ACCESS_POLICY"):
                    okta.provision(self.api, {"email_domain": "example.test", "app_sign_on_policy_id": "policy123"},
                                   "obco-test", self.path)
        self.assertFalse(any(method != "GET" for method, _, _ in self.api.calls))
        self.assertFalse(self.path.exists())

    def test_plus_aliases_share_one_inbox_and_are_unique(self):
        state = okta.provision(self.api, {"email_address": "tester@example.com", "email_domain": "ignored.test"},
                               "obco-test", self.path)
        users = state["users"]
        self.assertEqual(len({user["username"] for user in users}), 9)
        for user in users:
            self.assertEqual(user["username"], f'tester+obco-test-{user["namespace"]}-{user["role"]}@example.com')
        payloads = [body for method, path, body in self.api.calls if method == "POST" and path.startswith("/api/v1/users?")]
        self.assertTrue(all(item["profile"]["email"] == item["profile"]["login"] for item in payloads))
        self.assertTrue(all(item["credentials"]["password"]["value"] for item in payloads))

    def test_invalid_or_missing_mailbox_fails_before_provider_calls(self):
        for credentials in ({}, {"email_address": "tester+old@example.com"},
                            {"email_address": "tester@example.com/path"},
                            {"email_address": "a" * 64 + "@example.com"}):
            with self.subTest(credentials=credentials), self.assertRaises(okta.LabError):
                okta.provision(self.api, credentials, "obco-test", self.path)
        self.assertEqual(self.api.calls, [])
        self.assertFalse(self.path.exists())

    def test_matrix_and_private_state(self):
        state = self.apply()
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(len(state["users"]), 9)
        self.assertNotIn("machines", state)
        self.assertTrue(state["complete"])
        self.assertEqual(state["callback_uri"], "http://127.0.0.1:8250/oidc/callback")
        self.assertEqual({(x["namespace"], x["role"]) for x in state["users"]},
                         {(n, r) for n in okta.NAMESPACES for r in okta.ROLES})
        apps = [body for method, path, body in self.api.calls if method == "POST" and path == "/api/v1/apps"]
        self.assertEqual(len(apps), 1)
        self.assertEqual(apps[0]["settings"]["oauthClient"]["grant_types"], ["authorization_code"])
        self.assertFalse(any(path.endswith("/scopes") for _, path, _ in self.api.calls))

    def test_only_specific_clients_and_group_prefix_are_authorized(self):
        self.apply()
        claims = [data for method, path, data in self.api.calls if path.endswith("/claims")]
        self.assertEqual(claims[0]["value"], "obco-test-")
        policies = [data for method, path, data in self.api.calls if path.endswith("/policies")]
        self.assertTrue(all("ALL_CLIENTS" not in data["conditions"]["clients"]["include"] for data in policies))
        rules = [data for method, path, data in self.api.calls if path.endswith("/rules")]
        self.assertEqual(len(rules), 1)
        human_rule = next(r for r in rules if r["name"].endswith("humans"))
        self.assertEqual(len(human_rule["conditions"]["people"]["groups"]["include"]), 9)
        self.assertEqual(human_rule["conditions"]["grantTypes"]["include"], ["authorization_code"])

    def test_rerun_only_reads_recorded_ids(self):
        state = self.apply()
        self.api.calls = []
        self.assertEqual(self.apply(), state)
        self.assertTrue(all(method == "GET" for method, _, _ in self.api.calls))
        self.assertEqual(len(self.api.calls), len(state["resources"]))

    def test_partial_run_refuses_replay(self):
        self.api.fail_path = "/claims"
        with self.assertRaises(okta.LabError):
            self.apply()
        state = okta.private_json(self.path)
        self.assertFalse(state["complete"])
        self.assertIn("pending", state)
        self.api.fail_path = None
        self.api.calls = []
        with self.assertRaisesRegex(okta.LabError, "Incomplete"):
            self.apply()
        self.assertEqual(self.api.calls, [])

    def test_rejected_create_clears_pending_and_cleanup_removes_known_ids(self):
        call = self.api.call

        def reject_user(method, path, data=None):
            if method == "POST" and path.startswith("/api/v1/users?"):
                raise okta.APIError(method, 400)
            return call(method, path, data)

        self.api.call = reject_user
        with self.assertRaises(okta.APIError):
            self.apply()
        state = okta.private_json(self.path)
        self.assertNotIn("pending", state)
        self.assertGreater(len(state["resources"]), 0)
        self.api.call = lambda method, path, data=None: {"status": "INACTIVE"}
        okta.cleanup(self.api, "obco-test", self.path)
        self.assertTrue(okta.private_json(self.path)["cleanup_complete"])

    def test_request_timeout_keeps_ambiguous_create(self):
        self.api.fail_path = "/claims"
        with self.assertRaises(okta.LabError):
            self.apply()
        state = okta.private_json(self.path)
        self.assertTrue(state["pending"]["path"].endswith("/claims"))
        self.api.call = lambda method, path, data=None: {"status": "INACTIVE"}
        with self.assertRaisesRegex(okta.LabError, "ambiguous"):
            okta.cleanup(self.api, "obco-test", self.path)
        self.assertFalse(okta.private_json(self.path)["cleanup_complete"])

    def test_state_from_other_org_is_rejected(self):
        self.apply()
        self.api.domain = "https://another.okta.com"
        with self.assertRaisesRegex(okta.LabError, "different"):
            self.apply()

    def test_unsafe_private_file_is_rejected(self):
        self.path.write_text('{}')
        self.path.chmod(0o644)
        with self.assertRaises(okta.LabError):
            okta.private_json(self.path)
        self.path.chmod(0o600)
        link = self.path.with_name("link.json")
        link.symlink_to(self.path)
        with self.assertRaises(OSError):
            okta.private_json(link)

    def test_domain_rejects_credentials_paths_and_http(self):
        for value in ("http://test.okta.com", "https://user:secret@test.okta.com", "https://test.okta.com/path",
                      "https://test.okta.com?x=secret", "https://test.okta.com:443"):
            with self.subTest(value=value), self.assertRaises(okta.LabError):
                okta.domain_url(value)

    def test_concurrent_apply_does_not_reach_provider(self):
        creds = self.path.with_name("credentials.json")
        okta.save(creds, {"domain": "test.okta.com", "api_token": "fake-test-token", "email_domain": "example.test"})
        lock = self.path.with_suffix(".json.lock")
        lock.touch(mode=0o600)
        with patch("sys.argv", ["okta.py", "--credentials", str(creds), "--state", str(self.path),
                                "--prefix", "obco-test", "--apply"]):
            with patch.object(okta, "API") as api, self.assertRaises(FileExistsError):
                okta.main()
            api.assert_not_called()
        self.assertTrue(lock.exists())

    def cleanup_state(self, paths, pending=False):
        state = {"provider": "okta", "domain": self.api.domain, "prefix": "obco-test",
                 "resources": [{"path": p} for p in paths], "complete": True,
                 "users": [{"password": "private"}],
                 "oidc_client_secret": "private"}
        if pending:
            state["pending"] = {"method": "POST", "path": "/api/v1/apps"}
        okta.save(self.path, state)

    def test_cleanup_reverse_order_and_lifecycle(self):
        paths = ["/api/v1/authorizationServers/as1", "/api/v1/groups/g1",
                 "/api/v1/users/u1", "/api/v1/apps/a1"]
        self.cleanup_state(paths)
        calls = []
        statuses = {paths[0]: "ACTIVE", paths[2]: "ACTIVE", paths[3]: "ACTIVE"}

        def call(method, path, data=None):
            calls.append((method, path))
            if method == "POST":
                base = path.split("/lifecycle/")[0]
                statuses[base] = "DEPROVISIONED" if "/users/" in base else "INACTIVE"
            return {"status": statuses.get(path, "ACTIVE")}

        self.api.call = call
        okta.cleanup(self.api, "obco-test", self.path)
        self.assertEqual([p for method, p in calls if method == "DELETE"], list(reversed(paths)))
        self.assertIn(("POST", paths[2] + "/lifecycle/deactivate?sendEmail=false"), calls)
        self.assertIn(("POST", paths[3] + "/lifecycle/deactivate"), calls)
        state = okta.private_json(self.path)
        self.assertEqual(state["resources"], [])
        self.assertTrue(state["cleanup_complete"])
        self.assertNotIn("private", self.path.read_text())

    def test_cleanup_tolerates_404_and_keeps_other_failures(self):
        paths = ["/api/v1/apps/a1", "/api/v1/apps/a2"]
        self.cleanup_state(paths)

        def call(method, path, data=None):
            raise okta.APIError(method, 404 if path == paths[1] else 403)

        self.api.call = call
        with self.assertRaises(okta.APIError):
            okta.cleanup(self.api, "obco-test", self.path)
        self.assertEqual(okta.private_json(self.path)["resources"], [{"path": paths[0]}])

    def test_cleanup_reports_ambiguous_creation(self):
        self.cleanup_state([], pending=True)
        with self.assertRaisesRegex(okta.LabError, "ambiguous"):
            okta.cleanup(self.api, "obco-test", self.path)
        self.assertFalse(okta.private_json(self.path)["cleanup_complete"])

    def test_cleanup_refuses_unexpected_and_default_paths(self):
        for path in ("/api/v1/users", "/api/v1/authorizationServers/default", "/api/v1/apps/a1/../a2"):
            with self.subTest(path=path):
                self.cleanup_state([path])
                with self.assertRaisesRegex(okta.LabError, "unexpected"):
                    okta.cleanup(self.api, "obco-test", self.path)
        self.assertEqual(self.api.calls, [])

    def test_cleanup_rejects_wrong_org_and_is_idempotent(self):
        self.cleanup_state([])
        with self.assertRaisesRegex(okta.LabError, "different"):
            okta.cleanup(self.api, "other-prefix", self.path)
        okta.cleanup(self.api, "obco-test", self.path)
        okta.cleanup(self.api, "obco-test", self.path)
        self.assertEqual(self.api.calls, [])

    def test_redirect_rejected(self):
        with self.assertRaises(okta.LabError):
            okta.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.test/")

    def test_http_error_does_not_expose_response_or_token(self):
        api = okta.API("test.okta.com", "super-secret-token")
        with patch.object(api.opener, "open", side_effect=urllib.error.HTTPError(
                "https://test.okta.com", 400, "private-response", {}, io.BytesIO(b"private-response"))):
            with self.assertRaises(okta.LabError) as error:
                api.call("POST", "/api/v1/apps", {"secret": "private-body"})
        self.assertNotIn("private", str(error.exception))
        self.assertNotIn("super-secret", str(error.exception))


if __name__ == "__main__":
    unittest.main()
