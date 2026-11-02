#!/usr/bin/env bash
# Case verify-fuzz-corpus: data-driven adversarial battery for the verify
# pre-exec gate, graded from evals/fixtures/verify-fuzz-corpus.tsv.
#
# Grades (hermetic — crafted patch.json fixtures, commands aimed at
# sandbox-relative paths or the 127.0.0.1:9 discard port, tight --timeout):
#   strict refuse rows must refuse typed (exit 1, exact error.code, one-doc
#     envelope with data null, try: hint plus quoted fragment, patch.json
#     byte-identical, no .verify.out write) — an executing strict row is a
#     NEW BYPASS and fails the suite (record it as xfail, never fix the
#     gate in the corpus ticket).
#   xfail refuse rows pin known gaps: executing is the recorded expectation
#     (tallied, suite stays green); refusing is an XPASS and fails the suite
#     with a "promote to strict" message (the gap closed — update the corpus).
#   strict PASS rows must execute (error null, numeric data.exit, verifyExit
#     recorded, .verify.out written) — a refusal is a FALSE POSITIVE and
#     fails the suite.
#
# The TSV is language-neutral (id, expect, mode, command tab-separated, no
# quoting rules) so the Go port reuses the file unchanged. Extend by
# appending one line; the format is documented in the TSV header.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

CORPUS_SRC="$HERE/../fixtures/verify-fuzz-corpus.tsv"
if [ ! -f "$CORPUS_SRC" ]; then echo "corpus missing: $CORPUS_SRC" >&2; exit 1; fi

# The TSV stays language-neutral; the runtime copy only rewrites the shared
# /tmp namespace to a private one. Executed rows use /tmp scratch, so without
# this two copies of the suite (or a future nested run) would clobber the
# same fixed files.
FUZZ_TMP="$(mktemp -d /tmp/verify-fuzz.XXXXXX)"
trap 'rm -rf "$FUZZ_TMP"' EXIT
CORPUS="$EVAL_SANDBOX/verify-fuzz-corpus.tsv"
sed "s#/tmp/verify-fuzz[.-]#$FUZZ_TMP/#g" "$CORPUS_SRC" >"$CORPUS"

fails=0 strict_ok=0 pass_ok=0 xfail_ok=0
refused_total=0 executed_total=0

WANT_KEYS="at data error next pr repo schema_version verb warnings"

# Sandbox fixtures the executed rows need (executed = gate passed, command
# really runs — keep every fixture inert and fast):
printf 'line1\n' > "$EVAL_SANDBOX/fz-notes.txt"
printf -- '-s\n' > "$FUZZ_TMP/curlcfg"
printf 'http://127.0.0.1:9/f\n' > "$FUZZ_TMP/wgeturls"
mkdir -p "$FUZZ_TMP/gitexec"
printf 'true\n' > "$FUZZ_TMP/src"
printf 'exit 0\n' > "$FUZZ_TMP/fetched"
chmod +x "$FUZZ_TMP/fetched" 2>/dev/null || true
AWS_KEY="fuzzcanary"
export AWS_KEY

# run_probe <pr>: run verify for the row's patch.json; sets PROBE_STATUS and
# PROBE_CODE (empty when the command executed instead of refusing).
run_probe() { # $1 = pr
  local pr="$1"
  run_review verify acme/app "$pr" --json --timeout 10
  PROBE_STATUS="$EVAL_STATUS"
  PROBE_CODE="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
}

grade_refuse() { # $1 = pr, $2 = want-code, $3 = label
  local pr="$1" want="$2" label="$3"
  local before after docs msg
  before="$(shasum -a 1 "$EVAL_SANDBOX/.review/$pr/patch.json" | cut -d' ' -f1)"
  run_probe "$pr"
  if [ "$PROBE_STATUS" -ne 1 ]; then
    echo "  FAIL: $label executed with exit $PROBE_STATUS (want refusal $want)"
    return 1
  fi
  if [ "$PROBE_CODE" != "$want" ]; then
    echo "  FAIL: $label refuses as '$PROBE_CODE', want '$want'"
    return 1
  fi
  docs="$(stdout_docs)"
  msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  if [ "$docs" != "1" ] \
    || [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)" != "$WANT_KEYS" ] \
    || ! jq -e '.data == null and (.error.code | type) == "string"' <"$EVAL_OUT" >/dev/null 2>&1; then
    echo "  FAIL: $label refusal breaks the envelope shape"
    return 1
  fi
  if [[ "$msg" != *"try:"* || "$msg" != *"'"* ]]; then
    echo "  FAIL: $label message misses try: hint or quoted fragment"
    return 1
  fi
  after="$(shasum -a 1 "$EVAL_SANDBOX/.review/$pr/patch.json" | cut -d' ' -f1)"
  if [ "$before" != "$after" ] || [ -e "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    echo "  FAIL: $label mutated the store"
    return 1
  fi
  return 0
}

grade_exec() { # $1 = pr, $2 = label
  local pr="$1" label="$2"
  run_review verify acme/app "$pr" --json --timeout 10
  if jq -e '.error == null and (.data.exit | type) == "number"' <"$EVAL_OUT" >/dev/null 2>&1 \
    && [ "$(jq -r '.verifyExit // empty' "$EVAL_SANDBOX/.review/$pr/patch.json")" != "" ] \
    && [ -f "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    return 0
  fi
  echo "  FAIL: $label refused or broke: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
  return 1
}

TAB="$(printf '\t')"
n=0
while IFS="$TAB" read -r id expect mode command; do
  case "$id" in ''|'#'*) continue ;; esac
  if [ -z "${expect:-}" ] || [ -z "${mode:-}" ] || [ -z "${command:-}" ]; then
    echo "  FAIL: malformed corpus line for id '$id' (want id, expect, mode, command)"
    fails=$((fails + 1)); continue
  fi
  n=$((n + 1))
  pr=$((6050 + n))
  mkdir -p "$EVAL_SANDBOX/.review/$pr"
  printf '%s' "$command" | jq -R '{verifyCmd:.}' > "$EVAL_SANDBOX/.review/$pr/patch.json"
  case "$expect:$mode" in
    PASS:strict)
      if grade_exec "$pr" "$id PASS $command"; then
        pass_ok=$((pass_ok + 1)); executed_total=$((executed_total + 1))
      else
        echo "  FAIL: $id FALSE POSITIVE — benign command refused (widen the gate, not the corpus)"
        fails=$((fails + 1))
      fi
      ;;
    *:strict)
      if grade_refuse "$pr" "$expect" "$id $command"; then
        echo "  ok: $id refuses as $expect"
        strict_ok=$((strict_ok + 1)); refused_total=$((refused_total + 1))
      else
        echo "  FAIL: $id NEW BYPASS — gate executed or mis-graded (record as xfail, do not fix the gate here)"
        fails=$((fails + 1))
      fi
      ;;
    *:xfail)
      run_probe "$pr"
      if [ "$PROBE_STATUS" -eq 1 ] && [ -n "$PROBE_CODE" ]; then
        echo "  FAIL: $id XPASS — known gap now refuses as $PROBE_CODE (aspirational $expect; promote to strict)"
        fails=$((fails + 1)); refused_total=$((refused_total + 1))
      else
        echo "  ok: $id xfail (known gap, still executes — aspirational code $expect)"
        xfail_ok=$((xfail_ok + 1)); executed_total=$((executed_total + 1))
      fi
      ;;
    *)
      echo "  FAIL: $id has unknown mode '$mode' (want strict or xfail)"
      fails=$((fails + 1))
      ;;
  esac
done < "$CORPUS"

# Fixture cleanup: the private /tmp dir goes with the EXIT trap; the sandbox
# dir goes with run.sh.
rm -rf "$FUZZ_TMP"
rm -f "$EVAL_SANDBOX/fz-notes.txt" "$EVAL_SANDBOX/f" "$EVAL_SANDBOX/fz-out"

echo "corpus: $strict_ok strict-refuse ok, $pass_ok pass ok, $xfail_ok xfail-known gaps ($refused_total refused, $executed_total executed)"
if [ "$fails" -ne 0 ]; then echo "case verify-fuzz-corpus: FAIL ($fails)"; exit 1; fi
echo "case verify-fuzz-corpus: PASS"
