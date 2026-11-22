#!/usr/bin/env bash
#
# fetch-local --scan: a diff-free review of files rather than a git change.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

mkdir -p "$EVAL_SANDBOX/xdg-empty"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
unset REVIEW_MAX_BYTES REVIEW_OUTPUT_FORMAT REVIEW_LAYOUT_COMPACT REVIEW_COLOR_MODE || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION REVIEW_PUBLISH_ATTRIBUTION_TEXT || true
unset REVIEW_PUBLISH_COPYRIGHT REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL || true
unset REVIEW_BUDGET_USD REVIEW_MODEL || true

STATE="$EVAL_SANDBOX/.verdict/state"
TREE="$EVAL_SANDBOX/tree"

# A committed, clean tree: every scan below reviews files no git diff names,
# which is exactly the property --scan exists for.
(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "scan@example.com"
  git config user.name "scan"
  cat > .gitignore <<'EOF'
.verdict/state/
stubbin/
fakebin/
*.log
eval-out.bin
eval-err.txt
case-results.txt
scan-concat.diff
EOF
  mkdir -p tree/app tree/docs
  printf 'package main\n\nfunc main() { println(1) }\n' > tree/app/main.go
  printf 'package main\n\nfunc util() int { return 1 }\n' > tree/app/util.go
  printf '# docs\n' > tree/docs/readme.md
  printf 'DATABASE_PASSWORD=hunter2\n' > tree/.env
  printf -- '-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----\n' > tree/server.pem
  git add -A
  git -c commit.gpgsign=false commit -qm scan-base
)
if [ -z "$(cd "$EVAL_SANDBOX" && git status --porcelain)" ]; then
  good "the fixture tree is committed and clean, so no leg below has a diff to find"
else
  bad "the fixture tree is dirty, so the scans would not prove a zero-diff run"
fi

# Preview first: nothing exists under .verdict yet, so the zero-write claim is
# about a run that had no state home at all.
run_review fetch-local acme/app pv --scan tree --preview --json
assert_exit "a scan preview succeeds" 0
assert_envelope "scan preview"
assert_json_field "the preview lists the reviewed files" \
  "tree/app/main.go tree/app/util.go tree/docs/readme.md" \
  '.data.preview.included | map(.path) | sort | join(" ")'
assert_json_field "the preview names the withheld env document" "default:secret secret-builtin" \
  '.data.preview.excluded[] | select(.path=="tree/.env") | "\(.rule_id) \(.reason)"'
assert_json_field "the preview names the withheld private key" "default:secret secret-builtin" \
  '.data.preview.excluded[] | select(.path=="tree/server.pem") | "\(.rule_id) \(.reason)"'
if [ ! -d "$EVAL_SANDBOX/.verdict" ]; then
  good "a scan preview writes no state directory (and no bundle file)"
else
  bad "a scan preview left $EVAL_SANDBOX/.verdict behind: $(find "$EVAL_SANDBOX/.verdict" -maxdepth 3 | tr '\n' ' ')"
fi
assert_stream_nogrep "a scan preview writes no done line" "$EVAL_ERR" '^done:'

# A directory operand reviews every regular file beneath it, as added files.
run_review fetch-local acme/app dir --scan tree --json
assert_exit "a directory scan succeeds on a clean tree" 0
assert_envelope "directory scan"
DIRB="$STATE/local-dir"
assert_json_field "source.json records the scan kind" scan '.kind' "$DIRB/source.json"
assert_json_field "a scan claims no commit head" null '.head_sha' "$DIRB/source.json"
assert_file_grep "the synthesized diff opens the scanned file" "$DIRB/full.diff" \
  '^diff --git a/tree/app/main\.go b/tree/app/main\.go$' "-E"
assert_file_grep "the section is an added file" "$DIRB/full.diff" '^new file mode 100644$' "-E"
assert_file_grep "the hunk claims every line as added" "$DIRB/full.diff" '^@@ -0,0 \+1,3 @@$' "-E"
assert_file_grep "the hunk body carries the file content" "$DIRB/full.diff" '^\+func main\(\) \{ println\(1\) \}$' "-E"
# ANSI-C quoting so GNU grep -E on Linux matches the literal tab bytes.
assert_file_grep "hunks.tsv indexes the NEW span" "$DIRB/hunks.tsv" $'^tree/app/main\\.go\tNEW\t1\t3$' "-E"
assert_file_grep "toc.txt names the scanned file" "$DIRB/toc.txt" '^1:diff --git a/tree/app/main\.go' "-E"
for artifact in full.diff hunks.tsv toc.txt roles.txt source.json run.json provenance.json identity.json \
  excluded.json controls.json lenses.txt kb.txt; do
  if [ -e "$DIRB/$artifact" ]; then
    good "the scan bundle carries $artifact"
  else
    bad "the scan bundle misses $artifact"
  fi
done

# A file operand selects exactly that file.
run_review fetch-local acme/app one --scan tree/app/util.go --json
assert_exit "a file scan succeeds" 0
ONEB="$STATE/local-one"
assert_file_grep "the file scan reviews the named file" "$ONEB/full.diff" '^diff --git a/tree/app/util\.go' "-E"
assert_file_nogrep "the file scan reviews nothing else" "$ONEB/full.diff" 'tree/app/main\.go|tree/docs/readme\.md' "-E"

# A linked worktree or a submodule carries `.git` as a regular file naming the
# real git directory; a scan skips it rather than publishing the host path.
mkdir -p "$EVAL_SANDBOX/linked"
printf 'package main\n' >"$EVAL_SANDBOX/linked/main.go"
printf 'gitdir: /host/checkout/.git/worktrees/linked\n' >"$EVAL_SANDBOX/linked/.git"
run_review fetch-local acme/app linked --scan linked --json
assert_exit "a scan of a linked worktree directory succeeds" 0
LINKB="$STATE/local-linked"
assert_file_grep "the scanned file is reviewed" "$LINKB/full.diff" '^diff --git a/linked/main\.go' "-E"
assert_file_nogrep "a linked worktree's .git file never reaches full.diff" "$LINKB/full.diff" \
  'gitdir|host/checkout' "-E"

# The withholding floor is a path rule: it applies to a scan exactly as it
# does to a diff, and its receipt names the built-in rule.
run_review fetch-local acme/app all --scan tree --json
assert_exit "a full-tree scan succeeds" 0
ALLB="$STATE/local-all"
assert_file_nogrep "the env document's values never reach full.diff" "$ALLB/full.diff" 'DATABASE_PASSWORD'
assert_file_nogrep "the private key body never reaches full.diff" "$ALLB/full.diff" 'BEGIN PRIVATE KEY'
assert_json_field "both secret paths are withheld by the built-in floor" "tree/.env tree/server.pem" \
  '[.excluded[] | select(.rule_id=="default:secret") | .path] | sort | join(" ")' "$ALLB/excluded.json"
assert_json_field "the built-in secret set is receipted even for a scan" \
  "default:attribute default:generated default:secret" '.defaults | sort | join(" ")' "$ALLB/excluded.json"
assert_file_grep "toc.txt receipts the withheld env document" "$ALLB/toc.txt" \
  '^- tree/\.env \(default:secret, [0-9]+ bytes\)$' "-E"
assert_file_grep "the safe files stay in the review" "$ALLB/full.diff" '^diff --git a/tree/app/util\.go' "-E"
assert_file_grep "sensitive.txt still flags the secret-adjacent paths" "$ALLB/sensitive.txt" 'tree/\.env' "-E"

# Every selector failure is fail-closed.
OUTSIDE="$(mktemp -d "${TMPDIR:-/tmp}/verdict-scan-outside.XXXXXX")"
trap 'rm -rf "$OUTSIDE"' EXIT
printf 'outside\n' >"$OUTSIDE/outside.txt"
run_review fetch-local acme/app esc --scan "$OUTSIDE/outside.txt" --json
assert_exit "a scan path outside the repository refuses" 1
assert_refusal "the escape refusal" bad-scan
assert_json_field "the escape refusal names the repository root" true \
  '.error.message | test("resolves outside the repository root")'
run_review fetch-local acme/app esc --scan ../outside-missing --json
assert_exit "a relative parent escape refuses" 1
assert_refusal "the parent-escape refusal" bad-scan
assert_json_field "the parent escape refuses without probing the outside path" true \
  '.error.message | test("resolves outside the repository root")'
run_review fetch-local acme/app esc --scan .git --json
assert_exit "a managed directory refuses" 1
assert_refusal "the managed-directory refusal" bad-scan
assert_json_field "the managed-directory refusal says a review never reads it" true \
  '.error.message | test("which a review never reads")'
mkdir -p "$EVAL_SANDBOX/tree/sub/.verdict"
printf '{}\n' >"$EVAL_SANDBOX/tree/sub/.verdict/note.md"
run_review fetch-local acme/app esc --scan tree/sub/.verdict --json
assert_exit "a nested managed directory refuses" 1
assert_refusal "the nested managed-directory refusal" bad-scan
assert_json_field "the nested refusal names the managed entry" true \
  '.error.message | test("inside \\.verdict, which a review never reads")'
run_review fetch-local acme/app esc --scan tree/missing.go --json
assert_exit "a missing scan path refuses" 1
assert_refusal "the missing-path refusal" bad-scan
assert_json_field "the missing-path refusal names the operand" true \
  '.error.message | test("no such path inside")'
ln -s app/main.go "$TREE/inside-link.go"
run_review fetch-local acme/app esc --scan tree/inside-link.go --json
assert_exit "a symlinked operand refuses" 1
assert_refusal "the symlink refusal" bad-scan
assert_json_field "the symlink refusal says links are never scanned" true \
  '.error.message | test("is a symlink")'
rm -f "$TREE/inside-link.go"
ln -s app "$TREE/linkdir"
run_review fetch-local acme/app esc --scan tree/linkdir/util.go --json
assert_exit "a link between the root and the operand refuses" 1
assert_refusal "the intermediate-link refusal" bad-scan
assert_json_field "the intermediate-link refusal names the link" true \
  '.error.message | test("is a symlink")'
rm -f "$TREE/linkdir"

# --scan is a selector of its own: it cannot combine with a diff source, an
# isolated checkout, or a pathspec filter.
for clash in "--staged" "--range HEAD~1..HEAD" "--base HEAD" "--worktree" "-- tree/app/main.go"; do
  # shellcheck disable=SC2086 # clash is a deliberate argv fragment
  run_review fetch-local acme/app clash --json --scan tree $clash
  assert_exit "--scan with $clash refuses" 1
  assert_refusal "--scan with $clash" bad-flags
  assert_json_field "--scan with $clash names the mutual exclusion" true \
    '.error.message | test("mutually exclusive")'
done

# Over the cap, --chunk slices the synthesized diff on file boundaries exactly
# like a fetched diff, and the ordered chunks concatenate back to full.diff.
mkdir -p "$EVAL_SANDBOX/big"
i=1
while [ "$i" -le 6 ]; do
  awk -v f="$i" 'BEGIN{for(j=0;j<1500;j++) printf "scanned line %06d file%d padding padding padding padding\n", j, f}' \
    >"$EVAL_SANDBOX/big/f$i.txt"
  i=$((i + 1))
done
run_review fetch-local acme/app chunked --scan big --chunk
assert_exit "an over-cap scan chunks" 0
CHUNKB="$STATE/local-chunked"
MAN="$CHUNKB/chunks/manifest.json"
N="$(jq -r '.chunk_count' <"$MAN" 2>/dev/null || echo 0)"
if [ -f "$MAN" ] && [ "$N" -ge 2 ] 2>/dev/null \
  && [ "$(jq -r '.cap.bytes' <"$MAN")" = "512000" ] \
  && [ "$(jq -r '.chunks | length' <"$MAN")" = "$N" ]; then
  good "the scan manifest caps $N ordered chunks at 512000 bytes"
else
  bad "the scan manifest is $(jq -c '{cap,chunk_count}' <"$MAN" 2>/dev/null || echo MISSING)"
fi
: >"$EVAL_SANDBOX/scan-concat.diff"
i=1
while [ "$i" -le "$N" ]; do
  cat "$CHUNKB/chunks/$(printf 'chunk-%02d' "$i").diff" >>"$EVAL_SANDBOX/scan-concat.diff"
  i=$((i + 1))
done
if cmp -s "$EVAL_SANDBOX/scan-concat.diff" "$CHUNKB/full.diff"; then
  good "the ordered scan chunks concatenate byte-identically to full.diff"
else
  bad "the scan chunk concat differs from full.diff"
fi
for name in $(jq -r '.chunks[].name' <"$MAN"); do
  if [ -s "$CHUNKB/chunks/$name.toc.txt" ] && [ -s "$CHUNKB/chunks/$name.roles.txt" ]; then
    good "$name carries its checklist and roles"
  else
    bad "$name misses its checklist or roles"
  fi
done

# Downstream: a scan bundle is an ordinary local bundle. record stores the
# review, report renders it and exports SARIF, and gate composes a pass.
cat >"$DIRB/review.json" <<'EOF'
{"schema_version":"1","summary":"one finding on the scanned file",
 "findings":[{"path":"tree/app/main.go","line":3,"side":"RIGHT",
   "claim":"The entry point prints a constant that should come from configuration.",
   "quote":"func main() { println(1) }","confidence":"high","rigor":"walked","severity":"informational"}],
 "coverage":[{"file":"tree/app/main.go","ranges":"all"},{"file":"tree/app/util.go","ranges":"all"},
   {"file":"tree/docs/readme.md","ranges":"all"}],
 "receipts":[{"hunk":"tree/app/main.go\tNEW\t1\t3","quote":"package main"},
   {"hunk":"tree/app/util.go\tNEW\t1\t3","quote":"package main"},
   {"hunk":"tree/docs/readme.md\tNEW\t1\t1","quote":"# docs"}],
 "spec_checks":[]}
EOF
run_review record acme/app local-dir --json
assert_exit "record accepts the scan bundle" 0
assert_json_field "record stores the scan finding open" 1 '.data.findings.open'
run_review report acme/app local-dir --json
assert_exit "report reads the scan bundle" 0
assert_json_field "report ranks the scan finding" 1 '.data.verdict.open'
assert_json_field "report renders the scan finding's claim" 1 \
  '[.data.items[] | select(.body | test("configuration"))] | length'
run_review report acme/app local-dir --sarif "$EVAL_SANDBOX/scan.sarif" --json
assert_exit "report --sarif accepts the scan bundle" 0
assert_json_field "the SARIF document is 2.1.0" "2.1.0" '.version' "$EVAL_SANDBOX/scan.sarif"
assert_json_field "the SARIF result anchors the scanned path and line" "tree/app/main.go:3" \
  '.runs[0].results[0].locations[0].physicalLocation | "\(.artifactLocation.uri):\(.region.startLine)"' \
  "$EVAL_SANDBOX/scan.sarif"
run_review gate acme/app local-dir --max-severity critical --json
assert_exit "gate passes a clean scan review" 0
assert_json_field "the gate envelope reports a pass" true '.data.passed'
assert_json_field "the gate's coverage dimension keys to the scan's own diff" pass \
  '.data.dimensions[] | select(.id=="coverage") | .status'

# A repository with no commit yet: --scan needs neither a commit nor a diff,
# and the whole local pipeline reads the bundle it builds.
UNBORN="$EVAL_SANDBOX/unborn"
mkdir -p "$UNBORN/app"
printf 'package main\n\nfunc main() {}\n' >"$UNBORN/app/main.go"
(
  cd "$UNBORN" || exit 99
  git init -q
)
run_unborn() {
  set +e
  (
    cd "$UNBORN" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" "$EVAL_CLI" "$@" >"$EVAL_OUT" 2>"$EVAL_ERR"
  )
  # shellcheck disable=SC2034 # the assert_* helpers in ../lib/common.sh read it
  EVAL_STATUS=$?
  set -e
}
run_unborn fetch-local acme/app fresh --scan app --json
assert_exit "a scan in a repository with no commit succeeds" 0
UNB="$UNBORN/.verdict/state/local-fresh"
assert_json_field "an unborn scan records no base commit" null '.base_sha' "$UNB/source.json"
assert_file_grep "the unborn scan still synthesizes the file" "$UNB/full.diff" \
  '^\+func main\(\) \{\}$' "-E"
cat >"$UNB/review.json" <<'EOF'
{"schema_version":"1","summary":"unborn scan","findings":[],
 "coverage":[{"file":"app/main.go","ranges":"all"}],
 "receipts":[{"hunk":"app/main.go\tNEW\t1\t3","quote":"package main"}],"spec_checks":[]}
EOF
run_unborn record acme/app local-fresh --json
assert_exit "record accepts a bundle with no base commit" 0
run_unborn report acme/app local-fresh --json
assert_exit "report reads a bundle with no base commit" 0
run_unborn gate acme/app local-fresh --max-severity critical --json
assert_exit "gate passes a bundle with no base commit" 0
assert_json_field "the unborn gate reports a pass" true '.data.passed'

# The explicit head-controls opt-out is a second base-less shape: the snapshot
# is stamped head, and the bundle still records no commit to verify against.
run_unborn fetch-local acme/app fresh-head --scan app --allow-head-controls --json
assert_exit "a scan with --allow-head-controls in an unborn repository succeeds" 0
HEADB="$UNBORN/.verdict/state/local-fresh-head"
assert_json_field "the opt-out snapshot is stamped head" head '.policy' "$HEADB/controls.json"
assert_json_field "the opt-out snapshot records no base commit" null '.base_sha' "$HEADB/source.json"
cp "$UNB/review.json" "$HEADB/review.json"
run_unborn record acme/app local-fresh-head --json
assert_exit "record accepts the head-controls bundle with no base commit" 0
run_unborn gate acme/app local-fresh-head --max-severity critical --json
assert_exit "gate passes the head-controls bundle with no base commit" 0

rm -rf "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/big" "$EVAL_SANDBOX/scan.sarif" "$EVAL_SANDBOX/scan-concat.diff" "$OUTSIDE"
trap - EXIT
unset XDG_CONFIG_HOME || true

eval_end
