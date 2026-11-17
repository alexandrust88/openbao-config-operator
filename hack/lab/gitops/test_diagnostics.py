import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('gitops_diagnostics', Path(__file__).with_name('diagnostics.py'))
diag = importlib.util.module_from_spec(spec)
spec.loader.exec_module(diag)


class DiagnosticsTests(unittest.TestCase):
    def test_only_safe_status_fields_are_printed(self):
        value = {'items': [{'kind': 'Policy', 'metadata': {'name': 'reader', 'namespace': 'lab', 'generation': 3, 'annotations': {'token': 'private'}},
                           'spec': {'password': 'private'}, 'data': {'token': 'private'},
                           'status': {'observedGeneration': 3, 'conditions': [
                               {'type': 'Ready', 'status': 'True', 'reason': 'Available', 'observedGeneration': 2, 'message': 'private'},
                               {'type': 'Synced', 'status': 'False', 'reason': 'ReconcileError', 'observedGeneration': 3, 'message': 'private'},
                               {'type': 'private', 'status': 'private', 'observedGeneration': 'private'}]}}]}
        response = type('Response', (), {'returncode': 0, 'stdout': json.dumps(value)})()
        output = io.StringIO()
        with patch.dict(os.environ, {'LAB_CONTEXT': 'kind-fixture'}), patch.object(diag.subprocess, 'run', return_value=response), contextlib.redirect_stdout(output):
            diag.report('policies.sys.openbaoconfig.io')
        text = output.getvalue()
        self.assertNotIn('private', text)
        self.assertIn('ReconcileError', text)
        self.assertIn('reader', text)

        record = json.loads(text)
        self.assertEqual(record['generation'], 3)
        self.assertEqual(record['observedGeneration'], 3)
        self.assertEqual([(c['type'], c['observedGeneration']) for c in record['conditions']], [('Ready', 2), ('Synced', 3)])

    def test_generation_fields_never_render_non_numeric_values(self):
        for value in ('private', True, -1, {}, None):
            self.assertIsNone(diag.generation(value))
        self.assertEqual(diag.generation(3), 3)
