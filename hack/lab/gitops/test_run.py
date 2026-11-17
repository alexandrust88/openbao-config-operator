"""Exercise the portable per-cluster ownership lock without a real cluster."""
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[3]


class RunLockTests(unittest.TestCase):
    def test_concurrent_same_cluster_cannot_create_or_delete_sibling(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            kind = base / 'kind'
            kind.write_text('''#!/bin/sh
printf '%s\\n' "$*" >> "$LOCK_TEST_CALLS"
case "$1" in
 get) exit 0 ;;
 create)
  touch "$LOCK_TEST_STARTED"
  while [ ! -f "$LOCK_TEST_RELEASE" ]; do sleep 0.05; done
  exit 1 ;;
 delete) exit 0 ;;
esac
''')
            kind.chmod(0o700)
            python = base / 'python3'
            python.write_text('#!/bin/sh\nexit 0\n')
            python.chmod(0o700)
            make = base / 'make'
            make.write_text('#!/bin/sh\nexit 0\n')
            make.chmod(0o700)
            calls, started, release = (base / name for name in ('calls', 'started', 'release'))
            context = 'kind-obco-lock-test-' + str(os.getpid())
            env = {**os.environ, 'TMPDIR': directory, 'KIND': str(kind), 'LAB_CONTEXT': context,
                   'PATH': str(base) + os.pathsep + os.environ['PATH'], 'LOCK_TEST_CALLS': str(calls),
                   'LOCK_TEST_STARTED': str(started), 'LOCK_TEST_RELEASE': str(release)}
            command = ['bash', str(ROOT / 'hack/lab/gitops/run.sh'), 'flux']
            first = subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                deadline = time.monotonic() + 20
                while not started.exists() and first.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.05)
                self.assertTrue(started.exists(), 'First process must own the lock before the second starts')
                second = subprocess.run(command, cwd=ROOT, env=env, capture_output=True, text=True, timeout=10)
                self.assertNotEqual(second.returncode, 0)
                self.assertIn('already holds the cluster lock', second.stderr)
                self.assertEqual(sum(line.startswith('create ') for line in calls.read_text().splitlines()), 1)
                self.assertFalse(any(line.startswith('delete ') for line in calls.read_text().splitlines()))
            finally:
                release.touch()
                first.communicate(timeout=10)
            self.assertNotEqual(first.returncode, 0)
            self.assertFalse(any(line.startswith('delete ') for line in calls.read_text().splitlines()))
            self.assertFalse(list(base.glob('openbao-gitops-*.lock')))


if __name__ == '__main__':
    unittest.main()
