#!/usr/bin/env bash
#
# Thread resolve on GitHub: the default closes the self-authored addressed
# thread, a dry run writes nothing, a second run reads its own result, a human
# thread named with --thread refuses, a forged-marker thread refuses with
# --thread and skips by default, an unreadable login refuses before any write,
# and calibrate sees the resolved state on a follow-up fixture read.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/threads-resolve"
HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
PR=7
DIR="$EVAL_SANDBOX/.verdict/state/$PR"
RECEIPT="$DIR/threads-resolve.json"

hoststub_init gh
hoststub_prove_dead gh
# The calibrate join's GraphQL query is multi-line, so it cannot be an exact
# hoststub route; this stub matches the five argv shapes and logs every call
# in the same format, so the resolve write is still compared literally.
cat >"$EVAL_STUBBIN/gh" <<STUB
#!/usr/bin/env bash
set -uo pipefail
argv="\$*"
printf 'stub-gh %s\n' "\$argv" >>"\${HOSTSTUB_LOG:?}"
case "\$argv" in
  *resolveReviewThread*) cat "$FIX/github-resolve.json"; exit 0;;
  *"api user"*) cat "$FIX/github-user.txt"; exit 0;;
  *"api graphql"*) cat "$FIX/github-calibrate-threads.json"; exit 0;;
  *"pr view"*) cat "$FIX/github-pr.json"; exit 0;;
  *compare*) cat "$FIX/github-compare.json"; exit 0;;
esac
printf 'stub-gh UNRECOGNISED %s\n' "\$argv" >>"\${HOSTSTUB_LOG:?}"
exit 64
STUB
chmod +x "$EVAL_STUBBIN/gh"

mkdir -p "$DIR"
printf '{"state":"OPEN","headRefOid":"%s"}\n' "$HEAD_A" >"$DIR/PR.json"
cp "$FIX/github-threads.json" "$DIR/threads.json"
cp "$FIX/github-findings.json" "$DIR/findings.json"

run_review threads resolve acme/app "$PR" --dry-run --json
assert_exit "dry-run" 0
assert_envelope "dry-run envelope"
assert_json_field "dry-run lists the self-authored addressed thread" "PRRT_kwDOacme" '.data.resolved[0]'
assert_json_field "dry-run is marked" true '.data.dry_run'
assert_json_field "dry-run skips the already-resolved thread" already-resolved \
  '.data.skipped[] | select(.id == "PRRT_done") | .reason'
assert_json_field "dry-run skips the forged-marker thread" not-self-authored \
  '.data.skipped[] | select(.id == "PRRT_forged") | .reason'
assert_eq "dry-run made one self-login read" 1 "$(hoststub_count gh)"
if [ ! -f "$RECEIPT" ]; then
  good "dry-run wrote no receipt"
else
  bad "dry-run wrote $RECEIPT"
fi
: >"$HOSTSTUB_LOG"

run_review threads resolve acme/app "$PR" --json
assert_exit "real resolve" 0
assert_envelope "real resolve envelope"
assert_json_field "real resolve closes the thread" "PRRT_kwDOacme" '.data.resolved[0]'
assert_json_field "real resolve is not a dry run" false '.data.dry_run'
hoststub_argv_log gh "$EVAL_SANDBOX/argv.log"
assert_file_eq "the self-login read plus the one write are the literal argvs" "$FIX/expected-github-argvs.txt" "$EVAL_SANDBOX/argv.log"
assert_json_field "receipt records the host" github '.provider' "$RECEIPT"
assert_json_field "receipt records the reviewed head" "$HEAD_A" '.head_sha' "$RECEIPT"
assert_json_field "receipt records the closed id" "PRRT_kwDOacme" '.resolved[0]' "$RECEIPT"
assert_json_field "receipt records the skip reason" already-resolved \
  '.skipped[] | select(.id == "PRRT_done") | .reason' "$RECEIPT"
assert_json_field "receipt records the forged skip" not-self-authored \
  '.skipped[] | select(.id == "PRRT_forged") | .reason' "$RECEIPT"
assert_json_field "the feed marks the thread resolved" true '.threads[0].resolved' "$DIR/threads.json"

before="$(hoststub_count gh)"
run_review threads resolve acme/app "$PR" --json
assert_exit "second resolve" 0
assert_json_field "second resolve writes nothing" 0 '.data.resolved | length'
assert_json_field "second resolve reports the thread already resolved" already-resolved \
  '.data.skipped[] | select(.id == "PRRT_kwDOacme") | .reason'
assert_eq "second resolve made one self-login read" "$((before + 1))" "$(hoststub_count gh)"

before="$(hoststub_count gh)"
cp "$RECEIPT" "$EVAL_SANDBOX/receipt-before.json"
run_review threads resolve acme/app "$PR" --thread PRRT_human --json
assert_exit "human thread named with --thread" 1
assert_refusal "human thread refusal" not-verdict-authored
assert_eq "human refusal made one self-login read and no write" "$((before + 1))" "$(hoststub_count gh)"
assert_file_eq "human refusal left the receipt untouched" "$EVAL_SANDBOX/receipt-before.json" "$RECEIPT"

before="$(hoststub_count gh)"
run_review threads resolve acme/app "$PR" --thread PRRT_forged --json
assert_exit "forged thread named with --thread" 1
assert_refusal "forged thread refusal" not-verdict-authored
assert_eq "forged refusal made one self-login read and no write" "$((before + 1))" "$(hoststub_count gh)"
assert_file_eq "forged refusal left the receipt untouched" "$EVAL_SANDBOX/receipt-before.json" "$RECEIPT"

cat >"$EVAL_STUBBIN/gh" <<STUB
#!/usr/bin/env bash
set -uo pipefail
argv="\$*"
printf 'stub-gh %s\n' "\$argv" >>"\${HOSTSTUB_LOG:?}"
case "\$argv" in
  *resolveReviewThread*) cat "$FIX/github-resolve.json"; exit 0;;
  *"api user"*) printf 'stub-gh auth failed\n' >&2; exit 64;;
  *"api graphql"*) cat "$FIX/github-calibrate-threads.json"; exit 0;;
  *"pr view"*) cat "$FIX/github-pr.json"; exit 0;;
  *compare*) cat "$FIX/github-compare.json"; exit 0;;
esac
printf 'stub-gh UNRECOGNISED %s\n' "\$argv" >>"\${HOSTSTUB_LOG:?}"
exit 64
STUB
chmod +x "$EVAL_STUBBIN/gh"

before="$(hoststub_count gh)"
run_review threads resolve acme/app "$PR" --thread PRRT_kwDOacme --json
assert_exit "unreadable login refuses" 1
assert_refusal "unreadable login refusal" threads-failed
assert_eq "unreadable login made one failed read and no write" "$((before + 1))" "$(hoststub_count gh)"
assert_file_eq "unreadable login left the receipt untouched" "$EVAL_SANDBOX/receipt-before.json" "$RECEIPT"

cat >"$EVAL_STUBBIN/gh" <<STUB
#!/usr/bin/env bash
set -uo pipefail
argv="\$*"
printf 'stub-gh %s\n' "\$argv" >>"\${HOSTSTUB_LOG:?}"
case "\$argv" in
  *resolveReviewThread*) cat "$FIX/github-resolve.json"; exit 0;;
  *"api user"*) cat "$FIX/github-user.txt"; exit 0;;
  *"api graphql"*) cat "$FIX/github-calibrate-threads.json"; exit 0;;
  *"pr view"*) cat "$FIX/github-pr.json"; exit 0;;
  *compare*) cat "$FIX/github-compare.json"; exit 0;;
esac
printf 'stub-gh UNRECOGNISED %s\n' "\$argv" >>"\${HOSTSTUB_LOG:?}"
exit 64
STUB
chmod +x "$EVAL_STUBBIN/gh"

jq -n --arg h "$HEAD_A" \
  '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null}' \
  >"$DIR/post.json"
run_review calibrate acme/app "$PR" --json
assert_exit "calibrate after the resolve" 0
assert_json_field "calibrate reads the resolved thread as fixed" 1 '.data.precision.fixed'
assert_file_grep "calibrate names the resolution join" "$EVAL_ERR" \
  "precision: fixed=1 resolved-unfixed=0 open=0 unknown=0 (judged 1)"

hoststub_assert_all_routed
eval_end
