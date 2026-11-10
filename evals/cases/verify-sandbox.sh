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
mkdir -p "$DIR" \
  "$FAKEHOME/.aws" "$FAKEHOME/.ssh" "$FAKEHOME/.config/gh" \
  "$FAKEHOME/.docker" "$FAKEHOME/.kube" "$FAKEHOME/.config/gcloud" \
  "$FAKEHOME/.azure" "$FAKEHOME/.gnupg"
: > "$HITS"
printf 'aws_secret_access_key = sandbox-sentinel\n' > "$FAKEHOME/.aws/credentials"
printf 'ssh-rsa sandbox-sentinel\n' > "$FAKEHOME/.ssh/id_rsa"
printf 'github.com:\n  oauth_token: sandbox-sentinel\n' > "$FAKEHOME/.config/gh/hosts.yml"
printf 'machine example.invalid login sandbox password sandbox-sentinel\n' > "$FAKEHOME/.netrc"
printf 'https://sandbox-sentinel@example.invalid\n' > "$FAKEHOME/.git-credentials"
printf '{"auths":{"example.invalid":{"auth":"sandbox-sentinel"}}}\n' > "$FAKEHOME/.docker/config.json"
printf 'users:\n- name: sandbox-sentinel\n' > "$FAKEHOME/.kube/config"
printf '{"client_id":"sandbox-sentinel"}\n' > "$FAKEHOME/.config/gcloud/application_default_credentials.json"
printf '{"accessToken":"sandbox-sentinel"}\n' > "$FAKEHOME/.azure/accessTokens.json"
printf 'sandbox-sentinel\n' > "$FAKEHOME/.gnupg/secring.gpg"
printf '//registry.npmjs.org/:_authToken=sandbox-sentinel\n' > "$FAKEHOME/.npmrc"
printf '[pypi]\nusername = sandbox-sentinel\n' > "$FAKEHOME/.pypirc"

listener_stop() {
  if [ -n "${LISTENER_PID:-}" ]; then
    kill "$LISTENER_PID" 2>/dev/null || true
  fi
  if [ -n "${SOCK_PID:-}" ]; then
    kill "$SOCK_PID" 2>/dev/null || true
  fi
  if [ -n "${CANARY_PID:-}" ]; then
    kill "$CANARY_PID" 2>/dev/null || true
  fi
  if [ -n "${SOCKRUN:-}" ]; then
    rm -rf "$SOCKRUN"
  fi
  rm -f "$PAYLOAD_SRC" "$PAYLOAD_BIN"
}
trap listener_stop EXIT

sandbox_works() {
  bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid --tmpfs /tmp \
    --tmpfs /run --unshare-net --new-session --die-with-parent -- true >/dev/null 2>&1
}

# socket_dir is a directory under /run the runner can write: a unix socket
# there is the route out --unshare-net does not close. Prefer
# $XDG_RUNTIME_DIR (the session bus), then /run/user/<uid>, then /run/lock
# (systemd's world-writable lock tmpfs), then /run itself.
socket_dir() {
  local uid cand
  uid="$(id -u)"
  for cand in "${XDG_RUNTIME_DIR:-}" "/run/user/$uid" /run/lock /run; do
    case "$cand" in /run | /run/*) ;; *) continue ;; esac
    if [ -d "$cand" ] && [ -w "$cand" ]; then
      printf '%s' "$cand"
      return 0
    fi
  done
  return 1
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

# Host PIDs and their environments are outside the sandbox. The canary is a
# same-uid host process carrying a sentinel value: today its environment is
# readable through the host /proc, so the leg fails unless the sandbox gets
# its own PID namespace and proc mount. The proc path rides an env var so the
# static gate, which refuses /proc text, leaves this leg to the sandbox.
env VERDICT_SANDBOX_CANARY=sandbox-sentinel sleep 120 &
CANARY_PID=$!
export VERDICT_SANDBOX_PROC=/proc
export VERDICT_SANDBOX_HOSTPID=$$
mkpatch "c=\"\$VERDICT_SANDBOX_PROC/$CANARY_PID\"; s=\"\$VERDICT_SANDBOX_PROC/self\"; h=\"\$VERDICT_SANDBOX_PROC/\$VERDICT_SANDBOX_HOSTPID\"; if grep -q sandbox-sentinel \"\$c/environ\" 2>/dev/null; then printf leak > sandbox-proc.txt; elif [ -e \"\$h\" ]; then printf leak > sandbox-proc.txt; elif [ -r \"\$s/environ\" ] && [ -d \"\$VERDICT_SANDBOX_PROC/1\" ]; then printf hidden > sandbox-proc.txt; else printf broken > sandbox-proc.txt; fi"
rm -f "$EVAL_SANDBOX/sandbox-proc.txt"
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed /proc run" 0
if [ -f "$EVAL_SANDBOX/sandbox-proc.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-proc.txt")" = "hidden" ]; then
  good "no host PID or environment is visible in /proc"
else
  bad "host /proc is visible inside the sandbox ($(cat "$EVAL_SANDBOX/sandbox-proc.txt" 2>/dev/null || echo missing))"
fi

# /run is empty: the session bus, docker.sock and agent sockets are gone. The
# path is assembled so the static gate's absolute-path arm leaves this leg to
# the sandbox.
mkpatch 'z=; r=$z/run; if [ -z "$(ls -A "$r" 2>/dev/null)" ]; then printf empty > sandbox-run.txt; else printf leak > sandbox-run.txt; fi'
rm -f "$EVAL_SANDBOX/sandbox-run.txt"
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed /run run" 0
if [ -f "$EVAL_SANDBOX/sandbox-run.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-run.txt")" = "empty" ]; then
  good "/run is empty inside the sandbox"
else
  bad "/run is shared with the host ($(cat "$EVAL_SANDBOX/sandbox-run.txt" 2>/dev/null || echo missing))"
fi

# Credential paths exist on the host but not inside the sandbox. Names are
# assembled from variables so the static gate stays out of the way: this leg
# grades the sandbox, not the string scan.
rm -f "$EVAL_SANDBOX/sandbox-cred.txt" "$EVAL_SANDBOX/sandbox-cred-files.txt"
OLD_HOME="$HOME"
HOME="$FAKEHOME"
mkpatch 'h=$(printenv HOME); a=.aws; b=.ssh; c=.config; g=gh; [ -e "$h/$a/credentials" ] || [ -e "$h/$b/id_rsa" ] || [ -e "$h/$c/$g/hosts.yml" ] && printf leak > sandbox-cred.txt || printf hidden > sandbox-cred.txt'
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed credential dirs run" 0
mkpatch 'h=$(printenv HOME); n=.netrc; g=.git-credentials; d=.docker; k=.kube; c=.config; cl=gcloud; az=.azure; gp=.gnupg; m=.npmrc; p=.pypirc; f=config.json; a=application_default_credentials.json; t=accessTokens.json; s=secring.gpg; [ -s "$h/$n" ] || [ -s "$h/$g" ] || [ -s "$h/$d/$f" ] || [ -s "$h/$k/$f" ] || [ -s "$h/$c/$cl/$a" ] || [ -s "$h/$az/$t" ] || [ -s "$h/$gp/$s" ] || [ -s "$h/$m" ] || [ -s "$h/$p" ] && printf leak > sandbox-cred-files.txt || printf hidden > sandbox-cred-files.txt'
run_review verify acme/app 5201 --sandbox --json --timeout 10
HOME="$OLD_HOME"
assert_exit "sandboxed credential files run" 0
if [ -f "$EVAL_SANDBOX/sandbox-cred.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-cred.txt")" = "hidden" ]; then
  good "the credential dir is empty inside the sandbox"
else
  bad "the credential dir is visible inside the sandbox ($(cat "$EVAL_SANDBOX/sandbox-cred.txt" 2>/dev/null || echo missing))"
fi
if [ -f "$EVAL_SANDBOX/sandbox-cred-files.txt" ] && [ "$(cat "$EVAL_SANDBOX/sandbox-cred-files.txt")" = "hidden" ]; then
  good "the credential files are empty inside the sandbox"
else
  bad "a credential file is visible inside the sandbox ($(cat "$EVAL_SANDBOX/sandbox-cred-files.txt" 2>/dev/null || echo missing))"
fi

# A unix socket under /run is a route out --unshare-net does not close. The
# listener lives where the runner can create it (socket_dir); the sandbox
# mounts /run empty, and the unsandboxed control proves the socket is live.
# The path reaches the command through the environment so the static gate
# leaves this leg to the sandbox.
if SOCKDIR="$(socket_dir)"; then
  SOCKRUN="$SOCKDIR/verdict-eval-$UNIQ"
  SOCK="$SOCKRUN/sock"
  SOCKHITS="$EVAL_SANDBOX/socket-hits.log"
  SOCKREADY="$SOCKRUN/ready"
  SOCKSERVER="$EVAL_SANDBOX/unix-server.js"
  mkdir -p "$SOCKRUN"
  : > "$SOCKHITS"
  cat > "$SOCKSERVER" <<'EOF'
const http = require("http");
const fs = require("fs");
const sock = process.argv[2];
const hits = process.argv[3];
const ready = process.argv[4];
const server = http.createServer((req, res) => {
  fs.appendFileSync(hits, "hit\n");
  res.end("ok");
});
server.on("error", (err) => {
  fs.writeFileSync(ready, "error: " + err.message);
  process.exit(1);
});
server.listen(sock, () => fs.writeFileSync(ready, "ready"));
EOF
  rm -f "$SOCK" "$SOCKREADY"
  node "$SOCKSERVER" "$SOCK" "$SOCKHITS" "$SOCKREADY" &
  SOCK_PID=$!
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    [ -f "$SOCKREADY" ] && break
    sleep 0.1
  done
  if [ "$(cat "$SOCKREADY" 2>/dev/null || true)" != "ready" ]; then
    bad "unix-socket listener never came up ($(cat "$SOCKREADY" 2>/dev/null || echo missing))"
  else
    export VERDICT_SANDBOX_SOCK="$SOCK"
    mkpatch 'curl -s --noproxy "*" --unix-socket "$VERDICT_SANDBOX_SOCK" http://localhost/hit >/dev/null && printf reached > sandbox-sock.txt'
    rm -f "$EVAL_SANDBOX/sandbox-sock.txt"
    : > "$SOCKHITS"
    run_review verify acme/app 5201 --json --timeout 10
    assert_exit "unsandboxed unix-socket control" 0
    assert_eq "unsandboxed unix-socket control reaches the listener" 1 "$(wc -l <"$SOCKHITS" | tr -d ' ')"
    if [ -f "$EVAL_SANDBOX/sandbox-sock.txt" ]; then
      good "unsandboxed unix-socket control exits clean"
    else
      bad "unsandboxed unix-socket control did not run"
    fi

    rm -f "$EVAL_SANDBOX/sandbox-sock.txt"
    : > "$SOCKHITS"
    run_review verify acme/app 5201 --sandbox --json --timeout 10
    assert_exit "sandboxed unix-socket run" 0
    if jq -e '.error == null and .data.sandboxed == true and .data.exit != 0' <"$EVAL_OUT" >/dev/null 2>&1; then
      good "sandboxed unix-socket run fails to connect (exit $(jq -r '.data.exit' <"$EVAL_OUT"))"
    else
      bad "sandboxed unix-socket run reached the socket or broke: $(head -c 300 "$EVAL_OUT")"
    fi
    assert_eq "sandboxed unix-socket run leaves the listener untouched" 0 "$(wc -l <"$SOCKHITS" | tr -d ' ')"
    if [ ! -e "$EVAL_SANDBOX/sandbox-sock.txt" ]; then
      good "sandboxed unix-socket run writes no marker"
    else
      bad "sandboxed unix-socket run wrote the marker"
    fi
  fi
else
  bad "no writable directory under /run for the unix-socket leg"
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
