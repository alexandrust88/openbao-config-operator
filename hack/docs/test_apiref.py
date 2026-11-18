"""Generated API navigation, compatibility anchors and per-kind page contracts."""
import re
import unittest

import yaml

import apiref
import examplesref


class ReferencePagesTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.crds = apiref.load_crds(apiref.ROOT / 'config/crd/bases')
        cls.pages = apiref.render(apiref.ROOT / 'config/crd/bases')

    def test_every_kind_has_unique_page_and_metadata(self):
        targets = set()
        for crd in self.crds:
            kind = crd['spec']['names']['kind']
            target = 'reference/api/' + apiref.kind_filename(crd)
            self.assertNotIn(target, targets)
            targets.add(target)
            page = self.pages[target]
            metadata = yaml.safe_load(page.split('---', 2)[1])
            self.assertEqual(metadata['title'], kind + ' CRD reference')
            self.assertIn(kind, metadata['description'])
            self.assertIn('\n# ' + kind + '\n', page)
            self.assertIn('\n## Fields\n', page)
            self.assertIn('## Example', page)
            self.assertTrue(page.split('---', 2)[2].lstrip().startswith('# ' + kind + '\n'))
            self.assertNotIn('Example:', page.split('## Example')[0])
            self.assertIn('](' + apiref.kind_filename(crd) + ')', self.pages['reference/api/index.md'])
        self.assertEqual(len(self.pages), len(self.crds) + 7)

    def test_field_tables_have_three_columns_and_inline_facts(self):
        page = self.pages['reference/api/sys-baonamespace.md']
        self.assertIn('<div class="api-fields">', page)
        self.assertIn('<thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead>', page)
        self.assertNotIn('<details>', page)
        self.assertNotIn('`custom_metadata`', page)
        self.assertIn('<code>custom_metadata</code>', page)
        self.assertIn('Default: <code>Create</code> · One of: <code>Create</code>', page)
        for row in re.findall(r'^<tr><td>.*</td></tr>$', page, re.M):
            self.assertEqual(row.count('<td>'), 3, row)

    def test_published_group_anchors_remain_and_link_to_full_reference(self):
        for crd in self.crds:
            group = apiref.short_group(crd['spec']['group'])
            kind = crd['spec']['names']['kind']
            page = self.pages[f'reference/api/{group}.md']
            self.assertIn('\n## ' + kind + '\n', page)
            self.assertIn('](' + apiref.kind_filename(crd) + ')', page)
            self.assertNotIn('## Fields', page)
        self.assertLess(len(self.pages['reference/api/auth.md']), 25000)

    def test_catalogue_links_every_crd_and_does_not_link_untyped_features(self):
        catalogue = (apiref.ROOT / 'docs/kinds.md').read_text()
        for crd in self.crds:
            kind = crd['spec']['names']['kind']
            target = 'reference/api/' + apiref.kind_filename(crd)
            self.assertIn(f'[`{kind}`]({target})', catalogue)
        for target in re.findall(r'\]\((reference/api/[^)#]+)', catalogue):
            self.assertIn(target, self.pages)

    def test_gallery_links_to_per_kind_fields(self):
        pages = examplesref.pages(apiref.ROOT)
        for crd in self.crds:
            group = apiref.short_group(crd['spec']['group'])
            self.assertIn('../api/' + apiref.kind_filename(crd), pages[f'reference/examples/{group}.md'])


if __name__ == '__main__':
    unittest.main()
