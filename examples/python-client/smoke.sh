#!/usr/bin/env sh
# Offline smoke test. Skips (exit 0) when python3 is absent, so it is safe to
# run from any gate; it never invokes pip and never touches the network.
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  echo "skip: python3 not found"
  exit 0
fi

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)
BIN=${VERDICT_BIN:-"$ROOT/bin/verdict"}
if [ ! -x "$BIN" ]; then
  echo "smoke: missing $BIN (run make build)" >&2
  exit 1
fi

exec python3 "$ROOT/examples/python-client/smoke.py" "$BIN"
