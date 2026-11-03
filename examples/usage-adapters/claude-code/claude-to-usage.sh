#!/bin/sh
# claude-to-usage.sh — convert one Claude Code transcript (.jsonl) to one
# verdict usage record (schemas/usage.schema.json).
#
# Usage: claude-to-usage.sh [--source NAME] <transcript.jsonl> <out.json>
#
# The transcript is harness-private: verdict never opens it. The operator
# runs this converter explicitly (or via on-stop.sh) and hands the produced
# file to the budget gate with VERDICT_USAGE=<file|dir>.
#
# Pricing: the last cost-state totalCostUSD wins; without one, deduplicated
# per-request costUSD entries sum (repeat lines for one requestId count
# once). Tokens sum from message.usage when present, even when nothing is
# priced — the core reads an unpriced record as no measurement, never a
# measured claim. A transcript with no timestamps refuses: an explicit
# handoff must not silently produce a windowless claim.
set -eu

prog="claude-to-usage"
source="claude-code"
while [ $# -gt 0 ]; do
  case "$1" in
    --source)
      [ $# -ge 2 ] || { echo "$prog: --source needs a value" >&2; exit 2; }
      source="$2"; shift 2;;
    --source=*)
      source="${1#--source=}"; shift;;
    -h|--help)
      echo "usage: $prog [--source NAME] <transcript.jsonl> <out.json>" >&2; exit 0;;
    --)
      shift; break;;
    -*)
      echo "$prog: unknown flag $1" >&2; exit 2;;
    *)
      break;;
  esac
done
[ $# -eq 2 ] || { echo "usage: $prog [--source NAME] <transcript.jsonl> <out.json>" >&2; exit 2; }
in="$1"
out="$2"
command -v jq >/dev/null 2>&1 || { echo "$prog: jq not on PATH" >&2; exit 1; }
[ -f "$in" ] || { echo "$prog: transcript not found: $in" >&2; exit 1; }

filter='
def cost: (.costUSD // .message.costUSD);
def tkey: (.requestId // .message.id // "");
def tok: (.message.usage // null);
[inputs | select(type == "object")] as $lines
| ([$lines[] | .timestamp | select(type == "string" and length > 0)] | sort) as $ts
| if ($ts | length) == 0 then error("no transcript timestamps") else . end
| ([$lines[] | select(.type == "cost-state") | .totalCostUSD
    | select(type == "number")] | last) as $total
| ([$lines[] | select((.type // "") != "cost-state"
    and ((cost | type == "number") or (tok | type == "object")))
    | {key: tkey, cost: (cost // null | select(type == "number") // null), tok: tok}]
    | ((map(select(.key != "")) | unique_by(.key)) + map(select(.key == "")))) as $reqs
| ($reqs | map(.cost) | map(select(type == "number")) | add) as $sum
| ($reqs | map(.tok) | map(select(type == "object"))) as $toks
| {
    schema_version: "1",
    source: $source,
    started_at: ($ts[0] | sub("\\.[0-9]+Z$"; "Z")),
    ended_at: ($ts[-1] | sub("\\.[0-9]+Z$"; "Z"))
  }
| ([$lines[] | .message.model? // empty
    | select(type == "string" and length > 0)] | last) as $model
| if $model != null then .model = $model else . end
| if ($toks | length) > 0 then
    .tokens = {
      input: ([$toks[] | .input_tokens | select(type == "number")] | add // 0),
      output: ([$toks[] | .output_tokens | select(type == "number")] | add // 0)
    }
    | ([$toks[] | .cache_read_input_tokens
      | select(type == "number")] | add // null) as $cr
    | ([$toks[] | .cache_creation_input_tokens
      | select(type == "number")] | add // null) as $cw
    | if $cr != null then .tokens.cache_read = $cr else . end
    | if $cw != null then .tokens.cache_write = $cw else . end
  else . end
| if $total != null then .usd = $total
  elif $sum != null then .usd = $sum
  else . end
'

tmp="$out.tmp.$$"
trap 'rm -f "$tmp"' EXIT INT TERM
if ! jq -n --arg source "$source" "$filter" <"$in" >"$tmp"; then
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
