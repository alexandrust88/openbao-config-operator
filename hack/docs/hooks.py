"""MkDocs hooks: build the home page from README.md when docs/index.md is absent, the ADR index, the API reference
and the examples gallery, expand directory nav entries, point relative links that leave docs/ at the repository on
GitHub, replace the @VERSION@ and @TAG@ tokens with the current release, and write redirect pages for moved pages."""

import html
import logging
import os
import posixpath
import re
import sys
from pathlib import Path

from mkdocs.structure.files import File
from mkdocs.utils import get_relative_url

sys.path.insert(0, str(Path(__file__).resolve().parent))
import apiref  # noqa: E402
import examplesref  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
BRANCH = "main"
LINK = re.compile(r"(\]\()([^)\s]+)((?:\s+\"[^\"]*\")?\))")
FENCE = re.compile(r"^\s*(```|~~~)")
TAG_PATTERN = re.compile(r"^v\d+\.\d+\.\d+$")
# Pygments may wrap the "@" of a token in its own span inside a code block; keep those tags around the value.
TOKEN = re.compile(r"@((?:</?span[^>]*>)*)(VERSION|TAG)((?:</?span[^>]*>)*)@")

# Old docs path -> new docs path. Every moved page keeps its old URL as a redirect page.
REDIRECTS = {
    "getting-started.md": "get-started/index.md",
    "operations/install.md": "get-started/install.md",
    "operations/connections.md": "get-started/connect.md",
    "operations/README.md": "operate/index.md",
    "operations/backup-restore.md": "operate/backup-restore.md",
    "operations/tenant-quotas.md": "operate/tenant-quotas.md",
    "operations/openbao-policy.md": "operate/openbao-policy.md",
    "operations/troubleshooting.md": "operate/troubleshooting.md",
    "operations/runbooks.md": "operate/runbooks.md",
    "operations/self-init.md": "guides/self-init.md",
    "operations/argocd.md": "guides/argocd.md",
    "operations/migration.md": "guides/migration.md",
    "upgrade.md": "operate/upgrade.md",
    "load-testing.md": "operate/load-testing.md",
    "live-testing.md": "operate/live-testing.md",
    "import.md": "guides/import.md",
    "templated-policies.md": "guides/templated-policies.md",
    "backlog.md": "project/backlog.md",
    "tech-debt.md": "project/tech-debt.md",
    "verification.md": "project/verification.md",
}

log = logging.getLogger("mkdocs.hooks")
generated = {}
sources = {}
state = {}


def release_tag():
    tag = os.environ.get("RELEASE_TAG", "").strip()
    if tag and not TAG_PATTERN.fullmatch(tag):
        raise ValueError("RELEASE_TAG must be a published vMAJOR.MINOR.PATCH release")
    return tag


def adr_index(docs):
    lines = ["# Architecture decision records", "", "| ADR | status |", "|-----|--------|"]
    for path in sorted((docs / "adr").glob("[0-9]*.md")):
        text = path.read_text().splitlines()
        title = next((line[2:] for line in text if line.startswith("# ")), path.stem)
        status = next((line for line in text if line.startswith("Status:")), "")
        status = status.removeprefix("Status:").split(". Date:")[0].strip()
        lines.append(f"| [{title}]({path.name}) | {status} |")
    return "\n".join(lines) + "\n"


def on_config(config):
    docs = Path(config["docs_dir"])
    generated.clear()
    sources.clear()
    tag = release_tag()
    config["extra"]["release_tag"] = tag
    tag = tag or "vX.Y.Z"
    state["tokens"] = {"TAG": tag, "VERSION": tag.removeprefix("v")}
    if not (docs / "index.md").exists():
        generated["index.md"] = (ROOT / "README.md").read_text()
        sources["index.md"] = "README.md"
    # README.md is the hand-written index; the generated status table only fills in when it is absent.
    if not (docs / "adr/index.md").exists() and not (docs / "adr/README.md").exists():
        generated["adr/index.md"] = adr_index(docs)
    generated.update(apiref.render(ROOT / "config/crd/bases"))
    generated.update(examplesref.pages(ROOT))
    config["nav"] = expand(config["nav"], docs)
    return config


def expand(nav, docs):
    """Turn a nav value ending in '/' into the index page plus every other page of that directory.

    A directory entry inside a list is flattened into that list, and a page already named there is kept once.
    """
    if isinstance(nav, list):
        flat = []
        seen = set()
        for item in nav:
            expanded = expand(item, docs)
            items = expanded if isinstance(expanded, list) else [expanded]
            for page in items:
                if isinstance(page, str):
                    if page in seen:
                        continue
                    seen.add(page)
                flat.append(page)
        return flat
    if isinstance(nav, dict):
        return {title: expand(value, docs) for title, value in nav.items()}
    if nav == "reference/api/":
        result = ["reference/api/index.md"]
        for group in apiref.GROUP_ORDER:
            index = f"reference/api/{group}.md"
            if index in generated:
                kinds = sorted(p for p in generated if p.startswith(f"reference/api/{group}-"))
                result.append({apiref.GROUP_LABELS[group]: [index, *kinds]})
        return result
    if isinstance(nav, str) and nav.endswith("/"):
        on_disk = [p.relative_to(docs).as_posix() for p in (docs / nav).glob("*.md")]
        pages = sorted(set(on_disk) | {p for p in generated if posixpath.dirname(p) + "/" == nav})
        index = [p for p in pages if posixpath.basename(p) in ("index.md", "README.md")]
        return index + [p for p in pages if p not in index]
    return nav


def on_files(files, config):
    for src_uri, content in generated.items():
        if files.get_file_from_path(src_uri) is None:
            files.append(File.generated(config, src_uri, content=content))
    state["files"] = files
    return files


def target_for(link, page_src, page_uri, files, repo_url):
    if re.match(r"^[a-z][a-z0-9+.-]*:|^#|^/", link):
        return link
    path, sep, anchor = link.partition("#")
    if not path:
        return link
    resolved = posixpath.normpath(posixpath.join(posixpath.dirname(page_src), path))
    if resolved.startswith("../"):
        return link
    if resolved == "docs" or resolved.startswith("docs/"):
        doc = posixpath.relpath(resolved, "docs")
        if files.get_file_from_path(doc) is None:
            index = next(
                (f"{doc}/{n}" for n in ("index.md", "README.md") if files.get_file_from_path(f"{doc}/{n}")),
                None,
            )
            if index is None:
                if not (ROOT / resolved).is_dir():
                    return link
                return f"{repo_url.rstrip('/')}/tree/{BRANCH}/{resolved}" + sep + anchor
            doc = index
        new = posixpath.relpath(doc, posixpath.dirname(page_uri) or ".")
        return new + sep + anchor
    kind = "tree" if (ROOT / resolved).is_dir() else "blob"
    return f"{repo_url.rstrip('/')}/{kind}/{BRANCH}/{resolved}" + sep + anchor


def on_page_markdown(markdown, page, config, files):
    src_uri = page.file.src_uri
    page_src = sources.get(src_uri, f"docs/{src_uri}")
    out, fenced = [], False
    for line in markdown.split("\n"):
        if FENCE.match(line):
            fenced = not fenced
        elif not fenced:
            # GitHub ends a table at a heading; Python-Markdown needs a blank line.
            if line.startswith("#") and out and out[-1].startswith("|"):
                out.append("")
            line = LINK.sub(
                lambda m: m.group(1) + target_for(m.group(2), page_src, src_uri, files, config["repo_url"]) + m.group(3),
                line,
            )
        out.append(line)
    return "\n".join(out)


def replace_tokens(text):
    return TOKEN.sub(lambda m: m.group(1) + state["tokens"][m.group(2)] + m.group(3), text)


def on_page_content(content, page, config, files):
    return replace_tokens(content)


def on_post_page(output, page, config):
    return replace_tokens(output)


REDIRECT_PAGE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Moved to {title}</title>
<link rel="canonical" href="{canonical}">
<meta name="robots" content="noindex">
<meta http-equiv="refresh" content="0; url={target}">
<script>location.replace("{target}" + location.hash);</script>
</head>
<body>
<p>This page moved to <a href="{target}">{title}</a>.</p>
</body>
</html>
"""


def on_post_template(output, template_name, config):
    return replace_tokens(output)


def on_post_build(config):
    files = state["files"]
    for old, new in REDIRECTS.items():
        existing = files.get_file_from_path(old)
        if existing is not None and existing.inclusion.is_included():
            log.info("redirect %s skipped: the page still exists", old)
            continue
        target = files.get_file_from_path(new)
        if target is None:
            log.warning("redirect %s: target %s is not a page", old, new)
            continue
        source = File(old, config["docs_dir"], config["site_dir"], config["use_directory_urls"])
        if source.url == target.url:
            continue
        rel = get_relative_url(target.url or ".", source.url or ".")
        dest = Path(source.abs_dest_path)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(
            REDIRECT_PAGE.format(
                title=html.escape(new),
                canonical=html.escape(config["site_url"].rstrip("/") + "/" + target.url),
                target=html.escape(rel),
            )
        )
