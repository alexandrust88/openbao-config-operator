"""Generate the API reference (markdown) from the CRDs in config/crd/bases.

Usage: python hack/docs/apiref.py [<crd-dir>] [<out-dir>]
The mkdocs hook (hack/docs/hooks.py) calls render() during the build, so the pages are never committed.
"""

import html
import json
import sys
from pathlib import Path

import yaml

GROUP_ORDER = ["core", "sys", "auth", "secrets", "identity", "access"]
SKIPPED_ROOT_FIELDS = {"apiVersion", "kind", "metadata"}


def load_crds(crd_dir):
    crds = []
    for path in sorted(Path(crd_dir).glob("*.yaml")):
        for doc in yaml.safe_load_all(path.read_text()):
            if doc and doc.get("kind") == "CustomResourceDefinition":
                crds.append(doc)
    return crds


def short_group(group):
    return group.split(".", 1)[0]


def group_key(group):
    name = short_group(group)
    return (GROUP_ORDER.index(name) if name in GROUP_ORDER else len(GROUP_ORDER), name)


def esc(text):
    return html.escape(str(text), quote=False)


def code(text):
    return f"<code>{esc(text)}</code>"


def describe(text):
    """Render a godoc-style description: tab-indented paragraphs are code, others are prose."""
    if not text:
        return ""
    out = []
    for para in text.strip("\n").split("\n\n"):
        lines = para.split("\n")
        if all(line.startswith("\t") or not line.strip() for line in lines):
            block = "\n".join(line[1:] if line.startswith("\t") else line for line in lines)
            out.append(f"<pre><code>{esc(block)}</code></pre>")
            continue
        parts = []
        for line in lines:
            stripped = line.strip()
            if parts and stripped.startswith(("- ", "* ")):
                parts.append("<br>" + esc(stripped))
            else:
                parts.append(esc(stripped))
        out.append(" ".join(parts).replace(" <br>", "<br>"))
    return "<br><br>".join(p for p in out if p)


def type_of(schema):
    if schema.get("x-kubernetes-int-or-string"):
        return "int-or-string"
    if schema.get("x-kubernetes-preserve-unknown-fields") and "properties" not in schema:
        return "object (any fields)"
    kind = schema.get("type", "")
    if kind == "array":
        return "[]" + type_of(schema.get("items", {}))
    if kind == "object" and isinstance(schema.get("additionalProperties"), dict):
        return "map[string]" + type_of(schema["additionalProperties"])
    if "anyOf" in schema or "oneOf" in schema:
        alts = schema.get("anyOf") or schema.get("oneOf")
        return " or ".join(type_of(alt) or "any" for alt in alts)
    fmt = schema.get("format")
    return f"{kind} ({fmt})" if fmt else kind


def bounds(lo, hi, lo_excl=False, hi_excl=False):
    left = "" if lo is None else (f">{lo}" if lo_excl else f">={lo}")
    right = "" if hi is None else (f"<{hi}" if hi_excl else f"<={hi}")
    return ", ".join(p for p in (left, right) if p)


def rules(schema, prefix=""):
    """Validation rules of one schema node, as escaped HTML fragments."""
    out = []
    if "enum" in schema:
        out.append("one of: " + ", ".join(code(json.dumps(v) if not isinstance(v, str) else v) for v in schema["enum"]))
    if "pattern" in schema:
        out.append("pattern: " + code(schema["pattern"]))
    for name, lo, hi in (
        ("length", "minLength", "maxLength"),
        ("items", "minItems", "maxItems"),
        ("properties", "minProperties", "maxProperties"),
    ):
        text = bounds(schema.get(lo), schema.get(hi))
        if text:
            out.append(f"{name}: {esc(text)}")
    text = bounds(
        schema.get("minimum"),
        schema.get("maximum"),
        schema.get("exclusiveMinimum") is True,
        schema.get("exclusiveMaximum") is True,
    )
    if text:
        out.append(f"value: {esc(text)}")
    if "multipleOf" in schema:
        out.append(f"multiple of: {esc(schema['multipleOf'])}")
    if schema.get("uniqueItems"):
        out.append("unique items")
    if schema.get("nullable"):
        out.append("nullable")
    if "x-kubernetes-list-type" in schema:
        text = f"list type: {schema['x-kubernetes-list-type']}"
        if schema.get("x-kubernetes-list-map-keys"):
            text += " (keys: " + ", ".join(schema["x-kubernetes-list-map-keys"]) + ")"
        out.append(esc(text))
    if "x-kubernetes-map-type" in schema:
        out.append(esc(f"map type: {schema['x-kubernetes-map-type']}"))
    for rule in schema.get("x-kubernetes-validations", []):
        text = "CEL: " + code(rule["rule"])
        message = rule.get("message") or rule.get("messageExpression")
        if message:
            text += " (" + esc(message) + ")"
        if rule.get("fieldPath"):
            text += " on " + code(rule["fieldPath"])
        out.append(text)
    return [prefix + r for r in out]


def is_scalar_container(schema):
    return "properties" not in schema and "items" not in schema and not isinstance(
        schema.get("additionalProperties"), dict
    )


def walk(schema, path, required, rows):
    """Append one row per field, depth first; scalar list items and map values fold into their parent row."""
    validation = rules(schema)
    child_paths = []
    items = schema.get("items")
    values = schema.get("additionalProperties")
    if isinstance(items, dict):
        if is_scalar_container(items):
            validation += rules(items, "each item: ")
        else:
            child_paths.append((items, path + "[]"))
    if isinstance(values, dict):
        if is_scalar_container(values):
            validation += rules(values, "each value: ")
        else:
            child_paths.append((values, path + ".<key>"))
    rows.append(
        {
            "path": path,
            "type": type_of(schema),
            "required": required,
            "default": json.dumps(schema["default"]) if "default" in schema else "",
            "validation": validation,
            "description": schema.get("description", ""),
        }
    )
    for child, child_path in child_paths:
        walk(child, child_path, False, rows)
    walk_properties(schema, path, rows)


def walk_properties(schema, path, rows):
    needed = set(schema.get("required", []))
    for name in sorted(schema.get("properties", {})):
        walk(schema["properties"][name], f"{path}.{name}" if path else name, name in needed, rows)


def field_table(schema):
    rows = []
    needed = set(schema.get("required", []))
    for name in sorted(schema.get("properties", {})):
        if name not in SKIPPED_ROOT_FIELDS:
            walk(schema["properties"][name], name, name in needed, rows)
    lines = [
        "<table>",
        "<thead><tr><th>Field</th><th>Type</th><th>Default</th><th>Validation</th><th>Description</th></tr></thead>",
        "<tbody>",
    ]
    for row in rows:
        kind = esc(row["type"]) + ("<br><strong>required</strong>" if row["required"] else "")
        default = code(row["default"]) if row["default"] else ""
        lines.append(
            f"<tr><td>{code(row['path'])}</td><td>{kind}</td><td>{default}</td>"
            f"<td>{'<br>'.join(row['validation'])}</td><td>{describe(row['description'])}</td></tr>"
        )
    lines += ["</tbody>", "</table>"]
    return "\n".join(lines)


def kind_section(crd):
    spec = crd["spec"]
    names = spec["names"]
    facts = [
        f"**API version:** `{spec['group']}/{', '.join(v['name'] for v in spec['versions'])}`",
        f"**Scope:** {spec['scope']}",
        f"**Resource:** `{names['plural']}.{spec['group']}`",
    ]
    if names.get("shortNames"):
        facts.append("**Short names:** " + ", ".join(f"`{n}`" for n in names["shortNames"]))
    if names.get("categories"):
        facts.append("**Categories:** " + ", ".join(f"`{n}`" for n in names["categories"]))
    out = [f"## {names['kind']}", "", "  \n".join(facts), ""]
    for version in spec["versions"]:
        schema = version["schema"]["openAPIV3Schema"]
        if len(spec["versions"]) > 1:
            state = ", ".join(s for s, on in (("served", version.get("served")), ("storage", version.get("storage"))) if on)
            out += [f"### {version['name']} ({state})", ""]
        if version.get("deprecated"):
            out += [f"Deprecated: {esc(version.get('deprecationWarning', 'yes'))}", ""]
        description = describe(schema.get("description", ""))
        if description:
            out += [f"<div>{description}</div>", ""]
        if rules(schema):
            out += ["<div><strong>Object validation:</strong><br>" + "<br>".join(rules(schema)) + "</div>", ""]
        out += [field_table(schema), ""]
    return "\n".join(out)


def render(crd_dir):
    """Return {relative markdown path: content} for the reference index and one page per API group."""
    groups = {}
    for crd in load_crds(crd_dir):
        groups.setdefault(crd["spec"]["group"], []).append(crd)
    pages = {}
    index = [
        "# API reference",
        "",
        "Generated from the CustomResourceDefinitions in `config/crd/bases`, which `controller-gen` builds from",
        "the Go types in `api/`. Every field lists its type, default, validation (OpenAPI and CEL) and",
        "description. The [kind catalogue](../../kinds.md) has the implementation status of each kind.",
        "",
        "| group | kinds |",
        "|-------|-------|",
    ]
    for group in sorted(groups, key=group_key):
        crds = sorted(groups[group], key=lambda c: c["spec"]["names"]["kind"])
        name = short_group(group)
        index.append(f"| [`{group}`]({name}.md) | {len(crds)} |")
        page = [f"# {group}", ""]
        page += [", ".join(f"[{c['spec']['names']['kind']}](#{c['spec']['names']['kind'].lower()})" for c in crds), ""]
        page += [kind_section(c) for c in crds]
        pages[f"reference/api/{name}.md"] = "\n".join(page)
    index.append("")
    pages["reference/api/index.md"] = "\n".join(index)
    return pages


def main(argv):
    root = Path(__file__).resolve().parents[2]
    crd_dir = Path(argv[1]) if len(argv) > 1 else root / "config/crd/bases"
    out_dir = Path(argv[2]) if len(argv) > 2 else root / ".tmp/apiref"
    pages = render(crd_dir)
    if not pages or len(pages) == 1:
        sys.exit(f"apiref: no CRDs found in {crd_dir}")
    for rel, content in pages.items():
        target = out_dir / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
    print(f"apiref: wrote {len(pages)} pages to {out_dir}")


if __name__ == "__main__":
    main(sys.argv)
