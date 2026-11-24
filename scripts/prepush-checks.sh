#!/bin/sh
# The Go bar CI runs, in full: build, vet, test, deadcode, cross, pinned lint.
# `hooks/pre-push` runs it when the pushed commit is not already stamped, and
# `make verify-stamp` runs it before recording the stamp, so the two cannot
# drift. Run from the checkout root. Exits non-zero when any gate fails.
set -eu

if ! command -v go >/dev/null 2>&1; then
  echo "pre-push: go not found — the Go gates are required (SKIP_PRE_PUSH=1 overrides)" >&2
  exit 1
fi

failed=0
fail() { printf 'pre-push: %s\n' "$1" >&2; failed=1; }

go build ./... || fail "go build ./... failed"
go vet ./... || fail "go vet ./... failed"
go test ./... || fail "go test ./... failed"

if command -v make >/dev/null 2>&1 && grep -q '^deadcode:' Makefile; then
  make deadcode || fail "make deadcode failed"
fi

if command -v make >/dev/null 2>&1 && grep -q '^cross:' Makefile; then
  make cross || fail "make cross failed"
fi

pinned="$(awk '/golangci-lint-action/ { found = 1 } found && /^[[:space:]]*version:/ {
  sub(/#.*/, ""); sub(/^[[:space:]]*version:[[:space:]]*/, ""); sub(/[[:space:]]*$/, ""); print; exit
}' .github/workflows/ci.yml)"
lint="$(command -v golangci-lint || true)"
if [ -z "$lint" ] && [ -x "$(go env GOPATH)/bin/golangci-lint" ]; then
  lint="$(go env GOPATH)/bin/golangci-lint"
fi
if [ -z "$pinned" ]; then
  echo "pre-push: no golangci-lint version pinned in .github/workflows/ci.yml — lint skipped"
elif [ -z "$lint" ]; then
  echo "pre-push: golangci-lint $pinned not installed — lint skipped"
  echo "pre-push: install the pinned release binary (a go install build reports phantom revive findings):"
  echo "  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$pinned/install.sh | sh -s -- -b \"\$(go env GOPATH)/bin\" \"$pinned\""
else
  have="$("$lint" version --short 2>/dev/null | head -1 || true)"
  if [ -z "$have" ]; then
    have="$("$lint" --version 2>/dev/null | awk '{ print $NF }' || true)"
  fi
  case "$have" in v*) ;; *) have="v$have" ;; esac
  if [ "$have" = "$pinned" ]; then
    "$lint" run --timeout=10m || fail "golangci-lint found issues"
  else
    echo "pre-push: golangci-lint $pinned pinned, found $have — lint skipped (versions disagree)"
    echo "pre-push: install the pinned release binary (a go install build reports phantom revive findings):"
    echo "  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$pinned/install.sh | sh -s -- -b \"\$(go env GOPATH)/bin\" \"$pinned\""
  fi
fi

exit "$failed"
