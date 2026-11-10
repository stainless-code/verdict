#!/usr/bin/env bash
#
# doctor checks one host: `--provider gitlab` reads through the network-dead
# `glab` stub and shows no gh row, so a machine without gh is still green for a
# GitLab run, while a bare doctor keeps today's GitHub preflight.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"
# shellcheck source=../lib/hoststub.sh
. "$HERE/../lib/hoststub.sh"

FIX="$EVAL_ROOT/evals/fixtures/gitlab"
MINBIN="$EVAL_SANDBOX/minbin"
NOGLAB="$EVAL_SANDBOX/noglabbin"
GITHUB_ROWS="gh-cli,gh-auth,token-scopes,ps,git,api-reachable,storage-writable"
GITLAB_ROWS="glab-cli,glab-auth,ps,git,api-reachable,storage-writable"

hoststub_init glab
hoststub_prove_dead glab
hoststub_route glab "--version" "$FIX/glab-version.txt"
hoststub_route glab "auth status" "$FIX/glab-auth-status.txt"
hoststub_route glab "api user" "$FIX/glab-user.json"

# S1: the bare verb keeps the GitHub rows and never touches glab.
export GH_STUB_DOCTOR="repo"
run_review doctor --json
assert_exit "default doctor" 0
assert_json_field "the bare doctor keeps today's rows" "$GITHUB_ROWS" '[.data.checks[].name] | join(",")'
assert_json_field "the bare doctor is healthy" true '.data.healthy'
assert_json_field "the gh token row reads the stub scopes" "x-oauth-scopes: repo" \
  '.data.checks[] | select(.name == "token-scopes") | .detail'
assert_eq "the bare doctor shells no glab" 0 "$(hoststub_count glab)"
unset GH_STUB_DOCTOR

# S2: a PATH carrying the glab stub and no gh at all.
mkdir -p "$MINBIN" "$NOGLAB"
for tool in bash cat dirname git ps readlink; do
  ln -s "$(command -v "$tool")" "$MINBIN/$tool"
  ln -s "$(command -v "$tool")" "$NOGLAB/$tool"
done
cp "$EVAL_STUBBIN/glab" "$MINBIN/glab"
chmod +x "$MINBIN/glab"
assert_eq "the glab leg has no gh on PATH" "" "$(PATH="$MINBIN" command -v gh || true)"
set +e
( cd "$EVAL_SANDBOX" && PATH="$MINBIN" "$EVAL_CLI" doctor --provider gitlab --json >"$EVAL_OUT" 2>"$EVAL_ERR" )
st=$?
set -e
if [ "$st" -eq 0 ]; then
  good "doctor --provider gitlab without gh exits 0"
else
  bad "doctor --provider gitlab without gh exits $st: $(head -c 300 "$EVAL_ERR")"
fi
assert_json_field "the gitlab preflight carries no gh row" "$GITLAB_ROWS" '[.data.checks[].name] | join(",")'
assert_json_field "the gitlab preflight is healthy" true '.data.healthy'
assert_json_field "glab-cli reports the stub version" "glab 1.40.0 (2024-11-01)" \
  '.data.checks[] | select(.name == "glab-cli") | .detail'
assert_json_field "glab-auth reads the stub login" authenticated \
  '.data.checks[] | select(.name == "glab-auth") | .detail'
assert_json_field "api-reachable reads through glab" "GitLab API reachable" \
  '.data.checks[] | select(.name == "api-reachable") | .detail'
hoststub_argv_log glab "$EVAL_SANDBOX/glab-calls.txt"
assert_file_eq "the gitlab preflight issues the registered argvs in order" \
  "$FIX/expected-doctor-argvs.txt" "$EVAL_SANDBOX/glab-calls.txt"

# S3: an unknown host refuses typed, naming the accepted enum.
run_review doctor --provider gitea --json
assert_exit "doctor --provider gitea refuses" 1
assert_json_field "the unknown host refusal is typed" bad-provider '.error.code'
assert_file_grep "the refusal names the accepted hosts" "$EVAL_OUT" \
  "unknown --provider 'gitea' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)" -F
assert_eq "the refused run keeps the glab call count" 3 "$(hoststub_count glab)"

# S4: the same leg without glab fails the host CLI row instead of passing.
set +e
( cd "$EVAL_SANDBOX" && PATH="$NOGLAB" "$EVAL_CLI" doctor --provider gitlab --json >"$EVAL_OUT" 2>"$EVAL_ERR" )
st=$?
set -e
if [ "$st" -eq 1 ]; then
  good "doctor --provider gitlab without glab exits 1"
else
  bad "doctor --provider gitlab without glab exits $st, want 1"
fi
assert_json_field "the missing host CLI is a failing row" false \
  '.data.checks[] | select(.name == "glab-cli") | .ok'
assert_json_field "the missing host CLI is not healthy" false '.data.healthy'
assert_file_grep "the missing host CLI names the install" "$EVAL_OUT" "install gitlab.com/gitlab-org/cli (glab)"
assert_eq "the refused leg shells no glab" 3 "$(hoststub_count glab)"
hoststub_assert_all_routed

eval_end
