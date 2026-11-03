#!/bin/bash
# Verdict release chain, run by the Release workflow (publish-script) after a
# Version PR merges. Four idempotent stages, always in this order:
#
#   tag         create the bumped version's git tag and push that tag
#   goreleaser  build/sign/attest and create the GitHub Release
#   npm         publish the six platform packages, then the wrapper
#   tap         render the Homebrew formula and push it to the tap
#
# Every stage first asks whether its effect already exists and exits 0 when it
# does, so re-running the workflow after a mid-chain failure finishes the
# remaining stages instead of wedging on an existing tag, release, npm
# version, or tap commit. The one destructive case is a *partial* GitHub
# Release (an interrupted upload): that release is deleted and rebuilt, since
# GoReleaser cannot publish over it.
#
#   scripts/release.sh            all four stages (what changesets runs)
#   scripts/release.sh tag        only the tag stage, etc.
#
# `make release-dry-run` proves the artifact content without publishing; this
# script is the publishing half and never runs on PRs.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="$(node -p "require('./package.json').version")"
if [ -z "$VERSION" ]; then
  echo "release: package.json has no version" >&2
  exit 1
fi
TAG_PREFIX="$(node -p "require('./release/contract.json').tagPrefix")"
REPO="$(node -p "require('./release/contract.json').repo")"
TAP_REPO="$(node -p "require('./release/contract.json').formula.tap")"
# `brew install owner/name/verdict` resolves to github.com/owner/homebrew-name,
# so the formula must be pushed to the homebrew-prefixed repository.
TAP_GH_REPO="${TAP_REPO%%/*}/homebrew-${TAP_REPO#*/}"
# Path of the formula inside the (possibly multi-product) tap, e.g. Formula/verdict.rb.
TAP_FORMULA="$(node -p "require('./release/contract.json').formula.path")"
TAG="${TAG_PREFIX}${VERSION}"

TAP_DIR=""
cleanup() {
  if [ -n "$TAP_DIR" ]; then rm -rf "$TAP_DIR"; fi
}
trap cleanup EXIT

die() {
  echo "release: $*" >&2
  exit 1
}

# stage_tag — `changeset git-tag` already skips versions with a local or
# remote tag, so the remaining work is pushing a tag origin does not have.
# "Already done" = origin's tag points at HEAD.
stage_tag() {
  changeset git-tag
  local head local_commit remote_commit
  head="$(git rev-parse HEAD)"
  if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
    local_commit="$(git rev-parse "refs/tags/$TAG^{commit}")"
  else
    local_commit=""
  fi
  # Annotated tags need the peeled ref to compare commit SHAs; a lightweight
  # tag (pre-existing from an operator) only has the direct ref.
  remote_commit="$(git ls-remote --tags origin "refs/tags/$TAG^{}" | awk 'NR == 1 { print $1 }')"
  if [ -z "$remote_commit" ]; then
    remote_commit="$(git ls-remote --tags origin "refs/tags/$TAG" | awk 'NR == 1 { print $1 }')"
  fi
  if [ -n "$remote_commit" ]; then
    if [ -z "$local_commit" ]; then
      # A fresh checkout of a re-run: changesets skipped the local tag because
      # origin already had it, so fetch it before GoReleaser resolves it.
      git fetch --force origin "refs/tags/$TAG:refs/tags/$TAG"
      local_commit="$(git rev-parse "refs/tags/$TAG^{commit}")"
    fi
    [ "$local_commit" = "$remote_commit" ] ||
      die "tag $TAG is $remote_commit on origin but $local_commit locally; refusing to move it"
    [ "$remote_commit" = "$head" ] ||
      die "tag $TAG points at $remote_commit, not HEAD $head"
    echo "release: tag $TAG already on origin at $remote_commit; skipping push"
    return 0
  fi
  [ -n "$local_commit" ] || die "changeset git-tag did not create $TAG"
  [ "$local_commit" = "$head" ] ||
    die "tag $TAG points at $local_commit, not HEAD $head"
  git push origin "refs/tags/$TAG"
}

# archive_files — the six contract archive names for this version.
archive_files() {
  node - <<'EOF'
const c = require("./release/contract.json");
const v = require("./package.json").version;
for (const entry of c.archives) console.log(entry.file.replaceAll("{version}", v));
EOF
}

# expected_assets — every asset a complete GitHub Release carries.
expected_assets() {
  node - <<'EOF'
const c = require("./release/contract.json");
const v = require("./package.json").version;
for (const entry of c.archives) {
  const file = entry.file.replaceAll("{version}", v);
  console.log(file);
  console.log(file + c.sbomSuffix);
}
console.log(c.checksums);
console.log(c.signatureBundle);
console.log("install.sh");
console.log("install.ps1");
EOF
}

# RELEASE_ASSETS holds release_assets' output (a function cannot both print
# and abort on a real gh error inside a command substitution).
RELEASE_ASSETS=""
release_assets() {
  local out
  if out="$(gh api "repos/$REPO/releases/tags/$TAG" --jq '.assets[].name' 2>&1)"; then
    RELEASE_ASSETS="$out"
    return 0
  fi
  case "$out" in
    *"HTTP 404"* | *"Not Found"*) return 1 ;;
  esac
  die "gh api release $TAG failed: $out"
}

# detect_release — RELEASE_STATE is absent, complete, or partial; a partial
# release names its missing assets in RELEASE_MISSING.
RELEASE_STATE=""
RELEASE_MISSING=""
detect_release() {
  local name missing=""
  RELEASE_MISSING=""
  if ! release_assets; then
    RELEASE_STATE=absent
    return 0
  fi
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    if ! printf '%s\n' "$RELEASE_ASSETS" | grep -Fxq "$name"; then
      missing="$missing $name"
    fi
  done < <(expected_assets)
  if [ -n "$missing" ]; then
    RELEASE_STATE=partial
    RELEASE_MISSING="$missing"
  else
    RELEASE_STATE=complete
  fi
}

# stage_goreleaser — "already done" = the release exists with every contract
# asset. GoReleaser cannot republish over an existing release, so an
# incomplete one (interrupted upload) is deleted and rebuilt.
stage_goreleaser() {
  goreleaser check
  detect_release
  case "$RELEASE_STATE" in
    complete)
      echo "release: GitHub Release $TAG already has every contract asset; skipping GoReleaser"
      return 0
      ;;
    partial)
      echo "release: GitHub Release $TAG is missing:$RELEASE_MISSING; deleting the incomplete release and rebuilding"
      gh release delete "$TAG" --repo "$REPO" --yes
      ;;
    absent) ;;
  esac
  goreleaser release --clean
}

# npm_packages — the platform packages in publish order, wrapper last.
npm_packages() {
  node - <<'EOF'
const c = require("./release/contract.json");
for (const entry of c.archives) console.log(entry.npmPackage);
console.log(c.npm.wrapper);
EOF
}

# npm_published <name> — true when the registry already serves this version.
npm_published() {
  [ "$(npm view "$1@$VERSION" version 2>/dev/null || true)" = "$VERSION" ]
}

# ensure_release_archives — a re-run reaches npm after GoReleaser is
# skipped, so dist/ is empty; download the archive assets the release
# already carries instead of rebuilding them.
ensure_release_archives() {
  local name
  for name in $(archive_files); do
    if [ ! -f "dist/$name" ]; then
      echo "release: dist/ is missing $name; downloading the $TAG release assets"
      mkdir -p dist
      gh release download "$TAG" --repo "$REPO" --dir dist --clobber
      return 0
    fi
  done
}

# stage_npm — "already done" = every package version is on the registry.
# A partial publish stays ordered: publish-npm.mjs --skip-existing skips the
# versions already there and publishes the rest, wrapper last.
stage_npm() {
  local missing="" pkg
  for pkg in $(npm_packages); do
    npm_published "$pkg" || missing="$missing $pkg"
  done
  if [ -z "$missing" ]; then
    echo "release: npm already has every $VERSION package; skipping publish"
    return 0
  fi
  echo "release: npm is missing:$missing"
  ensure_release_archives
  # The wrapper bundles the TS client (subpath export ./client): build it
  # first so generate-npm.mjs finds clients/ts/dist.
  node_modules/.bin/tsc -p clients/ts/tsconfig.json
  node scripts/publish-npm.mjs --skip-existing
}

# ensure_checksums — the formula renders from dist/checksums.txt; fetch it
# from the completed release when a re-run skipped GoReleaser.
ensure_checksums() {
  local checksums
  checksums="$(node -p "require('./release/contract.json').checksums")"
  if [ ! -f "dist/$checksums" ]; then
    mkdir -p dist
    gh release download "$TAG" --repo "$REPO" --dir dist --pattern "$checksums" --clobber
  fi
}

# stage_tap — "already done" = the tap already carries this exact formula.
# The tap push stays opt-in (TAP_PUSH=1) until tap auth is settled.
stage_tap() {
  if [ "${TAP_PUSH:-0}" != "1" ]; then
    echo "release: tap push skipped (TAP_PUSH!=1); render the formula with make release-dry-run"
    return 0
  fi
  if [ -z "${TAP_GITHUB_TOKEN:-}" ]; then
    die "TAP_PUSH=1 needs TAP_GITHUB_TOKEN"
  fi
  ensure_checksums
  if [ ! -f dist/verdict.rb ]; then
    node scripts/generate-formula.mjs --version "$VERSION" --dist dist --out dist/verdict.rb
  fi
  # Grade the exact file the tap will carry; a formula audit would reject here
  # beats a broken `brew install verdict` after the push.
  sh scripts/brew-audit.sh dist/verdict.rb
  TAP_DIR="$(mktemp -d)"
  git clone --quiet "https://x-access-token:${TAP_GITHUB_TOKEN}@github.com/${TAP_GH_REPO}.git" "$TAP_DIR"
  if [ -f "$TAP_DIR/$TAP_FORMULA" ] && cmp -s dist/verdict.rb "$TAP_DIR/$TAP_FORMULA"; then
    echo "release: tap already carries the $VERSION formula; skipping push"
    return 0
  fi
  mkdir -p "$TAP_DIR/$(dirname "$TAP_FORMULA")"
  cp dist/verdict.rb "$TAP_DIR/$TAP_FORMULA"
  git -C "$TAP_DIR" add "$TAP_FORMULA"
  git -C "$TAP_DIR" -c user.name="verdict-release" \
    -c user.email="verdict-release@users.noreply.github.com" \
    commit -m "verdict ${VERSION}"
  # The tap is shared by every stainless-code product: another release may
  # push between our clone and our push. Rebase onto it and retry.
  attempt=1
  until git -C "$TAP_DIR" push origin HEAD:main; do
    if [ "$attempt" -ge 3 ]; then
      die "tap push to ${TAP_GH_REPO} still rejected after ${attempt} attempts"
    fi
    attempt=$((attempt + 1))
    git -C "$TAP_DIR" pull --rebase --quiet origin main
  done
}

run_all() {
  # Static check first: fail before tagging when the config is invalid.
  goreleaser check
  stage_tag
  stage_goreleaser
  stage_npm
  stage_tap
}

case "${1:-all}" in
  all) run_all ;;
  tag | goreleaser | npm | tap) "stage_$1" ;;
  *) die "unknown stage '$1' (want: tag, goreleaser, npm, tap, or no argument)" ;;
esac
