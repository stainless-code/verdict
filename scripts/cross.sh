#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

pairs=$(go run ./cmd/crossplatforms)
for pair in $pairs; do
  GOOS=${pair%%/*}
  GOARCH=${pair##*/}
  echo "cross: $GOOS/$GOARCH"
  # CGO_ENABLED=0 matches the release build's env in .goreleaser.yml.
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build ./...
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go vet ./...
done
printf 'cross: %s platforms built and vetted\n' "$(printf '%s\n' "$pairs" | wc -l | tr -d ' ')"
