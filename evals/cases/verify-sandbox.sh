#!/usr/bin/env bash
#
# verify --sandbox: on a host that can enforce bubblewrap the command runs
# contained (no network, hidden credentials, writable repo, private /tmp);
# on every other host the run refuses sandbox-unavailable and executes
# nothing. The refusal leg is a real leg, never a skip.
#
# EVAL_REQUIRE_SANDBOX=1 (the Linux CI lane) fails the case instead of
# grading the refusal leg, so a broken bubblewrap install cannot pass by
# exercising only the fallback.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

DIR="$EVAL_SANDBOX/.verdict/state/5201"
UNIQ="$$"
HITS="$EVAL_SANDBOX/listener-hits.log"
READY="$EVAL_SANDBOX/listener-ready"
SERVER="$EVAL_SANDBOX/listener.js"
FAKEHOME="$EVAL_SANDBOX/fake-home"
PAYLOAD_SRC="/tmp/verify-fuzz-src.$UNIQ"
PAYLOAD_BIN="/tmp/verify-fuzz-fetched.$UNIQ"
mkdir -p "$DIR" "$FAKEHOME/.aws" "$FAKEHOME/.ssh" "$FAKEHOME/.config/gh"
: > "$HITS"
printf 'aws_secret_access_key = sandbox-sentinel\n' > "$FAKEHOME/.aws/credentials"
printf 'ssh-rsa sandbox-sentinel\n' > "$FAKEHOME/.ssh/id_rsa"
printf 'github.com:\n  oauth_token: sandbox-sentinel\n' > "$FAKEHOME/.config/gh/hosts.yml"

listener_stop() {
  if [ -n "${LISTENER_PID:-}" ]; then
    kill "$LISTENER_PID" 2>/dev/null || true
  fi
  rm -f "$PAYLOAD_SRC" "$PAYLOAD_BIN"
}
trap listener_stop EXIT

sandbox_works() {
  bwrap --ro-bind / / --tmpfs /tmp --unshare-net -- true >/dev/null 2>&1
}

mkpatch() {
  jq -n --arg c "$1" '{verifyCmd:$c}' > "$DIR/patch.json"
}

patch_hash() {
  shasum -a 1 "$DIR/patch.json" | cut -d' ' -f1
}

hits() {
  wc -l < "$HITS" | tr -d ' '
}

# The refusal leg runs wherever the sandbox cannot be enforced. The sentinel
# would be written by the command, so its absence proves nothing executed.
refusal_leg() {
  mkpatch 'printf created > sandbox-sentinel.txt'
  rm -f "$EVAL_SANDBOX/sandbox-sentinel.txt" "$DIR/.verify.out"
  local before after
  before="$(patch_hash)"
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandbox refusal" 1
  assert_refusal "sandbox refusal" sandbox-unavailable
  after="$(patch_hash)"
  if [ "$before" = "$after" ]; then
    good "refusal leaves patch.json byte-identical"
  else
    bad "refusal mutated patch.json ($before -> $after)"
  fi
  if [ ! -e "$DIR/.verify.out" ]; then
    good "refusal writes no .verify.out"
  else
    bad "refusal wrote .verify.out"
  fi
  if [ ! -e "$EVAL_SANDBOX/sandbox-sentinel.txt" ]; then
    good "refusal executed nothing (no sentinel)"
  else
    bad "refusal ran the command unsandboxed (sentinel present)"
  fi
  if jq -r '.error.message // empty' <"$EVAL_OUT" | matches -i -E "bwrap|namespace"; then
    good "refusal names the bwrap cause"
  else
    bad "refusal does not name the bwrap cause: $(head -c 200 "$EVAL_OUT")"
  fi
}

if ! sandbox_works; then
  if [ "${EVAL_REQUIRE_SANDBOX:-0}" = "1" ]; then
    bad "EVAL_REQUIRE_SANDBOX=1 but bwrap cannot create its namespaces"
  fi
  refusal_leg
  eval_end
  exit 0
fi

# The listener is the containment target: the unsandboxed run must reach it,
# and every sandboxed run must leave it untouched.
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

# Positive control: the same command with no --sandbox reaches the listener,
# so a zero-hit sandboxed run proves isolation rather than a dead target.
mkpatch 'curl -s http://127.0.0.1:$PORT/hit >/dev/null && printf reached > sandbox-net.txt'
rm -f "$EVAL_SANDBOX/sandbox-net.txt"
: > "$HITS"
run_review verify acme/app 5201 --json --timeout 10
assert_exit "unsandboxed control" 0
assert_eq "unsandboxed control reaches the listener" 1 "$(hits)"
if [ -f "$EVAL_SANDBOX/sandbox-net.txt" ]; then
  good "unsandboxed control exits clean"
else
  bad "unsandboxed control did not run"
fi

# The same command under --sandbox cannot reach the listener: the child gets
# its own loopback, so the connection fails and the marker is never written.
rm -f "$EVAL_SANDBOX/sandbox-net.txt"
: > "$HITS"
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed listener run" 0
if jq -e '.error == null and .data.sandboxed == true and .data.exit != 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "sandboxed listener run fails to connect (exit $(jq -r '.data.exit' <"$EVAL_OUT"))"
else
  bad "sandboxed listener run reached the listener or broke: $(head -c 300 "$EVAL_OUT")"
fi
assert_eq "sandboxed listener run leaves the listener untouched" 0 "$(hits)"
if [ ! -e "$EVAL_SANDBOX/sandbox-net.txt" ]; then
  good "sandboxed listener run writes no marker"
else
  bad "sandboxed listener run wrote the marker"
fi

# A benign command passes and records the sandbox on both surfaces.
mkpatch 'printf ok'
rm -f "$DIR/.verify.out"
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed benign run" 0
assert_json_field "sandboxed benign run records sandboxed" "true" '.data.sandboxed'
assert_json_field "sandboxed benign run exit" "0" '.data.exit'
assert_json_field "sandboxed benign run tail" "ok" '.data.outputTail'
assert_json_field "patch record carries sandboxed" "true" '.sandboxed' "$DIR/patch.json"
assert_json_field "patch record carries exit" "0" '.verifyExit' "$DIR/patch.json"
if [ -f "$DIR/.verify.out" ] && [ "$(cat "$DIR/.verify.out")" = "ok" ]; then
  good "sandboxed benign run writes .verify.out"
else
  bad "sandboxed benign run lost .verify.out"
fi

# Repo writes and /tmp scratch succeed; /tmp is private, so the host does not
# see the file the child wrote.
mkpatch "printf repo > sandbox-repo.txt && printf tmp > /tmp/sandbox-tmp.$UNIQ.txt && printf both > sandbox-both.txt"
rm -f "$EVAL_SANDBOX/sandbox-repo.txt" "$EVAL_SANDBOX/sandbox-both.txt"
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed writes run" 0
if [ -f "$EVAL_SANDBOX/sandbox-repo.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-repo.txt")" = "repo" ]; then
  good "repo write succeeds inside the sandbox"
else
  bad "repo write failed inside the sandbox"
fi
if [ -f "$EVAL_SANDBOX/sandbox-both.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-both.txt")" = "both" ]; then
  good "/tmp write succeeds inside the sandbox"
else
  bad "/tmp write failed inside the sandbox"
fi
if [ ! -e "/tmp/sandbox-tmp.$UNIQ.txt" ]; then
  good "/tmp is private (host does not see the child's file)"
else
  bad "/tmp is shared with the host"
  rm -f "/tmp/sandbox-tmp.$UNIQ.txt"
fi

# A credential file exists on the host but not inside the sandbox. The dir
# name is assembled from a variable so the static gate stays out of the way:
# this leg grades the sandbox, not the string scan.
mkpatch 'h=$(printenv HOME); a=.aws; b=.ssh; c=.config; g=gh; [ -e "$h/$a/credentials" ] || [ -e "$h/$b/id_rsa" ] || [ -e "$h/$c/$g/hosts.yml" ] && printf leak > sandbox-cred.txt || printf hidden > sandbox-cred.txt'
rm -f "$EVAL_SANDBOX/sandbox-cred.txt"
OLD_HOME="$HOME"
HOME="$FAKEHOME"
run_review verify acme/app 5201 --sandbox --json --timeout 10
HOME="$OLD_HOME"
assert_exit "sandboxed credential run" 0
if [ -f "$EVAL_SANDBOX/sandbox-cred.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-cred.txt")" = "hidden" ]; then
  good "the credential dir is empty inside the sandbox"
else
  bad "the credential dir is visible inside the sandbox ($(cat "$EVAL_SANDBOX/sandbox-cred.txt" 2>/dev/null || echo missing))"
fi

# Gate-bypass corpus rows F-X12..F-X18 execute (the static gate stays xfail)
# but stay contained: the live listener sees no hit and the host /tmp
# payloads the rows name never load.
cat > "$PAYLOAD_SRC" <<EOF
#!/bin/sh
printf 'hit\n' >> "$HITS"
EOF
cat > "$PAYLOAD_BIN" <<EOF
#!/bin/sh
printf 'hit\n' >> "$HITS"
EOF
chmod +x "$PAYLOAD_BIN"
TAB="$(printf '\t')"
while IFS="$TAB" read -r id _ mode cmd; do
  case "$id" in F-X1[2-8]) ;; *) continue ;; esac
  [ "$mode" = "xfail" ] || continue
  cmd="${cmd//127.0.0.1:9/127.0.0.1:$PORT}"
  cmd="${cmd//verify-fuzz-src/verify-fuzz-src.$UNIQ}"
  cmd="${cmd//verify-fuzz-fetched/verify-fuzz-fetched.$UNIQ}"
  : > "$HITS"
  mkpatch "$cmd"
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  if [ "$EVAL_STATUS" -eq 0 ] \
    && jq -e '.error == null and .data.sandboxed == true and (.data.exit | type) == "number"' <"$EVAL_OUT" >/dev/null 2>&1; then
    good "$id runs contained (exit $(jq -r '.data.exit' <"$EVAL_OUT"))"
  else
    bad "$id was refused or broke: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
  fi
  assert_eq "$id leaves the listener untouched" 0 "$(hits)"
done < "$EVAL_ROOT/evals/fixtures/verify-fuzz-corpus.tsv"

eval_end
