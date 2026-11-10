#!/usr/bin/env bash
#
# Network-dead host stubs for provider eval cases: one script per host tool
# (`glab`, `curl`), installed first on the case PATH. A stub answers the exact
# argv a case registered, logs every call, and refuses anything else with exit
# 64 plus an UNRECOGNISED line in the log, so a case cannot pass against a call
# nobody registered and no stub body ever reaches a host.
#
# A case sources this after common.sh, then:
#
#   hoststub_init glab
#   hoststub_prove_dead glab
#   hoststub_route glab 'mr view 7 -R acme/app -F json' "$FIX/MR.json"
#
# Route keys are the argv joined by single spaces, which is exactly what the
# stub logs and what `hoststub_argv_log` writes out for comparison.
set -euo pipefail

: "${EVAL_STUBBIN:?run via evals/run.sh}"

HOSTSTUB_LOG="$(cd "$EVAL_STUBBIN/.." && pwd)/hoststub.log"
HOSTSTUB_ROUTES="$(cd "$EVAL_STUBBIN/.." && pwd)/hoststub-routes"
export HOSTSTUB_LOG HOSTSTUB_ROUTES

# hoststub_init [tool...] installs the network-dead stub for each tool
# (default: glab curl) and starts an empty route table and call log. The proxy
# variables leave any client that ignores PATH nowhere to connect.
hoststub_init() {
  local tools="${*:-glab curl}" tool
  mkdir -p "$HOSTSTUB_ROUTES"
  : >"$HOSTSTUB_LOG"
  export HTTP_PROXY="http://127.0.0.1:9" HTTPS_PROXY="http://127.0.0.1:9"
  export ALL_PROXY="http://127.0.0.1:9" NO_PROXY=""
  # shellcheck disable=SC2086 # a deliberate tool list, never a filename
  for tool in $tools; do
    : >"$HOSTSTUB_ROUTES/$tool"
    hoststub_write "$tool" "$EVAL_STUBBIN/$tool"
  done
}

# hoststub_write <tool> <path> emits one stub: log the argv, answer the first
# matching route (body file, then exit status), refuse everything else.
hoststub_write() {
  local tool="$1" path="$2"
  cat >"$path" <<STUB
#!/usr/bin/env bash
set -uo pipefail
routes="\${HOSTSTUB_ROUTES:?hoststub_init was not called}"
log="\${HOSTSTUB_LOG:?hoststub_init was not called}"
argv="\$*"
printf 'stub-$tool %s\n' "\$argv" >>"\$log"
while IFS=\$'\\t' read -r want body status; do
  [ "\$want" = "\$argv" ] || continue
  [ -z "\$body" ] || cat "\$body"
  exit "\${status:-0}"
done <"\$routes/$tool"
printf 'stub-$tool UNRECOGNISED %s\n' "\$argv" >>"\$log"
printf 'evals-stub-$tool: unrecognised argv (network disabled): %s\n' "\$argv" >&2
exit 64
STUB
  chmod +x "$path"
}

# hoststub_route <tool> <argv> <body-file> [exit-status] registers one exact
# argv. An empty body file (`/dev/null`) answers with no stdout, which is what
# a write route looks like from the caller's side.
hoststub_route() {
  local tool="$1" argv="$2" body="$3" status="${4:-0}"
  case "$argv" in
    *$'\t'*)
      bad "hoststub_route argv carries a tab: $argv"
      return 1
      ;;
  esac
  if [ ! -x "$EVAL_STUBBIN/$tool" ]; then
    bad "hoststub_route for $tool before hoststub_init (no stub at $EVAL_STUBBIN/$tool)"
    return 1
  fi
  if [ ! -f "$body" ]; then
    bad "hoststub_route fixture for '$argv' is missing: $body"
    return 1
  fi
  printf '%s\t%s\t%s\n' "$argv" "$body" "$status" >>"$HOSTSTUB_ROUTES/$tool"
}

# hoststub_count <tool> is the number of calls that tool logged.
hoststub_count() {
  grep -c "^stub-$1 " "$HOSTSTUB_LOG" || true
}

# hoststub_argv_log <tool> <out> writes one call's argv per line, in call
# order, for comparison against an expected-argvs fixture.
hoststub_argv_log() {
  sed -n "s/^stub-$1 //p" "$HOSTSTUB_LOG" >"$2"
}

# hoststub_prove_dead <tool> proves the stub is what PATH resolves and that an
# unregistered argv is refused loudly, then clears the log so only real calls
# remain.
hoststub_prove_dead() {
  local tool="$1" stub="$EVAL_STUBBIN/$1" resolved status=0
  resolved="$(PATH="$EVAL_STUBBIN:$PATH" command -v "$tool" 2>/dev/null || true)"
  assert_eq "PATH resolves $tool to the network-dead stub" "$stub" "$resolved"
  "$stub" hoststub-self-check >/dev/null 2>&1 || status=$?
  assert_eq "$tool stub refuses an unregistered argv" 64 "$status"
  assert_file_grep "$tool stub logs the refusal" "$HOSTSTUB_LOG" "^stub-$tool UNRECOGNISED hoststub-self-check\$"
  : >"$HOSTSTUB_LOG"
}

# hoststub_assert_all_routed fails when any call reached a stub with an argv no
# case registered.
hoststub_assert_all_routed() {
  local refusals calls
  refusals="$(grep -c ' UNRECOGNISED ' "$HOSTSTUB_LOG" || true)"
  calls="$(grep -c '^stub-' "$HOSTSTUB_LOG" || true)"
  if [ "$refusals" = "0" ]; then
    good "every host-stub call was a registered argv ($calls call(s))"
  else
    bad "$refusals unregistered host-stub argv(s) — see $HOSTSTUB_LOG"
  fi
}
