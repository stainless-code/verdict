#!/usr/bin/env bash
# Case usage-errors: the binary owns usage errors (go-port 08, F11).
#
# Grades (all hermetic — no bundles, no network; the built binary is
# invoked directly, never through a dispatcher):
#   U1  no verb prints the full usage block on stderr, exit 2.
#   U2  missing positionals print the full usage block on stderr, exit 2:
#       one leg per verb; bare verbs (check-tree, doctor, learn, init,
#       skill) never fail the gate; trend needs a repo.
#   U3  usage wins over corrupt settings: a missing positional plus a
#       corrupt settings file still prints usage/exit 2 with no settings
#       read, while a complete invocation reads settings and refuses typed
#       bad-settings.
#   U4  unknown verbs: a bare unknown verb fails with the usage block and
#       exit 2; an unknown verb with both positionals fails with the
#       unknown-cmd message and exit 2.
#   U5  the block names `verdict` (never review.sh) and still names --json,
#       the four verify refusal codes, the token-scopes-excess row, and the
#       experimental mcp/mcp config entry points.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

if [ -z "${EVAL_CLI:-}" ] || [ ! -x "$EVAL_CLI" ]; then
  bail "EVAL_CLI missing (run via evals/run.sh or evals/one.sh after make build)"
fi

# Deterministic surface: no settings file, no ambient REVIEW_* knobs.
mkdir -p "$EVAL_SANDBOX/xdg-empty/verdict"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_BUDGET_USD REVIEW_MODEL REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_KB_MIN_JUDGED REVIEW_KB_MAX_FP_RATE NO_COLOR || true

USAGE="$EVAL_SANDBOX/usage-block.txt"
"$EVAL_CLI" --help >"$USAGE" 2>"$EVAL_SANDBOX/usage-help-err.txt"
if [ "$?" -ne 0 ] || [ ! -s "$USAGE" ] || [ -s "$EVAL_SANDBOX/usage-help-err.txt" ]; then
  bad "--help exits non-zero, prints nothing, or leaks on stderr"
else
  good "--help prints the usage block on stdout, exit 0"
fi

# usage_gate <label> -- <cli args...>: the run must exit 2 with exactly
# the usage block on stderr and nothing on stdout.
usage_gate() { # $1 = label, rest after -- = cli args
  local label="$1"; shift
  [ "${1:-}" = "--" ] && shift
  set +e
  ( cd "$EVAL_SANDBOX" && "$EVAL_CLI" "$@" >"$EVAL_SANDBOX/u-out.txt" 2>"$EVAL_SANDBOX/u-err.txt" )
  st=$?
  set -e
  if [ "$st" -eq 2 ] \
    && cmp -s "$EVAL_SANDBOX/u-err.txt" "$USAGE" \
    && [ ! -s "$EVAL_SANDBOX/u-out.txt" ]; then
    good "$label: usage block on stderr, exit 2"
  else
    bad "$label: exit $st (want 2 with the usage block on stderr)"
  fi
}

# --- U1: no verb ---
usage_gate "U1 no verb" --

# --- U2: missing positionals, one leg per verb ---
for verb in fetch fetch-local record adopt fix post triage report next show verify calibrate ready; do
  usage_gate "U2 $verb (no positionals)" -- "$verb"
  usage_gate "U2 $verb (repo only)" -- "$verb" acme/app
done
usage_gate "U2 trend (no repo)" -- trend
for verb in check-tree doctor learn init skill; do
  set +e
  ( cd "$EVAL_SANDBOX" && XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty" "$EVAL_CLI" "$verb" </dev/null >"$EVAL_SANDBOX/u-out.txt" 2>"$EVAL_SANDBOX/u-err.txt" )
  st=$?
  set -e
  # Bare verbs never fail the ARITY gate: whatever the verb itself decides
  # (needs-confirm, dirty tree, --json envelope), the stderr is not the
  # full usage block.
  if ! cmp -s "$EVAL_SANDBOX/u-err.txt" "$USAGE"; then
    good "U2 $verb (bare) never fails the arity gate (exit $st)"
  else
    bad "U2 $verb (bare) unexpectedly prints the full usage block"
  fi
done

# --- U3: usage wins over corrupt settings ---
mkdir -p "$EVAL_SANDBOX/xdg-bad/verdict"
printf '{broken' > "$EVAL_SANDBOX/xdg-bad/verdict/settings.json"
set +e
( cd "$EVAL_SANDBOX" && XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-bad" "$EVAL_CLI" report >"$EVAL_SANDBOX/u-out.txt" 2>"$EVAL_SANDBOX/u-err.txt" )
st=$?
set -e
if [ "$st" -eq 2 ] && cmp -s "$EVAL_SANDBOX/u-err.txt" "$USAGE"; then
  good "U3 corrupt settings + missing pr still usage-errors (exit 2)"
else
  bad "U3 corrupt-settings gate exits $st"
fi
set +e
( cd "$EVAL_SANDBOX" && XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-bad" "$EVAL_CLI" report acme/app 1 --json >"$EVAL_SANDBOX/u-out.txt" 2>"$EVAL_SANDBOX/u-err.txt" )
st=$?
set -e
if [ "$st" -eq 1 ] && [ "$(jq -r '.error.code // empty' <"$EVAL_SANDBOX/u-out.txt" 2>/dev/null)" = "bad-settings" ]; then
  good "U3 complete invocation reads settings and refuses bad-settings"
else
  bad "U3 complete invocation exits $st (want typed bad-settings)"
fi

# --- U4: unknown verbs ---
usage_gate "U4 unknown verb (bare)" -- bogus
set +e
( cd "$EVAL_SANDBOX" && "$EVAL_CLI" bogus acme/app 1 >"$EVAL_SANDBOX/u-out.txt" 2>"$EVAL_SANDBOX/u-err.txt" )
st=$?
set -e
if [ "$st" -eq 2 ] && grep -q "unknown cmd 'bogus'" "$EVAL_SANDBOX/u-err.txt"; then
  good "U4 unknown verb with positionals names the verb (exit 2)"
else
  bad "U4 unknown verb with positionals exits $st"
fi

# --- U5: program name and anchor strings ---
if grep -q "usage: verdict " "$USAGE" && ! grep -q "review\.sh" "$USAGE"; then
  good "U5 block names verdict, never review.sh"
else
  bad "U5 block program name wrong"
fi
anchors_ok="true"
for anchor in --json verify-destructive verify-networked verify-credential-read verify-repo-escape token-scopes-excess; do
  grep -q -F -- "$anchor" "$USAGE" || { anchors_ok="false"; bad "U5 block misses $anchor"; }
done
[ "$anchors_ok" = "true" ] && good "U5 block keeps --json, the verify codes, and token-scopes-excess"
if grep -q -F -- "mcp [--read-only | --operator] (experimental)" "$USAGE" \
  && grep -q -F -- "mcp config --agent <claude|cursor|codex>" "$USAGE" \
  && grep -q -F -- "(experimental)" "$USAGE"; then
  good "U5 block lists mcp and mcp config tagged experimental"
else
  bad "U5 block misses the experimental mcp entry points"
fi

unset XDG_CONFIG_HOME || true
rm -rf "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/xdg-bad"

eval_end
