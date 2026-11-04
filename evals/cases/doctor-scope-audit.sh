#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

snap_tree() { # sandbox tree minus harness-owned capture/minimal-PATH files (content changes, not the tree)
  (cd "$EVAL_SANDBOX" && find . -mindepth 1 -maxdepth 2 | grep -v -e './eval-out.bin' -e './eval-err.txt' -e './gh-stub.log' -e './minbin' | sort) || true
}
pre_tree="$(snap_tree)"

excess_rows() { # count of token-scopes-excess rows in the last --json stdout
  jq -r '[.data.checks[] | select(.name == "token-scopes-excess")] | length' <"$EVAL_OUT" 2>/dev/null || echo "?"
}

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

for scopes in "repo" "public_repo, read:org"; do
  export GH_STUB_DOCTOR="$scopes"
  run_review doctor --json
  if [ "$EVAL_STATUS" -eq 0 ] && [ "$(excess_rows)" = "0" ]; then
    good "S2 minimal stub '$scopes' is clean (exit 0, no excess row)"
  else
    bad "S2 stub '$scopes' gave exit $EVAL_STATUS with $(excess_rows) excess row(s)"
  fi
done

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

export GH_STUB_DOCTOR="repo, delete_repo, workflow"
run_review doctor
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "WARN token-scopes-excess" "$EVAL_OUT"; then
  good "S5 text mode prints WARN token-scopes-excess with exit 0"
else
  bad "S5 text mode gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_OUT")"
fi
unset GH_STUB_DOCTOR

run_review doctor
stamp="$(head -n 1 "$EVAL_OUT")"
summary="$(tail -n 1 "$EVAL_OUT")"
if printf '%s' "$stamp" | matches -E '^doctor @ [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'; then
  good "D1 first line is the doctor run stamp"
else
  bad "D1 first line: $(printf '%s' "$stamp" | head -c 120)"
fi
if printf '%s' "$summary" | matches -E '^Summary: (healthy|unhealthy) — [0-9]+ ok, [0-9]+ warn, [0-9]+ fail$'; then
  word="${summary#Summary: }"
  word="${word%% *}"
  if { [ "$EVAL_STATUS" -eq 0 ] && [ "$word" = "healthy" ]; } \
    || { [ "$EVAL_STATUS" -ne 0 ] && [ "$word" = "unhealthy" ]; }; then
    good "D2 summary word '$word' agrees with exit $EVAL_STATUS"
  else
    bad "D2 summary word '$word' disagrees with exit $EVAL_STATUS"
  fi
else
  bad "D2 last line: $(printf '%s' "$summary" | head -c 120)"
fi

MINBIN="$EVAL_SANDBOX/minbin"
mkdir -p "$MINBIN"
for tool in bash dirname git ps readlink; do
  ln -s "$(command -v "$tool")" "$MINBIN/$tool"
done
ln -s "$EVAL_STUBBIN/gh" "$MINBIN/gh"
export GH_STUB_DOCTOR="repo"
set +e
( cd "$EVAL_SANDBOX" && PATH="$MINBIN" "$EVAL_CLI" doctor --json >"$EVAL_OUT" 2>"$EVAL_ERR" )
st=$?
set -e
unset GH_STUB_DOCTOR
if [ "$st" -eq 0 ] \
  && jq -e '.data.healthy == true
    and ([.data.checks[].name] | index("jq") == null)
    and ([.data.checks[].name] | index("shasum") == null)' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "D3 minimal PATH without jq/shasum is healthy (no phantom rows, exit 0)"
else
  bad "D3 minimal PATH exits $st: $(head -c 300 "$EVAL_OUT")"
fi

post_tree="$(snap_tree)"
if [ "$post_tree" = "$pre_tree" ]; then
  good "S6 sandbox tree unchanged (doctor wrote nothing)"
else
  bad "S6 sandbox tree changed: $(diff <(printf '%s\n' "$pre_tree") <(printf '%s\n' "$post_tree") | head -5)"
fi

eval_end
