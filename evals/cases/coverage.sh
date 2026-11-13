#!/usr/bin/env bash
#
# Diff-coverage attestation (STA-90 C8). `next --coverage` reports which
# changed source lines the review attested; `--require-coverage` refuses
# post/record when the attestation is absent or partial. Every number here is
# derived from the bundle's own hunks.tsv, so the fixture writes that snapshot
# and the diff the snapshot describes.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

# write_coverage_snapshot <dir> — the reviewed hunk snapshot: one source file
# with two changed ranges (NEW 10-20, OLD 40-50) and one generated file
# (dist/app.min.js). The union of both sides is 22 lines; the generated file
# is exempt and never counted.
write_coverage_snapshot() { # $1 = bundle dir
  local dir="$1"
  {
    echo 'diff --git a/src/db.ts b/src/db.ts'
    echo 'index 1111111..2222222 100644'
    echo '--- a/src/db.ts'
    echo '+++ b/src/db.ts'
    echo '@@ -10,11 +10,11 @@ export function query()'
    i=10; while [ "$i" -le 20 ]; do echo "-old $i"; echo "+new $i"; i=$((i + 1)); done
    echo '@@ -40,11 +40,0 @@ export function close()'
    i=40; while [ "$i" -le 50 ]; do echo "-dropped $i"; i=$((i + 1)); done
    echo 'diff --git a/dist/app.min.js b/dist/app.min.js'
    echo 'new file mode 100644'
    echo 'index 0000000..3333333'
    echo '--- /dev/null'
    echo '+++ b/dist/app.min.js'
    echo '@@ -0,0 +1,5 @@'
    echo '+var a=1;var b=2;var c=3;var d=4;var e=5;'
  } > "$dir/full.diff"
  printf 'src/db.ts\tNEW\t10\t20\nsrc/db.ts\tOLD\t40\t50\ndist/app.min.js\tNEW\t1\t5\n' >"$dir/hunks.tsv"
}

# mkcoverage <pr> — a provider bundle carrying the coverage snapshot.
mkcoverage() { # $1 = pr
  eval_mk_bundle "$1"
  write_coverage_snapshot "$EVAL_SANDBOX/.verdict/state/$1"
}

# mklocalcoverage <slug> — the same snapshot as a local bundle, so record's
# gate is exercised on the bundle kind record accepts.
mklocalcoverage() { # $1 = local slug (without the local- prefix)
  eval_mk_bundle "local-$1"
  write_coverage_snapshot "$EVAL_SANDBOX/.verdict/state/local-$1"
}

# mkchunked <pr> — a two-chunk bundle with a manifest. Coverage is not
# evaluated against hunk rows here: chunks[] is the attestation.
mkchunked() { # $1 = pr
  local pr="$1" dir="$EVAL_SANDBOX/.verdict/state/$1" total="" b1="" b2=""
  eval_mk_bundle "$pr"
  mkdir -p "$dir/chunks"
  cat > "$dir/full.diff" <<'EOF'
diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 top
+line-a
diff --git a/b.txt b/b.txt
index 3333333..4444444 100644
--- a/b.txt
+++ b/b.txt
@@ -1 +1,2 @@
 top
+line-b
EOF
  head -n 7 "$dir/full.diff" >"$dir/chunks/chunk-01.diff"
  tail -n +8 "$dir/full.diff" >"$dir/chunks/chunk-02.diff"
  printf '# Chunk 1/2\n\ndiff --git a/a.txt b/a.txt\n\n## files\n- a.txt\n' >"$dir/chunks/chunk-01.toc.txt"
  printf '# Chunk 2/2\n\ndiff --git a/b.txt b/b.txt\n\n## files\n- b.txt\n' >"$dir/chunks/chunk-02.toc.txt"
  printf '## source\n- a.txt\n' >"$dir/chunks/chunk-01.roles.txt"
  printf '## source\n- b.txt\n' >"$dir/chunks/chunk-02.roles.txt"
  printf 'a.txt\tNEW\t1\t2\nb.txt\tNEW\t1\t2\n' >"$dir/hunks.tsv"
  total="$(wc -c <"$dir/full.diff" | tr -d ' ')"
  b1="$(wc -c <"$dir/chunks/chunk-01.diff" | tr -d ' ')"
  b2="$(wc -c <"$dir/chunks/chunk-02.diff" | tr -d ' ')"
  jq -n --arg p "$pr" --argjson total "$total" --argjson b1 "$b1" --argjson b2 "$b2" \
    '{schema_version:"1",bundle:(".verdict/state/" + $p),
      head_sha:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      base_sha:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      cap:{bytes:512000,source:null},total_bytes:$total,chunk_count:2,
      chunks:[{index:1,name:"chunk-01",files:["a.txt"],estimated_bytes:$b1,bytes:$b1,
        diff:(".verdict/state/" + $p + "/chunks/chunk-01.diff"),toc:(".verdict/state/" + $p + "/chunks/chunk-01.toc.txt"),roles:(".verdict/state/" + $p + "/chunks/chunk-01.roles.txt")},
        {index:2,name:"chunk-02",files:["b.txt"],estimated_bytes:$b2,bytes:$b2,
        diff:(".verdict/state/" + $p + "/chunks/chunk-02.diff"),toc:(".verdict/state/" + $p + "/chunks/chunk-02.toc.txt"),roles:(".verdict/state/" + $p + "/chunks/chunk-02.roles.txt")}]}' \
    >"$dir/chunks/manifest.json"
}

# review_with <pr> <coverage-fragment> — one anchored comment plus the
# optional coverage[] attestation ("absent" writes no coverage key at all).
review_with() { # $1 = pr, $2 = coverage json or "absent"
  local pr="$1" cov="$2" dir="$EVAL_SANDBOX/.verdict/state/$1" body=""
  body='{"schema_version":"1","body":"> ✅ Coverage case review.","approved":false,"comments":[{"path":"src/db.ts","line":12,"side":"RIGHT","body":"Anchored note inside the changed range.","rigor":"pointed","severity":"informational"}]'
  if [ "$cov" = "absent" ]; then
    printf '%s}\n' "$body" >"$dir/review.json"
  else
    printf '%s,"coverage":%s}\n' "$body" "$cov" >"$dir/review.json"
  fi
}

# A canned success stub for the one real post leg (assertion 5's post.json):
# every authoring call the verb makes is answered locally, anything else
# still fails network-dead so no unexpected path can leave the sandbox.
STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
install_canned_stub() {
  cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  'api repos/'*'/reviews --paginate'|'api repos/'*'/comments --paginate')
    part="${2#*/pulls/}"
    pr="${part%%/*}"
    file="${2##*/}"
    if [ "$file" = comments ]; then file=inline; fi
    snapshot=".verdict/state/$pr/$file.json"
    if [ -f "$snapshot" ]; then cat "$snapshot"; else echo '[]'; fi
    exit 0;;

  *reviews*)
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *pulls*)
    printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'; exit 0;;
  *user*)
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
  chmod +x "$EVAL_STUBBIN/gh"
}

# --- 1. no attestation: 0% of 22 lines, full db range, no findings.json ----
PR=6101
mkcoverage "$PR"
review_with "$PR" absent
rm -f "$EVAL_SANDBOX/.verdict/state/$PR/findings.json"
run_review next acme/app "$PR" --coverage
assert_exit "next --coverage needs no findings.json and exits 0" 0
assert_file_grep "unattested review reads 0% over the union of both sides" "$EVAL_OUT" \
  'coverage: 0% of full hunk spans, both sides and context (0/22 lines, 1 files, 1 generated exempt)'
assert_file_grep "the whole db range is named as one gap" "$EVAL_OUT" '^- src/db.ts:10-20,40-50$'
assert_file_nogrep "the generated file is never a gap" "$EVAL_OUT" 'dist/app.min.js'
if [ -f "$EVAL_SANDBOX/.verdict/state/$PR/findings.json" ]; then
  bad "next --coverage wrote a findings store it must not need"
else
  good "the coverage read left no findings.json behind"
fi

# --- 2. partial attestation: 54% and the two gap ranges ---------------------
review_with "$PR" '[{"file":"src/db.ts","ranges":"10-15,40-45"}]'
run_review next acme/app "$PR" --coverage
assert_exit "partial attestation still reads (exit 0)" 0
assert_file_grep "partial attestation reads 54% of 22 lines" "$EVAL_OUT" \
  'coverage: 54% of full hunk spans, both sides and context (12/22 lines, 1 files, 1 generated exempt)'
assert_file_grep "the gaps are the unattested tails of both ranges" "$EVAL_OUT" \
  '^- src/db.ts:16-20,46-50$'
run_review next acme/app "$PR" --coverage --json
assert_exit "next --coverage --json exits 0 on a partial attestation" 0
assert_json_field "the envelope carries the same percentage" 54 '.data.coverage.percent'
assert_json_field "the envelope carries the gap ranges" '16-20,46-50' '.data.coverage.unattested[0].ranges'
assert_json_field "the envelope counts the generated file as exempt" 1 '.data.coverage.exempt'

# --- 3. --require-coverage refuses, and --force never bypasses -------------
run_review post acme/app "$PR" --dry-run --require-coverage --json
assert_exit "post --dry-run --require-coverage refuses a partial attestation" 1
assert_refusal "coverage refusal keeps the envelope contract" "coverage-incomplete"
assert_file_grep "the refusal names the percentage and the gap" "$EVAL_OUT" \
  '54% of full hunk spans, both sides and context attested (12/22)'
if [ -f "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json" ]; then
  bad "the refused dry run wrote findings.dryrun.json"
else
  good "the refused dry run writes no findings.dryrun.json"
fi
run_review post acme/app "$PR" --dry-run --require-coverage --force --json
assert_exit "--force does not bypass the coverage gate" 1
assert_refusal "--force refusal keeps its typed code" "coverage-incomplete"
if [ -f "$EVAL_SANDBOX/.verdict/state/$PR/findings.dryrun.json" ]; then
  bad "the forced refusal still wrote findings.dryrun.json"
else
  good "the forced refusal writes no findings.dryrun.json either"
fi
# record owns the same gate on the bundle kind it accepts (a local one).
mklocalcoverage covlocal
review_with local-covlocal '[{"file":"src/db.ts","ranges":"10-15,40-45"}]'
run_review record acme/app local-covlocal --require-coverage --json
assert_exit "record --require-coverage refuses the same partial attestation" 1
assert_refusal "record refuses with the shared coverage code" "coverage-incomplete"
if [ -f "$EVAL_SANDBOX/.verdict/state/local-covlocal/record.json" ]; then
  bad "the refused record wrote record.json"
else
  good "the refused record writes no record.json"
fi

# --- 4. ranges "all": complete, so the gate passes and the body stays clean -
PR=6102
mkcoverage "$PR"
review_with "$PR" '[{"file":"src/db.ts","ranges":"all"}]'
run_review post acme/app "$PR" --dry-run --require-coverage --json
assert_exit "a whole-file attestation satisfies --require-coverage (exit 0)" 0
assert_json_field "the dry-run receipt reads 100%" 100 '.data.coverage.percent'
assert_json_field "the receipt counts no gaps" 0 '.data.coverage.gaps'
assert_json_field "the receipt counts the generated file as exempt" 1 '.data.coverage.exempt'
assert_stream_nogrep "a complete attestation warns about nothing" "$EVAL_ERR" 'coverage:'
install_canned_stub
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-6102.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --require-coverage --json
unset EVAL_SAVE_PAYLOAD
assert_exit "the complete attestation posts through the stub (exit 0)" 0
assert_json_field "post.json records the complete receipt" 100 '.coverage.percent' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
jq -r '.body' "$EVAL_SANDBOX/posted-6102.json" >"$EVAL_SANDBOX/body-6102.txt"
assert_file_nogrep "the complete body carries no coverage line" \
  "$EVAL_SANDBOX/body-6102.txt" '⚠️ coverage:'

# --- 5. partial without the flag: warn once, one body line, a stored receipt
PR=6101
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-6101.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
assert_exit "a partial review posts without the flag (exit 0)" 0
jq -r '.body' "$EVAL_SANDBOX/posted-6101.json" >"$EVAL_SANDBOX/body-6101.txt"
lines="$(grep -c -F '> ⚠️ coverage:' "$EVAL_SANDBOX/body-6101.txt" || true)"
assert_eq "the partial body carries exactly one coverage line" 1 "$lines"
assert_file_grep "the body line names the percentage and both gap ranges" \
  "$EVAL_SANDBOX/body-6101.txt" \
  '^> ⚠️ coverage: 54% of changed lines attested (unattested: src/db.ts:16-20,46-50)'
assert_json_field "post.json counts the two gap ranges" 2 '.coverage.gaps' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "post.json counts the attested and total lines" 12 '.coverage.attested' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "post.json counts all 22 changed lines" 22 '.coverage.total' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
assert_json_field "post.json stores the same percentage" 54 '.coverage.percent' \
  "$EVAL_SANDBOX/.verdict/state/$PR/post.json"
restore_stub

# --- 6. a chunked bundle: warn and proceed; next names the chunks ----------
PR=6103
mkchunked "$PR"
review_with "$PR" absent
run_review post acme/app "$PR" --dry-run --require-coverage --json
assert_exit "a chunked bundle is never refused by the coverage gate" 0
assert_json_field "the chunked gate warns instead of refusing" "true" \
  'any(.warnings[]; startswith("coverage: chunked bundle"))'
assert_json_field "the chunked dry run stores no coverage receipt" "false" \
  '.data | has("coverage")'
run_review next acme/app "$PR" --coverage
assert_exit "next --coverage reads a chunked bundle (exit 0)" 0
assert_file_grep "next names the chunked bundle and its chunk count" "$EVAL_OUT" \
  'coverage: chunked bundle (2 chunks)'
assert_file_nogrep "next lists no per-file gaps for a chunked bundle" "$EVAL_OUT" '^unattested:'

# --- 7. no real gh: the stub is the only gh, and it logged every call ------
assert_eq "the harness stub is the only gh on PATH" "$EVAL_STUBBIN/gh" \
  "$(PATH="$EVAL_STUBBIN:$PATH" command -v gh)"
total="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
logged="$(grep -c '^stub-gh ' "$EVAL_STUBLOG" || true)"
assert_eq "every gh invocation this case made is the stub's" "$total" "$logged"
if [ "$total" -ge 1 ]; then
  good "the canned post legs really reached the stub ($total interception(s))"
else
  bad "no gh invocation was intercepted, so the post legs prove nothing"
fi


run_review next --coverage
assert_exit "next --coverage requires positionals" 2
assert_file_grep "missing positionals lead with an error" "$EVAL_ERR" '^error: next requires <owner/repo> and <pr\|local-name>$'

eval_end
