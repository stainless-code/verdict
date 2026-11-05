#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SLUG="local-norm"
LB="$EVAL_SANDBOX/.review/$SLUG"

mk_local() { # $1 = slug dir; stdin becomes review.json
  local lb="$EVAL_SANDBOX/.review/$1"
  unset EVAL_NO_SEED
  rm -rf "$lb"
  mkdir -p "$lb"
  cat > "$lb/source.json" <<'JSON'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
JSON
  cat > "$lb/full.diff" <<'DIFF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
DIFF
  cat > "$lb/review.json"
  eval_seed_controls
  export EVAL_NO_SEED=1
}

WEAK_REVIEW='```json
{"schema_version":"1","body":"> ✅ Weak-model review with enough length to pass the guard.","approved":false,"resolved":"a1b2","comments":[{"file":"notes.txt","line":2,"body":"A finding with enough substance.","rung":"traced","severity":"Critical","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"Why is this safe?"}}]}
```'

printf '%s' "$WEAK_REVIEW" | mk_local "$SLUG"
run_review record acme/app "$SLUG" --dry-run --json
assert_exit "fenced aliased dry run warns and exits 0" 0
assert_envelope "normalized dry run"
assert_json_field "normalized path alias" "notes.txt" '.data.normalized.comments[0].path'
assert_json_field "normalized rigor synonym" "walked" '.data.normalized.comments[0].rigor'
assert_json_field "normalized severity case" "critical" '.data.normalized.comments[0].severity'
assert_json_field "normalized scalar resolved" "a1b2" '.data.normalized.resolved[0]'
assert_json_field "normalized scalar questions" "Why is this safe?" '.data.normalized.comments[0].brief.questions[0]'
for code in normalized-fence scalar-to-array key-alias enum-synonym; do
  if jq -e --arg c "$code" 'any(.data.repairs[]; .code == $c)' <"$EVAL_OUT" >/dev/null; then
    good "repair $code reported"
  else
    bad "repair $code missing from .data.repairs"
  fi
done
if grep -Fq '```' "$LB/review.json"; then
  good "dry run leaves review.json fenced on disk"
else
  bad "dry run rewrote review.json"
fi

run_review record acme/app "$SLUG" --json
assert_exit "fenced aliased record succeeds" 0
if jq -e 'any(.repairs[]; .code == "normalized-fence")' "$LB/record.json" >/dev/null; then
  good "record.json carries the fence repair"
else
  bad "record.json misses repairs: $(cat "$LB/record.json" 2>/dev/null || echo MISSING)"
fi
if [ "$(jq -r '.[] | select(.path == "notes.txt") | .rigor_source' "$LB/findings.json" 2>/dev/null)" = "model" ] \
  && [ "$(jq -r '.[] | select(.path == "notes.txt") | .rigor' "$LB/findings.json" 2>/dev/null)" = "walked" ]; then
  good "finding records the walked rung with model provenance"
else
  bad "finding misses rigor provenance"
fi

STRICT_BUNDLE="$EVAL_SANDBOX/.review/local-strictfetch"
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "record-normalize@example.com"
  git config user.name "record normalize"
  printf 'top\nold line\nbottom\n' > notes.txt
  printf '# verdict skill (eval fixture)\n' > SKILL.md
  mkdir -p prompts
  printf '# system (eval fixture)\n' > prompts/system.md
  printf '# review mode (eval fixture)\n' > prompts/review.md
  printf '# diagrams (eval fixture)\n' > prompts/diagrams.md
  git add notes.txt SKILL.md prompts/system.md prompts/review.md prompts/diagrams.md
  git commit -qm norm-base 2>/dev/null || true
  printf 'top\nnew line\nbottom\n' > notes.txt
)
export REVIEW_STRICT_INPUT=1
run_review fetch-local acme/app strictfetch -- notes.txt
unset REVIEW_STRICT_INPUT
assert_exit "strict fetch resolves" 0
if [ "$(jq -r '.strict_input // empty' "$STRICT_BUNDLE/controls.json" 2>/dev/null)" = "true" ]; then
  good "REVIEW_STRICT_INPUT=1 baked into controls.json"
else
  bad "fetch dropped REVIEW_STRICT_INPUT=1"
fi
printf '%s' "$WEAK_REVIEW" > "$STRICT_BUNDLE/review.json"
run_review record acme/app local-strictfetch --json
assert_exit "strict snapshot refuses the repaired review" 1
assert_refusal "strict repaired review" "bad-review-input"
if jq -e 'any(.error.problems[]; .code == "normalized-fence" and .level == "error")' <"$EVAL_OUT" >/dev/null; then
  good "strict promotes the fence repair to an error"
else
  bad "strict refusal misses the promoted fence repair"
fi
if [ ! -e "$STRICT_BUNDLE/findings.json" ] && [ ! -e "$STRICT_BUNDLE/record.json" ]; then
  good "strict refusal writes no findings or record"
else
  bad "strict refusal wrote store files"
fi

ENV_SLUG="local-recordenv"
ENV_BUNDLE="$EVAL_SANDBOX/.review/$ENV_SLUG"
printf '%s' "$WEAK_REVIEW" | mk_local "$ENV_SLUG"
if [ "$(jq -r '.strict_input // empty' "$ENV_BUNDLE/controls.json" 2>/dev/null)" != "" ]; then
  bad "record-env bundle baked strict_input at seed time"
else
  good "record-env bundle carries no fetch-time strict_input"
fi
REVIEW_STRICT_INPUT=1 run_review record acme/app "$ENV_SLUG" --dry-run --json
assert_exit "env-at-record strict dry run refuses" 1
assert_refusal "env-at-record strict review" "bad-review-input"
if jq -e 'any(.error.problems[]; .code == "normalized-fence" and .level == "error")' <"$EVAL_OUT" >/dev/null; then
  good "env-at-record promotes the fence repair to an error"
else
  bad "env-at-record refusal misses the promoted fence repair"
fi
if [ ! -e "$ENV_BUNDLE/findings.json" ] && [ ! -e "$ENV_BUNDLE/record.json" ]; then
  good "env-at-record refusal writes no findings or record"
else
  bad "env-at-record refusal wrote store files"
fi

unset EVAL_NO_SEED
rm -rf "$LB" "$STRICT_BUNDLE" "$ENV_BUNDLE"
eval_end
