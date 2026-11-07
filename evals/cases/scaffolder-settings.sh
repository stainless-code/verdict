#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

for _f in config.json kb.jsonl kb-joins.jsonl learnings.md; do
  if [ -f "$EVAL_SANDBOX/.verdict/$_f" ]; then
    cp "$EVAL_SANDBOX/.verdict/$_f" "$EVAL_SANDBOX/.verdict/$_f.hold-06"
  fi
done

mkdir -p "$EVAL_SANDBOX/initrepo"
(
  cd "$EVAL_SANDBOX/initrepo" || exit 99
  git init -q
  git config user.email "init-eval@example.com"
  git config user.name "init eval"
  echo "probe" > probe.txt
  git add -A
  git commit -qm init
)
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset standard --yes >"$EVAL_SANDBOX/init-out.txt" 2>"$EVAL_SANDBOX/init-err.txt") || st=$?
if [ "$st" -eq 0 ]; then
  good "init --preset standard --yes exits 0 in a fresh repo"
else
  bad "init exits $st"
fi
for _f in config.json kb.jsonl .gitignore; do
  if [ -f "$EVAL_SANDBOX/initrepo/.verdict/$_f" ]; then
    good "init writes .verdict/$_f"
  else
    bad "init missing .verdict/$_f"
  fi
done
INIT_ALL="$(cat "$EVAL_SANDBOX/init-out.txt" "$EVAL_SANDBOX/init-err.txt")"
if grep -q "check-tree: dirty worktree" <<<"$INIT_ALL" \
  && grep -q "?? .verdict/config.json" <<<"$INIT_ALL" \
  && grep -q "warn: init completed; the worktree has uncommitted changes" <<<"$INIT_ALL"; then
  good "init reports the accepted dirty check-tree for its three shared files"
else
  bad "init check-tree line: $INIT_ALL"
fi
CFG="$EVAL_SANDBOX/initrepo/.verdict/config.json"
if [ "$(jq -r '.defaults.budget_usd == 0.5' "$CFG")" = "true" ] \
  && [ "$(jq -r '.defaults.panel | join(",")' "$CFG")" = "main" ] \
  && [ "$(jq -r '.defaults.kb_policy.min_judged' "$CFG")" = "5" ] \
  && [ "$(jq -r '.defaults.kb_policy.max_fp_rate' "$CFG")" = "0.5" ] \
  && [ "$(wc -l <"$EVAL_SANDBOX/initrepo/.verdict/kb.jsonl" | tr -d ' ')" = "2" ]; then
  good "standard preset contents match the documented mapping"
else
  bad "standard preset contents drifted"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" learn --status --json >"$EVAL_SANDBOX/init-learn.txt" 2>/dev/null) || st=$?
if [ "$st" -eq 0 ] && [ "$(jq -r '.data.rules | length' <"$EVAL_SANDBOX/init-learn.txt" 2>/dev/null)" = "2" ]; then
  good "starter kb.jsonl passes learn --status (2 active rules)"
else
  bad "learn --status on starter exits $st"
fi

st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset chill --yes >/dev/null 2>&1) || st=$?
if [ "$st" -eq 0 ] \
  && [ "$(jq -r '.defaults.panel // [] | length' "$CFG")" = "0" ] \
  && [ "$(jq -r '.defaults.kb_policy.min_judged' "$CFG")" = "10" ] \
  && [ "$(jq -r '.defaults.kb_policy.max_fp_rate' "$CFG")" = "0.7" ] \
  && [ "$(jq -r '.defaults.budget_usd == 2' "$CFG")" = "true" ]; then
  good "chill maps to unpinned panel + 10/0.7 + lenient budget"
else
  bad "chill mapping drifted (exit $st)"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset strict --yes >/dev/null 2>&1) || st=$?
if [ "$st" -eq 0 ] \
  && [ "$(jq -r '.defaults.panel | length' "$CFG")" = "3" ] \
  && [ "$(jq -r '.defaults.kb_policy.min_judged' "$CFG")" = "3" ] \
  && [ "$(jq -r '.defaults.kb_policy.max_fp_rate' "$CFG")" = "0.3" ] \
  && [ "$(jq -r '.defaults.budget_usd == 0.1' "$CFG")" = "true" ]; then
  good "strict maps to lead+2 lenses + 3/0.3 + tight budget"
else
  bad "strict mapping drifted (exit $st)"
fi

st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset chill </dev/null >"$EVAL_SANDBOX/init-needs.txt" 2>"$EVAL_SANDBOX/init-needs-err.txt") || st=$?
if [ "$st" -ne 0 ] \
  && grep -q "without confirm" "$EVAL_SANDBOX/init-needs-err.txt" \
  && [ "$(jq -r '.defaults.kb_policy.min_judged' "$CFG")" = "3" ]; then
  good "overwrite without --yes refuses (needs-confirm) and writes nothing"
else
  bad "overwrite guard gave exit $st"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset chill --yes >/dev/null 2>&1) || st=$?
if [ "$st" -eq 0 ] && [ "$(jq -r '.defaults.kb_policy.min_judged' "$CFG")" = "10" ]; then
  good "--yes overwrites after preview"
else
  bad "--yes overwrite exits $st"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset chill >"$EVAL_SANDBOX/init-keep.txt" 2>&1) || st=$?
if [ "$st" -eq 0 ] && grep -q "already up to date" "$EVAL_SANDBOX/init-keep.txt"; then
  good "identical re-run reads keep and exits 0 without --yes"
else
  bad "idempotent re-run exits $st"
fi

PR=4401
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Parity probe finding.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"5555555555555555555555555555555555555555","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
unset XDG_CONFIG_HOME || true
run_review report acme/app "$PR"
absent_status="$EVAL_STATUS"
cp "$EVAL_OUT" "$EVAL_SANDBOX/parity-absent-out.bin"
cp "$EVAL_ERR" "$EVAL_SANDBOX/parity-absent-err.txt"
mkdir -p "$EVAL_SANDBOX/xdg-defaults/verdict"
printf '{"$schema":"https://example.invalid/settings.schema.json","output":{"format":"text"},"layout":{"compact":false},"color":{"mode":"never"}}' > "$EVAL_SANDBOX/xdg-defaults/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-defaults"
run_review report acme/app "$PR"
if [ "$EVAL_STATUS" = "$absent_status" ] \
  && cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/parity-absent-out.bin" \
  && cmp -s "$EVAL_ERR" "$EVAL_SANDBOX/parity-absent-err.txt"; then
  good "defaults file is byte-identical to absent (stdout, stderr, exit)"
else
  bad "defaults file changed behavior (exit $EVAL_STATUS vs $absent_status)"
fi
unset XDG_CONFIG_HOME || true

mkdir -p "$EVAL_SANDBOX/xdg-json/verdict" "$EVAL_SANDBOX/xdg-text/verdict" "$EVAL_SANDBOX/xdg-empty/verdict"
printf '{"output":{"format":"json"}}' > "$EVAL_SANDBOX/xdg-json/verdict/settings.json"
printf '{"output":{"format":"text"}}' > "$EVAL_SANDBOX/xdg-text/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-json"
run_review report acme/app "$PR"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ] \
  && [ "$(jq -r '.verb' <"$EVAL_OUT" 2>/dev/null)" = "report" ]; then
  good "file format=json defaults the envelope without --json"
else
  bad "file json default exits $EVAL_STATUS with $(stdout_docs) docs"
fi
run_review report acme/app "$PR" --no-json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "0" ]; then
  good "--no-json forces human over file json"
else
  bad "--no-json over file json exits $EVAL_STATUS with $(stdout_docs) docs"
fi
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-text"
# shellcheck disable=SC2034
REVIEW_OUTPUT_FORMAT="json"
export REVIEW_OUTPUT_FORMAT
run_review report acme/app "$PR"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "0" ]; then
  good "file text wins over env json"
else
  bad "file-over-env gave $(stdout_docs) docs (exit $EVAL_STATUS)"
fi
unset REVIEW_OUTPUT_FORMAT || true
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
# shellcheck disable=SC2034
REVIEW_OUTPUT_FORMAT="json"
export REVIEW_OUTPUT_FORMAT
run_review report acme/app "$PR"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ]; then
  good "env json alone defaults the envelope (no file)"
else
  bad "env json alone exits $EVAL_STATUS with $(stdout_docs) docs"
fi
unset REVIEW_OUTPUT_FORMAT || true
unset XDG_CONFIG_HOME || true

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "scaff-eval@example.com"
  git config user.name "scaff eval"
  grep -qxF '.verdict/state/' .gitignore 2>/dev/null || echo ".verdict/state/" >> .gitignore
  mkdir -p compactdir
  echo "compact v1" > compactdir/probe.txt
  git add compactdir/probe.txt .gitignore 2>/dev/null || true
  git commit -qm compact-init 2>/dev/null || true
  echo "compact v2" >> compactdir/probe.txt
)
mv "$EVAL_SANDBOX/.verdict/config.json" "$EVAL_SANDBOX/.verdict/config.json.hold-06" 2>/dev/null || true
unset XDG_CONFIG_HOME || true
run_review fetch-local acme/app compact-a
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "panel: none pinned" "$EVAL_OUT"; then
  good "default prints the panel advisory"
else
  bad "default panel advisory missing (exit $EVAL_STATUS)"
fi
mkdir -p "$EVAL_SANDBOX/xdg-compact/verdict"
printf '{"layout":{"compact":true}}' > "$EVAL_SANDBOX/xdg-compact/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-compact"
run_review fetch-local acme/app compact-b
assert_exit "compact file render succeeds" 0
assert_stream_nogrep "compact file suppresses the panel advisory" "$EVAL_OUT" "panel: none pinned"
run_review fetch-local acme/app compact-c --no-compact
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "panel: none pinned" "$EVAL_OUT"; then
  good "--no-compact wins over the compact file"
else
  bad "--no-compact over file exits $EVAL_STATUS"
fi
mkdir -p "$EVAL_SANDBOX/xdg-color/verdict"
printf '{"color":{"mode":"always"}}' > "$EVAL_SANDBOX/xdg-color/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-color"
run_review fetch-local acme/app compact-d
if [ "$EVAL_STATUS" -eq 0 ] && grep -q $'\033' "$EVAL_ERR"; then
  good "color always adds codes on the warn path (envelope stays clean)"
else
  bad "color always left stderr plain (exit $EVAL_STATUS)"
fi
assert_eq "human stdout parsed as zero JSON docs" "0" "$(stdout_docs)"
assert_stream_nogrep "human stdout carries no codes even when always" "$EVAL_OUT" $'\033' "-F"
printf '{"color":{"mode":"never"}}' > "$EVAL_SANDBOX/xdg-color/verdict/settings.json"
run_review fetch-local acme/app compact-e
assert_exit "color never render succeeds" 0
assert_stream_nogrep "color never stays plain" "$EVAL_ERR" $'\033' "-F"
printf '{"color":{"mode":"always"}}' > "$EVAL_SANDBOX/xdg-color/verdict/settings.json"
run_review fetch-local acme/app compact-f --color never
assert_exit "--color never render succeeds" 0
assert_stream_nogrep "--color never wins over the always file" "$EVAL_ERR" $'\033' "-F"
export NO_COLOR=1
run_review fetch-local acme/app compact-g
if [ "$EVAL_STATUS" -eq 0 ] && grep -q $'\033' "$EVAL_ERR"; then
  good "explicit file always beats NO_COLOR (flags win over the convention)"
else
  bad "NO_COLOR overrode the explicit file (exit $EVAL_STATUS)"
fi
unset NO_COLOR || true
unset XDG_CONFIG_HOME || true
mv "$EVAL_SANDBOX/.verdict/config.json.hold-06" "$EVAL_SANDBOX/.verdict/config.json" 2>/dev/null || true

mkdir -p "$EVAL_SANDBOX/xdg-cred/verdict"
printf '{"output":{"format":"text"},"gh_token":"x"}' > "$EVAL_SANDBOX/xdg-cred/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-cred"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings-credentials" ]; then
  good "credential key refuses typed bad-settings-credentials in the envelope"
else
  bad "credential refuse gave exit $EVAL_STATUS"
fi
printf '{"output":{"nested":{"auth_key":"x"}}}' > "$EVAL_SANDBOX/xdg-cred/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings-credentials" ]; then
  good "nested credential key refuses too"
else
  bad "nested credential refuse gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

mkdir -p "$EVAL_SANDBOX/xdg-org/verdict"
printf '{"org":{"mode":"strict"}}' > "$EVAL_SANDBOX/xdg-org/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-org"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "org.* namespace refuses typed bad-settings"
else
  bad "org namespace gave exit $EVAL_STATUS"
fi
printf '{"output":{"format":"yaml"}}' > "$EVAL_SANDBOX/xdg-org/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "bad output.format refuses typed bad-settings"
else
  bad "bad format gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

for _f in config.json kb.jsonl kb-joins.jsonl learnings.md; do
  if [ -f "$EVAL_SANDBOX/.verdict/$_f.hold-06" ]; then
    mv "$EVAL_SANDBOX/.verdict/$_f.hold-06" "$EVAL_SANDBOX/.verdict/$_f"
  else
    rm -f "$EVAL_SANDBOX/.verdict/$_f"
  fi
done
rm -rf "$EVAL_SANDBOX/initrepo" "$EVAL_SANDBOX/xdg-defaults" "$EVAL_SANDBOX/xdg-json" \
  "$EVAL_SANDBOX/xdg-text" "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/xdg-compact" \
  "$EVAL_SANDBOX/xdg-color" "$EVAL_SANDBOX/xdg-cred" "$EVAL_SANDBOX/xdg-org"

if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across init + prefs legs (offline)"
else
  bad "stub gh intercepted calls during offline legs"
fi

eval_end
