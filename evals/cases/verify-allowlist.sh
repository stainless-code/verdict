#!/usr/bin/env bash
#
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

ROWS="$EVAL_ROOT/evals/fixtures/verify-allowlist.tsv"

WA_TMP="/tmp/verify-allowlist.$$"
mkdir -p "$WA_TMP"
trap 'rm -rf "$WA_TMP"' EXIT

refuse_probe() { # $1 = pr, $2 = want-code, $3 = label
  local pr="$1" want="$2" label="$3"
  local before after msg
  before="$(shasum -a 1 "$EVAL_SANDBOX/.review/$pr/patch.json" | cut -d' ' -f1)"
  run_review verify acme/app "$pr" --json --timeout 10
  msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  assert_exit "$label" 1
  assert_refusal "$label" "$want"
  if [[ "$msg" == *"try:"* && "$msg" == *"'"* ]]; then
    good "$label message carries a try: hint plus a quoted fragment"
  else
    bad "$label message misses try: hint or quoted fragment: $(head -c 200 "$EVAL_OUT")"
  fi
  after="$(shasum -a 1 "$EVAL_SANDBOX/.review/$pr/patch.json" | cut -d' ' -f1)"
  if [ "$before" = "$after" ] && [ ! -e "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    good "$label leaves patch.json byte-identical with no .verify.out write"
  else
    bad "$label mutated the store (patch $before -> $after, out present: $(test -e "$EVAL_SANDBOX/.review/$pr/.verify.out" && echo yes || echo no))"
  fi
}

exec_probe() { # $1 = pr, $2 = label
  local pr="$1" label="$2"
  run_review verify acme/app "$pr" --json --timeout 10
  if jq -e '.error == null and (.data.exit | type) == "number"' <"$EVAL_OUT" >/dev/null 2>&1 \
    && [ "$(jq -r '.verifyExit // empty' "$EVAL_SANDBOX/.review/$pr/patch.json")" != "" ] \
    && [ -f "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    good "$label executes (gate passes)"
  else
    bad "$label gated or broke: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
  fi
}

printf 'line1\n' > "$EVAL_SANDBOX/notes.txt"
git init -q "$EVAL_SANDBOX/clone-src" 2>/dev/null || true

TAB="$(printf '\t')"
n=0
while IFS="$TAB" read -r mode code pr cmd label; do
  case "$mode" in ''|'#'*) continue ;; esac
  if [ -z "$code" ] || [ -z "$pr" ] || [ -z "$cmd" ] || [ -z "$label" ]; then
    bad "malformed TSV row after $n good row(s): wants mode, code, pr, verifyCmd, label"
    continue
  fi
  [ "$code" = "-" ] && code=""
  n=$((n + 1))
  cmd="${cmd//@WA_TMP@/$WA_TMP}"
  eval_mk_patch "$pr" "$cmd"
  case "$mode" in
    refuse) refuse_probe "$pr" "$code" "$label" ;;
    exec) exec_probe "$pr" "$label" ;;
    *) bad "row $pr carries unknown mode '$mode' (want refuse|exec)" ;;
  esac
done <"$ROWS"
rm -rf "$EVAL_SANDBOX/clone-src" "$EVAL_SANDBOX/clone-dst-ok"

eval_mk_patch 5151 'printf ok'
run_review verify acme/app 5151 --json --timeout 10
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ] \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)" = "$EVAL_ENVELOPE_KEYS" ] \
  && jq -e '.error == null and .data.exit == 0 and .data.timedOut == false' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.outputTail' <"$EVAL_OUT")" = "ok" ] \
  && [ "$(jq -r '.verb' <"$EVAL_OUT")" = "verify" ]; then
  good "V5 printf ok executes (exit 0 recorded, success envelope intact)"
else
  bad "V5 printf ok gave exit $EVAL_STATUS: $(head -c 300 "$EVAL_OUT")"
fi
if [ "$(jq -r '.verifyExit' "$EVAL_SANDBOX/.review/5151/patch.json")" = "0" ] \
  && [ "$(jq -r '.verifiedTail' "$EVAL_SANDBOX/.review/5151/patch.json")" = "ok" ]; then
  good "V5 patch.json records the pass (verifyExit 0, tail ok)"
else
  bad "V5 patch.json: $(cat "$EVAL_SANDBOX/.review/5151/patch.json" 2>/dev/null)"
fi
if [ -f "$EVAL_SANDBOX/.review/5151/.verify.out" ] \
  && [ "$(cat "$EVAL_SANDBOX/.review/5151/.verify.out")" = "ok" ]; then
  good "V5 .verify.out holds the command output"
else
  bad "V5 .verify.out missing or wrong"
fi

printf '%s\n' 'printf ok | grep ok' 'FOO=1 printf ok' 'cd . && printf ok' \
  'git diff --stat' 'GOPROXY=off go test ./...' 'curl http://127.0.0.1:9/fixture' \
  "printf ok > $WA_TMP/probe && cat $WA_TMP/probe" > "$EVAL_SANDBOX/v5-battery.txt"
n=0
while IFS= read -r cmd; do
  [ -z "$cmd" ] && continue
  n=$((n + 1))
  pr=$((5160 + n))
  eval_mk_patch "$pr" "$cmd"
  run_review verify acme/app "$pr" --json --timeout 10
  if jq -e '.error == null and (.data.exit | type) == "number"' <"$EVAL_OUT" >/dev/null 2>&1 \
    && [ "$(jq -r '.verifyExit // empty' "$EVAL_SANDBOX/.review/$pr/patch.json")" != "" ] \
    && [ -f "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    good "V5 battery executes ($cmd)"
  else
    bad "V5 battery refused or broke ($cmd): exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
  fi
done < "$EVAL_SANDBOX/v5-battery.txt"

eval_mk_patch 5199 'rm -rf ./text-probe'
before="$(shasum -a 1 "$EVAL_SANDBOX/.review/5199/patch.json" | cut -d' ' -f1)"
run_review verify acme/app 5199 --timeout 10
after="$(shasum -a 1 "$EVAL_SANDBOX/.review/5199/patch.json" | cut -d' ' -f1)"
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "refusing verifyCmd" "$EVAL_ERR" \
  && grep -q "try:" "$EVAL_ERR" \
  && grep -q "'" "$EVAL_ERR"; then
  good "V6 text-mode refusal exits 1 with fragment + try: hint (typed code rides the --json envelope)"
else
  bad "V6 text-mode refusal gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
if [ "$before" = "$after" ] && [ ! -e "$EVAL_SANDBOX/.review/5199/.verify.out" ]; then
  good "V6 text-mode refusal leaves the store untouched"
else
  bad "V6 text-mode refusal mutated the store"
fi

mkdir -p "$EVAL_SANDBOX/.review/local-vallow"
cat > "$EVAL_SANDBOX/.review/local-vallow/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
EOF
cat > "$EVAL_SANDBOX/.review/local-vallow/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
cat > "$EVAL_SANDBOX/.review/local-vallow/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real record-path issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Rec verify me.","rigor":"walked","severity":"important"}]}
EOF
jq -n '{verifyCmd:"rm -rf ./record-probe"}' > "$EVAL_SANDBOX/.review/local-vallow/patch.json"
run_review record acme/app local-vallow --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "V6 record stores claims without gating (first pass exit 0)"
else
  bad "V6 first record exits $EVAL_STATUS"
fi
sig="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.review/local-vallow/findings.json" 2>/dev/null || true)"
jq -n --arg s "$sig" '[{sig:$s,verdict:"VERIFIED",resultHead:"aaaaaaa",verifyCmd:"rm -rf ./record-probe"}]' \
  > "$EVAL_SANDBOX/.review/local-vallow/verification.json"
run_review record acme/app local-vallow --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].status' "$EVAL_SANDBOX/.review/local-vallow/findings.json")" = "addressed" ] \
  && jq -e '.[0].history[-1].note | contains("rm -rf ./record-probe")' "$EVAL_SANDBOX/.review/local-vallow/findings.json" >/dev/null 2>&1; then
  good "V6 record keeps the nasty verifyCmd claim unrefused (gate is verify-only)"
else
  bad "V6 second record exits $EVAL_STATUS (gate leaked into record?)"
fi

eval_end
