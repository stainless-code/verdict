#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "provenance-eval@example.com"
  git config user.name "provenance eval"
  grep -qxF '.verdict/state/' .gitignore 2>/dev/null || echo ".verdict/state/" >> .gitignore
  mkdir -p src .verdict
  echo "base" > src/a.ts
  cat > .verdict/config.json <<'EOF'
{"schema_version":"1","defaults":{"kb_policy":{"max_chars":120}}}
EOF
  cat > .verdict/learnings.md <<'EOF'
- SECRET_LEARNING_TEXT_DO_NOT_LEAK
EOF
  git add -A
  git commit -qm provenance-base 2>/dev/null || true
)

for idx in 1 2 3 4; do
  run_review learn --rule "SECRET_RULE_BODY_$idx Always validate boundary conditions in src handlers carefully" --scope "src/*.ts" --id "prov-r$idx" --json >/dev/null
done
eval_commit_controls

(
  cd "$EVAL_SANDBOX" || exit 99
  echo "changed" >> src/a.ts
)

run_review fetch-local acme/app prov1 --json
assert_exit "fetch-local with over-cap KB and max_chars exits 0" 0
receipt_file="$EVAL_SANDBOX/.verdict/state/local-prov1/provenance.json"
if [ -f "$receipt_file" ]; then
  good "fetch-local writes provenance.json"
else
  bad "provenance.json missing after fetch-local"
fi

run_id_1="$(jq -r '.run_id // empty' "$receipt_file" 2>/dev/null)"
kb_status="$(jq -r '.inputs[] | select(.kind == "kb") | .status' "$receipt_file" 2>/dev/null)"
kb_reason="$(jq -r '.inputs[] | select(.kind == "kb") | .reason' "$receipt_file" 2>/dev/null)"
kb_cap="$(jq -r '.inputs[] | select(.kind == "kb") | .cap' "$receipt_file" 2>/dev/null)"
kb_inc="$(jq -r '.inputs[] | select(.kind == "kb") | .included' "$receipt_file" 2>/dev/null)"
kb_om="$(jq -r '.inputs[] | select(.kind == "kb") | .omitted' "$receipt_file" 2>/dev/null)"
learn_status="$(jq -r '.inputs[] | select(.kind == "learnings") | .status' "$receipt_file" 2>/dev/null)"
top_n_count="$(jq -r '[.inputs[] | select(.kind == "kb_candidate" and .reason == "top_n")] | length' "$receipt_file" 2>/dev/null)"

if [ -n "$run_id_1" ] && [ "$kb_status" = "truncated" ] && [ "$kb_reason" = "cap" ] && [ "$kb_cap" = "120" ] \
  && [ "$kb_inc" = "3" ] && [ "$kb_om" = "1" ] && [ "$learn_status" = "included" ] && [ "$top_n_count" = "1" ]; then
  good "provenance.json records kb cap truncation, top_n omission, and included learnings"
else
  bad "provenance.json fields unexpected: run=$run_id_1 kb=$kb_status/$kb_reason/$kb_cap($kb_inc/$kb_om) learn=$learn_status topn=$top_n_count"
fi

assert_file_nogrep "provenance.json copies zero rule text" "$receipt_file" "SECRET_RULE_BODY_" "-F"
assert_file_nogrep "provenance.json copies zero learning text" "$receipt_file" "SECRET_LEARNING_TEXT_DO_NOT_LEAK" "-F"

# Identical re-run preserves run_id; changed control flips run_id.
run_review fetch-local acme/app prov1 --json >/dev/null || true
run_id_2="$(jq -r '.run_id // empty' "$receipt_file" 2>/dev/null)"
if [ "$run_id_1" = "$run_id_2" ]; then
  good "run_id is deterministic across identical runs"
else
  bad "run_id drifted on identical re-run: $run_id_1 vs $run_id_2"
fi

cat > "$EVAL_SANDBOX/.verdict/config.json" <<'EOF'
{"schema_version":"1","defaults":{"kb_policy":{"max_chars":120},"effort":"deep"}}
EOF
eval_commit_controls
run_review fetch-local acme/app prov1 --json >/dev/null || true
run_id_3="$(jq -r '.run_id // empty' "$receipt_file" 2>/dev/null)"
if [ -n "$run_id_3" ] && [ "$run_id_3" != "$run_id_1" ]; then
  good "changing control SHA updates run_id"
else
  bad "changing control config left run_id unchanged ($run_id_1 vs $run_id_3)"
fi

# Record a review on local-prov1 so report has findings.json.
cat > "$EVAL_SANDBOX/.verdict/state/local-prov1/review.json" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
run_review record acme/app local-prov1 --json >/dev/null

# report --provenance exposes data.receipt.inputs[] & data.truncated[], omitted without --provenance.
run_review report acme/app local-prov1 --provenance --json
assert_exit "report --provenance --json exits 0" 0
if jq -e '.data.receipt.inputs | length > 0' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.data.truncated | length >= 2' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "report --provenance --json exposes data.receipt.inputs[] and data.truncated[]"
else
  bad "report --provenance --json missed receipt or truncated: $(cat "$EVAL_OUT")"
fi

run_review report acme/app local-prov1 --json
assert_exit "report without --provenance exits 0" 0
if jq -e '(.data | has("receipt") | not) and (.data | has("truncated") | not)' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "report without --provenance omits provenance keys"
else
  bad "report without --provenance leaked provenance keys: $(cat "$EVAL_OUT")"
fi

# post --dry-run --provenance emits one > Inputs: footer line and receipt/truncated; old bundle omits > Inputs:.
PR=4901
eval_mk_bundle "$PR"
cp "$receipt_file" "$EVAL_SANDBOX/.verdict/state/$PR/provenance.json"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --provenance --json
assert_exit "post --dry-run --provenance --json exits 0" 0
footer_txt="$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"
inputs_lines="$(printf '%s\n' "$footer_txt" | grep -c '^> Inputs: ' || true)"
if [ "$inputs_lines" = "1" ] && jq -e '.data.receipt.inputs | length > 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "post --dry-run --provenance renders one > Inputs: line and data.receipt.inputs[]"
else
  bad "post --dry-run --provenance failed footer/receipt check (lines=$inputs_lines)"
fi

PR_OLD=4902
eval_mk_bundle "$PR_OLD"
rm -f "$EVAL_SANDBOX/.verdict/state/$PR_OLD/provenance.json"
eval_write_review "$PR_OLD" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR_OLD" --dry-run --json
assert_exit "old bundle post --dry-run exits 0" 0
old_footer="$(jq -r '.data.body.footer // empty' <"$EVAL_OUT" 2>/dev/null)"
printf '%s' "$old_footer" >"$EVAL_SANDBOX/old-footer.txt"
assert_file_nogrep "old bundle without provenance.json keeps old footer byte-identical without Inputs line" \
  "$EVAL_SANDBOX/old-footer.txt" '> Inputs:' "-F"

# Malformed provenance.json warns and degrades gracefully without failing report.
printf '{corrupt' > "$receipt_file"
run_review report acme/app local-prov1 --provenance --json
assert_exit "report --provenance with malformed provenance.json exits 0" 0
if jq -e '.warnings | map(select(contains("provenance.json"))) | length >= 1' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "malformed provenance.json degrades with a warning"
else
  bad "malformed provenance.json missed warning: $(cat "$EVAL_OUT")"
fi
eval_end
