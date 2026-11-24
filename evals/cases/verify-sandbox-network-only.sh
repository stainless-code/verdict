#!/usr/bin/env bash
#
# verify --sandbox-network-only: the explicit Linux unshare --net opt-in.
# Where unshare can build the user and network namespaces, the command runs
# with no route out, the run records sandboxMechanism "unshare-net", and the
# warning says mount/filesystem/PID isolation is absent — so the same leg
# proves what the flag does NOT isolate (credentials and host /tmp stay
# visible). Everywhere else the flag refuses sandbox-unavailable and executes
# nothing. The mutual-exclusion refusal runs on every host.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

DIR="$EVAL_SANDBOX/.verdict/state/5201"
UNIQ="$$"
HITS="$EVAL_SANDBOX/netonly-hits.log"
READY="$EVAL_SANDBOX/netonly-ready"
SERVER="$EVAL_SANDBOX/netonly-listener.js"
FAKEHOME="$EVAL_SANDBOX/fake-net-home"
HOSTTMP="/tmp/verdict-netonly.$UNIQ"
mkdir -p "$DIR" "$FAKEHOME/.ssh"
printf 'ssh-rsa netonly-sentinel\n' > "$FAKEHOME/.ssh/id_rsa"
: > "$HITS"

cleanup() {
  if [ -n "${LISTENER_PID:-}" ]; then
    kill "$LISTENER_PID" 2>/dev/null || true
  fi
  rm -f "$HOSTTMP"
}
trap cleanup EXIT

mkpatch() {
  jq -n --arg c "$1" '{verifyCmd:$c}' > "$DIR/patch.json"
}

patch_hash() {
  shasum -a 1 "$DIR/patch.json" | cut -d' ' -f1
}

hits() {
  wc -l < "$HITS" | tr -d ' '
}

unshare_works() {
  unshare --user --map-root-user --net -- true >/dev/null 2>&1
}

# The combined flags refuse before any mechanism is chosen or any command
# runs: the caller must state one boundary.
mkpatch 'printf created > netonly-sentinel.txt'
rm -f "$EVAL_SANDBOX/netonly-sentinel.txt" "$DIR/.verify.out"
before="$(patch_hash)"
run_review verify acme/app 5201 --sandbox --sandbox-network-only --json --timeout 10
assert_exit "combined sandbox flags" 1
assert_refusal "combined sandbox flags" bad-flags
if jq -r '.error.message // empty' <"$EVAL_OUT" | matches -- "--sandbox" \
  && jq -r '.error.message // empty' <"$EVAL_OUT" | matches -- "--sandbox-network-only" \
  && jq -r '.error.message // empty' <"$EVAL_OUT" | matches "mutually exclusive"; then
  good "the bad-flags refusal names both exclusive flags"
else
  bad "the bad-flags refusal does not name both flags: $(head -c 200 "$EVAL_OUT")"
fi
if [ "$(patch_hash)" = "$before" ] && [ ! -e "$DIR/.verify.out" ] && [ ! -e "$EVAL_SANDBOX/netonly-sentinel.txt" ]; then
  good "the combined-flag refusal leaves the store untouched and executes nothing"
else
  bad "the combined-flag refusal mutated the store or executed the command"
fi

if ! unshare_works; then
  # No unprivileged user+net namespace here: the flag must refuse, never fall
  # back to an unwrapped run.
  mkpatch 'printf created > netonly-sentinel.txt'
  rm -f "$EVAL_SANDBOX/netonly-sentinel.txt" "$DIR/.verify.out"
  before="$(patch_hash)"
  run_review verify acme/app 5201 --sandbox-network-only --json --timeout 10
  assert_exit "network-only refusal" 1
  assert_refusal "network-only refusal" sandbox-unavailable
  if jq -r '.error.message // empty' <"$EVAL_OUT" | matches "Linux" \
    && jq -r '.error.message // empty' <"$EVAL_OUT" | matches "unshare"; then
    good "the refusal names the Linux unshare opt-in"
  else
    bad "the refusal does not name the Linux unshare opt-in: $(head -c 200 "$EVAL_OUT")"
  fi
  if [ "$(patch_hash)" = "$before" ] && [ ! -e "$DIR/.verify.out" ] && [ ! -e "$EVAL_SANDBOX/netonly-sentinel.txt" ]; then
    good "the network-only refusal leaves the store untouched and executes nothing"
  else
    bad "the network-only refusal mutated the store or executed the command"
  fi
  eval_end
  exit 0
fi

good "unshare --user --map-root-user --net works on this host"

# A benign run passes, records the mechanism, and warns that only the network
# half is enforced.
mkpatch 'printf ok'
rm -f "$DIR/.verify.out"
run_review verify acme/app 5201 --sandbox-network-only --json --timeout 10
assert_exit "network-only benign run" 0
assert_json_field "network-only run records sandboxed" "true" '.data.sandboxed'
assert_json_field "network-only run records the mechanism" "unshare-net" '.data.sandboxMechanism'
assert_json_field "network-only run exit" "0" '.data.exit'
assert_json_field "network-only patch record carries the mechanism" "unshare-net" '.sandboxMechanism' "$DIR/patch.json"
if jq -e '[.warnings[]? | select(test("network-only") and test("no mount, filesystem, or PID isolation"))] | length == 1' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "the run warns that mount, filesystem, and PID isolation are absent"
else
  bad "the run misses the no-mount warning: $(head -c 300 "$EVAL_OUT")"
fi

# The listener is the containment target: the unsandboxed control must reach
# it, and the network-only run must not.
cat > "$SERVER" <<'EOF'
const http = require("http");
const fs = require("fs");
const hits = process.argv[2];
const ready = process.argv[3];
const server = http.createServer((req, res) => {
  fs.appendFileSync(hits, "hit\n");
  res.end("ok");
});
server.listen(0, "127.0.0.1", () => {
  fs.writeFileSync(ready, String(server.address().port));
});
EOF
rm -f "$READY"
node "$SERVER" "$HITS" "$READY" &
LISTENER_PID=$!
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  [ -f "$READY" ] && break
  sleep 0.1
done
if [ ! -f "$READY" ]; then
  bad "local listener never came up"
  eval_end
  exit 0
fi
PORT="$(cat "$READY")"
export PORT

mkpatch 'curl -s http://127.0.0.1:$PORT/hit >/dev/null && printf reached > netonly-net.txt'
rm -f "$EVAL_SANDBOX/netonly-net.txt"
: > "$HITS"
run_review verify acme/app 5201 --env-allowlist PORT --json --timeout 10
assert_exit "unsandboxed control" 0
assert_eq "unsandboxed control reaches the listener" 1 "$(hits)"
if [ -f "$EVAL_SANDBOX/netonly-net.txt" ]; then
  good "unsandboxed control exits clean"
else
  bad "unsandboxed control did not run"
fi

rm -f "$EVAL_SANDBOX/netonly-net.txt"
: > "$HITS"
run_review verify acme/app 5201 --sandbox-network-only --env-allowlist PORT --json --timeout 10
assert_exit "network-only listener run" 0
if jq -e '.error == null and .data.sandboxed == true and .data.sandboxMechanism == "unshare-net" and .data.exit != 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "network-only run fails to connect (exit $(jq -r '.data.exit' <"$EVAL_OUT"))"
else
  bad "network-only run reached the listener or broke: $(head -c 300 "$EVAL_OUT")"
fi
assert_eq "network-only run leaves the listener untouched" 0 "$(hits)"
if [ ! -e "$EVAL_SANDBOX/netonly-net.txt" ]; then
  good "network-only run writes no marker"
else
  bad "network-only run wrote the marker"
fi

# The same legs prove the absence of the filesystem half: the fake HOME's
# credential file and the shared host /tmp are still visible.
OLD_HOME="$HOME"
HOME="$FAKEHOME"
mkpatch 'h=$(printenv HOME); if [ -s "$h/.ssh/id_rsa" ]; then printf visible > netonly-cred.txt; else printf hidden > netonly-cred.txt; fi'
rm -f "$EVAL_SANDBOX/netonly-cred.txt"
run_review verify acme/app 5201 --sandbox-network-only --json --timeout 10
HOME="$OLD_HOME"
assert_exit "network-only credential leg" 0
if [ -f "$EVAL_SANDBOX/netonly-cred.txt" ] && [ "$(cat "$EVAL_SANDBOX/netonly-cred.txt")" = "visible" ]; then
  good "network-only leaves credential paths visible, as documented"
else
  bad "network-only unexpectedly hid the credential path ($(cat "$EVAL_SANDBOX/netonly-cred.txt" 2>/dev/null || echo missing))"
fi

rm -f "$HOSTTMP"
mkpatch "printf shared > $HOSTTMP"
run_review verify acme/app 5201 --sandbox-network-only --json --timeout 10
assert_exit "network-only host /tmp leg" 0
if [ -f "$HOSTTMP" ] && [ "$(cat "$HOSTTMP")" = "shared" ]; then
  good "network-only leaves the shared host /tmp writable, as documented"
else
  bad "network-only unexpectedly hid the shared host /tmp"
fi
rm -f "$HOSTTMP"

eval_end
