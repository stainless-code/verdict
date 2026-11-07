#!/usr/bin/env bash
# Chaos agent fixes a little each round, then abandons the loop; verdict stays re-enterable.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }
reviews_since() {
  tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true
}
calls_since() {
  tail -n +"$(( $1 + 1 ))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "" || true
}

snap() {
  local dir="$1" out="$2"
  if [ -d "$dir" ]; then
    (cd "$dir" && find . -type f -exec shasum {} + | LC_ALL=C sort) >"$out" 2>/dev/null || : >"$out"
  else
    printf 'ABSENT %s\n' "$dir" >"$out"
  fi
}
snap_same() {
  local label="$1" before="$2" after="$3"
  if cmp -s "$before" "$after"; then
    good "$label store byte-identical"
  else
    bad "$label store changed"
  fi
}
matrix_row() {
  printf 'matrix: verb=%s prior=%s exit=%s code=%s next=%s reenter=%s store=%s stub_reviews=%s\n' \
    "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8"
}
open_count() {
  run_review report acme/app loop1 --json
  jq -r '[.data.items[] | select(.status == "open")] | length' "$EVAL_OUT" 2>/dev/null || true
}
norm_store() {
  jq -S '[.[] | {sig, status}] | sort_by(.sig)' "$LB/findings.json" 2>/dev/null || true
}

git -C "$EVAL_SANDBOX" init -q 2>/dev/null || true
git -C "$EVAL_SANDBOX" config user.email chaos@example.com
git -C "$EVAL_SANDBOX" config user.name chaos
printf 'top\nold line\nbottom\nmore lines here\nand here\n' > "$EVAL_SANDBOX/notes.txt"
(cd "$EVAL_SANDBOX" && git add -A && git -c commit.gpgsign=false commit -qm chaos-base)
printf 'top\nnew line\nbottom\nmore lines here\nand here\n' > "$EVAL_SANDBOX/notes.txt"

LB="$EVAL_SANDBOX/.verdict/state/local-loop1"

m0="$(mark)"
run_review fetch-local acme/app loop1 --json
assert_exit "fetch-local baseline exits 0" 0
if [ "$(calls_since "$m0")" -eq 0 ]; then
  good "fetch-local made zero stub calls"
else
  bad "fetch-local reached the stub"
fi
matrix_row "fetch-local" "clean-tree" "$EVAL_STATUS" "none" "yes" "no" "created" "0"

cat > "$LB/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Three nits below with enough length to pass the guard.","approved":false,"comments":[
{"path":"notes.txt","line":2,"side":"RIGHT","body":"First nit with enough substance here.","rigor":"pointed","severity":"informational"},
{"path":"notes.txt","line":3,"side":"RIGHT","body":"Second nit with enough substance here.","rigor":"pointed","severity":"informational"},
{"path":"notes.txt","line":4,"side":"RIGHT","body":"Third nit with enough substance here.","rigor":"pointed","severity":"informational"}]}
EOF
m0="$(mark)"
run_review record acme/app loop1 --json
assert_exit "round 1 records 3 findings" 0
OPEN1="$(open_count)"
if [ "$OPEN1" = "3" ]; then
  good "round 1 leaves 3 open findings"
else
  bad "round 1 open count is $OPEN1, want 3"
fi
run_review gate acme/app loop1 --max-severity informational --live skip --json
assert_exit "gate with 3 open findings exits 1" 1
if [ "$(jq -r '.data.passed' "$EVAL_OUT" 2>/dev/null)" = "false" ] \
  && jq -e '.error == null' "$EVAL_OUT" >/dev/null 2>&1; then
  good "open gate failure is a verdict, not a refusal"
else
  bad "gate failure broke the verdict shape"
fi
matrix_row "record" "round-1" "0" "none" "yes" "no" "merged" "$(reviews_since "$m0")"
matrix_row "gate" "3-open" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

run_review report acme/app loop1 --json
SIG1="$(jq -r '.data.items[0].sig' "$EVAL_OUT" 2>/dev/null || true)"
if [ -n "$SIG1" ] && [ "$SIG1" != "null" ]; then
  good "round 1 sig captured for the round 2 claim"
else
  bad "round 1 sig missing"
fi
cat > "$LB/review.json" <<EOF
{"schema_version":"1","body":"> ✅ Two nits remain with enough length to pass the guard.","approved":false,"comments":[
{"path":"notes.txt","line":3,"side":"RIGHT","body":"Second nit with enough substance here.","rigor":"pointed","severity":"informational"},
{"path":"notes.txt","line":4,"side":"RIGHT","body":"Third nit with enough substance here.","rigor":"pointed","severity":"informational"}],
"triage":[{"sig":"$SIG1","status":"wont-fix","rationale":"By design: this line is intentionally terse for log brevity."}]}
EOF
cp "$LB/review.json" "$EVAL_SANDBOX/round2.json"
m0="$(mark)"
run_review record acme/app loop1 --json
assert_exit "round 2 records (one fewer problem)" 0
OPEN2="$(open_count)"
if [ "$OPEN2" = "2" ]; then
  good "round 2 leaves 2 open findings (problems shrink but remain)"
else
  bad "round 2 open count is $OPEN2, want 2"
fi
run_review gate acme/app loop1 --max-severity informational --live skip --json
assert_exit "gate with 2 open findings still exits 1" 1
matrix_row "record" "round-2" "0" "none" "yes" "no" "merged" "$(reviews_since "$m0")"
matrix_row "gate" "2-open" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

norm_store > "$EVAL_SANDBOX/f.r2"
cp "$EVAL_SANDBOX/round2.json" "$LB/review.json"
run_review record acme/app loop1 --json
assert_exit "re-submitting round 2 exits 0" 0
norm_store > "$EVAL_SANDBOX/f.r2b"
if cmp -s "$EVAL_SANDBOX/f.r2" "$EVAL_SANDBOX/f.r2b"; then
  good "re-submitted round 2 is a bounded merge (sig set + statuses stable)"
else
  bad "re-submitted round 2 changed the sig set or statuses"
fi
matrix_row "record" "repeat-round-2" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

run_review report acme/app loop1 --json
assert_exit "abandoned loop still reports (exit 0)" 0
if [ "$(jq -r '[.data.items[] | select(.status == "open")] | length' "$EVAL_OUT" 2>/dev/null)" = "2" ]; then
  good "abandoned loop reports the 2 remaining opens"
else
  bad "report after abandon lost findings"
fi
NEXT_SIG="$(jq -r '.data.items[0].sig' "$EVAL_OUT" 2>/dev/null || true)"
run_review next acme/app loop1 --json
assert_exit "abandoned loop still has a next step (exit 0)" 0
if [ -n "$(jq -r '.next // empty' "$EVAL_OUT" 2>/dev/null || true)" ]; then
  good "next names the way forward after abandon"
else
  bad "next is empty after abandon"
fi
run_review show acme/app loop1 --sig "$NEXT_SIG" --json
assert_exit "show resolves the carried sig (exit 0)" 0
assert_json_field "show returns the carried finding" "${NEXT_SIG:0:8}" '.data.item.sig | .[0:8]'
run_review gate acme/app loop1 --max-severity informational --live skip --json
assert_exit "gate after abandon still exits 1 (verdict, not refusal)" 1
matrix_row "report" "abandoned" "0" "none" "yes" "no" "same" "0"
matrix_row "next" "abandoned" "0" "none" "yes" "no" "same" "0"
matrix_row "show" "abandoned" "0" "none" "yes" "no" "same" "0"
matrix_row "gate" "abandoned" "1" "none" "yes" "no" "same" "0"

snap "$LB" "$EVAL_SANDBOX/s5.before"
m0="$(mark)"
run_review fix acme/app loop1 --json
assert_exit "re-entry via fix exits 0" 0
if [ -f "$LB/fix.json" ]; then
  good "fix writes only its ledger (fix.json)"
else
  bad "fix wrote no ledger"
fi
snap "$LB" "$EVAL_SANDBOX/s5.after"
if cmp -s <(grep -v 'fix.json' "$EVAL_SANDBOX/s5.before") <(grep -v 'fix.json' "$EVAL_SANDBOX/s5.after"); then
  good "fix left the findings store alone"
else
  bad "fix touched the findings store"
fi
if [ "$(calls_since "$m0")" -eq 0 ]; then
  good "fix made zero stub calls"
else
  bad "fix reached the stub"
fi
matrix_row "fix" "abandoned" "$EVAL_STATUS" "none" "yes" "no" "ledger-only" "0"

run_review report acme/app loop1 --json
SIGS="$(jq -r '.data.items[] | select(.status == "open") | .sig' "$EVAL_OUT" 2>/dev/null || true)"
R2A="$(printf '%s' "$SIGS" | sed -n '1p')"
R2B="$(printf '%s' "$SIGS" | sed -n '2p')"
cat > "$LB/review.json" <<EOF
{"schema_version":"1","body":"> ✅ Final round with enough length to pass the guard here.","approved":false,"comments":[],
"triage":[{"sig":"$R2A","status":"wont-fix","rationale":"By design: this hunk is intentionally terse for log brevity."},{"sig":"$R2B","status":"wont-fix","rationale":"By design: this hunk is intentionally terse for log brevity."}]}
EOF
run_review record acme/app loop1 --json
assert_exit "final round records (loop closed)" 0
OPEN3="$(open_count)"
if [ "$OPEN3" = "0" ]; then
  good "final round leaves 0 open findings"
else
  bad "final round open count is $OPEN3, want 0"
fi
run_review gate acme/app loop1 --max-severity informational --live skip --json
assert_exit "gate on the closed loop exits 0" 0
matrix_row "record" "round-3" "0" "none" "yes" "no" "merged" "0"
matrix_row "gate" "0-open" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

cp "$LB/review.json" "$EVAL_SANDBOX/round3.json"
norm_store > "$EVAL_SANDBOX/f.r3"
cp "$EVAL_SANDBOX/round3.json" "$LB/review.json"
run_review record acme/app loop1 --json
assert_exit "re-submitting the final round exits 0" 0
norm_store > "$EVAL_SANDBOX/f.r3b"
if cmp -s "$EVAL_SANDBOX/f.r3" "$EVAL_SANDBOX/f.r3b"; then
  good "re-submitted final round keeps the sig set + statuses stable"
else
  bad "re-submitted final round changed the sig set or statuses"
fi
matrix_row "record" "repeat-round-3" "$EVAL_STATUS" "none" "yes" "no" "same" "0"

eval_end
