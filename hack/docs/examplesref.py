"""Generate the example gallery (markdown) from examples/ and examples-generated/.

Usage: python hack/docs/examplesref.py [<out-dir>]
The mkdocs hook (hack/docs/hooks.py) calls pages() during the build, like apiref.render(), so the pages are never
committed. Every manifest is included with a pymdown snippet, so the gallery always shows the files CI tests. The
group pages use content tabs (pymdownx.tabbed) for the curated example, its variants and the generated sample.
"""

import html
import re
import sys
from pathlib import Path

import apiref

REPO_LINK = apiref.REPO_LINK
# Example sets that are not one kind per file; listed when the directory exists.
OTHER_SETS = [
    ("examples/quickstart", "the manifests of the quickstart tutorial"),
    ("examples/tutorials", "the manifests of the role-based tutorials, in the order they build on each other"),
    ("examples/policies/templated", "templated ACL policies, in the order they build on each other"),
    ("examples/live", "the live kit: kustomize overlays that apply every kind to a real OpenBao, by login mode"),
    ("examples/self-init", "an OpenBao self-init configuration and its verification script"),
]


def inline(text):
    """Escape text for a markdown table cell, leaving `code spans` as they are."""
    parts = text.split("`")
    for i in range(0, len(parts), 2):
        parts[i] = html.escape(parts[i], quote=False)
    return "`".join(parts).replace("|", "\\|")


def first_sentence(text):
    text = " ".join(text.split())
    match = re.search(r"\.(\s|$)", text)
    return text[: match.start() + 1] if match else text


def scenario(path):
    """First sentence of the comment block at the top of a manifest, or "" when it has none."""
    lines = []
    for line in path.read_text().splitlines():
        if not line.startswith("#"):
            break
        lines.append(line.lstrip("#").strip())
    return first_sentence(" ".join(lines))


def summary(crd):
    version = next(v for v in crd["spec"]["versions"] if v.get("storage"))
    description = version["schema"]["openAPIV3Schema"].get("description", "")
    return first_sentence(description.split("\n\n", 1)[0])


def kinds_by_group(root):
    groups = {}
    for crd in apiref.load_crds(root / "config/crd/bases"):
        groups.setdefault(crd["spec"]["group"], []).append(crd)
    return [
        (group, sorted(groups[group], key=lambda c: c["spec"]["names"]["kind"]))
        for group in sorted(groups, key=apiref.group_key)
    ]


def variant_name(kind, path):
    return Path(path).stem.removeprefix(kind.lower() + "-")


def index_page(root, groups):
    out = [
        "# Examples",
        "",
        "Curated examples live in `examples/<group>/<kind>.yaml`: realistic manifests, most with a comment that",
        "explains the scenario. Generated samples live in `examples-generated/` (`make samples`): one per kind, with",
        "every spec field set to a placeholder that passes the CRD validation. CI applies both with a server-side",
        "dry run, and each release attaches them as `examples-<version>.tar.gz`.",
        "",
        "Secret inputs are always references to a Kubernetes Secret by name and key; create the Secret first.",
        "",
    ]
    for group, crds in groups:
        short = apiref.short_group(group)
        out += [
            f"## {group}",
            "",
            f"[All {short} examples on one page]({short}.md)",
            "",
            "| Kind | Scenario | Example | Files |",
            "|------|----------|---------|-------|",
        ]
        for crd in crds:
            kind = crd["spec"]["names"]["kind"]
            curated, variants, generated = apiref.examples_of(root, group, kind)
            shown = curated or generated
            text = scenario(root / shown) if curated else ""
            files = [f"[{Path(p).name}]({REPO_LINK}{p})" for p in [curated, *variants] if p]
            if generated:
                files.append(f"[generated]({REPO_LINK}{generated})")
            files.append(f"[API](../api/{short}.md#{kind.lower()})")
            source = "curated" if curated else "generated"
            if variants:
                source += f" + {len(variants)} variant{'s' if len(variants) > 1 else ''}"
            out.append(
                f"| [{kind}]({short}.md#{kind.lower()}) | {inline(text or summary(crd))} | {source} | "
                + " · ".join(files)
                + " |"
            )
        out.append("")
    others = [(path, text) for path, text in OTHER_SETS if (root / path).is_dir()]
    if others:
        out += ["## Other example sets", ""]
        out += [f"- [`{path}/`]({REPO_LINK}{path}): {text}." for path, text in others]
        out.append("")
    return "\n".join(out)


def group_page(root, group, crds):
    short = apiref.short_group(group)
    out = [
        f"# {group} examples",
        "",
        "Each kind shows its curated example first, then any variants, then the generated sample with every spec",
        f"field set. The fields are described in the [API reference](../api/{short}.md).",
        "",
    ]
    for crd in crds:
        kind = crd["spec"]["names"]["kind"]
        curated, variants, generated = apiref.examples_of(root, group, kind)
        if not (curated or generated):
            continue
        text = scenario(root / curated) if curated else ""
        out += [f"## {kind}", "", inline(text or summary(crd)), ""]
        out += [f"[API reference](../api/{short}.md#{kind.lower()})", ""]
        tabs = [("Curated", curated)] if curated else []
        tabs += [(f"Variant: {variant_name(kind, v)}", v) for v in variants]
        if generated:
            tabs.append(("Generated (every field)", generated))
        for title, path in tabs:
            out += [f'=== "{title}"', ""] + apiref.snippet(path, "    ") + [""]
    return "\n".join(out)


def pages(root: Path) -> dict[str, str]:
    """Return {docs-relative markdown path: content} for the gallery index and one page per API group."""
    groups = kinds_by_group(root)
    out = {"reference/examples/index.md": index_page(root, groups)}
    for group, crds in groups:
        out[f"reference/examples/{apiref.short_group(group)}.md"] = group_page(root, group, crds)
    return out


def main(argv):
    root = apiref.ROOT
    out_dir = Path(argv[1]) if len(argv) > 1 else root / ".tmp/examplesref"
    result = pages(root)
    if len(result) == 1:
        sys.exit("examplesref: no CRDs found in config/crd/bases")
    for rel, content in result.items():
        target = out_dir / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
    print(f"examplesref: wrote {len(result)} pages to {out_dir}")


if __name__ == "__main__":
    main(sys.argv)
