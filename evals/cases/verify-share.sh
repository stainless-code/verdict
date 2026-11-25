#!/usr/bin/env bash
#
# verify.share: the base configuration lists a gitignored dependency
# directory, and verify exposes the checkout's copy inside its clean
# snapshot. Unsandboxed, the command runs the installed node_modules/.bin
# stub through a link; under --sandbox it runs it too, but a write into the
# shared directory fails and the checkout's copy stays untouched. Both runs'
# envelope and receipt name the share. A host with no mechanism grades the
# sandbox-unavailable refusal instead; EVAL_REQUIRE_SANDBOX=1 fails there.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

(cd "$EVAL_SANDBOX" && { [ -d .git ] || git init -q; })
printf 'node_modules\n' > "$EVAL_SANDBOX/.gitignore"
mkdir -p "$EVAL_SANDBOX/node_modules/.bin" "$EVAL_SANDBOX/.verdict"
printf '#!/bin/sh\necho stub-ran\n' > "$EVAL_SANDBOX/node_modules/.bin/stub"
chmod +x "$EVAL_SANDBOX/node_modules/.bin/stub"
printf '{"schema_version":"1","verify":{"share":["node_modules"]}}\n' > "$EVAL_SANDBOX/.verdict/config.json"
eval_mk_bundle 5301
DIR="$EVAL_SANDBOX/.verdict/state/5301"

mkpatch() {
  jq -n --arg c "$1" '{verifyCmd:$c}' > "$DIR/patch.json"
}

out() {
  jq -r '.data.outputTail // empty' <"$EVAL_OUT"
}

# shared_recorded grades the envelope and the receipt for one leg.
shared_recorded() {
  assert_json_field "$1: the envelope names the share" '["node_modules"]' '.data.shared | tojson'
  assert_json_field "$1: the receipt names the share" '["node_modules"]' '.shared | tojson' "$DIR/verify-receipt.json"
}

stub_intact() {
  if [ "$(cat "$EVAL_SANDBOX/node_modules/.bin/stub")" = "$(printf '#!/bin/sh\necho stub-ran')" ] \
    && [ ! -e "$EVAL_SANDBOX/node_modules/planted" ]; then
    good "$1: the checkout's node_modules is untouched"
  else
    bad "$1: the checkout's node_modules changed"
  fi
}

mkpatch 'node_modules/.bin/stub'
run_review verify acme/app 5301 --json --timeout 30
assert_exit "unsandboxed shared run" 0
if [ "$(out)" = "stub-ran" ]; then
  good "unsandboxed: the command runs the shared node_modules/.bin stub"
else
  bad "unsandboxed: the shared stub did not run: $(head -c 300 "$EVAL_OUT")"
fi
shared_recorded "unsandboxed"
stub_intact "unsandboxed"

sandbox_works() {
  bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid --tmpfs /tmp \
    --tmpfs /run --unshare-net --new-session --die-with-parent -- true >/dev/null 2>&1
}

sandbox_exec_works() {
  [ -x /usr/bin/sandbox-exec ] \
    && /usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' true >/dev/null 2>&1
}

mkpatch 'node_modules/.bin/stub && if (printf x > node_modules/planted) 2>/dev/null; then echo WROTE; else echo DENIED; fi'
run_review verify acme/app 5301 --sandbox --json --timeout 30
if sandbox_works || sandbox_exec_works; then
  assert_exit "sandboxed shared run" 0
  assert_json_field "sandboxed shared run records sandboxed" "true" '.data.sandboxed'
  if [ "$(out)" = "$(printf 'stub-ran\nDENIED')" ]; then
    good "sandboxed: the shared stub runs and a write into node_modules is denied"
  else
    bad "sandboxed: want the stub to run and the write denied: $(head -c 300 "$EVAL_OUT")"
  fi
  shared_recorded "sandboxed"
  stub_intact "sandboxed"
else
  if [ "${EVAL_REQUIRE_SANDBOX:-0}" = "1" ]; then
    bad "EVAL_REQUIRE_SANDBOX=1 but no sandbox mechanism can be enforced here"
  fi
  assert_exit "sandboxed shared run without a mechanism" 1
  assert_refusal "sandboxed shared run without a mechanism" "sandbox-unavailable"
fi

eval_end
