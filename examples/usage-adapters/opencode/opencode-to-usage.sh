#!/bin/sh
# opencode-to-usage.sh — convert one `opencode export <session>` JSON file to
# one verdict usage record (schemas/usage.schema.json).
#
# Usage: opencode-to-usage.sh [--source NAME] <export.json> <out.json>
#
# The export is harness-private: verdict never opens it. The operator runs
# this converter explicitly and hands the produced file to the budget gate
# with VERDICT_USAGE=<file|dir>.
#
# Pricing: the export's info.cost wins; without it, per-message info.cost
# entries sum. An export with no session time refuses; an unpriced export
# still converts (window only) — the core reads it as no measurement, never
# a measured claim. `opencode export` carries no token counts in this shape,
# so the record prices USD only.
set -eu

prog="opencode-to-usage"
source="opencode"
while [ $# -gt 0 ]; do
  case "$1" in
    --source)
      [ $# -ge 2 ] || { echo "$prog: --source needs a value" >&2; exit 2; }
      source="$2"; shift 2;;
    --source=*)
      source="${1#--source=}"; shift;;
    -h|--help)
      echo "usage: $prog [--source NAME] <export.json> <out.json>" >&2; exit 0;;
    --)
      shift; break;;
    -*)
      echo "$prog: unknown flag $1" >&2; exit 2;;
    *)
      break;;
  esac
done
[ $# -eq 2 ] || { echo "usage: $prog [--source NAME] <export.json> <out.json>" >&2; exit 2; }
in="$1"
out="$2"
command -v jq >/dev/null 2>&1 || { echo "$prog: jq not on PATH" >&2; exit 1; }
[ -f "$in" ] || { echo "$prog: export not found: $in" >&2; exit 1; }
if ! jq -n '0 | gmtime | strftime("%Y-%m-%dT%H:%M:%SZ")' >/dev/null 2>&1; then
  echo "$prog: jq >= 1.7 needed (gmtime for epoch-millisecond windows)" >&2
  exit 1
fi

filter='
(.info // error("not an opencode export")) as $info
| ($info.cost // null | select(type == "number") // null) as $total
| ([.messages? // [] | .[]? | .info.cost? // empty
    | select(type == "number")] | add) as $msum
| ($info.time.created // 0) as $created
| ($info.time.updated // 0) as $updated
| if $updated <= 0 then error("export carries no session time") else . end
| (if $created <= 0 or $created > $updated then $updated else $created end) as $start
| {
    schema_version: "1",
    source: $source,
    started_at: ($start / 1000 | floor | gmtime | strftime("%Y-%m-%dT%H:%M:%SZ")),
    ended_at: ($updated / 1000 | floor | gmtime | strftime("%Y-%m-%dT%H:%M:%SZ"))
  }
| if $total != null then .usd = $total
  elif $msum != null then .usd = $msum
  else . end
'

tmp="$out.tmp.$$"
trap 'rm -f "$tmp"' EXIT INT TERM
if ! jq --arg source "$source" "$filter" <"$in" >"$tmp"; then
  echo "$prog: cannot convert $in (see jq error above)" >&2
  exit 1
fi
if ! jq -e 'has("source") and has("started_at") and has("ended_at")' \
    <"$tmp" >/dev/null; then
  echo "$prog: converter produced an incomplete record" >&2
  exit 1
fi
mv "$tmp" "$out"
trap - EXIT INT TERM
echo "$prog: wrote $out"
