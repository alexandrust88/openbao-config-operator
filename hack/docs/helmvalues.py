"""Generate docs/reference/helm-values.md from the chart values and schema.

Reads charts/openbao-config-operator/values.yaml (defaults + `# --` comments) and
values.schema.json (types and constraints). Prints markdown: short intro, the template
refuse-combinations table, then one Key|Type|Default|Constraints|Description table with dotted keys.

Usage:
  python hack/docs/helmvalues.py > docs/reference/helm-values.md
  make helm-values-docs
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
CHART = ROOT / "charts" / "openbao-config-operator"
VALUES = CHART / "values.yaml"
SCHEMA = CHART / "values.schema.json"

KEY_LINE = re.compile(r"^(\s*)([A-Za-z0-9_.-]+)\s*:(?:\s*(.*))?$")
DOC_START = re.compile(r"^\s*#\s*--(?:\s+(.*))?$")
DOC_CONT = re.compile(r"^\s*#\s?(.*)$")

INTRO = """---
description: "Configure the OpenBao Config Operator Helm chart, including deployment, RBAC, networking, metrics and resource settings."
---

# Helm values

Every value of the `openbao-config-operator` chart, with its type, default, constraints and meaning.

!!! note "Source of truth"
    Defaults and comments come from
    [`charts/openbao-config-operator/values.yaml`](../../charts/openbao-config-operator/values.yaml);
    types and constraints from
    [`charts/openbao-config-operator/values.schema.json`](../../charts/openbao-config-operator/values.schema.json).
    Those files win when they disagree with this page.

The schema sets `additionalProperties: false` on the top level and on most sections, so Helm
refuses a misspelt or unknown key at install time. Besides the schema, the templates refuse these
combinations:

| combination | error |
|-------------|-------|
| `replicas` greater than 1 with `leaderElection.enabled: false` | `replicas > 1 requires leaderElection.enabled=true` |
| `injectedIdentity.projectedToken.enabled` with `path` directly in `/var/run/secrets/kubernetes.io/serviceaccount/` | the automounted token directory is refused |
| `crds.install: false` with `crds.keep: false` | an upgrade would delete the CRDs of an earlier release and every custom resource with them |
| `metrics.enabled` and `metrics.certSecret` with `serviceMonitor.enabled`, without `serviceMonitor.tlsConfig.ca` (or `caFile`) and `serverName`, or with `insecureSkipVerify: true` | the ServiceMonitor cannot verify the certificate |

Helm merges your `serviceMonitor.tlsConfig` with the default `insecureSkipVerify: true`, so when you set `ca`
also set `insecureSkipVerify: false`.

String values that land in a manifest (names, image tag, audiences, CIDRs, durations, the token path) must match a pattern without whitespace, so a value cannot add YAML keys to the rendered objects.

Manager flags without a chart value, such as `--drift-interval-critical`,
`--drift-interval-standard`, `--max-concurrent-reconciles`,
`--max-concurrent-reconciles-high-volume`, `--write-budget-per-minute`, `--cluster-id` and the
`--zap-*` logging flags, go through `extraArgs`. `--hash-key-secret` cannot be changed through the
chart: the namespace-scoped Role pins the Secret name.

[Observability](../operate/observability.md) explains the metrics and the alerts. The
[hardening guide](../security/hardening.md#r-network-01-enable-the-networkpolicy) explains why
empty `networkPolicy.apiServer.cidrs` and `networkPolicy.openbao.to` leave egress open by destination.

## Values

<div class="helm-values" markdown="1">

| Key | Type | Default | Constraints | Description |
| --- | --- | --- | --- | --- |
"""

OUTRO = """
</div>
"""


def load_schema() -> dict:
    return json.loads(SCHEMA.read_text())


def resolve(schema: dict, node: dict) -> dict:
    ref = node.get("$ref")
    if not ref:
        return node
    if not ref.startswith("#/"):
        return node
    cur: object = schema
    for part in ref[2:].split("/"):
        if not isinstance(cur, dict) or part not in cur:
            return node
        cur = cur[part]
    return cur if isinstance(cur, dict) else node


def schema_node(schema: dict, dotted: str) -> dict:
    node = schema
    for part in dotted.split("."):
        node = resolve(schema, node)
        props = node.get("properties")
        if not isinstance(props, dict) or part not in props:
            return {}
        node = props[part]
    return resolve(schema, node)


def map_type(name: str) -> str:
    return {"boolean": "bool", "integer": "int", "array": "list", "object": "object", "string": "string", "number": "number"}.get(
        name, name or "object"
    )


def type_of(node: dict, value) -> str:
    if not node:
        if isinstance(value, bool):
            return "bool"
        if isinstance(value, int) and not isinstance(value, bool):
            return "int"
        if isinstance(value, float):
            return "number"
        if isinstance(value, list):
            return "list"
        if isinstance(value, dict):
            return "object"
        return "string"
    if "enum" in node and "type" not in node:
        return "string"
    t = node.get("type")
    if isinstance(t, list):
        return " or ".join(map_type(x) for x in t)
    if t:
        return map_type(t)
    if "properties" in node or node.get("additionalProperties") is not None:
        return "object"
    if "items" in node:
        return "list"
    return type_of({}, value)


def format_default(value) -> str:
    if value is None:
        return "`null`"
    if isinstance(value, bool):
        return "`true`" if value else "`false`"
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return f"`{value}`"
    if isinstance(value, str):
        if value == "":
            return '`""`'
        return "`" + value.replace("`", "'") + "`"
    return "`" + json.dumps(value, separators=(",", ":"), ensure_ascii=False) + "`"


def cell(text: str) -> str:
    # Python-Markdown keeps a pipe inside a code span in its cell, and would show an escaped one as "\|".
    parts = text.replace("\n", " ").split("`")
    return "`".join(p if i % 2 else p.replace("|", "\\|") for i, p in enumerate(parts))


def bounds(node: dict) -> str:
    lo, hi, above = node.get("minimum"), node.get("maximum"), node.get("exclusiveMinimum")
    if lo is not None and hi is not None:
        return f"{lo} to {hi}"
    if above is not None:
        return f"greater than {above}" + (f", at most {hi}" if hi is not None else "")
    if lo is not None:
        return f"at least {lo}"
    if hi is not None:
        return f"at most {hi}"
    return ""


def rules_of(node: dict) -> list[str]:
    out: list[str] = []
    if "enum" in node:
        prefix = "only " if len(node["enum"]) == 1 else "one of "
        out.append(prefix + ", ".join(format_default(v) for v in node["enum"]))
    if "const" in node:
        out.append("must be " + format_default(node["const"]))
    negated = node.get("not")
    if isinstance(negated, dict) and "const" in negated:
        out.append("not " + format_default(negated["const"]))
    limits = bounds(node)
    if limits:
        out.append(limits)
    if node.get("minLength") == 1:
        out.append("non-empty")
    if "maxLength" in node:
        out.append(f"at most {node['maxLength']} characters")
    if "pattern" in node:
        out.append(f"pattern `{node['pattern']}`")
    if "format" in node:
        out.append(f"format `{node['format']}`")
    return out


def constraints(schema: dict, node: dict) -> str:
    out = rules_of(node)
    for part in node.get("allOf", []):
        out.extend(rules_of(resolve(schema, part)))
    items = node.get("items")
    if isinstance(items, dict):
        out.extend("each item: " + r for r in rules_of(resolve(schema, items)))
    return "; ".join(out)


def parse_comments(path: Path) -> dict[str, str]:
    """Map dotted keys to `# --` descriptions from values.yaml."""
    stack: list[tuple[int, str]] = []
    docs: dict[str, str] = {}
    pending: list[str] = []
    list_indent: int | None = None
    for raw in path.read_text().splitlines():
        stripped = raw.lstrip()
        if not stripped:
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        start = DOC_START.match(raw)
        if start:
            pending = [(start.group(1) or "").strip()]
            continue
        if pending and stripped.startswith("#"):
            pending.append(DOC_CONT.match(raw).group(1).strip())
            continue
        if stripped.startswith("#"):
            pending = []
            continue
        if list_indent is not None and indent > list_indent:
            continue
        list_indent = None
        if stripped.startswith("- "):
            list_indent = indent
            pending = []
            continue
        m = KEY_LINE.match(raw)
        if not m:
            pending = []
            continue
        while stack and stack[-1][0] >= indent:
            stack.pop()
        key = ".".join([p for _, p in stack] + [m.group(2)])
        if pending:
            docs[key] = " ".join(p for p in pending if p)
        pending = []
        stack.append((indent, m.group(2)))
    return docs


def flatten(obj, prefix: str = "") -> list[tuple[str, object]]:
    rows: list[tuple[str, object]] = []
    if not isinstance(obj, dict):
        return rows
    for k, v in obj.items():
        key = f"{prefix}.{k}" if prefix else k
        rows.append((key, v))
        if isinstance(v, dict):
            rows.extend(flatten(v, key))
    return rows


def description_for(key: str, node: dict, comments: dict[str, str]) -> str:
    if key in comments:
        return comments[key]
    desc = node.get("description")
    if isinstance(desc, str) and desc.strip():
        return desc.strip()
    return ""


def render() -> str:
    schema = load_schema()
    values = yaml.safe_load(VALUES.read_text())
    comments = parse_comments(VALUES)
    lines = [INTRO.rstrip("\n")]
    for key, value in flatten(values):
        node = schema_node(schema, key)
        typ = type_of(node, value)
        default = "see below" if isinstance(value, dict) and value else format_default(value)
        desc = description_for(key, node, comments)
        rules = constraints(schema, node)
        lines.append(f"| `{key}` | {cell(typ)} | {cell(default)} | {cell(rules)} | {cell(desc)} |")
    lines.append(OUTRO.rstrip("\n"))
    lines.append("")
    return "\n".join(lines)


def main() -> int:
    sys.stdout.write(render())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
