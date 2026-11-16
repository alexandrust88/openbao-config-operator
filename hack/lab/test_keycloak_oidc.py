"""Contracts for the account-free Keycloak group regression."""
import unittest
import json
from unittest.mock import patch

import identity
import keycloak_oidc


def credentials():
    roles = identity.ROLES + identity.SHOWCASE_ROLES
    values = {f"{tenant}-{role}": "fixture-password" for tenant in identity.TENANTS for role in roles}
    values.update({"machine-" + tenant: "fixture-machine" for tenant in identity.TENANTS})
    values["client-secret"] = "fixture-client"
    return values


class KeycloakOIDCTests(unittest.TestCase):
    def test_additive_realm_preserves_legacy_users_and_adds_showcase_groups(self):
        realm = identity.realm(credentials())
        usernames = {user["username"] for user in realm["users"]}
        self.assertTrue({f"{tenant}-{role}" for tenant in identity.TENANTS for role in identity.ROLES + identity.SHOWCASE_ROLES}.issubset(usernames))
        self.assertEqual(len(realm["groups"]), 15)
        client = next(client for client in realm["clients"] if client["clientId"] == "openbao")
        self.assertEqual(set(client["redirectUris"]), {identity.CALLBACK, keycloak_oidc.lab.CALLBACK})
        self.assertTrue(client["directAccessGrantsEnabled"])

    def test_up_rolls_realm_only_when_content_changes(self):
        def rollout_digest(values):
            with patch.object(identity, "credentials", return_value=values), patch.object(identity, "apply"), patch.object(identity, "wait_ready"), patch.object(identity, "kube", return_value="secret/identity-users") as kube, patch("builtins.print"):
                identity.up()
            calls = [call.args for call in kube.call_args_list]
            patch_index = next(i for i, args in enumerate(calls) if "patch" in args)
            rollout_index = next(i for i, args in enumerate(calls) if "rollout" in args)
            self.assertLess(patch_index, rollout_index)
            body = json.loads(calls[patch_index][-1])
            return body["spec"]["template"]["metadata"]["annotations"]["openbaoconfig.io/realm-sha256"]
        values = credentials()
        first = rollout_digest(values)
        self.assertEqual(len(first), 64)
        self.assertEqual(first, rollout_digest(dict(reversed(list(values.items())))))
        self.assertNotEqual(first, rollout_digest({**values, "alpha-admin": "changed-fixture"}))
        original_realm = identity.realm
        def additional_callback(values):
            result = original_realm(values)
            result["clients"][0]["redirectUris"].append("http://localhost:8251/callback")
            return result
        with patch.object(identity, "realm", side_effect=additional_callback):
            self.assertNotEqual(first, rollout_digest(values))

    def test_state_contains_only_nine_showcase_users(self):
        state = keycloak_oidc.state_from_credentials(credentials())
        self.assertEqual(len(state["users"]), 9)
        self.assertEqual(state["issuer"], identity.ISSUER)
        self.assertEqual(state["oidc_client_id"], "openbao")
        self.assertEqual({user["role"] for user in state["users"]}, {"admin", "read", "ops"})
        self.assertNotIn("machines", state)

    def test_keycloak_render_is_separate_from_legacy_namespaces(self):
        state = keycloak_oidc.state_from_credentials(credentials())
        objects = keycloak_oidc.lab.render(state)
        namespaces = {obj["spec"]["namespace"] for obj in objects if obj.get("spec", {}).get("namespace")}
        self.assertEqual(namespaces, {"saas-keycloak-alpha", "saas-keycloak-beta"})
        configs = [obj["spec"] for obj in objects if obj["kind"] == "JWTAuthConfig"]
        self.assertTrue(all(config["oidcClientSecretSecretRef"]["name"] == "saas-keycloak-oidc" for config in configs))

    def test_local_mode_cannot_weaken_saas_https_validation(self):
        state = keycloak_oidc.state_from_credentials(credentials())
        for changes in ({"provider": "okta"}, {"issuer": "http://untrusted.test"}, {"issuer": "https://untrusted.test"}):
            with self.assertRaises(ValueError):
                keycloak_oidc.lab.validate_state({**state, **changes})


if __name__ == "__main__":
    unittest.main()
