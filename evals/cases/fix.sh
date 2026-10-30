#!/usr/bin/env bash
# Case fix: bounded verify-gated fix loops over local bundles (offline).
#
# Grades (hermetic — crafted local bundles plus a scratch git repo in the
# sandbox; the network-dead gh stub stays first on PATH throughout):
#   F1  default pick: top open inline sig by severity (critical first),
#       path-asc tiebreak; pass output carries readiness, finding, anchors,
#       instructions; --json envelope is one doc with the fix data shape.
#   F2  --sig must be open + inline: addressed/section/unknown refuse as
#       bad-sig, ambiguous prefix as ambiguous-sig, all with try: hints.
#   F3  PR bundles refuse as not-local; closed local bundles (PR.json state
#       closed) refuse; conflicted worktrees refuse as conflicted-tree.
#   F4  ceiling: 3 passes land, the 4th refuses as fix-ceiling with a
#       stopped:ceiling tombstone (passes stay 3, repeat refusal stable).
#   F5  scope-escape: touching a file outside the anchor stops the loop,
#       names the file, tombstones stopped:scope-escape; reverting recovers.
#   F6  kb.txt content surfaces in full in the fix output (human + envelope);
#       completion fires with no open inline sigs (counts + ledger + done).
#   F7  fix writes only fix.json (findings.json byte-identical, no tmp left).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# 40-hex sigs (same length/shape as the kb case fixtures).
CRIT="c123456789abcdef0123456789abcdef01234567"
INFO="1123456789abcdef0123456789abcdef01234567"
INFO2="1123456789abcdef0123456789abcdef01234568"
ADDR="2123456789abcdef0123456789abcdef01234567"
SECT="3123456789abcdef0123456789abcdef01234567"
CEIL="4123456789abcdef0123456789abcdef01234567"
SCOP="5123456789abcdef0123456789abcdef01234567"

mk_local() { # $1 = bare slug (bundle lands at .review/local-$1)
  local slug="$1" dir="$EVAL_SANDBOX/.review/local-$1"
  mkdir -p "$dir"
  cat > "$dir/source.json" <<EOF
{"schema_version":"1","kind":"worktree","repo":"acme/app","slug":"$slug","base_ref":null,"head_ref":"work","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"","range":"HEAD:worktree","dirty":true,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
  cat > "$dir/full.diff" <<'EOF'
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
}

fix_write_findings() { # $1 = bare slug, stdin = findings array
  cat > "$EVAL_SANDBOX/.review/local-$1/findings.json"
}

mk_finding() { # $1=sig $2=kind $3=status $4=severity $5=path $6=body
  jq -n -c --arg sig "$1" --arg kind "$2" --arg st "$3" --arg sev "$4" --arg p "$5" --arg b "$6" \
    '{kind:$kind,path:(if $kind == "inline" then $p else null end),heading:(if $kind == "inline" then null else "Notes" end),side:"RIGHT",line:2,anchored:true,body:$b,suggestion:null,inBounds:true,rigor:"pointed",severity:$sev,kb:[],sig:$sig,sig_version:"1",firstSeen:"aaaaaaa",lastSeen:"aaaaaaa",status:$st,rationale:"",history:[]}'
}

# Scratch git repo for the worktree audits. Everything git runs in a subshell
# pinned to the sandbox cwd — never the caller's cwd, never the repo checkout
# under test. Harness artifacts are gitignored so status audits see only probe
# files (the script itself also ignores .review/ either way).
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "fix-eval@example.com"
  git config user.name "fix eval"
  cat > .gitignore <<'EOF'
.review/
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

# --- F1: default pick (severity first), pass output, envelope shape ---
mk_local fixpick
{
  mk_finding "$INFO" inline open informational notes.txt "Minor wording nit here."
  mk_finding "$CRIT" inline open critical app.txt "Critical boundary bug here."
} | jq -s '.' | fix_write_findings fixpick
before_sum="$(shasum -a 1 "$EVAL_SANDBOX/.review/local-fixpick/findings.json" | cut -d' ' -f1)"
run_review fix acme/app local-fixpick
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "default pick exits 0"
else
  bad "default pick exits $EVAL_STATUS"
fi
if grep -q "fix: ${CRIT:0:8} \\[critical\\] app.txt:2 (pass 1/3" "$EVAL_OUT"; then
  good "severity pick wins (critical app.txt, pass 1/3)"
else
  bad "pass line: $(head -c 300 "$EVAL_OUT")"
fi
if grep -q "readiness:" "$EVAL_OUT" && grep -q "finding: Critical boundary bug" "$EVAL_OUT" \
  && grep -q "anchors: app.txt:2-2" "$EVAL_OUT" && grep -q "instructions:" "$EVAL_OUT" \
  && grep -q "next: patch app.txt" "$EVAL_OUT"; then
  good "human output carries readiness + finding + anchors + instructions + next"
else
  bad "human output missing fix context"
fi
run_review fix acme/app local-fixpick --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ]; then
  good "fix --json exits 0 with exactly one envelope"
else
  bad "fix --json exit $EVAL_STATUS docs $(stdout_docs)"
fi
if [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT")" = "at data error next pr repo schema_version verb warnings" ] \
  && [ "$(jq -r '.verb' <"$EVAL_OUT")" = "fix" ] && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope carries exactly the documented keys with null error"
else
  bad "envelope shape broke"
fi
if [ "$(jq -r '.data.sig' <"$EVAL_OUT")" = "$CRIT" ] \
  && [ "$(jq -r '.data.pass' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '.data.max_passes' <"$EVAL_OUT")" = "3" ] \
  && [ "$(jq -r '.pr' <"$EVAL_OUT")" = "null" ] \
  && jq -e '.data.complete == false' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope data names the sig, pass 2/3, null pr, complete false"
else
  bad "envelope data: $(jq -c '.data | {sig,pass}' <"$EVAL_OUT" 2>/dev/null)"
fi
if grep -q "readiness:" "$EVAL_ERR" && grep -q "instructions:" "$EVAL_ERR"; then
  good "json mode keeps human progress on stderr"
else
  bad "stderr missing human progress in --json mode"
fi
if [ "$(jq -r ".sigs[\"$CRIT\"].passes" "$EVAL_SANDBOX/.review/local-fixpick/fix.json")" = "2" ] \
  && [ "$(jq -r ".sigs[\"$CRIT\"] | has(\"stopped\")" "$EVAL_SANDBOX/.review/local-fixpick/fix.json")" = "false" ]; then
  good "ledger records passes=2 with no tombstone"
else
  bad "ledger: $(cat "$EVAL_SANDBOX/.review/local-fixpick/fix.json" 2>/dev/null)"
fi
after_sum="$(shasum -a 1 "$EVAL_SANDBOX/.review/local-fixpick/findings.json" | cut -d' ' -f1)"
if [ "$before_sum" = "$after_sum" ] \
  && [ "$(ls "$EVAL_SANDBOX/.review/local-fixpick/" | LC_ALL=C sort | tr '\n' ',')" = "findings.json,fix.json,full.diff,source.json," ]; then
  good "fix writes only fix.json (findings byte-identical, no tmp left)"
else
  bad "bundle dir drifted: $(ls "$EVAL_SANDBOX/.review/local-fixpick/")"
fi

# Path-asc tiebreak at equal severity.
mk_local fixorder
{
  mk_finding "$INFO" inline open important b.txt "Bee file issue here."
  mk_finding "$INFO2" inline open important a.txt "Aye file issue here."
} | jq -s '.' | fix_write_findings fixorder
run_review fix acme/app local-fixorder --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.locator' <"$EVAL_OUT")" = "a.txt:2" ]; then
  good "equal severity breaks toward path asc (a.txt)"
else
  bad "tiebreak gave '$(jq -r '.data.locator // empty' <"$EVAL_OUT")'"
fi

# --- F2: --sig must be open + inline ---
mk_local fixbad
{
  mk_finding "$INFO" inline open informational notes.txt "Open nit here."
  mk_finding "$INFO2" inline open informational notes.txt "Second open nit."
  mk_finding "$ADDR" inline addressed informational notes.txt "Settled nit."
  mk_finding "$SECT" section open informational notes.txt "Section note."
} | jq -s '.' | fix_write_findings fixbad
run_review fix acme/app local-fixbad --sig "$ADDR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-sig" ]; then
  good "addressed --sig refuses as bad-sig"
else
  bad "addressed --sig gave exit $EVAL_STATUS code '$code'"
fi
run_review fix acme/app local-fixbad --sig "$SECT" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-sig" ]; then
  good "section --sig refuses as bad-sig (inline only)"
else
  bad "section --sig gave exit $EVAL_STATUS code '$code'"
fi
run_review fix acme/app local-fixbad --sig deadbeef --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-sig" ]; then
  good "unknown --sig refuses as bad-sig"
else
  bad "unknown --sig gave exit $EVAL_STATUS code '$code'"
fi
run_review fix acme/app local-fixbad --sig 1123 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "ambiguous-sig" ]; then
  good "ambiguous prefix refuses as ambiguous-sig"
else
  bad "ambiguous prefix gave exit $EVAL_STATUS code '$code'"
fi
if jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "sig refusals carry try: hints"
else
  bad "sig refusals missing try: hints"
fi
run_review fix acme/app local-fixbad --sig "$INFO2" --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.sig' <"$EVAL_OUT")" = "$INFO2" ]; then
  good "explicit open inline --sig is honored"
else
  bad "explicit --sig gave exit $EVAL_STATUS sig '$(jq -r '.data.sig // empty' <"$EVAL_OUT")'"
fi

# --- F3: locality, closed bundles, conflicted trees ---
PR=4291
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"PR finding.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review fix acme/app "$PR" --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "not-local" ] \
  && jq -e '.error.message | contains("fetch-local first")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "PR bundle refuses as not-local with the fetch-local pointer"
else
  bad "PR bundle gave exit $EVAL_STATUS code '$code'"
fi
mk_local fixclosed
mk_finding "$INFO" inline open informational notes.txt "Closed bundle nit." | jq -s '.' | fix_write_findings fixclosed
cat > "$EVAL_SANDBOX/.review/local-fixclosed/PR.json" <<'EOF'
{"state":"closed","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}
EOF
run_review fix acme/app local-fixclosed --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "pr-not-open" ]; then
  good "closed bundle refuses as pr-not-open"
else
  bad "closed bundle gave exit $EVAL_STATUS code '$code'"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -qb fixside
  printf 'line1\nSIDE\nline3\n' > notes.txt
  git commit -qam side
  git checkout -q -
  printf 'line1\nMAIN\nline3\n' > notes.txt
  git commit -qam main
  git merge fixside >/dev/null 2>&1 || true
)
if [ -z "$(git -C "$EVAL_SANDBOX" ls-files -u)" ]; then
  bad "conflict setup failed (no unmerged entries)"
else
  good "conflict setup holds unmerged entries"
fi
run_review fix acme/app local-fixbad --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "conflicted-tree" ] \
  && jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "conflicted worktree refuses as conflicted-tree with a try: hint"
else
  bad "conflicted tree gave exit $EVAL_STATUS code '$code'"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git merge --abort
  git branch -qD fixside >/dev/null
)
if [ -z "$(git -C "$EVAL_SANDBOX" ls-files -u)" ] && [ -z "$(git -C "$EVAL_SANDBOX" status --porcelain)" ]; then
  good "conflict aborted, tree clean again"
else
  bad "tree still dirty after abort: $(git -C "$EVAL_SANDBOX" status --porcelain)"
fi

# --- F4: ceiling refuses the 4th pass, tombstones once ---
mk_local fixceil
mk_finding "$CEIL" inline open important notes.txt "Stubborn bug here." | jq -s '.' | fix_write_findings fixceil
for n in 1 2 3; do
  run_review fix acme/app local-fixceil --json
  if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.pass' <"$EVAL_OUT")" = "$n" ]; then
    good "pass $n lands"
  else
    bad "pass $n gave exit $EVAL_STATUS pass '$(jq -r '.data.pass // empty' <"$EVAL_OUT")'"
  fi
done
run_review fix acme/app local-fixceil --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "fix-ceiling" ] \
  && jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && grep -q "stopped=1" "$EVAL_ERR"; then
  good "4th pass refuses as fix-ceiling with try: hint + stopped count"
else
  bad "4th pass gave exit $EVAL_STATUS code '$code'"
fi
if [ "$(jq -r ".sigs[\"$CEIL\"].passes" "$EVAL_SANDBOX/.review/local-fixceil/fix.json")" = "3" ] \
  && [ "$(jq -r ".sigs[\"$CEIL\"].stopped" "$EVAL_SANDBOX/.review/local-fixceil/fix.json")" = "ceiling" ]; then
  good "tombstone records stopped:ceiling, passes stay 3"
else
  bad "ledger: $(cat "$EVAL_SANDBOX/.review/local-fixceil/fix.json")"
fi
run_review fix acme/app local-fixceil --json >/dev/null
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "fix-ceiling" ] \
  && [ "$(jq -r ".sigs[\"$CEIL\"].passes" "$EVAL_SANDBOX/.review/local-fixceil/fix.json")" = "3" ] \
  && [ "$(jq -r ".sigs[\"$CEIL\"].stopped" "$EVAL_SANDBOX/.review/local-fixceil/fix.json")" = "ceiling" ]; then
  good "repeat refusal stays stable (still 3 passes, tombstone kept)"
else
  bad "5th pass gave exit $EVAL_STATUS code '$code'"
fi

# --- F5: scope-escape on out-of-anchor touch, recovery on revert ---
mk_local fixscope
mk_finding "$SCOP" inline open important notes.txt "Scoped bug here." | jq -s '.' | fix_write_findings fixscope
run_review fix acme/app local-fixscope --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.pass' <"$EVAL_OUT")" = "1" ]; then
  good "first pass records its baseline"
else
  bad "first pass gave exit $EVAL_STATUS"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'touched\n' >> other.txt
)
run_review fix acme/app local-fixscope --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "scope-escape" ] \
  && jq -e '.error.message | contains("other.txt")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "out-of-anchor touch stops the loop naming other.txt with a try: hint"
else
  bad "scope escape gave exit $EVAL_STATUS code '$code'"
fi
if [ "$(jq -r ".sigs[\"$SCOP\"].stopped" "$EVAL_SANDBOX/.review/local-fixscope/fix.json")" = "scope-escape" ] \
  && [ "$(jq -r ".sigs[\"$SCOP\"].passes" "$EVAL_SANDBOX/.review/local-fixscope/fix.json")" = "1" ]; then
  good "scope tombstone keeps passes at 1"
else
  bad "scope ledger: $(cat "$EVAL_SANDBOX/.review/local-fixscope/fix.json")"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- other.txt
)
run_review fix acme/app local-fixscope --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.pass' <"$EVAL_OUT")" = "2" ]; then
  good "reverting recovers the loop (pass 2)"
else
  bad "recovery gave exit $EVAL_STATUS"
fi
if [ "$(jq -r ".sigs[\"$SCOP\"] | has(\"stopped\")" "$EVAL_SANDBOX/.review/local-fixscope/fix.json")" = "false" ]; then
  good "recovery clears the stopped flag"
else
  bad "stopped flag stuck after recovery"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'touched again\n' >> other.txt
)
run_review fix acme/app local-fixscope --json >/dev/null 2>&1
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r ".sigs[\"$SCOP\"].passes" "$EVAL_SANDBOX/.review/local-fixscope/fix.json")" = "2" ] \
  && [ "$(jq -r ".sigs[\"$SCOP\"].stopped" "$EVAL_SANDBOX/.review/local-fixscope/fix.json")" = "scope-escape" ]; then
  good "repeat escape re-stops without advancing passes"
else
  bad "repeat escape moved the ledger"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  git checkout -q -- other.txt
)

# --- F5b: quoted anchor paths never false-trigger the audit ---
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'spaced\n' > "my file.txt"
  git add -A
  git commit -qm spaced
)
mk_local fixspace
mk_finding "$SCOP" inline open important "my file.txt" "Spaced anchor bug." | jq -s '.' | fix_write_findings fixspace
run_review fix acme/app local-fixspace --json >/dev/null
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'more\n' >> "my file.txt"
)
run_review fix acme/app local-fixspace --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.pass' <"$EVAL_OUT")" = "2" ]; then
  good "editing only a spaced anchor never scope-escapes"
else
  bad "spaced anchor false escape, exit $EVAL_STATUS"
fi

# --- F6: kb.txt recall + terminal completion ---
mk_local fixkb
mk_finding "$INFO" inline open informational notes.txt "Kb-guided nit." | jq -s '.' | fix_write_findings fixkb
cat > "$EVAL_SANDBOX/.review/local-fixkb/kb.txt" <<'EOF'
# kb recall — context, never orders. Cited rules inform the review; evidence still decides.
# fixkb-rule — notes.txt — precision 0/0
Check return values on every syscall in this file, seven-seven-seven.
EOF
run_review fix acme/app local-fixkb --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "seven-seven-seven" "$EVAL_ERR" \
  && jq -e '.data.kb.content | contains("seven-seven-seven")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.kb.file' <"$EVAL_OUT")" = ".review/local-fixkb/kb.txt" ]; then
  good "full kb.txt content surfaces on stderr and in the envelope"
else
  bad "kb recall missing from fix output"
fi
mk_local fixdone
mk_finding "$ADDR" inline addressed informational notes.txt "Settled nit." | jq -s '.' | fix_write_findings fixdone
run_review fix acme/app local-fixdone --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ] \
  && jq -e '.data.complete == true and .data.sig == null' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.next | contains("done")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && grep -q "fix complete: .review/local-fixdone — fixed=1 open=0 stopped=0 (ledger .review/local-fixdone/fix.json)" "$EVAL_ERR"; then
  good "completion fires with counts, ledger path, and a done next"
else
  bad "completion gave exit $EVAL_STATUS"
fi
run_review fix acme/app local-fixdone
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "fix complete: .review/local-fixdone" "$EVAL_OUT"; then
  good "completion works in human mode too"
else
  bad "human-mode completion gave exit $EVAL_STATUS"
fi

if [ "$fails" -ne 0 ]; then echo "case fix: FAIL ($fails)"; exit 1; fi
echo "case fix: PASS"
