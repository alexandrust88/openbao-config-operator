#!/usr/bin/env python3
"""Provision a dedicated Okta lab; default execution only prints a plan."""

import argparse
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


class LabError(Exception):
    """A safe diagnostic that contains no response body or credentials."""


class APIError(LabError):
    def __init__(self, method, code):
        self.code = code
        super().__init__(f"Okta API {method} failed with HTTP {code}; inspect the Okta System Log")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise LabError("Okta redirected a management request; use the canonical org domain")


def private_json(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd) as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
            raise LabError("Credential and state files must be regular files with mode 0600")
        try:
            return json.load(stream)
        except (ValueError, UnicodeError):
            raise LabError("Invalid JSON in private input file") from None


def save(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink():
        raise LabError("Refusing symlink state file")
    fd, temp = tempfile.mkstemp(prefix=".okta-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, indent=2)
            stream.write("\n")
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def domain_url(domain):
    parsed = urllib.parse.urlsplit(domain if "://" in domain else "https://" + domain)
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password
            or parsed.port or parsed.path not in ("", "/") or parsed.query or parsed.fragment
            or not re.fullmatch(r"[A-Za-z0-9.-]+", parsed.hostname)):
        raise LabError("domain must be a canonical HTTPS Okta org hostname")
    return "https://" + parsed.hostname.lower()


class API:
    def __init__(self, domain, token):
        self.domain = domain_url(domain)
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect())

    def call(self, method, path, data=None):
        if not path.startswith("/api/v1/") or ".." in path:
            raise LabError("Invalid management API path")
        request = urllib.request.Request(
            self.domain + path,
            data=None if data is None else json.dumps(data).encode(), method=method,
            headers={"Authorization": "SSWS " + self.token, "Accept": "application/json",
                     "Content-Type": "application/json"},
        )
        try:
            with self.opener.open(request, timeout=30) as response:
                body = response.read()
                return json.loads(body) if body else {}
        except urllib.error.HTTPError as error:
            error.close()
            # Never retry mutations: a timeout can leave an object created remotely.
            raise APIError(method, error.code) from None
        except (urllib.error.URLError, TimeoutError, ValueError):
            raise LabError("Okta request failed; inspect private state and the Okta System Log before retrying") from None


def app_payload(label):
    oauth = {"application_type": "web", "grant_types": ["authorization_code"],
             "response_types": ["code"], "redirect_uris": [CALLBACK], "consent_method": "TRUSTED"}
    return {"name": "oidc_client", "label": label, "signOnMode": "OPENID_CONNECT",
            "credentials": {"oauthClient": {"token_endpoint_auth_method": "client_secret_basic"}},
            "settings": {"oauthClient": oauth}}


def usernames(credentials, prefix):
    """Build distinct test logins, optionally delivered to one plus-addressed inbox."""
    address = credentials.get("email_address")
    if address is not None:
        if not isinstance(address, str) or address.count("@") != 1:
            raise LabError("email_address must be a mailbox you control without an existing plus tag")
        local, domain = address.split("@")
        if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9._-]*", local) or local.endswith(".") or ".." in local:
            raise LabError("email_address must use a simple local part without an existing plus tag")
    else:
        local, domain = None, credentials.get("email_domain")
    if not isinstance(domain, str) or not re.fullmatch(r"(?:[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?\.)+[A-Za-z]{2,63}", domain):
        raise LabError("Provide email_address or email_domain for test email you control")
    result = {}
    for namespace in NAMESPACES:
        for role in ROLES:
            name = f"{prefix}-{namespace}-{role}"
            alias = name if local is None else local + "+" + name
            if len(alias) > 64 or len(alias + "@" + domain) > 254:
                raise LabError("Test email alias is too long; use a shorter mailbox local part or lab prefix")
            result[(namespace, role)] = alias + "@" + domain
    return result


def provision(api, credentials, prefix, state_path):
    logins = usernames(credentials, prefix)
    expected = {"provider": "okta", "domain": api.domain, "prefix": prefix}
    if Path(state_path).exists():
        state = private_json(state_path)
        if any(state.get(key) != value for key, value in expected.items()):
            raise LabError("State belongs to a different provider, org or prefix")
        if not state.get("complete"):
            raise LabError("Incomplete provisioning: inspect recorded IDs and Okta System Log; do not delete state and retry")
        for resource in state["resources"]:
            api.call("GET", resource["path"])
        return state

    policy_id = credentials.get("app_sign_on_policy_id")
    if policy_id is not None:
        if not isinstance(policy_id, str) or not re.fullmatch(r"[A-Za-z0-9_-]+", policy_id):
            raise LabError("app_sign_on_policy_id must be an Okta policy ID")
        policy = api.call("GET", "/api/v1/policies/" + policy_id)
        if policy.get("type") != "ACCESS_POLICY" or policy.get("status") != "ACTIVE":
            raise LabError("app_sign_on_policy_id must identify an ACTIVE ACCESS_POLICY")

    # Avoid silently adopting an earlier run whose private state was lost.
    for path in ("/api/v1/apps?q=" + urllib.parse.quote(prefix),
                 "/api/v1/groups?q=" + urllib.parse.quote(prefix),
                 "/api/v1/users?search=" + urllib.parse.quote('profile.login sw "' + prefix + '-"')):
        if api.call("GET", path):
            raise LabError("Lab prefix already exists in Okta; recover its state or use a new prefix")
    servers = api.call("GET", "/api/v1/authorizationServers")
    if any(server.get("name") == prefix for server in servers):
        raise LabError("Authorization server already exists; recover its state")
    state = {**expected, "complete": False, "resources": [], "users": [],
             "groups_claim": "groups", "callback_uri": CALLBACK,
             "audience": "api://" + prefix}
    save(state_path, state)

    def create(path, payload):
        state["pending"] = {"method": "POST", "path": path}
        save(state_path, state)
        try:
            result = api.call("POST", path, payload)
        except APIError as error:
            if error.code in (400, 401, 403, 404, 422, 429):
                state.pop("pending", None)
                save(state_path, state)
            raise
        if not isinstance(result.get("id"), str) or not re.fullmatch(r"[A-Za-z0-9_-]+", result["id"]):
            raise LabError("Create returned no safe resource ID; inspect the Okta System Log")
        resource_path = path.split("?")[0] + "/" + result["id"]
        state["resources"].append({"path": resource_path})
        state.pop("pending", None)
        save(state_path, state)
        return result

    server = create("/api/v1/authorizationServers", {
        "name": prefix, "description": "Dedicated OpenBao operator integration lab",
        "audiences": [state["audience"]], "issuerMode": "ORG_URL"})
    server_path = "/api/v1/authorizationServers/" + server["id"]
    state["issuer"] = api.domain + "/oauth2/" + server["id"]
    create(server_path + "/claims", {"name": "groups", "status": "ACTIVE", "claimType": "IDENTITY",
        "valueType": "GROUPS", "group_filter_type": "STARTS_WITH", "value": prefix + "-",
        "alwaysIncludeInToken": True, "conditions": {"scopes": []}})
    web = create("/api/v1/apps", app_payload(prefix + "-oidc"))
    if policy_id is not None:
        api.call("PUT", f'/api/v1/apps/{web["id"]}/policies/{policy_id}')
        state["app_sign_on_policy_id"] = policy_id
    try:
        oauth = web["credentials"]["oauthClient"]
        state["oidc_client_id"] = oauth["client_id"]
        state["oidc_client_secret"] = oauth["client_secret"]
    except KeyError:
        raise LabError("Okta did not return web client credentials; inspect private state") from None
    save(state_path, state)
    group_ids = []
    for namespace in NAMESPACES:
        for role in ROLES:
            name = f"{prefix}-{namespace}-{role}"
            group = create("/api/v1/groups", {"profile": {"name": name, "description": "OpenBao lab access"}})
            group_ids.append(group["id"])
            password = "".join(secrets.choice(chars) for chars in (
                string.ascii_uppercase, string.ascii_lowercase, string.digits, "!@#$%"
            )) + secrets.token_urlsafe(28)
            username = logins[(namespace, role)]
            user = {"namespace": namespace, "role": role, "username": username,
                    "password": password, "group": name}
            state["users"].append(user)
            save(state_path, state)
            created = create("/api/v1/users?activate=true", {
                "profile": {"firstName": "OpenBao", "lastName": namespace + " " + role,
                            "email": username, "login": username},
                "credentials": {"password": {"value": password}}, "groupIds": [group["id"]]})
            user["id"] = created["id"]
            api.call("PUT", f'/api/v1/apps/{web["id"]}/groups/{group["id"]}', {})

    policy = create(server_path + "/policies", {
        "name": prefix + "-humans", "description": "OpenBao lab browser client only",
        "type": "OAUTH_AUTHORIZATION_POLICY", "status": "ACTIVE", "priority": 1,
        "conditions": {"clients": {"include": [state["oidc_client_id"]]}}})
    create(server_path + "/policies/" + policy["id"] + "/rules", {
        "name": prefix + "-humans", "type": "RESOURCE_ACCESS", "status": "ACTIVE", "priority": 1,
        "conditions": {"grantTypes": {"include": ["authorization_code"]},
                       "scopes": {"include": ["openid", "profile", "email"]},
                       "people": {"groups": {"include": group_ids}}},
        "actions": {"token": {"accessTokenLifetimeMinutes": 5,
                              "refreshTokenLifetimeMinutes": 0, "refreshTokenWindowMinutes": 10}}})

    state["complete"] = True
    save(state_path, state)
    return state


def cleanup(api, prefix, state_path):
    if not Path(state_path).exists():
        return
    state = private_json(state_path)
    if (state.get("provider"), state.get("domain"), state.get("prefix")) != ("okta", api.domain, prefix):
        raise LabError("State belongs to a different provider, org or prefix")
    resources = state.get("resources", [])
    safe_path = re.compile(r"/api/v1/(?:apps/[A-Za-z0-9_-]+|users/[A-Za-z0-9_-]+|groups/[A-Za-z0-9_-]+|"
                           r"authorizationServers/(?!default(?:/|$))[A-Za-z0-9_-]+"
                           r"(?:/(?:scopes|claims|policies)/[A-Za-z0-9_-]+(?:/rules/[A-Za-z0-9_-]+)?)?)")
    if any(not safe_path.fullmatch(item.get("path", "")) for item in resources):
        raise LabError("State contains an unexpected resource path; refusing cleanup")
    state["complete"] = False
    state["cleanup_started"] = True
    save(state_path, state)
    for item in list(reversed(resources)):
        path = item["path"]
        try:
            current = api.call("GET", path)
            if re.fullmatch(r"/api/v1/users/[A-Za-z0-9_-]+", path):
                if current.get("status") != "DEPROVISIONED":
                    api.call("POST", path + "/lifecycle/deactivate?sendEmail=false")
                    for attempt in range(30):
                        if api.call("GET", path).get("status") == "DEPROVISIONED":
                            break
                        time.sleep(1)
                    else:
                        raise LabError("User deactivation is pending; retry cleanup later")
            elif re.fullmatch(r"/api/v1/(?:apps|authorizationServers)/[A-Za-z0-9_-]+", path):
                if current.get("status") == "ACTIVE":
                    api.call("POST", path + "/lifecycle/deactivate")
            api.call("DELETE", path)
        except APIError as error:
            if error.code != 404:
                raise
        resources.remove(item)
        save(state_path, state)
    state["users"] = []
    state.pop("oidc_client_secret", None)
    state["cleanup_complete"] = not bool(state.get("pending"))
    save(state_path, state)
    if state.get("pending"):
        raise LabError("Recorded resources removed; an ambiguous pending creation remains. Inspect the Okta System Log and lab prefix manually")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--credentials", type=Path)
    parser.add_argument("--state", type=Path, required=True)
    parser.add_argument("--prefix", required=True)
    operation = parser.add_mutually_exclusive_group()
    operation.add_argument("--apply", action="store_true")
    operation.add_argument("--cleanup", action="store_true", help="Delete only resources recorded in this lab state")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,30}", args.prefix):
        raise LabError("prefix must be 3–31 lowercase letters, digits or hyphens, starting with a letter")
    if not args.apply and not args.cleanup:
        print("Plan: create one dedicated authorization server, one OIDC app, nine groups and nine users")
        return
    if not args.credentials:
        raise LabError("--credentials is required with --apply or --cleanup")
    credentials = private_json(args.credentials)
    if not all(isinstance(credentials.get(key), str) and credentials[key]
               for key in ("domain", "api_token")):
        raise LabError("Credentials require domain and api_token strings")
    if not args.cleanup:
        usernames(credentials, args.prefix)
    args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if args.state.parent.stat().st_mode & 0o077:
        raise LabError("State directory must have mode 0700")
    lock = args.state.with_suffix(args.state.suffix + ".lock")
    fd = os.open(lock, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        os.close(fd)
        api = API(credentials["domain"], credentials["api_token"])
        if args.cleanup:
            cleanup(api, args.prefix, args.state)
            print("Recorded Okta lab resources removed")
        else:
            provision(api, credentials, args.prefix, args.state)
            print("Okta provisioning complete; credentials are in the private state file")
    finally:
        lock.unlink()


if __name__ == "__main__":
    try:
        main()
    except (LabError, OSError, ValueError, KeyError, TypeError) as error:
        print(str(error) if isinstance(error, LabError) else "Okta lab failed; inspect private inputs and provider configuration", file=sys.stderr)
        sys.exit(1)
