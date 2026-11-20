#!/usr/bin/env python3
"""Operator-invoked, local-only scanner evidence pilot (POSIX)."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import resource
import signal
import subprocess
import sys
import tempfile

VERSION = '2.0.0'
CAP = 1024 * 1024
RULES = Path(__file__).resolve().parent / 'rules' / 'starter.yaml'
MESSAGES = {
    'verdict.go.insecure-tls': 'TLS certificate verification is disabled.',
    'verdict.go.shell-command': 'A shell command needs argument and trust review.',
    'verdict.ts.eval': 'Dynamic evaluation needs trust review.',
    'verdict.ts.loose-false': 'Loose equality with false may coerce unexpected values.',
}


def limits():
    if sys.platform != "darwin":
        resource.setrlimit(resource.RLIMIT_AS, (2 * 1024**3, 2 * 1024**3))
    resource.setrlimit(resource.RLIMIT_FSIZE, (CAP, CAP))
    resource.setrlimit(resource.RLIMIT_CPU, (60, 60))


def run(argv, cwd, seconds):
    with tempfile.TemporaryDirectory() as home, tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        env = {k: v for k, v in os.environ.items()
               if k in ('PATH', 'SYSTEMROOT', 'TMPDIR')}
        env.update(HOME=home, SEMGREP_SEND_METRICS='off', OPENGREP_SEND_METRICS='off')
        proc = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                                start_new_session=True, preexec_fn=limits)
        try:
            code = proc.wait(timeout=seconds)
        except BaseException:
            os.killpg(proc.pid, signal.SIGKILL)
            proc.wait()
            raise
        out.seek(0)
        data = out.read(CAP + 1)
        if len(data) >= CAP:
            raise ValueError('output-limit')
        return code, data


def scan(args, evidence):
    checkout = Path(args.checkout).resolve(strict=True)
    code, head = run(['git', 'rev-parse', 'HEAD'], str(checkout), 5)
    if code or head.decode().strip() != args.commit:
        raise ValueError('checkout-mismatch')
    code, dirty = run(['git', 'status', '--porcelain', '--untracked-files=all'],
                      str(checkout), 5)
    if code or dirty:
        raise ValueError('checkout-not-clean')
    hunks = []
    with (Path(args.bundle) / 'hunks.tsv').open('rb') as handle:
        raw = handle.read(CAP + 1)
    if len(raw) > CAP:
        raise ValueError('hunks-limit')
    for row in raw.decode().splitlines():
        path, side, start, end = row.split('\t')
        if side not in ('NEW', 'OLD') or int(start) < 1 or int(end) < int(start):
            raise ValueError('invalid-hunks')
        hunks.append((path, side, int(start), int(end)))
    code, version = run(['opengrep', '--version'], str(checkout), 5)
    if code or version.decode().strip() != VERSION:
        raise ValueError('version-mismatch')
    evidence['tool']['version'] = VERSION
    code, output = run(['opengrep', 'scan', '--config', str(RULES), '--json',
                        '--metrics=off', '--disable-version-check', '--disable-nosem',
                        '--timeout', '10', '--max-memory', '1024', '--jobs', '1',
                        '--dataflow-traces', '.'], str(checkout), 90)
    if code:
        raise ValueError('scanner-failed')
    result = json.loads(output)
    if result.get('errors') or not result.get('paths', {}).get('scanned'):
        raise ValueError('scanner-errors-or-no-supported-files')
    scanned = set(result['paths']['scanned'])
    if any(side == 'NEW' and path not in scanned and './' + path not in scanned
           for path, side, _, _ in hunks):
        raise ValueError('unsupported-or-unscanned-changed-file')
    matches = result['results']
    if len(matches) > 100:
        raise ValueError('finding-limit')
    for match in matches:
        rule = match['check_id'].split('verdict.', 1)[-1]
        rule = 'verdict.' + rule
        if rule not in MESSAGES:
            raise ValueError('unknown-rule')
        path = Path(match['path'])
        resolved = (checkout / path).resolve()
        relative = resolved.relative_to(checkout).as_posix()
        # Only conservative path characters enter model context; no source text does.
        safe_path = relative if re.fullmatch(r'[\w./-]{1,240}', relative, re.ASCII) else '[redacted-path]'
        start, end = int(match['start']['line']), int(match['end']['line'])
        if start < 1 or end < start:
            raise ValueError('invalid-location')
        changed = any(p == relative and side == 'NEW' and start <= hi and end >= lo
                      for p, side, lo, hi in hunks)
        evidence['findings'].append(dict(rule_id=rule, rule_sha256=evidence['rule_sha256'],
            path=safe_path, start=start, end=end, changed_hunk=changed,
            message=MESSAGES[rule], severity='WARNING',
            trace='[source and dataflow content withheld]'))
    evidence['status'] = 'complete'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bundle')
    parser.add_argument('checkout')
    parser.add_argument('commit', help='full pinned HEAD SHA; checkout must be clean')
    args = parser.parse_args()
    bundle = Path(args.bundle).resolve(strict=True)
    evidence = dict(schema_version='1', status='incomplete', tool=dict(name='opengrep', version=None),
                    commit=args.commit if re.fullmatch('[0-9a-f]{40,64}', args.commit) else None,
                    rule_sha256=hashlib.sha256(RULES.read_bytes()).hexdigest(), findings=[])
    try:
        scan(args, evidence)
    except (OSError, ValueError, KeyError, TypeError, AttributeError, OverflowError, subprocess.SubprocessError):
        evidence.update(status='incomplete', reason='scan failed, unsupported coverage, or limit reached')
    text = ['# Scanner candidates', '', 'Status: ' + evidence['status'],
            'Tool: opengrep ' + (evidence['tool']['version'] or 'unverified'),
            'Commit: ' + (evidence['commit'] or 'invalid'),
            'Rule-file SHA-256: ' + evidence['rule_sha256'],
            'Rule matches are candidates; no bug is reproduced. A clean scan never means safe.', '']
    for finding in evidence['findings']:
        text.append('{rule_id}: {path}:{start}-{end} changed_hunk={changed_hunk} — {message}'.format(**finding))
    for name, data in [('x-scanner.json', json.dumps(evidence, indent=2) + '\n'),
                       ('x-scanner.md', '\n'.join(text) + '\n')]:
        fd, temp = tempfile.mkstemp(prefix='.x-scanner-', dir=bundle)
        try:
            with os.fdopen(fd, 'w', encoding='utf-8') as handle:
                handle.write(data)
            os.replace(temp, bundle / name)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
    return 0 if evidence['status'] == 'complete' else 1


if __name__ == '__main__':
    raise SystemExit(main())
