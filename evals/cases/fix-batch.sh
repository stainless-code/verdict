#!/usr/bin/env bash
#
# fix --all: the batch form of the bounded fix loop, its patch output, and the
# path scope. Hermetic: fixtures are crafted local bundles, no network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

CRIT="c123456789abcdef0123456789abcdef01234567"
IMP="6123456789abcdef0123456789abcdef01234567"
INFO="1123456789abcdef0123456789abcdef01234567"

mk_local() { # $1 = bare slug (bundle lands at .verdict/state/local-$1)
  local slug="$1" dir="$EVAL_SANDBOX/.verdict/state/local-$1"
  mkdir -p "$dir"
  cat > "$dir/source.json" <<EOF
{"schema_version":"1","kind":"worktree","repo":"acme/app","slug":"$slug","base_ref":null,"head_ref":"work","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"","range":"HEAD:worktree","dirty":true,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
  cat > "$dir/patch.json" <<'EOF'
{"schema_version":"1","verifyCmd":"go test ./..."}
EOF
}

mk_finding() { # $1=sig $2=status $3=severity $4=path $5=body
  jq -n -c --arg sig "$1" --arg st "$2" --arg sev "$3" --arg p "$4" --arg b "$5" \
    '{kind:"inline",path:$p,heading:null,side:"RIGHT",line:2,anchored:true,body:$b,suggestion:null,inBounds:true,rigor:"pointed",severity:$sev,kb:[],sig:$sig,sig_version:"1",firstSeen:"aaaaaaa",lastSeen:"aaaaaaa",status:$st,rationale:"",history:[]}'
}

write_findings() { # $1 = bare slug, stdin = findings array
  cat > "$EVAL_SANDBOX/.verdict/state/local-$1/findings.json"
}

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "fix-all-eval@example.com"
  git config user.name "fix all eval"
  cat > .gitignore <<'EOF'
.verdict/state/
stubbin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
EOF
  printf 'line1\nline2\nline3\n' > notes.txt
  printf 'alpha\nbeta\ngamma\n' > app.txt
  printf 'other1\nother2\n' > other.txt
  git add -A
  git commit -qm init
)

mk_local batch
{
  mk_finding "$CRIT" open critical app.txt "Critical boundary bug here."
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
  mk_finding "$INFO" open informational notes.txt "Minor wording nit here."
} | jq -s '.' | write_findings batch
run_review fix acme/app local-batch --all --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "fix --all exits 0 with the bundle's patch.json verifyCmd"
else
  bad "fix --all exits $EVAL_STATUS"
fi
if [ "$(jq -r '.data.batch | length' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '[.data.batch[].severity] | join(",")' <"$EVAL_OUT")" = "critical,important" ] \
  && [ "$(jq -r '[.data.batch[].outcome] | join(",")' <"$EVAL_OUT")" = "ready,ready" ] \
  && [ "$(jq -r '.data.min_severity' <"$EVAL_OUT")" = "important" ] \
  && [ "$(jq -r '.data.verify_cmd' <"$EVAL_OUT")" = "go test ./..." ]; then
  good "batch selects critical+important, both ready, floor and verify cmd named"
else
  bad "batch data: $(jq -c '.data | {min_severity,verify_cmd,batch}' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq -r ".sigs[\"$IMP\"].passes" "$EVAL_SANDBOX/.verdict/state/local-batch/fix.json")" = "1" ] \
  && [ "$(jq -r ".sigs[\"$INFO\"] // \"absent\"" "$EVAL_SANDBOX/.verdict/state/local-batch/fix.json")" = "absent" ]; then
  good "ledger records one pass for the selected sigs only"
else
  bad "ledger: $(cat "$EVAL_SANDBOX/.verdict/state/local-batch/fix.json" 2>/dev/null)"
fi
if [ ! -e "$EVAL_SANDBOX/.verdict/state/local-batch/fix.patch" ]; then
  good "a clean worktree writes no fix.patch"
else
  bad "fix.patch appeared on a clean worktree"
fi

mk_local patchy
{
  mk_finding "$IMP" open important app.txt "Important scoping bug here."
} | jq -s '.' | write_findings patchy
printf 'alpha\nbeta fixed\ngamma\n' > "$EVAL_SANDBOX/app.txt"
run_review fix acme/app local-patchy --all --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.patch' <"$EVAL_OUT")" = ".verdict/state/local-patchy/fix.patch" ]; then
  good "a pass names its fix.patch beside the ledger"
else
  bad "patch data: $(jq -c '.data.patch' <"$EVAL_OUT" 2>/dev/null) exit $EVAL_STATUS"
fi
if [ -f "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.patch" ] \
  && grep -q '^+beta fixed$' "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.patch" \
  && grep -q '^diff --git a/app.txt b/app.txt$' "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.patch"; then
  good "fix.patch carries the worktree diff against HEAD"
else
  bad "fix.patch: $(head -c 200 "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.patch" 2>/dev/null)"
fi
before_ledger="$(cat "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.json")"
printf 'alpha\ntoken=ghp_0123456789abcdefghijklmnopqrstuvwxyz\ngamma\n' > "$EVAL_SANDBOX/app.txt"
run_review fix acme/app local-patchy --all --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "fix-patch-secret" ] \
  && grep -q 'github-token' "$EVAL_OUT"; then
  good "a secret-shaped patch refuses fix-patch-secret naming the shape"
else
  bad "secret patch: exit $EVAL_STATUS $(jq -c '.error' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(cat "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.json")" = "$before_ledger" ]; then
  good "the refused patch spends no pass"
else
  bad "refused patch moved the ledger"
fi
assert_file_nogrep "the refused patch writes no secret" \
  "$EVAL_SANDBOX/.verdict/state/local-patchy/fix.patch" 'ghp_'
printf 'alpha\nbeta fixed\ngamma\n' > "$EVAL_SANDBOX/app.txt"

mk_local scoped
{
  mk_finding "$CRIT" open critical app.txt "Critical boundary bug here."
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
} | jq -s '.' | write_findings scoped
run_review fix acme/app local-scoped --all --paths 'other.*' --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.batch | length' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.batch[0].locator' <"$EVAL_OUT")" = "other.txt:2" ] \
  && [ "$(jq -r '.data.paths | join(",")' <"$EVAL_OUT")" = "other.*" ]; then
  good "--paths scopes the batch to matching anchors"
else
  bad "paths scope: $(jq -c '.data | {paths,batch}' <"$EVAL_OUT" 2>/dev/null)"
fi
if grep -q 'batch of 1 selected under other\.\*' "$EVAL_ERR"; then
  good "the ledger line names the path scope"
else
  bad "ledger line: $(head -c 200 "$EVAL_ERR")"
fi
run_review fix acme/app local-scoped --all --paths 'docs/**' --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.batch | length' <"$EVAL_OUT")" = "0" ] \
  && grep -q 'under docs/\*\*' "$EVAL_ERR"; then
  good "a scope that matches nothing says so with the globs"
else
  bad "empty scope: exit $EVAL_STATUS $(head -c 200 "$EVAL_ERR")"
fi
run_review fix acme/app local-scoped --paths 'app.txt'
if [ "$EVAL_STATUS" -eq 2 ] && grep -q -- '--paths scopes a batch' "$EVAL_ERR"; then
  good "--paths without --all is a usage error"
else
  bad "--paths alone: exit $EVAL_STATUS $(head -c 160 "$EVAL_ERR")"
fi
run_review fix acme/app local-scoped --all --sig "$CRIT"
if [ "$EVAL_STATUS" -eq 2 ] && grep -q 'fix takes --sig or --all, not both' "$EVAL_ERR"; then
  good "--all with --sig is a usage error"
else
  bad "--all --sig: exit $EVAL_STATUS $(head -c 160 "$EVAL_ERR")"
fi

mk_local needverify
{
  mk_finding "$IMP" open important app.txt "Important scoping bug here."
} | jq -s '.' | write_findings needverify
rm "$EVAL_SANDBOX/.verdict/state/local-needverify/patch.json"
run_review fix acme/app local-needverify --all --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "no-verify-cmd" ]; then
  good "--all without a verify command refuses no-verify-cmd"
else
  bad "no verify cmd: exit $EVAL_STATUS $(jq -c '.error' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ ! -e "$EVAL_SANDBOX/.verdict/state/local-needverify/fix.json" ]; then
  good "a refusal on a fresh bundle leaves no fix.json"
else
  bad "the refusal created a ledger: $(cat "$EVAL_SANDBOX/.verdict/state/local-needverify/fix.json")"
fi

mk_local ceiling
{
  mk_finding "$CRIT" open critical app.txt "Critical boundary bug here."
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
} | jq -s '.' | write_findings ceiling
printf '{"sigs":{"%s":{"passes":3,"last_at":"2026-01-01T00:00:00Z","baseline":["app.txt","notes.txt","other.txt"]}}}' "$CRIT" \
  >"$EVAL_SANDBOX/.verdict/state/local-ceiling/fix.json"
run_review fix acme/app local-ceiling --all --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.batch[0].outcome' <"$EVAL_OUT")" = "ceiling" ] \
  && [ "$(jq -r '.data.batch[1].outcome' <"$EVAL_OUT")" = "ready" ]; then
  good "a ceiling-exhausted sig is reported beside the ready one"
else
  bad "ceiling batch: $(jq -c '.data.batch' <"$EVAL_OUT" 2>/dev/null)"
fi

eval_end
