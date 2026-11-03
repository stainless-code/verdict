#!/usr/bin/env bash
# Case adopt: local-to-PR finding adoption (offline).
#
# Grades (hermetic — crafted local + PR bundles in the sandbox, stub gh):
#   A1  happy path: findings copy verbatim with the handoff stamp
#       (lastSeen + adopted history), open count, human line, --json
#       envelope shape with the adopt data keys.
#   A2  re-anchor: covered anchors stay, drifted anchors clear to false.
#   A3  refusals: bad-target (local pr), bad-from, missing-findings,
#       missing-diff, provider-unimplemented, already-adopted, corrupt
#       from-store — all typed with try: hints, nothing written.
#   A4  offline: zero stub-gh intercepts across every leg.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

mk_local_findings() { # $1 = bare slug, stdin = findings array
  local dir="$EVAL_SANDBOX/.review/local-$1"
  mkdir -p "$dir"
  cat > "$dir/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}
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
  cat > "$dir/findings.json"
}

# --- A1: happy path ---
PR=4311
eval_mk_bundle "$PR"
mk_local_findings adopt1 <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Adopt me.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]},
{"kind":"inline","path":"notes.txt","side":"RIGHT","line":99,"anchored":true,"body":"Drifted anchor here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"123456789abcdef0123456789abcdef012345678","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review adopt acme/app "$PR" --from adopt1 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ]; then
  good "happy adopt exits 0 with one envelope"
else
  bad "happy adopt exits $EVAL_STATUS"
fi
if [ "$(jq -r '.verb' <"$EVAL_OUT")" = "adopt" ] \
  && [ "$(jq -r '.data.from' <"$EVAL_OUT")" = "adopt1" ] \
  && [ "$(jq -r '.data.adopted' <"$EVAL_OUT")" = "2" ] \
  && [ "$(jq -r '.data.open' <"$EVAL_OUT")" = "2" ] \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "envelope carries from/adopted/open with null error"
else
  bad "envelope data: $(jq -c '.data' <"$EVAL_OUT" 2>/dev/null)"
fi
if grep -q "» adopted 2 findings from .review/local-adopt1 (open: 2)" "$EVAL_ERR"; then
  good "human progress names the handoff on stderr"
else
  bad "human line: $(head -c 200 "$EVAL_ERR")"
fi
if [ "$(jq -r '.[0].lastSeen' "$EVAL_SANDBOX/.review/$PR/findings.json")" = "aaaaaaa" ] \
  && [ "$(jq -r '.[0].history[-1].event' "$EVAL_SANDBOX/.review/$PR/findings.json")" = "adopted" ] \
  && [ "$(jq -r '.[0].history[-1].note' "$EVAL_SANDBOX/.review/$PR/findings.json")" = "from adopt1" ]; then
  good "handoff stamp lands (lastSeen + adopted history)"
else
  bad "handoff stamp missing"
fi

# --- A2: re-anchor keeps covered, clears drifted ---
if [ "$(jq -r '.[0].anchored' "$EVAL_SANDBOX/.review/$PR/findings.json")" = "true" ] \
  && [ "$(jq -r '.[1].anchored' "$EVAL_SANDBOX/.review/$PR/findings.json")" = "false" ]; then
  good "covered anchor kept, drifted anchor cleared"
else
  bad "anchors: $(jq -c 'map(.anchored)' "$EVAL_SANDBOX/.review/$PR/findings.json")"
fi

# --- A3: refusals ---
run_review adopt acme/app local-foo --from adopt1 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-target" ]; then
  good "local target refuses as bad-target"
else
  bad "local target gave exit $EVAL_STATUS code '$code'"
fi
run_review adopt acme/app 4312 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "bad-from" ]; then
  good "missing --from refuses as bad-from"
else
  bad "missing --from gave exit $EVAL_STATUS code '$code'"
fi
run_review adopt acme/app 4313 --from nope-missing --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "missing-findings" ]; then
  good "missing from-store refuses as missing-findings"
else
  bad "missing from-store gave exit $EVAL_STATUS code '$code'"
fi
PR=4314
eval_mk_bundle "$PR"
rm "$EVAL_SANDBOX/.review/$PR/full.diff"
run_review adopt acme/app "$PR" --from adopt1 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "missing-diff" ]; then
  good "missing target diff refuses as missing-diff"
else
  bad "missing diff gave exit $EVAL_STATUS code '$code'"
fi
if jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "refusals carry try: hints"
else
  bad "refusals missing try: hints"
fi
run_review adopt acme/app "$PR" --from adopt1 --json >/dev/null 2>&1
before_sum="$(shasum -a 1 "$EVAL_SANDBOX/.review/$PR/PR.json" | cut -d' ' -f1)"
run_review adopt acme/app "$PR" --from adopt1 --json >/dev/null 2>&1
after_sum="$(shasum -a 1 "$EVAL_SANDBOX/.review/$PR/PR.json" | cut -d' ' -f1)"
if [ "$before_sum" = "$after_sum" ] && [ ! -e "$EVAL_SANDBOX/.review/$PR/findings.json" ]; then
  good "refused adopt writes nothing"
else
  bad "refused adopt mutated the target"
fi
# already-adopted: the A1 target now carries findings.
run_review adopt acme/app 4311 --from adopt1 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "already-adopted" ]; then
  good "second adopt refuses as already-adopted"
else
  bad "second adopt gave exit $EVAL_STATUS code '$code'"
fi
# corrupt from-store refuses typed instead of dying in jq.
mk_local_findings adoptbad <<'EOF'
[{"kind":"inline","sig":"nope"
EOF
run_review adopt acme/app 4315 --from adoptbad --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "corrupt-store" ] \
  && jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "corrupt from-store refuses typed corrupt-store with a try: hint"
else
  bad "corrupt from-store gave exit $EVAL_STATUS code '$code'"
fi

# --- A4: offline across every leg ---
if [ "$(( $(wc -l <"$EVAL_STUBLOG" | tr -d ' ') - stub_before ))" = "0" ]; then
  good "zero stub-gh intercepts across adopt legs (offline)"
else
  bad "stub gh intercepted calls during adopt legs"
fi

eval_end
