#!/usr/bin/env bash
# Case upgrade: `verdict upgrade` + `verdict uninstall` (ticket 11).
#
# Grades (all hermetic; a local loopback server serves locally built
# archives/checksums, fake manager binaries log argv, fake HOME/install dirs
# pin every channel):
#   U1  installer-managed pinned self-replace: verified archive, cosign
#       identity/issuer argv, SHA-256, --version smoke, receipt update, and
#       the replaced binary answers --version.
#   U2  latest resolves through the release redirect (never the API).
#   U3  checksum mismatch refuses and leaves the old binary + receipt.
#   U4  --dry-run prints the channel/action, makes no request, touches nothing.
#   U5  npm delegation: exact argv, streamed output, exit code propagated,
#       typed manager-failed envelope; no prompt (/dev/null stdin).
#   U6  refusals are typed with the exact command: project-local, skill copy,
#       unidentifiable.
#   U7  uninstall removes only installer-managed files (+ --remove-path marker),
#       dry-run touches nothing, foreign channels refuse with the owner command.
#   U8  signature policy: checksum-only warns loudly, VERDICT_REQUIRE_SIGNATURE=1
#       fails closed without cosign.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

CASE="$EVAL_SANDBOX/upgrade"
REL="$CASE/release"
FAKEBIN="$CASE/fakebin"
CLEANBIN="$CASE/cleanbin"
INSTALL_DIR="$CASE/install"
CASE_HOME="$CASE/home"
NPM_LOG="$CASE/npm.log"
COSIGN_LOG="$CASE/cosign.log"
REQ_LOG="$CASE/requests.log"
BIN_SRC="$CASE/fake-verdict"
VERSION=1.2.3
mkdir -p "$REL/v$VERSION" "$FAKEBIN" "$CLEANBIN" "$CASE_HOME"
: > "$NPM_LOG"
: > "$COSIGN_LOG"
: > "$REQ_LOG"

# The fake released binary answers the smoke test and otherwise is inert.
cat > "$BIN_SRC" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then printf '1.2.3\n'; exit 0; fi
printf 'fake-verdict %s\n' "$*"
EOF
chmod +x "$BIN_SRC"

# Contract platform of the machine running the eval.
os="$(uname -s)"
case "$os" in Darwin) os=darwin ;; Linux) os=linux ;; *) bad "unsupported eval OS $os"; echo "case upgrade: FAIL ($fails)"; exit 1 ;; esac
machine="$(uname -m)"
case "$machine" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) bad "unsupported eval arch $machine"; echo "case upgrade: FAIL ($fails)"; exit 1 ;; esac
archive="$(jq -r --arg v "$VERSION" --arg os "$os" --arg arch "$arch" \
  '.archives[] | select(.goos == $os and .goarch == $arch) | .file | sub("\\{version\\}"; $v)' \
  "$EVAL_ROOT/release/contract.json")"
[ -n "$archive" ] || { bad "contract has no archive for $os/$arch"; archive="verdict_${VERSION}_${os}_${arch}.tar.gz"; }

mkdir -p "$REL/v$VERSION/payload"
cp "$BIN_SRC" "$REL/v$VERSION/payload/verdict"
: > "$REL/v$VERSION/payload/LICENSE"
: > "$REL/v$VERSION/payload/README.md"
(cd "$REL/v$VERSION/payload" && COPYFILE_DISABLE=1 tar -czf "../$archive" verdict LICENSE README.md)
rm -rf "$REL/v$VERSION/payload"
(cd "$REL/v$VERSION" && shasum -a 256 "$archive" > checksums.txt)
printf '{}\n' > "$REL/v$VERSION/checksums.txt.sigstore.json"

# Fake manager + cosign.
cat > "$FAKEBIN/npm" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "${FAKE_NPM_LOG:-/dev/null}"
printf 'npm-stub %s\n' "$*"
exit "${FAKE_NPM_STATUS:-0}"
EOF
cat > "$FAKEBIN/cosign" <<'EOF'
#!/bin/sh
for a in "$@"; do printf '%s ' "$a" >> "${FAKE_COSIGN_LOG:-/dev/null}"; done
printf '\n' >> "${FAKE_COSIGN_LOG:-/dev/null}"
exit "${FAKE_COSIGN_STATUS:-0}"
EOF
chmod +x "$FAKEBIN/npm" "$FAKEBIN/cosign"

# Local release server (python3 stdlib). Without python3 the server-backed
# legs are skipped; every channel/delegation/refusal leg still runs.
SERVER_PID=''
PORT=''
cleanup() {
  [ -z "$SERVER_PID" ] || kill "$SERVER_PID" 2>/dev/null || true
}
trap cleanup EXIT
if command -v python3 >/dev/null 2>&1; then
  PORT_FILE="$CASE/port"
  : > "$PORT_FILE"
  python3 - "$REL" "$PORT_FILE" "$REQ_LOG" <<'PY' &
import http.server, os, socketserver, sys
root, port_file, log_file = sys.argv[1], sys.argv[2], sys.argv[3]
os.chdir(root)
class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        with open(log_file, "a") as log:
            log.write(self.path + "\n")
        if self.path == "/latest":
            self.send_response(302)
            self.send_header("Location", "/v1.2.3")
            self.end_headers()
            return
        super().do_GET()
    def log_message(self, *args):
        pass
with socketserver.TCPServer(("127.0.0.1", 0), Handler) as server:
    with open(port_file, "w") as out:
        out.write(str(server.server_address[1]))
    server.serve_forever()
PY
  SERVER_PID=$!
  for _ in $(seq 1 100); do
    [ -s "$PORT_FILE" ] && break
    sleep 0.05
  done
  PORT="$(cat "$PORT_FILE" 2>/dev/null || true)"
  [ -n "$PORT" ] || { bad "local release server did not report a port"; SERVER_PID=''; }
else
  echo "  skip: python3 unavailable; server-backed legs skipped"
fi
BASE_URL=""
[ -z "$PORT" ] || BASE_URL="http://127.0.0.1:$PORT"

# run_cli <binary> <download-url-or-empty> [args...]
run_cli() {
  local bin="$1" base="$2"
  shift 2
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    HOME="$CASE_HOME" SHELL=/bin/zsh PATH="$FAKEBIN:$PATH" \
      VERDICT_INSTALL="$INSTALL_DIR" VERDICT_DOWNLOAD_URL="$base" \
      VERDICT_COSIGN="$FAKEBIN/cosign" VERDICT_REQUIRE_SIGNATURE=0 \
      FAKE_NPM_LOG="$NPM_LOG" FAKE_NPM_STATUS="${FAKE_NPM_STATUS:-0}" \
      FAKE_COSIGN_LOG="$COSIGN_LOG" FAKE_COSIGN_STATUS="${FAKE_COSIGN_STATUS:-0}" \
      "$bin" "$@" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  EVAL_STATUS=$?
  set -e
}

# fresh_install installs the eval binary as an installer-managed copy.
fresh_install() {
  rm -rf "$INSTALL_DIR"
  mkdir -p "$INSTALL_DIR"
  cp "$EVAL_CLI" "$INSTALL_DIR/verdict"
  printf '{"binary":"verdict","version":"0.2.0","installed_by":"install.sh"}\n' > "$INSTALL_DIR/.verdict-installer"
}

digest() { shasum -a 256 "$1" | awk '{print $1}'; }
envelope_code() { jq -r '.error.code // empty' "$EVAL_OUT" 2>/dev/null; }
envelope_data() { jq -r "$1" "$EVAL_OUT" 2>/dev/null; }

# --- U1: pinned installer self-replace ---
if [ -n "$PORT" ]; then
  fresh_install
  before="$(digest "$INSTALL_DIR/verdict")"
  run_cli "$INSTALL_DIR/verdict" "$BASE_URL" upgrade --version "v$VERSION"
  if [ "$EVAL_STATUS" -eq 0 ] \
    && cmp -s "$BIN_SRC" "$INSTALL_DIR/verdict" \
    && [ -x "$INSTALL_DIR/verdict" ] \
    && grep -Fq '"version":"1.2.3"' "$INSTALL_DIR/.verdict-installer" \
    && grep -Fq '"installed_by":"install.sh"' "$INSTALL_DIR/.verdict-installer"; then
    good "U1 installer self-replace swaps in the verified binary and updates the receipt"
  else
    bad "U1 self-replace exit $EVAL_STATUS ($(head -c 240 "$EVAL_ERR"))"
  fi
  if [ "$(digest "$INSTALL_DIR/verdict")" != "$before" ] \
    && "$INSTALL_DIR/verdict" --version | grep -qx "$VERSION"; then
    good "U1 the replaced binary answers --version"
  else
    bad "U1 the replaced binary is unchanged or mis-versioned"
  fi
  if grep -Fq -- '--certificate-identity-regexp' "$COSIGN_LOG" \
    && grep -Fq 'stainless-code/verdict/' "$COSIGN_LOG" \
    && grep -Fq -- '--certificate-oidc-issuer' "$COSIGN_LOG" \
    && grep -Fq "$(jq -r .cosign.issuer "$EVAL_ROOT/release/contract.json")" "$COSIGN_LOG"; then
    good "U1 cosign verifies the contract identity/issuer"
  else
    bad "U1 cosign argv missing the contract identity/issuer: $(cat "$COSIGN_LOG")"
  fi
  if find "$INSTALL_DIR" \( -name '.verdict-stage-*' -o -name 'previous-*' \) | grep -q .; then
    bad "U1 self-replace left staging artifacts"
  else
    good "U1 self-replace cleans its staging"
  fi

  # --- U2: latest through the redirect ---
  fresh_install
  run_cli "$INSTALL_DIR/verdict" "$BASE_URL" upgrade
  if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq '"version":"1.2.3"' "$INSTALL_DIR/.verdict-installer" \
    && grep -qx '/latest' "$REQ_LOG"; then
    good "U2 latest resolves through the release redirect"
  else
    bad "U2 latest exit $EVAL_STATUS (requests: $(tr '\n' ' ' <"$REQ_LOG"))"
  fi

  # --- U3: checksum mismatch leaves the old binary ---
  fresh_install
  cp "$INSTALL_DIR/verdict" "$CASE/old-verdict"
  cp "$REL/v$VERSION/checksums.txt" "$CASE/good-checksums.txt"
  printf '%064d  %s\n' 0 "$archive" > "$REL/v$VERSION/checksums.txt"
  run_cli "$INSTALL_DIR/verdict" "$BASE_URL" upgrade --version "$VERSION"
  cp "$CASE/good-checksums.txt" "$REL/v$VERSION/checksums.txt"
  if [ "$EVAL_STATUS" -ne 0 ] && grep -q "SHA-256 mismatch" "$EVAL_ERR" \
    && cmp -s "$CASE/old-verdict" "$INSTALL_DIR/verdict" \
    && grep -Fq '"version":"0.2.0"' "$INSTALL_DIR/.verdict-installer"; then
    good "U3 checksum mismatch refuses and leaves the old binary + receipt"
  else
    bad "U3 checksum mismatch exit $EVAL_STATUS (old binary changed?)"
  fi

  # --- U4: dry-run touches nothing and makes no request ---
  fresh_install
  requests_before="$(wc -l <"$REQ_LOG" | tr -d ' ')"
  cp "$INSTALL_DIR/verdict" "$CASE/dry-verdict"
  run_cli "$INSTALL_DIR/verdict" "$BASE_URL" upgrade --version "$VERSION" --dry-run --json
  requests_after="$(wc -l <"$REQ_LOG" | tr -d ' ')"
  if [ "$EVAL_STATUS" -eq 0 ] && [ "$requests_before" = "$requests_after" ] \
    && cmp -s "$CASE/dry-verdict" "$INSTALL_DIR/verdict" \
    && [ "$(envelope_data .data.channel)" = "installer-managed" ] \
    && [ "$(envelope_data .data.action)" = "self-replace" ] \
    && [ "$(envelope_data .data.dry_run)" = "true" ]; then
    good "U4 dry-run plans the installer channel without a request or a write"
  else
    bad "U4 dry-run exit $EVAL_STATUS (requests $requests_before/$requests_after, data $(envelope_data .data.channel)/$(envelope_data .data.action))"
  fi

  # --- U8: signature policy ---
  fresh_install
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    HOME="$CASE_HOME" PATH="$CLEANBIN" VERDICT_INSTALL="$INSTALL_DIR" \
      VERDICT_DOWNLOAD_URL="$BASE_URL" VERDICT_REQUIRE_SIGNATURE=1 \
      "$INSTALL_DIR/verdict" upgrade --version "$VERSION" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  st=$?
  set -e
  if [ "$st" -ne 0 ] && grep -q "VERDICT_REQUIRE_SIGNATURE=1" "$EVAL_ERR"; then
    good "U8 VERDICT_REQUIRE_SIGNATURE=1 fails closed without cosign"
  else
    bad "U8 require-signature exit $st"
  fi
  fresh_install
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    HOME="$CASE_HOME" PATH="$CLEANBIN" VERDICT_INSTALL="$INSTALL_DIR" \
      VERDICT_DOWNLOAD_URL="$BASE_URL" \
      "$INSTALL_DIR/verdict" upgrade --version "$VERSION" </dev/null >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  st=$?
  set -e
  if [ "$st" -eq 0 ] && grep -q "WARNING: cosign not found" "$EVAL_ERR"; then
    good "U8 missing cosign falls back to checksum-only with a loud warning"
  else
    bad "U8 checksum-only fallback exit $st"
  fi
fi

# --- U5: npm delegation acts, streams, and propagates the exit code ---
NPM_ROOT="$CASE/home/.npm-global/lib/node_modules/@stainless-code/verdict/bin"
mkdir -p "$NPM_ROOT"
cp "$EVAL_CLI" "$NPM_ROOT/verdict"
: > "$NPM_LOG"
FAKE_NPM_STATUS=7
run_cli "$NPM_ROOT/verdict" "" upgrade --version 9.9.9 --json
FAKE_NPM_STATUS=0
if [ "$EVAL_STATUS" -eq 7 ] && grep -qx 'i -g @stainless-code/verdict@9.9.9' "$NPM_LOG" \
  && grep -q "npm-stub i -g @stainless-code/verdict@9.9.9" "$EVAL_ERR" \
  && [ "$(envelope_code)" = "manager-failed" ]; then
  good "U5 npm delegation runs the exact argv, streams output, propagates exit 7"
else
  bad "U5 npm delegation exit $EVAL_STATUS (argv: $(cat "$NPM_LOG"), code $(envelope_code))"
fi
if [ -s "$EVAL_OUT" ] && jq -e '.schema_version == "1" and .verb == "upgrade" and .error.code == "manager-failed"' "$EVAL_OUT" >/dev/null 2>&1; then
  good "U5 --json prints one manager-failed envelope"
else
  bad "U5 --json envelope missing or malformed: $(head -c 160 "$EVAL_OUT")"
fi
: > "$NPM_LOG"
run_cli "$NPM_ROOT/verdict" "" upgrade
if [ "$EVAL_STATUS" -eq 0 ] && grep -qx 'i -g @stainless-code/verdict@latest' "$NPM_LOG"; then
  good "U5 npm delegation defaults to @latest and acts without a prompt"
else
  bad "U5 npm default update exit $EVAL_STATUS (argv: $(cat "$NPM_LOG"))"
fi

# --- U6: typed refusals with the exact command ---
PROJECT_ROOT="$CASE/project"
mkdir -p "$PROJECT_ROOT/node_modules/@stainless-code/verdict/bin"
printf '{"name":"demo"}\n' > "$PROJECT_ROOT/package.json"
cp "$EVAL_CLI" "$PROJECT_ROOT/node_modules/@stainless-code/verdict/bin/verdict"
run_cli "$PROJECT_ROOT/node_modules/@stainless-code/verdict/bin/verdict" "" upgrade --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(envelope_code)" = "upgrade-project-local" ] \
  && jq -r '.error.message' "$EVAL_OUT" | grep -Fq 'npm install @stainless-code/verdict@latest'; then
  good "U6 project-local refuses typed with the manifest command"
else
  bad "U6 project-local exit $EVAL_STATUS code $(envelope_code)"
fi
SKILL_ROOT="$CASE_HOME/.agents/skills/verdict/bin"
mkdir -p "$SKILL_ROOT"
cp "$EVAL_CLI" "$SKILL_ROOT/verdict"
run_cli "$SKILL_ROOT/verdict" "" upgrade --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(envelope_code)" = "upgrade-skill-copy" ] \
  && jq -r '.error.message' "$EVAL_OUT" | grep -Fq 'npx skills update'; then
  good "U6 skill copy refuses typed at the skills update flow"
else
  bad "U6 skill copy exit $EVAL_STATUS code $(envelope_code)"
fi
mkdir -p "$CASE/random"
cp "$EVAL_CLI" "$CASE/random/verdict"
run_cli "$CASE/random/verdict" "" upgrade --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(envelope_code)" = "upgrade-unidentified" ] \
  && jq -r '.error.message' "$EVAL_OUT" | grep -Fq 'install.sh'; then
  good "U6 unidentifiable refuses typed with the installer command"
else
  bad "U6 unidentifiable exit $EVAL_STATUS code $(envelope_code)"
fi

# --- U7: uninstall ---
fresh_install
printf 'export KEEP_ME=1\nexport PATH="%s:$PATH" # verdict installer\n' "$INSTALL_DIR" > "$CASE_HOME/.zshrc"
run_cli "$INSTALL_DIR/verdict" "" uninstall --remove-path
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -e "$INSTALL_DIR/verdict" ] && [ ! -e "$INSTALL_DIR/.verdict-installer" ] \
  && grep -q 'KEEP_ME=1' "$CASE_HOME/.zshrc" && ! grep -q '# verdict installer' "$CASE_HOME/.zshrc"; then
  good "U7 uninstall removes managed files + its marked PATH line, keeps foreign config"
else
  bad "U7 uninstall exit $EVAL_STATUS"
fi
fresh_install
printf 'export PATH="%s:$PATH" # verdict installer\n' "$INSTALL_DIR" > "$CASE_HOME/.zshrc"
run_cli "$INSTALL_DIR/verdict" "" uninstall --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ -e "$INSTALL_DIR/verdict" ] \
  && [ "$(envelope_data .data.dry_run)" = "true" ] \
  && grep -q '# verdict installer' "$CASE_HOME/.zshrc"; then
  good "U7 uninstall --dry-run touches nothing"
else
  bad "U7 uninstall dry-run exit $EVAL_STATUS"
fi
run_cli "$NPM_ROOT/verdict" "" uninstall --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(envelope_code)" = "uninstall-foreign" ] \
  && jq -r '.error.message' "$EVAL_OUT" | grep -Fq 'npm uninstall -g @stainless-code/verdict'; then
  good "U7 foreign npm install refuses with the owner command"
else
  bad "U7 foreign uninstall exit $EVAL_STATUS code $(envelope_code)"
fi

if [ "$fails" -gt 0 ]; then
  echo "case upgrade: FAIL ($fails)"
  exit 1
fi
echo "case upgrade: PASS"
