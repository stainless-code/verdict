#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
eval_mk_bundle local-panel
dir="$EVAL_SANDBOX/.verdict/state/local-panel"
printf 'synth\nverifier-a\nverifier-b\n' > "$dir/panel"
printf 'src/db.ts\tNEW\t10\t20\n' > "$dir/hunks.tsv"
cat > "$dir/full.diff" <<'DIFF'
diff --git a/src/db.ts b/src/db.ts
index 1111111..2222222 100644
--- a/src/db.ts
+++ b/src/db.ts
@@ -10,2 +10,2 @@
-old guard
-old dedup
+unguarded call
+duplicate write
DIFF
cat > "$dir/review.json" <<'JSON'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Independent review with two anchored findings.","approved":false,"comments":[{"path":"src/db.ts","line":10,"side":"RIGHT","body":"The caller passes nil and the removed guard causes a crash.","severity":"important","rigor":"walked"},{"path":"src/db.ts","line":11,"side":"RIGHT","body":"The retry path repeats the write without its previous deduplication guard.","severity":"important","rigor":"walked"}]}
JSON
cp "$dir/review.json" "$EVAL_SANDBOX/review-before.json"
cp "$dir/panel" "$EVAL_SANDBOX/roster-before"
run_review panel acme/app local-panel --json
assert_exit 'planner succeeds' 0
assert_json_field 'two verifiers and four dispatches' 'true' '.data.verifiers == 2 and .data.findings == 2 and .data.dispatches == 4'
assert_file_eq 'planner leaves review unchanged' "$EVAL_SANDBOX/review-before.json" "$dir/review.json"
assert_file_eq 'planner preserves roster file' "$EVAL_SANDBOX/roster-before" "$dir/panel"
assert_eq 'planner creates no store or receipt' '0' "$(find "$dir" -maxdepth 1 \( -name findings.json -o -name post.json -o -name record.json \) | wc -l | tr -d ' ')"
a="$(jq -r '.findings[0].sig' "$dir/verification/plan.json")"
b="$(jq -r '.findings[1].sig' "$dir/verification/plan.json")"
jq -n --arg a "$a" --arg b "$b" --arg h "$EVAL_FIXTURE_HEAD" \
  '{schema_version:"1",head_sha:$h,results:[{sig:$a,model:"verifier-a",provider:"one",verdict:"confirmed",evidence:"SECRET_VERIFIER_EVIDENCE confirms the missing guard."},{sig:$b,model:"verifier-a",provider:"one",verdict:"refuted",evidence:"SECRET_VERIFIER_EVIDENCE shows the caller deduplicates first."}]}' > "$dir/verification/verdicts.json"
cp "$dir/verification/verdicts.json" "$EVAL_SANDBOX/verdicts-before.json"
run_review record acme/app local-panel --json
if [ "$EVAL_STATUS" -ne 0 ]; then cat "$EVAL_OUT"; cat "$EVAL_ERR"; bail 'record applies invoker verdicts'; fi
assert_exit 'record applies invoker verdicts' 0
assert_json_field 'record derives counts' 'true' '.data.verification == {verifiers:1,confirmed:1,contested:1,single:0}'
assert_eq 'refutation keeps both findings open' '2' "$(jq '[.[]|select(.status == "open")]|length' "$dir/findings.json")"
run_review panel acme/app local-panel --json
assert_exit 'second-look planning succeeds' 0
assert_json_field 'exactly one second look' '1' '.data.second_look'
assert_file_grep 'dispatch includes prior evidence' "$dir/verification/dispatch.md" '^> SECRET_VERIFIER_EVIDENCE shows'
assert_file_eq 'planner never rewrites verdicts' "$EVAL_SANDBOX/verdicts-before.json" "$dir/verification/verdicts.json"
jq --arg b "$b" '.results += [{sig:$b,model:"verifier-b",verdict:"confirmed",evidence:"The second look confirms a reachable duplicate write."}]' \
  "$dir/verification/verdicts.json" > "$EVAL_SANDBOX/next-verdicts.json"
mv "$EVAL_SANDBOX/next-verdicts.json" "$dir/verification/verdicts.json"
run_review record acme/app local-panel --json
assert_exit 'second-look record succeeds' 0
assert_json_field 'confirmation cannot outvote refutation' '1' '.data.verification.contested'
run_review report acme/app local-panel --json
assert_exit 'report reads stored opinions' 0
assert_json_field 'report shows contested count' '1' '.data.verification.contested'
cp "$dir/verification/verdicts.json" "$EVAL_SANDBOX/good-verdicts.json"
jq '.head_sha="old"' "$EVAL_SANDBOX/good-verdicts.json" > "$dir/verification/verdicts.json"
run_review record acme/app local-panel --json
assert_refusal 'stale verifier receipt refuses' panel-stale
jq '.results += [.results[0]]' "$EVAL_SANDBOX/good-verdicts.json" > "$dir/verification/verdicts.json"
run_review record acme/app local-panel --json
assert_refusal 'duplicate verifier receipt refuses' bad-panel
jq '.results[0].verdict="maybe"' "$EVAL_SANDBOX/good-verdicts.json" > "$dir/verification/verdicts.json"
run_review panel acme/app local-panel --json
assert_refusal 'unknown verifier verdict refuses' bad-panel
cp "$EVAL_SANDBOX/good-verdicts.json" "$dir/verification/verdicts.json"
rm "$dir/panel"
run_review panel acme/app local-panel --json
assert_refusal 'missing pinned roster refuses' panel-empty
assert_eq 'local panel flow makes no host calls' '0' "$(wc -l < "$EVAL_STUBLOG" | tr -d ' ')"
eval_end
