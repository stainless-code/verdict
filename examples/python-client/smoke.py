"""Offline smoke test: success, typed refusal, exit 2 (flag and target), and the demo script."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from typed import report
from verdict_client import VerdictError, VerdictRefusal, _envelope, _refusal, run


def main(binary: str) -> int:
    root = Path(__file__).resolve().parent
    with tempfile.TemporaryDirectory() as tmp:
        shutil.copytree(root / "fixtures" / "bundle", Path(tmp) / ".verdict" / "state")
        env, data = report("acme/app", "123", cwd=tmp, binary=binary)
        assert env.schema_version == "1", env.schema_version
        assert data.verdict.open == 0, data.verdict
        try:
            report("acme/app", "404", cwd=tmp, binary=binary)
        except VerdictRefusal as exc:
            assert exc.code == "missing-findings", exc.code
            assert exc.hint.startswith("try:"), exc.hint
        else:
            raise AssertionError("missing findings did not refuse")
        try:
            run("report", ["acme/app", "123", "--nope"], cwd=tmp, binary=binary)
        except VerdictError as exc:
            assert exc.exit_code == 2, exc.exit_code
        else:
            raise AssertionError("bad flag did not exit 2")
        try:
            run("report", [], cwd=tmp, binary=binary)
        except VerdictRefusal as exc:
            assert exc.code == "target-unresolved", exc.code
            assert exc.exit_code == 2, exc.exit_code
        else:
            raise AssertionError("a missing target did not refuse")
        keys = {"verb": "report", "repo": "", "pr": 0, "at": "", "next": "", "warnings": []}
        bad_docs = [
            '{"schema_version":"2","data":null,"error":{"code":"x","message":"m"},' + json.dumps(keys)[1:],
            '{"data":null,"error":{"code":"x","message":"m"},"warnings":[]}',
        ]
        for text in bad_docs:
            try:
                _envelope(text)
            except VerdictError:
                pass
            else:
                raise AssertionError(f"malformed envelope accepted: {text}")
        for error in ({"code": "x", "message": None}, {"code": 1, "message": "m"}, None):
            doc = {"data": None, "error": error, "warnings": []}
            try:
                _refusal(doc, 2)
            except VerdictError:
                pass
            else:
                raise AssertionError(f"untyped refusal accepted: {error!r}")
        demo = subprocess.run(
            [sys.executable, str(root / "example.py"), "acme/app", "123", tmp, binary],
            capture_output=True, text=True, check=False,
        )
        assert demo.returncode == 0, demo.stderr
        assert "0 open" in demo.stdout, demo.stdout
    print("ok: success, refusal, exit 2, demo")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: smoke.py /path/to/verdict")
    raise SystemExit(main(sys.argv[1]))
