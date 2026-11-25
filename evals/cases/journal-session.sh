#!/usr/bin/env bash
#
# STA-447: end-to-end bundle journal session.
# Reconstructs fetch-local -> run -> record --dry-run -> record -> report
# from the external journal (<XDG_STATE_HOME>/verdict/journal/<repo-key>/<pr>.jsonl), verifying:
# - nothing journal-related is written under the repo or .verdict/;
# - every line carries the canonical repo root in `repo`;
# - exact 5-step sequence with zero unfinished calls and zero chain breaks;
# - dry-run-first derivability from argv and exit codes;
# - real content sha256 on written bundle paths;
# - secret redaction for both --token operands and secret-named env values;
# - report's recorded next hint matches the --json envelope's .next;
# - consecutive record --dry-run calls leave provenance.json byte-identical and
#   run.json inputs_sha256 fixed.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
if [ ! -f "$STUB_SRC" ]; then
  bail "missing stub agent fixture: $STUB_SRC"
fi

SANDBOX="$(cd "$EVAL_SANDBOX" && pwd)"
REPO="$SANDBOX/repo"
eval_fixture_repo "$REPO"
REPO_CANON="$(cd "$REPO" && pwd -P)"

AGENTBIN="$SANDBOX/agentbin"
mkdir -p "$AGENTBIN"
cp "$STUB_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"

CLI_SECRET="cli-secret-token-value-9991"
ENV_SECRET="sk-ant-env-secret-value-8882"
BUNDLE_SLUG="session"
BUNDLE_NAME="local-$BUNDLE_SLUG"
BUNDLE_DIR="$REPO/.verdict/state/$BUNDLE_NAME"

str_sha256_prefix() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | cut -c1-16
  else
    printf '%s' "$1" | shasum -a 256 | cut -c1-16
  fi
}

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

REPO_KEY="$(str_sha256_prefix "$REPO_CANON")"
JOURNAL="$XDG_STATE_HOME/verdict/journal/$REPO_KEY/$BUNDLE_NAME.jsonl"

run_in_repo() {
  set +e
  (
    cd "$REPO" || exit 99
    PATH="$AGENTBIN:$EVAL_STUBBIN:$PATH" ANTHROPIC_API_KEY="$ENV_SECRET" \
      "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit
  EVAL_STATUS=$?
  set -e
  return 0
}

# 1. fetch-local
run_in_repo fetch-local acme/app "$BUNDLE_SLUG" --json
assert_exit "fetch-local succeeds" 0

# 2. run --agent claude (passing CLI + env secrets after -- so redaction is tested)
run_in_repo run acme/app "$BUNDLE_NAME" --agent claude --finders 1 --rounds 1 --json \
  -- --token "$CLI_SECRET" "$ENV_SECRET"
assert_exit "run --agent claude succeeds" 0

# 3. record --dry-run
run_in_repo record acme/app "$BUNDLE_NAME" --dry-run --json
assert_exit "record --dry-run succeeds" 0

# 4. record
run_in_repo record acme/app "$BUNDLE_NAME" --json
assert_exit "record succeeds" 0

# 5. report (advisory: gating belongs to gate, which writes no journal line)
run_in_repo report acme/app "$BUNDLE_NAME" --json
assert_exit "report succeeds" 0
REPORT_NEXT="$(jq -r '.next' "$EVAL_OUT")"

# External location (J4): journal lives under XDG_STATE_HOME, never under the repo or .verdict/.
if [ -f "$JOURNAL" ]; then
  good "journal written at external state path"
else
  bad "external journal missing at $JOURNAL"
fi
if [ ! -e "$BUNDLE_DIR/journal.jsonl" ] && [ ! -e "$BUNDLE_DIR/journal.jsonl.lock" ]; then
  good "no journal files written inside bundle dir"
else
  bad "found unexpected journal artifact inside $BUNDLE_DIR"
fi
if jq -se --arg repo "$REPO_CANON" 'length > 0 and all(.[]; .repo == $repo)' "$JOURNAL" >/dev/null; then
  good "every journal line carries the canonical repo root"
else
  bad "a journal line is missing or mismatched repo"
fi

# Read the journal (read-only: must not append a 6th step).
run_in_repo journal acme/app "$BUNDLE_NAME" --json
assert_exit "verdict journal --json succeeds" 0
assert_envelope "journal"
cp "$EVAL_OUT" "$SANDBOX/journal.json"

# Exact 5 steps in order, zero unfinished, zero chain breaks.
VERBS="$(jq -r '.data.steps | map(.verb) | join(",")' "$SANDBOX/journal.json")"
assert_eq "5 steps in exact session order" "fetch-local,run,record,record,report" "$VERBS"
assert_json_field "zero unfinished steps" "0" '.data.unfinished | length' "$SANDBOX/journal.json"
assert_json_field "zero chain breaks" "0" '.data.chain_breaks | length' "$SANDBOX/journal.json"
assert_json_field "zero unreadable lines" "0" '.data.unreadable_lines' "$SANDBOX/journal.json"

# dry-run-first derivability: step[2] is record --dry-run exit 0, step[3] is plain record exit 0.
assert_json_field "record --dry-run exited 0" "0" '.data.steps[2].exit' "$SANDBOX/journal.json"
assert_json_field "dry-run step carries --dry-run in argv" "true" '.data.steps[2].argv | index("--dry-run") != null' "$SANDBOX/journal.json"
assert_json_field "real record exited 0" "0" '.data.steps[3].exit' "$SANDBOX/journal.json"
assert_json_field "real record omits --dry-run in argv" "true" '.data.steps[3].argv | index("--dry-run") == null' "$SANDBOX/journal.json"

# Redaction: neither secret appears anywhere in the journal, and run argv holds ***REDACTED***.
assert_file_nogrep "CLI --token secret never reaches journal" "$JOURNAL" "$CLI_SECRET"
assert_file_nogrep "secret-named env value never reaches journal" "$JOURNAL" "$ENV_SECRET"
assert_json_field "run argv redacted both secrets" "2" '.data.steps[1].argv | map(select(. == "***REDACTED***")) | length' "$SANDBOX/journal.json"

# Tee honesty: report's journal next equals the report envelope's .next.
assert_json_field "report journal next matches envelope .next" "$REPORT_NEXT" '.data.steps[4].next' "$SANDBOX/journal.json"

# Real sha256 verification: for every path recorded in wrote[], check the latest
# recorded sha256 against the actual file bytes on disk.
WROTE_CHECKED=0
while IFS=$'\t' read -r rel want_sha; do
  [ -n "$rel" ] || continue
  target="$BUNDLE_DIR/$rel"
  if [ ! -f "$target" ]; then
    bad "recorded wrote path missing on disk: $rel"
    continue
  fi
  got_sha="$(file_sha256 "$target")"
  assert_eq "wrote sha256 matches disk for $rel" "$want_sha" "$got_sha"
  WROTE_CHECKED=$((WROTE_CHECKED + 1))
done < <(jq -r '
  [.data.steps[].wrote[]]
  | reduce .[] as $f ({}; .[$f.path] = $f.sha256)
  | to_entries[]
  | "\(.key)\t\(.value)"
' "$SANDBOX/journal.json")
if [ "$WROTE_CHECKED" -ge 5 ]; then
  good "verified real on-disk sha256 for $WROTE_CHECKED written bundle files"
else
  bad "expected at least 5 written bundle files verified, got $WROTE_CHECKED"
fi

# Determinism (§4): consecutive record --dry-run runs keep provenance.json
# byte-identical and run.json inputs_sha256 unchanged.
cp "$BUNDLE_DIR/provenance.json" "$SANDBOX/prov.1.json"
RUN_SHA_1="$(jq -r '.inputs_sha256' "$BUNDLE_DIR/run.json")"
run_in_repo record acme/app "$BUNDLE_NAME" --dry-run --json
assert_exit "consecutive record --dry-run #1 succeeds" 0
cp "$BUNDLE_DIR/provenance.json" "$SANDBOX/prov.2.json"
RUN_SHA_2="$(jq -r '.inputs_sha256' "$BUNDLE_DIR/run.json")"
run_in_repo record acme/app "$BUNDLE_NAME" --dry-run --json
assert_exit "consecutive record --dry-run #2 succeeds" 0
cp "$BUNDLE_DIR/provenance.json" "$SANDBOX/prov.3.json"
RUN_SHA_3="$(jq -r '.inputs_sha256' "$BUNDLE_DIR/run.json")"

assert_file_eq "provenance.json byte-identical after record --dry-run #1" "$SANDBOX/prov.1.json" "$SANDBOX/prov.2.json"
assert_file_eq "provenance.json byte-identical after record --dry-run #2" "$SANDBOX/prov.2.json" "$SANDBOX/prov.3.json"
assert_eq "run.json inputs_sha256 unchanged after record --dry-run #1" "$RUN_SHA_1" "$RUN_SHA_2"
assert_eq "run.json inputs_sha256 unchanged after record --dry-run #2" "$RUN_SHA_2" "$RUN_SHA_3"

eval_end
