#!/bin/sh
# setup-verdict hermetic test (macOS/Linux): runs the action's install-step
# logic from action.yml against fake release assets served over file://,
# with a fake cosign and a logging curl. No network, no real Sigstore.
#
# Grades (mirrors evals/cases/installer-binary.sh B1/B3/B7, plus the action):
#   A1  pinned install verifies (fake cosign invoked with the contract
#       identity/issuer, the push-trigger pin, and the release's published
#       build commit), installs verdict, writes a versioned receipt.
#   A2  `verdict --version` runs; the install dir is the action's
#       RUNNER_TOOL_CACHE layout and PATH-ready (no rc files touched).
#   A3  cached binaries are re-verified before execution.
#   A4  corrupt checksums fail closed; the previous binary is untouched.
#   A5  require-signature=false without cosign installs checksum-only.
#   A6  a release that publishes no commit.txt fails closed.
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
NOPIN=0.0.0-test.4
RELEASE_COMMIT=72e7d304d3cb8b85bc6c18eab2e51a50bd38fdad

# --- fixtures: one good, one corrupt, one for the checksum-only leg ---
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$GOOD"
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$CORRUPT" --corrupt
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$NOSIG" --no-shims
python3 "$HERE/test-fixtures/make-fake-release.py" --out "$SANDBOX/fake" --version "$NOPIN"
rm "$SANDBOX/fake/v$NOPIN/commit.txt"

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
# The identity must be the ref the Release workflow actually fires on: a tag
# identity never matches a real certificate, so a green run here would hide a
# broken action.
grep -q 'certificate-identity-regexp' "$FAKE_COSIGN_LOG" \
  && grep -q "refs/heads/main" "$FAKE_COSIGN_LOG" \
  && ! grep -q 'refs/tags/' "$FAKE_COSIGN_LOG" \
  && grep -q 'token.actions.githubusercontent' "$FAKE_COSIGN_LOG" \
  && good "fake cosign got the contract identity/issuer" \
  || bad "fake cosign argv wrong"
grep -q "certificate-github-workflow-sha $RELEASE_COMMIT" "$FAKE_COSIGN_LOG" \
  && good "fake cosign got the build-commit pin" \
  || bad "fake cosign argv lacks the build-commit pin"
grep -q 'certificate-github-workflow-trigger push' "$FAKE_COSIGN_LOG" \
  && good "fake cosign got the push-trigger pin" \
  || bad "fake cosign argv lacks the push-trigger pin"
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

# Extract the shipped shell bodies so these tests exercise the action itself.
python3 - "$HERE/action.yml" "$SANDBOX" <<'PYTHON'
import pathlib, sys
source = pathlib.Path(sys.argv[1]).read_text()
for step in ('resolve-nix', 'install-nix'):
    body = source.split('      id: ' + step + '\n', 1)[1]
    body = body.split('      run: |\n', 1)[1].split('\n    - name:', 1)[0]
    lines = []
    for line in body.splitlines():
        if line and not line.startswith('        '):
            break
        lines.append(line[8:])
    pathlib.Path(sys.argv[2], step + '.sh').write_text('\n'.join(lines) + '\n')
PYTHON
export ACTION_PATH="$HERE" VERDICT_VERSION="$GOOD"
export GITHUB_OUTPUT="$SANDBOX/output" GITHUB_PATH="$SANDBOX/path"
cosign_before="$(wc -l < "$FAKE_COSIGN_LOG")"
bash "$SANDBOX/install-nix.sh" > /dev/null
cosign_after="$(wc -l < "$FAKE_COSIGN_LOG")"
[ "$cosign_after" -gt "$cosign_before" ] && good "cache hit re-verifies signature" || bad "cache hit skipped signature"
cp "$INSTALL/verdict" "$SANDBOX/original"
printf '#!/bin/sh\ntouch "%s"\n' "$SANDBOX/cached-executed" > "$INSTALL/verdict"
if bash "$SANDBOX/install-nix.sh" > "$SANDBOX/cache.log" 2>&1; then
  bad "tampered cache accepted"
else
  grep -q 'cached binary differs from verified release' "$SANDBOX/cache.log" \
    && good "tampered cache refused" || bad "unexpected cache refusal"
fi
[ ! -e "$SANDBOX/cached-executed" ] && good "tampered cache never executed" || bad "tampered cache executed"
cp "$SANDBOX/original" "$INSTALL/verdict"
export RUNNER_TOOL_CACHE="$SANDBOX/toolcache" INPUT_REQUIRE_SIGNATURE=true
export INPUT_VERSION="1.0.0'; touch pwned; #\""
if (cd "$SANDBOX" && bash ./resolve-nix.sh) > "$SANDBOX/injection.log" 2>&1; then
  bad "injection-shaped version accepted"
else
  good "injection-shaped version rejected"
fi
[ ! -e "$SANDBOX/pwned" ] && good "injection created no file" || bad "injection executed"

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
export VERDICT_COSIGN="$SANDBOX/no-cosign"
export VERDICT_INSTALL="$SANDBOX/toolcache/setup-verdict/$NOSIG" VERDICT_REQUIRE_SIGNATURE=0
if VERDICT_VERSION="$NOSIG" sh "$ROOT/install.sh" >"$SANDBOX/nosig.log" 2>&1; then
  grep -q 'WARNING' "$SANDBOX/nosig.log" && good "checksum-only warns" || bad "no checksum warning"
  [ -x "$VERDICT_INSTALL/verdict" ] && good "checksum-only installed" || bad "checksum-only missing binary"
else
  cat "$SANDBOX/nosig.log" >&2
  bad "checksum-only install failed"
fi

# --- A6: a release without commit.txt fails closed ---
export VERDICT_COSIGN="$FAKEBIN/cosign" VERDICT_REQUIRE_SIGNATURE=1
PATH="$FAKEBIN:$PATH"
export VERDICT_INSTALL="$SANDBOX/toolcache/setup-verdict/$NOPIN"
if VERDICT_VERSION="$NOPIN" sh "$ROOT/install.sh" >"$SANDBOX/nopin.log" 2>&1; then
  bad "a release without commit.txt installed"
else
  grep -q 'publishes no build commit' "$SANDBOX/nopin.log" \
    && good "missing commit.txt refused as unpinned" \
    || bad "refusal does not name the build commit"
fi
[ ! -e "$VERDICT_INSTALL/verdict" ] \
  && good "nothing installed without a build commit" \
  || bad "unpinned binary present"

if [ "$fails" != 0 ]; then echo "$fails failure(s)"; exit 1; fi
echo "setup-verdict hermetic test ok"
