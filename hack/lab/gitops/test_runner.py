"""Protect an existing cluster when discovery fails before lab ownership is established."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class RunnerTests(unittest.TestCase):
    def test_failed_cluster_discovery_does_not_create_or_delete(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            kind = root / 'kind'
            calls = root / 'calls'
            kind.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$CALLS"\nexit 7\n')
            kind.chmod(0o700)
            make = root / 'make'
            make.write_text('#!/bin/sh\nexit 0\n')
            make.chmod(0o700)
            env = {**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH'],
                   'KIND': str(kind), 'CALLS': str(calls), 'LAB_CONTEXT': 'kind-gitops-discovery-unit'}
            result = subprocess.run(['bash', str(Path(__file__).with_name('run.sh')), 'flux'], env=env,
                                    capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 7)
            self.assertEqual(calls.read_text().splitlines(), ['get clusters'])
