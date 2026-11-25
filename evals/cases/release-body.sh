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

# Leg 8 — a changelog section over GitHub's 125,000-character body limit must
# stop the tag stage before any git call: the tag is pushed before the release
# is created, so a body GitHub refuses would strand a tag with no release. The
# copy root holds only what the stage reads; the changeset and git stubs log
# every call, so a stage that tags first is caught by the log, not by a crash.
BIG="$EVAL_SANDBOX/oversized-release"
mkdir -p "$BIG/scripts" "$BIG/release" "$BIG/bin" "$BIG/node_modules/.bin"
cp "$EVAL_ROOT/scripts/release.sh" "$EVAL_ROOT/scripts/release-notes.mjs" "$BIG/scripts/"
cp "$EVAL_ROOT/release/contract.json" "$BIG/release/"
printf '{"version": "9.9.9"}\n' > "$BIG/package.json"
line="$(printf '%0100d' 0)"
{
  printf '# changelog\n\n## 9.9.9\n\n'
  i=0
  while [ "$i" -lt 1300 ]; do
    printf -- '- %s\n' "$line"
    i=$((i + 1))
  done
  printf '\n## 9.9.8\n\n- older\n'
} > "$BIG/CHANGELOG.md"
cat > "$BIG/bin/git" <<EOF
#!/usr/bin/env bash
echo "git \$*" >> "$BIG/git.log"
exit 1
EOF
cat > "$BIG/node_modules/.bin/changeset" <<EOF
#!/usr/bin/env bash
echo "changeset \$*" >> "$BIG/git.log"
EOF
chmod +x "$BIG/bin/git" "$BIG/node_modules/.bin/changeset"
# A seeded log lets the absence check below prove the omission: a log that was
# never written cannot pass as "no call".
printf 'call log\n' > "$BIG/git.log"
set +e
PATH="$BIG/bin:$EVAL_STUBBIN:$PATH" bash "$BIG/scripts/release.sh" tag >"$EVAL_OUT" 2>"$EVAL_ERR"
EVAL_STATUS=$?
set -e
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "an oversized release body fails the tag stage (exit $EVAL_STATUS)"
else
  bad "an oversized release body passed the tag stage"
fi
if matches -F "GitHub refuses a release body over 125000" "$EVAL_ERR"; then
  good "the failure names the body limit"
else
  bad "the failure does not name the body limit: $(cat "$EVAL_ERR")"
fi
assert_file_nogrep "the tag stage made no changeset or git call" "$BIG/git.log" '^(git|changeset) ' -E

# Legs 9–12 run scripts/release-notes.mjs in a copy root, so the highlights
# file and the changelog are fixtures this case owns, not the repository's.
COMPOSE="$EVAL_SANDBOX/compose-release"
mkdir -p "$COMPOSE/scripts" "$COMPOSE/release" "$COMPOSE/docs/releases"
cp "$EVAL_ROOT/scripts/release-notes.mjs" "$COMPOSE/scripts/"
cp "$EVAL_ROOT/release/contract.json" "$COMPOSE/release/"
# Three changesets: an indented continuation and a nested bullet must not count
# as changes, and the commit ids must not reach the body.
cat > "$COMPOSE/CHANGELOG.md" <<'EOF'
# changelog

## 9.9.9

### Minor Changes

- 1a2b3c4: **Alpha.** First change.

  - a nested bullet

### Patch Changes

- 5d6e7f8: Beta fix.
- 9a8b7c6: Gamma fix.

## 9.9.8

- older
EOF
render() { # runs release-notes.mjs in the copy root; leaves status + streams
  set +e
  node "$COMPOSE/scripts/release-notes.mjs" 9.9.9 >"$EVAL_OUT" 2>"$EVAL_ERR"
  EVAL_STATUS=$?
  set -e
}

# Leg 9 — a highlights file leads the body: header with the computed change
# count, the file verbatim, the remainder line, then the whole section folded
# into <details>. The expectation is a literal: it is what a reader sees.
cat > "$COMPOSE/docs/releases/9.9.9.md" <<'EOF'
- **Alpha.** What it does for you.

### Breaking changes

- **Alpha moved.** Do this instead.
EOF
cat > "$EVAL_SANDBOX/composed-want.md" <<'EOF'
**v9.9.9** · 3 changes

- **Alpha.** What it does for you.

### Breaking changes

- **Alpha moved.** Do this instead.

### Plus 2 more fixes and improvements.

<details>
<summary>Full changelog</summary>

### Minor Changes

- **Alpha.** First change.

  - a nested bullet

### Patch Changes

- Beta fix.
- Gamma fix.

</details>
EOF
render
assert_exit "a highlights file composes the body" 0
assert_file_eq "the composed body is header, highlights, breaking, remainder, then the folded changelog" \
  "$EVAL_SANDBOX/composed-want.md" "$EVAL_OUT"
if [ -s "$EVAL_ERR" ]; then
  bad "a composed render wrote to stderr: $(cat "$EVAL_ERR")"
else
  good "a composed render writes nothing to stderr"
fi

# Leg 10 — no highlights file: the body is the section alone, as before, and
# stderr warns so a release cut without highlights is visible in the log.
rm "$COMPOSE/docs/releases/9.9.9.md"
cat > "$EVAL_SANDBOX/fallback-want.md" <<'EOF'
### Minor Changes

- **Alpha.** First change.

  - a nested bullet

### Patch Changes

- Beta fix.
- Gamma fix.
EOF
render
assert_exit "a missing highlights file falls back" 0
assert_file_eq "the fallback body is the changelog section alone" "$EVAL_SANDBOX/fallback-want.md" "$EVAL_OUT"
if matches -F "warning: no docs/releases/9.9.9.md" "$EVAL_ERR"; then
  good "the fallback warns naming the missing highlights file"
else
  bad "the fallback did not warn: $(cat "$EVAL_ERR")"
fi

# Leg 11 — a highlights file with no bullet before its first heading is an
# authoring slip; publishing it would claim highlights the body lacks.
printf '### Breaking changes\n\n- **Alpha moved.** Do this instead.\n' > "$COMPOSE/docs/releases/9.9.9.md"
render
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "a highlights file with no highlight bullets refuses (exit $EVAL_STATUS)"
else
  bad "a highlights file with no highlight bullets rendered"
fi
if matches -F "has no highlight bullets" "$EVAL_ERR"; then
  good "the refusal names the missing highlight bullets"
else
  bad "the refusal does not name the missing bullets: $(cat "$EVAL_ERR")"
fi

# Leg 12 — the size guard measures the composed body, not the section: a
# section that fits alone plus highlights that push it over must refuse.
rm "$COMPOSE/docs/releases/9.9.9.md"
line="$(printf '%0100d' 0)"
{
  printf '# changelog\n\n## 9.9.9\n\n'
  i=0
  while [ "$i" -lt 1200 ]; do
    printf -- '- %s\n' "$line"
    i=$((i + 1))
  done
} > "$COMPOSE/CHANGELOG.md"
render
if [ "$EVAL_STATUS" -ne 0 ]; then
  bail "the 1,200-entry section alone does not fit; this leg would prove nothing"
fi
{
  printf -- '- **Big.** %s\n' "$(printf '%08000d' 0)"
  printf '\n### Breaking changes\n\n- **Huge.** %s\n' "$(printf '%05000d' 0)"
} > "$COMPOSE/docs/releases/9.9.9.md"
render
if [ "$EVAL_STATUS" -ne 0 ]; then
  good "a composed body over the limit refuses (exit $EVAL_STATUS)"
else
  bad "a composed body over the limit rendered ($(wc -c < "$EVAL_OUT") bytes)"
fi
if matches -F "GitHub refuses a release body over 125000" "$EVAL_ERR"; then
  good "the composed-body failure names the body limit"
else
  bad "the composed-body failure does not name the body limit: $(cat "$EVAL_ERR")"
fi

eval_end
