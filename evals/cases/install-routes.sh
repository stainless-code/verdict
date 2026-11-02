#!/usr/bin/env bash
# Case install-routes: every documented install route is coherent and true.
#
# Grades (all hermetic — real `npm pack` into the sandbox, fake HOME, stub gh):
#   R1  registry copy carries a working CLI: the packed tarball unpacks to a
#       skill dir whose scripts/review.sh runs `install --yes` into repo and
#       home scopes (SKILL.md + prompts/ + scripts/ byte-identical,
#       receipted), and the installed copy's own CLI re-runs to keep.
#   R2  cwd owns .review: the unpacked CLI's `init` writes .review/ controls
#       into the cwd target repo, never beside the tool.
#   R3  README routes honestly: the registry block says install.sh is not
#       shipped and never tells registry users to run ./install.sh (that
#       command appears only in the source-checkout route).
#   R4  SKILL.md resolves from any install: the resolution rule is stated,
#       and every scripts/… or prompts/… path it names exists inside the
#       installed copy layout, not just the checkout.
#   R5  install.sh is checkout-only: it refuses outside a git checkout with
#       a typed hint, and --list still passes inside the checkout.
#   R6  offline: zero stub-gh intercepts across every leg.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

command -v npm >/dev/null 2>&1 || { bad "npm not on PATH (route proof needs npm pack)"; echo "case install-routes: FAIL ($fails)"; exit 1; }

# --- R1: pack, unpack as a registry skill copy, install from it ---
PACK_OUT="$EVAL_SANDBOX/install-routes-pack.txt"
set +e
(cd "$EVAL_ROOT" && npm pack --offline --pack-destination "$EVAL_SANDBOX" >"$PACK_OUT" 2>&1)
pack_status=$?
set -e
if [ "$pack_status" -ne 0 ]; then
  bad "npm pack exits $pack_status: $(head -c 200 "$PACK_OUT")"
  echo "case install-routes: FAIL ($fails)"; exit 1
fi
TARBALL="$EVAL_SANDBOX/$(tail -n 1 "$PACK_OUT")"
if [ -f "$TARBALL" ]; then
  good "real pack lands in the sandbox, never the repo"
else
  bad "pack printed no tarball ($TARBALL missing)"
fi
mkdir -p "$EVAL_SANDBOX/regunpack"
tar -xzf "$TARBALL" -C "$EVAL_SANDBOX/regunpack"
PKG="$EVAL_SANDBOX/regunpack/package"
if [ -x "$PKG/scripts/review.sh" ] && [ -f "$PKG/SKILL.md" ]; then
  good "registry copy carries the CLI (scripts/review.sh + SKILL.md)"
else
  bad "unpacked registry copy lacks scripts/review.sh or SKILL.md"
fi

OLD_HOME="$HOME"
export HOME="$EVAL_SANDBOX/fakehome-install"
TARGET="$EVAL_SANDBOX/target"
mkdir -p "$TARGET/.agents/skills" "$HOME/.agents/skills"
set +e
(cd "$TARGET" && HOME="$HOME" PATH="$EVAL_STUBBIN:$PATH" bash "$PKG/scripts/review.sh" install --yes </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
install_status=$?
set -e
if [ "$install_status" -eq 0 ]; then
  good "registry CLI installs its payload (exit 0)"
else
  bad "registry CLI install exits $install_status"
fi
copy_ok="true"
for root in "$TARGET/.agents/skills" "$HOME/.agents/skills"; do
  cmp -s "$PKG/SKILL.md" "$root/verdict/SKILL.md" || copy_ok="false"
  diff -r -q "$PKG/prompts" "$root/verdict/prompts" >/dev/null 2>&1 || copy_ok="false"
  diff -r -q "$PKG/scripts" "$root/verdict/scripts" >/dev/null 2>&1 || copy_ok="false"
  [ "$(jq -r '.tool // empty' "$root/verdict/.verdict-managed" 2>/dev/null)" = "verdict" ] || copy_ok="false"
done
if [ "$copy_ok" = "true" ]; then
  good "installed copy payload (SKILL.md + prompts/ + scripts/) byte-identical with receipts"
else
  bad "installed copy payload differs from the registry copy"
fi
set +e
(cd "$TARGET" && HOME="$HOME" PATH="$EVAL_STUBBIN:$PATH" bash "$TARGET/.agents/skills/verdict/scripts/review.sh" install </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
rerun_status=$?
set -e
if [ "$rerun_status" -eq 0 ] && grep -q "already up to date" "$EVAL_OUT"; then
  good "installed copy's own CLI re-runs to keep (carried CLI works)"
else
  bad "installed copy's CLI re-run exits $rerun_status"
fi

# --- R2: .review/ lands in cwd, never beside the tool ---
set +e
(cd "$TARGET" && HOME="$HOME" PATH="$EVAL_STUBBIN:$PATH" bash "$PKG/scripts/review.sh" init --preset standard --yes </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
init_status=$?
set -e
if [ "$init_status" -eq 0 ] && [ -f "$TARGET/.review/budget" ] && [ ! -e "$PKG/.review" ]; then
  good "init writes .review/ into the cwd target repo, nothing beside the tool"
else
  bad "init cwd ownership wrong (exit $init_status)"
fi

# --- R3: README routes registry users away from install.sh ---
REG_RANGE="$EVAL_SANDBOX/readme-registry.txt"
awk '/registry route/,/source route/' "$EVAL_ROOT/README.md" >"$REG_RANGE"
if grep -q -F "NOT shipped in the npm tarball" "$REG_RANGE"; then
  good "README says install.sh is not shipped in the registry route"
else
  bad "README registry block lost the install.sh note"
fi
if grep -q -F "./install.sh" "$REG_RANGE"; then
  bad "README tells registry users to run ./install.sh"
else
  good "README never offers ./install.sh to registry users"
fi

# --- R4: SKILL.md resolves from the installed copy, not just the checkout ---
if grep -q "never beside the tool" "$EVAL_ROOT/SKILL.md" \
  && grep -q 'installed `verdict/` skill dir' "$EVAL_ROOT/SKILL.md"; then
  good "SKILL.md states the command-resolution rule"
else
  bad "SKILL.md lost the where-commands-run block"
fi
COPY="$TARGET/.agents/skills/verdict"
refs_ok="true"
while IFS= read -r ref; do
  [ -n "$ref" ] || continue
  [ -f "$COPY/$ref" ] || { refs_ok="false"; bad "installed copy lacks SKILL.md reference $ref"; }
done < <(grep -o -E '(scripts|prompts)/[A-Za-z0-9_.-]+' "$COPY/SKILL.md" | LC_ALL=C sort -u)
if [ "$refs_ok" = "true" ]; then
  good "every SKILL.md script/prompt path resolves inside the installed copy"
fi

# --- R5: install.sh links checkouts only ---
# The fixture lives outside the shared sandbox: chunk-* cases git-init the
# sandbox root itself, so anything under it counts as inside a work tree.
NOGIT_BASE="$(mktemp -d "${TMPDIR:-/tmp}/verdict-nogit.XXXXXX")"
NOGIT="$NOGIT_BASE/nogit"
mkdir -p "$NOGIT"
cp "$EVAL_ROOT/install.sh" "$EVAL_ROOT/SKILL.md" "$NOGIT/"
mkdir -p "$NOGIT/scripts"
cp "$EVAL_ROOT"/scripts/*.sh "$NOGIT/scripts/"
set +e
(cd "$NOGIT" && HOME="$HOME" bash ./install.sh --check </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
nogit_status=$?
set -e
if [ "$nogit_status" -ne 0 ] && grep -q "git checkout" "$EVAL_ERR"; then
  good "install.sh refuses outside a git checkout with a typed hint"
else
  bad "install.sh outside a checkout exits $nogit_status"
fi
set +e
(cd "$EVAL_ROOT" && HOME="$HOME" bash ./install.sh --list >/dev/null 2>&1)
list_status=$?
set -e
if [ "$list_status" -eq 0 ]; then
  good "install.sh --list still passes inside the checkout"
else
  bad "install.sh --list exits $list_status in the checkout"
fi

# --- R6: offline across every leg ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across route legs (offline)"
else
  bad "stub gh intercepted calls during route legs"
fi

# --- restore: fake HOME and sandbox dirs stay out of the repo ---
export HOME="$OLD_HOME"
rm -rf "$NOGIT_BASE"
rm -rf "$EVAL_SANDBOX/fakehome-install" "$EVAL_SANDBOX/target" \
  "$EVAL_SANDBOX/regunpack" "$EVAL_SANDBOX"/install-routes-pack.txt "$EVAL_SANDBOX"/readme-registry.txt
rm -f "$EVAL_SANDBOX"/*.tgz

if [ "$fails" -ne 0 ]; then echo "case install-routes: FAIL ($fails)"; exit 1; fi
echo "case install-routes: PASS"
