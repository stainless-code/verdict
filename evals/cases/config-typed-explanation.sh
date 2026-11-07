#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_BUDGET_USD REVIEW_MODEL REVIEW_EFFORT || true

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "config-explanation@example.com"
  git config user.name "config explanation"
  cat > .gitignore <<'EOF'
.verdict/state/
stubbin/
fakebin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
EOF
  mkdir -p .verdict vendor app
  cat > .verdict/config.json <<'EOF'
{"schema_version":"1",
 "defaults":{"effort":"standard","display_min_severity":"important"},
 "scopes":[{"name":"vendored","include":["vendor/**"],
 "defaults":{"effort":"deep","display_min_severity":"critical"}}]}
EOF
  mkdir -p prompts
  printf '# system (eval fixture)\n' > prompts/system.md
  printf '# review mode (eval fixture)\n' > prompts/review.md
  printf 'base vendor\n' > vendor/lib.go
  printf 'base app\n' > app/main.go
  git add .gitignore .verdict/config.json prompts/system.md prompts/review.md vendor/lib.go app/main.go
  git commit -qm typed-config-base 2>/dev/null || true
  printf 'base vendor\nstaged vendor\n' > vendor/lib.go
  printf 'base app\nstaged app\n' > app/main.go
  git add vendor/lib.go app/main.go
)

run_review fetch-local acme/app cfgprobe --staged --worktree
assert_exit "fetch-local produces the bundle" 0
assert_file_grep "the producer captured a per-file resolution" \
  "$EVAL_SANDBOX/.verdict/state/local-cfgprobe/run.json" '"path": "vendor/lib.go"'

run_review config acme/app local-cfgprobe --explain --json
assert_exit "explain reads the stored capture" 0
assert_json_field "vendored file keeps its scope effort" "deep" \
  '.data.files[] | select(.path=="vendor/lib.go") | .values[] | select(.key=="effort") | .value'
assert_json_field "vendored effort origin is the scope layer" "scope" \
  '.data.files[] | select(.path=="vendor/lib.go") | .values[] | select(.key=="effort") | .layer'
assert_json_field "ordinary file keeps the repository effort" "standard" \
  '.data.files[] | select(.path=="app/main.go") | .values[] | select(.key=="effort") | .value'
assert_json_field "the scope override reports the value it shadowed" "standard" \
  '[.data.files[] | select(.path=="vendor/lib.go") | .shadowed[] | select(.key=="effort" and .layer=="repo") | .value] | join(",")'
assert_json_field "every losing layer is reported for the shadowed field" "repo,built-in" \
  '[.data.files[] | select(.path=="vendor/lib.go") | .shadowed[] | select(.key=="effort" and .value=="standard") | .layer] | join(",")'

assert_json_field "explain carries its invocation timestamp" "string" '.at | type'
jq -S 'del(.at)' "$EVAL_OUT" >"$EVAL_SANDBOX/explain-saved.json"

(
  cd "$EVAL_SANDBOX" || exit 99
  cat > .verdict/config.json <<'EOF'
{"schema_version":"1","defaults":{"effort":"cheap","display_min_severity":"informational"}}
EOF
)
export REVIEW_EFFORT=cheap
run_review config acme/app local-cfgprobe --explain --json
assert_exit "explain still reads the capture after drift" 0
assert_json_field "replayed explain carries its invocation timestamp" "string" '.at | type'
jq -S 'del(.at)' "$EVAL_OUT" >"$EVAL_SANDBOX/explain-current.json"
cmp -s "$EVAL_SANDBOX/explain-saved.json" "$EVAL_SANDBOX/explain-current.json" \
  && good "saved values beat the changed configuration and environment" \
  || bad "explain re-read the working configuration or environment"
unset REVIEW_EFFORT

cat > "$EVAL_SANDBOX/proposed.json" <<'EOF'
{"schema_version":"1","defaults":{"effort":"standard","budget_usd":1.0}}
EOF
PROPOSED="$EVAL_SANDBOX/proposed.json"
TREE_BEFORE="$(cd "$EVAL_SANDBOX" && find .verdict -type f ! -name '*.json.tmp' -exec shasum -a 256 {} + | sort | shasum -a 256)"
run_review config acme/app local-cfgprobe --preview --file "$PROPOSED" --json
assert_exit "preview accepts a proposed configuration" 0
assert_json_field "preview names its mode" "preview" '.data.mode'
assert_json_field "preview is untrusted" "false" '.data.trusted'
assert_json_field "preview reports the proposed effort" "standard" \
  '.data.values[] | select(.key=="effort") | .value'
assert_json_field "preview resolves the same reviewed paths" "2" '.data.files | length'
assert_json_field "preview drops the saved scope for the proposed defaults" "standard" \
  '.data.files[] | select(.path=="vendor/lib.go") | .values[] | select(.key=="effort") | .value'
TREE_AFTER="$(cd "$EVAL_SANDBOX" && find .verdict -type f ! -name '*.json.tmp' -exec shasum -a 256 {} + | sort | shasum -a 256)"
assert_eq "preview writes nothing" "$TREE_BEFORE" "$TREE_AFTER"

rm -rf "$EVAL_SANDBOX/.verdict" "$EVAL_SANDBOX/xdg-empty"
rm -f "$EVAL_SANDBOX"/explain-saved.json "$EVAL_SANDBOX"/explain-current.json "$EVAL_SANDBOX"/proposed.json
unset XDG_CONFIG_HOME || true

eval_end
