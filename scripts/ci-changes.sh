#!/bin/sh
# Reads changed paths on stdin and prints `full=true|false`: a change whose
# every path is docs-site, changeset, changelog, or issue-template plumbing
# skips the Go, eval, installer, and client lanes.
set -eu

full=false
seen=false
while IFS= read -r path; do
  [ -n "$path" ] || continue
  seen=true
  case "$path" in
    apps/docs/* | .changeset/* | CHANGELOG.md | .github/ISSUE_TEMPLATE/*) ;;
    *) full=true ;;
  esac
done

[ "$seen" = true ] || full=true
printf 'full=%s\n' "$full"
