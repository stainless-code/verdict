#!/usr/bin/env bash
# Case scaffolder-settings: one-command onboarding (init) + minimal prefs file.
#
# Grades (all hermetic — crafted bundles, stub gh, sandbox git worktree):
#   I1  scaffold-then-validate round-trip: init writes the four control
#       files with the documented preset contents, starter kb.jsonl passes
#       learn --status, check-tree validation runs (clean in a fresh repo,
#       advisory-dirty in the shared sandbox), zero new gh intercepts.
#   I2  presets map to panel strictness + kb-policy thresholds
#       (chill unpinned/10/0.7, standard 1 member/5/0.5, strict lead+2/3/0.3).
#   I3  overwrite guard: existing different files without --yes on a non-tty
#       refuse as needs-confirm (nothing overwritten); --yes overwrites;
#       identical re-runs read keep and exit 0 without --yes.
#   P1  absent parity: report stdout/status/stderr byte-identical with no
#       settings file vs a file holding only defaults.
#   P2  flag-wins matrix: file format=json defaults to the envelope;
#       --no-json forces human; file text wins over env json; env json alone
#       defaults to the envelope.
#   P3  layout/color: compact suppresses the panel advisory; --no-compact
#       wins over the file; color always adds codes on the warn path while
#       never stays plain; --color wins over the file; file wins over NO_COLOR.
#   P4  credentials refuse: token/key/auth-shaped keys fail typed
#       bad-settings-credentials (never written, never read).
#   P5  unknown keys refuse: server-shaped or org.* namespaces fail typed
#       bad-settings.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

# --- sandbox root controls: back up whatever earlier cases left ---
for _f in budget panel kb-policy kb.jsonl kb-joins.jsonl learnings.md max_bytes; do
  if [ -f "$EVAL_SANDBOX/.review/$_f" ]; then
    cp "$EVAL_SANDBOX/.review/$_f" "$EVAL_SANDBOX/.review/$_f.hold-06"
  fi
done

# --- I1: scaffold-then-validate round-trip in a fresh repo (clean tree) ---
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
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" init --preset standard --yes >"$EVAL_SANDBOX/init-out.txt" 2>"$EVAL_SANDBOX/init-err.txt") || st=$?
if [ "$st" -eq 0 ]; then
  good "init --preset standard --yes exits 0 in a fresh repo"
else
  bad "init exits $st"
fi
for _f in budget panel kb-policy kb.jsonl; do
  if [ -f "$EVAL_SANDBOX/initrepo/.review/$_f" ]; then
    good "init writes .review/$_f"
  else
    bad "init missing .review/$_f"
  fi
done
if grep -q "check-tree: clean" "$EVAL_SANDBOX/init-out.txt"; then
  good "init runs check-tree validation (clean, .review/ ignored)"
else
  bad "init check-tree line: $(cat "$EVAL_SANDBOX/init-out.txt")"
fi
if [ "$(grep -v '^[[:space:]]*#' "$EVAL_SANDBOX/initrepo/.review/budget" | grep -v '^[[:space:]]*$' | head -n 1)" = "0.50" ] \
  && grep -q "^main$" "$EVAL_SANDBOX/initrepo/.review/panel" \
  && grep -q "^MIN_JUDGED=5$" "$EVAL_SANDBOX/initrepo/.review/kb-policy" \
  && grep -q "^MAX_FP_RATE=0.5$" "$EVAL_SANDBOX/initrepo/.review/kb-policy" \
  && [ "$(wc -l <"$EVAL_SANDBOX/initrepo/.review/kb.jsonl" | tr -d ' ')" = "2" ]; then
  good "standard preset contents match the documented mapping"
else
  bad "standard preset contents drifted"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" learn --status --json >"$EVAL_SANDBOX/init-learn.txt" 2>/dev/null) || st=$?
if [ "$st" -eq 0 ] && [ "$(jq -r '.data.rules | length' <"$EVAL_SANDBOX/init-learn.txt" 2>/dev/null)" = "2" ]; then
  good "starter kb.jsonl passes learn --status (2 active rules)"
else
  bad "learn --status on starter exits $st"
fi

# --- I2: preset mapping (panel strictness + kb-policy thresholds) ---
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" init --preset chill --yes >/dev/null 2>&1) || st=$?
if [ "$st" -eq 0 ] \
  && [ "$(grep -v '^[[:space:]]*#' "$EVAL_SANDBOX/initrepo/.review/panel" | grep -v '^[[:space:]]*$' | wc -l | tr -d ' ')" = "0" ] \
  && grep -q "^MIN_JUDGED=10$" "$EVAL_SANDBOX/initrepo/.review/kb-policy" \
  && grep -q "^MAX_FP_RATE=0.7$" "$EVAL_SANDBOX/initrepo/.review/kb-policy" \
  && [ "$(grep -v '^[[:space:]]*#' "$EVAL_SANDBOX/initrepo/.review/budget" | grep -v '^[[:space:]]*$' | head -n 1)" = "2.00" ]; then
  good "chill maps to unpinned panel + 10/0.7 + lenient budget"
else
  bad "chill mapping drifted (exit $st)"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" init --preset strict --yes >/dev/null 2>&1) || st=$?
if [ "$st" -eq 0 ] \
  && [ "$(grep -v '^[[:space:]]*#' "$EVAL_SANDBOX/initrepo/.review/panel" | grep -v '^[[:space:]]*$' | wc -l | tr -d ' ')" = "3" ] \
  && grep -q "^MIN_JUDGED=3$" "$EVAL_SANDBOX/initrepo/.review/kb-policy" \
  && grep -q "^MAX_FP_RATE=0.3$" "$EVAL_SANDBOX/initrepo/.review/kb-policy" \
  && [ "$(grep -v '^[[:space:]]*#' "$EVAL_SANDBOX/initrepo/.review/budget" | grep -v '^[[:space:]]*$' | head -n 1)" = "0.10" ]; then
  good "strict maps to lead+2 lenses + 3/0.3 + tight budget"
else
  bad "strict mapping drifted (exit $st)"
fi

# --- I3: overwrite guard (non-tty stdin: no prompt possible) ---
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" init --preset chill </dev/null >"$EVAL_SANDBOX/init-needs.txt" 2>"$EVAL_SANDBOX/init-needs-err.txt") || st=$?
if [ "$st" -ne 0 ] \
  && grep -q "without confirm" "$EVAL_SANDBOX/init-needs-err.txt" \
  && grep -q "^MIN_JUDGED=3$" "$EVAL_SANDBOX/initrepo/.review/kb-policy"; then
  good "overwrite without --yes refuses (needs-confirm) and writes nothing"
else
  bad "overwrite guard gave exit $st"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" init --preset chill --yes >/dev/null 2>&1) || st=$?
if [ "$st" -eq 0 ] && grep -q "^MIN_JUDGED=10$" "$EVAL_SANDBOX/initrepo/.review/kb-policy"; then
  good "--yes overwrites after preview"
else
  bad "--yes overwrite exits $st"
fi
st=0
(cd "$EVAL_SANDBOX/initrepo" && PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/review.sh" init --preset chill >"$EVAL_SANDBOX/init-keep.txt" 2>&1) || st=$?
if [ "$st" -eq 0 ] && grep -q "already up to date" "$EVAL_SANDBOX/init-keep.txt"; then
  good "identical re-run reads keep and exits 0 without --yes"
else
  bad "idempotent re-run exits $st"
fi

# --- P1: absent parity (report on a crafted bundle) ---
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

# --- P2: flag-wins matrix on output.format ---
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

# --- P3: layout compact + color mode (fetch-local human legs) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "scaff-eval@example.com"
  git config user.name "scaff eval"
  grep -qxF '.review/' .gitignore 2>/dev/null || echo ".review/" >> .gitignore
  mkdir -p compactdir
  echo "compact v1" > compactdir/probe.txt
  git add compactdir/probe.txt .gitignore 2>/dev/null || true
  git commit -qm compact-init 2>/dev/null || true
  echo "compact v2" >> compactdir/probe.txt
)
mv "$EVAL_SANDBOX/.review/panel" "$EVAL_SANDBOX/.review/panel.hold-06" 2>/dev/null || true
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
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "panel: none pinned" "$EVAL_OUT"; then
  good "compact file suppresses the panel advisory"
else
  bad "compact file kept the advisory (exit $EVAL_STATUS)"
fi
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
if [ "$(stdout_docs)" = "0" ] && ! grep -q $'\033' "$EVAL_OUT"; then
  good "human stdout carries no codes even when always"
else
  bad "stdout leaked codes or parsed as JSON"
fi
printf '{"color":{"mode":"never"}}' > "$EVAL_SANDBOX/xdg-color/verdict/settings.json"
run_review fetch-local acme/app compact-e
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q $'\033' "$EVAL_ERR"; then
  good "color never stays plain"
else
  bad "color never emitted codes (exit $EVAL_STATUS)"
fi
printf '{"color":{"mode":"always"}}' > "$EVAL_SANDBOX/xdg-color/verdict/settings.json"
run_review fetch-local acme/app compact-f --color never
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q $'\033' "$EVAL_ERR"; then
  good "--color never wins over the always file"
else
  bad "--color flag lost to the file (exit $EVAL_STATUS)"
fi
export NO_COLOR=1
run_review fetch-local acme/app compact-g
if [ "$EVAL_STATUS" -eq 0 ] && grep -q $'\033' "$EVAL_ERR"; then
  good "explicit file always beats NO_COLOR (flags win over the convention)"
else
  bad "NO_COLOR overrode the explicit file (exit $EVAL_STATUS)"
fi
unset NO_COLOR || true
unset XDG_CONFIG_HOME || true
mv "$EVAL_SANDBOX/.review/panel.hold-06" "$EVAL_SANDBOX/.review/panel" 2>/dev/null || true

# --- P4: credentials refuse (typed, nothing written) ---
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

# --- P5: unknown namespaces refuse ---
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

# --- restore sandbox root controls ---
for _f in budget panel kb-policy kb.jsonl kb-joins.jsonl learnings.md max_bytes; do
  if [ -f "$EVAL_SANDBOX/.review/$_f.hold-06" ]; then
    mv "$EVAL_SANDBOX/.review/$_f.hold-06" "$EVAL_SANDBOX/.review/$_f"
  else
    rm -f "$EVAL_SANDBOX/.review/$_f"
  fi
done
rm -rf "$EVAL_SANDBOX/initrepo" "$EVAL_SANDBOX/xdg-defaults" "$EVAL_SANDBOX/xdg-json" \
  "$EVAL_SANDBOX/xdg-text" "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/xdg-compact" \
  "$EVAL_SANDBOX/xdg-color" "$EVAL_SANDBOX/xdg-cred" "$EVAL_SANDBOX/xdg-org"

# --- P6: network never touched across every leg above ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across init + prefs legs (offline)"
else
  bad "stub gh intercepted calls during offline legs"
fi

if [ "$fails" -ne 0 ]; then echo "case scaffolder-settings: FAIL ($fails)"; exit 1; fi
echo "case scaffolder-settings: PASS"
