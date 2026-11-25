#!/usr/bin/env bash
# eval: serial
#
# verify --sandbox: on a host that can enforce a mechanism the command runs
# contained — bubblewrap on Linux (no network, hidden credentials, writable
# repo, private /tmp), sandbox-exec on macOS (no network, loopback included;
# hidden credentials, no writes outside the repo and a private TMPDIR) —
# and on every other host the run refuses sandbox-unavailable and executes
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
# Every path this case puts in the shared host /tmp lives under one fresh
# mktemp directory (or carries its random suffix), never a PID a parallel run
# could reuse.
HOSTTMPDIR="$(mktemp -d /tmp/verdict-sandbox.XXXXXX)"
UNIQ="${HOSTTMPDIR##*.}"
HITS="$EVAL_SANDBOX/listener-hits.log"
HOST_SENTINEL="$HOSTTMPDIR/host-sentinel"
READY="$EVAL_SANDBOX/listener-ready"
SERVER="$EVAL_SANDBOX/listener.js"
FAKEHOME="$EVAL_SANDBOX/fake-home"
MOVEDGH="$EVAL_SANDBOX/moved-gh"
PAYLOAD_SRC="$HOSTTMPDIR/verify-fuzz-src"
PAYLOAD_BIN="$HOSTTMPDIR/verify-fuzz-fetched"
mkdir -p "$DIR" \
  "$FAKEHOME/.aws" "$FAKEHOME/.ssh" "$FAKEHOME/.config/gh" \
  "$FAKEHOME/.docker" "$FAKEHOME/.kube" "$FAKEHOME/.config/gcloud" \
  "$FAKEHOME/.azure" "$FAKEHOME/.gnupg" "$FAKEHOME/.terraform.d" "$FAKEHOME/.cargo" "$MOVEDGH"
: > "$HITS"
# The reviewed root is a git checkout from the start: on Linux, --sandbox
# refuses a root with no .git, because bwrap could make <root>/.git read-only
# only by creating a mount point in the host tree.
(cd "$EVAL_SANDBOX" && { [ -d .git ] || git init -q; })
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
printf '{"credentials":{"app.terraform.io":{"token":"sandbox-sentinel"}}}\n' > "$FAKEHOME/.terraform.d/credentials.tfrc.json"
printf '[registry]\ntoken = "sandbox-sentinel"\n' > "$FAKEHOME/.cargo/credentials.toml"
printf 'sandbox-sentinel\n' > "$FAKEHOME/.vault-token"
# A store moved by an environment variable the child never receives: only the
# verdict process sees GH_CONFIG_DIR, and the sandbox must still hide it.
printf 'github.com:\n  oauth_token: sandbox-sentinel\n' > "$MOVEDGH/hosts.yml"

# pasteboard_restore writes the saved host find pasteboard back, byte for
# byte (from a file, so trailing newlines survive). The backup is forgotten
# only once pbcopy succeeds; a failed restore keeps it for the next attempt.
pasteboard_restore() {
  if [ -n "${PB_SAVED:-}" ]; then
    if pbcopy -pboard find < "$PB_SAVED" 2>/dev/null; then
      PB_SAVED=""
    else
      printf 'verify-sandbox: could not restore the host find pasteboard from %s\n' "$PB_SAVED" >&2
    fi
  fi
}

# pasteboard_keep runs at exit, after the last restore attempt: a backup that
# still could not be restored is moved out of the temp tree the trap removes,
# and its path is reported so the operator can restore it by hand.
pasteboard_keep() {
  if [ -n "${PB_SAVED:-}" ]; then
    local keep
    keep="$(mktemp "${TMPDIR:-/tmp}/verdict-eval-find-pasteboard.XXXXXX")"
    cp "$PB_SAVED" "$keep"
    printf 'verify-sandbox: the host find pasteboard was not restored; its saved text is in %s\n' "$keep" >&2
  fi
}

listener_stop() {
  pasteboard_restore
  pasteboard_keep
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
  rm -rf "$HOSTTMPDIR"
  rm -f "/tmp/sandbox-tmp.$UNIQ.txt"
}
trap listener_stop EXIT

sandbox_works() {
  bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid --tmpfs /tmp \
    --tmpfs /run --unshare-net --new-session --die-with-parent -- true >/dev/null 2>&1
}

sandbox_exec_works() {
  [ -x /usr/bin/sandbox-exec ] \
    && /usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' true >/dev/null 2>&1
}

# mechanism is the runner this host enforces, or empty when it cannot.
mechanism() {
  if sandbox_works; then
    printf 'bwrap'
  elif sandbox_exec_works; then
    printf 'sandbox-exec'
  fi
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

# out is the last run's recorded output tail. verify runs the command in a
# throwaway checkout of the content it grades, gone once the run ends, so
# every leg reports what it saw on stdout instead of in a marker file.
out() {
  jq -r '.data.outputTail // empty' <"$EVAL_OUT"
}

# macos_service_legs proves the Mach default-deny end to end: cfprefsd cannot
# persist a `defaults write` for the command, and the pasteboard can be
# neither read nor written. The pasteboard leg uses the find pasteboard and
# restores it, so the operator's clipboard is never touched.
macos_service_legs() {
  local domain="dev.verdict.eval.$UNIQ"
  export VERDICT_SANDBOX_DOMAIN="$domain"
  mkpatch 'defaults write "$VERDICT_SANDBOX_DOMAIN" k v >/dev/null 2>&1; defaults write -g "$VERDICT_SANDBOX_DOMAIN" v >/dev/null 2>&1; printf ran'
  run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_DOMAIN --json --timeout 10
  assert_exit "sandboxed defaults write run" 0
  if [ "$(out)" = "ran" ] \
    && ! defaults read "$domain" k >/dev/null 2>&1 && ! defaults read -g "$domain" >/dev/null 2>&1; then
    good "a defaults write inside the sandbox persists nothing on the host"
  else
    bad "a defaults write inside the sandbox reached the host preferences"
  fi
  defaults delete "$domain" >/dev/null 2>&1 || true
  defaults delete -g "$domain" >/dev/null 2>&1 || true
  rm -f "$HOME/Library/Preferences/$domain.plist"

  # The host find pasteboard is shared state: the case runs serially (see
  # the header). It is saved to a file under the host temp directory the
  # sandbox cannot reach, and the EXIT trap restores it on every path out,
  # a failing step under `set -e` included. A failed save changes nothing.
  local saved_file="$HOSTTMPDIR/find-pasteboard.saved"
  if pbpaste -pboard find > "$saved_file" 2>/dev/null; then
    PB_SAVED="$saved_file"
  fi
  if [ -z "${PB_SAVED:-}" ] || ! printf 'verdict-eval-sentinel' | pbcopy -pboard find 2>/dev/null \
    || [ "$(pbpaste -pboard find 2>/dev/null)" != "verdict-eval-sentinel" ]; then
    pasteboard_restore
    if [ "${EVAL_REQUIRE_SANDBOX:-0}" = "1" ]; then
      bad "EVAL_REQUIRE_SANDBOX=1 but the host pasteboard control failed"
    else
      skip "no usable pasteboard server on this host for the pasteboard leg"
    fi
    return 0
  fi
  mkpatch 'r=$(pbpaste -pboard find 2>/dev/null) && printf read || printf denied; [ -z "$r" ] || printf %s -leaked; printf overwritten | pbcopy -pboard find 2>/dev/null && printf %s -wrote || printf %s -denied'
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandboxed pasteboard run" 0
  if [ "$(out)" = "denied-denied" ] \
    && [ "$(pbpaste -pboard find 2>/dev/null)" = "verdict-eval-sentinel" ]; then
    good "the pasteboard can be neither read nor written inside the sandbox"
  else
    bad "the sandbox reached the pasteboard ($(out))"
  fi
  pasteboard_restore
}

# The refusal leg runs wherever the sandbox cannot be enforced. The sentinel
# would be written by the command at a host path (an unsandboxed run's
# checkout is thrown away), so its absence proves nothing executed.
refusal_leg() {
  export VERDICT_SANDBOX_SENTINEL="$HOSTTMPDIR/sandbox-sentinel.txt"
  mkpatch 'printf created > "$VERDICT_SANDBOX_SENTINEL"'
  rm -f "$VERDICT_SANDBOX_SENTINEL" "$DIR/.verify.out"
  local before after
  before="$(patch_hash)"
  run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_SENTINEL --json --timeout 10
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
  if [ ! -e "$VERDICT_SANDBOX_SENTINEL" ]; then
    good "refusal executed nothing (no sentinel)"
  else
    bad "refusal ran the command unsandboxed (sentinel present)"
  fi
  if jq -r '.error.message // empty' <"$EVAL_OUT" | matches -i -E "bwrap|namespace|enforced sandbox|sandbox-exec"; then
    good "refusal names the mechanism cause"
  else
    bad "refusal does not name the mechanism cause: $(head -c 200 "$EVAL_OUT")"
  fi
}

MECH="$(mechanism)"
if [ -z "$MECH" ]; then
  if [ "${EVAL_REQUIRE_SANDBOX:-0}" = "1" ]; then
    bad "EVAL_REQUIRE_SANDBOX=1 but no sandbox mechanism can be enforced here"
  fi
  refusal_leg
  eval_end
  exit 0
fi
good "sandbox mechanism: $MECH"

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
mkpatch 'curl -s http://127.0.0.1:$PORT/hit >/dev/null && printf reached'
: > "$HITS"
run_review verify acme/app 5201 --env-allowlist PORT --json --timeout 10
assert_exit "unsandboxed control" 0
assert_eq "unsandboxed control reaches the listener" 1 "$(hits)"
if [ "$(out)" = "reached" ]; then
  good "unsandboxed control exits clean"
else
  bad "unsandboxed control did not run"
fi

# The same command under --sandbox cannot reach the listener: the child gets
# its own loopback, so the connection fails and the marker is never printed.
: > "$HITS"
run_review verify acme/app 5201 --sandbox --env-allowlist PORT --json --timeout 10
assert_exit "sandboxed listener run" 0
if jq -e '.error == null and .data.sandboxed == true and .data.exit != 0' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "sandboxed listener run fails to connect (exit $(jq -r '.data.exit' <"$EVAL_OUT"))"
else
  bad "sandboxed listener run reached the listener or broke: $(head -c 300 "$EVAL_OUT")"
fi
assert_eq "sandboxed listener run leaves the listener untouched" 0 "$(hits)"
if [ "$(out)" != "reached" ]; then
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
assert_json_field "sandboxed benign run records the mechanism" "$MECH" '.data.sandboxMechanism'
assert_json_field "sandboxed benign run exit" "0" '.data.exit'
assert_json_field "sandboxed benign run tail" "ok" '.data.outputTail'
if jq -e '.data.tree | type == "string" and length == 40' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "sandboxed benign run ran in a snapshot and records its content tree"
else
  bad "sandboxed benign run records no content tree (it ran in place): $(head -c 300 "$EVAL_OUT")"
fi
assert_json_field "patch record carries sandboxed" "true" '.sandboxed' "$DIR/patch.json"
assert_json_field "patch record carries the mechanism" "$MECH" '.sandboxMechanism' "$DIR/patch.json"
assert_json_field "patch record carries exit" "0" '.verifyExit' "$DIR/patch.json"
if [ -f "$DIR/.verify.out" ] && [ "$(cat "$DIR/.verify.out")" = "ok" ]; then
  good "sandboxed benign run writes .verify.out"
else
  bad "sandboxed benign run lost .verify.out"
fi

# Writes to the checkout the command runs in succeed under both mechanisms:
# the snapshot is the writable root. The checkout under review stays
# untouched: the snapshot's files are thrown away, and a write aimed at the
# real checkout is denied.
mkpatch "printf repo > sandbox-repo.txt && cat sandbox-repo.txt"
rm -f "$EVAL_SANDBOX/sandbox-repo.txt"
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed repo write run" 0
if [ "$(out)" = "repo" ]; then
  good "repo write succeeds inside the sandbox"
else
  bad "repo write failed inside the sandbox ($(out))"
fi
if [ ! -e "$EVAL_SANDBOX/sandbox-repo.txt" ]; then
  good "a write in the snapshot never reaches the reviewed checkout"
else
  bad "a write in the snapshot reached the reviewed checkout"
fi
export VERDICT_SANDBOX_REAL="$EVAL_SANDBOX/sandbox-real.txt"
mkpatch '{ printf x > "$VERDICT_SANDBOX_REAL"; } 2>/dev/null && printf leak || printf denied'
rm -f "$VERDICT_SANDBOX_REAL"
run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_REAL --json --timeout 10
assert_exit "sandboxed write to the reviewed checkout" 0
if [ "$(out)" = "denied" ] && [ ! -e "$VERDICT_SANDBOX_REAL" ]; then
  good "a write to the reviewed checkout is denied inside the sandbox"
else
  bad "a write to the reviewed checkout landed ($(out))"
  rm -f "$VERDICT_SANDBOX_REAL"
fi

if [ "$MECH" = "bwrap" ]; then
  # The full shape mounts a private tmpfs: the write works, the host cannot
  # see the file.
  mkpatch "printf tmp > /tmp/sandbox-tmp.$UNIQ.txt && printf both"
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandboxed /tmp write run" 0
  if [ "$(out)" = "both" ]; then
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
else
  # Seatbelt denies the shared host /tmp and allows the per-run private
  # TMPDIR, which the wrapper exports to the child.
  mkpatch 'printf private > "$TMPDIR/verdict-eval-tmp.txt" && cat "$TMPDIR/verdict-eval-tmp.txt"'
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandboxed private TMPDIR run" 0
  if [ "$(out)" = "private" ]; then
    good "the child writes and reads inside its private TMPDIR"
  else
    bad "the child could not use its private TMPDIR"
  fi
  printf 'host-secret\n' > "$HOST_SENTINEL"
  mkpatch "cat $HOST_SENTINEL >/dev/null 2>&1 && printf leak || printf denied"
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandboxed host /tmp read run" 0
  if [ "$(out)" = "denied" ]; then
    good "the shared host /tmp is not readable inside the sandbox"
  else
    bad "the shared host /tmp is readable inside the sandbox"
  fi
  rm -f "$HOST_SENTINEL"
  mkpatch "{ printf nope > $HOSTTMPDIR/written.txt; } 2>/dev/null && printf leak || printf denied"
  rm -f "$HOSTTMPDIR/written.txt"
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandboxed host /tmp write run" 0
  if [ "$(out)" = "denied" ]; then
    good "the shared host /tmp is not writable inside the sandbox"
  else
    bad "the shared host /tmp is writable inside the sandbox"
  fi
  if [ ! -e "$HOSTTMPDIR/written.txt" ]; then
    good "the host /tmp saw no sandboxed write"
  else
    bad "the host /tmp saw the sandboxed write"
  fi
  # Every write outside the reviewed root and the private TMPDIR is denied,
  # not only the temp roots: a sibling of the repo is the persistence shape (a
  # LaunchAgent, a shell rc file) the profile closes.
  OUTSIDE="$(dirname "$EVAL_SANDBOX")/verdict-outside-$UNIQ.txt"
  export VERDICT_SANDBOX_OUTSIDE="$OUTSIDE"
  mkpatch '{ printf nope > "$VERDICT_SANDBOX_OUTSIDE"; } 2>/dev/null && printf leak || printf denied'
  rm -f "$OUTSIDE"
  run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_OUTSIDE --json --timeout 10
  assert_exit "sandboxed write outside the root" 0
  if [ "$(out)" = "denied" ] && [ ! -e "$OUTSIDE" ]; then
    good "a write outside the reviewed root is denied inside the sandbox"
  else
    bad "a write outside the reviewed root landed ($(out))"
    rm -f "$OUTSIDE"
  fi
  macos_service_legs
fi

# credential_legs proves the fake HOME's credential dirs and files stay
# unreadable under both mechanisms.
credential_legs() {

# Credential paths exist on the host but not inside the sandbox. Names are
# assembled from variables so the static gate stays out of the way: this leg
# grades the sandbox, not the string scan.
OLD_HOME="$HOME"
HOME="$FAKEHOME"
mkpatch 'h=$(printenv HOME); a=.aws; b=.ssh; c=.config; g=gh; [ -e "$h/$a/credentials" ] || [ -e "$h/$b/id_rsa" ] || [ -e "$h/$c/$g/hosts.yml" ] && printf leak || printf hidden'
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed credential dirs run" 0
if [ "$(out)" = "hidden" ]; then
  good "the credential dir is empty inside the sandbox"
else
  bad "the credential dir is visible inside the sandbox ($(out))"
fi
mkpatch 'h=$(printenv HOME); n=.netrc; g=.git-credentials; d=.docker; k=.kube; c=.config; cl=gcloud; az=.azure; gp=.gnupg; m=.npmrc; p=.pypirc; f=config.json; a=application_default_credentials.json; t=accessTokens.json; s=secring.gpg; tf=.terraform.d; tfc=credentials.tfrc.json; cg=.cargo; cgc=credentials.toml; v=.vault-token; [ -s "$h/$n" ] || [ -s "$h/$g" ] || [ -s "$h/$d/$f" ] || [ -s "$h/$k/$f" ] || [ -s "$h/$c/$cl/$a" ] || [ -s "$h/$az/$t" ] || [ -s "$h/$gp/$s" ] || [ -s "$h/$m" ] || [ -s "$h/$p" ] || [ -s "$h/$tf/$tfc" ] || [ -s "$h/$cg/$cgc" ] || [ -s "$h/$v" ] && printf leak || printf hidden'
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed credential files run" 0
if [ "$(out)" = "hidden" ]; then
  good "the credential files are empty inside the sandbox"
else
  bad "a credential file is visible inside the sandbox ($(out))"
fi
# GH_CONFIG_DIR rides only the verdict process environment (it is not on the
# child allowlist), so the hidden store proves the host environment is read.
# The store sits in the reviewed checkout, not in the content the command
# runs on, so the path reaches the command through the environment.
export VERDICT_SANDBOX_MOVED="$MOVEDGH"
mkpatch 'y=hosts.yml; [ -s "$VERDICT_SANDBOX_MOVED/$y" ] && printf leak || printf hidden'
GH_CONFIG_DIR="$MOVEDGH" run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_MOVED --json --timeout 10
HOME="$OLD_HOME"
assert_exit "sandboxed relocated credential run" 0
if [ "$(out)" = "hidden" ]; then
  good "a store moved by a host-only GH_CONFIG_DIR is hidden inside the sandbox"
else
  bad "the GH_CONFIG_DIR store is visible inside the sandbox ($(out))"
fi

}

bwrap_legs() {

# Host PIDs and their environments are outside the sandbox. The canary is a
# same-uid host process carrying a sentinel value: today its environment is
# readable through the host /proc, so the leg fails unless the sandbox gets
# its own PID namespace and proc mount. The proc path rides an env var so the
# static gate, which refuses /proc text, leaves this leg to the sandbox.
env VERDICT_SANDBOX_CANARY=sandbox-sentinel sleep 120 &
CANARY_PID=$!
export VERDICT_SANDBOX_PROC=/proc
export VERDICT_SANDBOX_HOSTPID=$$
mkpatch "c=\"\$VERDICT_SANDBOX_PROC/$CANARY_PID\"; s=\"\$VERDICT_SANDBOX_PROC/self\"; h=\"\$VERDICT_SANDBOX_PROC/\$VERDICT_SANDBOX_HOSTPID\"; if grep -q sandbox-sentinel \"\$c/environ\" 2>/dev/null; then printf leak; elif [ -e \"\$h\" ]; then printf leak; elif [ -r \"\$s/environ\" ] && [ -d \"\$VERDICT_SANDBOX_PROC/1\" ]; then printf hidden; else printf broken; fi"
run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_PROC,VERDICT_SANDBOX_HOSTPID --json --timeout 10
assert_exit "sandboxed /proc run" 0
if [ "$(out)" = "hidden" ]; then
  good "no host PID or environment is visible in /proc"
else
  bad "host /proc is visible inside the sandbox ($(out))"
fi

# /run is empty: the session bus, docker.sock and agent sockets are gone. The
# path is assembled so the static gate's absolute-path arm leaves this leg to
# the sandbox.
mkpatch 'z=; r=$z/run; if [ -z "$(ls -A "$r" 2>/dev/null)" ]; then printf empty; else printf leak; fi'
run_review verify acme/app 5201 --sandbox --json --timeout 10
assert_exit "sandboxed /run run" 0
if [ "$(out)" = "empty" ]; then
  good "/run is empty inside the sandbox"
else
  bad "/run is shared with the host ($(out))"
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
    mkpatch 'curl -s --noproxy "*" --unix-socket "$VERDICT_SANDBOX_SOCK" http://localhost/hit >/dev/null && printf reached'
    : > "$SOCKHITS"
    run_review verify acme/app 5201 --env-allowlist VERDICT_SANDBOX_SOCK --json --timeout 10
    assert_exit "unsandboxed unix-socket control" 0
    assert_eq "unsandboxed unix-socket control reaches the listener" 1 "$(wc -l <"$SOCKHITS" | tr -d ' ')"
    if [ "$(out)" = "reached" ]; then
      good "unsandboxed unix-socket control exits clean"
    else
      bad "unsandboxed unix-socket control did not run"
    fi

    : > "$SOCKHITS"
    run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_SOCK --json --timeout 10
    assert_exit "sandboxed unix-socket run" 0
    if jq -e '.error == null and .data.sandboxed == true and .data.exit != 0' <"$EVAL_OUT" >/dev/null 2>&1; then
      good "sandboxed unix-socket run fails to connect (exit $(jq -r '.data.exit' <"$EVAL_OUT"))"
    else
      bad "sandboxed unix-socket run reached the socket or broke: $(head -c 300 "$EVAL_OUT")"
    fi
    assert_eq "sandboxed unix-socket run leaves the listener untouched" 0 "$(wc -l <"$SOCKHITS" | tr -d ' ')"
    if [ "$(out)" != "reached" ]; then
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
  cmd="${cmd//\/tmp\/verify-fuzz-src/$PAYLOAD_SRC}"
  cmd="${cmd//\/tmp\/verify-fuzz-fetched/$PAYLOAD_BIN}"
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

}

credential_legs

# The git control files are the one part of a checkout the PR cannot plant
# itself, and git runs them later outside any sandbox. The command runs in a
# snapshot: a linked worktree whose `.git` file points into the reviewed
# checkout's git directory. A hook write there must fail under both
# mechanisms while `git status` keeps working, and so must rewriting the
# snapshot's `.git` pointer and minting a second name for the git config
# (Seatbelt denies the link; under bwrap link(2) across the read-only bind
# fails EXDEV), since a hardlink would write the file past every path rule.
link_count() {
  ls -l "$1" | awk '{print $2}'
}

git_legs() {
  local cfg="$EVAL_SANDBOX/.git/config" links_before
  rm -f "$EVAL_SANDBOX/.git/hooks/pre-commit" "$EVAL_SANDBOX/sandbox-git-link"
  # Positive control: the same link succeeds outside the sandbox, so a denied
  # one below proves the boundary, not a missing `ln` or git config. It is
  # removed before the run, which would otherwise refuse the hard link.
  if (cd "$EVAL_SANDBOX" && ln .git/config sandbox-git-link 2>/dev/null) && [ -f "$EVAL_SANDBOX/sandbox-git-link" ]; then
    rm -f "$EVAL_SANDBOX/sandbox-git-link"
  else
    bad "the unsandboxed control could not hardlink .git/config"
  fi
  links_before="$(link_count "$cfg")"
  # The command finds the git directory through the snapshot's `.git`
  # pointer; hook=nogitdir means it could not, and fails the leg rather than
  # passing a denial nobody attempted.
  mkpatch 'if d=$(git rev-parse --git-common-dir 2>/dev/null) && [ -d "$d/hooks" ]; then { printf x > "$d/hooks/pre-commit"; } 2>/dev/null && printf hook=leak || printf hook=denied; else d=missing; printf hook=nogitdir; fi; { printf x >> .git; } 2>/dev/null && printf %s ,pointer=leak || printf %s ,pointer=denied; ln "$d/config" sandbox-git-link 2>/dev/null && printf %s ,ln=leak || printf %s ,ln=denied; git status --short >/dev/null 2>&1 && printf %s ,status=ok || printf %s ,status=broken'
  run_review verify acme/app 5201 --sandbox --json --timeout 10
  assert_exit "sandboxed git control run" 0
  local got
  got="$(out)"
  case "$got" in
    hook=denied,*) if [ ! -e "$EVAL_SANDBOX/.git/hooks/pre-commit" ]; then
        good "a write to the git directory's hooks is denied inside the sandbox"
      else
        bad "a hook was planted inside the sandbox (no error, file present)"
      fi ;;
    *) bad "a hook was planted inside the sandbox ($got)" ;;
  esac
  case "$got" in
    *,pointer=denied,*) good "the snapshot's .git pointer cannot be rewritten inside the sandbox" ;;
    *) bad "the snapshot's .git pointer was rewritten inside the sandbox ($got)" ;;
  esac
  if case "$got" in *,ln=denied,*) true ;; *) false ;; esac && [ "$(link_count "$cfg")" = "$links_before" ]; then
    good "hardlinking the git config is denied inside the sandbox"
  else
    bad "the sandbox minted a second name for the git config ($got)"
  fi
  case "$got" in
    *,status=ok) good "git status still works inside the sandbox" ;;
    *) bad "git status broke inside the sandbox ($got)" ;;
  esac
}

git_legs

# A nested repository in the reviewed checkout is git control state the
# content tree never carries (git records it as a gitlink, never its `.git`),
# so the command, running in the snapshot, must not reach it either: a write
# to `nested-repo/.git/hooks` in the reviewed checkout must fail under both
# mechanisms. The positive control writes the same file outside the sandbox
# first, so the denial proves the boundary, not a missing directory. The
# nested repository carries a commit, so the snapshot can record it.
nested_git_leg() {
  local nested="$EVAL_SANDBOX/nested-repo"
  rm -rf "$nested"
  git init -q "$nested"
  git -C "$nested" -c user.name=eval -c user.email=eval@localhost commit -q --allow-empty -m nested
  mkdir -p "$nested/.git/hooks"
  if printf x > "$nested/.git/hooks/sandbox-control" 2>/dev/null && [ -f "$nested/.git/hooks/sandbox-control" ]; then
    rm -f "$nested/.git/hooks/sandbox-control"
  else
    bad "the unsandboxed control could not write nested-repo/.git/hooks"
  fi
  export VERDICT_SANDBOX_NESTED="$nested"
  mkpatch '{ printf x > "$VERDICT_SANDBOX_NESTED/.git/hooks/pre-commit"; } 2>/dev/null && printf leak || printf denied'
  run_review verify acme/app 5201 --sandbox --env-allowlist VERDICT_SANDBOX_NESTED --json --timeout 10
  assert_exit "sandboxed nested git run" 0
  if [ "$(out)" = "denied" ] && [ ! -e "$nested/.git/hooks/pre-commit" ]; then
    good "a write to a nested repository's .git/hooks is denied inside the sandbox"
  else
    bad "a hook was planted in a nested repository inside the sandbox ($(out))"
  fi
  rm -rf "$nested"
}

nested_git_leg

if [ "$MECH" = "bwrap" ]; then
  bwrap_legs
fi

eval_end
