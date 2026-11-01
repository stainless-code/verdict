#!/usr/bin/env bash
# Case doctor-scope-audit: token-scope excess audit in the doctor verb (slice 02).
#
# Grades (hermetic — network-dead gh stub with a header affordance driven by
# GH_STUB_DOCTOR, sandbox cwd, no fixtures beyond the sandbox itself):
#   S1  excess stub ("repo, delete_repo, admin:org, workflow, gist") →
#       token-scopes-excess WARN row naming the excess scopes with a try:
#       hint, exit 0, --json checks array carries it
#   S2  minimal stubs ("repo"; "public_repo, read:org") → no excess row,
#       exit 0 (read:* is baseline, not excess)
#   S3  no-header stub → today's fine-grained advisory row verbatim, exit 0,
#       no excess row
#   S4  insufficient stub ("gist") → today's insufficient-scope row verbatim
#       plus the excess row for the non-baseline grant; exit 0 (warn-only
#       never fails the run)
#   S5  text mode with excess stub → WARN line on stdout, exit stays 0
#   S6  doctor writes nothing to the sandbox (tree-clean: no .review/ dir)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

snap_tree() { # sandbox tree minus harness-owned capture files (content changes, not the tree)
  (cd "$EVAL_SANDBOX" && find . -mindepth 1 -maxdepth 2 | grep -v -e './eval-out.bin' -e './eval-err.txt' -e './gh-stub.log' | sort)
}
pre_tree="$(snap_tree)"

excess_rows() { # count of token-scopes-excess rows in the last --json stdout
  jq -r '[.data.checks[] | select(.name == "token-scopes-excess")] | length' <"$EVAL_OUT" 2>/dev/null || echo "?"
}

# --- S1: excess scopes → WARN row, exit 0, checks array carries it ---
export GH_STUB_DOCTOR="repo, delete_repo, admin:org, workflow, gist"
user_calls_before="$(grep -c "^stub-gh api user --include" "$EVAL_STUBLOG" || true)"
run_review doctor --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "S1 excess stub exits 0 (warn-only)"
else
  bad "S1 excess stub exits $EVAL_STATUS, want 0"
fi
if [ "$(stdout_docs)" = "1" ] \
  && [ "$(excess_rows)" = "1" ] \
  && jq -e '.data.checks[] | select(.name == "token-scopes-excess") | select(.severity == "warn" and .ok == false)' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "S1 --json checks array carries one warn/false token-scopes-excess row"
else
  bad "S1 checks array misses the row: $(head -c 400 "$EVAL_OUT")"
fi
detail="$(jq -r '.data.checks[] | select(.name == "token-scopes-excess") | .detail' <"$EVAL_OUT" 2>/dev/null || true)"
if [[ "$detail" == *"delete_repo"* && "$detail" == *"admin:org"* && "$detail" == *"workflow"* && "$detail" == *"gist"* && "$detail" == *"try:"* ]]; then
  good "S1 detail names the excess scopes with a try: hint"
else
  bad "S1 detail misses scopes or hint: $detail"
fi
elist="$(printf '%s' "$detail" | sed -e 's/.*need: //' -e 's/ — try:.*//')"
if [[ ", $elist," == *", repo,"* || ", $elist," == *", public_repo,"* ]]; then
  bad "S1 baseline leaked into excess list: $elist"
else
  good "S1 baseline scopes (repo) are not listed as excess"
fi
if jq -e '.warnings[] | contains("token-scopes-excess")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.checks[] | select(.name == "token-scopes") | "\(.severity)/\(.ok)"' <"$EVAL_OUT" 2>/dev/null || true)" = "warn/true" ]; then
  good "S1 row lands in warnings while token-scopes stays warn/true"
else
  bad "S1 warnings array or token-scopes row off: $(head -c 300 "$EVAL_OUT")"
fi
if grep -q "WARN token-scopes-excess" "$EVAL_ERR"; then
  good "S1 text line prints WARN token-scopes-excess"
else
  bad "S1 text line missing (want WARN token-scopes-excess)"
fi
n_user_calls="$(( $(grep -c "^stub-gh api user --include" "$EVAL_STUBLOG" || true) - user_calls_before ))"
if [ "$n_user_calls" = "1" ]; then
  good "S1 single gh api user read (no new network calls)"
else
  bad "S1 gh api user call count off: $n_user_calls"
fi

# --- S2: minimal grants → no excess row, exit 0 ---
for scopes in "repo" "public_repo, read:org"; do
  export GH_STUB_DOCTOR="$scopes"
  run_review doctor --json
  if [ "$EVAL_STATUS" -eq 0 ] && [ "$(excess_rows)" = "0" ]; then
    good "S2 minimal stub '$scopes' is clean (exit 0, no excess row)"
  else
    bad "S2 stub '$scopes' gave exit $EVAL_STATUS with $(excess_rows) excess row(s)"
  fi
done

# --- S3: no header → today's fine-grained advisory verbatim ---
export GH_STUB_DOCTOR="__NO_HEADER__"
run_review doctor --json
want_adv="no scopes header — fine-grained token, scopes proven at post time"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.checks[] | select(.name == "token-scopes") | "\(.severity)/\(.ok)"' <"$EVAL_OUT" 2>/dev/null || true)" = "warn/true" ] \
  && [ "$(jq -r '.data.checks[] | select(.name == "token-scopes") | .detail' <"$EVAL_OUT" 2>/dev/null || true)" = "$want_adv" ] \
  && [ "$(excess_rows)" = "0" ]; then
  good "S3 no-header advisory byte-identical, exit 0, no excess row"
else
  bad "S3 advisory drifted: $(jq -r '.data.checks[] | select(.name == "token-scopes")' <"$EVAL_OUT" 2>/dev/null || head -c 300 "$EVAL_OUT")"
fi

# --- S4: insufficient scope → today's row verbatim + excess row, exit 0 ---
export GH_STUB_DOCTOR="gist"
run_review doctor --json
want_ins="needs repo (private) or public_repo scope — x-oauth-scopes: gist"
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.checks[] | select(.name == "token-scopes") | "\(.severity)/\(.ok)"' <"$EVAL_OUT" 2>/dev/null || true)" = "warn/false" ] \
  && [ "$(jq -r '.data.checks[] | select(.name == "token-scopes") | .detail' <"$EVAL_OUT" 2>/dev/null || true)" = "$want_ins" ]; then
  good "S4 insufficient-scope row byte-identical (warn/false)"
else
  bad "S4 insufficient row drifted: $(jq -r '.data.checks[] | select(.name == "token-scopes")' <"$EVAL_OUT" 2>/dev/null || head -c 300 "$EVAL_OUT")"
fi
if [ "$(excess_rows)" = "1" ] \
  && jq -e '.data.checks[] | select(.name == "token-scopes-excess") | select(.severity == "warn" and .ok == false) | .detail | contains("gist")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "S4 non-baseline grant also raises the excess row (still exit 0)"
else
  bad "S4 excess row missing for gist grant"
fi

# --- S5: text mode with excess stub → WARN line, exit 0 ---
export GH_STUB_DOCTOR="repo, delete_repo, workflow"
run_review doctor
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "WARN token-scopes-excess" "$EVAL_OUT"; then
  good "S5 text mode prints WARN token-scopes-excess with exit 0"
else
  bad "S5 text mode gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_OUT")"
fi
unset GH_STUB_DOCTOR

# --- S6: doctor writes nothing (tree-clean: sandbox tree identical) ---
post_tree="$(snap_tree)"
if [ "$post_tree" = "$pre_tree" ]; then
  good "S6 sandbox tree unchanged (doctor wrote nothing)"
else
  bad "S6 sandbox tree changed: $(diff <(printf '%s\n' "$pre_tree") <(printf '%s\n' "$post_tree") | head -5)"
fi

if [ "$fails" -ne 0 ]; then echo "case doctor-scope-audit: FAIL ($fails)"; exit 1; fi
echo "case doctor-scope-audit: PASS"
