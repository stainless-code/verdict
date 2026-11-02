#!/bin/bash
# Verdict release chain, run by the Release workflow (publish-script) after a
# Version PR merges. Steps: git-tag the bumped version, push the tag,
# build/sign/attest with GoReleaser (which owns the GitHub Release), publish
# the npm tree (platforms first, wrapper last), render the Homebrew formula.
# `set -eu -o pipefail`: a failed step stops the chain. This script never runs
# on PRs; `make release-dry-run` proves the same steps without publishing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="$(node -p "require('./package.json').version")"
if [ -z "$VERSION" ]; then
  echo "release: package.json has no version" >&2
  exit 1
fi

# Static check first: fail before tagging when the config is invalid.
goreleaser check

# Tag the bumped version (publishes nothing) and push the tag so GoReleaser
# resolves {{.Version}} from it.
changeset git-tag
git push --follow-tags

# Build the six static binaries, sign checksums.txt (cosign keyless bundle),
# emit SPDX SBOMs, and create the GitHub Release. Needs GITHUB_TOKEN.
goreleaser release --clean

# Six platform packages, then the wrapper, with npm provenance (OIDC).
# The wrapper bundles the TS client (subpath export ./client): build it
# first so generate-npm.mjs finds clients/ts/dist (it skips the client
# when dist is absent, which must never happen in a real release).
node_modules/.bin/tsc -p clients/ts/tsconfig.json
node scripts/publish-npm.mjs

# Render the formula; push it to the tap only when explicitly enabled.
# Tap auth (PAT vs GitHub App vs deploy key) is still an open operator
# question, so the push stays opt-in: TAP_PUSH=1 with TAP_GITHUB_TOKEN set.
node scripts/generate-formula.mjs --version "$VERSION" --dist dist --out dist/verdict.rb
if [ "${TAP_PUSH:-0}" = "1" ]; then
  if [ -z "${TAP_GITHUB_TOKEN:-}" ]; then
    echo "release: TAP_PUSH=1 needs TAP_GITHUB_TOKEN" >&2
    exit 1
  fi
  TAP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TAP_DIR"' EXIT
  git clone "https://x-access-token:${TAP_GITHUB_TOKEN}@github.com/stainless-code/tap.git" "$TAP_DIR"
  cp dist/verdict.rb "$TAP_DIR/verdict.rb"
  git -C "$TAP_DIR" add verdict.rb
  git -C "$TAP_DIR" -c user.name="verdict-release" -c user.email="verdict-release@users.noreply.github.com" commit -m "verdict ${VERSION}"
  git -C "$TAP_DIR" push origin HEAD:main
else
  echo "release: tap push skipped (TAP_PUSH!=1); formula at dist/verdict.rb"
fi
