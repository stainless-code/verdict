#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

SKILLS="$EVAL_SANDBOX/skills"
SKILL="$SKILLS/verdict"

run_review skill
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_OUT" "$EVAL_SKILL/SKILL.md"; then
  good "skill prints the checked-in SKILL.md byte-for-byte"
else
  bad "skill exits $EVAL_STATUS or differs from SKILL.md"
fi
run_review skill files
if [ "$EVAL_STATUS" -eq 0 ] && grep -qx "scripts/verdict" "$EVAL_OUT" \
  && grep -qx "prompts/review.md" "$EVAL_OUT" \
  && LC_ALL=C sort -c "$EVAL_OUT"; then
  good "skill files lists the sorted payload including scripts/verdict"
else
  bad "skill files output wrong (exit $EVAL_STATUS)"
fi
run_review skill --format json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT")" = "$EVAL_ENVELOPE_KEYS" ] \
  && [ "$(jq -r '.data.name' <"$EVAL_OUT")" = "verdict" ] \
  && [ "$(jq -r '.data.version' <"$EVAL_OUT")" = "$(jq -r .version "$EVAL_ROOT/package.json")" ] \
  && jq -e '.data.files | index("scripts/verdict") != null' <"$EVAL_OUT" >/dev/null \
  && jq -e '.error == null' <"$EVAL_OUT" >/dev/null; then
  good "skill --format json is one envelope with name/version/files"
else
  bad "skill --format json envelope wrong (exit $EVAL_STATUS)"
fi

run_review skill install --dir "$SKILLS" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] && [ ! -e "$SKILL" ] && grep -q "(create)" "$EVAL_OUT"; then
  good "install --dry-run previews create and writes nothing"
else
  bad "dry-run exits $EVAL_STATUS or created the target"
fi
run_review skill install --dir "$SKILLS" --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.status' <"$EVAL_OUT")" = "create" ] \
  && [ "$(jq -r '.data.written' <"$EVAL_OUT")" -gt 0 ] \
  && cmp -s "$SKILL/SKILL.md" "$EVAL_SKILL/SKILL.md" \
  && cmp -s "$SKILL/prompts/review.md" "$EVAL_SKILL/prompts/review.md" \
  && [ -x "$SKILL/scripts/verdict" ] \
  && [ "$(jq -r '.tool' "$SKILL/.verdict-managed")" = "verdict" ] \
  && [ "$(jq -r '.version' "$SKILL/.verdict-managed")" = "$(jq -r .version "$EVAL_ROOT/package.json")" ]; then
  good "install writes the payload atomically with an executable shim and receipt"
else
  bad "install output or payload wrong (exit $EVAL_STATUS)"
fi
run_review skill install --dir "$SKILLS"
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "(keep)" "$EVAL_OUT"; then
  good "re-run reads keep (idempotent)"
else
  bad "re-run exits $EVAL_STATUS without keep"
fi
printf 'stale\n' >"$SKILL/SKILL.md"
run_review skill install --dir "$SKILLS"
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$SKILL/SKILL.md" "$EVAL_SKILL/SKILL.md"; then
  good "byte drift updates the installed copy"
else
  bad "update did not restore SKILL.md"
fi

FOREIGN="$EVAL_SANDBOX/foreign"
mkdir -p "$FOREIGN/verdict"
printf 'foreign bytes\n' >"$FOREIGN/verdict/keep.txt"
run_review skill install --dir "$FOREIGN"
if [ "$EVAL_STATUS" -eq 1 ] && grep -q "non-verdict skill dir" "$EVAL_ERR" \
  && [ ! -e "$FOREIGN/verdict/SKILL.md" ] \
  && grep -q "foreign bytes" "$FOREIGN/verdict/keep.txt"; then
  good "foreign dir refuses typed and writes nothing"
else
  bad "foreign refusal exits $EVAL_STATUS"
fi
run_review skill install --dir "$FOREIGN" --dry-run
if [ "$EVAL_STATUS" -eq 0 ] && grep -q "(blocked)" "$EVAL_OUT"; then
  good "dry-run previews blocked for a foreign dir"
else
  bad "foreign dry-run exits $EVAL_STATUS without blocked"
fi
run_review skill install --dir "$FOREIGN" --force
if [ "$EVAL_STATUS" -eq 0 ] && [ -x "$FOREIGN/verdict/scripts/verdict" ] \
  && grep -q "foreign bytes" "$FOREIGN/verdict/keep.txt"; then
  good "--force writes only verdict's payload and spares foreign files"
else
  bad "--force exits $EVAL_STATUS or touched foreign files"
fi

ESCAPE_INSTALL="$EVAL_SANDBOX/install-escape"
ESCAPE_FRESH="$EVAL_SANDBOX/install-fresh"
ESCAPE_OUTSIDE="$EVAL_SANDBOX/install-outside"
mkdir -p "$ESCAPE_OUTSIDE" "$ESCAPE_FRESH/verdict"
printf 'outside bytes\n' >"$ESCAPE_OUTSIDE/review.md"
dir_hash() {
  (cd "$1" && find . -type f | LC_ALL=C sort | while IFS= read -r f; do shasum "$f"; done | shasum)
}
run_review skill install --dir "$ESCAPE_INSTALL" --json
assert_exit "seed an escaping install" 0
rm -rf "$ESCAPE_INSTALL/verdict/prompts"
ln -s "$ESCAPE_OUTSIDE" "$ESCAPE_INSTALL/verdict/prompts"
ln -s "$ESCAPE_OUTSIDE" "$ESCAPE_FRESH/verdict/prompts"
outside_before="$(dir_hash "$ESCAPE_OUTSIDE")"

run_review skill install --dir "$ESCAPE_INSTALL" --json
assert_exit "install on an escaping payload dir refuses" 1
assert_refusal "escaping payload dir (upgrade)" "skill-foreign-conflict"
assert_json_field "the refusal names the upgrade escape" "true" '.error.message | contains("path escapes from parent")'

run_review skill install --dir "$ESCAPE_FRESH" --json
assert_exit "a fresh install on an escaping payload dir refuses" 1
assert_refusal "escaping payload dir (fresh)" "skill-foreign-conflict"

run_review skill install --dir "$ESCAPE_FRESH" --force --json
assert_exit "install --force on an escaping payload dir refuses" 1
assert_refusal "escaping payload dir (--force)" "skill-foreign-conflict"

run_review skill status --dir "$ESCAPE_INSTALL" --json
assert_exit "status on an escaping payload dir refuses" 1
assert_refusal "status names the escaping payload dir" "skill-foreign-conflict"
assert_json_field "status names the root escape" "true" '.error.message | contains("path escapes from parent")'

outside_after="$(dir_hash "$ESCAPE_OUTSIDE")"
if [ "$outside_before" = "$outside_after" ] \
  && [ "$(find "$ESCAPE_OUTSIDE" -name '.tmp-*' | wc -l | tr -d ' ')" = "0" ] \
  && [ -f "$ESCAPE_OUTSIDE/review.md" ] && grep -qx 'outside bytes' "$ESCAPE_OUTSIDE/review.md" \
  && cmp -s "$ESCAPE_INSTALL/verdict/SKILL.md" "$EVAL_SKILL/SKILL.md" \
  && [ -f "$ESCAPE_INSTALL/verdict/.verdict-managed" ] \
  && [ ! -e "$ESCAPE_FRESH/verdict/SKILL.md" ] && [ ! -e "$ESCAPE_FRESH/verdict/.verdict-managed" ]; then
  good "the install escape refused before writing and left the outside dir byte-identical"
else
  bad "the install escape wrote outside, left a temp file, or partially installed"
fi

jq '.version="0.0.0-stale"' "$SKILL/.verdict-managed" >"$EVAL_SANDBOX/rec.tmp"
mv "$EVAL_SANDBOX/rec.tmp" "$SKILL/.verdict-managed"
skill_sum="$(shasum "$SKILL/SKILL.md")"
run_review skill install --dir "$SKILLS"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -q "receipt refreshed" "$EVAL_OUT" \
  && [ "$(jq -r '.version' "$SKILL/.verdict-managed")" = "$(jq -r .version "$EVAL_ROOT/package.json")" ] \
  && [ "$(shasum "$SKILL/SKILL.md")" = "$skill_sum" ]; then
  good "stale receipt refreshes on keep without touching payload bytes"
else
  bad "stale receipt leg exits $EVAL_STATUS"
fi

FAKEBIN="$EVAL_SANDBOX/fakebin"
MISSBIN="$EVAL_SANDBOX/missbin"
mkdir -p "$FAKEBIN" "$MISSBIN"
cat >"$FAKEBIN/verdict" <<'EOF'
#!/bin/sh
printf 'path-verdict %s\n' "$*"
EOF
cat >"$FAKEBIN/custom-verdict" <<'EOF'
#!/bin/sh
printf 'custom-verdict %s\n' "$*"
EOF
cat >"$MISSBIN/curl" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$EVAL_SANDBOX/miss-curl.log"
exit 1
EOF
chmod +x "$FAKEBIN/verdict" "$FAKEBIN/custom-verdict" "$MISSBIN/curl"
SHIM="$SKILL/scripts/verdict"
set +e
PATH="$FAKEBIN:/usr/bin:/bin" sh "$SHIM" --version >"$EVAL_OUT" 2>"$EVAL_ERR"
st=$?
set -e
if [ "$st" -eq 0 ] && [ "$(cat "$EVAL_OUT")" = "path-verdict --version" ]; then
  good "shim resolves verdict on PATH"
else
  bad "shim PATH resolution exits $st"
fi
set +e
PATH="$FAKEBIN:/usr/bin:/bin" VERDICT_BIN="$FAKEBIN/custom-verdict" sh "$SHIM" --version >"$EVAL_OUT" 2>"$EVAL_ERR"
st=$?
set -e
if [ "$st" -eq 0 ] && [ "$(cat "$EVAL_OUT")" = "custom-verdict --version" ]; then
  good "shim prefers VERDICT_BIN over PATH"
else
  bad "shim VERDICT_BIN resolution exits $st"
fi
set +e
PATH="$MISSBIN:/usr/bin:/bin" sh "$SHIM" fetch acme/app 1 >"$EVAL_OUT" 2>"$EVAL_ERR"
st=$?
set -e
if [ "$st" -eq 1 ] && [ ! -s "$EVAL_OUT" ] \
  && grep -q "missing-go-binary" "$EVAL_ERR" \
  && grep -q "scripts/verdict bootstrap" "$EVAL_ERR" \
  && { [ ! -f "$EVAL_SANDBOX/miss-curl.log" ] || [ ! -s "$EVAL_SANDBOX/miss-curl.log" ]; }; then
  good "shim miss is typed missing-go-binary and never fetches"
else
  bad "shim miss exits $st"
fi

REL="$EVAL_SANDBOX/release"
REL_URL="$(file_url "$REL")"
RELPAY="$EVAL_SANDBOX/release-payload"
mkdir -p "$RELPAY"
cat >"$RELPAY/verdict" <<'EOF'
#!/bin/sh
printf 'bootstrapped-verdict 1.2.3\n'
EOF
chmod +x "$RELPAY/verdict"
: >"$RELPAY/LICENSE"
: >"$RELPAY/README.md"
: >"$RELPAY/NOTICE.md"
for v in 1.2.3 2.0.0; do
  mkdir -p "$REL/v$v"
  cp "$EVAL_ROOT/install.sh" "$REL/v$v/install.sh"
  (cd "$RELPAY" && tar -czf "$REL/v$v/verdict_${v}_linux_amd64.tar.gz" verdict LICENSE README.md NOTICE.md)
  printf '{}\n' >"$REL/v$v/checksums.txt.sigstore.json"
  printf '72e7d304d3cb8b85bc6c18eab2e51a50bd38fdad\n' >"$REL/v$v/commit.txt"
done
(cd "$REL/v1.2.3" && shasum -a 256 verdict_1.2.3_linux_amd64.tar.gz >checksums.txt)
printf '%064d  verdict_2.0.0_linux_amd64.tar.gz\n' 0 >"$REL/v2.0.0/checksums.txt"

PLAT="$EVAL_SANDBOX/platbin"
mkdir -p "$PLAT"
cat >"$PLAT/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exec command -p uname "$@" ;;
esac
EOF
cat >"$PLAT/sysctl" <<'EOF'
#!/bin/sh
printf '0\n'
EOF
cat >"$PLAT/cosign" <<'EOF'
#!/bin/sh
exit 0
EOF
REAL_CURL="$(command -v curl)"
cat >"$PLAT/curl" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$EVAL_SANDBOX/bootstrap-curl.log"
exec "$REAL_CURL" "\$@"
EOF
chmod +x "$PLAT/uname" "$PLAT/sysctl" "$PLAT/cosign" "$PLAT/curl"
BOOTPATH="$PLAT:/usr/bin:/bin:/usr/sbin:/sbin"
mkdir -p "$EVAL_SANDBOX/boot-home"
rm -rf "${SKILL:?}/bin"
printf '{"tool":"verdict","version":"1.2.3","installed_at":"2026-11-02T00:00:00Z"}\n' >"$SKILL/.verdict-managed"
: >"$EVAL_SANDBOX/bootstrap-curl.log"
set +e
(cd "$EVAL_SANDBOX" && HOME="$EVAL_SANDBOX/boot-home" PATH="$BOOTPATH" \
    VERDICT_DOWNLOAD_URL="$REL_URL" VERDICT_COSIGN="$PLAT/cosign" sh "$SHIM" bootstrap) >"$EVAL_OUT" 2>"$EVAL_ERR"
st=$?
set -e
if [ "$st" -eq 0 ] && [ -x "$SKILL/bin/verdict" ] \
  && "$SKILL/bin/verdict" >"$EVAL_OUT" 2>/dev/null \
  && grep -q "bootstrapped-verdict 1.2.3" "$EVAL_OUT" \
  && grep -q '"version":"1.2.3"' "$SKILL/bin/.verdict-installer" \
  && grep -q "/v1.2.3/install.sh" "$EVAL_SANDBOX/bootstrap-curl.log"; then
  good "bootstrap fetches the pinned install.sh and installs the verified binary"
else
  bad "bootstrap exits $st ($(head -c 200 "$EVAL_ERR"))"
fi
: >"$EVAL_SANDBOX/bootstrap-curl.log"
set +e
(cd "$EVAL_SANDBOX" && HOME="$EVAL_SANDBOX/boot-home" PATH="$BOOTPATH" \
    VERDICT_DOWNLOAD_URL="$REL_URL" VERDICT_COSIGN="$PLAT/cosign" sh "$SHIM" bootstrap) >"$EVAL_OUT" 2>"$EVAL_ERR"
st=$?
set -e
if [ "$st" -eq 0 ] && grep -q "already bootstrapped" "$EVAL_OUT" \
  && [ ! -s "$EVAL_SANDBOX/bootstrap-curl.log" ]; then
  good "bootstrap is idempotent and makes no second network call"
else
  bad "idempotent bootstrap exits $st or fetched again"
fi
rm -rf "${SKILL:?}/bin"
printf '{"tool":"verdict","version":"2.0.0","installed_at":"2026-11-02T00:00:00Z"}\n' >"$SKILL/.verdict-managed"
set +e
(cd "$EVAL_SANDBOX" && HOME="$EVAL_SANDBOX/boot-home" PATH="$BOOTPATH" \
    VERDICT_DOWNLOAD_URL="$REL_URL" VERDICT_COSIGN="$PLAT/cosign" sh "$SHIM" bootstrap) >"$EVAL_OUT" 2>"$EVAL_ERR"
st=$?
set -e
if [ "$st" -ne 0 ] && grep -q "bootstrap-failed" "$EVAL_ERR" \
  && [ ! -e "$SKILL/bin/verdict" ]; then
  good "checksum mismatch refuses and leaves no partial binary"
else
  bad "bad bootstrap exits $st (partial binary left?)"
fi

eval_end
