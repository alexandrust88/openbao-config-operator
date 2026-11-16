"""Check provider outputs against the shared CRD and CI-state consumers."""
import json
from pathlib import Path
import tempfile
import unittest

import auth0
import okta
import operator_lab
from test_auth0 import FakeAPI as Auth0API
from test_okta import FakeAPI as OktaAPI


class ProviderContractTests(unittest.TestCase):
    def test_provisioned_state_renders_human_oidc_claim_arrays(self):
        with tempfile.TemporaryDirectory() as directory:
            states = [auth0.provision(Auth0API(), {}, Path(directory) / "auth0.json",
                                      "obco-test", "example.us.auth0.com"),
                      okta.provision(OktaAPI(), {"email_domain": "example.test"},
                                     "obco-test", Path(directory) / "okta.json")]
            for state in states:
                with self.subTest(provider=state["provider"]):
                    operator_lab.validate_state(state)
                    objects = operator_lab.render(state)
                    roles = [item["spec"] for item in objects if item["kind"] == "JWTAuthRole"]
                    self.assertEqual(len(roles), 9)
                    self.assertTrue(all(role["roleType"] == "oidc" for role in roles))
                    self.assertNotIn("machines", state)
                    for role in roles:
                        self.assertTrue(all(isinstance(value, list) and value
                                            for value in role["boundClaims"].values()))
                    for user in state["users"]:
                        self.assertNotIn(user["password"], json.dumps(objects))
                    public = [item for item in objects if item["kind"] != "Secret"]
                    self.assertNotIn(state["oidc_client_secret"], json.dumps(public))
