#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

BUDGET='scripts/size-baseline.txt'
# Per-target byte ceilings plus one `deps` line; data only, no comments.
# `make size` fails when a target or the dep count grows past its line, when a
# shipped target has no line, or a line names a target the release contract no
# longer ships. A deliberate growth raises only the crossed ceilings, in the
# same change, to the next 10 kB above the exact measurement; the measured
# values and the reason go in the commit message body.
# The release build's own inputs (.goreleaser.yml): ./cmd/verdict, -trimpath,
# -s -w, and a fixed version so the embedded string length cannot drift.
VERSION='0.0.0'

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

status=0
fail() {
  printf 'size: %s\n' "$1" >&2
  status=1
}

budget_of() {
  awk -v want="$1" '$1 == want { print $2 }' "$BUDGET"
}

graph_deps() {
  go mod graph |
    awk '{ sub(/@.*/, "", $2); if ($2 != "go" && $2 != "toolchain") print $2 }' |
    LC_ALL=C sort -u
}

# Reads an unstripped rebuild: -s strips the symbol table the shipped binary
# would need to answer which package grew.
largest_symbols() {
  dir="$work/symbols-$1-$2"
  mkdir -p "$dir"
  CGO_ENABLED=0 GOOS="$1" GOARCH="$2" \
    go build -trimpath -ldflags "-X main.version=$VERSION" -o "$dir/verdict" ./cmd/verdict
  printf 'size: largest symbols in %s/%s (unstripped build):\n' "$1" "$2" >&2
  go tool nm -size "$dir/verdict" |
    awk 'NF >= 4 && $3 ~ /^[TtRrDd]$/ { printf "%10d  %s\n", $2, $4 }' |
    sort -k1,1nr |
    head -10 |
    sed 's/^/    /' >&2
}

seen=''
for pair in $(go run ./cmd/crossplatforms); do
  goos=${pair%%/*}
  goarch=${pair##*/}
  dir="$work/$goos-$goarch"
  mkdir -p "$dir"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$dir/verdict" ./cmd/verdict
  bytes=$(wc -c < "$dir/verdict" | tr -d ' ')
  seen="$seen $pair"
  ceiling=$(budget_of "$pair")
  if [ -z "$ceiling" ]; then
    fail "$pair is unbudgeted in $BUDGET"
    continue
  fi
  if [ "$bytes" -gt "$ceiling" ]; then
    fail "$pair is $bytes bytes, $((bytes - ceiling)) over its $ceiling-byte budget"
    largest_symbols "$goos" "$goarch"
    continue
  fi
  printf 'size: %-14s %8s bytes (budget %s)\n' "$pair" "$bytes" "$ceiling"
done

budgeted="$work/budgeted"
awk '$1 != "deps" && NF == 2 { print $1 }' "$BUDGET" > "$budgeted"
while read -r pair; do
  case " $seen " in
    *" $pair "*) ;;
    *) fail "$BUDGET budgets $pair, which the release contract does not ship" ;;
  esac
done < "$budgeted"

graph_deps > "$work/deps"
deps=$(wc -l < "$work/deps" | tr -d ' ')
ceiling=$(budget_of deps)
if [ -z "$ceiling" ]; then
  fail "$BUDGET declares no deps ceiling"
elif [ "$deps" -gt "$ceiling" ]; then
  fail "the module graph lists $deps dependencies, over its $ceiling budget: bump the line in $BUDGET"
  sed 's/^/    /' "$work/deps" >&2
else
  printf 'size: deps %s (budget %s)\n' "$deps" "$ceiling"
fi

if [ "$status" -eq 0 ]; then
  echo "size: every shipped binary and the module graph are within budget"
fi
exit "$status"
