#!/usr/bin/env bash
# Case version-source: package.json is the single version source.
#
# Grades (all hermetic — repo itself as fixture, stub gh, no network):
#   V1  `verdict --version` stdout equals `jq -r .version package.json`.
#   V2  the rendered footer resolves `{version}` to the same string on a
#       healthy tree (report --output carries `Verdict <version>`, never
#       the degrade `0.0.0-unknown`); no second version source exists (no
#       VERSION file at the root, the version is read from package.json at
#       runtime, every `.version` read targets package.json).
#   V3  degrade path: with package.json unreadable, `--version` reads
#       `0.0.0-unknown` and the footer renders `Verdict 0.0.0-unknown`
#       (the same string; never a failure).
#   D1  docs: CONTRIBUTING ## Versioning states the changeset rule; the manual
#       points at it rather than duplicating it.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

VER="$(jq -r .version "$EVAL_ROOT/package.json")"

# --- V1: --version is single-sourced on package.json ---
run_review --version
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(tr -d '\n' <"$EVAL_OUT")" = "$VER" ]; then
  good "--version equals package.json ($VER)"
else
  bad "--version gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_OUT")"
fi

# --- V2: footer carries the same version on a healthy tree ---
PR=4601
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":true,"comments":[]}
EOF
eval_write_findings "$PR" <<'EOF'
[]
EOF
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4601.md"
assert_exit "rendered footer succeeds" 0
assert_file_grep "rendered footer carries the package.json version" "$EVAL_SANDBOX/out-4601.md" "Verdict $VER" "-F"
assert_file_nogrep "rendered footer never says the degrade" "$EVAL_SANDBOX/out-4601.md" 'Verdict 0.0.0-unknown' "-F"
if [ -e "$EVAL_ROOT/VERSION" ] || [ -e "$EVAL_ROOT/version.txt" ]; then
  bad "second version source file exists at the repo root"
else
  good "no second version source file at the repo root"
fi
if grep -rn -- "-version" "$EVAL_ROOT/cmd/verdict" | grep -v '_test\.go' | matches 'package\.json'; then
  good "the CLI reads its version from package.json at runtime"
else
  bad "no package.json version read in cmd/verdict"
fi
# --include rides before the paths: BSD grep treats options after the
# first operand as filenames, so the GNU-permuted order silently searched
# every file type (goldens included).
# Both sides are computed first: an empty scan on either side is a distinct
# diagnosis, not a pass (test-audit F5 — a bare negated grep over a pipeline
# cannot tell "no literal" from "the search never ran").
unknown_literals="$(grep -rn --include='*.go' -- '"0\.0\.0-unknown"' "$EVAL_ROOT/cmd/verdict" "$EVAL_ROOT/internal" | grep -v '_test\.go' || true)"
release_literals="$(grep -rn --include='*.go' -- '"0\.[0-9][0-9]*\.[0-9][0-9]*' "$EVAL_ROOT/cmd/verdict" "$EVAL_ROOT/internal" | grep -v '_test\.go' | grep -v '0\.0\.0-unknown' || true)"
if [ -z "$unknown_literals" ]; then
  bad "the 0.0.0-unknown degrade fallback is missing from the Go sources"
elif [ -n "$release_literals" ]; then
  bad "a release version literal lives in the Go sources: $(printf '%s' "$release_literals" | head -c 300)"
else
  good "the only version literal in the Go sources is the 0.0.0-unknown degrade fallback"
fi

# --- V3: degrade path with package.json unreadable ---
# A copied binary with no checkout above it: the version lookup and the
# footer must degrade, never fail.
FAKE="$EVAL_SANDBOX/fake-root"
mkdir -p "$FAKE/fakebin"
cp "$EVAL_CLI" "$FAKE/fakebin/verdict"
set +e
"$FAKE/fakebin/verdict" --version >"$EVAL_SANDBOX/fake-version.txt" 2>/dev/null
fake_status=$?
set -e
if [ "$fake_status" -eq 0 ] && [ "$(tr -d '\n' <"$EVAL_SANDBOX/fake-version.txt")" = "0.0.0-unknown" ]; then
  good "unreadable package.json degrades --version to 0.0.0-unknown"
else
  bad "degraded --version gave exit $fake_status: $(head -c 200 "$EVAL_SANDBOX/fake-version.txt")"
fi
PR=4602
eval_mk_bundle "$PR"
eval_write_review "$PR" <<'EOF'
{"schema_version":"1","body":"> ✅ Looks fine.","approved":true,"comments":[]}
EOF
eval_write_findings "$PR" <<'EOF'
[]
EOF
set +e
(
  cd "$EVAL_SANDBOX" || exit 99
  # V3 grades the binary degrade path: the fake root holds no package.json
  # above the copied binary, so the footer degrades without failing.
  PATH="$EVAL_STUBBIN:$PATH" "$FAKE/fakebin/verdict" report acme/app "$PR" \
    --output "$EVAL_SANDBOX/out-4602.md" >"$EVAL_SANDBOX/fake-report.txt" 2>/dev/null
)
fake_status=$?
set -e
if [ "$fake_status" -eq 0 ] && grep -Fq 'Verdict 0.0.0-unknown' "$EVAL_SANDBOX/out-4602.md"; then
  good "unreadable package.json still renders Verdict 0.0.0-unknown, never fails"
else
  bad "degraded footer gave exit $fake_status"
fi
rm -rf "$FAKE"

# --- D1: CONTRIBUTING canonical, manual points rather than duplicates ---
if grep -Fq '## Versioning' "$EVAL_ROOT/CONTRIBUTING.md" \
  && grep -Fq '.changeset/' "$EVAL_ROOT/CONTRIBUTING.md"; then
  good "CONTRIBUTING ## Versioning states the changeset rule"
else
  bad "CONTRIBUTING ## Versioning missing the changeset rule"
fi
assert_file_grep "manual points at CONTRIBUTING.md ## Versioning" "$EVAL_ROOT/docs/manual.md" 'CONTRIBUTING.md ## Versioning' "-F"
assert_file_nogrep "manual never duplicates the version-packages rule" "$EVAL_ROOT/docs/manual.md" 'version packages' "-F"

eval_end
