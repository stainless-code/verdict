#!/usr/bin/env bash
#
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_OVERVIEW || true

SHA_A="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
SHA_B="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

mk_overview_fixture() {
  local pr="$1"
  local dir="$EVAL_SANDBOX/.verdict/state/$pr"
  mkdir -p "$dir"
  cat > "$dir/PR.json" <<EOF
{"state":"OPEN","headRefOid":"$SHA_A","baseRefOid":"$SHA_B","author":{"login":"teammate"},"files":[{"path":"src/app.ts","additions":10,"deletions":2},{"path":"src/theme.ts","additions":18,"deletions":9},{"path":"test/app.test.ts","additions":5,"deletions":0},{"path":"schema.json","additions":27,"deletions":52}]}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/src/app.ts b/src/app.ts
index 1111111..2222222 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,3 +1,4 @@
 top
+import { theme } from "./theme.ts";
 old
+new line
@@ -10,2 +11,3 @@
 ctx
-old line
+replacement
diff --git a/src/theme.ts b/src/theme.ts
index 1111111..2222222 100644
--- a/src/theme.ts
+++ b/src/theme.ts
@@ -1,2 +1,3 @@
 top
+export const theme = {};
 bottom
diff --git a/test/app.test.ts b/test/app.test.ts
index 1111111..2222222 100644
--- a/test/app.test.ts
+++ b/test/app.test.ts
@@ -1 +1,2 @@
 top
+// validates src/app.ts contract
diff --git a/schema.json b/schema.json
index 1111111..2222222 100644
--- a/schema.json
+++ b/schema.json
@@ -5,3 +5,0 @@
-old a
-old b
-old c
EOF
  cat > "$dir/hunks.tsv" <<'EOF'
src/app.ts	NEW	1	4
src/app.ts	NEW	11	13
src/theme.ts	NEW	1	3
test/app.test.ts	NEW	1	2
schema.json	OLD	5	7
EOF
  cat > "$dir/roles.txt" <<'EOF'
## config
- schema.json

## source
- src/app.ts
- src/theme.ts

## test
- test/app.test.ts
EOF
  printf '[{"kind":"inline","path":"src/app.ts","side":"RIGHT","line":2,"anchored":true,"body":"Off by one.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"cccccccccccccccccccccccccccccccccccccccc","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]' > "$dir/findings.json"
}

extract_overview() { # $1 = report file; prints the two <details> blocks
  awk '/^<details>$/{on=1} on{print} /^<\/details>$/{c++; if(c==2)exit}' "$1"
}

PR=4601
mk_overview_fixture "$PR"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4601.md" --overview
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "forced overview exits 0"
else
  bad "forced overview exits $EVAL_STATUS"
fi
cat > "$EVAL_SANDBOX/want-4601.md" <<'EOF'
<details>
<summary>Files changed (4)</summary>

| # | File | +/- | Hunks | Role |
|---|---|---|---|---|
| F1 | `src/app.ts` | +10/-2 | 2 | source |
| F2 | `src/theme.ts` | +18/-9 | 1 | source |
| F3 | `test/app.test.ts` | +5/-0 | 1 | test |
| F4 | `schema.json` | +27/-52 | 0 | config |

</details>

<details>
<summary>Change topology</summary>

```mermaid
flowchart TD
    PR["PR #4601 +60/-63"]
    PR --> F1["F1 app.ts"]
    PR --> F2["F2 theme.ts"]
    PR --> F3["F3 app.test.ts"]
    PR --> F4["F4 schema.json"]
    F1 -->|"uses theme.ts"| F2
    F3 -->|"validates app.ts"| F1
```

</details>
EOF
extract_overview "$EVAL_SANDBOX/out-4601.md" > "$EVAL_SANDBOX/got-4601.md"
if cmp -s "$EVAL_SANDBOX/got-4601.md" "$EVAL_SANDBOX/want-4601.md"; then
  good "table exact (columns, order, ids, deltas, hunks, roles) and topology exact"
else
  bad "overview block differs: $(diff "$EVAL_SANDBOX/want-4601.md" "$EVAL_SANDBOX/got-4601.md" | head -c 800)"
fi
if [ "$(grep -c 'F4 -->' "$EVAL_SANDBOX/got-4601.md")" = "0" ] \
  && [ "$(grep -c -- '-->|' "$EVAL_SANDBOX/got-4601.md")" = "2" ]; then
  good "no invented relations (unmentioned F4 carries no edges, exactly 2 mention edges)"
else
  bad "relation edges wrong"
fi
if grep -Fq 'verdict-footer' "$EVAL_SANDBOX/out-4601.md" \
  && [ "$(grep -n 'Change topology' "$EVAL_SANDBOX/out-4601.md" | cut -d: -f1)" -lt "$(grep -n 'verdict-footer' "$EVAL_SANDBOX/out-4601.md" | cut -d: -f1)" ]; then
  good "overview sits above the footer inside the content"
else
  bad "overview placement drifted"
fi

mk_sized_bundle() { # $1 = pr, $2 = file count, $3 = pad lines per file
  local pr="$1" n="$2" pad="$3"
  local dir="$EVAL_SANDBOX/.verdict/state/$pr" i
  mkdir -p "$dir"
  {
    printf '{"state":"OPEN","headRefOid":"%s","baseRefOid":"%s","author":{"login":"teammate"},"files":[' "$SHA_A" "$SHA_B"
    i=0
    while [ "$i" -lt "$n" ]; do
      [ "$i" -gt 0 ] && printf ','
      printf '{"path":"f%d.txt","additions":1,"deletions":1}' "$i"
      i=$((i + 1))
    done
    printf ']}\n'
  } > "$dir/PR.json"
  {
    i=0
    while [ "$i" -lt "$n" ]; do
      printf 'diff --git a/f%d.txt b/f%d.txt\n--- a/f%d.txt\n+++ b/f%d.txt\n@@ -1 +1 @@\n-old\n+new\n' "$i" "$i" "$i" "$i"
      seq 1 "$pad" 2>/dev/null | sed 's/^/+pad line /' || true
      i=$((i + 1))
    done
  } > "$dir/full.diff"
  echo '[]' > "$dir/findings.json"
}
mk_sized_bundle 4603 7 0
run_review report acme/app 4603 --output "$EVAL_SANDBOX/out-4603.md"
assert_exit "7-file small bundle renders" 0
assert_file_nogrep "7-file small bundle omits the overview" "$EVAL_SANDBOX/out-4603.md" "Files changed"
mk_sized_bundle 4604 9 0
run_review report acme/app 4604 --output "$EVAL_SANDBOX/out-4604.md"
if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq '<summary>Files changed (9)</summary>' "$EVAL_SANDBOX/out-4604.md"; then
  good "9-file bundle shows the overview via the file-count trigger"
else
  bad "threshold file side gave exit $EVAL_STATUS"
fi
mk_sized_bundle 4605 2 1600
if [ "$(wc -c <"$EVAL_SANDBOX/.verdict/state/4605/full.diff" | tr -d ' ')" -gt 24576 ]; then
  good "byte fixture really exceeds 24KB"
else
  bad "byte fixture too small"
fi
run_review report acme/app 4605 --output "$EVAL_SANDBOX/out-4605.md"
if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq '<summary>Files changed (2)</summary>' "$EVAL_SANDBOX/out-4605.md"; then
  good "2-file 30KB bundle shows the overview via the byte trigger"
else
  bad "threshold byte side gave exit $EVAL_STATUS"
fi

run_review report acme/app 4603 --output "$EVAL_SANDBOX/out-4606.md" --overview
if [ "$EVAL_STATUS" -eq 0 ] && grep -Fq '<summary>Files changed (7)</summary>' "$EVAL_SANDBOX/out-4606.md"; then
  good "--overview forces show below threshold"
else
  bad "--overview force gave exit $EVAL_STATUS"
fi
run_review report acme/app 4604 --output "$EVAL_SANDBOX/out-4607.md" --no-overview
assert_exit "--no-overview render succeeds" 0
assert_file_nogrep "--no-overview forces hide above threshold" "$EVAL_SANDBOX/out-4607.md" "Files changed"

mkdir -p "$EVAL_SANDBOX/xdg-empty/verdict"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
run_review report acme/app 4601 --output "$EVAL_SANDBOX/out-new.md"
assert_exit "omitted-default render succeeds" 0
assert_file_nogrep "below threshold the report renders with no <details> overview" "$EVAL_SANDBOX/out-new.md" "<details>"
unset XDG_CONFIG_HOME || true

mkdir -p "$EVAL_SANDBOX/xdg-ov/verdict"
printf '{"publish":{"overview":false}}' > "$EVAL_SANDBOX/xdg-ov/verdict/settings.json"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-ov"
run_review report acme/app 4604 --output "$EVAL_SANDBOX/out-4608.md"
assert_exit "file-off render succeeds" 0
assert_file_nogrep "file false hides a large bundle" "$EVAL_SANDBOX/out-4608.md" "Files changed"
run_review report acme/app 4604 --output "$EVAL_SANDBOX/out-4609.md" --overview
if [ "$EVAL_STATUS" -eq 0 ] && grep -q 'Files changed' "$EVAL_SANDBOX/out-4609.md"; then
  good "flag --overview wins over a false file"
else
  bad "flag-over-file gave exit $EVAL_STATUS"
fi
unset XDG_CONFIG_HOME || true
# shellcheck disable=SC2034
REVIEW_PUBLISH_OVERVIEW="false"
export REVIEW_PUBLISH_OVERVIEW
run_review report acme/app 4604 --output "$EVAL_SANDBOX/out-4610.md"
assert_exit "env-off render succeeds" 0
assert_file_nogrep "env false alone (no file) hides a large bundle" "$EVAL_SANDBOX/out-4610.md" "Files changed"
unset REVIEW_PUBLISH_OVERVIEW || true

PR=4611
dir="$EVAL_SANDBOX/.verdict/state/$PR"
mkdir -p "$dir"
cat > "$dir/PR.json" <<'EOF'
{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"},"files":[{"path":"a/theme.ts","additions":3,"deletions":0},{"path":"b/theme.ts","additions":1,"deletions":0},{"path":"src/app.ts","additions":2,"deletions":0},{"path":"c.ts","additions":20,"deletions":20},{"path":"d.ts","additions":20,"deletions":20},{"path":"e.ts","additions":20,"deletions":20},{"path":"f.ts","additions":20,"deletions":20},{"path":"g.ts","additions":20,"deletions":20},{"path":"h.ts","additions":20,"deletions":20}]}
EOF
cat > "$dir/full.diff" <<'EOF'
diff --git a/a/theme.ts b/a/theme.ts
index 1111111..2222222 100644
--- a/a/theme.ts
+++ b/a/theme.ts
@@ -1,2 +1,3 @@
 top
+export const a = 1;
 bottom
diff --git a/b/theme.ts b/b/theme.ts
index 1111111..2222222 100644
--- a/b/theme.ts
+++ b/b/theme.ts
@@ -1,2 +1,3 @@
 top
+export const b = 1;
 bottom
diff --git a/src/app.ts b/src/app.ts
index 1111111..2222222 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,2 +1,4 @@
 top
+// uses b/theme.ts tokens
 bottom
EOF
cat > "$dir/hunks.tsv" <<'EOF'
a/theme.ts	NEW	1	3
b/theme.ts	NEW	1	3
src/app.ts	NEW	1	4
EOF
printf '[{"kind":"inline","path":"src/app.ts","side":"RIGHT","line":2,"anchored":true,"body":"Collision probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"dddddddddddddddddddddddddddddddddddddddd","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]' > "$dir/findings.json"
run_review report acme/app "$PR" --output "$EVAL_SANDBOX/out-4611.md" --overview
assert_exit "collision bundle renders" 0
assert_file_grep "colliding basenames label the edge with the full path" "$EVAL_SANDBOX/out-4611.md" 'F3 -->|"uses b/theme.ts"|' "-F"
assert_file_nogrep "colliding basenames never drop to the bare basename" "$EVAL_SANDBOX/out-4611.md" 'uses theme.ts"|' "-F"

if grep -Fq '24KB (24576 bytes)' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq -- '--overview' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq -- '--no-overview' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'F1..Fn' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'publish.overview' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries threshold numbers, flags, and the row-id reservation note"
else
  bad "overview paragraph missing from docs/manual.md"
fi

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_OVERVIEW || true
rm -rf "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/xdg-ov"

eval_end
