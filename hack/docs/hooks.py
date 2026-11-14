"""MkDocs hooks: build the home page from README.md, the ADR index and the API reference, expand directory nav
entries, and point relative links that leave docs/ at the repository on GitHub."""

import posixpath
import re
import sys
from pathlib import Path

from mkdocs.structure.files import File

sys.path.insert(0, str(Path(__file__).resolve().parent))
import apiref  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
BRANCH = "main"
LINK = re.compile(r"(\]\()([^)\s]+)((?:\s+\"[^\"]*\")?\))")
FENCE = re.compile(r"^\s*(```|~~~)")

generated = {}
sources = {}


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
    if not (docs / "index.md").exists():
        generated["index.md"] = (ROOT / "README.md").read_text()
        sources["index.md"] = "README.md"
    if not (docs / "adr/index.md").exists():
        generated["adr/index.md"] = adr_index(docs)
    generated.update(apiref.render(ROOT / "config/crd/bases"))
    config["nav"] = expand(config["nav"], docs)
    return config


def expand(nav, docs):
    """Turn a nav value ending in '/' into the index page plus every other page of that directory."""
    if isinstance(nav, list):
        return [expand(item, docs) for item in nav]
    if isinstance(nav, dict):
        return {title: expand(value, docs) for title, value in nav.items()}
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
