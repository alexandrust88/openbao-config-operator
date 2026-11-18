"""The Helm values reference page must match the generator and list every values.yaml key."""
import json
import re
import tempfile
import unittest
from pathlib import Path

import helmvalues

ROOT = Path(__file__).resolve().parents[2]
KEY = re.compile(r"^(\s*)([A-Za-z0-9_.-]+)\s*:")


def values_keys(path: Path) -> set[str]:
    """Dotted keys from values.yaml maps; do not expand fields inside list items."""
    stack: list[tuple[int, str]] = []
    keys: set[str] = set()
    list_indent: int | None = None
    for line in path.read_text().splitlines():
        stripped = line.lstrip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        if list_indent is not None and indent > list_indent:
            continue
        list_indent = None
        if stripped.startswith("- "):
            list_indent = indent
            continue
        m = KEY.match(line)
        if not m:
            continue
        while stack and stack[-1][0] >= indent:
            stack.pop()
        path_key = ".".join([p for _, p in stack] + [m.group(2)])
        keys.add(path_key)
        stack.append((indent, m.group(2)))
    return keys


class HelmValuesDocTest(unittest.TestCase):
    def test_every_values_key_on_page(self):
        page = (ROOT / "docs/reference/helm-values.md").read_text()
        missing = sorted(k for k in values_keys(ROOT / "charts/openbao-config-operator/values.yaml") if f"`{k}`" not in page)
        self.assertEqual(missing, [])

    def test_page_is_generated(self):
        page = (ROOT / "docs/reference/helm-values.md").read_text()
        self.assertEqual(helmvalues.render(), page, "run make helm-values-docs")

    def test_continuation_starting_with_flag_stays_in_description(self):
        with tempfile.TemporaryDirectory() as tmp:
            values = Path(tmp) / "values.yaml"
            values.write_text("# -- Extra arguments (--a,\n# --b) are refused.\nextraArgs: []\n")
            self.assertEqual(helmvalues.parse_comments(values), {"extraArgs": "Extra arguments (--a, --b) are refused."})

    def test_schema_only_key_is_global(self):
        schema = json.loads((ROOT / "charts/openbao-config-operator/values.schema.json").read_text())
        values_top = {k.split(".")[0] for k in values_keys(ROOT / "charts/openbao-config-operator/values.yaml")}
        self.assertEqual(sorted(set(schema["properties"]) - values_top), ["global"])


if __name__ == "__main__":
    unittest.main()
