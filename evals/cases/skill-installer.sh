#!/usr/bin/env bash
# Case skill-installer: copy-based skill installer (detect + preview + confirm).
#
# Grades (all hermetic — sandbox cwd as the repo root, fake HOME, stub gh):
#   K1  refuse-without-confirm: a preview without --yes on a non-tty refuses
#       typed needs-confirm and writes nothing (foreign skills byte-identical).
#   K2  confirm apply: --yes installs the tool-owned verdict/ payload
#       (SKILL.md + prompts/) byte-identical under each detected root and
#       writes an installer receipt; foreign skills and unowned verdict/
#       dirs stay byte-identical.
#   K3  idempotent re-run: a second run reads keep and exits 0 without --yes.
#   K4  version skew nudges: a stale receipt warns but stays exit 0; --yes
#       refreshes the owned receipt.
#   K5  offline: zero stub-gh intercepts across every leg.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

# --- sandbox roots: repo scope is the sandbox cwd; home scope is a fake HOME ---
OLD_HOME="$HOME"
export HOME="$EVAL_SANDBOX/fakehome"
mkdir -p "$HOME/.agents/skills" "$HOME/.claude/skills"
mkdir -p "$EVAL_SANDBOX/.agents/skills"
# foreign skills alongside our future slot (repo + home scope)
mkdir -p "$EVAL_SANDBOX/.agents/skills/foreign-skill" "$HOME/.claude/skills/other"
echo "foreign repo skill" > "$EVAL_SANDBOX/.agents/skills/foreign-skill/SKILL.md"
echo "foreign home skill" > "$HOME/.claude/skills/other/SKILL.md"
# unowned verdict/ dir: foreign content, no installer receipt (must never touch)
mkdir -p "$HOME/.agents/skills/verdict"
echo "someone else's verdict copy" > "$HOME/.agents/skills/verdict/SKILL.md"
# snapshot every foreign byte before the installer runs
FOREIGN_SNAP="$EVAL_SANDBOX/foreign-snap.txt"
(cd "$EVAL_SANDBOX" && find .agents fakehome -type f | LC_ALL=C sort | xargs shasum) > "$FOREIGN_SNAP"

# --- K1: preview without --yes refuses typed needs-confirm, writes nothing ---
run_review install </dev/null
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "preview without --yes refuses (exit $EVAL_STATUS)"
else
  bad "preview without --yes applied (exit 0)"
fi
if grep -q "without confirm" "$EVAL_ERR"; then
  good "refusal names the confirm handshake on stderr"
else
  bad "refusal stderr: $(head -c 200 "$EVAL_ERR")"
fi
run_review install --json </dev/null
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "needs-confirm" ]; then
  good "refusal is typed needs-confirm in the envelope"
else
  bad "refusal code: $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true) (exit $EVAL_STATUS)"
fi
if [ ! -e "$EVAL_SANDBOX/.agents/skills/verdict" ] \
  && [ ! -e "$HOME/.claude/skills/verdict" ] \
  && (cd "$EVAL_SANDBOX" && shasum -c "$FOREIGN_SNAP" >/dev/null 2>&1); then
  good "refused run wrote nothing (foreign bytes intact)"
else
  bad "refused run wrote files or touched foreign skills"
fi

# --- K2: --yes applies the owned payload, receipts it, spares the foreign ---
run_review install --yes </dev/null
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "--yes applies (exit 0)"
else
  bad "--yes exits $EVAL_STATUS"
fi
payload_ok="true"
for root in "$EVAL_SANDBOX/.agents/skills" "$HOME/.claude/skills"; do
  cmp -s "$EVAL_ROOT/SKILL.md" "$root/verdict/SKILL.md" || payload_ok="false"
  diff -r -q "$EVAL_ROOT/prompts" "$root/verdict/prompts" >/dev/null 2>&1 || payload_ok="false"
  [ "$(jq -r '.tool // empty' "$root/verdict/.verdict-managed" 2>/dev/null)" = "verdict" ] || payload_ok="false"
done
if [ "$payload_ok" = "true" ]; then
  good "owned verdict/ payload installed byte-identical with receipts (repo + home)"
else
  bad "installed payload differs from the tool checkout"
fi
if grep -q "someone else's verdict copy" "$HOME/.agents/skills/verdict/SKILL.md" \
  && [ ! -f "$HOME/.agents/skills/verdict/.verdict-managed" ] \
  && (cd "$EVAL_SANDBOX" && shasum -c "$FOREIGN_SNAP" >/dev/null 2>&1); then
  good "unowned verdict/ and foreign skills byte-identical after apply"
else
  bad "apply touched foreign-owned state"
fi
if grep -q "(blocked: no installer receipt" "$EVAL_OUT" \
  && grep -q "(create)" "$EVAL_OUT"; then
  good "preview names the blocked slot and the created slots"
else
  bad "preview plan missing blocked/create lines"
fi
if [ ! -e "$EVAL_SANDBOX/.codex" ] && [ ! -e "$HOME/.codex" ]; then
  good "undetected roots (no agent dir) gained nothing"
else
  bad "installer invented an agent home"
fi

# --- K3: idempotent re-run reads keep, exits 0, needs no confirm ---
run_review install </dev/null
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "already up to date" "$EVAL_OUT"; then
  good "re-run without --yes reads keep and exits 0"
else
  bad "re-run exits $EVAL_STATUS"
fi
run_review install --yes </dev/null
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "re-run with --yes stays exit 0"
else
  bad "re-run --yes exits $EVAL_STATUS"
fi

# --- K4: receipt/tool version skew nudges, never fails; --yes refreshes ---
jq '.version="0.0.0-stale"' "$HOME/.claude/skills/verdict/.verdict-managed" > "$EVAL_SANDBOX/rec.tmp"
mv "$EVAL_SANDBOX/rec.tmp" "$HOME/.claude/skills/verdict/.verdict-managed"
run_review install </dev/null
if [ "$EVAL_STATUS" -eq 0 ] && grep -qi "version skew" "$EVAL_ERR"; then
  good "stale receipt nudges on stderr and stays exit 0"
else
  bad "skew leg exits $EVAL_STATUS"
fi
if [ "$(jq -r '.version' "$HOME/.claude/skills/verdict/.verdict-managed")" = "0.0.0-stale" ]; then
  good "unconfirmed re-run leaves the owned receipt alone (no silent writes)"
else
  bad "unconfirmed re-run rewrote the receipt"
fi
run_review install --yes </dev/null
tool_v="$(jq -r .version "$EVAL_ROOT/package.json")"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.version' "$HOME/.claude/skills/verdict/.verdict-managed")" = "$tool_v" ] \
  && cmp -s "$EVAL_ROOT/SKILL.md" "$HOME/.claude/skills/verdict/SKILL.md"; then
  good "--yes refreshes the owned receipt to tool v$tool_v, payload intact"
else
  bad "receipt refresh exits $EVAL_STATUS"
fi

# --- K5: offline across every leg ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across install legs (offline)"
else
  bad "stub gh intercepted calls during install legs"
fi

# --- restore: fake HOME and repo-scope skill dirs stay in the sandbox ---
export HOME="$OLD_HOME"
rm -rf "$EVAL_SANDBOX/fakehome" "$EVAL_SANDBOX/.agents" "$EVAL_SANDBOX/foreign-snap.txt"

if [ "$fails" -ne 0 ]; then echo "case skill-installer: FAIL ($fails)"; exit 1; fi
echo "case skill-installer: PASS"
