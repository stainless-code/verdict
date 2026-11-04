#!/bin/sh
# setup-verdict hermetic test (macOS/Linux): runs the action's install-step
# logic from action.yml against fake release assets served over file://,
# with a fake cosign and a logging curl. No network, no real Sigstore.
#
# Grades (mirrors evals/cases/installer-binary.sh B1/B3/B7, plus the action):
#   A1  pinned install verifies (fake cosign invoked with the contract
#       identity/issuer), installs verdict, writes a versioned receipt.
#   A2  `verdict --version` runs; the install dir is the action's
#       RUNNER_TOOL_CACHE layout and PATH-ready (no rc files touched).
#   A3  second invocation with the same version reuses the binary: the
#       action's `[ -x binary ]` guard (copied below; see action.yml)
#       means zero new curl calls.
#   A4  corrupt checksums fail closed; the previous binary is untouched.
#   A5  require-signature=false without cosign installs checksum-only.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/setup-verdict-test.XXXXXX")"
trap 'rm -rf "$SANDBOX"' 0 1 2 15

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

GOOD=0.0.0-test.1
CORRUPT=0.0.0-test.2
NOSIG=0.0.0-test.3

# --- fixtures: one good, one corrupt, one for the checksum-only leg ---
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$GOOD"
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$CORRUPT" --corrupt
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$NOSIG" --no-shims

FAKEBIN="$SANDBOX/fake/fakebin"
export FAKE_COSIGN_LOG="$SANDBOX/cosign.log" FAKE_COSIGN_STATUS=0
export FAKE_CURL_LOG="$SANDBOX/curl.log"
: > "$FAKE_COSIGN_LOG"; : > "$FAKE_CURL_LOG"

INSTALL="$SANDBOX/toolcache/setup-verdict/$GOOD"
export VERDICT_DOWNLOAD_URL="file://$SANDBOX/fake"
export VERDICT_NO_MODIFY_PATH=1 VERDICT_REQUIRE_SIGNATURE=1
export VERDICT_COSIGN="$FAKEBIN/cosign"
export VERDICT_INSTALL="$INSTALL"
PATH="$FAKEBIN:$PATH"

# --- A1: pinned install through the real installer, as the action runs it ---
VERDICT_VERSION="$GOOD" sh "$ROOT/install.sh" >/dev/null
[ -x "$INSTALL/verdict" ] && good "installed $GOOD" || bad "binary missing"
receipt="$(cat "$INSTALL/.verdict-installer")"
case "$receipt" in
  *'"version":"'"$GOOD"'"'*) good "receipt pins $GOOD" ;;
  *) bad "receipt wrong: $receipt" ;;
esac
grep -q 'certificate-identity-regexp' "$FAKE_COSIGN_LOG" \
  && grep -q 'token.actions.githubusercontent' "$FAKE_COSIGN_LOG" \
  && good "fake cosign got the contract identity/issuer" \
  || bad "fake cosign argv wrong"
grep -q "$GOOD" "$FAKE_CURL_LOG" && good "fetched the pinned archive" || bad "no fetch logged"

# --- A2: smoke + PATH-readiness, no rc files ---
out="$("$INSTALL/verdict" --version)"
case "$out" in
  *"$GOOD"*) good "--version prints $GOOD" ;;
  *) bad "--version printed: $out" ;;
esac
[ ! -f "$HOME/.bashrc.verdict-test-marker" ] || bad "rc file touched"
grep -qFx "export PATH=\"$INSTALL:\$PATH\"" ~/.bashrc 2>/dev/null \
  && bad "PATH line leaked into ~/.bashrc" \
  || good "no shell rc file modified"

# --- A3: the action's reuse guard means no second download ---
curl_before="$(wc -l <"$FAKE_CURL_LOG" | tr -d ' ')"
# Copied from setup-verdict/action.yml (install verdict step): reuse when the
# version-scoped binary is already present.
if [ -x "$VERDICT_INSTALL/verdict" ]; then
  echo "setup-verdict: reusing $VERDICT_INSTALL/verdict (already installed; no download)"
else
  VERDICT_VERSION="$GOOD" sh "$ROOT/install.sh" >/dev/null
fi
curl_after="$(wc -l <"$FAKE_CURL_LOG" | tr -d ' ')"
[ "$curl_before" = "$curl_after" ] \
  && good "second invocation made no download ($curl_before curl calls)" \
  || bad "second invocation downloaded again ($curl_before -> $curl_after)"

# --- A4: corrupt checksums fail closed, old binary untouched ---
sum_before="$(shasum -a 256 "$INSTALL/verdict" | awk '{print $1}')"
export VERDICT_INSTALL="$SANDBOX/toolcache/setup-verdict/$CORRUPT"
if VERDICT_VERSION="$CORRUPT" sh "$ROOT/install.sh" >/dev/null 2>&1; then
  bad "corrupt release installed"
else
  good "corrupt release refused"
fi
[ ! -x "$SANDBOX/toolcache/setup-verdict/$CORRUPT/verdict" ] \
  && good "nothing installed for $CORRUPT" \
  || bad "corrupt binary present"
sum_after="$(shasum -a 256 "$INSTALL/verdict" | awk '{print $1}')"
[ "$sum_before" = "$sum_after" ] && good "previous binary untouched" || bad "previous binary changed"

# --- A5: checksum-only leg (require-signature=false, no cosign) ---
PATH="$(printf '%s' "$PATH" | tr ':' '\n' | grep -v "^$FAKEBIN\$" | paste -sd: -)"
unset VERDICT_COSIGN
export VERDICT_INSTALL="$SANDBOX/toolcache/setup-verdict/$NOSIG" VERDICT_REQUIRE_SIGNATURE=0
if VERDICT_VERSION="$NOSIG" sh "$ROOT/install.sh" >"$SANDBOX/nosig.log" 2>&1; then
  grep -q 'WARNING' "$SANDBOX/nosig.log" && good "checksum-only warns" || bad "no checksum warning"
  [ -x "$VERDICT_INSTALL/verdict" ] && good "checksum-only installed" || bad "checksum-only missing binary"
else
  bad "checksum-only install failed"
fi

if [ "$fails" != 0 ]; then echo "$fails failure(s)"; exit 1; fi
echo "setup-verdict hermetic test ok"
