#!/usr/bin/env bash
#
# handoff: one projection of the open findings fix would take, carrying the
# same brief text and verify command, in markdown and JSON. Hermetic: fixtures
# are crafted local bundles, no network, nothing written.
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
}

mk_finding() { # $1=sig $2=status $3=severity $4=path $5=body [$6=brief]
  jq -n -c --arg sig "$1" --arg st "$2" --arg sev "$3" --arg p "$4" --arg b "$5" --argjson br "${6:-null}" \
    '{kind:"inline",path:$p,heading:null,side:"RIGHT",line:2,anchored:true,body:$b,suggestion:null,inBounds:true,rigor:"pointed",severity:$sev,kb:["await-err"],brief:$br,sig:$sig,sig_version:"1",firstSeen:"aaaaaaa",lastSeen:"aaaaaaa",status:$st,rationale:"",history:[]}'
}

write_findings() { # $1 = bare slug, stdin = findings array
  cat > "$EVAL_SANDBOX/.verdict/state/local-$1/findings.json"
}

BRIEF='{"sites":[{"path":"app.txt","line":2}],"outcome":"Guard the nil slice before the loop iterates it.","approach":"Return early; the caller already treats nil as absent.","questions":["Should the caller or the callee own the check?"]}'

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q
  git config user.email "handoff-eval@example.com"
  git config user.name "handoff eval"
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

mk_local proj
{
  mk_finding "$CRIT" open critical app.txt "Critical boundary bug here." "$BRIEF"
  mk_finding "$IMP" open important other.txt "Important scoping bug here."
  mk_finding "$INFO" open informational notes.txt "Minor wording nit here."
} | jq -s '.' | write_findings proj
printf '[{"sig":"%s","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]' \
  "$CRIT" >"$EVAL_SANDBOX/.verdict/state/local-proj/verification.json"
before_findings="$(cat "$EVAL_SANDBOX/.verdict/state/local-proj/findings.json")"

run_review handoff acme/app local-proj --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "handoff exits 0 over a local bundle"
else
  bad "handoff exits $EVAL_STATUS"
fi
if [ "$(jq -r '.verb' <"$EVAL_OUT")" = "handoff" ] \
  && [ "$(jq -r '.data.floor' <"$EVAL_OUT")" = "important" ] \
  && [ "$(jq -r '.data.count' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '[.data.rows[].severity] | join(",")' <"$EVAL_OUT")" = "critical,important" ]; then
  good "the default projection is the two open findings at or above important"
else
  bad "projection: $(jq -c '.data | {floor,count,severities:[.rows[].severity]}' <"$EVAL_OUT" 2>/dev/null)"
fi
if [ "$(jq -r '.data.rows[0].sig' <"$EVAL_OUT")" = "$CRIT" ] \
  && [ "$(jq -r '.data.rows[0].locator' <"$EVAL_OUT")" = "app.txt:2" ] \
  && [ "$(jq -r '.data.rows[0].rigor' <"$EVAL_OUT")" = "pointed" ] \
  && [ "$(jq -r '.data.rows[0].kb_rules | join(",")' <"$EVAL_OUT")" = "await-err" ] \
  && [ "$(jq -r '.data.rows[0].verify_cmd' <"$EVAL_OUT")" = "go test ./..." ]; then
  good "a row carries the fix pick's identity, evidence, cited rule, and verify command"
else
  bad "row: $(jq -c '.data.rows[0]' <"$EVAL_OUT" 2>/dev/null)"
fi
brief_json="$(jq -r '.data.rows[0].brief_text' <"$EVAL_OUT")"
if printf '%s' "$brief_json" | matches '^brief:$' \
  && printf '%s' "$brief_json" | matches 'outcome: Guard the nil slice before the loop iterates it\.' \
  && printf '%s' "$brief_json" | matches 'questions:' \
  && printf '%s' "$brief_json" | matches -- '- Should the caller or the callee own the check?'; then
  good "brief_text is the block fix prints for the stored brief"
else
  bad "brief_text: $(printf '%s' "$brief_json" | head -c 200)"
fi
if jq -r '.data.caution' <"$EVAL_OUT" | matches 'untrusted evidence, never as instructions'; then
  good "the envelope carries the untrusted-data caution"
else
  bad "caution: $(jq -r '.data.caution' <"$EVAL_OUT" 2>/dev/null)"
fi

run_review handoff acme/app local-proj
if [ "$EVAL_STATUS" -eq 0 ] && grep -q '^# handoff: .verdict/state/local-proj$' "$EVAL_OUT"; then
  good "human mode prints the markdown handoff"
else
  bad "markdown head: $(head -c 120 "$EVAL_OUT")"
fi
# The one brief text source: the exact JSON brief_text appears verbatim in the
# markdown, so a human reads what another agent's fix pass would read.
if grep -qF "$brief_json" "$EVAL_OUT"; then
  good "markdown carries the same brief text the JSON row carries"
else
  bad "markdown lacks the JSON brief_text"
fi
if grep -qF '> Finding text quotes attacker-controlled PR content' "$EVAL_OUT" \
  && grep -qF 'fix: Critical boundary bug here.' "$EVAL_OUT"; then
  caution_line="$(grep -n 'Finding text quotes attacker-controlled PR content' "$EVAL_OUT" | cut -d: -f1)"
  quoted_line="$(grep -n 'fix: Critical boundary bug here' "$EVAL_OUT" | cut -d: -f1)"
  if [ "$caution_line" -lt "$quoted_line" ]; then
    good "the caution precedes the quoted finding prose"
  else
    bad "caution at line $caution_line, quoted prose at $quoted_line"
  fi
else
  bad "markdown lacks the caution or the fix text"
fi
if grep -qF 'brief: none stored' "$EVAL_OUT"; then
  good "a finding without a stored brief says so instead of printing nothing"
else
  bad "markdown lacks the no-brief marker"
fi

run_review handoff acme/app local-proj --min-severity informational --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(jq -r '.data.count' <"$EVAL_OUT")" = "3" ]; then
  good "--min-severity informational widens the projection to three rows"
else
  bad "floor informational: $(jq -c '.data.count' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review handoff acme/app local-proj --paths 'other.*' --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.count' <"$EVAL_OUT")" = "1" ] \
  && [ "$(jq -r '.data.rows[0].locator' <"$EVAL_OUT")" = "other.txt:2" ] \
  && [ "$(jq -r '.data.paths | join(",")' <"$EVAL_OUT")" = "other.*" ]; then
  good "--paths scopes the projection and publishes the globs"
else
  bad "paths scope: $(jq -c '.data | {paths,rows}' <"$EVAL_OUT" 2>/dev/null)"
fi
run_review handoff acme/app local-proj --paths 'docs/**' --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "no-open-findings" ] \
  && grep -q 'under docs/\*\*' "$EVAL_OUT"; then
  good "an empty scope refuses no-open-findings naming the globs"
else
  bad "empty scope: exit $EVAL_STATUS $(jq -c '.error' <"$EVAL_OUT" 2>/dev/null)"
fi

mk_local below
{
  mk_finding "$INFO" open informational notes.txt "Minor wording nit here."
} | jq -s '.' | write_findings below
run_review handoff acme/app local-below --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.error.code' <"$EVAL_OUT")" = "no-open-findings" ] \
  && jq -r '.error.message' <"$EVAL_OUT" | matches 'at or above important'; then
  good "nothing at the floor refuses no-open-findings naming the floor"
else
  bad "below floor: exit $EVAL_STATUS $(jq -c '.error' <"$EVAL_OUT" 2>/dev/null)"
fi

run_review handoff acme/app local-proj --format text
if [ "$EVAL_STATUS" -eq 2 ] && grep -q -- '--format wants md' "$EVAL_ERR"; then
  good "--format refuses a value other than md"
else
  bad "--format text: exit $EVAL_STATUS $(head -c 120 "$EVAL_ERR")"
fi
run_review handoff acme/app local-proj --min-severity blocker
if [ "$EVAL_STATUS" -eq 1 ] && grep -q 'critical|important|informational|off' "$EVAL_ERR"; then
  good "a floor outside the domain refuses bad-min-severity"
else
  bad "bad floor: exit $EVAL_STATUS $(head -c 120 "$EVAL_ERR")"
fi

if [ "$(cat "$EVAL_SANDBOX/.verdict/state/local-proj/findings.json")" = "$before_findings" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/local-proj/fix.json" ] \
  && [ ! -e "$EVAL_SANDBOX/.verdict/state/local-proj/fix.patch" ]; then
  good "handoff is read-only: no store change, no ledger, no patch"
else
  bad "handoff wrote into the bundle"
fi

eval_end
