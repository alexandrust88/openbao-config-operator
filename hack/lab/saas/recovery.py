#!/usr/bin/env python3
"""Export only validated resource IDs for retrying failed CI cleanup."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import urllib.parse


def inventory(state):
    provider = state.get("provider")
    domain = state.get("domain", "")
    prefix = state.get("prefix", "")
    if provider not in ("okta", "auth0") or not re.fullmatch(r"[a-z][a-z0-9-]{2,30}", prefix):
        raise ValueError("invalid recovery identity")
    if not re.fullmatch(r"(?:https://)?[A-Za-z0-9.-]+", domain):
        raise ValueError("invalid recovery domain")
    result = {"provider": provider, "domain": domain, "prefix": prefix}
    if provider == "auth0":
        result["status"] = "cleaning"
        result["owned"] = []
        allowed = {"clients", "resource-servers", "client-grants", "connections", "users", "actions/actions"}
        for item in state.get("owned", []):
            kind, identifier = item.get("kind"), item.get("id", "")
            if kind not in allowed or not re.fullmatch(r"[A-Za-z0-9_|-]+", identifier):
                raise ValueError("invalid resource identifier")
            path = "/api/v2/" + kind + "/" + urllib.parse.quote(identifier, safe="")
            if item.get("path") != path:
                raise ValueError("invalid resource path")
            result["owned"].append({"kind": kind, "id": identifier, "path": path,
                                    "deleted": item.get("deleted") is True})
        if state.get("pending_create"):
            result["pending_create"] = {"unresolved_create": True}
    else:
        result["complete"] = False
        result["resources"] = []
        pattern = r"/api/v1/(?:apps/[A-Za-z0-9_-]+|users/[A-Za-z0-9_-]+|groups/[A-Za-z0-9_-]+|authorizationServers/(?!default(?:/|$))[A-Za-z0-9_-]+(?:/(?:scopes|claims|policies)/[A-Za-z0-9_-]+(?:/rules/[A-Za-z0-9_-]+)?)?)"
        for item in state.get("resources", []):
            path = item.get("path", "")
            if not re.fullmatch(pattern, path):
                raise ValueError("invalid resource path")
            result["resources"].append({"path": path})
        if state.get("pending"):
            result["pending"] = {"unresolved_create": True}
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        fd = os.open(args.state, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd) as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
                raise ValueError("unsafe state")
            safe = inventory(json.load(stream))
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(safe, stream, indent=2)
    except (OSError, ValueError, KeyError, TypeError):
        raise SystemExit("Could not export validated recovery IDs; inspect the run prefix in the provider dashboard") from None


if __name__ == "__main__":
    main()
