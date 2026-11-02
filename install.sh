#!/bin/sh
# verdict binary installer (macOS/Linux): download one release archive, verify
# the cosign signature over checksums.txt (when cosign is present) and the
# archive SHA-256, extract only `verdict`, and atomically replace the target.
# No root, no daemon, no eval of release metadata. Windows: use install.ps1.
#
#   curl -fsSL .../install.sh | sh
#   sh install.sh ./verdict_1.2.3_linux_amd64.tar.gz   # offline/local
#   sh install.sh --uninstall [--remove-path]
set -eu

REPO_BASE='https://github.com/stainless-code/verdict/releases/download'
CHECKSUMS='checksums.txt'
BUNDLE='checksums.txt.sigstore.json'
COSIGN_IDENTITY='https://github.com/stainless-code/verdict/.github/workflows/release.yml@refs/tags/v*'
# The contract identity above is a tag wildcard; cosign takes a regexp.
COSIGN_IDENTITY_RE='^https://github\.com/stainless-code/verdict/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+.*$'
COSIGN_ISSUER='https://token.actions.githubusercontent.com'
MARKER='# verdict installer'
MODE=install; ARCHIVE=''; REMOVE_PATH=0; TMP=''; STAGE=''

die() { echo "install: $*" >&2; exit 1; }

usage() {
  cat <<EOF
usage: install.sh [ARCHIVE] [--no-modify-path] [--uninstall [--remove-path]] [--help]

  (no args)         install the latest release for this OS/arch
  ARCHIVE           install a local verdict_<version>_<os>_<arch>.tar.gz
                    (sibling checksums.txt and optional signature bundle)
  --uninstall       remove a binary installed by this script (no foreign files)
  --remove-path     with --uninstall, also remove the marked PATH line
  --no-modify-path  never edit shell config (same: VERDICT_NO_MODIFY_PATH=1)
  --help            this text
  VERDICT_VERSION (default latest), VERDICT_INSTALL (~/.verdict/bin),
  VERDICT_DOWNLOAD_URL (release base), VERDICT_REQUIRE_SIGNATURE=1,
  VERDICT_COSIGN (cosign override). cosign identity: $COSIGN_IDENTITY
EOF
}

[ -z "${HOME:-}" ] && die "HOME is unset; set VERDICT_INSTALL explicitly"
INSTALL="${VERDICT_INSTALL:-$HOME/.verdict/bin}"
case "$INSTALL" in
  /*) ;;
  *) die "VERDICT_INSTALL must be an absolute path (got $INSTALL)" ;;
esac
[ "$INSTALL" = / ] && die "refusing to install into /"
RECEIPT="$INSTALL/.verdict-installer"
NO_MODIFY_PATH=0; [ "${VERDICT_NO_MODIFY_PATH:-0}" = 1 ] && NO_MODIFY_PATH=1
REQUIRE_SIGNATURE=0; [ "${VERDICT_REQUIRE_SIGNATURE:-0}" = 1 ] && REQUIRE_SIGNATURE=1

while [ $# -gt 0 ]; do
  case "$1" in
    --no-modify-path) NO_MODIFY_PATH=1 ;;
    --uninstall) MODE=uninstall ;;
    --remove-path) REMOVE_PATH=1 ;;
    -h|--help) usage; exit 0 ;;
    -*) die "unknown flag: $1 (see --help)" ;;
    *) [ -z "$ARCHIVE" ] || die "unexpected argument: $1"; ARCHIVE="$1" ;;
  esac
  shift
done

profile_path() {
  case "$(basename "${SHELL:-sh}")" in
    zsh) PROFILE="${ZDOTDIR:-$HOME}/.zshrc" ;;
    bash) if [ -f "$HOME/.bashrc" ]; then PROFILE="$HOME/.bashrc"
          elif [ -f "$HOME/.bash_profile" ]; then PROFILE="$HOME/.bash_profile"
          else PROFILE="$HOME/.bashrc"; fi ;;
    *) PROFILE="$HOME/.profile" ;;
  esac
}

remove_marked_line() {
  [ -f "$PROFILE" ] || return 0
  grep -Fv "$MARKER" "$PROFILE" > "$PROFILE.verdict-tmp" || true
  cat "$PROFILE.verdict-tmp" > "$PROFILE"; rm -f "$PROFILE.verdict-tmp"
}

uninstall() {
  profile_path
  if [ ! -f "$RECEIPT" ]; then
    [ -e "$INSTALL/verdict" ] && die "refusing to remove $INSTALL/verdict: no installer receipt (not installed by this script)"
    echo "install: nothing to uninstall in $INSTALL"
  else
    rm -f "$INSTALL/verdict" "$RECEIPT"
    rmdir "$INSTALL" 2>/dev/null || true
    echo "install: removed $INSTALL/verdict"
  fi
  [ "$REMOVE_PATH" = 1 ] && { remove_marked_line; echo "install: removed the marked PATH line from $PROFILE"; }
  return 0
}

[ "$MODE" = uninstall ] && { uninstall; exit 0; }
[ "$REMOVE_PATH" = 1 ] && die "--remove-path is only valid with --uninstall"

# --- platform detection (Rosetta 2 maps an arm64 shell to x86_64) ---
os="$(uname -s)"
case "$os" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW*|MSYS*|CYGWIN*) die "Windows is not supported here; run install.ps1" ;;
  *) die "unsupported OS: $os (supported: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64; Windows: install.ps1)" ;;
esac
machine="$(uname -m)"
case "$machine" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $machine on $os (supported: amd64, arm64; Windows: install.ps1)" ;;
esac
ROSETTA=0
if [ "$os" = darwin ] && [ "$arch" = amd64 ] \
  && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
  arch=arm64; ROSETTA=1
fi
[ "$ROSETTA" = 1 ] && echo "install: running under Rosetta 2; installing the native darwin/arm64 build" >&2

if command -v shasum >/dev/null 2>&1; then SHA_CMD='shasum -a 256'
elif command -v sha256sum >/dev/null 2>&1; then SHA_CMD='sha256sum'
else die "need shasum or sha256sum to verify the download"; fi

# fetch_file URL DEST: curl handles both https and file:// mirrors.
fetch_file() { curl -fsSL "$1" -o "$2"; }

if [ -n "$ARCHIVE" ]; then
  [ -f "$ARCHIVE" ] || die "no such archive: $ARCHIVE"
  archive_dir="$(cd "$(dirname "$ARCHIVE")" && pwd)"
  archive_name="$(basename "$ARCHIVE")"
  case "$archive_name" in
    verdict_*.tar.gz) ;;
    *) die "local archives must be named verdict_<version>_<os>_<arch>.tar.gz (got $archive_name)" ;;
  esac
  VERSION="${archive_name#verdict_}"; VERSION="${VERSION%%_*}"
  expected="verdict_${VERSION}_${os}_${arch}.tar.gz"
  [ "$archive_name" = "$expected" ] || die "$archive_name is not the $os/$arch build (expected $expected)"
else
  command -v curl >/dev/null 2>&1 || die "network installs need curl (or pass a local archive)"
  base="${VERDICT_DOWNLOAD_URL:-$REPO_BASE}"; base="${base%/}"
  VERSION="${VERDICT_VERSION:-latest}"
  if [ "$VERSION" = latest ]; then
    effective="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$base/latest")" \
      || die "could not resolve the latest release via $base/latest"
    case "$effective" in
      */v*) VERSION="${effective##*/v}" ;;
      *) die "could not parse a version from the latest release redirect: $effective" ;;
    esac
  fi
  VERSION="${VERSION#v}"
  case "$VERSION" in
    ''|*[!0-9A-Za-z.+-]*) die "bad version: ${VERDICT_VERSION} (use X.Y.Z or vX.Y.Z)" ;;
  esac
  archive_name="verdict_${VERSION}_${os}_${arch}.tar.gz"
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/verdict-install.XXXXXX")" || die "cannot create a temp dir"
cleanup() { [ -z "$STAGE" ] || rm -rf "$STAGE"; [ -z "$TMP" ] || rm -rf "$TMP"; }
trap cleanup 0
trap 'cleanup; exit 1' 1 2 15

if [ -n "$ARCHIVE" ]; then
  archive_path="$archive_dir/$archive_name"
  CHECKSUMS_PATH="$archive_dir/$CHECKSUMS"; BUNDLE_PATH="$archive_dir/$BUNDLE"
else
  archive_path="$TMP/$archive_name"
  CHECKSUMS_PATH="$TMP/$CHECKSUMS"; BUNDLE_PATH="$TMP/$BUNDLE"
  echo "install: downloading verdict $VERSION ($os/$arch)" >&2
  fetch_file "$base/v$VERSION/$archive_name" "$archive_path" \
    || die "download failed: $base/v$VERSION/$archive_name (is $os/$arch published?)"
  fetch_file "$base/v$VERSION/$CHECKSUMS" "$CHECKSUMS_PATH" \
    || die "download failed: $base/v$VERSION/$CHECKSUMS"
fi

# --- signature: verify checksums.txt before trusting any hash from it ---
COSIGN=''
if [ -n "${VERDICT_COSIGN:-}" ]; then
  [ -x "$VERDICT_COSIGN" ] && COSIGN="$VERDICT_COSIGN"
else
  COSIGN="$(command -v cosign 2>/dev/null || true)"
fi
[ -f "$CHECKSUMS_PATH" ] || die "missing $CHECKSUMS beside the archive"
bundle_ok=0; [ -f "$BUNDLE_PATH" ] && bundle_ok=1
if [ "$bundle_ok" = 0 ] && [ -n "$ARCHIVE" ] && [ -f "$archive_dir/$BUNDLE" ]; then bundle_ok=1; fi
if [ "$bundle_ok" = 0 ] && [ -z "$ARCHIVE" ]; then
  fetch_file "$base/v$VERSION/$BUNDLE" "$BUNDLE_PATH" && bundle_ok=1 || true
fi
if [ -n "$COSIGN" ]; then
  [ "$bundle_ok" = 1 ] || die "cosign is installed but $BUNDLE is missing; refusing an unverified install"
  echo "install: verifying the sigstore bundle for $CHECKSUMS" >&2
  "$COSIGN" verify-blob --bundle "$BUNDLE_PATH" \
    --certificate-identity-regexp "$COSIGN_IDENTITY_RE" \
    --certificate-oidc-issuer "$COSIGN_ISSUER" "$CHECKSUMS_PATH" >&2 \
    || die "signature verification failed; nothing installed"
elif [ "$REQUIRE_SIGNATURE" = 1 ]; then
  die "VERDICT_REQUIRE_SIGNATURE=1 but cosign is not installed; nothing installed"
else
  echo "install: WARNING: cosign not found; verifying SHA-256 only (VERDICT_REQUIRE_SIGNATURE=1 fails closed)." >&2
fi

expected="$(awk -v f="$archive_name" '$2 == f { print $1; exit }' "$CHECKSUMS_PATH")"
[ -n "$expected" ] || die "$CHECKSUMS has no entry for $archive_name"
actual="$($SHA_CMD "$archive_path" | awk '{print $1}')"
[ "$actual" = "$expected" ] \
  || die "SHA-256 mismatch for $archive_name (expected $expected, got $actual); nothing installed"

# --- extract only the expected member; reject archives carrying anything else ---
tar -tzf "$archive_path" > "$TMP/members" 2>/dev/null || die "not a valid tar.gz archive: $archive_name"
bad="$(grep -Ev '^(verdict|LICENSE|README\.md)$' "$TMP/members" || true)"
[ -z "$bad" ] || die "archive has unexpected member(s): $bad"
grep -qx verdict "$TMP/members" || die "archive does not contain the verdict binary"
mkdir -p "$INSTALL" || die "cannot create $INSTALL"
[ -w "$INSTALL" ] || die "$INSTALL is not writable (set VERDICT_INSTALL; no root needed)"
STAGE="$(mktemp -d "$INSTALL/.verdict-stage.XXXXXX")" || die "cannot stage in $INSTALL"
tar -xzf "$archive_path" -C "$STAGE" verdict 2>/dev/null || die "could not extract verdict from $archive_name"
[ -f "$STAGE/verdict" ] && [ ! -L "$STAGE/verdict" ] || die "extracted verdict is not a regular file"
chmod 0755 "$STAGE/verdict"
printf '%s\n' "{\"binary\":\"verdict\",\"version\":\"$VERSION\",\"installed_by\":\"install.sh\"}" > "$STAGE/receipt"
sync 2>/dev/null || true
mv -f "$STAGE/verdict" "$INSTALL/verdict" || die "atomic replace failed; the old binary is untouched"
mv -f "$STAGE/receipt" "$RECEIPT" || die "installed the binary but could not write $RECEIPT"
rm -rf "$STAGE"; STAGE=''
echo "install: installed verdict $VERSION to $INSTALL/verdict (SHA-256 verified)"

if [ "$NO_MODIFY_PATH" = 1 ]; then
  echo "install: PATH not modified (VERDICT_NO_MODIFY_PATH). Add: export PATH=\"$INSTALL:\$PATH\""
  exit 0
fi
case ":$PATH:" in
  *":$INSTALL:"*) echo "install: $INSTALL is already on PATH" ;;
  *)
    profile_path
    if [ -f "$PROFILE" ] && grep -Fq "$INSTALL" "$PROFILE"; then
      echo "install: $PROFILE already mentions $INSTALL"
    else
      quoted="$(printf '%s' "$INSTALL" | sed -e 's/[\\"$`]/\\&/g')"
      printf '%s\n' "export PATH=\"$quoted:\$PATH\" $MARKER" >> "$PROFILE"
      echo "install: appended $INSTALL to PATH in $PROFILE (restart your shell)"
    fi ;;
esac
exit 0
