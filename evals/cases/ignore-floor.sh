#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
cd "$EVAL_SANDBOX"
git init -q
git config user.email 'ignore@example.com'
git config user.name 'ignore eval'
mkdir -p .verdict vendor
printf 'vendor/**\n' > .verdict/ignore
printf '*.gen.go linguist-generated\n' > .gitattributes
printf 'a\n' > vendor/.env
printf 'a\n' > types.gen.go
for file in notes.txt other.txt; do
  awk 'BEGIN{for(i=0;i<2500;i++) printf "before %04d padding padding padding padding padding padding padding\n",i}' > "$file"
done
git add -f .verdict/ignore .gitattributes vendor/.env types.gen.go notes.txt other.txt
git commit -qm base
printf '**\n' > .verdict/ignore
printf '* linguist-generated\n' > .gitattributes
printf 'b\n' > vendor/.env
printf 'b\n' > types.gen.go
for file in notes.txt other.txt; do
  awk 'BEGIN{for(i=0;i<2500;i++) printf "after %04d padding padding padding padding padding padding padding\n",i}' > "$file"
done
git add -f .verdict/ignore .gitattributes vendor/.env types.gen.go notes.txt other.txt
git diff --cached > expected.raw.diff
run_review fetch-local acme/app silence --staged --worktree --chunk --json
assert_exit 'trusted exclusions fetch' 0
BUNDLE=.verdict/state/local-silence
if cmp -s expected.raw.diff "$BUNDLE/full.raw.diff"; then good 'raw bytes preserved'; else bad 'raw bytes changed'; fi
assert_eq 'ignore receipt' ignore:1 "$(jq -r '.excluded[] | select(.path=="vendor/.env") | .rule_id' "$BUNDLE/excluded.json")"
assert_eq 'attribute receipt' default:attribute "$(jq -r '.excluded[] | select(.path=="types.gen.go") | .rule_id' "$BUNDLE/excluded.json")"
assert_file_grep 'hostile head controls did not exclude notes' "$BUNDLE/full.diff" 'b/notes.txt'
assert_file_grep 'sensitive path survives exclusion' "$BUNDLE/sensitive.txt" 'vendor/.env'
assert_eq 'base exclusions use no host calls' 0 "$(wc -l < "$EVAL_STUBLOG" | tr -d ' ')"
assert_file_grep 'TOC names receipt' "$BUNDLE/toc.txt" 'vendor/.env (ignore:1,'
cat "$BUNDLE"/chunks/*.diff > concatenated.diff
if cmp -s concatenated.diff "$BUNDLE/full.diff"; then good 'chunks concatenate byte-exactly'; else bad 'chunks differ'; fi
run_review fetch-local acme/app defaults-off --staged --chunk --no-default-excludes --json
assert_exit 'disable defaults preserves file rules' 0
assert_eq 'base ignore still applies without defaults' ignore:1 "$(jq -r '.excluded[]|select(.path=="vendor/.env")|.rule_id' .verdict/state/local-defaults-off/excluded.json)"
assert_file_grep 'attribute defaults disabled alone' .verdict/state/local-defaults-off/full.diff 'b/types.gen.go'
run_review fetch-local acme/app overrides --staged --chunk --ignore '!vendor/**' --ignore notes.txt --no-default-excludes --json
assert_exit 'run overrides fetch' 0
OVERRIDES=.verdict/state/local-overrides
assert_file_grep 'CLI negation overrides base ignore' "$OVERRIDES/full.diff" 'b/vendor/.env'
assert_file_grep 'no defaults disables attributes' "$OVERRIDES/full.diff" 'b/types.gen.go'
assert_eq 'CLI exclusion receipt' cli:2 "$(jq -r '.excluded[]|select(.path=="notes.txt")|.rule_id' "$OVERRIDES/excluded.json")"
assert_eq 'CLI source receipt' cli "$(jq -r '.patterns_source' "$OVERRIDES/excluded.json")"
assert_eq 'disabled default receipt' true "$(jq -r '.no_default_excludes' "$OVERRIDES/excluded.json")"
jq -n --slurpfile manifest "$BUNDLE/chunks/manifest.json" '{schema_version:"1",body:"> Review complete. Policy exceptions are recorded.",chunks:($manifest[0].chunks|map(.name)),comments:[{path:"notes.txt",line:2,body:"Intentional compatibility behavior.",severity:"critical"},{path:"vendor/.env",line:1,body:"Keep credential material out of the review body.",severity:"important"}],suppressed:[{path:"notes.txt",line:2,source:"agent",reason:"An intentional compatibility exception for the legacy parser."},{path:"vendor/.env",source:"exclusion",exclusion_id:"ignore:1",reason:"This path is excluded at the trusted base."}]}' > "$BUNDLE/review.json"
run_review record acme/app local-silence --json
assert_exit 'record suppression receipts' 0
if [ "$EVAL_STATUS" -ne 0 ]; then jq '.error' < "$EVAL_OUT"; fi
assert_eq 'both findings kept open and withheld' 2 "$(jq '[.[]|select(.status=="open" and .withheld.source!=null)]|length' "$BUNDLE/findings.json")"
run_review report acme/app local-silence --check critical --live skip --sarif quiet.sarif --json
assert_exit 'withheld critical finding does not fail tier gate' 0
assert_json_field 'hidden default list' 0 '.data.items|length'
assert_eq 'SARIF has no withheld results' 0 "$(jq '.runs[0].results|length' quiet.sarif)"
assert_eq 'SARIF source count' 1 "$(jq '.runs[0].properties.withheld.exclusion' quiet.sarif)"
run_review report acme/app local-silence --include-withheld
assert_exit 'include quiet findings' 0
assert_file_grep 'quiet section' "$EVAL_OUT" 'Quiet by policy (2)'
run_review trend acme/app --json
assert_exit 'trend source counts' 0
assert_json_field 'agent count' 1 '.data.withheld.agent'
assert_json_field 'exclusion count' 1 '.data.withheld.exclusion'

eval_mk_bundle 7
eval_write_review 7 <<'JSON'
{"schema_version":"1","body":"> Review complete. Two observations were recorded.","comments":[{"path":"notes.txt","line":2,"body":"A minor informational observation.","severity":"informational"},{"path":"notes.txt","line":2,"body":"An observation without severity.","severity":null}]}
JSON
run_review post acme/app 7 --min-severity important --dry-run --json
assert_exit 'post severity floor' 0
assert_json_field 'floor count' 1 '.data.withheld.by_reason["below-severity"]'
assert_json_field 'unlabelled count' 1 '.data.withheld.unlabelled'
assert_json_field 'only unlabelled finding is publishable' 1 '.data.items|length'
assert_eq 'floor finding remains open' open "$(jq -r '.[]|select(.severity=="informational")|.status' .verdict/state/7/findings.dryrun.json)"
assert_file_grep 'null severity warning' "$EVAL_ERR" 'carry no severity; the floor does not apply'
run_review post acme/app 7 --min-severity important --approve true --bind --dry-run --json
assert_exit 'withheld open finding blocks approval' 1
assert_json_field 'approval refusal' approve-blocked '.error.code'
run_review post acme/app 7 --min-severity important --approve true --bind --force --dry-run --json
assert_exit 'force permits approval' 0
assert_json_field 'force retains floor' 1 '.data.withheld.by_reason["below-severity"]'
run_review post acme/app 7 --min-severity typo --dry-run --json
assert_exit 'bad floor refuses' 1
assert_json_field 'bad floor code' bad-min-severity '.error.code'
eval_end
