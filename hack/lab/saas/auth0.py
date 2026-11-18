#!/usr/bin/env python3
"""Provision isolated Auth0 resources for the OpenBao identity lab."""

import argparse
from email.utils import parsedate_to_datetime
import math
import json
import os
from pathlib import Path
import re
import secrets
import stat
import string
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

NAMESPACES = ("root", "alpha", "beta")
ROLES = ("admin", "read", "ops")
CALLBACK = "http://127.0.0.1:8250/oidc/callback"
GROUPS_CLAIM = "https://obco.dev/groups"
SCOPES = (
    "read:clients", "create:clients", "read:client_keys",
    "read:connections", "create:connections", "read:users", "create:users",
    "read:actions", "create:actions", "update:actions",
)


class LabError(Exception):
    """An error safe to print without provider responses or credentials."""


DELETE_SCOPES = ("delete:clients", "delete:connections", "delete:users", "delete:actions")


class APIError(LabError):
    def __init__(self, method, status, resource="request", category=None):
        detail = f"; category={category}" if category else ""
        super().__init__(f"Auth0 {method} {resource} failed (HTTP {status}{detail}); inspect the Auth0 dashboard")
        self.status = status


def resource_label(path):
    """Return a fixed API stage label, never a resource ID, query or user identity."""
    path = urllib.parse.urlsplit(path).path
    if re.fullmatch(r"/api/v2/actions/actions/[^/]+/deploy", path):
        return "action-deployment"
    routes = {"/oauth/token": "management-token", "/api/v2/actions/triggers/post-login/bindings": "post-login-bindings",
              "/api/v2/clients": "clients", "/api/v2/connections": "connections", "/api/v2/users": "users",
              "/api/v2/actions/actions": "actions", "/api/v2/resource-servers": "legacy-resource-servers",
              "/api/v2/client-grants": "legacy-client-grants"}
    for route, label in routes.items():
        if path == route or path.startswith(route + "/"):
            return label
    return "request"


def error_category(error):
    """Recognise only fixed categories from a bounded provider error; discard all free text."""
    try:
        raw = error.read(4097)
        value = json.loads(raw) if len(raw) <= 4096 else None
    except (OSError, ValueError, TypeError):
        return None
    if not isinstance(value, dict):
        return None
    message = value.get("message")
    if value.get("name") == "PasswordStrengthError" or (
            isinstance(message, str) and message.startswith("PasswordStrengthError:")):
        return "password-strength"
    allowed = {"invalid_password", "password_strength_error", "auth0_idp_error", "invalid_body",
               "invalid_query_string", "invalid_token", "insufficient_scope", "too_many_requests",
               "invalid_client", "invalid_grant", "unauthorized_client", "access_denied"}
    for field in ("errorCode", "code", "error"):
        code = value.get(field)
        if isinstance(code, str) and code in allowed:
            return code
    return None


def generated_password():
    """Generate 48 characters satisfying the configured Auth0 excellent password policy."""
    groups = (string.ascii_uppercase, string.ascii_lowercase, string.digits, "!@#$%")
    characters = [secrets.choice(group) for group in groups]
    alphabet = string.ascii_letters + string.digits + "-_!@#$%"
    for _ in range(44):
        choices = alphabet.replace(characters[-1], "") if characters[-1] == characters[-2] else alphabet
        characters.append(secrets.choice(choices))
    return "".join(characters)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise LabError("Auth0 returned a redirect; credentials were not forwarded")


def private_json(path):
    path = Path(path)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd) as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
            raise LabError("Credential and state files must be regular files with mode 0600")
        return json.load(stream)


def save(path, value):
    path = Path(path)
    fd, temporary = tempfile.mkstemp(prefix=".auth0-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def validate_prefix(prefix):
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,30}", prefix):
        raise LabError("Prefix must contain 3–31 lowercase letters, digits or hyphens")


def validate(credentials, prefix):
    validate_prefix(prefix)
    domain = credentials.get("domain", "")
    if not re.fullmatch(r"[a-z0-9][a-z0-9.-]*\.auth0\.com", domain):
        raise LabError("Use the native Auth0 tenant hostname, without a scheme or path")
    for key in ("client_id", "client_secret"):
        if not isinstance(credentials.get(key), str) or not credentials[key]:
            raise LabError("Management API client_id and client_secret are required")
    email_domain = credentials.get("email_domain", "example.invalid")
    if not isinstance(email_domain, str) or not re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}", email_domain):
        raise LabError("email_domain must be a lowercase test hostname")
    email_address = credentials.get("email_address")
    if email_address is not None:
        if not isinstance(email_address, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*@[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}", email_address):
            raise LabError("email_address must be a mailbox without an existing plus suffix")
    return domain


def rate_limit_delay(headers, attempt):
    """Respect provider reset hints without accepting unbounded waits."""
    delay = min(2 ** attempt, 30)
    now = time.time()
    for name in ("Retry-After", "X-RateLimit-Reset"):
        value = headers.get(name) if headers else None
        if not value:
            continue
        try:
            seconds = float(value)
            if name == "X-RateLimit-Reset":
                seconds -= now
        except (ValueError, TypeError):
            if name != "Retry-After":
                continue
            try:
                seconds = parsedate_to_datetime(value).timestamp() - now
            except (ValueError, TypeError, OverflowError):
                continue
        if math.isfinite(seconds):
            delay = max(delay, seconds)
    return delay


class API:
    def __init__(self, credentials, cleanup=False, legacy_kinds=()):
        self.origin = "https://" + credentials["domain"]
        self.client_id = credentials["client_id"]
        self.opener = urllib.request.build_opener(NoRedirect())
        self.token = None
        scopes = SCOPES + (DELETE_SCOPES if cleanup else ())
        if cleanup:
            scopes += tuple("delete:" + kind.replace("-", "_") for kind in
                            ("resource-servers", "client-grants") if kind in legacy_kinds)
        result = self.request("POST", "/oauth/token", {
            "grant_type": "client_credentials", "client_id": credentials["client_id"],
            "client_secret": credentials["client_secret"],
            "audience": self.origin + "/api/v2/", "scope": " ".join(scopes),
        })
        self.token = result["access_token"]

    def request(self, method, path, payload=None):
        if not path.startswith("/") or path.startswith("//"):
            raise LabError("Invalid Auth0 API path")
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        body = json.dumps(payload).encode() if payload is not None else None
        request = urllib.request.Request(self.origin + path, body, headers, method=method)
        deadline = time.monotonic() + 120
        for attempt in range(7):
            try:
                with self.opener.open(request, timeout=min(30, max(0.1, deadline - time.monotonic()))) as response:
                    raw = response.read()
                    return json.loads(raw) if raw else {}
            except urllib.error.HTTPError as error:
                status = error.code
                delay = rate_limit_delay(error.headers, attempt)
                try:
                    category = error_category(error)
                finally:
                    error.close()
                # An explicit rejection is safe to retry, including POST. Network
                # failures are ambiguous and must never repeat a resource creation.
                if status != 429 or attempt == 6 or delay >= deadline - time.monotonic():
                    raise APIError(method, status, resource_label(path), category) from None
                time.sleep(delay)
            except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
                raise LabError(f"Auth0 {method} {resource_label(path)} failed; inspect connectivity and the Auth0 dashboard") from None

    def collection(self, path, field=None):
        result = []
        for page in range(100):
            response = self.request("GET", f"/api/v2/{path}?per_page=100&page={page}")
            items = response[field] if field else response
            if not isinstance(items, list):
                raise LabError("Unexpected Auth0 collection response")
            result.extend(items)
            if len(items) < 100:
                return result
        raise LabError("Auth0 pagination limit exceeded")


def bindings_payload(bindings):
    result = []
    for binding in bindings:
        if binding.get("secrets"):
            raise LabError("Existing Action binding has secrets; bind the lab Action manually to preserve them")
        action_id = binding.get("action", {}).get("id")
        if not action_id:
            raise LabError("Unknown Action binding shape; refusing to overwrite the login flow")
        result.append({"ref": {"type": "action_id", "value": action_id},
                       "display_name": binding["display_name"]})
    return result


def action_code(client_id, prefix):
    return """exports.onExecutePostLogin = async (event, api) => {
  if (event.client.client_id !== %s) return;
  const groups = event.user.app_metadata?.openbao_lab_groups;
  if (!Array.isArray(groups)) return;
  const allowed = groups.filter(group => typeof group === 'string' && group.startsWith(%s));
  api.idToken.setCustomClaim(%s, allowed);
};
""" % (json.dumps(client_id), json.dumps(prefix + "-"), json.dumps(GROUPS_CLAIM))


def provision(api, state, path, prefix, domain, email_domain="example.invalid", email_address=None):
    if state.get("status") == "complete":
        # A completed run is read-only: do not rotate credentials or change account policy.
        if any(item.get("kind") in ("resource-servers", "client-grants") for item in state["owned"]):
            raise LabError("State includes legacy machine resources; clean up this run before creating a human OIDC lab")
        for item in state["owned"]:
            api.request("GET", item["path"])
        return state
    if state:
        raise LabError("A partial state exists; inspect recorded IDs before manually removing this dedicated lab")
    existing = []
    for endpoint, field in (("clients", None), ("connections", None), ("actions/actions", "actions")):
        existing.extend(api.collection(endpoint, field))
    if any(item.get("name", "").startswith(prefix + "-") for item in existing):
        raise LabError("Resources with this prefix already exist; refusing to adopt or duplicate them")
    bindings = api.request("GET", "/api/v2/actions/triggers/post-login/bindings")["bindings"]
    bindings_payload(bindings)
    state.update(provider="auth0", prefix=prefix, domain=domain, issuer="https://" + domain + "/",
                 groups_claim=GROUPS_CLAIM, callback_uri=CALLBACK,
                 status="provisioning", email_domain=email_domain, owned=[], users=[])
    save(path, state)

    def create(endpoint, body, id_key="id"):
        state["pending_create"] = {"kind": endpoint, "name": body.get("name", body.get("email", endpoint))}
        save(path, state)
        try:
            response = api.request("POST", "/api/v2/" + endpoint, body)
        except APIError as error:
            if error.status in (400, 401, 403, 404, 422, 429):
                state.pop("pending_create", None)
                save(path, state)
            raise
        state["owned"].append({"kind": endpoint, "id": response[id_key], "path": "/api/v2/" + endpoint + "/" + urllib.parse.quote(response[id_key], safe=""),
                               "name": body.get("name", body.get("email", endpoint))})
        state.pop("pending_create", None)
        save(path, state)
        return response

    web = create("clients", {"name": prefix + "-oidc", "app_type": "regular_web",
                            "oidc_conformant": True, "is_first_party": True, "grant_types": ["authorization_code"],
                            "jwt_configuration": {"alg": "RS256"},
                            "token_endpoint_auth_method": "client_secret_post",
                            "callbacks": [CALLBACK]}, "client_id")
    state.update(oidc_client_id=web["client_id"], oidc_client_secret=web["client_secret"])
    save(path, state)
    connection = create("connections", {"name": prefix + "-users", "strategy": "auth0",
                                        "enabled_clients": [web["client_id"], api.client_id],
                                        "options": {"disable_signup": True, "passwordPolicy": "excellent"}})
    state["connection_name"] = connection["name"]
    save(path, state)
    for namespace in NAMESPACES:
        for role in ROLES:
            group = f"{prefix}-{namespace}-{role}"
            password = generated_password()
            if email_address:
                local, mailbox_domain = email_address.split("@")
                username = local + "+" + group + "@" + mailbox_domain
            else:
                username = group + "@" + email_domain
            user = create("users", {"email": username, "password": password,
                                    "connection": connection["name"], "verify_email": False,
                                    "email_verified": False,
                                    "app_metadata": {"openbao_lab_groups": [group]}}, "user_id")
            state["users"].append({"namespace": namespace, "role": role, "username": username,
                                   "password": password, "group": group, "user_id": user["user_id"]})
            save(path, state)
    action = create("actions/actions", {"name": prefix + "-groups", "runtime": "node22",
                                        "supported_triggers": [{"id": "post-login", "version": "v3"}],
                                        "code": action_code(web["client_id"], prefix)})
    action_path = "/api/v2/actions/actions/" + action["id"]
    for attempt in range(60):
        current = api.request("GET", action_path)
        if current.get("status") == "built":
            break
        if current.get("status") == "failed":
            raise LabError("Auth0 Action build failed; inspect the Action in the dashboard")
        time.sleep(2)
    else:
        raise LabError("Auth0 Action build did not finish within two minutes")
    api.request("POST", action_path + "/deploy", {})
    for attempt in range(60):
        if api.request("GET", action_path).get("all_changes_deployed"):
            break
        time.sleep(2)
    else:
        raise LabError("Auth0 Action deployment did not finish within two minutes")
    current = api.request("GET", "/api/v2/actions/triggers/post-login/bindings")["bindings"]
    if current != bindings:
        raise LabError("Login Action bindings changed during provisioning; refusing to overwrite them")
    updated = bindings_payload(current)
    updated.append({"ref": {"type": "action_id", "value": action["id"]}, "display_name": prefix + "-groups"})
    api.request("PATCH", "/api/v2/actions/triggers/post-login/bindings", {"bindings": updated})
    state["status"] = "complete"
    save(path, state)
    return state


def cleanup(api, state, path):
    """Delete only recorded lab IDs and preserve unrelated login Actions."""
    allowed = {"clients", "resource-servers", "client-grants", "connections", "users", "actions/actions"}
    owned = state.get("owned", [])
    for item in owned:
        kind, resource_id = item.get("kind"), item.get("id")
        if kind not in allowed or not isinstance(resource_id, str) or not re.fullmatch(r"[A-Za-z0-9_|-]+", resource_id):
            raise LabError("Invalid owned resource record; refusing cleanup")
        expected = "/api/v2/" + kind + "/" + urllib.parse.quote(resource_id, safe="")
        if item.get("path") != expected:
            raise LabError("Owned resource path does not match its ID; refusing cleanup")
    if state.get("status") == "cleaned":
        return
    state["status"] = "cleaning"
    save(path, state)
    action_ids = {item["id"] for item in owned if item["kind"] == "actions/actions" and not item.get("deleted")}
    if action_ids:
        endpoint = "/api/v2/actions/triggers/post-login/bindings"
        bindings = api.request("GET", endpoint)["bindings"]
        retained = [binding for binding in bindings if binding.get("action", {}).get("id") not in action_ids]
        if len(retained) != len(bindings):
            payload = bindings_payload(retained)
            if api.request("GET", endpoint)["bindings"] != bindings:
                raise LabError("Login Action bindings changed during cleanup; refusing to overwrite them")
            api.request("PATCH", endpoint, {"bindings": payload})
    for item in reversed(owned):
        if item.get("deleted"):
            continue
        try:
            api.request("DELETE", item["path"])
        except APIError as error:
            if error.status != 404:
                raise
        item["deleted"] = True
        save(path, state)
    if state.get("pending_create"):
        state["status"] = "cleanup-incomplete"
        save(path, state)
        raise LabError("Recorded resources removed, but an interrupted create may remain; inspect the prefix in Auth0")
    state["status"] = "cleaned"
    save(path, state)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--credentials", type=Path)
    parser.add_argument("--state", required=True, type=Path)
    parser.add_argument("--prefix", required=True)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--apply", action="store_true", help="Create resources in the dedicated Auth0 tenant")
    mode.add_argument("--cleanup", action="store_true", help="Delete only resources recorded in the private state")
    args = parser.parse_args()
    validate_prefix(args.prefix)
    if not args.apply and not args.cleanup:
        print("Plan: 1 OIDC application, 1 database connection, 9 users, 1 Action")
        print("Prefix: " + args.prefix)
        print("No network requests made; add --apply to provision")
        return
    if not args.credentials:
        raise LabError("--credentials is required with --apply or --cleanup")
    credentials = private_json(args.credentials)
    domain = validate(credentials, args.prefix)
    args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if args.state.parent.stat().st_mode & 0o077:
        raise LabError("State directory must have mode 0700")
    lock = args.state.with_suffix(args.state.suffix + ".lock")
    fd = os.open(lock, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        os.close(fd)
        state = private_json(args.state) if args.state.exists() else {}
        if state and (state.get("provider"), state.get("prefix"), state.get("domain")) != ("auth0", args.prefix, domain):
            raise LabError("State belongs to a different provider, tenant or prefix")
        if args.cleanup:
            if not state:
                print("No Auth0 state; no recorded resources to clean up")
                return
            cleanup(API(credentials, cleanup=True, legacy_kinds={item.get("kind") for item in state.get("owned", [])}), state, args.state)
            print("Recorded Auth0 lab resources removed; cleanup journal saved")
        else:
            provision(API(credentials), state, args.state, args.prefix, domain, credentials.get("email_domain", "example.invalid"), credentials.get("email_address"))
            print("Auth0 lab provisioned; private state saved")
    finally:
        lock.unlink()


if __name__ == "__main__":
    try:
        main()
    except (LabError, OSError, ValueError, KeyError) as exc:
        print(str(exc) if isinstance(exc, LabError) else "Auth0 lab failed; check private files and provider configuration", file=sys.stderr)
        sys.exit(1)
