#!/usr/bin/env bash
#
# Adopt hands a local review's findings to a GitLab bundle. The re-anchor reads
# the hunks the fetch stored, so the handoff shells nothing — the fetch itself
# goes through the network-dead `glab` stub — while a bundle on a host verdict
# cannot post to still refuses typed, naming that host.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"
PR=7
DIR="$EVAL_SANDBOX/.verdict/state/$PR"

mk_local_findings() { # $1 = bare slug, stdin = findings array
  local dir="$EVAL_SANDBOX/.verdict/state/local-$1"
  mkdir -p "$dir"
  cat > "$dir/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
  cat > "$dir/findings.json"
}

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "mr view $PR -R acme/app -F json" "$FIX/MR.json"
hoststub_route glab "api projects/acme%2Fapp --jq .id" "$FIX/project-id.txt"
hoststub_route glab "api projects/4242/merge_requests/$PR/diffs --paginate" "$FIX/diffs.json"
hoststub_route glab "mr diff $PR -R acme/app --raw" "$FIX/full.diff"
hoststub_route glab "api projects/4242/merge_requests/$PR/notes?per_page=100&page=1" "$FIX/notes.json"
hoststub_route glab "api projects/4242/merge_requests/$PR/discussions?per_page=100&page=1" "$FIX/discussions.json"

run_review fetch acme/app "$PR" --provider gitlab --json
assert_exit "gitlab fetch" 0
assert_json_field "the fetched bundle carries the gitlab marker" gitlab '.provider' "$DIR/PR.json"
assert_file_eq "the fetch indexed the fixture hunks" "$FIX/expected-hunks.tsv" "$DIR/hunks.tsv"
fetch_calls="$(hoststub_count glab)"

mk_local_findings adopt1 <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Adopt me.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":99,"anchored":true,"body":"Drifted anchor here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"123456789abcdef0123456789abcdef012345678","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review adopt acme/app "$PR" --from adopt1 --json
assert_exit "gitlab adopt" 0
assert_envelope "gitlab adopt envelope"
assert_json_field "the envelope names the source slug" adopt1 '.data.from'
assert_json_field "both findings are adopted" 2 '.data.adopted'
assert_json_field "both findings stay open" 2 '.data.open'
assert_json_field "the envelope points at the gitlab bundle" .verdict/state/7 '.data.dir'
assert_json_field "the adopt warns about nothing" 0 '.warnings | length'
assert_json_field "the bundle keeps its gitlab marker" gitlab '.provider' "$DIR/PR.json"
assert_json_field "the handoff stamp lands" adopted '.[0].history[-1].event' "$DIR/findings.json"
assert_json_field "the history names the source" "from adopt1" '.[0].history[-1].note' "$DIR/findings.json"
assert_json_field "the covered anchor holds" true '.[0].anchored' "$DIR/findings.json"
assert_json_field "the drifted anchor clears" false '.[1].anchored' "$DIR/findings.json"
assert_eq "the handoff shells no glab" "$fetch_calls" "$(hoststub_count glab)"

# Forgejo ships a submit path, so adopt hands findings to it like GitHub.
FORGEJO_PR=8
FORGEJO_DIR="$EVAL_SANDBOX/.verdict/state/$FORGEJO_PR"
cp -R "$DIR" "$FORGEJO_DIR"
rm -f "$FORGEJO_DIR/findings.json"
jq '.provider = "forgejo"' "$FORGEJO_DIR/PR.json" >"$FORGEJO_DIR/PR.json.tmp"
mv "$FORGEJO_DIR/PR.json.tmp" "$FORGEJO_DIR/PR.json"
run_review adopt acme/app "$FORGEJO_PR" --from adopt1 --json
assert_exit "forgejo adopt" 0
assert_envelope "forgejo adopt envelope"
assert_json_field "the bundle keeps its forgejo marker" forgejo '.provider' "$FORGEJO_DIR/PR.json"
assert_json_field "the forgejo handoff stamp lands" adopted '.[0].history[-1].event' "$FORGEJO_DIR/findings.json"
assert_json_field "the forgejo history names the source" "from adopt1" '.[0].history[-1].note' "$FORGEJO_DIR/findings.json"

# A bundle on a host verdict cannot post to has nowhere for the findings to go.
REFUSE_PR=9
REFUSE_DIR="$EVAL_SANDBOX/.verdict/state/$REFUSE_PR"
cp -R "$DIR" "$REFUSE_DIR"
rm -f "$REFUSE_DIR/findings.json"
jq '.provider = "gitcafe"' "$REFUSE_DIR/PR.json" >"$REFUSE_DIR/PR.json.tmp"
mv "$REFUSE_DIR/PR.json.tmp" "$REFUSE_DIR/PR.json"
before_sum="$(find "$REFUSE_DIR" -type f -exec shasum -a 1 {} + | sort | shasum -a 1 | cut -d' ' -f1)"
run_review adopt acme/app "$REFUSE_PR" --from adopt1 --json
assert_exit "gitcafe adopt refuses" 1
assert_json_field "the gitcafe refusal is typed" provider-unimplemented '.error.code'
assert_json_field "the refusal names the host" true '.error.message | contains("this bundle is \"gitcafe\"") '
after_sum="$(find "$REFUSE_DIR" -type f -exec shasum -a 1 {} + | sort | shasum -a 1 | cut -d' ' -f1)"
assert_eq "the refused adopt leaves the target byte-identical" "$before_sum" "$after_sum"
if [ -e "$REFUSE_DIR/findings.json" ]; then
  bad "the refused adopt wrote a findings store"
else
  good "the refused adopt writes nothing"
fi
assert_eq "the refusal shells no glab" "$fetch_calls" "$(hoststub_count glab)"

assert_eq "the case makes zero gh calls" 0 "$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
hoststub_assert_all_routed

eval_end
