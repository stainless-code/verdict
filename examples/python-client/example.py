"""Run the client against a local `.verdict/state/` bundle.

Usage: python3 example.py [REPO PR [CWD [VERDICT_BIN]]]
"""

from __future__ import annotations

import sys

from typed import report
from verdict_client import VerdictRefusal


def main(argv: list[str]) -> int:
    repo = argv[1] if len(argv) > 1 else "acme/app"
    pr = argv[2] if len(argv) > 2 else "123"
    cwd = argv[3] if len(argv) > 3 else None
    binary = argv[4] if len(argv) > 4 else None
    try:
        env, data = report(repo, pr, cwd=cwd, binary=binary)
    except VerdictRefusal as exc:
        print(f"refused: {exc.code}: {exc.message}")
        print(exc.hint)
        return 1
    print(f"{env.verb} {env.repo}#{env.pr}: {data.verdict.open} open")
    for warning in env.warnings:
        print(f"warn: {warning}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
