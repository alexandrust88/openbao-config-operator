"""Generate the API reference (markdown) from the CRDs in config/crd/bases.

Usage: python hack/docs/apiref.py [<crd-dir>] [<out-dir>]
The mkdocs hook (hack/docs/hooks.py) calls render() during the build, so the pages are never committed. Each kind
shows its curated example (examples/<group>/<kind>.yaml), or else its generated sample (examples-generated/), included
with a pymdown snippet so the page always shows the file CI tests.
"""

import html
import json
import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
GROUP_ORDER = ["core", "sys", "auth", "secrets", "identity", "access"]
GROUP_LABELS = {"core": "Connection", "sys": "System", "auth": "Authentication", "secrets": "Secrets engine", "identity": "Identity", "access": "Access"}
GROUP_SUMMARIES = {
    "core": "The OpenBao server, the connections that log in to it and BaoEndpoint, the escape hatch for any API path.",
    "sys": "Server settings: namespaces, mounts, ACL policies, audit devices, quotas, plugins and logging.",
    "auth": "Auth method configuration, roles and users: Kubernetes, JWT and OIDC, AppRole, LDAP, certificates and cloud logins.",
    "secrets": "Secrets engine configuration, roles and secrets: KV, PKI, transit, databases, SSH and cloud credentials.",
    "identity": "Entities, groups and their aliases, login MFA, and the OpenBao OIDC provider and identity tokens.",
    "access": "Access models that write ACL policies for KV version 2 secrets, for workloads and for people.",
}
SKIPPED_ROOT_FIELDS = {"apiVersion", "kind", "metadata"}
# Links from a page under docs/reference/<dir>/ to a repository file; hooks.py turns them into GitHub links.
REPO_LINK = "../../../"


def load_crds(crd_dir):
    crds = []
    for path in sorted(Path(crd_dir).glob("*.yaml")):
        for doc in yaml.safe_load_all(path.read_text()):
            if doc and doc.get("kind") == "CustomResourceDefinition":
                crds.append(doc)
    return crds


def short_group(group):
    return group.split(".", 1)[0]


def examples_of(root, group, kind):
    """Repository-relative example files of a kind: (curated, curated variants, generated); missing ones are None."""
    name = kind.lower()
    short = short_group(group)
    curated = root / "examples" / short / f"{name}.yaml"
    generated = root / "examples-generated" / short / f"{name}.yaml"
    variants = sorted((root / "examples" / short).glob(f"{name}-*.yaml"))

    def rel(path):
        return path.relative_to(root).as_posix() if path.is_file() else None

    return rel(curated), [rel(v) for v in variants], rel(generated)


def snippet(path, indent=""):
    """A yaml fence that includes a repository file with pymdown snippets, titled with the file path."""
    return [f'{indent}```yaml title="{path}"', f'{indent}--8<-- "{path}"', f"{indent}```"]


def example_section(root, crd, heading):
    spec = crd["spec"]
    curated, variants, generated = examples_of(root, spec["group"], spec["names"]["kind"])
    shown = curated or generated
    if not shown:
        return []
    links = [f"[`{Path(shown).name}`]({REPO_LINK}{shown})"]
    links += [f"[`{Path(v).name}`]({REPO_LINK}{v})" for v in variants]
    if curated and generated:
        links.append(f"[generated sample with every field]({REPO_LINK}{generated})")
    return [f"{heading} Example", ""] + snippet(shown) + ["", "On GitHub: " + ", ".join(links) + ".", ""]


def group_key(group):
    name = short_group(group)
    return (GROUP_ORDER.index(name) if name in GROUP_ORDER else len(GROUP_ORDER), name)


def esc(text):
    return html.escape(str(text), quote=False)


def code(text):
    return f"<code>{esc(text)}</code>"


def inline_code(escaped):
    """Turn markdown `code` spans of already escaped text into <code>; the table is raw HTML, so markdown never runs."""
    return re.sub(r"`([^`]+)`", r"<code>\1</code>", escaped)


def break_after(escaped, *marks):
    """Allow line breaks after the given marks only, so paths and types never split mid-word."""
    for mark in marks:
        escaped = escaped.replace(mark, mark + "<wbr>")
    return escaped


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
                parts.append("<br>" + inline_code(esc(stripped)))
            else:
                parts.append(inline_code(esc(stripped)))
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
    """Short validation rules of one schema node (CEL excluded), as escaped HTML fragments."""
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
    return [(prefix + r)[:1].upper() + (prefix + r)[1:] for r in out]


def cel_rules(schema, prefix=""):
    """CEL rules of one schema node, as escaped HTML fragments."""
    out = []
    for rule in schema.get("x-kubernetes-validations", []):
        text = "CEL: " + code(rule["rule"])
        message = rule.get("message") or rule.get("messageExpression")
        if message:
            text += " (" + esc(message) + ")"
        if rule.get("fieldPath"):
            text += " on " + code(rule["fieldPath"])
        out.append(text)
    return [(prefix + r)[:1].upper() + (prefix + r)[1:] for r in out]


def is_scalar_container(schema):
    return "properties" not in schema and "items" not in schema and not isinstance(
        schema.get("additionalProperties"), dict
    )


def walk(schema, path, required, rows):
    """Append one row per field, depth first; scalar list items and map values fold into their parent row."""
    validation = rules(schema)
    cel = cel_rules(schema)
    child_paths = []
    items = schema.get("items")
    values = schema.get("additionalProperties")
    if isinstance(items, dict):
        if is_scalar_container(items):
            validation += rules(items, "each item ")
            cel += cel_rules(items, "each item: ")
        else:
            child_paths.append((items, path + "[]"))
    if isinstance(values, dict):
        if is_scalar_container(values):
            validation += rules(values, "each value ")
            cel += cel_rules(values, "each value: ")
        else:
            child_paths.append((values, path + ".<key>"))
    default = ""
    if "default" in schema:
        # Non-empty strings read better unquoted, like the enum values; "" and non-strings stay JSON.
        value = schema["default"]
        default = value if isinstance(value, str) and value else json.dumps(value)
    rows.append(
        {
            "path": path,
            "type": type_of(schema),
            "required": required,
            "default": default,
            "validation": validation,
            "cel": cel,
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
        '<div class="api-fields">',
        "<table>",
        "<thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead>",
        "<tbody>",
    ]
    for row in rows:
        path = f"<code>{break_after(esc(row['path']), '.')}</code>"
        kind = f"<code>{break_after(esc(row['type']), 'map[string]')}</code>"
        meta = (["Required"] if row["required"] else []) + (["Default: " + code(row["default"])] if row["default"] else [])
        meta += row["validation"]
        cell = describe(row["description"])
        if meta:
            cell += '<div class="api-meta">' + " · ".join(meta) + "</div>"
        if row["cel"]:
            cell += '<ul class="api-rules">' + "".join(f"<li>{r}</li>" for r in row["cel"]) + "</ul>"
        lines.append(f"<tr><td>{path}</td><td>{kind}</td><td>{cell}</td></tr>")
    lines += ["</tbody>", "</table>", "</div>"]
    return "\n".join(lines)


def kind_section(crd, root=ROOT, level=2):
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
    out = [f"{'#' * level} {names['kind']}", "", "  \n".join(facts), ""]
    heading = "#" * (level + (1 if len(spec["versions"]) == 1 else 2))
    for version in spec["versions"]:
        schema = version["schema"]["openAPIV3Schema"]
        if len(spec["versions"]) > 1:
            state = ", ".join(s for s, on in (("served", version.get("served")), ("storage", version.get("storage"))) if on)
            out += [f"{'#' * (level + 1)} {version['name']} ({state})", ""]
        if version.get("deprecated"):
            out += [f"Deprecated: {esc(version.get('deprecationWarning', 'yes'))}", ""]
        description = schema.get("description", "")
        example = example_section(root, crd, heading) if version.get("storage") else []
        if example:
            # The full example file follows, so the description's inline snippet would repeat it.
            description = description.split("\n\nExample:", 1)[0]
        description = describe(description)
        if description:
            out += [f"<div>{description}</div>", ""]
        if version.get("storage"):
            out += example
            notes = root / "hack/docs/resource-notes" / f"{names['kind'].lower()}.md"
            if notes.is_file():
                out += [f"{heading} Behaviour and permissions", "", notes.read_text().strip(), ""]
        out += [f"{heading} Fields", "", "Defaults below are CRD schema defaults. A field without a listed default does not imply zero, false or an empty value; see the field description for omission behaviour. Required fields inside an optional object apply only when that object is set.", ""]
        object_rules = rules(schema) + cel_rules(schema)
        if object_rules:
            out += ['<div class="api-rules"><strong>Object validation:</strong><ul>' + "".join(f"<li>{r}</li>" for r in object_rules) + "</ul></div>", ""]
        out += [field_table(schema), ""]
    return "\n".join(out)


def kind_filename(crd):
    """Stable same-depth URLs retain repository/example relative links."""
    return f"{short_group(crd['spec']['group'])}-{crd['spec']['names']['kind'].lower()}.md"


def render(crd_dir, root=ROOT):
    """Return the index, compact compatibility group indexes and one page per kind."""
    groups = {}
    for crd in load_crds(crd_dir):
        groups.setdefault(crd["spec"]["group"], []).append(crd)
    pages = {}
    index = [
        "---",
        "description: OpenBao Kubernetes CRD reference with field types, defaults, validation rules and example manifests generated from the schemas.",
        "---",
        "",
        "# API reference",
        "",
        "Generated from the CustomResourceDefinitions in `config/crd/bases`, which `controller-gen` builds from",
        "the Go types in `api/`. Every field lists its type, default, validation (OpenAPI and CEL) and",
        "description, after an example manifest. The [kind catalogue](../../kinds.md) has the implementation status of",
        "each kind; the [example gallery](../examples/index.md) lists every example.",
        "",
    ]
    for group in sorted(groups, key=group_key):
        crds = sorted(groups[group], key=lambda c: c["spec"]["names"]["kind"])
        name = short_group(group)
        label = GROUP_LABELS.get(name, name)
        index += [f"## {label}", "", f"[`{group}`]({name}.md): {GROUP_SUMMARIES.get(name, '')}", "",
                  " · ".join(f"[{c['spec']['names']['kind']}]({kind_filename(c)})" for c in crds), ""]
        page = ["---", f"title: {label} CRD reference",
                f"description: OpenBao {label.lower()} custom resource fields, defaults, validation rules and YAML examples for the {group} API group.",
                "---", "", f"# {group}", ""]
        page += [", ".join(f"[{c['spec']['names']['kind']}](#{c['spec']['names']['kind'].lower()})" for c in crds), ""]
        page += ["Choose a kind for its complete field reference, validation rules and example manifests.", ""]
        for crd in crds:
            kind = crd["spec"]["names"]["kind"]
            filename = kind_filename(crd)
            version = next(v for v in crd["spec"]["versions"] if v.get("storage"))
            summary = " ".join(version["schema"]["openAPIV3Schema"].get("description", "").split())
            summary = summary.split(". ", 1)[0].rstrip(".") + "." if summary else f"{kind} custom resource."
            # Keep these headings stable: published links use <group>.md#<kind>.
            page += [f"## {kind}", "", esc(summary), "",
                     f"[{kind} fields and YAML example]({filename}) · [Example gallery](../examples/{name}.md#{kind.lower()})", ""]
            description = f"{kind} Kubernetes CRD fields, defaults, validation and YAML examples for OpenBao. {summary}"
            heading, body = kind_section(crd, root, level=1).split("\n\n", 1)
            pages[f"reference/api/{filename}"] = "\n".join([
                "---", "title: " + json.dumps(f"{kind} CRD reference"),
                "description: " + json.dumps(description), "---", "", heading, "",
                f"[{label} resources]({name}.md) · [Kind catalogue](../../kinds.md) · [Examples](../examples/{name}.md#{kind.lower()})", "",
                body,
            ])
        pages[f"reference/api/{name}.md"] = "\n".join(page)
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
