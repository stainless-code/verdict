"""Offline adapter contract, invoked by the scanner-evidence shell eval."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ADAPTER = Path(__file__).with_name('scan.py')


class Contract(unittest.TestCase):
    def test_contract(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            checkout = root / 'checkout'
            checkout.mkdir()
            bundle = root / 'bundle'
            bundle.mkdir()
            (bundle / 'hunks.tsv').write_text('a.go\tNEW\t2\t4\n')
            (checkout / 'a.go').write_text('package a\n')
            subprocess.run(['git', 'init', '-q', str(checkout)], check=True)
            subprocess.run(['git', '-C', str(checkout), 'add', '.'], check=True)
            subprocess.run(['git', '-C', str(checkout), '-c', 'user.name=Test', '-c',
                            'user.email=test@example.com', 'commit', '-qm', 'fixture'], check=True)
            head = subprocess.check_output(['git', '-C', str(checkout), 'rev-parse', 'HEAD']).decode().strip()
            stub = root / 'opengrep'
            stub.write_text('''#!/bin/sh
if [ "$1" = --version ]; then echo 2.0.0; exit; fi
case " $* " in
  *" --metrics=off "*) ;;
  *) exit 9 ;;
esac
case " $* " in
  *" --disable-version-check "*) ;;
  *) exit 9 ;;
esac
case " $* " in
  *" --config /"*"/rules/starter.yaml "*) ;;
  *) exit 9 ;;
esac
cat <<'EOF'
{"paths":{"scanned":["a.go"]},"errors":[],"results":[
{"check_id":"verdict.go.insecure-tls","path":"a.go","start":{"line":3},"end":{"line":3},"extra":{"message":"password=hunter2","lines":"SECRET","dataflow_trace":"token=secret"}},
{"check_id":"verdict.go.shell-command","path":"a.go","start":{"line":8},"end":{"line":9},"extra":{}}]}
EOF
''')
            stub.chmod(0o700)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'])

            def invoke():
                return subprocess.run(['python3', str(ADAPTER), str(bundle), str(checkout), head], env=env, check=False).returncode

            self.assertEqual(invoke(), 0)
            raw = (bundle / 'x-scanner.json').read_text()
            evidence = json.loads(raw)
            self.assertEqual(evidence['status'], 'complete')
            self.assertEqual(evidence['tool'], {'name': 'opengrep', 'version': '2.0.0'})
            self.assertEqual(evidence['findings'][0]['path'], 'a.go')
            self.assertEqual(evidence['findings'][0]['start'], 3)
            self.assertEqual(evidence['findings'][0]['changed_hunk'], True)
            self.assertEqual(evidence['findings'][1]['changed_hunk'], False)
            self.assertRegex(evidence['rule_sha256'], r'^[a-f0-9]{64}$')
            for secret in ('hunter2', 'SECRET', 'token=secret'):
                self.assertNotIn(secret, raw + (bundle / 'x-scanner.md').read_text())
            original_stub = stub.read_text()
            for old, new in [('"scanned":["a.go"]', '"scanned":[]'),
                             ('"errors":[]', '"errors":[{"type":"unsupported language"}]')]:
                stub.write_text(original_stub.replace(old, new))
                self.assertEqual(invoke(), 1)
                self.assertEqual(json.loads((bundle / 'x-scanner.json').read_text())['status'], 'incomplete')
            for body in ('exit 7', 'echo 1.0.0', 'echo 2.0.0; exit 0', 'sleep 6'):
                stub.write_text('#!/bin/sh\n' + body + '\n')
                self.assertEqual(invoke(), 1)
                self.assertEqual(json.loads((bundle / 'x-scanner.json').read_text())['status'], 'incomplete')
            stub.unlink()
            self.assertEqual(invoke(), 1)


if __name__ == '__main__':
    unittest.main()
