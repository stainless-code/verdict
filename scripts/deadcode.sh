#!/bin/sh
# deadcode: ratchet against unreachable functions.
#
#   sh scripts/deadcode.sh v0.52.0     (make deadcode pins the version)
#
# `deadcode -test` counts helpers reachable only from a test, so what it
# reports is the code neither a test nor any call path can reach. The tool
# only sees this module, so pkg/verdict's exported SDK surface (dialled from
# other repositories) always looks dead; scripts/deadcode-baseline.txt records
# the entries we accept. A new entry fails the gate, and so does a baseline
# line that no longer reproduces, so the list cannot rot silently.
set -eu

VERSION="${1:?usage: deadcode.sh <vX.Y.Z>}"
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
BASELINE='scripts/deadcode-baseline.txt'

report="$(go run "golang.org/x/tools/cmd/deadcode@$VERSION" -test ./...)"
current="$(mktemp)"
baseline="$(mktemp)"
trap 'rm -f "$current" "$baseline"' EXIT

printf '%s\n' "$report" |
  sed -n 's/^\([^:]*\):[0-9]*:[0-9]*: unreachable func: \(.*\)$/\1: \2/p' |
  LC_ALL=C sort > "$current"
sed -e 's/#.*//' -e 's/[[:space:]]*$//' -e '/^[[:space:]]*$/d' "$BASELINE" |
  LC_ALL=C sort > "$baseline"

added="$(comm -23 "$current" "$baseline")"
gone="$(comm -13 "$current" "$baseline")"
status=0
if [ -n "$added" ]; then
  echo "deadcode: new unreachable function(s):" >&2
  printf '%s\n' "$added" | sed 's/^/  /' >&2
  echo "  -> delete them, or record the reason in $BASELINE" >&2
  status=1
fi
if [ -n "$gone" ]; then
  echo "deadcode: baseline entry no longer reported (delete the line):" >&2
  printf '%s\n' "$gone" | sed 's/^/  /' >&2
  status=1
fi
if [ "$status" -eq 0 ]; then
  echo "deadcode: no new unreachable functions ($(wc -l < "$current" | tr -d ' ') baselined)"
fi
exit "$status"
