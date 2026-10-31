#!/usr/bin/env bash
# Case publish-diagram: opt-in behavioral diagram (P4).
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   D1  knob matrix: off default strips (exit 0, disposition stripped);
#       file behavioral=true keeps collapsed and silent; file expanded=true
#       renders open (body-chars delta of exactly 5 for <details> open).
#   D2  contract presence: prompts/diagrams.md carries the 7-type map,
#       honesty rule, one-diagram cap, tables-stay-tables, marker pair, and
#       generated label; review.md references the fragment.
#   D3  grammar validation: beta keyword drops with a note; unbalanced drops
#       fails-soft; empty drops; cap keeps the first; unmarked valid keeps
#       with a warn; fenceless markers drop — all exit 0, never a failure.
#   D4  structural split: diagrams.structural=false hides the P3 topology
#       half but keeps the table; the default keeps today's bytes.
#   D5  no-diagram default byte-parity: the pre-P4 HEAD script renders
#       identical report bytes and identical dry-run stdout on a
#       diagram-less bundle.
#   D6  precedence flags > file > env on the new knobs.
#   D7  unknown diagrams keys and non-boolean values refuse typed bad-settings.
#   M1  the manual carries the behavioral paragraph (knobs, flags, markers).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_PUBLISH_OVERVIEW || true

diagram_status() { jq -r '.data.body.diagram.status // empty' <"$EVAL_OUT" 2>/dev/null; }
diagram_field() { jq -r ".data.body.diagram.$1 // empty" <"$EVAL_OUT" 2>/dev/null; }
diagram_bool() { jq -r ".data.body.diagram.$1" <"$EVAL_OUT" 2>/dev/null; }
body_chars() { grep -o 'body chars: [0-9]*' "$EVAL_OUT" | grep -o '[0-9]*'; }

# --- D1a: off default strips (opt-in, renders only when set) ---
PR=4701
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n**Reviewed changes**\n\n- `notes.txt` — one line changed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n```mermaid\nsequenceDiagram\n    participant C as Caller\n    participant S as Service\n    C->>S: request\n    S-->>C: response\n```\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "stripped" ] \
  && [ "$(diagram_field blocks)" = "1" ] \
  && grep -q "publish.diagrams.behavioral is off" "$EVAL_ERR"; then
  good "off default strips the block (exit 0, disposition stripped)"
else
  bad "off-default gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "^diagram: stripped" "$EVAL_OUT"; then
  good "human dry-run names the stripped disposition"
else
  bad "human dry-run disposition missing (exit $EVAL_STATUS)"
fi

# --- D1b: file behavioral=true keeps collapsed and silent ---
mkdir -p "$EVAL_SANDBOX/xdg-beh/verdict"
printf '{"publish":{"diagrams":{"behavioral":true}}}' > "$EVAL_SANDBOX/xdg-beh/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-beh"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "kept" ] \
  && [ "$(diagram_field keyword)" = "sequenceDiagram" ] \
  && [ "$(diagram_bool collapsed)" = "true" ] \
  && [ "$(diagram_bool wrapped)" = "true" ] \
  && [ "$(diagram_field note)" = "" ] \
  && ! grep -qi 'diagram' "$EVAL_ERR"; then
  good "behavioral=true keeps the block collapsed with no warnings"
else
  bad "kept path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
run_review post acme/app "$PR" --dry-run
collapsed_chars="$(body_chars)"
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "^diagram: kept" "$EVAL_OUT"; then
  good "human dry-run names the kept disposition ($collapsed_chars chars)"
else
  bad "human kept disposition missing (exit $EVAL_STATUS)"
fi

# --- D1c: expanded renders open (wrapper swap is exactly 5 chars: " open") ---
printf '{"publish":{"diagrams":{"behavioral":true,"expanded":true}}}' > "$EVAL_SANDBOX/xdg-beh/verdict/settings.json"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "kept" ] \
  && [ "$(diagram_bool collapsed)" = "false" ]; then
  good "expanded=true renders the kept block open"
else
  bad "expanded path gave exit $EVAL_STATUS collapsed '$(diagram_bool collapsed)'"
fi
run_review post acme/app "$PR" --dry-run
expanded_chars="$(body_chars)"
if [ "$EVAL_STATUS" -eq 0 ] && [ "$((expanded_chars - collapsed_chars))" = "5" ]; then
  good "open wrapper adds exactly 5 body chars (<details> -> <details open>)"
else
  bad "wrapper delta wrong ($collapsed_chars -> $expanded_chars)"
fi
unset XDG_CONFIG_HOME || true

# --- D2: contract presence (map + honesty + cap in the prompt text) ---
frag="$EVAL_ROOT/prompts/diagrams.md"
if [ -f "$frag" ] \
  && grep -Fq 'sequenceDiagram' "$frag" && grep -Fq 'stateDiagram' "$frag" \
  && grep -Fq 'gitGraph' "$frag" && grep -Fq 'quadrantChart' "$frag" \
  && grep -Fq 'classDiagram' "$frag" && grep -Fq 'erDiagram' "$frag" \
  && grep -Fq 'gantt' "$frag" && grep -Fq 'flowchart' "$frag" \
  && grep -Fq 'mindmap' "$frag"; then
  good "fragment carries the 7-type selection map"
else
  bad "selection map incomplete in prompts/diagrams.md"
fi
if grep -Fq 'illustrative' "$frag" \
  && grep -Fq 'One diagram max' "$frag" \
  && grep -Fq 'stay markdown tables' "$frag" \
  && grep -Fq 'verdict-diagram:start' "$frag" \
  && grep -Fq 'verdict-diagram:end' "$frag" \
  && grep -Fq 'verify against the diff' "$frag"; then
  good "fragment carries honesty label, cap, tables rule, markers, generated label"
else
  bad "contract clauses missing from prompts/diagrams.md"
fi
if grep -Fq 'prompts/diagrams.md' "$EVAL_ROOT/prompts/review.md" \
  && grep -Fq 'publish.diagrams.behavioral' "$EVAL_ROOT/prompts/review.md"; then
  good "review.md references the fragment and the opt-in knob"
else
  bad "review.md hook missing"
fi

# --- D3b: beta keyword drops with a note, never fails ---
PR=4702
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n```mermaid\nxychart-beta\n    title Beta\n```\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
mkdir -p "$EVAL_SANDBOX/xdg-d3/verdict"
printf '{"publish":{"diagrams":{"behavioral":true}}}' > "$EVAL_SANDBOX/xdg-d3/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-d3"
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "dropped" ] \
  && grep -q "unknown keyword 'xychart-beta'" "$EVAL_ERR" \
  && [ "$(diagram_field note)" = "> Diagram omitted: unknown keyword 'xychart-beta' (want one of: flowchart graph mindmap sequenceDiagram stateDiagram gitGraph quadrantChart classDiagram erDiagram gantt) — see prompts/diagrams.md." ]; then
  good "beta keyword exits 0, warns, drops with the allowlist note"
else
  bad "beta path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi

# --- D3c: unbalanced fence drops fails-soft ---
PR=4703
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n```mermaid\nsequenceDiagram\n    A->>B: unclosed","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "dropped" ] \
  && grep -q "unbalanced fence" "$EVAL_ERR"; then
  good "unclosed fence exits 0 and drops fails-soft"
else
  bad "unbalanced path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi

# --- D3d: empty fence drops ---
PR=4704
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n```mermaid\n\n```\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "dropped" ] \
  && grep -q "empty fence" "$EVAL_ERR"; then
  good "empty fence exits 0 and drops with a note"
else
  bad "empty path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi

# --- D3e: cap keeps the first, drops the extra ---
PR=4705
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n```mermaid\nsequenceDiagram\n    A->>B: first\n```\n\n</details>\n<!-- verdict-diagram:end -->\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n```mermaid\nflowchart TD\n    A --> B\n```\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "kept" ] \
  && [ "$(diagram_field keyword)" = "sequenceDiagram" ] \
  && [ "$(diagram_field blocks)" = "2" ] \
  && grep -q "one-diagram cap" "$EVAL_ERR"; then
  good "cap keeps the first block and drops the extra with a note"
else
  bad "cap path gave exit $EVAL_STATUS status '$(diagram_status)' kw '$(diagram_field keyword)'"
fi

# --- D3f: unmarked valid fence keeps with an advisory warn ---
PR=4706
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n```mermaid\nsequenceDiagram\n    A->>B: bare\n```","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "kept" ] \
  && grep -q "outside the marked wrapper" "$EVAL_ERR"; then
  good "unmarked valid fence keeps (keep-biased) with a wrapper warn"
else
  bad "unmarked path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi

# --- D3g: fenceless markers drop ---
PR=4707
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\nno fence here\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "dropped" ] \
  && grep -q "no mermaid fence" "$EVAL_ERR"; then
  good "marked block without a fence drops with a note"
else
  bad "fenceless path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "preflight: body=verdict" "$EVAL_OUT"; then
  good "fenceless drop preserves the verdict marker (no content eaten past the span)"
else
  bad "fenceless drop ate the callout (exit $EVAL_STATUS)"
fi
unset XDG_CONFIG_HOME || true

# --- D8: indented fences parse; single-line fences never swallow; notes precede the divider ---
# The doc wrapper is 2-space indented: fences must parse inside 3 columns.
PR=4712
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\n  ```mermaid\n  sequenceDiagram\n  C->>S: hi\n  ```\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json --diagram-behavioral
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "kept" ] \
  && [ "$(diagram_field keyword)" = "sequenceDiagram" ]; then
  good "indented multi-line fence validates and keeps"
else
  bad "indented fence gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
run_review post acme/app "$PR" --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(diagram_status)" = "stripped" ]; then
  good "indented block is detected by the opt-in gate when off"
else
  bad "indented block evaded the gate (exit $EVAL_STATUS status '$(diagram_status)')"
fi
# A non-indented single-line fence is not a GitHub block: it must not open a
# swallow, and the valid block after it must still keep.
PR=4713
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>First (single-line, invalid)</summary>\n\n```mermaid sequenceDiagram C->>S: hi ```\n\n</details>\n<!-- verdict-diagram:end -->\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Second (valid)</summary>\n\n```mermaid\nsequenceDiagram\nC->>S: hi\n```\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run --json --diagram-behavioral
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(diagram_status)" = "kept" ] \
  && [ "$(diagram_field blocks)" = "2" ] \
  && ! grep -q "unbalanced fence" "$EVAL_ERR"; then
  good "single-line fence never opens a swallow; later valid block keeps"
else
  bad "single-line fence path gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
# Notes-before-divider idempotency: a pasted-output body (footer + note)
# re-renders byte-identically to the fresh run — the note survives strip-replace.
PR=4714
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\nno fence here\n\n</details>\n<!-- verdict-diagram:end -->","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ]; then
  fresh_chars="$(body_chars)"
else
  fresh_chars="FAIL($EVAL_STATUS)"
fi
PR=4715
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.\n> Confidence: 4/5 · Safety: not assessed\n\n<!-- verdict-diagram:start -->\n<details>\n<summary>Behavior flow (generated — verify against the diff)</summary>\n\nno fence here\n\n</details>\n<!-- verdict-diagram:end -->\n\n<!-- verdict-footer -->\n> stale footer\n\n> Diagram omitted: stale reason","approved":true,"comments":[]}
EOF
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "replacing it with the canonical block" "$EVAL_ERR" \
  && [ "$(body_chars)" = "$fresh_chars" ]; then
  good "pasted footer+note re-renders byte-identically (note survives strip-replace)"
else
  bad "re-render parity gave exit $EVAL_STATUS chars '$(body_chars)' vs fresh '$fresh_chars'"
fi

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
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '<summary>Files changed (1)</summary>' "$EVAL_SANDBOX/out-4708.md" \
  && ! grep -q 'Change topology' "$EVAL_SANDBOX/out-4708.md" \
  && ! grep -q '```mermaid' "$EVAL_SANDBOX/out-4708.md"; then
  good "structural=false keeps the table, hides the topology diagram"
else
  bad "structural split gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4709.md" --overview
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '<summary>Change topology</summary>' "$EVAL_SANDBOX/out-4709.md"; then
  good "structural default keeps today's topology bytes"
else
  bad "structural default gave exit $EVAL_STATUS"
fi

# --- D5: no-diagram default byte-parity vs pre-P4 HEAD ---
PR=4710
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ No new issues found.\n> Confidence: 4/5 · Safety: not assessed","approved":true,"comments":[]}
EOF
eval_write_findings "$PR" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Parity probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"8888888888888888888888888888888888888888","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
mkdir -p "$EVAL_SANDBOX/xdg-empty/verdict" "$EVAL_SANDBOX/oldrun/scripts"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
git -C "$EVAL_ROOT" show HEAD:scripts/review.sh > "$EVAL_SANDBOX/oldrun/scripts/review.sh"
chmod +x "$EVAL_SANDBOX/oldrun/scripts/review.sh"
cp "$EVAL_ROOT/package.json" "$EVAL_SANDBOX/oldrun/package.json"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-new.md"
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "parity baseline exits $EVAL_STATUS"
else
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_SANDBOX/oldrun/scripts/review.sh" \
      report acme/app "$PR" --output "$EVAL_SANDBOX/out-old.md" \
      >"$EVAL_SANDBOX/old-out.txt" 2>"$EVAL_SANDBOX/old-err.txt"
  )
  if cmp -s "$EVAL_SANDBOX/out-old.md" "$EVAL_SANDBOX/out-new.md"; then
    good "report --output byte-identical to pre-P4 HEAD on a diagram-less bundle"
  else
    bad "report parity broke (old vs new differ)"
  fi
fi
run_review post acme/app "$PR" --dry-run
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "dry-run baseline exits $EVAL_STATUS"
else
  cp "$EVAL_OUT" "$EVAL_SANDBOX/new-dry.txt"
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_SANDBOX/oldrun/scripts/review.sh" \
      post acme/app "$PR" --dry-run >"$EVAL_SANDBOX/old-dry.txt" 2>/dev/null
  )
  if cmp -s "$EVAL_SANDBOX/old-dry.txt" "$EVAL_SANDBOX/new-dry.txt" \
    && ! grep -qi 'diagram' "$EVAL_SANDBOX/new-dry.txt"; then
    good "human dry-run stdout byte-identical with no diagram line"
  else
    bad "dry-run parity broke"
  fi
fi
unset XDG_CONFIG_HOME || true

# --- D6: precedence flags > file > env ---
mkdir -p "$EVAL_SANDBOX/xdg-doff/verdict"
printf '{"publish":{"diagrams":{"behavioral":false}}}' > "$EVAL_SANDBOX/xdg-doff/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-doff"
run_review post acme/app 4701 --dry-run --json --diagram-behavioral
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(diagram_status)" = "kept" ]; then
  good "flag --diagram-behavioral wins over a false file"
else
  bad "flag-over-file gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
run_review post acme/app 4701 --dry-run --json --diagram-behavioral --diagram-expanded
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(diagram_status)" = "kept" ] \
  && [ "$(diagram_bool collapsed)" = "false" ]; then
  good "flags --diagram-behavioral --diagram-expanded keep and open over a false file"
else
  bad "expanded flag gave exit $EVAL_STATUS status '$(diagram_status)' collapsed '$(diagram_bool collapsed)'"
fi
unset XDG_CONFIG_HOME || true
# shellcheck disable=SC2034
REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL="true"
export REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL
run_review post acme/app 4701 --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(diagram_status)" = "kept" ]; then
  good "env true alone (no file) keeps the block"
else
  bad "env alone gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
run_review post acme/app 4701 --dry-run --json --no-diagram-behavioral
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(diagram_status)" = "stripped" ]; then
  good "flag --no-diagram-behavioral wins over a true env"
else
  bad "no-flag-over-env gave exit $EVAL_STATUS status '$(diagram_status)'"
fi
unset REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL || true

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
printf '{"publish":{"diagrams":{"behavioral":"yes"}}}' > "$EVAL_SANDBOX/xdg-dbad/verdict/settings.json"
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

# --- M1: manual carries the behavioral paragraph ---
if grep -Fq 'publish.diagrams.behavioral' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq -- '--diagram-behavioral' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'verdict-diagram:start' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'byte-identical whether the knobs' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries knobs, flags, markers, and the parity note"
else
  bad "behavioral paragraph missing from docs/manual.md"
fi

# --- cleanup: no knob state leaks into later cases ---
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_PUBLISH_OVERVIEW || true
rm -rf "$EVAL_SANDBOX/xdg-beh" "$EVAL_SANDBOX/xdg-d3" "$EVAL_SANDBOX/xdg-nostruct" \
  "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/oldrun" "$EVAL_SANDBOX/xdg-doff" "$EVAL_SANDBOX/xdg-dbad"

if [ "$fails" -ne 0 ]; then echo "case publish-diagram: FAIL ($fails)"; exit 1; fi
echo "case publish-diagram: PASS"
