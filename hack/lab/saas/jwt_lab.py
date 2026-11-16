"""JWT discovery and direct JWKS regression using operator-managed configuration."""

import base64
import copy
import json
import urllib.error
import urllib.request

import operator_lab as lab

MODES = ("discovery", "jwks")
ERROR_MARKERS = {
    "group": "does not match any associated bound claim values",
    "issuer": "invalid issuer (iss) claim",
    "audience": "audience claim does not match any expected audience",
    "signature": "error verifying token signature",
}


def mode_suffix(state, mode):
    suffix = state.get("jwt_mount_suffix", "")
    if suffix not in ("", "-machine"):
        raise ValueError("unsupported JWT lab mount suffix")
    return "-jwt-" + mode + suffix


def render(state, kube_namespace="obco-lab", connection="lab-openbao"):
    """Reuse the OIDC lab's policies while giving each JWT mount its own groups."""
    base = lab.render(state, kube_namespace, connection)
    objects = []
    for mode in MODES:
        suffix = mode_suffix(state, mode)
        for item in base:
            if item["kind"] not in ("AuthMount", "JWTAuthConfig", "JWTAuthRole", "GroupAlias", "Group", "GroupMembership"):
                continue
            if item["kind"] == "Group" and item["spec"]["type"] != "external":
                continue
            current = copy.deepcopy(item)
            current["metadata"]["name"] += suffix
            spec = current["spec"]
            mount = lab.prefix(state) + mode_suffix(state, mode)
            if item["kind"] == "AuthMount":
                spec.update(path=mount, type="jwt")
            elif item["kind"] == "JWTAuthConfig":
                for key in ("oidcDiscoveryURL", "oidcClientID", "oidcClientSecretSecretRef"):
                    spec.pop(key, None)
                spec.update(mountPath=mount, jwtSupportedAlgs=["RS256"])
                spec["jwksURL" if mode == "jwks" else "oidcDiscoveryURL"] = (
                    state["jwks_url"] if mode == "jwks" else state["issuer"])
            elif item["kind"] == "JWTAuthRole":
                for key in ("allowedRedirectURIs", "oidcScopes", "callbackMode"):
                    spec.pop(key, None)
                spec.update(mountPath=mount, roleType="jwt")
            elif item["kind"] == "Group":
                spec["name"] += suffix
            elif item["kind"] == "GroupAlias":
                spec["mountPath"] = mount
                spec["groupRef"]["name"] += suffix
            else:
                spec["memberGroupRef"]["name"] += suffix
            objects.append(current)
        for item in list(objects):
            if not item["metadata"]["name"].endswith(suffix):
                continue
            kind, spec = item["kind"], item["spec"]
            if kind in ("AuthMount", "JWTAuthConfig") or (kind == "JWTAuthRole" and spec["name"] == "read"):
                control = copy.deepcopy(item)
                control["metadata"]["name"] += "-bad-issuer"
                if kind == "AuthMount":
                    control["spec"]["path"] += "-bad-issuer"
                else:
                    control["spec"]["mountPath"] += "-bad-issuer"
                    if kind == "JWTAuthConfig":
                        control["spec"]["boundIssuer"] = state["issuer"] + "/wrong-issuer"
                objects.append(control)
            if kind == "JWTAuthRole" and spec["name"] == "read":
                control = copy.deepcopy(item)
                control["metadata"]["name"] += "-bad-audience"
                control["spec"].update(name="wrong-audience", boundAudiences=["not-the-openbao-audience"])
                objects.append(control)
    return objects


def corrupt_signature(token):
    pieces = token.split(".")
    if len(pieces) != 3:
        raise RuntimeError("provider did not return a signed JWT")
    signature = bytearray(base64.urlsafe_b64decode(pieces[2] + "=" * (-len(pieces[2]) % 4)))
    if not signature:
        raise RuntimeError("provider returned an empty signature")
    signature[0] ^= 1
    pieces[2] = base64.urlsafe_b64encode(signature).decode().rstrip("=")
    return ".".join(pieces)


def login(bao, state, tenant, mount, role, token):
    headers = {"Content-Type": "application/json"}
    if tenant != "root":
        headers["X-Vault-Namespace"] = lab.bao_namespace(state, tenant)
    request = urllib.request.Request(bao.address + "/v1/auth/" + mount + "/login", method="POST",
        headers=headers, data=json.dumps({"role": role, "jwt": token}).encode())
    try:
        with bao.opener.open(request, timeout=30) as response:
            body = json.load(response)
            if not isinstance(body, dict):
                raise RuntimeError("JWT login response was not an object; response omitted")
            return response.status, body
    except urllib.error.HTTPError as error:
        try:
            body = json.loads(error.read(65536))
            errors = body.get("errors", []) if isinstance(body, dict) else []
            if not isinstance(errors, list):
                errors = []
            category = next((name for name, marker in ERROR_MARKERS.items()
                if any(isinstance(message, str) and marker in message.lower() for message in errors)), None)
            return error.code, {"rejection": category}
        except (ValueError, TypeError):
            raise RuntimeError("JWT rejection response was not valid JSON; response omitted") from None
        finally:
            error.close()
    except (urllib.error.URLError, ValueError, TypeError):
        raise RuntimeError("JWT login request failed; response omitted") from None


def rejected(bao, state, tenant, mount, role, token, reason):
    status, body = login(bao, state, tenant, mount, role, token)
    if body.get("auth", {}).get("client_token"):
        bao.request(tenant, "POST", "auth/token/revoke-self", token=body["auth"]["client_token"])
    if status not in (400, 403) or body.get("rejection") != reason:
        raise RuntimeError("JWT negative case did not fail specifically for " + reason)


def check(bao, state, tokens):
    for mode in MODES:
        mount = lab.prefix(state) + mode_suffix(state, mode)
        for tenant in lab.TENANTS:
            for role in lab.ROLES:
                status, body = login(bao, state, tenant, mount, role, tokens[tenant, role])
                lab.expect(status, 200, state["provider"] + " " + mode + " JWT login")
                lab.test_access(bao, state, tenant, role, body.get("auth", {}))
            reader = tokens[tenant, "read"]
            other = lab.TENANTS[(lab.TENANTS.index(tenant) + 1) % len(lab.TENANTS)]
            rejected(bao, state, tenant, mount, "admin", reader, "group")
            rejected(bao, state, tenant, mount, "read", tokens[other, "read"], "group")
            rejected(bao, state, tenant, mount, "wrong-audience", reader, "audience")
            rejected(bao, state, tenant, mount + "-bad-issuer", "read", reader, "issuer")
            rejected(bao, state, tenant, mount, "read", corrupt_signature(reader), "signature")
            print(f"{mode}/{tenant}: three JWT identities and five classified denial cases passed")


def run(bao, state, kube, kube_namespace="obco-lab", connection="lab-openbao"):
    """Acquire ID tokens through hosted login, then test both operator JWT modes."""
    from jwt_login import acquire_id_token, discover
    metadata = discover(state)
    jwt_state = {**state, "jwks_url": metadata["jwks_uri"]}
    objects = render(jwt_state, kube_namespace, connection)
    kube.apply(objects)
    kube.wait(objects)
    tokens = {(user["namespace"], user["role"]): acquire_id_token(state, user, metadata=metadata)
              for user in state["users"]}
    check(bao, jwt_state, tokens)
    return tokens


def run_machine(bao, state, kube, tokens, audience, kube_namespace="obco-lab", connection="lab-openbao", human_tokens=None):
    """Validate provider-issued service JWTs on dedicated, audience-bound mounts."""
    from jwt_login import discover
    expected = {(tenant, role) for tenant in lab.TENANTS for role in lab.ROLES}
    if not isinstance(audience, str) or not audience or set(tokens) != expected or any(not isinstance(value, str) or not value for value in tokens.values()):
        raise ValueError("machine regression requires a configured audience and all nine issued tokens")
    separate_audience = audience != state['oidc_client_id']
    if separate_audience and (human_tokens is None or set(human_tokens) != expected):
        raise ValueError("different machine audience requires human tokens for isolation checks")
    metadata = discover(state)
    machine_state = {**state, "oidc_client_id": audience, "jwks_url": metadata['jwks_uri'], "jwt_mount_suffix": "-machine"}
    objects = render(machine_state, kube_namespace, connection)
    kube.apply(objects)
    kube.wait(objects)
    check(bao, machine_state, tokens)
    if separate_audience:
        for mode in MODES:
            human_mount = lab.prefix(state) + mode_suffix(state, mode)
            machine_mount = lab.prefix(state) + mode_suffix(machine_state, mode)
            for tenant in lab.TENANTS:
                rejected(bao, state, tenant, human_mount, "read", tokens[tenant, "read"], "audience")
                rejected(bao, machine_state, tenant, machine_mount, "read", human_tokens[tenant, "read"], "audience")
        print("machine/human audience separation: both JWT verification modes rejected cross-use in every namespace")
    print("machine JWT: nine service identities passed discovery/JWKS access and denial checks")
