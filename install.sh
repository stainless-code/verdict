#!/bin/sh
set -eu

REPO_BASE='https://github.com/stainless-code/verdict/releases/download'
LATEST_URL='https://github.com/stainless-code/verdict/releases/latest'
CHECKSUMS='checksums.txt'
BUNDLE='checksums.txt.sigstore.json'
COMMIT='commit.txt'
COSIGN_IDENTITY='https://github.com/stainless-code/verdict/.github/workflows/release.yml@refs/heads/main'
# The SAN above is exact, not a pattern: cosign takes a regexp, and the
# identity must stay anchored on it so no other run can sign this artifact.
COSIGN_IDENTITY_RE='^https://github\.com/stainless-code/verdict/\.github/workflows/release\.yml@refs/heads/main$'
COSIGN_ISSUER='https://token.actions.githubusercontent.com'
# Only a push-triggered run may sign a release. The SHA pin below is the commit
# the tag points at, so a workflow_dispatch run of the same workflow at that
# commit would satisfy it; the trigger claim is what rules that run out.
COSIGN_TRIGGER='push'
MARKER='# verdict installer'
MODE=install; ARCHIVE=''; REMOVE_PATH=0; TMP=''; STAGE=''

die() { echo "install: $*" >&2; exit 1; }

next_step() {
  echo "Next: verdict skill install --detect   (adds the review skill to your coding agents)"
  echo "      none detected? name one: verdict skill install --agent <name>"
}

usage() {
  cat <<EOF
usage: install.sh [ARCHIVE] [--no-modify-path] [--uninstall [--remove-path]] [--help]

  (no args)         install the latest release for this OS/arch
  ARCHIVE           install a local verdict_<version>_<os>_<arch>.tar.gz
                    (sibling checksums.txt, signature bundle, and commit.txt)
  --uninstall       remove a binary installed by this script (no foreign files)
  --remove-path     with --uninstall, also remove the marked PATH line
  --no-modify-path  never edit shell config (same: VERDICT_NO_MODIFY_PATH=1)
  --help            this text
  VERDICT_VERSION (default latest), VERDICT_INSTALL (~/.verdict/bin),
  VERDICT_DOWNLOAD_URL (release base), VERDICT_REQUIRE_SIGNATURE=1,
  VERDICT_ARCHIVE_CACHE (optional archive cache; metadata is always refreshed),
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
    latest="$LATEST_URL"; [ "$base" = "$REPO_BASE" ] || latest="$base/latest"
    effective="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$latest")" \
      || die "could not resolve the latest release via $latest"
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
  COMMIT_PATH="$archive_dir/$COMMIT"
else
  archive_path="$TMP/$archive_name"
  CHECKSUMS_PATH="$TMP/$CHECKSUMS"; BUNDLE_PATH="$TMP/$BUNDLE"
  COMMIT_PATH="$TMP/$COMMIT"
  echo "install: downloading verdict $VERSION ($os/$arch)" >&2
  cached_archive=''
  if [ -n "${VERDICT_ARCHIVE_CACHE:-}" ]; then
    cached_archive="$VERDICT_ARCHIVE_CACHE/$archive_name"
  fi
  if [ -n "$cached_archive" ] && [ -f "$cached_archive" ]; then
    cp "$cached_archive" "$archive_path"
  else
    fetch_file "$base/v$VERSION/$archive_name" "$archive_path" \
      || die "download failed: $base/v$VERSION/$archive_name (is $os/$arch published?)"
  fi
  fetch_file "$base/v$VERSION/$CHECKSUMS" "$CHECKSUMS_PATH" \
    || die "download failed: $base/v$VERSION/$CHECKSUMS"
  fetch_file "$base/v$VERSION/$COMMIT" "$COMMIT_PATH" \
    || die "download failed: $base/v$VERSION/$COMMIT (release $VERSION publishes no build commit; refusing an unpinned install)"
fi

COSIGN=''
if [ -n "${VERDICT_COSIGN:-}" ]; then
  [ -x "$VERDICT_COSIGN" ] && COSIGN="$VERDICT_COSIGN"
else
  COSIGN="$(command -v cosign 2>/dev/null || true)"
fi
[ -f "$CHECKSUMS_PATH" ] || die "missing $CHECKSUMS beside the archive"
[ -f "$COMMIT_PATH" ] \
  || die "missing $COMMIT beside the archive: release $VERSION publishes no build commit, so this installer refuses an unpinned install"
commit_sha="$(tr -d '[:space:]' < "$COMMIT_PATH")"
case "$commit_sha" in
  *[!0-9a-fA-F]*) die "$COMMIT is not a commit SHA: $commit_sha" ;;
esac
[ "${#commit_sha}" -eq 40 ] \
  || die "$COMMIT is not a full 40-character commit SHA (got ${#commit_sha} characters): $commit_sha"
commit_sha="$(printf '%s' "$commit_sha" | tr 'A-F' 'a-f')"
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
    --certificate-oidc-issuer "$COSIGN_ISSUER" \
    --certificate-github-workflow-trigger "$COSIGN_TRIGGER" \
    --certificate-github-workflow-sha "$commit_sha" "$CHECKSUMS_PATH" >&2 \
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

tar -tzf "$archive_path" > "$TMP/members" 2>/dev/null || die "not a valid tar.gz archive: $archive_name"
bad="$(grep -Ev '^(verdict|LICENSE|README\.md|NOTICE\.md)$' "$TMP/members" || true)"
[ -z "$bad" ] || die "archive has unexpected member(s): $bad"
grep -qx verdict "$TMP/members" || die "archive does not contain the verdict binary"
if [ -z "$ARCHIVE" ] && [ -n "$cached_archive" ]; then
  mkdir -p "$VERDICT_ARCHIVE_CACHE"
  cp "$archive_path" "$cached_archive"
fi
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
reported="$("$INSTALL/verdict" --version 2>/dev/null)" || reported=''
[ -n "$reported" ] || die "installed $INSTALL/verdict but it does not answer --version; check the archive for this platform"
echo "install: installed verdict $VERSION to $INSTALL/verdict (SHA-256 verified; reports $reported)"

if [ "$NO_MODIFY_PATH" = 1 ]; then
  if [ "${VERDICT_NO_MODIFY_PATH:-0}" = 1 ]; then no_path_knob='VERDICT_NO_MODIFY_PATH'; else no_path_knob='--no-modify-path'; fi
  echo "install: PATH not modified ($no_path_knob). Add: export PATH=\"$INSTALL:\$PATH\""
  next_step
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
next_step
exit 0
