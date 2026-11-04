#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

CORPUS_SRC="$HERE/../fixtures/verify-fuzz-corpus.tsv"
if [ ! -f "$CORPUS_SRC" ]; then echo "corpus missing: $CORPUS_SRC" >&2; exit 1; fi

FUZZ_TMP="$(mktemp -d /tmp/verify-fuzz.XXXXXX)"
trap 'rm -rf "$FUZZ_TMP"' EXIT
CORPUS="$EVAL_SANDBOX/verify-fuzz-corpus.tsv"
sed "s#/tmp/verify-fuzz[.-]#$FUZZ_TMP/#g" "$CORPUS_SRC" >"$CORPUS"

strict_ok=0 pass_ok=0 xfail_ok=0
refused_total=0 executed_total=0

printf 'line1\n' > "$EVAL_SANDBOX/fz-notes.txt"
printf -- '-s\n' > "$FUZZ_TMP/curlcfg"
printf 'http://127.0.0.1:9/f\n' > "$FUZZ_TMP/wgeturls"
mkdir -p "$FUZZ_TMP/gitexec"
printf 'true\n' > "$FUZZ_TMP/src"
printf 'exit 0\n' > "$FUZZ_TMP/fetched"
chmod +x "$FUZZ_TMP/fetched" 2>/dev/null || true
AWS_KEY="fuzzcanary"
export AWS_KEY

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
    || [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)" != "$EVAL_ENVELOPE_KEYS" ] \
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

rm -rf "$FUZZ_TMP"
rm -f "$EVAL_SANDBOX/fz-notes.txt" "$EVAL_SANDBOX/f" "$EVAL_SANDBOX/fz-out"

echo "corpus: $strict_ok strict-refuse ok, $pass_ok pass ok, $xfail_ok xfail-known gaps ($refused_total refused, $executed_total executed)"
eval_end
