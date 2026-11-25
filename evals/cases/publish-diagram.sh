#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL || true
unset REVIEW_PUBLISH_OVERVIEW || true

diagram_status() { jq -r '.data.body.diagram.status // empty' <"$EVAL_OUT" 2>/dev/null; }

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
# post pins its calls to the bundle's host: github.com here, and only it.
if [ "${1:-}" = api ] && [ "${2:-}" = --hostname ] && [ "${3:-}" = github.com ]; then set -- api "${@:4}"; fi
case " $* " in *" --hostname"*) echo "evals-stub-gh: call pinned to another host ($*)" >&2; exit 64;; esac
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  'api repos/acme/app/pulls/4701 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4701/reviews --paginate'|'api repos/acme/app/pulls/4701/comments --paginate'|'api repos/acme/app/issues/4701/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4701 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4701/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4701/comments --method POST --input -')
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    echo '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  'api repos/acme/app/pulls/4710 --jq .head.sha') printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  'api repos/acme/app/pulls/4710/reviews --paginate'|'api repos/acme/app/pulls/4710/comments --paginate'|'api repos/acme/app/issues/4710/comments --paginate') echo '[]'; exit 0;;
  'api graphql --paginate -F owner=acme -F name=app -F pr=4710 -f query=
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }') echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0;;
  'api repos/acme/app/pulls/4710/reviews --method POST --input - --jq {id, state, html_url, submitted_at}'|'api repos/acme/app/issues/4710/comments --method POST --input -')
    if [ -n "${EVAL_SAVE_PAYLOAD:-}" ]; then cat > "$EVAL_SAVE_PAYLOAD"; fi
    echo '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'; exit 0;;
  "api user --jq .login") printf someone-else; exit 0;;
esac
echo "evals-stub-gh: unregistered argv ($*)" >&2
exit 64
EOF
chmod +x "$EVAL_STUBBIN/gh"
status=0
"$EVAL_STUBBIN/gh" api repos/acme/app/pulls/4701/reviews --method DELETE >/dev/null 2>&1 || status=$?
assert_eq "unexpected review deletion is refused" 64 "$status"


# --- D1: smuggled diagram markers and mermaid fences in summary are neutralized ---
PR=4701
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","findings":[],"summary":"Reviewed notes.txt.\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow</summary>\n\n```mermaid\nsequenceDiagram\n    C->>S: request\n```\n\n</details>\n<!-- verdict-diagram:end -->"}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "smuggled diagram in summary exits 0" 0
assert_eq "neutralized summary reports no live diagram" "" "$(diagram_status)"
run_review post acme/app "$PR" --dry-run
assert_exit "smuggled diagram human dry-run exits 0" 0
assert_file_grep "verdict marker preserved when summary smuggles diagram markup" "$EVAL_OUT" "preflight: body=verdict" "-F"
export EVAL_SAVE_PAYLOAD="$EVAL_SANDBOX/posted-4701.json"
rm -f "$EVAL_SAVE_PAYLOAD"
run_review post acme/app "$PR" --json
unset EVAL_SAVE_PAYLOAD
assert_exit "smuggled diagram post exits 0" 0
jq -r '.body // empty' "$EVAL_SANDBOX/posted-4701.json" > "$EVAL_SANDBOX/body-4701.md"
assert_file_grep "posted body carries the clean verdict lead" "$EVAL_SANDBOX/body-4701.md" '> ✅ No new issues found.' "-F"
assert_file_nogrep "raw verdict-diagram marker is neutralized" "$EVAL_SANDBOX/body-4701.md" "<!-- verdict-diagram:start -->" "-F"
assert_file_nogrep "raw mermaid fence is neutralized" "$EVAL_SANDBOX/body-4701.md" '```mermaid' "-F"

# --- D2: review.md no longer instructs agents to draw behavioral diagrams ---
assert_file_nogrep "review.md no longer references prompts/diagrams.md" "$EVAL_PROCEDURE/prompts/review.md" "prompts/diagrams.md" "-F"
assert_file_nogrep "review.md no longer references publish.diagrams.behavioral" "$EVAL_PROCEDURE/prompts/review.md" "publish.diagrams.behavioral" "-F"

# --- D4: structural split (topology half, table stays) ---
PR=4708
eval_mk_bundle "$PR"
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Split probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"7777777777777777777777777777777777777777","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
mkdir -p "$EVAL_SANDBOX/xdg-nostruct/verdict"
printf '{"publish":{"diagrams":{"structural":false}}}' > "$EVAL_SANDBOX/xdg-nostruct/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-nostruct"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4708.md" --overview
assert_exit "structural=false render succeeds" 0
assert_file_grep "structural=false keeps the files table" "$EVAL_SANDBOX/out-4708.md" '<summary>Files changed (1)</summary>' "-F"
assert_file_nogrep "structural=false hides the topology heading" "$EVAL_SANDBOX/out-4708.md" "Change topology"
assert_file_nogrep "structural=false hides the mermaid block" "$EVAL_SANDBOX/out-4708.md" '```mermaid' "-F"
unset XDG_CONFIG_HOME || true
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4709.md" --overview
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '<summary>Change topology (computed from the diff)</summary>' "$EVAL_SANDBOX/out-4709.md"; then
  good "structural default keeps the computed topology bytes"
else
  bad "structural default gave exit $EVAL_STATUS"
fi

# --- D5: no-diagram default (diagram-less bundle renders clean) ---
PR=4710
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","findings":[],"summary":"No new issues found."}
EOF
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Parity probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"8888888888888888888888888888888888888888","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
mkdir -p "$EVAL_SANDBOX/xdg-empty/verdict"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-new.md"
if [ "$EVAL_STATUS" -eq 0 ] && [ -s "$EVAL_SANDBOX/out-new.md" ]; then
  good "report --output succeeds on a diagram-less bundle"
else
  bad "report baseline exits $EVAL_STATUS"
fi
run_review post acme/app "$PR" --dry-run
assert_exit "dry-run baseline succeeds" 0
assert_stream_nogrep "human dry-run stdout carries no diagram line" "$EVAL_OUT" "diagram" "-i"
unset XDG_CONFIG_HOME || true

# --- T13: the computed topology reaches the posted body, and both knobs remove it ---
# A 9-file diff crosses the 8-file gate, so the body carries the block the
# agent never draws. PR 4710's stub arms are reused from D5.
capture_body() { # $1 = destination body file, $2.. = extra post flags
  local dest="$1" payload="$EVAL_SANDBOX/capture-payload.json"
  shift
  export EVAL_SAVE_PAYLOAD="$payload"
  rm -f "$payload"
  run_review post acme/app "$PR" --json "$@"
  unset EVAL_SAVE_PAYLOAD
  if [ -f "$payload" ]; then jq -r '.body // empty' "$payload" >"$dest"; fi
}
PR=4710
fixture_repo="$(eval_fixture_scratch)/repo"
mkdir -p "$fixture_repo"
i=1
while [ "$i" -le 8 ]; do
  printf 'top\nold %s\n' "$i" >"$fixture_repo/extra$i.ts"
  i=$((i + 1))
done
eval_fixture_repo "$fixture_repo"
i=1
while [ "$i" -le 8 ]; do
  printf 'top\nnew %s\n' "$i" >"$fixture_repo/extra$i.ts"
  i=$((i + 1))
done
eval_mk_bundle "$PR"
# The producer's provider bundle carries metadata only, so the fixture states
# the nine changed files the gate must count.
bundle_dir="$EVAL_SANDBOX/.verdict/state/$PR"
jq '.files = [
  {"path":"notes.txt","additions":1,"deletions":1},
  {"path":"extra1.ts","additions":1,"deletions":1},
  {"path":"extra2.ts","additions":1,"deletions":1},
  {"path":"extra3.ts","additions":1,"deletions":1},
  {"path":"extra4.ts","additions":1,"deletions":1},
  {"path":"extra5.ts","additions":1,"deletions":1},
  {"path":"extra6.ts","additions":1,"deletions":1},
  {"path":"extra7.ts","additions":1,"deletions":1},
  {"path":"extra8.ts","additions":1,"deletions":1}
]' "$bundle_dir/PR.json" >"$bundle_dir/PR.json.tmp"
mv "$bundle_dir/PR.json.tmp" "$bundle_dir/PR.json"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","findings":[],"summary":"Large PR with a computed topology."}
EOF
run_review post acme/app "$PR" --dry-run --json
assert_exit "large-bundle dry-run exits 0" 0
assert_eq "large-bundle dry-run reports no diagram status" "" "$(diagram_status)"
capture_body "$EVAL_SANDBOX/body-topology.md"
assert_exit "large-bundle post exits 0" 0
assert_file_grep "posted body carries the computed topology block" "$EVAL_SANDBOX/body-topology.md" \
  '<summary>Change topology (computed from the diff)</summary>' "-F"
assert_file_grep "posted topology renders every file node" "$EVAL_SANDBOX/body-topology.md" '    PR --> F9[' "-F"
assert_file_nogrep "posted topology invents no relation" "$EVAL_SANDBOX/body-topology.md" '-->|' "-F"
capture_body "$EVAL_SANDBOX/body-noflag.md" --force --no-diagram-structural
assert_exit "--no-diagram-structural post exits 0" 0
assert_file_nogrep "--no-diagram-structural removes the block" "$EVAL_SANDBOX/body-noflag.md" "Change topology"
mkdir -p "$EVAL_SANDBOX/xdg-nostruct-body/verdict"
printf '{"publish":{"diagrams":{"structural":false}}}' > "$EVAL_SANDBOX/xdg-nostruct-body/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-nostruct-body"
capture_body "$EVAL_SANDBOX/body-nostruct.md" --force
assert_exit "structural=false post exits 0" 0
assert_file_nogrep "structural=false removes the block from the body" "$EVAL_SANDBOX/body-nostruct.md" "Change topology"
unset XDG_CONFIG_HOME || true
mkdir -p "$EVAL_SANDBOX/xdg-noov-body/verdict"
printf '{"publish":{"overview":false}}' > "$EVAL_SANDBOX/xdg-noov-body/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-noov-body"
capture_body "$EVAL_SANDBOX/body-noov.md" --force
assert_exit "overview=false post exits 0" 0
assert_file_nogrep "overview=false removes the block from the body" "$EVAL_SANDBOX/body-noov.md" "Change topology"
unset XDG_CONFIG_HOME || true

# --- D6: the anchored behavioral diagram (structured field, never raw mermaid) ---
# PR 4710's stub arms are reused from D5. The bundle's one changed line is
# notes.txt:2 ("new line"), so a verified element quotes exactly that.
PR=4710
anchored_review() { # $1 = diagram object, $2.. reached through
  printf '{"schema_version":"1","findings":[],"summary":"Anchored diagram probe.","diagram":%s}' "$1" | eval_write_review "$PR"
}
good_diagram='{"type":"sequence","title":"Record flow","nodes":[
  {"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"new line"}},
  {"id":"b","label":"Service","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}],
 "edges":[{"from":"a","to":"b","label":"refresh","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}'
anchored_review "$good_diagram"
capture_body "$EVAL_SANDBOX/body-anchored-off.md" --force
assert_exit "anchored default-off post exits 0" 0
assert_file_nogrep "anchored default off draws no behavioral block" "$EVAL_SANDBOX/body-anchored-off.md" "Behavior flow"
# Off means off: nothing is settled, so the stored receipt carries no diagram
# either — no surface reports a picture the body does not draw.
assert_eq "anchored default off stores no evidence.diagram" "false" \
  "$(jq -r '.evidence | has("diagram")' <"$EVAL_SANDBOX/.verdict/state/$PR/post.json")"
capture_body "$EVAL_SANDBOX/body-anchored.md" --force --diagram-anchored
assert_exit "--diagram-anchored post exits 0" 0
assert_file_grep "verified diagram renders its summary" "$EVAL_SANDBOX/body-anchored.md" \
  '<summary>Behavior flow (each element anchored to a diff line — 3/3 anchored)</summary>' "-F"
assert_file_grep "verified diagram renders the declared type" "$EVAL_SANDBOX/body-anchored.md" \
  'sequenceDiagram' "-F"
assert_file_grep "verified diagram draws the verified edge" "$EVAL_SANDBOX/body-anchored.md" \
  '    n1->>n2: refresh' "-F"
assert_file_nogrep "rendered block carries no agent-chosen id" "$EVAL_SANDBOX/body-anchored.md" 'Caller as' "-F"
assert_file_nogrep "rendered block carries no raw fence marker" "$EVAL_SANDBOX/body-anchored.md" '<!-- verdict-diagram' "-F"
# On/off env knobs take only true|false, like every REVIEW_PUBLISH_* switch.
export REVIEW_PUBLISH_DIAGRAMS_ANCHORED=true
capture_body "$EVAL_SANDBOX/body-anchored-env.md" --force
assert_exit "REVIEW_PUBLISH_DIAGRAMS_ANCHORED post exits 0" 0
assert_file_grep "the env knob turns the block on" "$EVAL_SANDBOX/body-anchored-env.md" 'Behavior flow' "-F"
capture_body "$EVAL_SANDBOX/body-anchored-envflag.md" --force --no-diagram-anchored
assert_exit "--no-diagram-anchored over the env post exits 0" 0
assert_file_nogrep "the flag beats the env knob" "$EVAL_SANDBOX/body-anchored-envflag.md" 'Behavior flow' "-F"
unset REVIEW_PUBLISH_DIAGRAMS_ANCHORED || true
mkdir -p "$EVAL_SANDBOX/xdg-anchored/verdict"
printf '{"publish":{"diagrams":{"anchored":true}}}' > "$EVAL_SANDBOX/xdg-anchored/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-anchored"
capture_body "$EVAL_SANDBOX/body-anchored-file.md" --force
assert_exit "publish.diagrams.anchored post exits 0" 0
assert_file_grep "the settings knob turns the block on" "$EVAL_SANDBOX/body-anchored-file.md" 'Behavior flow' "-F"
unset XDG_CONFIG_HOME || true
# The block sits beside the computed topology: topology first, then the
# behavioral diagram, then the findings.
topo_at="$(grep -n 'Change topology (computed from the diff)' "$EVAL_SANDBOX/body-anchored.md" | head -1 | cut -d: -f1)"
behav_at="$(grep -n 'Behavior flow (each element anchored to a diff line' "$EVAL_SANDBOX/body-anchored.md" | head -1 | cut -d: -f1)"
if [ -n "$topo_at" ] && [ -n "$behav_at" ] && [ "$topo_at" -lt "$behav_at" ]; then
  good "the behavioral block lands right after the topology block"
else
  bad "block order: topology at '${topo_at}', behavioral at '${behav_at}'"
fi
# The persisted receipt and the dry run publish one disposition for the same
# review: post.json stores the settled counts, and the dry run reports them.
assert_eq "post.json persists the settled receipt" "sequence Record flow 3 3 0" \
  "$(jq -r '.evidence.diagram | "\(.type) \(.title) \(.supplied) \(.verified) \(.dropped)"' <"$bundle_dir/post.json")"
run_review post acme/app "$PR" --dry-run --json --force --diagram-anchored
assert_exit "dry-run of an anchored diagram exits 0" 0
# dry_run_matches_receipt <label> <status> — the dry run's typed body.diagram
# is post.json's evidence.diagram plus the status, field for field.
dry_run_matches_receipt() {
  assert_eq "$1" \
    "$(jq -cS --arg st "$2" '.evidence.diagram + {status: $st}' <"$bundle_dir/post.json")" \
    "$(jq -cS '.data.body.diagram' <"$EVAL_OUT")"
}
dry_run_matches_receipt "dry-run body.diagram is the stored receipt plus status rendered" rendered
assert_eq "dry-run body.diagram carries the title and counts" \
  '{"dropped":0,"status":"rendered","supplied":3,"title":"Record flow","type":"sequence","verified":3}' \
  "$(jq -cS '.data.body.diagram' <"$EVAL_OUT")"
# A diagram the diff cannot prove is omitted whole, and the run still succeeds.
anchored_review '{"type":"flow","nodes":[{"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"forged line"}}]}'
capture_body "$EVAL_SANDBOX/body-forged.md" --force --diagram-anchored
assert_exit "forged diagram post exits 0" 0
assert_file_grep "forged diagram leaves one omission note" "$EVAL_SANDBOX/body-forged.md" \
  '> Behavioral diagram omitted: none of the 1 supplied elements anchored (each did not verify or was malformed)' "-F"
# The topology block keeps its own fence; the forged diagram adds none.
assert_eq "forged diagram adds no fence beside the topology" 1 \
  "$(grep -c '^```mermaid$' "$EVAL_SANDBOX/body-forged.md")"
assert_file_nogrep "forged diagram draws no behavioral block" "$EVAL_SANDBOX/body-forged.md" 'Behavior flow' "-F"
run_review post acme/app "$PR" --dry-run --json --force --diagram-anchored
assert_exit "dry-run of a forged diagram exits 0" 0
dry_run_matches_receipt "dry-run body.diagram is the stored receipt plus status omitted" omitted
assert_eq "dry-run names the omission reason" \
  "omitted none of the 1 supplied elements anchored (each did not verify or was malformed)" \
  "$(jq -r '.data.body.diagram | "\(.status) \(.omitted)"' <"$EVAL_OUT")"
# A hostile label cannot break out of its node, the fence, or the <details>
# region: the posted body keeps exactly two fences and two regions.
anchored_review '{"type":"flow","nodes":[
  {"id":"a","label":"x\"]; click a evil\n```\n</details><script>alert(1)</script>","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}'
capture_body "$EVAL_SANDBOX/body-hostile.md" --force --diagram-anchored
assert_exit "hostile-label diagram post exits 0" 0
assert_file_grep "hostile label renders as inert text" "$EVAL_SANDBOX/body-hostile.md" \
  '    n1["x click a evil /detailsscriptalert(1)/script"]' "-F"
assert_file_nogrep "hostile label leaves no script tag" "$EVAL_SANDBOX/body-hostile.md" '<script>' "-F"
assert_eq "hostile label adds no fence" 4 "$(grep -c '^```' "$EVAL_SANDBOX/body-hostile.md")"
assert_eq "hostile label closes no extra region" 2 "$(grep -c '^</details>$' "$EVAL_SANDBOX/body-hostile.md")"
# An out-of-scope type is omitted, never refused.
anchored_review '{"type":"gantt","nodes":[{"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}'
capture_body "$EVAL_SANDBOX/body-gantt.md" --force --diagram-anchored
assert_exit "out-of-scope diagram type exits 0" 0
assert_file_grep "out-of-scope type names itself" "$EVAL_SANDBOX/body-gantt.md" \
  '> Behavioral diagram omitted: type `gantt` is out of scope (want sequence, state, flow)' "-F"
# A URL-shaped type is the one agent string outside the fence: it posts inside
# a code span, so the public comment carries no autolink.
anchored_review '{"type":"https://evil.example/x","nodes":[{"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}'
capture_body "$EVAL_SANDBOX/body-urltype.md" --force --diagram-anchored
assert_exit "URL-shaped diagram type exits 0" 0
assert_file_grep "URL-shaped type posts inside a code span" "$EVAL_SANDBOX/body-urltype.md" \
  '> Behavioral diagram omitted: type `https://evil.example/x` is out of scope (want sequence, state, flow)' "-F"
assert_eq "URL-shaped type appears nowhere outside the code span" 1 \
  "$(grep -c 'evil.example' "$EVAL_SANDBOX/body-urltype.md")"

# --- D7: unknown diagrams keys and bad values refuse typed bad-settings ---
mkdir -p "$EVAL_SANDBOX/xdg-dbad/verdict"
printf '{"publish":{"diagrams":{"motto":"ship it"}}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-dbad"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "unknown diagrams key refuses typed bad-settings"
else
  bad "unknown diagrams key gave exit $EVAL_STATUS"
fi
printf '{"publish":{"diagrams":{"structural":"yes"}}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "non-boolean diagrams value refuses typed bad-settings"
else
  bad "non-boolean diagrams value gave exit $EVAL_STATUS"
fi
printf '{"publish":{"diagrams":true}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
run_review check-tree --json
if [ "$EVAL_STATUS" -ne 0 ] \
  && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "bad-settings" ]; then
  good "non-object diagrams refuses typed bad-settings"
else
  bad "non-object diagrams gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true

# --- M1: manual documents structural diagrams and retirement of behavioral diagrams ---
if grep -Fq 'publish.diagrams.structural' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'retired from agent-authored' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'publish.diagrams.anchored' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq '"diagram"' "$EVAL_ROOT/docs/manual.md"; then
  good "manual documents structural diagrams, behavioral retirement, and the anchored field"
else
  bad "diagram documentation missing from docs/manual.md"
fi

# --- cleanup: no knob state leaks into later cases ---
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL || true
unset REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_ANCHORED || true
rm -rf "$EVAL_SANDBOX/xdg-nostruct" "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/xdg-dbad" \
  "$EVAL_SANDBOX/xdg-nostruct-body" "$EVAL_SANDBOX/xdg-noov-body" "$EVAL_SANDBOX/xdg-anchored"

eval_end
