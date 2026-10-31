#!/usr/bin/env bash
# evals/run.sh — run the offline contract evals with one command.
#
#   ./evals/run.sh [--keep] [--help]
#
# Builds a temp sandbox, crafts hermetic PR bundles inside it (no network —
# a stub `gh` that fails every call sits first on PATH and logs each
# interception), runs evals/cases/*.sh against scripts/review.sh, prints a
# PASS/FAIL tally, writes evals/results/summary.json (gitignored), then
# removes the sandbox (unless --keep). Exits 0 only when every case passes
# and the repo tree is byte-identical before/after (fixtures never touch it).
set -euo pipefail

KEEP="false"
usage() {
  cat <<'EOF'
usage: evals/run.sh [--keep] [--help]

Run the offline contract evals (terminal-write discipline, --bind
downgrade, --json envelope shape, HEAD-reviewed gate). No network: bundles are crafted fixtures
in a temp sandbox and a stub gh fails every network attempt.

  --keep   leave the sandbox in place and print its path (debugging)
  --help   this text
EOF
}
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP="true" ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag '$arg' (see --help)" >&2; exit 2 ;;
  esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for dep in jq shasum git; do
  command -v "$dep" >/dev/null 2>&1 || { echo "evals need $dep on PATH" >&2; exit 2; }
done

pre_status="$(git -C "$ROOT" status --short)"

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/verdict-evals.XXXXXX")"
STUBBIN="$SANDBOX/stubbin"
STUBLOG="$SANDBOX/gh-stub.log"
mkdir -p "$STUBBIN"
: > "$STUBLOG"
cat > "$STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
# Network-dead gh stub for the offline contract evals: log and fail every call.
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$STUBBIN/gh"

export EVAL_ROOT="$ROOT" EVAL_SANDBOX="$SANDBOX"
export EVAL_STUBBIN="$STUBBIN" EVAL_STUBLOG="$STUBLOG"

cleanup() {
  if [ "$KEEP" = "true" ]; then
    echo "eval sandbox kept: $SANDBOX"
  else
    rm -rf "$SANDBOX"
  fi
}
trap cleanup EXIT

echo "evals: stub gh at $(PATH="$STUBBIN:$PATH" command -v gh) (all network calls fail there)"
echo "evals: sandbox $SANDBOX (fixtures live here, never in the repo)"

pass=0
fail=0
tally="$SANDBOX/case-results.txt"
: > "$tally"
for c in "$ROOT/evals/cases/"*.sh; do
  name="$(basename "$c" .sh)"
  if bash "$c"; then
    echo "PASS $name"
    echo "$name PASS" >> "$tally"
    pass=$((pass + 1))
  else
    echo "FAIL $name"
    echo "$name FAIL" >> "$tally"
    fail=$((fail + 1))
  fi
done

intercepts="$(wc -l <"$STUBLOG" | tr -d ' ')"
echo "evals: stub gh intercepted $intercepts network attempt(s), zero real calls"

mkdir -p "$ROOT/evals/results"
cases_json="$(jq -R -s '[split("\n")[] | select(length > 0) | split(" ") | {name: .[0], status: .[1]}]' "$tally")"
at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n --arg at "$at" --argjson pass "$pass" --argjson fail "$fail" \
  --argjson intercepts "$intercepts" --argjson cases "$cases_json" \
  '{at: $at, pass: $pass, fail: $fail,
    hermetic: {network: "disabled via stub gh first on PATH", fixtures: "crafted bundles in a temp sandbox, removed unless --keep", gh_intercepts: $intercepts},
    cases: $cases}' > "$ROOT/evals/results/summary.json"
echo "evals: wrote evals/results/summary.json (gitignored)"

post_status="$(git -C "$ROOT" status --short)"
if [ "$post_status" != "$pre_status" ]; then
  echo "evals FAIL: repo tree changed during the run" >&2
  echo "--- before ---" >&2; printf '%s\n' "$pre_status" >&2
  echo "--- after ---" >&2; printf '%s\n' "$post_status" >&2
  exit 1
fi
echo "evals: repo tree unchanged (fixtures stayed in the sandbox)"

echo "evals: $pass passed, $fail failed"
if [ "$fail" -ne 0 ]; then exit 1; fi
