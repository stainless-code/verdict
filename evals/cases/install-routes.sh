#!/usr/bin/env bash
# Case install-routes: every documented install route is coherent and true.
#
# Grades (all hermetic — real `npm pack` into the sandbox, fake HOME, stub gh):
#   R1  registry copy carries the skill payload: the packed tarball unpacks
#       to a skill dir holding SKILL.md + prompts/ + remaining scripts/ (no
#       scripts/review.sh — the CLI is the `verdict` binary). The binary's
#       embedded `skill install` writes the same payload into repo and home
#       scopes (byte-identical, receipted), and a re-run reads keep.
#   R2  cwd owns .review: the binary's `init` writes .review/ controls
#       into the cwd target repo, never beside the tool.
#   R3  README routes honestly: the registry block says install.sh is not
#       shipped and never tells registry users to run ./install.sh (that
#       command appears only in the binary/source-checkout routes).
#   R4  SKILL.md resolves from any install: the resolution rule is stated,
#       and every scripts/… or prompts/… path it names exists inside the
#       installed copy layout, not just the checkout.
#   R5  install-skill.sh is checkout-only: it refuses outside a git checkout
#       with a typed hint, and --list still passes inside the checkout.
#   R6  the install-skill.sh doctor gate passes on a PATH without jq/shasum:
#       those tools were never needed by the Go binary.
#   R7  offline: zero stub-gh intercepts across every route leg (the doctor
#       preflight excluded; it necessarily probes gh).
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
PACK_OUT="$EVAL_SANDBOX/install-routes-pack.json"
PACK_ERR="$EVAL_SANDBOX/install-routes-pack.err"
set +e
(cd "$EVAL_ROOT" && npm pack --offline --json --pack-destination "$EVAL_SANDBOX" >"$PACK_OUT" 2>"$PACK_ERR")
pack_status=$?
set -e
if [ "$pack_status" -ne 0 ]; then
  bad "npm pack exits $pack_status: $(head -c 200 "$PACK_ERR")"
  echo "case install-routes: FAIL ($fails)"; exit 1
fi
PACK_NAME="$(jq -r '.[0].filename // empty' "$PACK_OUT" 2>/dev/null || true)"
TARBALL="$EVAL_SANDBOX/$PACK_NAME"
if [ -n "$PACK_NAME" ] && [ -f "$TARBALL" ]; then
  good "real pack lands in the sandbox, never the repo"
else
  bad "pack reported no tarball ($TARBALL missing)"
fi
mkdir -p "$EVAL_SANDBOX/regunpack"
tar -xzf "$TARBALL" -C "$EVAL_SANDBOX/regunpack"
PKG="$EVAL_SANDBOX/regunpack/package"
if [ -f "$PKG/SKILL.md" ] && [ -f "$PKG/scripts/verdict-complete.sh" ] \
  && [ ! -e "$PKG/scripts/review.sh" ]; then
  good "registry copy carries the payload (SKILL.md + remaining scripts, no review.sh)"
else
  bad "unpacked registry copy payload wrong (want SKILL.md + scripts, no review.sh)"
fi

OLD_HOME="$HOME"
export HOME="$EVAL_SANDBOX/fakehome-install"
TARGET="$EVAL_SANDBOX/target"
mkdir -p "$TARGET/.agents/skills" "$HOME/.agents/skills"
install_status=0
for dir in "$TARGET/.agents/skills" "$HOME/.agents/skills"; do
  set +e
  (cd "$TARGET" && HOME="$HOME" PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" skill install --dir "$dir" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
  st=$?
  set -e
  if [ "$st" -ne 0 ]; then install_status=$st; fi
done
if [ "$install_status" -eq 0 ]; then
  good "verdict skill install applies the embedded payload (exit 0)"
else
  bad "verdict skill install exits $install_status"
fi
copy_ok="true"
inventory_status=0
inventory="$("$EVAL_CLI" skill files)" || inventory_status=$?
if [ "$inventory_status" -ne 0 ] || [ -z "$inventory" ]; then
  copy_ok="false"
  bad "skill files exits $inventory_status with an empty payload inventory"
fi
for want in SKILL.md scripts/verdict schemas/validate.sh prompts/review.md; do
  if ! grep -qxF "$want" <<<"$inventory"; then
    copy_ok="false"
    bad "payload inventory misses $want"
  fi
done
for root in "$TARGET/.agents/skills" "$HOME/.agents/skills"; do
  # Every embedded payload file matches the packed registry copy (the
  # unpacked tarball is the source of record, not the checkout), and the
  # receipt names the same tool version.
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    cmp -s "$PKG/$rel" "$root/verdict/$rel" || copy_ok="false"
  done <<<"$inventory"
  [ "$(jq -r '.tool // empty' "$root/verdict/.verdict-managed" 2>/dev/null)" = "verdict" ] || copy_ok="false"
  [ "$(jq -r '.version // empty' "$root/verdict/.verdict-managed" 2>/dev/null)" = "$(jq -r .version "$EVAL_ROOT/package.json")" ] || copy_ok="false"
done
if [ "$copy_ok" = "true" ]; then
  good "embedded payload (SKILL.md + prompts/ + schemas/ + scripts/verdict) byte-identical with receipts"
else
  bad "installed payload differs from the registry copy"
fi
set +e
(cd "$TARGET" && HOME="$HOME" PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" skill install --dir "$TARGET/.agents/skills" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
rerun_status=$?
set -e
if [ "$rerun_status" -eq 0 ] && grep -q "(keep)" "$EVAL_OUT"; then
  good "re-run reads keep (exit 0)"
else
  bad "skill install re-run exits $rerun_status"
fi

# --- R2: .review/ lands in cwd, never beside the tool ---
set +e
(cd "$TARGET" && HOME="$HOME" PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" init --preset standard --yes </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
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
refs_seen=0
if [ ! -f "$COPY/SKILL.md" ]; then
  refs_ok="false"
  bad "installed copy lacks SKILL.md"
fi
while IFS= read -r ref; do
  [ -n "$ref" ] || continue
  refs_seen=$((refs_seen + 1))
  [ -f "$COPY/$ref" ] || { refs_ok="false"; bad "installed copy lacks SKILL.md reference $ref"; }
done < <(grep -o -E '(scripts|prompts)/[A-Za-z0-9_.-]+' "$COPY/SKILL.md" 2>/dev/null | LC_ALL=C sort -u || true)
if [ "$refs_seen" -eq 0 ]; then
  refs_ok="false"
  bad "SKILL.md names no script/prompt paths"
fi
if [ "$refs_ok" = "true" ]; then
  good "every SKILL.md script/prompt path resolves inside the installed copy"
fi

# --- R5: install-skill.sh links checkouts only ---
# The fixture lives outside the shared sandbox: chunk-* cases git-init the
# sandbox root itself, so anything under it counts as inside a work tree.
NOGIT_BASE="$(mktemp -d "${TMPDIR:-/tmp}/verdict-nogit.XXXXXX")"
NOGIT="$NOGIT_BASE/nogit"
mkdir -p "$NOGIT"
cp "$EVAL_ROOT/install-skill.sh" "$EVAL_ROOT/SKILL.md" "$NOGIT/"
mkdir -p "$NOGIT/scripts"
cp "$EVAL_ROOT"/scripts/*.sh "$NOGIT/scripts/"
set +e
(cd "$NOGIT" && HOME="$HOME" bash ./install-skill.sh --check </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
nogit_status=$?
set -e
if [ "$nogit_status" -ne 0 ] && grep -q "git checkout" "$EVAL_ERR"; then
  good "install-skill.sh refuses outside a git checkout with a typed hint"
else
  bad "install-skill.sh outside a checkout exits $nogit_status"
fi
set +e
(cd "$EVAL_ROOT" && HOME="$HOME" bash ./install-skill.sh --list >/dev/null 2>&1)
list_status=$?
set -e
if [ "$list_status" -eq 0 ]; then
  good "install-skill.sh --list still passes inside the checkout"
else
  bad "install-skill.sh --list exits $list_status in the checkout"
fi

# --- R6: install-skill.sh doctor gate needs no jq/shasum ---
# A minimal PATH keeps only the tools doctor probes (gh/git/ps) plus the
# shell helpers install-skill.sh uses; jq and shasum are absent. The source
# install route must still pass its doctor gate.
route_intercepts="$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))"
MINBIN="$EVAL_SANDBOX/minbin"
mkdir -p "$MINBIN"
for tool in bash dirname git ps readlink; do
  ln -s "$(command -v "$tool")" "$MINBIN/$tool"
done
ln -s "$EVAL_STUBBIN/gh" "$MINBIN/gh"
MINHOME="$EVAL_SANDBOX/fakehome-minbin"
MINLINKS="$EVAL_SANDBOX/minbin-skills"
mkdir -p "$MINHOME" "$MINLINKS"
ln -s "$EVAL_ROOT" "$MINLINKS/verdict"
set +e
(cd "$EVAL_SANDBOX" && HOME="$MINHOME" PATH="$MINBIN" GH_STUB_DOCTOR=repo \
  bash "$EVAL_ROOT/install-skill.sh" --check --dir "$MINLINKS" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR")
minpath_status=$?
set -e
if [ "$minpath_status" -eq 0 ] && grep -q "check: scope ok" "$EVAL_OUT"; then
  good "install-skill.sh doctor gate passes without jq/shasum (exit 0)"
else
  bad "install-skill.sh without jq/shasum exits $minpath_status: $(head -c 240 "$EVAL_ERR")"
fi

# --- R7: offline across every route leg (doctor preflight excluded) ---
if [ "$route_intercepts" = "0" ]; then
  good "zero stub-gh intercepts across route legs (doctor preflight excluded)"
else
  bad "stub gh intercepted $route_intercepts call(s) during route legs"
fi

# --- restore: fake HOME and sandbox dirs stay out of the repo ---
export HOME="$OLD_HOME"
rm -rf "$NOGIT_BASE"
rm -rf "$EVAL_SANDBOX/fakehome-install" "$EVAL_SANDBOX/fakehome-minbin" \
  "$EVAL_SANDBOX/target" "$EVAL_SANDBOX/minbin" "$EVAL_SANDBOX/minbin-skills" \
  "$EVAL_SANDBOX/regunpack" "$EVAL_SANDBOX"/install-routes-pack.txt "$EVAL_SANDBOX"/readme-registry.txt
rm -f "$EVAL_SANDBOX"/*.tgz

if [ "$fails" -ne 0 ]; then echo "case install-routes: FAIL ($fails)"; exit 1; fi
echo "case install-routes: PASS"
