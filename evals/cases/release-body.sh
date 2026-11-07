#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

STUB_SAVED="$EVAL_SANDBOX/gh-stub-orig"
cp "$EVAL_STUBBIN/gh" "$STUB_SAVED"
restore_stub() { cp "$STUB_SAVED" "$EVAL_STUBBIN/gh"; }
trap restore_stub EXIT

BODY="$EVAL_SANDBOX/release-body.txt"
: > "$BODY"
cat > "$EVAL_STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
sandbox="${EVAL_SANDBOX:-/tmp}"
case "$*" in
  api\ repos/*/releases/tags/*)
    if [ -f "$sandbox/release-absent" ]; then
      echo "gh: Not Found (HTTP 404)" >&2
      exit 1
    fi
    cat "$sandbox/release-body.txt"
    exit 0;;
  "release edit "*)
    prev=""
    for arg in "$@"; do
      if [ "$prev" = "--notes-file" ] && [ ! -f "$sandbox/release-ignore-edit" ]; then
        cp "$arg" "$sandbox/release-body.txt"
      fi
      prev="$arg"
    done
    echo "https://example.invalid/releases/tag/pinned"
    exit 0;;
esac
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

cd "$EVAL_ROOT"
VERSION="$(node -p "require('./package.json').version")"
NOTES="$EVAL_SANDBOX/expected-notes.md"
node scripts/release-notes.mjs "$VERSION" > "$NOTES"

edits() { grep -c "^stub-gh release edit" "$EVAL_STUBLOG" 2>/dev/null || true; }
run_body() { # runs the body stage against the stub; leaves status + streams
  set +e
  PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/release.sh" body \
    >"$EVAL_OUT" 2>"$EVAL_ERR"
  EVAL_STATUS=$?
  set -e
}
clear_markers() {
  rm -f "$EVAL_SANDBOX/release-absent" "$EVAL_SANDBOX/release-ignore-edit"
}

if [ ! -s "$NOTES" ]; then
  bail "scripts/release-notes.mjs produced no notes for $VERSION; the release body has no source"
fi

# Leg 1 — the published body is empty: the stage must rewrite it from the
# changelog and prove the read-back, not trust that publishing set a body.
clear_markers
: > "$BODY"
before="$(edits)"
run_body
assert_exit "empty body is repaired" 0
if cmp -s "$BODY" "$NOTES"; then
  good "release body equals CHANGELOG.md's $VERSION section"
else
  bad "release body does not equal CHANGELOG.md's $VERSION section"
fi
after="$(edits)"
assert_eq "the repair calls gh release edit once" "$((before + 1))" "$after"

# Leg 2 — the body already matches: no write, no churn, exit 0. This is what
# makes a re-run of the publish script safe.
cp "$NOTES" "$BODY"
before="$(edits)"
run_body
assert_exit "matching body is left alone" 0
assert_eq "no edit is issued for a matching body" "$before" "$(edits)"

# Leg 3 — an edit that does not stick must fail the release, not report
# success while the release page stays wrong.
clear_markers
: > "$BODY"
touch "$EVAL_SANDBOX/release-ignore-edit"
run_body
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "an edit that does not stick fails the run (exit $EVAL_STATUS)"
else
  bad "an edit that does not stick reported success"
fi
if matches -F "still does not match" "$EVAL_ERR"; then
  good "the failure names the unapplied body"
else
  bad "the failure does not name the unapplied body: $(cat "$EVAL_ERR")"
fi

# Leg 4 — no release to attach a body to: refuse instead of silently passing.
clear_markers
touch "$EVAL_SANDBOX/release-absent"
run_body
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "a missing release fails the body stage (exit $EVAL_STATUS)"
else
  bad "a missing release passed the body stage"
fi
if matches -F "does not exist" "$EVAL_ERR"; then
  good "the failure names the missing release"
else
  bad "the failure does not name the missing release: $(cat "$EVAL_ERR")"
fi
clear_markers

eval_end
