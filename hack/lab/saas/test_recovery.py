"""Recovery artifacts must retain cleanup identities without retaining credentials."""
import json
from pathlib import Path
import tempfile
import unittest

import auth0
import okta
import recovery
from test_auth0 import FakeAPI as Auth0API
from test_okta import FakeAPI as OktaAPI


class RecoveryTests(unittest.TestCase):
    def test_provider_state_exports_no_credentials_or_usernames(self):
        with tempfile.TemporaryDirectory() as directory:
            states = [auth0.provision(Auth0API(), {}, Path(directory) / "auth0.json", "obco-test", "example.us.auth0.com"),
                      okta.provision(OktaAPI(), {"email_domain": "example.test"}, "obco-test", Path(directory) / "okta.json")]
            for state in states:
                with self.subTest(provider=state["provider"]):
                    safe = recovery.inventory(state)
                    text = json.dumps(safe)
                    for user in state["users"]:
                        self.assertNotIn(user["password"], text)
                        self.assertNotIn(user["username"], text)
                    self.assertNotIn(state["oidc_client_secret"], text)
                    self.assertEqual(safe["prefix"], state["prefix"])
                    self.assertTrue(safe.get("owned") or safe.get("resources"))

    def test_arbitrary_paths_and_default_server_rejected(self):
        for path in ("/api/v1/users", "/api/v1/authorizationServers/default", "/api/v1/apps/id?secret=bad"):
            with self.assertRaises(ValueError):
                recovery.inventory({"provider": "okta", "domain": "https://example.okta.com", "prefix": "obco-test", "resources": [{"path": path}]})

    def test_ambiguous_create_retained_without_original_payload(self):
        state = {"provider": "auth0", "domain": "example.auth0.com", "prefix": "obco-test",
                 "owned": [], "pending_create": {"password": "secret", "name": "user@example.test"}}
        safe = recovery.inventory(state)
        self.assertTrue(safe["pending_create"])
        self.assertNotIn("secret", json.dumps(safe))
        self.assertNotIn("user@", json.dumps(safe))
