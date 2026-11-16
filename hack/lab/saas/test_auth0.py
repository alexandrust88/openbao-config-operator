"""Offline contracts for Auth0 lab provisioning and credential handling."""

import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
import urllib.error

import auth0


class FakeAPI:
    def __init__(self):
        self.calls = []
        self.client_id = "bootstrap-provisioner"
        self.counter = 0
        self.existing = []
        self.bindings = [{"action": {"id": "unrelated"}, "display_name": "Keep existing"}]
        self.changed_bindings = False
        self.binding_reads = 0
        self.owned = {}

    def collection(self, path, field=None):
        return self.existing

    def request(self, method, path, body=None):
        self.calls.append((method, path, body))
        if method == "GET" and path.endswith("/bindings"):
            self.binding_reads += 1
            bindings = list(self.bindings)
            if self.changed_bindings and self.binding_reads > 1:
                bindings.append({"action": {"id": "concurrent"}, "display_name": "Concurrent"})
            return {"bindings": bindings}
        if method == "GET":
            return {"status": "built", "all_changes_deployed": True}
        self.counter += 1
        result = dict(body or {})
        result.update(id=str(self.counter), client_id="client-" + str(self.counter),
                      client_secret="generated-not-real", user_id="auth0|" + str(self.counter))
        self.owned[result["id"]] = result
        return result


class ProvisionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.state_path = Path(self.temp.name) / "state.json"
        self.api = FakeAPI()

    def provision(self, state=None):
        return auth0.provision(self.api, state or {}, self.state_path, "obco-demo", "example.us.auth0.com")

    def test_create_matrix_and_preserve_binding(self):
        state = self.provision()
        self.assertEqual(state["status"], "complete")
        self.assertNotIn("machines", state)
        self.assertNotIn("audience", state)
        self.assertFalse(any("resource-servers" in path or "client-grants" in path for _, path, _ in self.api.calls))
        clients = [body for method, path, body in self.api.calls if method == "POST" and path == "/api/v2/clients"]
        self.assertEqual(len(clients), 1)
        self.assertTrue(clients[0]["is_first_party"])
        self.assertEqual(clients[0]["jwt_configuration"]["alg"], "RS256")
        self.assertEqual(clients[0]["grant_types"], ["authorization_code"])
        self.assertEqual(len(state["users"]), 9)
        self.assertEqual(self.state_path.stat().st_mode & 0o777, 0o600)
        bindings = [body for method, path, body in self.api.calls if method == "PATCH"][0]["bindings"]
        self.assertEqual(bindings[0], {"ref": {"type": "action_id", "value": "unrelated"}, "display_name": "Keep existing"})
        self.assertEqual(len(bindings), 2)
        action = [body for method, path, body in self.api.calls if method == "POST" and path.endswith("actions/actions")][0]
        self.assertEqual(action["runtime"], "node22")
        self.assertIn(state["oidc_client_id"], action["code"])
        self.assertIn("app_metadata", action["code"])
        self.assertNotIn("user_metadata", action["code"])

    def test_provisioning_scopes_do_not_manage_machine_clients_or_apis(self):
        self.assertFalse(any("resource_servers" in scope or "client_grants" in scope for scope in auth0.SCOPES))
        self.assertFalse(any("resource_servers" in scope or "client_grants" in scope for scope in auth0.DELETE_SCOPES))
        with patch.object(auth0.API, "request", return_value={"access_token": "fake"}) as request:
            auth0.API({"domain": "example.us.auth0.com", "client_id": "fake", "client_secret": "fake"})
            payload = request.call_args.args[2]
            self.assertEqual(payload["grant_type"], "client_credentials")
            self.assertTrue(payload["audience"].endswith("/api/v2/"))
            self.assertNotIn("resource_servers", payload["scope"])

    def test_legacy_cleanup_scopes_only_when_recorded(self):
        credentials = {"domain": "example.us.auth0.com", "client_id": "fake", "client_secret": "fake"}
        with patch.object(auth0.API, "request", return_value={"access_token": "fake"}) as request:
            auth0.API(credentials, cleanup=True)
            self.assertNotIn("delete:resource_servers", request.call_args.args[2]["scope"])
            auth0.API(credentials, cleanup=True, legacy_kinds={"resource-servers", "client-grants"})
            self.assertIn("delete:resource_servers", request.call_args.args[2]["scope"])
            self.assertIn("delete:client_grants", request.call_args.args[2]["scope"])

    def test_owned_test_email_domain(self):
        state = auth0.provision(self.api, {}, self.state_path, "obco-demo", "example.us.auth0.com", "test.example.org")
        self.assertTrue(all(user["username"].endswith("@test.example.org") for user in state["users"]))
        users = [body for method, path, body in self.api.calls if method == "POST" and path.endswith("/users")]
        self.assertTrue(all(not user["email_verified"] and not user["verify_email"] for user in users))

    def test_single_mailbox_plus_aliases_are_unique_and_unverified(self):
        state = auth0.provision(self.api, {}, self.state_path, "obco-demo", "example.us.auth0.com",
                                email_address="tester@example.com")
        usernames = [user["username"] for user in state["users"]]
        self.assertEqual(len(set(usernames)), 9)
        self.assertIn("tester+obco-demo-alpha-read@example.com", usernames)
        self.assertTrue(all(username.startswith("tester+obco-demo-") and username.endswith("@example.com") for username in usernames))
        users = [body for method, path, body in self.api.calls if method == "POST" and path.endswith("/users")]
        self.assertTrue(all(not user["email_verified"] and not user["verify_email"] for user in users))

    def test_email_address_rejects_ambiguous_or_invalid_alias_base(self):
        for address in ("tester+old@example.com", "tester@@example.com", "tester@example.com/path", "tester@example.com\n"):
            with self.subTest(address=address), self.assertRaises(auth0.LabError):
                auth0.validate({"domain": "example.auth0.com", "client_id": "id", "client_secret": "fake", "email_address": address}, "obco-demo")
        auth0.validate({"domain": "example.auth0.com", "client_id": "id", "client_secret": "fake", "email_address": "tester@example.com"}, "obco-demo")

    def test_new_connection_allows_management_client_to_create_users(self):
        state = self.provision()
        connections = [body for method, path, body in self.api.calls
                       if method == "POST" and path == "/api/v2/connections"]
        self.assertEqual(len(connections), 1)
        self.assertEqual(connections[0]["enabled_clients"],
                         [state["oidc_client_id"], self.api.client_id])
        self.assertFalse(any(method == "PATCH" and path.startswith("/api/v2/clients/")
                             for method, path, _ in self.api.calls))

    def test_rejected_user_create_can_clean_up_without_ambiguous_resource(self):
        request = self.api.request

        def reject_user(method, path, body=None):
            if method == "POST" and path == "/api/v2/users":
                raise auth0.APIError(method, 400)
            return request(method, path, body)

        self.api.request = reject_user
        with self.assertRaises(auth0.APIError):
            self.provision()
        state = auth0.private_json(self.state_path)
        self.assertNotIn("pending_create", state)
        self.assertEqual([item["kind"] for item in state["owned"]], ["clients", "connections"])
        auth0.cleanup(self.api, state, self.state_path)
        self.assertEqual(state["status"], "cleaned")

    def test_timeout_during_create_preserves_ambiguous_resource(self):
        request = self.api.request

        def timeout_user(method, path, body=None):
            if method == "POST" and path == "/api/v2/users":
                raise auth0.LabError("Request timed out")
            return request(method, path, body)

        self.api.request = timeout_user
        with self.assertRaises(auth0.LabError):
            self.provision()
        state = auth0.private_json(self.state_path)
        self.assertEqual(state["pending_create"]["kind"], "users")
        with self.assertRaisesRegex(auth0.LabError, "interrupted"):
            auth0.cleanup(self.api, state, self.state_path)
        self.assertEqual(state["status"], "cleanup-incomplete")

    def test_completed_rerun_does_not_write(self):
        state = self.provision()
        self.api.calls.clear()
        self.provision(state)
        self.assertTrue(all(method == "GET" for method, _, _ in self.api.calls))

    def test_partial_run_is_not_duplicated(self):
        with self.assertRaisesRegex(auth0.LabError, "partial"):
            self.provision({"status": "provisioning"})
        self.assertEqual(self.api.calls, [])

    def test_prefix_collision_is_not_adopted(self):
        self.api.existing = [{"name": "obco-demo-old"}]
        with self.assertRaisesRegex(auth0.LabError, "already exist"):
            self.provision()
        self.assertFalse(self.state_path.exists())

    def test_changed_binding_aborts_without_patch(self):
        self.api.changed_bindings = True
        with self.assertRaisesRegex(auth0.LabError, "changed"):
            self.provision()
        self.assertFalse(any(method == "PATCH" for method, _, _ in self.api.calls))
        self.assertEqual(json.loads(self.state_path.read_text())["status"], "provisioning")

    def test_binding_secrets_fail_closed(self):
        self.api.bindings[0]["secrets"] = [{"name": "private"}]
        with self.assertRaisesRegex(auth0.LabError, "secrets"):
            self.provision()
        self.assertFalse(any(method == "POST" for method, _, _ in self.api.calls))

    def test_private_file_and_symlink_checks(self):
        self.state_path.write_text('{}')
        self.state_path.chmod(0o644)
        with self.assertRaises(auth0.LabError):
            auth0.private_json(self.state_path)
        self.state_path.chmod(0o600)
        self.assertEqual(auth0.private_json(self.state_path), {})
        link = self.state_path.with_name('link')
        link.symlink_to(self.state_path)
        with self.assertRaises(OSError):
            auth0.private_json(link)

    def test_fifo_credential_is_rejected_without_blocking(self):
        os.mkfifo(self.state_path, 0o600)
        with self.assertRaises(auth0.LabError):
            auth0.private_json(self.state_path)

    def test_plan_never_connects_or_prints_secret(self):
        self.state_path.write_text(json.dumps({"domain": "example.us.auth0.com", "client_id": "id", "client_secret": "not-a-real-secret"}))
        self.state_path.chmod(0o600)
        with patch('sys.argv', ['auth0.py', '--credentials', str(self.state_path), '--state', str(self.state_path.with_name('output')), '--prefix', 'obco-demo']), patch.object(auth0, 'API') as api:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                auth0.main()
            api.assert_not_called()
            self.assertNotIn('not-a-real-secret', output.getvalue())
            self.assertFalse(self.state_path.with_name('output').exists())

    def test_http_error_never_exposes_provider_body(self):
        api = auth0.API.__new__(auth0.API)
        api.origin = "https://example.us.auth0.com"
        api.token = "management-secret"
        api.opener = Mock()
        body = io.BytesIO(b'{"message":"sensitive-provider-response"}')
        api.opener.open.side_effect = urllib.error.HTTPError(api.origin, 403, "sensitive-provider-response", {}, body)
        with self.assertRaises(auth0.LabError) as caught:
            api.request("POST", "/api/v2/clients", {"client_secret": "secret-value"})
        self.assertNotIn("sensitive-provider-response", str(caught.exception))
        self.assertNotIn("secret-value", str(caught.exception))
        self.assertTrue(body.closed)
        self.assertEqual(api.opener.open.call_count, 1)

    def test_rate_limit_headers(self):
        with patch.object(auth0.time, "time", return_value=1000):
            self.assertEqual(auth0.rate_limit_delay({"Retry-After": "4"}, 0), 4)
            self.assertEqual(auth0.rate_limit_delay({"X-RateLimit-Reset": "1007"}, 0), 7)
            self.assertEqual(auth0.rate_limit_delay({"Retry-After": "Thu, 01 Jan 1970 00:16:45 GMT"}, 0), 5)
            for value in ("invalid", "nan", "inf", "-5"):
                self.assertEqual(auth0.rate_limit_delay({"Retry-After": value}, 1), 2)

    def test_rate_limit_retries_explicit_rejection_then_succeeds(self):
        api = auth0.API.__new__(auth0.API)
        api.origin, api.token, api.opener = "https://example.us.auth0.com", "secret", Mock()
        rejected = io.BytesIO(b"private-error")
        api.opener.open.side_effect = [
            urllib.error.HTTPError(api.origin, 429, "private", {"Retry-After": "3"}, rejected),
            io.BytesIO(b'{"id":"created-once"}'),
        ]
        with patch.object(auth0.time, "sleep") as sleep:
            self.assertEqual(api.request("POST", "/api/v2/clients", {}), {"id": "created-once"})
            sleep.assert_called_once_with(3)
        self.assertTrue(rejected.closed)
        self.assertEqual(api.opener.open.call_count, 2)

    def test_rate_limit_is_bounded_and_ambiguous_failures_never_retry(self):
        for failure, calls in ((429, 7), (500, 1), ("network", 1), ("long-reset", 1)):
            with self.subTest(failure=failure):
                api = auth0.API.__new__(auth0.API)
                api.origin, api.token, api.opener = "https://example.us.auth0.com", "secret", Mock()
                if failure == "network":
                    error = urllib.error.URLError("private-network-detail")
                else:
                    headers = {"Retry-After": "3600"} if failure == "long-reset" else {}
                    error = urllib.error.HTTPError(api.origin, 429 if failure == "long-reset" else failure, "private", headers, None)
                api.opener.open.side_effect = error
                with patch.object(auth0.time, "sleep"), self.assertRaises(auth0.LabError):
                    api.request("POST", "/api/v2/clients", {})
                self.assertEqual(api.opener.open.call_count, calls)

    def test_rate_limit_deadline_stops_before_another_wait(self):
        api = auth0.API.__new__(auth0.API)
        api.origin, api.token, api.opener = "https://example.us.auth0.com", "secret", Mock()
        api.opener.open.side_effect = urllib.error.HTTPError(api.origin, 429, "private", {}, None)
        with patch.object(auth0.time, "monotonic", side_effect=[0, 0, 119.5]), patch.object(auth0.time, "sleep") as sleep:
            with self.assertRaises(auth0.APIError):
                api.request("DELETE", "/api/v2/clients/id")
            sleep.assert_not_called()

    def test_plan_needs_no_credentials(self):
        with patch('sys.argv', ['auth0.py', '--state', str(self.state_path), '--prefix', 'obco-demo']), patch.object(auth0, 'API') as api:
            with contextlib.redirect_stdout(io.StringIO()):
                auth0.main()
            api.assert_not_called()
            self.assertFalse(self.state_path.exists())

    def test_cleanup_owned_ids_reverse_order_and_keep_other_actions(self):
        state = self.provision()
        action = next(item for item in state["owned"] if item["kind"] == "actions/actions")
        self.api.bindings.append({"action": {"id": action["id"]}, "display_name": "Own lab"})
        self.api.calls.clear()
        auth0.cleanup(self.api, state, self.state_path)
        deletes = [path for method, path, _ in self.api.calls if method == "DELETE"]
        self.assertEqual(deletes, [item["path"] for item in reversed(state["owned"])])
        bindings = next(body["bindings"] for method, _, body in self.api.calls if method == "PATCH")
        self.assertEqual(bindings, [{"ref": {"type": "action_id", "value": "unrelated"}, "display_name": "Keep existing"}])
        self.assertEqual(state["status"], "cleaned")
        self.assertTrue(all(item["deleted"] for item in state["owned"]))
        self.api.calls.clear()
        auth0.cleanup(self.api, state, self.state_path)
        self.assertEqual(self.api.calls, [])

    def test_cleanup_404_is_idempotent_but_other_errors_stop(self):
        state = self.provision()
        original = self.api.request
        def missing(method, path, body=None):
            if method == "DELETE":
                raise auth0.APIError(method, 404)
            return original(method, path, body)
        self.api.request = missing
        auth0.cleanup(self.api, state, self.state_path)
        self.assertEqual(state["status"], "cleaned")
        state = {"owned": [{"kind": "clients", "id": "owned", "path": "/api/v2/clients/owned"}]}
        self.api.request = Mock(side_effect=auth0.APIError("DELETE", 403))
        with self.assertRaises(auth0.APIError):
            auth0.cleanup(self.api, state, self.state_path)
        self.assertFalse(state["owned"][0].get("deleted"))

    def test_cleanup_rejects_path_injection(self):
        state = {"owned": [{"kind": "clients", "id": "owned", "path": "/api/v2/clients/unrelated"}]}
        with self.assertRaisesRegex(auth0.LabError, "does not match"):
            auth0.cleanup(self.api, state, self.state_path)
        self.assertEqual(self.api.calls, [])

    def test_cleanup_partial_run_reports_unknown_create(self):
        state = {"owned": [{"kind": "clients", "id": "owned", "path": "/api/v2/clients/owned"}],
                 "pending_create": {"kind": "users", "name": "obco-demo-root-admin"}}
        with self.assertRaisesRegex(auth0.LabError, "interrupted create"):
            auth0.cleanup(self.api, state, self.state_path)
        self.assertEqual(state["status"], "cleanup-incomplete")
        self.assertTrue(state["owned"][0]["deleted"])

    def test_cleanup_binding_race_does_not_delete_resources(self):
        state = self.provision()
        action = next(item for item in state["owned"] if item["kind"] == "actions/actions")
        self.api.bindings.append({"action": {"id": action["id"]}, "display_name": "Own lab"})
        self.api.changed_bindings = True
        self.api.binding_reads = 0
        self.api.calls.clear()
        with self.assertRaisesRegex(auth0.LabError, "changed"):
            auth0.cleanup(self.api, state, self.state_path)
        self.assertFalse(any(method in ("DELETE", "PATCH") for method, _, _ in self.api.calls))

    def test_redirect_rejected(self):
        with self.assertRaises(auth0.LabError):
            auth0.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://attacker.invalid')

    def test_domain_cannot_redirect_credentials(self):
        for domain in ('example.us.auth0.com/elsewhere', 'example.us.auth0.com@attacker.invalid', 'http://example.us.auth0.com'):
            with self.subTest(domain=domain), self.assertRaises(auth0.LabError):
                auth0.validate({'domain': domain, 'client_id': 'id', 'client_secret': 'secret'}, 'obco-demo')


if __name__ == '__main__':
    unittest.main()
