#!/usr/bin/env bash
#
# fix --selected: the reaction-scoped batch — the sigs a GitHub fetch recorded
# in selected.json because a human reacted 👍 to verdict's root comment.
# Hermetic: fixtures are crafted bundles, no network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

CRIT="c123456789abcdef0123456789abcdef01234567"
IMP="6123456789abcdef0123456789abcdef01234567"
INFO="1123456789abcdef0123456789abcdef01234567"
HEAD="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

mk_local() { # $1 = bare slug (bundle lands at .verdict/state/local-$1)
  local slug="$1" dir="$EVAL_SANDBOX/.verdict/state/local-$1"
  mkdir -p "$dir"
  cat > "$dir/source.json" <<EOF
{"schema_version":"1","kind":"worktree","repo":"acme/app","slug":"$slug","base_ref":null,"head_ref":"work","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"","range":"HEAD:worktree","dirty":true,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
  cat > "$dir/patch.json" <<'EOF'
{"schema_version":"1","verifyCmd":"go test ./..."}
EOF
}

mk_finding() { # $1=sig $2=status $3=severity $4=path $5=body
  jq -n -c --arg sig "$1" --arg st "$2" --arg sev "$3" --arg p "$4" --arg b "$5" \
    '{kind:"inline",path:$p,heading:null,side:"RIGHT",line:2,anchored:true,body:$b,suggestion:null,inBounds:true,rigor:"pointed",severity:$sev,kb:[],sig:$sig,sig_version:"1",firstSeen:"aaaaaaa",lastSeen:"aaaaaaa",status:$st,rationale:"",history:[]}'
}

write_findings() { # $1 = bare slug, stdin = findings array
  cat > "$EVAL_SANDBOX/.verdict/state/local-$1/findings.json"
}

write_selected() { # $1 = bundle dir name, rest = sigs
  local dir="$EVAL_SANDBOX/.verdict/state/$1" sig rows
  shift
  mkdir -p "$dir"
  rows=""
  for sig in "$@"; do
    rows="${rows:+$rows,}\"$sig\""
  done
  printf '{"schema_version":"1","repo":"acme/app","pr":"%s","head_sha":"%s","selected_at":"2026-01-01T00:00:00Z","sigs":[%s]}\n' \
    "$(basename "$dir")" "$HEAD" "$rows" >"$dir/selected.json"
}

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "fix-selected-eval@example.com"
  git config user.name "fix selected eval"
  cat > .gitignore <<'EOF'
.verdict/state/
stubbin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
EOF
  printf 'line1\nline2\nline3\n' > notes.txt
  printf 'alpha\nbeta\ngamma\n' > app.txt
  printf 'other1\nother2\n' > other.txt
  git add -A
  git commit -qm init
)

mk_local sel
{
  mk_finding "$CRIT" open critical app.txt "Critical boundary bug here."
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
  mk_finding "$INFO" open informational notes.txt "Minor wording nit here."
} | jq -s '.' | write_findings sel
write_selected local-sel "$IMP" "$INFO"
run_review fix acme/app local-sel --selected --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.selection' <"$EVAL_OUT")" = ".verdict/state/local-sel/selected.json" ]; then
  good "--selected reads selected.json and names it in data.selection"
else
  bad "--selected gave exit $EVAL_STATUS selection '$(jq -r '.data.selection // empty' <"$EVAL_OUT" 2>/dev/null)'"
fi
if [ "$(jq -r '.data.batch | length' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '[.data.batch[].sig] | join(",")' <"$EVAL_OUT")" = "$IMP,$INFO" ] \
  && [ "$(jq -r '.data.min_severity' <"$EVAL_OUT")" = "off" ]; then
  good "the selection is exactly the reacted sigs, under an off floor"
else
  bad "selection data: $(jq -c '.data | {min_severity,batch}' <"$EVAL_OUT" 2>/dev/null)"
fi
ledger="$EVAL_SANDBOX/.verdict/state/local-sel/fix.json"
if [ "$(jq -r ".sigs[\"$IMP\"].passes" "$ledger")" = "1" ] \
  && [ "$(jq -r ".sigs[\"$INFO\"].passes" "$ledger")" = "1" ] \
  && [ "$(jq -r ".sigs[\"$CRIT\"] // \"absent\"" "$ledger")" = "absent" ]; then
  good "the ledger spends one pass per reacted sig and none on the unreacted critical"
else
  bad "ledger: $(cat "$ledger" 2>/dev/null)"
fi
run_review fix acme/app local-sel --selected
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "» selection: .verdict/state/local-sel/selected.json — 2 reacted sig(s)" "$EVAL_OUT" \
  && grep -q "re-run fix acme/app local-sel --selected for the next pass" "$EVAL_OUT"; then
  good "human output names the selection it read and the --selected re-run"
else
  bad "human --selected output: $(head -c 300 "$EVAL_OUT")"
fi

mk_local selfloor
{
  mk_finding "$INFO" open informational notes.txt "Minor wording nit here."
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
} | jq -s '.' | write_findings selfloor
write_selected local-selfloor "$INFO" "$IMP"
run_review fix acme/app local-selfloor --selected --min-severity important --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '[.data.batch[].severity] | join(",")' <"$EVAL_OUT")" = "important" ]; then
  good "--min-severity narrows a selection"
else
  bad "narrowed selection: exit $EVAL_STATUS $(jq -c '.data.batch' <"$EVAL_OUT" 2>/dev/null)"
fi

mk_local selpaths
{
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
  mk_finding "$INFO" open informational notes.txt "Minor wording nit here."
} | jq -s '.' | write_findings selpaths
write_selected local-selpaths "$IMP" "$INFO"
run_review fix acme/app local-selpaths --selected --paths app.txt --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "no-selected-findings" ]; then
  good "--paths that excludes every reacted sig refuses no-selected-findings"
else
  bad "excluded selection gave exit $EVAL_STATUS code '$code'"
fi

mk_local selmissing
mk_finding "$IMP" open important other.txt "Important scoping bug here." | jq -s '.' | write_findings selmissing
run_review fix acme/app local-selmissing --selected --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "missing-selection" ] \
  && jq -e '.error.message | contains("re-run the bundle'"'"'s fetch")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "an absent selection refuses missing-selection with the fetch pointer"
else
  bad "absent selection gave exit $EVAL_STATUS code '$code'"
fi

mk_local selsettled
mk_finding "$IMP" addressed important other.txt "Settled bug here." | jq -s '.' | write_findings selsettled
write_selected local-selsettled "$IMP"
run_review fix acme/app local-selsettled --selected --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "no-selected-findings" ] \
  && jq -e '.error.message | contains("names 1 reacted sig(s)")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "a selection whose sigs settled refuses no-selected-findings"
else
  bad "settled selection gave exit $EVAL_STATUS code '$code'"
fi

mk_local selempty
mk_finding "$IMP" open important other.txt "Important scoping bug here." | jq -s '.' | write_findings selempty
printf '{"schema_version":"1","repo":"acme/app","pr":"1","head_sha":"%s","selected_at":"2026-01-01T00:00:00Z","sigs":[]}\n' \
  "$HEAD" >"$EVAL_SANDBOX/.verdict/state/local-selempty/selected.json"
run_review fix acme/app local-selempty --selected --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "no-selected-findings" ] \
  && jq -e '.error.message | contains("names no sig")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "an empty sigs list refuses no-selected-findings instead of widening to every finding"
else
  bad "empty selection gave exit $EVAL_STATUS code '$code'"
fi
if [ ! -e "$EVAL_SANDBOX/.verdict/state/local-selempty/fix.json" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/local-selempty/fix.patch" ]; then
  good "an empty selection spends no pass and writes no ledger"
else
  bad "empty selection wrote a ledger: $(cat "$EVAL_SANDBOX/.verdict/state/local-selempty/fix.json" 2>/dev/null)"
fi

mk_local selbad
mk_finding "$IMP" open important other.txt "Important scoping bug here." | jq -s '.' | write_findings selbad
printf '{"sigs":"nope"}\n' >"$EVAL_SANDBOX/.verdict/state/local-selbad/selected.json"
run_review fix acme/app local-selbad --selected --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "corrupt-store" ] \
  && jq -e '.error.message | contains("selected.json is unreadable")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "an unreadable selection refuses corrupt-store instead of starting empty"
else
  bad "corrupt selection gave exit $EVAL_STATUS code '$code'"
fi

mk_local selconflict
mk_finding "$IMP" open important other.txt "Important scoping bug here." | jq -s '.' | write_findings selconflict
write_selected local-selconflict "$IMP"
run_review fix acme/app local-selconflict --selected --sig "$IMP"
if [ "$EVAL_STATUS" -eq 2 ] && grep -q "fix takes --sig or --selected, not both" "$EVAL_ERR"; then
  good "--selected and --sig are a usage error"
else
  bad "--selected --sig gave exit $EVAL_STATUS"
fi
run_review fix acme/app local-selconflict --selected --all
if [ "$EVAL_STATUS" -eq 2 ] && grep -q "fix takes --all or --selected, not both" "$EVAL_ERR"; then
  good "--selected and --all are a usage error"
else
  bad "--selected --all gave exit $EVAL_STATUS"
fi

PR=4291
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"PR finding.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
write_selected "$PR" "0123456789abcdef0123456789abcdef01234567"
run_review fix acme/app "$PR" --selected --verify-cmd "go test ./..."
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "--selected is the one batch that runs on a PR bundle"
else
  bad "PR bundle --selected gave exit $EVAL_STATUS code '$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)'"
fi
if grep -q "push the fix, re-fetch with fetch acme/app $PR, then land it with post acme/app $PR" "$EVAL_OUT"; then
  good "a PR bundle's instructions land through fetch and post, not record"
else
  bad "PR instructions: $(grep -o 'instructions: .*' "$EVAL_OUT" | head -c 400)"
fi
run_review fix acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "not-local" ]; then
  good "the same PR bundle still refuses not-local without --selected"
else
  bad "PR bundle without the flag gave exit $EVAL_STATUS code '$code'"
fi

eval_end
