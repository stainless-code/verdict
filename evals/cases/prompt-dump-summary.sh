#!/usr/bin/env bash
# Case prompt-dump-summary: read-only prompt dump on fetch + confidence-led summary.
#
# Grades (all hermetic — crafted bundles, stub gh, sandbox git worktree):
#   P1  dump parity (fetch-local): --show-prompts stdout == the assembled
#       files concatenated in documented order (SKILL.md, system, mode,
#       diagrams, meta, toc, roles, diff, recall when present,
#       advisories/panel when present).
#   P2  no extra store writes: with-flag vs without-flag bundles hold identical
#       file sets and identical diff bytes; stdout carries only the dump
#       (progress stays on stderr).
#   P3  no agent, no extra network: fetch-local makes zero gh calls; the
#       canned-gh fetch path makes the same call count with and without flag.
#   P4  dump parity (fetch): same concatenation contract on the PR path via a
#       canned gh stub (run with the sandbox prompt files present; missing
#       siblings warn, a missing preamble refuses — graded under F7).
#   P5  --json parity: with the flag the envelope gains data.prompt_dump equal
#       to the concatenation; without the flag the key is absent.
#   F7  missing prompt files fail loudly: no prompts/system.md refuses typed
#       (prompt-dump-failed, no dump on stdout); a missing sibling prompt
#       warns on stderr (+ envelope warnings[] under --json) while the dump
#       proceeds without it.
#   S1  report --output leads with confidence + safety from the stored record,
#       keeps machine markers, and omits the checked list when findings stand.
#   S2  clean reports carry a bounded checked list (max 7 + overflow pointer).
#   S3  full reasoning stays in the machine envelope: file bodies truncate at
#       200 chars while --json items keep the full text.
#   S4  guard parity: the guided shape (verdict marker first, confidence lead
#       after) passes the degenerate-body guard; marker-less shorts still fail.
#   S5  missing records read neutral (unrecorded / not assessed), never fail.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

HEAD_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

# --- sandbox git worktree (kb.sh already inits one; ensure standalone-safe) ---
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "dump-eval@example.com"
  git config user.name "dump eval"
  grep -qxF '.review/' .gitignore 2>/dev/null || echo ".review/" >> .gitignore
  printf '# verdict skill (eval fixture)\n' > SKILL.md
  mkdir -p prompts
  printf '# system (eval fixture)\n' > prompts/system.md
  printf '# review mode (eval fixture)\n' > prompts/review.md
  printf '# diagrams (eval fixture)\n' > prompts/diagrams.md
  mkdir -p dumpdir
  echo "probe v1" > dumpdir/probe.txt
  git add SKILL.md prompts dumpdir/probe.txt .gitignore 2>/dev/null || true
  git commit -qm dump-init 2>/dev/null || true
  echo "probe v2" >> dumpdir/probe.txt
)
run_review learn --rule "Dump probe lines stay short" --scope "dumpdir/*.txt" --id dump-probe --json >/dev/null 2>&1 || true

# dump_expected <bundle-dir> <out> — the documented concatenation, built
# independently of render_prompt_dump (missing/optional files skipped).
dump_expected() {
  (
    cd "$EVAL_SANDBOX" || exit 99
    local dir="$1" meta="$1/PR.json" f
    [ -f "$1/source.json" ] && meta="$1/source.json"
    : > "$2"
    for f in SKILL.md prompts/system.md prompts/review.md prompts/diagrams.md "$meta" "$dir/toc.txt" "$dir/roles.txt" "$dir/full.diff" "$dir/kb.txt" "$dir/sensitive.txt" "$dir/panel"; do
      [ -f "$f" ] || continue
      cat -- "$f" >> "$2"
    done
  )
}

# --- P1: dump parity on fetch-local ---
calls_p3_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
run_review fetch-local acme/app dump-a --show-prompts
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "fetch-local --show-prompts exits 0"
else
  bad "fetch-local --show-prompts exits $EVAL_STATUS"
fi
dump_expected ".review/local-dump-a" "$EVAL_SANDBOX/expected-a.txt"
if cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/expected-a.txt"; then
  good "dump stdout equals the assembled files concatenated in order"
else
  bad "dump stdout differs from the assembled concatenation"
fi
if [ -f "$EVAL_SANDBOX/.review/local-dump-a/kb.txt" ]; then
  good "recall file present and included when rules match"
else
  bad "kb.txt missing (dump-probe rule should match dumpdir/*.txt)"
fi

# --- P2: no extra store writes, stdout carries only the dump ---
run_review fetch-local acme/app dump-b
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "twin fetch without the flag exits 0"
else
  bad "twin fetch exits $EVAL_STATUS"
fi
if [ "$(cd "$EVAL_SANDBOX/.review/local-dump-a" && find . -type f | sort)" = "$(cd "$EVAL_SANDBOX/.review/local-dump-b" && find . -type f | sort)" ]; then
  good "with-flag bundle holds exactly the normal fetch file set"
else
  bad "bundle file sets differ: $(cd "$EVAL_SANDBOX/.review" && diff <(cd local-dump-a && find . -type f | sort) <(cd local-dump-b && find . -type f | sort) | head -c 300)"
fi
if cmp -s "$EVAL_SANDBOX/.review/local-dump-a/full.diff" "$EVAL_SANDBOX/.review/local-dump-b/full.diff"; then
  good "diff bytes identical with and without the flag"
else
  bad "full.diff differs between flagged and plain fetch"
fi
if find "$EVAL_SANDBOX/.review/local-dump-a" -iname '*prompt*' | grep -q .; then
  bad "dump left prompt-named files in the store"
else
  good "no prompt-named files in the store"
fi
if grep -q "^done:" "$EVAL_SANDBOX/expected-a.txt"; then
  bad "expected fixture leaked progress lines"
else
  good "dump owns stdout (no progress lines in the concatenation)"
fi

# --- P3: fetch-local makes zero gh calls ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - calls_p3_before ))" = "0" ]; then
  good "zero stub-gh intercepts across the fetch-local legs (no agent, no network)"
else
  bad "stub gh intercepted $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') call(s)"
fi

# --- P4: dump parity on the PR fetch path via canned gh ---
cat > "$EVAL_SANDBOX/canned-PR.json" <<'EOF'
{"number":99,"title":"canned","body":"","baseRefName":"main","headRefName":"feat","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"},"url":"https://example.invalid/99","commits":[{"oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","messageHeadline":"canned"}],"files":[{"path":"notes.txt","additions":1,"deletions":1},{"path":"dumpdir/probe.txt","additions":1,"deletions":0}],"additions":2,"deletions":1,"isDraft":false,"reviewDecision":null,"state":"OPEN"}
EOF
cat > "$EVAL_SANDBOX/canned.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
diff --git a/dumpdir/probe.txt b/dumpdir/probe.txt
index 3333333..4444444 100644
--- a/dumpdir/probe.txt
+++ b/dumpdir/probe.txt
@@ -1 +1,2 @@
 probe v1
+probe v2
EOF
mkdir -p "$EVAL_SANDBOX/fakebin"
cat > "$EVAL_SANDBOX/fakebin/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  cat "$EVAL_SANDBOX/canned-PR.json"
elif [ "$1" = "pr" ] && [ "$2" = "diff" ]; then
  cat "$EVAL_SANDBOX/canned.diff"
elif [ "$1" = "api" ]; then
  echo "[]"
else
  echo "canned-gh: unhandled call ($*)" >&2
  exit 1
fi
EOF
chmod +x "$EVAL_SANDBOX/fakebin/gh"
calls_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN" run_review fetch acme/app 99 --show-prompts
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "fetch --show-prompts exits 0 on the PR path"
else
  bad "fetch --show-prompts exits $EVAL_STATUS"
fi
dump_expected ".review/99" "$EVAL_SANDBOX/expected-99.txt"
if cmp -s "$EVAL_OUT" "$EVAL_SANDBOX/expected-99.txt"; then
  good "PR-path dump equals the assembled concatenation"
else
  bad "PR-path dump differs from the assembled concatenation"
fi
calls_flagged="$(($(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - calls_before))"
EVAL_STUBBIN="$EVAL_SANDBOX/fakebin:$EVAL_STUBBIN" run_review fetch acme/app 98
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "twin fetch without the flag exits 0"
else
  bad "twin fetch exits $EVAL_STATUS"
fi
calls_plain="$(($(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - calls_before - calls_flagged))"
if [ "$calls_flagged" = "$calls_plain" ] && [ "$calls_flagged" -gt 0 ]; then
  good "flag adds no gh calls ($calls_flagged each)"
else
  bad "gh call counts differ (flagged $calls_flagged, plain $calls_plain)"
fi
if [ "$(cd "$EVAL_SANDBOX/.review/99" && find . -type f | sort)" = "$(cd "$EVAL_SANDBOX/.review/98" && find . -type f | sort)" ]; then
  good "PR-path bundles hold identical file sets"
else
  bad "PR-path bundle file sets differ"
fi

# --- P5: --json parity ---
run_review fetch-local acme/app dump-j --show-prompts --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ]; then
  good "json dump exits 0 with exactly one envelope"
else
  bad "json dump exits $EVAL_STATUS with $(stdout_docs) docs"
fi
dump_expected ".review/local-dump-j" "$EVAL_SANDBOX/expected-j.txt"
if jq -j '.data.prompt_dump // empty' <"$EVAL_OUT" | cmp -s - "$EVAL_SANDBOX/expected-j.txt"; then
  good "data.prompt_dump equals the assembled concatenation"
else
  bad "data.prompt_dump differs from the assembled concatenation"
fi
run_review fetch-local acme/app dump-k --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data | has("prompt_dump")' <"$EVAL_OUT" 2>/dev/null)" = "false" ]; then
  good "without the flag the envelope carries no prompt_dump key"
else
  bad "plain --json leaks prompt_dump"
fi

# --- F7: missing prompt files fail loudly, never silently ---
mv "$EVAL_SANDBOX/prompts/system.md" "$EVAL_SANDBOX/prompts/system.md.f7hide"
run_review fetch-local acme/app dump-f7a --show-prompts
f7a_status="$EVAL_STATUS"
mv "$EVAL_SANDBOX/prompts/system.md.f7hide" "$EVAL_SANDBOX/prompts/system.md"
if [ "$f7a_status" -ne 0 ] \
  && grep -q "prompts/system.md" "$EVAL_ERR" \
  && ! grep -q "system (eval fixture)" "$EVAL_OUT"; then
  good "F7: missing preamble refuses loudly (exit $f7a_status), no dump on stdout"
else
  bad "F7: missing preamble did not refuse loudly (exit $f7a_status)"
fi
mv "$EVAL_SANDBOX/prompts/system.md" "$EVAL_SANDBOX/prompts/system.md.f7hide"
run_review fetch-local acme/app dump-f7b --show-prompts --json
f7b_status="$EVAL_STATUS"
mv "$EVAL_SANDBOX/prompts/system.md.f7hide" "$EVAL_SANDBOX/prompts/system.md"
if [ "$f7b_status" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "prompt-dump-failed" ] \
  && [ "$(jq -r '.data | tostring' <"$EVAL_OUT" 2>/dev/null)" = "null" ]; then
  good "F7: missing preamble refuses typed in the envelope (prompt-dump-failed, data null)"
else
  bad "F7: missing preamble refusal not typed (exit $f7b_status)"
fi
mv "$EVAL_SANDBOX/prompts/review.md" "$EVAL_SANDBOX/prompts/review.md.f7hide"
run_review fetch-local acme/app dump-f7c --show-prompts
f7c_status="$EVAL_STATUS"
mv "$EVAL_SANDBOX/prompts/review.md.f7hide" "$EVAL_SANDBOX/prompts/review.md"
# The hidden file is tracked, so its deletion still rides the dump's own diff
# section (as a `-` line) — the prompt-section copy is the anchored `^#`
# line, which must be gone while the preamble stays.
f7c_omitted="$(grep -c '^# review mode (eval fixture)' "$EVAL_OUT" || true)"
if [ "$f7c_status" -eq 0 ] \
  && grep -q "prompts/review.md" "$EVAL_ERR" \
  && [ "$f7c_omitted" = "0" ] \
  && grep -q "review mode (eval fixture)" "$EVAL_OUT" \
  && grep -q "system (eval fixture)" "$EVAL_OUT"; then
  good "F7: missing sibling warns on stderr, dump proceeds with the preamble intact"
else
  bad "F7: missing sibling did not warn-and-proceed (exit $f7c_status)"
fi
mv "$EVAL_SANDBOX/prompts/review.md" "$EVAL_SANDBOX/prompts/review.md.f7hide"
run_review fetch-local acme/app dump-f7d --show-prompts --json
f7d_status="$EVAL_STATUS"
mv "$EVAL_SANDBOX/prompts/review.md.f7hide" "$EVAL_SANDBOX/prompts/review.md"
f7d_omitted="$(jq -j '.data.prompt_dump // empty' <"$EVAL_OUT" | grep -c '^# review mode (eval fixture)' || true)"
if [ "$f7d_status" -eq 0 ] \
  && jq -e '.warnings | join(" ") | contains("prompts/review.md")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$f7d_omitted" = "0" ] \
  && jq -j '.data.prompt_dump // empty' <"$EVAL_OUT" | grep -q "review mode (eval fixture)"; then
  good "F7: missing sibling rides warnings[] while data.prompt_dump stays complete otherwise"
else
  bad "F7: missing sibling warning missing from warnings[] (exit $f7d_status)"
fi

# --- S1: lead lines from the stored record + markers, no checked list ---
PR=4301
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Needs a null check on line 2 before dereference.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"1111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
jq -n --arg h "$HEAD_A" \
  '{schema_version:"1",review:{id:1},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"No new attack surface."}' \
  > "$EVAL_SANDBOX/.review/$PR/post.json"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4301.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(sed -n '4p' "$EVAL_SANDBOX/out-4301.md")" = "> Confidence: 4/5 · Safety: No new attack surface." ]; then
  good "output leads with confidence + safety from post.json"
else
  bad "lead line is '$(sed -n '4p' "$EVAL_SANDBOX/out-4301.md" 2>/dev/null)' (exit $EVAL_STATUS)"
fi
if grep -q "<!-- report-machine:start -->" "$EVAL_SANDBOX/out-4301.md" \
  && tail -n 1 "$EVAL_SANDBOX/out-4301.md" | grep -q "<!-- report-machine:end -->"; then
  good "machine markers wrap the new template"
else
  bad "machine markers broke"
fi
if grep -q "What we checked" "$EVAL_SANDBOX/out-4301.md"; then
  bad "checked list leaked onto a report with open findings"
else
  good "no checked list while findings stand open"
fi
run_review report acme/app "$PR"
if [ "$EVAL_STATUS" -eq 0 ] && ! grep -q "Confidence:" "$EVAL_OUT"; then
  good "human stdout stays marker-free (lead lives in the file)"
else
  bad "human stdout changed shape"
fi

# --- S2: clean reports carry a bounded checked list ---
PR=4302
eval_mk_bundle "$PR"
jq --arg h "$HEAD_A" '.headRefOid = $h
  | .files = ([range(1;10)] | map({path: ("f\(.).txt"), additions: 1, deletions: 0}))
  | .additions = 9 | .deletions = 0' "$EVAL_SANDBOX/.review/$PR/PR.json" \
  > "$EVAL_SANDBOX/.review/$PR/PR.json.new"
mv "$EVAL_SANDBOX/.review/$PR/PR.json.new" "$EVAL_SANDBOX/.review/$PR/PR.json"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"f1.txt","side":"RIGHT","line":1,"anchored":true,"body":"Fixed already.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"informational","sig":"2222222222222222222222222222222222222222","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"","history":[]}]
EOF
jq -n --arg h "$HEAD_A" \
  '{schema_version:"1",review:{id:2},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:5,security_summary:"None."}' \
  > "$EVAL_SANDBOX/.review/$PR/post.json"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4302.md"
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "^## What we checked$" "$EVAL_SANDBOX/out-4302.md"; then
  good "clean report carries a What we checked section"
else
  bad "checked section missing (exit $EVAL_STATUS)"
fi
if [ "$(grep -c '^- ' "$EVAL_SANDBOX/out-4302.md")" = "7" ] \
  && grep -q "^(+2 more" "$EVAL_SANDBOX/out-4302.md"; then
  good "checked list bounded at 7 with an overflow pointer"
else
  bad "checked list has $(grep -c '^- ' "$EVAL_SANDBOX/out-4302.md") bullets"
fi

# --- S3: file truncates, envelope retains ---
PR=4303
eval_mk_bundle "$PR"
LONG_A="$(printf 'A%.0s' {1..200})"
LONG_B="$(printf 'B%.0s' {1..100})"
jq -n --arg b "$LONG_A$LONG_B" \
  '[{kind:"inline",path:"notes.txt",side:"RIGHT",line:2,anchored:true,body:$b,suggestion:null,inBounds:true,rigor:"pointed",severity:"critical",sig:"3333333333333333333333333333333333333333",sig_version:"1",firstSeen:"aaaaaaa",lastSeen:"aaaaaaa",status:"open",rationale:"",history:[]}]' \
  | eval_write_findings "$PR"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4303.md" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -qF "$LONG_A" "$EVAL_SANDBOX/out-4303.md" \
  && ! grep -qF "$LONG_B" "$EVAL_SANDBOX/out-4303.md" \
  && [ "$(jq -r '.data.items[0].body | length' <"$EVAL_OUT" 2>/dev/null)" = "300" ]; then
  good "file keeps the 200-char summary, envelope keeps all 300 chars"
else
  bad "truncation/envelope contract broke (exit $EVAL_STATUS)"
fi

# --- S4: guided shape passes the degenerate-body guard ---
PR=4304
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 5/5 · Safety: not assessed.\n\n**What we checked**\n- `notes.txt` — single-line copy fix","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "marker-first confidence lead passes the guard"
else
  bad "guided shape refused (exit $EVAL_STATUS)"
fi
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"no marker here","approved":false,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "degenerate-body" ]; then
  good "marker-less shorts still refuse as degenerate-body"
else
  bad "degenerate-body guard broke (exit $EVAL_STATUS)"
fi

# --- S5: missing records read neutral ---
PR=4305
eval_mk_bundle "$PR"
printf '[]' | eval_write_findings "$PR"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4305.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "^> Confidence: unrecorded · Safety: not assessed$" "$EVAL_SANDBOX/out-4305.md"; then
  good "missing records read neutral without failing"
else
  bad "neutral lead missing (exit $EVAL_STATUS)"
fi

if [ "$fails" -ne 0 ]; then echo "case prompt-dump-summary: FAIL ($fails)"; exit 1; fi
echo "case prompt-dump-summary: PASS"
