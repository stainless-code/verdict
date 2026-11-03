#!/usr/bin/env bash
# evals/run.sh — run the offline contract evals with one command.
#
#   ./evals/run.sh [--keep] [--help]
#
# Builds a temp sandbox, crafts hermetic PR bundles inside it (no network —
# a stub `gh` that fails every call sits first on PATH and logs each
# interception), runs evals/cases/*.sh against the built verdict binary
# ($EVAL_CLI, from `make evals`), prints a PASS/FAIL tally, writes
# evals/results/summary.json (gitignored), then removes the sandbox
# (unless --keep). Exits 0 only when every case passes and the repo tree
# is byte-identical before/after (fixtures never touch it).
#
# Cases run concurrently (default: one job per CPU; EVAL_JOBS=N overrides,
# EVAL_JOBS=1 is the old serial run). Every case gets its own sandbox, stub
# dir and stub log; each case's stdout/stderr is buffered and replayed in
# sorted case order, so the report is deterministic. A case whose first ten
# lines carry `# eval: serial` runs alone after the parallel batch (use it
# for anything that genuinely cannot share the machine).
set -euo pipefail

KEEP="false"
usage() {
  cat <<'EOF'
usage: evals/run.sh [--keep] [--help]

Run the offline contract evals (terminal-write discipline, --bind
downgrade, --json envelope shape, HEAD-reviewed gate). No network: bundles are crafted fixtures
in a temp sandbox and a stub gh fails every network attempt.

Cases run concurrently; EVAL_JOBS sets the job limit (default: CPU count,
EVAL_JOBS=1 restores the serial run). A case with a `# eval: serial` header
runs alone after the parallel batch.

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
# shellcheck source=evals/lib/testenv.sh
. "$ROOT/evals/lib/testenv.sh"
for dep in jq shasum git; do
  command -v "$dep" >/dev/null 2>&1 || { echo "evals need $dep on PATH" >&2; exit 2; }
done

# Job limit: EVAL_JOBS wins; otherwise CPU count (getconf, then sysctl, then
# 4). Validate before creating anything so a typo fails fast.
if [ -n "${EVAL_JOBS:-}" ]; then
  JOBS="$EVAL_JOBS"
else
  JOBS="$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)"
  case "$JOBS" in ''|*[!0-9]*|0) JOBS="" ;; esac
  if [ -z "$JOBS" ]; then
    JOBS="$(sysctl -n hw.ncpu 2>/dev/null || true)"
    case "$JOBS" in ''|*[!0-9]*|0) JOBS="" ;; esac
  fi
  [ -n "$JOBS" ] || JOBS=4
fi
case "$JOBS" in ''|*[!0-9]*) echo "EVAL_JOBS must be a positive integer (got '$JOBS')" >&2; exit 2 ;; esac
JOBS=$((10#$JOBS))
if [ "$JOBS" -lt 1 ]; then
  echo "EVAL_JOBS must be a positive integer (got '$EVAL_JOBS')" >&2
  exit 2
fi

pre_status="$(git -C "$ROOT" status --short)"

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/verdict-evals.XXXXXX")"
# Hermetic by construction: no credential-shaped variable survives into a
# case or its git/gh/verdict children, and git runs with the pinned config.
testenv_init "$SANDBOX"
STUBBIN="$SANDBOX/stubbin"
mkdir -p "$STUBBIN"
cat > "$STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
# Network-dead gh stub for the offline contract evals: log and fail every call,
# except the narrow opt-in affordances below (unset = fail, today's behavior).
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
# doctor-scope-audit (verify-harden slice 02): shape the doctor verb's
# pre-existing reads via GH_STUB_DOCTOR. Unset → fail like every other call.
# Otherwise: `api user --include` emits the value as the x-oauth-scopes
# header line (__NO_HEADER__ = 200 with no header: fine-grained token);
# auth/rate_limit answer healthy so scope assertions own the exit code.
if [[ -n "${GH_STUB_DOCTOR+x}" ]]; then
  case "$*" in
    "api user --include")
      if [[ "$GH_STUB_DOCTOR" != "__NO_HEADER__" ]]; then
        echo "x-oauth-scopes: $GH_STUB_DOCTOR"
      fi
      echo '{"login":"evals-stub"}'
      exit 0;;
    "auth status") exit 0;;
    "api rate_limit"*) echo "999"; exit 0;;
  esac
fi
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$STUBBIN/gh"

# Each case gets a private copy of the stub and its own log, so concurrent
# cases never see each other's intercepts (several cases swap the stub out
# and count calls). The template above is only the source.
STUB_TEMPLATE="$STUBBIN/gh"
CASES_DIR="$SANDBOX/cases"
mkdir -p "$CASES_DIR"

export EVAL_ROOT="$ROOT"

# EVAL_CLI is the one CLI under test: the built verdict binary. make
# evals builds it first; a direct ./evals/run.sh run needs a prior
# `make build` (or `make evals`) and fails fast otherwise instead of
# silently grading nothing.
EVAL_CLI="${EVAL_CLI:-$ROOT/bin/verdict}"
if [ ! -x "$EVAL_CLI" ]; then
  echo "evals need a built binary at $EVAL_CLI (run make evals) instead of silently grading nothing" >&2
  exit 2
fi
export EVAL_CLI
echo "evals: CLI under test $EVAL_CLI"

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
echo "evals: running with $JOBS job(s) (EVAL_JOBS overrides)"
echo "evals: scrubbed $TESTENV_SCRUBBED secret-shaped variable(s); git config pinned"

# is_serial <case> — true when the case's header carries `# eval: serial`.
# awk (not grep in a pipe) keeps this safe under `set -o pipefail`.
is_serial() {
  awk 'NR <= 10 && /^# eval: serial[[:space:]]*$/ { found = 1 } END { exit(found ? 0 : 1) }' "$1"
}

# run_case <case> — set up the case's private sandbox/stub/log and run it.
# The exit status lands in $dir/status even when the case fails; the function
# itself returns 0 so `set -e` never aborts the runner mid-batch.
run_case() {
  local c="$1" name dir st
  name="$(basename "$c" .sh)"
  dir="$CASES_DIR/$name"
  mkdir -p "$dir/work" "$dir/stubbin"
  cp "$STUB_TEMPLATE" "$dir/stubbin/gh"
  chmod +x "$dir/stubbin/gh"
  : > "$dir/gh-stub.log"
  set +e
  EVAL_SANDBOX="$dir/work" \
    EVAL_STUBBIN="$dir/stubbin" \
    EVAL_STUBLOG="$dir/gh-stub.log" \
    bash "$c" >"$dir/output.log" 2>&1
  st=$?
  set -e
  printf '%s\n' "$st" >"$dir/status"
}

# report_case <case> — replay the buffered output in sorted order, then tally.
# Three statuses: exit 0 PASS, exit 77 SKIP (the case reported a leg it could
# not run), anything else FAIL. A skipped case is never counted as passed.
report_case() {
  local c="$1" name dir st
  name="$(basename "$c" .sh)"
  dir="$CASES_DIR/$name"
  if [ -f "$dir/output.log" ]; then
    cat "$dir/output.log"
  fi
  st=""
  if [ -f "$dir/status" ]; then st="$(cat "$dir/status")"; fi
  if [ "$st" = "0" ]; then
    echo "PASS $name"
    printf '%s PASS\n' "$name" >>"$tally"
    pass=$((pass + 1))
  elif [ "$st" = "77" ]; then
    echo "SKIP $name"
    printf '%s SKIP\n' "$name" >>"$tally"
    skipped=$((skipped + 1))
  else
    echo "FAIL $name"
    printf '%s FAIL\n' "$name" >>"$tally"
    fail=$((fail + 1))
  fi
}

pass=0
fail=0
skipped=0
tally="$SANDBOX/case-results.txt"
: >"$tally"

if [ "$JOBS" -le 1 ]; then
  # Serial run: identical to the old behaviour, one case at a time with its
  # output printed as it finishes (each case still gets its own sandbox).
  for c in "$ROOT/evals/cases/"*.sh; do
    run_case "$c"
    report_case "$c"
  done
else
  # Parallel batch: throttled by the live job count (no `wait -n` on bash 3.2).
  for c in "$ROOT/evals/cases/"*.sh; do
    if is_serial "$c"; then continue; fi
    while [ "$(jobs -pr | wc -l | tr -d ' ')" -ge "$JOBS" ]; do
      sleep 0.1
    done
    run_case "$c" &
  done
  wait || true

  # Serial-marked cases run one at a time after the parallel batch.
  for c in "$ROOT/evals/cases/"*.sh; do
    if ! is_serial "$c"; then continue; fi
    run_case "$c"
  done

  # Replay every case in deterministic (glob/sorted) order.
  for c in "$ROOT/evals/cases/"*.sh; do
    report_case "$c"
  done
fi

intercepts=0
for c in "$ROOT/evals/cases/"*.sh; do
  name="$(basename "$c" .sh)"
  log="$CASES_DIR/$name/gh-stub.log"
  if [ -f "$log" ]; then
    n="$(wc -l <"$log" | tr -d ' ')"
    intercepts=$((intercepts + n))
  fi
done
echo "evals: stub gh intercepted $intercepts network attempt(s), zero real calls"

mkdir -p "$ROOT/evals/results"
cases_json="$(jq -R -s '[split("\n")[] | select(length > 0) | split(" ") | {name: .[0], status: .[1]}]' "$tally")"
at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n --arg at "$at" --argjson pass "$pass" --argjson fail "$fail" \
  --argjson skipped "$skipped" \
  --argjson intercepts "$intercepts" --argjson cases "$cases_json" \
  '{at: $at, pass: $pass, fail: $fail, skipped: $skipped,
    hermetic: {network: "disabled via stub gh first on PATH", fixtures: "crafted bundles in a temp sandbox, removed unless --keep", gh_intercepts: $intercepts},
    cases: $cases}' >"$ROOT/evals/results/summary.json"
echo "evals: wrote evals/results/summary.json (gitignored)"

post_status="$(git -C "$ROOT" status --short)"
if [ "$post_status" != "$pre_status" ]; then
  echo "evals FAIL: repo tree changed during the run" >&2
  echo "--- before ---" >&2; printf '%s\n' "$pre_status" >&2
  echo "--- after ---" >&2; printf '%s\n' "$post_status" >&2
  exit 1
fi
echo "evals: repo tree unchanged (fixtures stayed in the sandbox)"

echo "evals: $pass passed, $fail failed, $skipped skipped"
if [ "$fail" -ne 0 ]; then exit 1; fi
# A skipped case is not a pass: a leg that did not run proves nothing, so the
# run fails unless the operator accepts the gap explicitly (test-audit F1/F8).
if [ "$skipped" -ne 0 ] && [ "${EVAL_ALLOW_SKIP:-0}" != "1" ]; then
  echo "evals FAIL: $skipped case(s) skipped — a skipped leg proves nothing." >&2
  echo "  Install the missing prerequisite, or re-run with EVAL_ALLOW_SKIP=1 to accept the gap." >&2
  exit 1
fi
