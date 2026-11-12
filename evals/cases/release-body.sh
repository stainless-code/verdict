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

cd "$EVAL_ROOT"
VERSION="$(node -p "require('./package.json').version")"
TAG="$(node -p "require('./release/contract.json').tagPrefix")$VERSION"
REPO="$(node -p "require('./release/contract.json').repo")"
NOTES="$EVAL_SANDBOX/expected-notes.md"
ASSETS="$EVAL_SANDBOX/release-assets.txt"
BODY="$EVAL_SANDBOX/release-body.txt"
node scripts/release-notes.mjs "$VERSION" > "$NOTES"
# The expected asset set comes from the contract, not from release.sh: a
# contract edit that the publisher's own list does not follow must fail here.
node - <<'EOF' > "$EVAL_SANDBOX/release-assets.txt"
const c = require("./release/contract.json");
const v = require("./package.json").version;
for (const entry of c.archives) {
  const file = entry.file.replaceAll("{version}", v);
  console.log(file);
  console.log(file + c.sbomSuffix);
}
for (const name of [c.checksums, c.signatureBundle, c.commit, "install.sh", "install.ps1"]) console.log(name);
EOF
: > "$BODY"

# The stub behaves like gh for the two calls the stage makes. It runs the real
# --jq filter over a JSON document, so a broken filter fails instead of
# silently returning the body, and it refuses an edit aimed at another tag or
# repository, so a wrong-target publish cannot pass.
cat > "$EVAL_STUBBIN/gh" <<EOF
#!/usr/bin/env bash
echo "stub-gh \$*" >> "\${EVAL_STUBLOG:-/dev/null}"
sandbox="\${EVAL_SANDBOX:-/tmp}"
expect_tag='$TAG'
expect_repo='$REPO'
guard="\$sandbox/release-absent"
case "\$*" in
  api\ repos/*/releases/tags/*)
    if [ -f "\$guard" ]; then
      echo "gh: Not Found (HTTP 404)" >&2
      exit 1
    fi
    filter=""
    prev=""
    for arg in "\$@"; do
      if [ "\$prev" = "--jq" ]; then filter="\$arg"; fi
      prev="\$arg"
    done
    if [ -z "\$filter" ]; then
      echo "stub-gh: api call without --jq" >&2
      exit 1
    fi
    case "\$*" in
      *assets*)
        jq -n --rawfile list "\$sandbox/release-assets.txt" \\
          '{assets: (\$list | split("\n") | map(select(length > 0)) | map({name: .}))}' \\
          | jq -r "\$filter"
        ;;
      *)
        jq -n --rawfile body "\$sandbox/release-body.txt" '{body: \$body}' | jq -r "\$filter"
        ;;
    esac
    exit 0;;
  "release edit "*)
    if [ "\$3" != "\$expect_tag" ]; then
      echo "stub-gh: edit aimed at '\$3', want '\$expect_tag'" >&2
      exit 1
    fi
    repo=""
    prev=""
    for arg in "\$@"; do
      if [ "\$prev" = "--repo" ]; then repo="\$arg"; fi
      prev="\$arg"
    done
    if [ "\$repo" != "\$expect_repo" ]; then
      echo "stub-gh: edit aimed at repo '\$repo', want '\$expect_repo'" >&2
      exit 1
    fi
    prev=""
    for arg in "\$@"; do
      if [ "\$prev" = "--notes-file" ] && [ ! -f "\$sandbox/release-ignore-edit" ]; then
        cp "\$arg" "\$sandbox/release-body.txt"
      fi
      prev="\$arg"
    done
    echo "https://example.invalid/releases/tag/pinned"
    exit 0;;
esac
echo "evals-stub-gh: network disabled (\$*)" >&2
exit 1
EOF
chmod +x "$EVAL_STUBBIN/gh"

edits() { grep -c "^stub-gh release edit" "$EVAL_STUBLOG" 2>/dev/null || true; }
run_body() { # runs the body stage against the stub; leaves status + streams
  set +e
  PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_ROOT/scripts/release.sh" body \
    >"$EVAL_OUT" 2>"$EVAL_ERR"
  EVAL_STATUS=$?
  set -e
}
clear_markers() { rm -f "$EVAL_SANDBOX/release-absent" "$EVAL_SANDBOX/release-ignore-edit"; }

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

# Leg 3 — GitHub hands the body back with CRLF endings and trailing blanks:
# the text is right, so the stage must accept it instead of rewriting or
# failing on whitespace it never wrote.
clear_markers
sed -e 's/$/\r/' "$NOTES" | sed -e 's/$/  /' > "$BODY"
before="$(edits)"
run_body
assert_exit "a whitespace-only body difference is accepted" 0
assert_eq "no edit is issued for a whitespace-only difference" "$before" "$(edits)"

# Leg 4 — an edit that does not stick must fail the release, not report
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

# Leg 5 — no release at all: refuse instead of silently passing.
clear_markers
touch "$EVAL_SANDBOX/release-absent"
run_body
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "a missing release fails the body stage (exit $EVAL_STATUS)"
else
  bad "a missing release passed the body stage"
fi
if matches -F "not complete" "$EVAL_ERR"; then
  good "the failure names the incomplete release"
else
  bad "the failure does not name the incomplete release: $(cat "$EVAL_ERR")"
fi

# Leg 6 — a release that is missing assets must not get a body: a body on an
# incomplete release makes a broken run look shipped.
clear_markers
printf '%s\n' "$(head -1 "$ASSETS")" > "$ASSETS.partial"
cp "$ASSETS" "$ASSETS.complete"
cp "$ASSETS.partial" "$ASSETS"
: > "$BODY"
run_body
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "an incomplete release refuses a body (exit $EVAL_STATUS)"
else
  bad "an incomplete release accepted a body"
fi
if matches -F "missing:" "$EVAL_ERR"; then
  good "the failure names the missing assets"
else
  bad "the failure does not name the missing assets: $(cat "$EVAL_ERR")"
fi
cp "$ASSETS.complete" "$ASSETS"
clear_markers

# Leg 7 — a present asset stays present when the list is larger than the pipe
# buffer. The old membership test piped the list into `grep -q`: grep exits at
# the first match, the writer takes SIGPIPE, and pipefail reported a shipped
# asset as missing (STA-378). The match sits first and the filler pushes the
# list past 64 KiB, so the old form loses every asset on every run, not only
# under load.
cp "$ASSETS" "$EVAL_SANDBOX/release-assets.big"
i=0
while [ "$i" -lt 8000 ]; do
  printf 'filler-%05d\n' "$i"
  i=$((i + 1))
done >> "$EVAL_SANDBOX/release-assets.big"
mv "$EVAL_SANDBOX/release-assets.big" "$ASSETS"
if [ "$(wc -c < "$ASSETS")" -le 65536 ]; then
  bail "the large asset list is not over 64 KiB; this leg would prove nothing"
fi
clear_markers
: > "$BODY"
before="$(edits)"
run_body
assert_exit "a present asset in a list over 64 KiB is found" 0
assert_eq "the large-list run repairs the body exactly once" "$((before + 1))" "$(edits)"

eval_end
