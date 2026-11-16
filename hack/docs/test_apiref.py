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
        self.assertEqual(len(self.pages), len(self.crds) + 7)

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
