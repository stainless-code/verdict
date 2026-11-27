#!/usr/bin/env bash
#
# STA-476: `verdict demo` is the zero-config first impression, so it is graded
# end to end: a public GitHub pull request read over the anonymous API when gh
# is not signed in, the caller's own working tree otherwise, one auto-detected
# agent, one record, and the rendered review — all inside a disposable temp
# directory that never touches the checkout.
#
# Every leg is offline. `gh` answers `auth status` with "not signed in" and
# refuses everything else; the five anonymous reads are exact registered curl
# argv; the agent is evals/fixtures/run-agent/agent-stub.sh copied onto PATH as
# `claude`, which writes the one review.json the artifact contract asks for.
# The case also grades the three-way read-only promise: no write-shaped argv
# reached a host stub, no artifact landed in the repository, and no `.verdict`
# directory appeared there.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/demo"
STUB_AGENT_SRC="$EVAL_ROOT/evals/fixtures/run-agent/agent-stub.sh"
REPO="$EVAL_SANDBOX/repo"
TMPROOT="$EVAL_SANDBOX/tmp"
AGENTBIN="$EVAL_SANDBOX/agentbin"
URL="https://github.com/acme/app/pull/7"
PR=7
API="https://api.github.com/repos/acme/app/pulls/$PR"

mkdir -p "$TMPROOT" "$AGENTBIN"
# The temp root the run is told to use, resolved the way the CLI resolves it:
# macOS reaches TMPDIR through /var, a link.
TMPROOT_PHYSICAL="$(cd "$TMPROOT" && pwd -P)"
export TMPDIR="$TMPROOT"
# The demo must not answer an eval prompt; NO_COLOR keeps the bytes stable.
export NO_COLOR=1

if [ ! -f "$STUB_AGENT_SRC" ]; then
  bail "the committed stub agent is missing: $STUB_AGENT_SRC"
fi
cp "$STUB_AGENT_SRC" "$AGENTBIN/claude"
chmod +x "$AGENTBIN/claude"
# The review runs in the OS review sandbox, which lets claude write only its
# snapshot, its private TMPDIR, and its own data paths: its config directory
# lives in the case's sandbox, and the stub's argv log in its debug directory.
export CLAUDE_CONFIG_DIR="$EVAL_SANDBOX/claude-config"
mkdir -p "$CLAUDE_CONFIG_DIR/debug"
export STUB_AGENT_LOG="$CLAUDE_CONFIG_DIR/debug/agent-argv.log"

# MINBIN carries only the tools the CLI itself shells, for the agent-missing
# leg: the point is that no agent binary resolves, not that verdict loses git.
MINBIN="$EVAL_SANDBOX/minbin"
mkdir -p "$MINBIN"
for tool in git jq sed grep cat dirname mkdir date env uname sh bash; do
  found="$(command -v "$tool" 2>/dev/null || true)"
  [ -n "$found" ] || continue
  ln -sf "$found" "$MINBIN/$tool"
done

# routes registers the five anonymous reads. `gh auth status --no-refresh` is
# the only gh call the demo ever makes, and it answers "not signed in" so the
# case grades the public path. Every curl route leads with --disable: a
# ~/.curlrc must never be able to add a header to an anonymous read. The reads
# use -sSfL (not -sfL) so curl keeps its own error line on stderr, which is the
# only text a real 403, 404, or 429 leaves behind.
routes() {
  hoststub_init gh curl
  hoststub_route gh "auth status --hostname github.com --active --no-refresh" "$FIX/empty.txt" 1
  hoststub_route curl "--disable -sSfL $API" "$FIX/PR.json"
  hoststub_route curl "--disable -sSfL -H Accept:application/vnd.github.v3.diff $API" "$FIX/diff.patch"
  hoststub_route curl "--disable -sSfL $API/files?per_page=100&page=1" "$FIX/files.json"
  hoststub_route curl "--disable -sSfL $API/reviews?per_page=100&page=1" "$FIX/reviews.json"
  hoststub_route curl "--disable -sSfL $API/comments?per_page=100&page=1" "$FIX/comments.json"
}

# demo_run <path> [env assignments...] -- <args...> runs one CLI call from the
# fixture repository with the case's PATH plus the given extras.
demo_run() {
  local path="$1" dir="$2"
  shift 2
  set +e
  (
    cd "$dir" || exit 99
    PATH="$path" "$EVAL_CLI" demo "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # EVAL_STATUS is read by assert_exit in the caller
  EVAL_STATUS=$?
  set -e
  return 0
}

# temp_dirs lists the disposable directories the case's temp root holds.
temp_dirs() {
  find "$TMPROOT" -maxdepth 1 -type d -name 'verdict-demo-*' 2>/dev/null | wc -l | tr -d ' '
}

eval_fixture_repo "$REPO"
# The fixture checkout is dirty on purpose (its uncommitted change is the local
# review target), so the URL leg proves the run changed nothing by comparing.
dirt_before="$(git -C "$REPO" status --porcelain)"
routes
hoststub_prove_dead curl
routes

# --- 1. a public pull request, anonymously -----------------------------------
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" "$URL"
assert_exit "public PR demo" 0
assert_file_grep "the review body carries the agent's finding" "$EVAL_OUT" 'notes.txt:2'
assert_file_grep "the review body carries the agent's own summary" "$EVAL_OUT" 'Nit: one note, nothing blocking'
assert_file_grep "stderr names the anonymous transport" "$EVAL_ERR" 'gh is not signed in for github.com'
assert_file_grep "stderr names the thread-resolution gap" "$EVAL_ERR" "thread resolution needs 'gh auth login'"
assert_file_grep "the header names the read path" "$EVAL_OUT" 'read via the public GitHub API (no gh login)'
assert_file_grep "the facts line carries the change size" "$EVAL_OUT" '1 file · +1 −1'
assert_file_grep "the human footer names the next step" "$EVAL_OUT" 'next:  verdict init --ci github'
assert_file_grep "the human footer names the bundle disposition" "$EVAL_OUT" 'removed; pass --keep to inspect'
assert_stream_nogrep "the review never claims to have posted" "$EVAL_OUT" 'posted review'
assert_eq "the temp root is empty after a successful run" 0 "$(temp_dirs)"
assert_eq "the URL run changed nothing in the checkout" "$dirt_before" "$(git -C "$REPO" status --porcelain)"
if [ -e "$REPO/.verdict" ]; then
  bad "the URL run created .verdict in the checkout"
else
  good "the URL run wrote nothing in the checkout"
fi

# --- 2. the same run as one envelope -----------------------------------------
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" "$URL" --json
assert_exit "public PR demo --json" 0
assert_envelope "demo envelope"
assert_json_field "the envelope names the verb" demo '.verb'
assert_json_field "the envelope names the repository" acme/app '.repo'
assert_json_field "the envelope names the pull request" 7 '.pr'
assert_json_field "the data names the mode" remote '.data.mode'
assert_json_field "the data names the anonymous transport" public-api '.data.fetched_via'
assert_json_field "the data names the URL it read" "$URL" '.data.url'
assert_json_field "the data names the detected agent" claude '.data.agent.name'
assert_json_field "the data pins one finder" 1 '.data.finders'
assert_json_field "the data carries the review body" true '.data.review_body | length > 0'
assert_json_field "the data counts the kept finding" 1 '.data.findings.kept'
assert_json_field "a URL run keeps no bundle" false '.data.kept'
assert_json_field "the data names the next step" "verdict init --ci github" '.next'
BUNDLE="$(jq -r '.data.bundle_dir' <"$EVAL_OUT")"
case "$BUNDLE" in
  "$TMPROOT_PHYSICAL"/verdict-demo-*) good "the bundle landed under TMPDIR" ;;
  *) bad "bundle dir '$BUNDLE' is not under $TMPROOT_PHYSICAL" ;;
esac
if [ -e "$BUNDLE" ]; then
  bad "the printed bundle dir still exists after a clean run"
else
  good "the printed bundle dir is gone"
fi

# --- 3. the caller's own dirty working tree ----------------------------------
printf 'top\nnew line\nbottom\n' >"$REPO/notes.txt"
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" --json
assert_exit "local demo on a dirty tree" 0
assert_json_field "the data names the local mode" local '.data.mode'
assert_json_field "the data names the git transport" git '.data.fetched_via'
assert_json_field "the local review kept the finding" 1 '.data.findings.kept'
assert_json_field "the local run reports a duration" true '.data.duration_ms >= 0'
assert_json_field "the local run names no URL" "" '.data.url // ""'
assert_eq "the checkout still carries only the reviewed change" " M notes.txt" "$(git -C "$REPO" status --porcelain)"
if [ -e "$REPO/.verdict" ]; then
  bad "the local run created .verdict in the checkout"
else
  good "the local run wrote nothing in the checkout"
fi

# --- 4. a clean tree falls back to the last commit ---------------------------
git -C "$REPO" -c user.email=eval@example.com -c user.name=eval -c commit.gpgsign=false \
  commit -qam eval-dirty
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" --json
assert_exit "local demo on a clean tree" 0
assert_json_field "the clean tree reviews the last commit" git '.data.fetched_via'
assert_file_grep "the last commit's change reached the review" "$EVAL_OUT" 'notes.txt:2'
assert_eq "the clean-tree run wrote nothing in the checkout" "" "$(git -C "$REPO" status --porcelain)"

# --- 5. no agent installed ---------------------------------------------------
# Human mode on purpose: the two-line guide is the human error line, and JSON
# mode would put it in the envelope instead of on stderr.
calls_before="$(hoststub_count curl)"
demo_run "$MINBIN" "$REPO"
assert_exit "demo without an agent" 1
assert_file_grep "the guide says what is missing" "$EVAL_ERR" \
  'error: no coding agent CLI found on PATH — install one and re-run'
assert_file_grep "the guide names an install command" "$EVAL_ERR" 'install.sh'
assert_file_grep "the guide names the --agent escape hatch" "$EVAL_ERR" 'pass --agent <name>'
assert_eq "the agent-missing leg leaves no temp dir" 0 "$(temp_dirs)"
assert_eq "the agent-missing leg calls no host" "$calls_before" "$(hoststub_count curl)"

# --- 6. --keep leaves the artifacts ------------------------------------------
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" --keep --json
assert_exit "local demo --keep" 0
assert_json_field "the kept run says so" true '.data.kept'
KEPT="$(jq -r '.data.bundle_dir' <"$EVAL_OUT")"
if [ -d "$KEPT" ]; then
  good "the kept bundle dir exists"
else
  bad "the kept bundle dir is missing: $KEPT"
fi
for artifact in record.json review.json launcher.json hunks.tsv; do
  if [ -f "$KEPT/.verdict/state/local-demo/$artifact" ]; then
    good "the kept bundle carries $artifact"
  else
    bad "the kept bundle is missing $artifact"
  fi
done
assert_file_grep "the kept run names the directory" "$EVAL_ERR" 'demo bundle kept at'
rm -rf "$KEPT"

# --- 7. the refusals ---------------------------------------------------------
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'not-a-url' --json
assert_exit "a non-URL positional" 1
assert_refusal "a non-URL positional" bad-target
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://gitlab.com/acme/app/pull/7' --json
assert_exit "a non-GitHub URL" 1
assert_refusal "a non-GitHub URL" bad-target
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://github.com/../app/pull/7' --json
assert_exit "a traversal owner" 1
assert_refusal "a traversal owner" bad-target
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://github.com/%61cme/app/pull/7' --json
assert_exit "a percent-escaped owner" 1
assert_refusal "a percent-escaped owner" bad-target
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://github.com/-owner/app/pull/7' --json
assert_exit "a flag-shaped owner" 1
assert_refusal "a flag-shaped owner" bad-target
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://github.com/acme!/app/pull/7' --json
assert_exit "an out-of-charset owner" 1
assert_refusal "an out-of-charset owner" bad-target
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" "$URL" "$URL" --json
assert_exit "two positionals" 2
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" "$URL" --agent= --json
assert_exit "an empty agent" 2
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" "$URL" --agent bogus --json
assert_exit "an unknown agent" 1
assert_refusal "an unknown agent" unknown-agent
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" "$URL" --bogus --json
assert_exit "an unknown flag" 2

# --- 8. the public read's one real failure -----------------------------------
# A different pull request, so the route registered here is the argv the stub
# sees: the anonymous hourly cap is the failure a public reader actually hits,
# and the refusal has to name the remedy instead of only the status. The fixture
# is the line a real `curl -sSfL` prints, so the classification is graded on
# production text and not on a hand-written message.
printf 'curl: (22) The requested URL returned error: 403\n' >"$EVAL_SANDBOX/rate-limit.txt"
hoststub_route curl "--disable -sSfL https://api.github.com/repos/acme/app/pulls/99" \
  "$EVAL_SANDBOX/rate-limit.txt" 1
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://github.com/acme/app/pull/99' --json
assert_exit "a rate-limited public read" 1
assert_refusal "a rate-limited public read" fetch-failed
assert_json_field "the refusal quotes curl's own line" true \
  '.error.message | contains("error: 403")'
assert_json_field "the refusal names the anonymous cap" true \
  '.error.message | contains("capped at 60 requests/hour")'
assert_json_field "the refusal names the remedy" true \
  '.error.message | contains("gh auth login")'

# A 404 is a missing or private pull request, not a quota problem: the refusal
# must not send the operator off to wait for an hourly cap that will not reset.
printf 'curl: (22) The requested URL returned error: 404\n' >"$EVAL_SANDBOX/not-found.txt"
hoststub_route curl "--disable -sSfL https://api.github.com/repos/acme/app/pulls/98" \
  "$EVAL_SANDBOX/not-found.txt" 1
demo_run "$EVAL_STUBBIN:$AGENTBIN:$PATH" "$REPO" 'https://github.com/acme/app/pull/98' --json
assert_exit "a missing or private public read" 1
assert_refusal "a missing or private public read" fetch-failed
assert_json_field "the 404 refusal names the missing-or-private cause" true \
  '.error.message | contains("missing or private")'
assert_json_field "the 404 refusal carries no quota advice" false \
  '.error.message | contains("capped at")'
# The failure happened after the temp dir existed, so the cleanup contract is
# proved here too, not only on the success legs.
assert_eq "a mid-run refusal leaves no temp dir" 0 "$(temp_dirs)"

# --- 9. read-only, proved from the stubs' own log ----------------------------
hoststub_assert_all_routed
hoststub_argv_log curl "$EVAL_SANDBOX/curl-argvs.txt"
hoststub_argv_log gh "$EVAL_SANDBOX/gh-argvs.txt"
# A write-shaped argv is one that names a method, a body, or a mutating gh
# subcommand. The pattern is one grep -E so no operand can lose a space.
WRITE_SHAPED='-X (POST|PATCH|PUT|DELETE)|--request|--data|(^| )-d |(^| )-F |--form|pr (comment|review|merge)|api (-X|--method)'
assert_file_nogrep "no curl call was a write" "$EVAL_SANDBOX/curl-argvs.txt" "$WRITE_SHAPED" -E
assert_file_nogrep "no gh call was a write" "$EVAL_SANDBOX/gh-argvs.txt" "$WRITE_SHAPED" -E
if [ "$(grep -c '^auth status --hostname github.com --active --no-refresh$' "$EVAL_SANDBOX/gh-argvs.txt" || true)" -gt 0 ]; then
  good "the only gh call shape is the active-account login probe"
else
  bad "the gh log carries no auth status --active --no-refresh probe: see $EVAL_SANDBOX/gh-argvs.txt"
fi
assert_eq "every gh call in the whole case is the active-account login probe" \
  "$(grep -c . "$EVAL_SANDBOX/gh-argvs.txt" || true)" \
  "$(grep -c '^auth status --hostname github.com --active --no-refresh$' "$EVAL_SANDBOX/gh-argvs.txt" || true)"
# --disable leads every anonymous read, so a ~/.curlrc cannot add a header.
assert_eq "every curl call disables curlrc first" \
  "$(grep -c . "$EVAL_SANDBOX/curl-argvs.txt" || true)" \
  "$(grep -c '^--disable ' "$EVAL_SANDBOX/curl-argvs.txt" || true)"
assert_eq "the temp root is empty at the end of the case" 0 "$(temp_dirs)"

eval_end
