#!/usr/bin/env bash
# Case version-source: package.json is the single version source.
#
# Grades (all hermetic — repo itself as fixture, stub gh, no network):
#   V1  `verdict --version` stdout equals `jq -r .version package.json`.
#   V2  the rendered footer resolves `{version}` to the same string on a
#       healthy tree (report --output carries `Verdict <version>`, never
#       `unknown`); no second version source exists (no VERSION file at
#       the root, the version is read from package.json at runtime, every
#       `.version` read targets package.json).
#   V3  degrade path: with package.json unreadable, `--version` reads
#       `0.0.0-unknown` and the footer still renders `Verdict unknown`
#       (never a failure).
#   D1  docs: README ## Versioning states the changeset rule; the manual
#       points at it rather than duplicating it.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

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
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "Verdict $VER" "$EVAL_SANDBOX/out-4601.md" \
  && ! grep -Fq 'Verdict unknown' "$EVAL_SANDBOX/out-4601.md"; then
  good "rendered footer carries the package.json version, never unknown"
else
  bad "footer version wrong (exit $EVAL_STATUS)"
fi
if [ -e "$EVAL_ROOT/VERSION" ] || [ -e "$EVAL_ROOT/version.txt" ]; then
  bad "second version source file exists at the repo root"
else
  good "no second version source file at the repo root"
fi
if grep -rn -- "-version" "$EVAL_ROOT/cmd/verdict" | grep -v '_test\.go' | grep -q 'package\.json'; then
  good "the CLI reads its version from package.json at runtime"
else
  bad "no package.json version read in cmd/verdict"
fi
if grep -rn -- '"0\.0\.0-unknown"' "$EVAL_ROOT/cmd/verdict" "$EVAL_ROOT/internal" --include='*.go' | grep -v '_test\.go' | grep -q . \
  && ! grep -rn -- '"0\.[0-9][0-9]*\.[0-9][0-9]*' "$EVAL_ROOT/cmd/verdict" "$EVAL_ROOT/internal" --include='*.go' | grep -v '_test\.go' | grep -v '0\.0\.0-unknown' | grep -q .; then
  good "the only version literal in the Go sources is the 0.0.0-unknown degrade fallback"
else
  bad "a release version literal lives in the Go sources"
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
if [ "$fake_status" -eq 0 ] && grep -Fq 'Verdict unknown' "$EVAL_SANDBOX/out-4602.md"; then
  good "unreadable package.json still renders Verdict unknown, never fails"
else
  bad "degraded footer gave exit $fake_status"
fi
rm -rf "$FAKE"

# --- D1: README canonical, manual points rather than duplicates ---
if grep -Fq '## Versioning' "$EVAL_ROOT/README.md" \
  && grep -Fq '.changeset/' "$EVAL_ROOT/README.md"; then
  good "README ## Versioning states the changeset rule"
else
  bad "README ## Versioning missing the changeset rule"
fi
if grep -Fq 'README ## Versioning' "$EVAL_ROOT/docs/manual.md" \
  && ! grep -Fq 'version packages' "$EVAL_ROOT/docs/manual.md"; then
  good "manual points at README ## Versioning, never a second copy"
else
  bad "manual versioning pointer missing or rule duplicated"
fi

if [ "$fails" -ne 0 ]; then echo "case version-source: FAIL ($fails)"; exit 1; fi
echo "case version-source: PASS"
