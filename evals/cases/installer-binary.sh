#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

CASE="$EVAL_SANDBOX/installer"
REL="$CASE/release"
FAKEBIN="$CASE/fakebin"
BIN_SRC="$CASE/fake-verdict"
mkdir -p "$REL" "$FAKEBIN" "$CASE"
printf '#!/bin/sh\nprintf "fake-verdict %%s\\n" "$*"\n' > "$BIN_SRC"
chmod +x "$BIN_SRC"

cat > "$FAKEBIN/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) printf '%s\n' "${FAKE_UNAME_S:-$(command -p uname -s)}" ;;
  -m) printf '%s\n' "${FAKE_UNAME_M:-$(command -p uname -m)}" ;;
  *) real=$(PATH="$(getconf PATH)"; command -v uname); exec "$real" "$@" ;;
esac
EOF
cat > "$FAKEBIN/sysctl" <<'EOF'
#!/bin/sh
case "$*" in
  "-n sysctl.proc_translated") printf '%s\n' "${FAKE_PROC_TRANSLATED:-0}" ;;
  *) exit 1 ;;
esac
EOF
cat > "$FAKEBIN/cosign" <<'EOF'
#!/bin/sh
for a in "$@"; do printf '%s ' "$a" >> "${FAKE_COSIGN_LOG:-/dev/null}"; done
printf '\n' >> "${FAKE_COSIGN_LOG:-/dev/null}"
exit "${FAKE_COSIGN_STATUS:-0}"
EOF
cat > "$FAKEBIN/curl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "${FAKE_CURL_LOG:-/dev/null}"
for a in "$@"; do
  if [ "$a" = '%{url_effective}' ]; then printf '%s\n' "${FAKE_EFFECTIVE_URL:-}"; exit 0; fi
done
real=$(PATH="$(getconf PATH)"; command -v curl)
exec "$real" "$@"
EOF
chmod +x "$FAKEBIN/uname" "$FAKEBIN/sysctl" "$FAKEBIN/cosign" "$FAKEBIN/curl"

mk_release() { # <version> <goos> <goarch>
  local v="$1" goos="$2" goarch="$3" dir="$REL/v$1/payload"
  mkdir -p "$dir"
  cp "$BIN_SRC" "$dir/verdict"
  : > "$dir/LICENSE"
  : > "$dir/README.md"
  : > "$dir/NOTICE.md"
  (cd "$dir" && tar -czf "../verdict_${v}_${goos}_${goarch}.tar.gz" verdict LICENSE README.md NOTICE.md)
  rm -rf "$dir"
}
finish_release() { # <version>
  local dir="$REL/v$1"
  (cd "$dir" && shasum -a 256 verdict_*.tar.gz > checksums.txt)
  printf '{}\n' > "$dir/checksums.txt.sigstore.json"
}

for platform in "darwin amd64" "darwin arm64" "linux amd64" "linux arm64"; do
  # shellcheck disable=SC2086
  set -- $platform
  mk_release 1.2.3 "$1" "$2"
done
finish_release 1.2.3
mk_release 2.0.0 linux amd64
finish_release 2.0.0
mk_release 3.0.0 linux amd64
finish_release 3.0.0
mk_release 9.9.9 linux amd64
finish_release 9.9.9
(cd "$REL/v2.0.0" && printf '%064d  verdict_2.0.0_linux_amd64.tar.gz\n' 0 > checksums.txt)

contract="$EVAL_ROOT/release/contract.json"
if grep -Fq "CHECKSUMS='$(jq -r .checksums "$contract")'" "$EVAL_ROOT/install.sh" \
  && grep -Fq "BUNDLE='$(jq -r .signatureBundle "$contract")'" "$EVAL_ROOT/install.sh" \
  && grep -Fq "COSIGN_ISSUER='$(jq -r .cosign.issuer "$contract")'" "$EVAL_ROOT/install.sh" \
  && grep -Fq "COSIGN_IDENTITY_RE='$(jq -r .cosign.identityRegexp "$contract")'" "$EVAL_ROOT/install.sh" \
  && grep -Fq 'https://github.com/stainless-code/verdict/releases/latest' "$EVAL_ROOT/install.sh" \
  && grep -Fq 'verdict_${VERSION}_${os}_${arch}.tar.gz' "$EVAL_ROOT/install.sh"; then
  good "install.sh names the contract checksums, bundle, archive shape, and issuer"
else
  bad "install.sh drifted from release/contract.json (checksums/bundle/archive/issuer)"
fi

INSTALL_DIR="$CASE/install"
CASE_HOME="$CASE/home"
COSIGN_LOG="$CASE/cosign.log"
CURL_LOG="$CASE/curl.log"
PLAT_OS=linux; PLAT_ARCH=amd64; ROSETTA=0; PIN=1.2.3
NO_PATH=1; REQUIRE_SIG=0; COSIGN_OVERRIDE=''; EFFECTIVE_URL=''; TEST_SHELL=/bin/sh

run_install() {
  local uname_s="$PLAT_OS"
  [ "$PLAT_OS" = darwin ] && uname_s=Darwin
  [ "$PLAT_OS" = linux ] && uname_s=Linux
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    HOME="$CASE_HOME" SHELL="$TEST_SHELL" PATH="$FAKEBIN:$PATH" \
      VERDICT_DOWNLOAD_URL="file://$REL" VERDICT_INSTALL="$INSTALL_DIR" \
      VERDICT_VERSION="$PIN" VERDICT_NO_MODIFY_PATH="$NO_PATH" \
      VERDICT_REQUIRE_SIGNATURE="$REQUIRE_SIG" \
      VERDICT_COSIGN="${COSIGN_OVERRIDE:-$FAKEBIN/cosign}" \
      FAKE_COSIGN_LOG="$COSIGN_LOG" FAKE_COSIGN_STATUS="${FAKE_COSIGN_STATUS:-0}" \
      FAKE_CURL_LOG="$CURL_LOG" FAKE_EFFECTIVE_URL="$EFFECTIVE_URL" \
      FAKE_UNAME_S="$uname_s" FAKE_UNAME_M="$PLAT_ARCH" \
      FAKE_PROC_TRANSLATED="$ROSETTA" \
      sh "$EVAL_ROOT/install.sh" "$@"
  ) >"$EVAL_OUT" 2>"$EVAL_ERR"
  EVAL_STATUS=$?
  set -e
}
fresh() { rm -rf "$INSTALL_DIR" "$CASE_HOME"; mkdir -p "$CASE_HOME"; : > "$COSIGN_LOG"; : > "$CURL_LOG"; }

for leg in "linux amd64 0 linux amd64" "linux arm64 0 linux arm64" \
  "darwin arm64 0 darwin arm64" "darwin amd64 1 darwin arm64"; do
  # shellcheck disable=SC2086
  set -- $leg
  PLAT_OS="$1"; PLAT_ARCH="$2"; ROSETTA="$3"; want_os="$4"; want_arch="$5"
  fresh
  PIN=1.2.3; NO_PATH=1; REQUIRE_SIG=0; COSIGN_OVERRIDE=''; FAKE_COSIGN_STATUS=0
  run_install
  archive="$(jq -r --arg v 1.2.3 --arg os "$want_os" --arg arch "$want_arch" \
    '.archives[] | select(.goos == $os and .goarch == $arch) | .file | sub("\\{version\\}"; $v)' \
    "$EVAL_ROOT/release/contract.json")"
  if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$BIN_SRC" "$INSTALL_DIR/verdict" \
    && [ -x "$INSTALL_DIR/verdict" ] \
    && grep -Fq "\"version\":\"1.2.3\"" "$INSTALL_DIR/.verdict-installer"; then
    good "$want_os/$want_arch installs the verified binary and receipt"
  else
    bad "$want_os/$want_arch install exits $EVAL_STATUS ($(head -c 200 "$EVAL_ERR"))"
  fi
  if grep -Fq "file://$REL/v1.2.3/$archive" "$CURL_LOG"; then
    good "$want_os/$want_arch requests the contract archive $archive"
  else
    bad "$want_os/$want_arch did not request contract archive $archive"
  fi
  if grep -Fq -- '--certificate-identity-regexp' "$COSIGN_LOG" \
    && grep -Fq -- "$(jq -r .cosign.identityRegexp "$EVAL_ROOT/release/contract.json")" "$COSIGN_LOG" \
    && grep -Fq -- '--certificate-oidc-issuer' "$COSIGN_LOG" \
    && grep -Fq "$(jq -r .cosign.issuer "$EVAL_ROOT/release/contract.json")" "$COSIGN_LOG"; then
    good "$want_os/$want_arch verifies the bundle with the contract identity/issuer"
  else
    bad "$want_os/$want_arch cosign argv missing identity/issuer: $(cat "$COSIGN_LOG")"
  fi
  if [ "$ROSETTA" = 1 ]; then
    if grep -q "Rosetta 2" "$EVAL_ERR"; then good "Rosetta note is printed"; else bad "Rosetta note missing"; fi
  fi
  if grep -Fq 'verified; reports fake-verdict --version' "$EVAL_OUT"; then
    good "$want_os/$want_arch smokes the installed binary with --version"
  else
    bad "$want_os/$want_arch has no --version smoke ($(head -c 200 "$EVAL_OUT"))"
  fi
  if grep -Fq 'Next: verdict skill install --detect' "$EVAL_OUT"; then
    good "$want_os/$want_arch points at the skill next step"
  else
    bad "$want_os/$want_arch has no skill next step ($(head -c 200 "$EVAL_OUT"))"
  fi
done

fresh
PLAT_OS=linux; PLAT_ARCH=amd64; ROSETTA=0; PIN=1.2.3; COSIGN_OVERRIDE=''; FAKE_COSIGN_STATUS=0
run_install
cp "$INSTALL_DIR/verdict" "$CASE/old-verdict"
PIN=2.0.0
run_install
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "SHA-256 mismatch" "$EVAL_ERR" \
  && cmp -s "$CASE/old-verdict" "$INSTALL_DIR/verdict" \
  && grep -Fq '"version":"1.2.3"' "$INSTALL_DIR/.verdict-installer"; then
  good "checksum mismatch refuses and leaves the old binary + receipt"
else
  bad "checksum mismatch exit $EVAL_STATUS (old binary changed?)"
fi
assert_stream_nogrep "a refused install prints no next step" "$EVAL_OUT" "Next:" "-F"
fresh
PIN=1.2.3; run_install
cp "$INSTALL_DIR/verdict" "$CASE/old-verdict"
PIN=2.0.0; FAKE_COSIGN_STATUS=1; run_install
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "signature verification failed" "$EVAL_ERR" \
  && cmp -s "$CASE/old-verdict" "$INSTALL_DIR/verdict"; then
  good "bad signature refuses before replacing the binary"
else
  bad "bad signature exit $EVAL_STATUS"
fi
FAKE_COSIGN_STATUS=0
if find "$INSTALL_DIR" -name '.verdict-stage-*' -print | matches .; then
  bad "refused runs left a staging dir"
else
  good "refused runs clean staging behind"
fi

printf '#!/bin/sh\nexit 3\n' > "$BIN_SRC"
mk_release 4.0.0 linux amd64
finish_release 4.0.0
PIN=4.0.0; PLAT_OS=linux; PLAT_ARCH=amd64; run_install
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "does not answer --version" "$EVAL_ERR"; then
  good "a binary that cannot answer --version fails the install instead of passing silently"
else
  bad "unrunnable binary exit $EVAL_STATUS ($(head -c 160 "$EVAL_ERR"))"
fi
printf '#!/bin/sh\nprintf "fake-verdict %%s\\n" "$*"\n' > "$BIN_SRC"
chmod +x "$BIN_SRC"

PLAT_OS=SunOS; PLAT_ARCH=x86_64; PIN=1.2.3
run_install
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "supported: darwin/amd64" "$EVAL_ERR"; then
  good "unknown OS refuses with the supported matrix"
else
  bad "unknown OS exit $EVAL_STATUS ($(head -c 160 "$EVAL_ERR"))"
fi
PLAT_OS=Linux; PLAT_ARCH=riscv64
run_install
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "supported: amd64, arm64" "$EVAL_ERR"; then
  good "unknown arch refuses with the supported list"
else
  bad "unknown arch exit $EVAL_STATUS"
fi
PLAT_OS=linux; PLAT_ARCH=arm64; PIN=2.0.0; COSIGN_OVERRIDE="$FAKEBIN/cosign"
run_install
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "download failed" "$EVAL_ERR"; then
  good "an unpublished arch refuses the download"
else
  bad "wrong-arch download exit $EVAL_STATUS"
fi
PLAT_OS=linux; PLAT_ARCH=arm64; PIN=''
run_install "$REL/v1.2.3/verdict_1.2.3_linux_amd64.tar.gz"
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "is not the linux/arm64 build" "$EVAL_ERR"; then
  good "a local archive for another arch refuses"
else
  bad "local wrong-arch exit $EVAL_STATUS"
fi

fresh
PLAT_OS=linux; PLAT_ARCH=amd64; PIN=v3.0.0; COSIGN_OVERRIDE=''
run_install
if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq '"version":"3.0.0"' "$INSTALL_DIR/.verdict-installer" \
  && grep -Fq "file://$REL/v3.0.0/" "$CURL_LOG"; then
  good "VERDICT_VERSION=v3.0.0 pins the release"
else
  bad "version pin exit $EVAL_STATUS"
fi
fresh
PIN=latest; EFFECTIVE_URL="file://$REL/v9.9.9"
run_install
assert_exit "latest resolution succeeds" 0
assert_file_grep "latest pins the resolved version" "$INSTALL_DIR/.verdict-installer" '"version":"9.9.9"' "-F"
assert_file_grep "latest fetches the resolved release" "$CURL_LOG" "file://$REL/v9.9.9/" "-F"
assert_file_grep "latest walks the redirect" "$CURL_LOG" "file://$REL/latest" "-F"
assert_file_nogrep "latest never falls back to the API" "$CURL_LOG" 'api.github.com'
EFFECTIVE_URL=''

fresh
TEST_SHELL=/bin/zsh; PIN=1.2.3; NO_PATH=0
printf 'export KEEP_ME=1\n' > "$CASE_HOME/.zshrc"
run_install
assert_file_grep "a PATH-appending install ends with the skill next step" "$EVAL_OUT" \
  'Next: verdict skill install --detect' "-F"
run_install
assert_file_grep "a PATH-already-set install ends with the skill next step" "$EVAL_OUT" \
  'Next: verdict skill install --detect' "-F"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(grep -c -F '# verdict installer' "$CASE_HOME/.zshrc")" = 1 ] \
  && grep -Fq "$INSTALL_DIR" "$CASE_HOME/.zshrc" && grep -Fq 'export KEEP_ME=1' "$CASE_HOME/.zshrc"; then
  good "PATH gets one marked line and preserves existing config"
else
  bad "PATH append not idempotent or clobbered config"
fi
fresh
NO_PATH=1
run_install
if [ ! -e "$CASE_HOME/.zshrc" ]; then good "VERDICT_NO_MODIFY_PATH leaves shell config alone"; else bad "env PATH opt-out wrote .zshrc"; fi
fresh
NO_PATH=0
run_install --no-modify-path
if [ ! -e "$CASE_HOME/.zshrc" ]; then good "--no-modify-path leaves shell config alone"; else bad "flag PATH opt-out wrote .zshrc"; fi
NO_PATH=1

fresh
TEST_SHELL=/bin/sh; COSIGN_OVERRIDE="$FAKEBIN/cosign"
run_install "$REL/v1.2.3/verdict_1.2.3_linux_amd64.tar.gz"
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -s "$CURL_LOG" ] && cmp -s "$BIN_SRC" "$INSTALL_DIR/verdict"; then
  good "local-file install succeeds without any curl call"
else
  bad "local-file install exit $EVAL_STATUS (curl log: $(cat "$CURL_LOG"))"
fi
fresh
COSIGN_OVERRIDE="$CASE/no-cosign"
run_install "$REL/v1.2.3/verdict_1.2.3_linux_amd64.tar.gz"
if [ "$EVAL_STATUS" -eq 0 ] && grep -qi "WARNING: cosign not found" "$EVAL_ERR"; then
  good "missing cosign falls back to checksum-only with a loud warning"
else
  bad "checksum-only fallback exit $EVAL_STATUS"
fi
fresh
REQUIRE_SIG=1; COSIGN_OVERRIDE="$CASE/no-cosign"
run_install "$REL/v1.2.3/verdict_1.2.3_linux_amd64.tar.gz"
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "VERDICT_REQUIRE_SIGNATURE=1" "$EVAL_ERR"; then
  good "VERDICT_REQUIRE_SIGNATURE=1 fails closed without cosign"
else
  bad "require-signature exit $EVAL_STATUS"
fi
REQUIRE_SIG=0; COSIGN_OVERRIDE=''

fresh
PLAT_OS=linux; PLAT_ARCH=amd64; PIN=1.2.3
run_install
cp "$INSTALL_DIR/verdict" "$CASE/first-verdict"
run_install
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$CASE/first-verdict" "$INSTALL_DIR/verdict" \
  && [ "$(grep -c -F '"binary":"verdict"' "$INSTALL_DIR/.verdict-installer")" = 1 ] \
  && ! find "$INSTALL_DIR" -name '.verdict-stage-*' -print | matches .; then
  good "re-install is idempotent and leaves no staging dirs"
else
  bad "re-install not idempotent (exit $EVAL_STATUS)"
fi
echo foreign > "$INSTALL_DIR/notes.txt"
NO_PATH=0; TEST_SHELL=/bin/zsh; : > "$CASE_HOME/.zshrc"
run_install
run_install --uninstall --remove-path
assert_exit "uninstall succeeds" 0
if [ ! -e "$INSTALL_DIR/verdict" ] && [ ! -e "$INSTALL_DIR/.verdict-installer" ]; then
  good "uninstall removes the managed binary and receipt"
else
  bad "uninstall left a managed file behind"
fi
assert_file_grep "uninstall leaves the foreign file" "$INSTALL_DIR/notes.txt" "foreign"
assert_stream_nogrep "uninstall removes its marked PATH line" "$CASE_HOME/.zshrc" '# verdict installer' "-F"
mkdir -p "$INSTALL_DIR"; printf 'foreign\n' > "$INSTALL_DIR/verdict"
run_install --uninstall
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "refusing to remove" "$EVAL_ERR" \
  && [ -f "$INSTALL_DIR/verdict" ]; then
  good "uninstall refuses a foreign verdict without a receipt"
else
  bad "foreign uninstall exit $EVAL_STATUS"
fi
NO_PATH=1

if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across installer legs (offline)"
else
  bad "stub gh intercepted calls during installer legs"
fi
rm -rf "$CASE"

eval_end
