#!/usr/bin/env bash
#
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

HEADA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
HEADB="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
HEADC="cccccccccccccccccccccccccccccccccccccccc"
PATCH1="9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"
PATCH2="0123456789abcdef0123456789abcdef01234567"
PATCH3="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "graphql" ]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}'; exit 0
fi
case "$*" in
  *reviews*)
    printf '%s' '{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}'
    exit 0;;
  *pulls*)
    printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0;;
  *user*)
    printf 'someone-else'; exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

reviews_posts_since() {
  local n
  n=$(tail -n +"$(($1 + 1))" "$EVAL_STUBLOG" 2>/dev/null | grep -c "reviews" || true)
  printf '%s' "$n"
}
mark() { wc -l <"$EVAL_STUBLOG" | tr -d ' '; }

file_sha() { # $1 = file — portable sha256 of a file
  (sha256sum "$1" 2>/dev/null || shasum -a 256 "$1") | cut -d' ' -f1
}
mk_url() { # $1 = pr — stamp the PR record URL so same-repo bundles match
  jq --arg u "https://github.com/acme/app/pull/$1" '.url = $u' \
    "$EVAL_SANDBOX/.review/$1/PR.json" > "$EVAL_SANDBOX/.review/$1/PR.json.new"
  mv "$EVAL_SANDBOX/.review/$1/PR.json.new" "$EVAL_SANDBOX/.review/$1/PR.json"
}
mk_identity() { # $1 = dir, $2 = patch, $3 = head — coherent: real byte hashes
  local d="$EVAL_SANDBOX/.review/$1" sha
  sha="$(file_sha "$d/full.diff")"
  jq -n --arg p "$2" --arg h "$3" --arg s "$sha" \
    '{schema_version:"1",patch_id:$p,patch_id_kind:"git-patch-id",diff_sha256:$s,raw_diff_sha256:$s,head_sha:$h,base_sha:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}' \
    > "$d/identity.json"
}
mk_twin() { # $1 = slug, $2 = patch, $3 = repo — coherent local twin
  local d="$EVAL_SANDBOX/.review/$1" sha
  mkdir -p "$d"
  cp "$EVAL_SANDBOX/.review/4425/full.diff" "$d/full.diff"
  sha="$(file_sha "$d/full.diff")"
  jq -n --arg p "$2" --arg s "$sha" \
    '{schema_version:"1",patch_id:$p,patch_id_kind:"git-patch-id",diff_sha256:$s,raw_diff_sha256:$s,head_sha:"cccccccccccccccccccccccccccccccccccccccc",base_sha:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}' \
    > "$d/identity.json"
  jq -n --arg r "$3" \
    '{schema_version:"1",repo:$r,head_sha:"cccccccccccccccccccccccccccccccccccccccc",base_sha:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}' \
    > "$d/source.json"
}
mk_posted() { # $1 = pr, $2 = head, $3 = patch
  jq -n --arg h "$2" --arg p "$3" \
    '{schema_version:"1",review:{id:99},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:null,security_summary:null,patch_id:$p,patch_id_kind:"git-patch-id"}' \
    > "$EVAL_SANDBOX/.review/$1/post.json"
}
mk_comment() { # $1 = pr
  eval_mk_bundle "$1"
  eval_write_review "$1" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}
EOF
}
mk_head() { # $1 = pr, $2 = head — move the bundle to a new fetched SHA
  jq --arg h "$2" '.headRefOid = $h' "$EVAL_SANDBOX/.review/$1/PR.json" \
    > "$EVAL_SANDBOX/.review/$1/PR.json.new"
  mv "$EVAL_SANDBOX/.review/$1/PR.json.new" "$EVAL_SANDBOX/.review/$1/PR.json"
}

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q 2>/dev/null || true
  git config user.email "patch-id@example.com"
  git config user.name "patch-id case"
  printf 'one\ntwo\nthree\n' > patch.txt
  git add patch.txt
  git commit -qm base 2>/dev/null || true
  printf 'one\nTWO\nthree\n' > patch.txt
  git add patch.txt
  git commit -qm change 2>/dev/null || true
)
A="$(cd "$EVAL_SANDBOX" && git rev-parse HEAD~1)"
B="$(cd "$EVAL_SANDBOX" && git rev-parse HEAD)"

run_review fetch-local acme/app pidwork --range "$A..$B" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.dir' <"$EVAL_OUT")" = ".review/local-pidwork" ] \
  && [ "$(jq -r '.data.artifacts."identity.json"' <"$EVAL_OUT")" = ".review/local-pidwork/identity.json" ]; then
  good "fetch-local writes identity.json and lists it in artifacts"
else
  bad "fetch-local exits $EVAL_STATUS: $(head -c 300 "$EVAL_ERR")"
fi
if [ "$(jq -r '.patch_id_kind' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" = "git-patch-id" ] \
  && [ "$(jq -r '.patch_id | length' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" = "40" ] \
  && [ "$(jq -r '.head_sha' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" = "$B" ] \
  && [ "$(jq -r '.base_sha' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" = "$A" ] \
  && [ "$(jq -r '.diff_sha256' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" = "$(jq -r '.raw_diff_sha256' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" ]; then
  good "identity.json carries the git patch id over the raw diff with head/base SHAs"
else
  bad "identity.json: $(cat "$EVAL_SANDBOX/.review/local-pidwork/identity.json" 2>/dev/null)"
fi
P1="$(jq -r '.patch_id' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")"
(
  cd "$EVAL_SANDBOX" || exit 99
  git commit -q --amend -m "change (reworded, same tree)" 2>/dev/null || true
)
B2="$(cd "$EVAL_SANDBOX" && git rev-parse HEAD)"
run_review fetch-local acme/app pidwork --range "$A..$B2" --json >/dev/null 2>&1
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.patch_id' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" = "$P1" ]; then
  good "amend with the same tree keeps the patch id"
else
  bad "amend changed the patch id to $(jq -r '.patch_id' "$EVAL_SANDBOX/.review/local-pidwork/identity.json" 2>/dev/null)"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  printf 'one\nTWO\nTHREE\n' > patch.txt
  git add patch.txt
  git commit -qm change2 2>/dev/null || true
)
C="$(cd "$EVAL_SANDBOX" && git rev-parse HEAD)"
run_review fetch-local acme/app pidwork --range "$A..$C" --json >/dev/null 2>&1
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.patch_id' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" != "$P1" ]; then
  good "a changed diff gets a new patch id"
else
  bad "changed diff kept patch id $P1"
fi
if [ "$(jq -r '.patch_id' "$EVAL_SANDBOX/.review/local-pidwork/source.json")" = "$(jq -r '.patch_id' "$EVAL_SANDBOX/.review/local-pidwork/identity.json")" ] \
  && [ "$(jq -r '.patch_id_kind' "$EVAL_SANDBOX/.review/local-pidwork/source.json")" = "git-patch-id" ]; then
  good "source.json stamps the patch provenance"
else
  bad "source.json misses the patch stamp"
fi

mk_comment 4421
mk_url 4421
mk_identity 4421 "$PATCH1" "$HEADA"
mk_comment 4422
mk_url 4422
mk_head 4422 "$HEADB"
mk_identity 4422 "$PATCH1" "$HEADB"
mk_posted 4421 "$HEADA" "$PATCH1"
m="$(mark)"
run_review post acme/app 4422 --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -eq 1 ] && [ "$code" = "already-reviewed-patch" ] \
  && [ "$(reviews_posts_since "$m")" -eq 0 ]; then
  good "same patch at a new SHA refuses already-reviewed-patch with no POST"
else
  bad "same-patch post gave exit $EVAL_STATUS code '$code'"
fi
if jq -e '.error.message | contains("pass --force to post again")' <"$EVAL_OUT" >/dev/null 2>&1 \
  && jq -e '.error.message | contains("try:")' <"$EVAL_OUT" >/dev/null 2>&1; then
  good "refusal names --force plus the try: hint"
else
  bad "refusal text: $(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null | head -c 300)"
fi
m="$(mark)"
run_review post acme/app 4422 --force --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "--force posts the same change (exit 0, one POST)"
else
  bad "forced post gave exit $EVAL_STATUS"
fi
if [ "$(jq -r '.forced[0].reason // empty' "$EVAL_SANDBOX/.review/4422/post.json")" = "already-reviewed-patch" ] \
  && [ "$(jq -r '.forced[0].previous_bundle // empty' "$EVAL_SANDBOX/.review/4422/post.json")" = "4421" ] \
  && [ "$(jq -r '.forced[0].previous_head // empty' "$EVAL_SANDBOX/.review/4422/post.json")" = "$HEADA" ] \
  && [ "$(jq -r '.patch_id // empty' "$EVAL_SANDBOX/.review/4422/post.json")" = "$PATCH1" ]; then
  good "forced post records reason, prior bundle, prior head, and patch id"
else
  bad "post.json: $(cat "$EVAL_SANDBOX/.review/4422/post.json" 2>/dev/null | head -c 500)"
fi

mk_comment 4423
mk_url 4423
mk_head 4423 "$HEADC"
mk_identity 4423 "$PATCH2" "$HEADC"
m="$(mark)"
run_review post acme/app 4423 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "a different patch at a new SHA posts normally"
else
  bad "different-patch post gave exit $EVAL_STATUS"
fi

mk_comment 4424
m="$(mark)"
run_review post acme/app 4424 --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "patch identity unavailable" "$EVAL_ERR" \
  && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "legacy bundle without identity warns and proceeds"
else
  bad "legacy post gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

eval_mk_bundle 4425
mk_url 4425
mk_identity 4425 "$PATCH1" "$HEADA"
mk_twin local-pidtwin "$PATCH1" "acme/app"
cat > "$EVAL_SANDBOX/.review/local-pidtwin/findings.json" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Twin finding.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review adopt acme/app 4425 --from auto --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.from' <"$EVAL_OUT")" = "auto -> local-pidtwin" ]; then
  good "adopt --from auto matches exactly one twin"
else
  bad "adopt auto gave exit $EVAL_STATUS: $(jq -c '.error // .data' <"$EVAL_OUT" 2>/dev/null)"
fi
mkdir -p "$EVAL_SANDBOX/.review/local-pidtwo"
cp "$EVAL_SANDBOX/.review/local-pidtwin/identity.json" "$EVAL_SANDBOX/.review/local-pidtwo/identity.json"
cp "$EVAL_SANDBOX/.review/local-pidtwin/source.json" "$EVAL_SANDBOX/.review/local-pidtwo/source.json"
cp "$EVAL_SANDBOX/.review/local-pidtwin/full.diff" "$EVAL_SANDBOX/.review/local-pidtwo/full.diff"
cp "$EVAL_SANDBOX/.review/local-pidtwin/findings.json" "$EVAL_SANDBOX/.review/local-pidtwo/findings.json"
eval_mk_bundle 4426
mk_url 4426
mk_identity 4426 "$PATCH1" "$HEADA"
run_review adopt acme/app 4426 --from auto --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "ambiguous-patch-match" ]; then
  good "two twins refuse as ambiguous-patch-match"
else
  bad "ambiguous adopt gave exit $EVAL_STATUS code '$code'"
fi
eval_mk_bundle 4427
mk_url 4427
mk_identity 4427 "$PATCH2" "$HEADA"
run_review adopt acme/app 4427 --from auto --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "no-patch-match" ]; then
  good "no twin refuses as no-patch-match"
else
  bad "no-twin adopt gave exit $EVAL_STATUS code '$code'"
fi

eval_mk_bundle 4430
mk_url 4430
mk_identity 4430 "$PATCH3" "$HEADA"
mk_twin local-foreign "$PATCH3" "other/repository"
cat > "$EVAL_SANDBOX/.review/local-foreign/findings.json" <<'EOF'
[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Foreign finding.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]
EOF
run_review adopt acme/app 4430 --from auto --json
code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
if [ "$EVAL_STATUS" -ne 0 ] && [ "$code" = "no-patch-match" ]; then
  good "a same-patch foreign-repo twin is excluded as no-patch-match"
else
  bad "foreign-twin adopt gave exit $EVAL_STATUS code '$code'"
fi

mk_comment 4431
mk_url 4431
mk_identity 4431 "$PATCH3" "$HEADA"
eval_mk_bundle 4432
mk_identity 4432 "$PATCH3" "$HEADA"
jq '.url = "https://github.com/other/repository/pull/4432"' \
  "$EVAL_SANDBOX/.review/4432/PR.json" > "$EVAL_SANDBOX/.review/4432/PR.json.new"
mv "$EVAL_SANDBOX/.review/4432/PR.json.new" "$EVAL_SANDBOX/.review/4432/PR.json"
mk_posted 4432 "$HEADA" "$PATCH3"
m="$(mark)"
run_review post acme/app 4431 --json
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(reviews_posts_since "$m")" -eq 1 ]; then
  good "a same-patch foreign-repo receipt never blocks the post"
else
  bad "foreign-receipt post gave exit $EVAL_STATUS"
fi

eval_mk_bundle 4428
mk_url 4428
mk_head 4428 "$HEADB"
printf '[]' | eval_write_findings 4428
mk_identity 4428 "$PATCH1" "$HEADB"
mk_posted 4428 "$HEADA" "$PATCH1"
run_review report acme/app 4428 --completeness
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "same patch reviewed at ${HEADB:0:7} (posted ${HEADA:0:7})" "$EVAL_ERR"; then
  good "report --completeness passes a rebase with the same patch"
else
  bad "same-patch completeness gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
eval_mk_bundle 4429
mk_url 4429
mk_head 4429 "$HEADB"
printf '[]' | eval_write_findings 4429
mk_identity 4429 "$PATCH2" "$HEADB"
mk_posted 4429 "$HEADA" "$PATCH1"
run_review report acme/app 4429 --completeness
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "stale-head" "$EVAL_ERR"; then
  good "report --completeness still fails for a different patch"
else
  bad "different-patch completeness gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

eval_mk_bundle 4433
mk_url 4433
mk_head 4433 "$HEADB"
printf '[]' | eval_write_findings 4433
mk_identity 4433 "$PATCH1" "$HEADB"
mk_posted 4433 "$HEADA" "$PATCH1"
printf 'stale bytes\n' > "$EVAL_SANDBOX/.review/4433/full.diff"
run_review report acme/app 4433 --completeness
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "patch identity unavailable" "$EVAL_ERR"; then
  good "stale bundle bytes never relax completeness"
else
  bad "stale-bytes completeness gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

eval_mk_bundle 4434
mk_url 4434
mk_head 4434 "$HEADB"
printf '[]' | eval_write_findings 4434
mk_identity 4434 "not-a-patch-id" "$HEADB"
mk_posted 4434 "$HEADA" "not-a-patch-id"
run_review report acme/app 4434 --completeness
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "patch identity unavailable" "$EVAL_ERR"; then
  good "an invalid patch id warns and falls back to heads"
else
  bad "invalid-id completeness gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

eval_mk_bundle 4435
mk_url 4435
mk_head 4435 "$HEADB"
printf '[]' | eval_write_findings 4435
mk_identity 4435 "$PATCH1" "$HEADB"
jq -n --arg p "$PATCH1" '{patch_id:$p,patch_id_kind:"git-patch-id"}' \
  > "$EVAL_SANDBOX/.review/4435/post.json"
run_review report acme/app 4435 --completeness
if [ "$EVAL_STATUS" -ne 0 ] && grep -q "incomplete" "$EVAL_ERR"; then
  good "a two-field post stub proves no publication"
else
  bad "stub-receipt completeness gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi

eval_end
