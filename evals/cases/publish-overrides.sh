#!/usr/bin/env bash
# Case publish-overrides: operator-owned per-block template fragments (P5).
#
# Grades (all hermetic — crafted bundles, stub gh, no network):
#   V1  absent-fragment parity: no .review/overrides reads today's bytes
#       (report --output and human post dry-run match the pre-P5 HEAD script).
#   V2  valid default fragments render byte-identically (all four present with
#       default content; report cmp identical; post dry-run exit 0; check-tree
#       passes validation and fails only on the missing git repo).
#   E1  custom attribution fragment renders substituted (exit 0).
#   E2  custom footer-order reorders/drops receipt fields (exact segments).
#   E3  custom threshold shows the overview below the defaults (files=0).
#   E4  custom preamble rewords the clean-review heading (placeholder fed).
#   E5  full v1 inventory substitutes in one line (exact match).
#   L1  marker in a fragment refuses naming marker-first-line (check + post).
#   L2  ### in a fragment refuses naming findings-structure (check + post).
#   L3  bad threshold refuses naming degenerate-guard (check + post).
#   L4  metadata comment in a fragment refuses naming metadata-presence.
#   L5  divider in a fragment refuses naming divider-shape (check + post).
#   U1  unknown placeholder refuses typed bad-settings (check + post).
#   F1  unknown footer field refuses naming footer-order-subset (check + post).
#   P1  precedence: --attribution-text flag wins over the attribution fragment.
#   M1  the manual carries paths, locked/open lists, inventory, fail-closed.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true
unset REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL || true
unset REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_MODEL || true

VER="$(jq -r .version "$EVAL_ROOT/package.json")"

OV="$EVAL_SANDBOX/.review/overrides"
ov_rm() { rm -rf "$OV"; }

mk_full_bundle() { # $1 = pr; 4 files, priced spend, posted link, one open finding
  local pr="$1" dir=""
  dir="$EVAL_SANDBOX/.review/$pr"
  mkdir -p "$dir"
  cat > "$dir/PR.json" <<'EOF'
{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"},"files":[{"path":"a.txt","additions":10,"deletions":2},{"path":"b.txt","additions":3,"deletions":0},{"path":"c.txt","additions":0,"deletions":5},{"path":"d.txt","additions":1,"deletions":1}]}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 top
+line
EOF
  printf '[{"kind":"inline","path":"a.txt","side":"RIGHT","line":2,"anchored":true,"body":"Open probe.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","sig":"1111111111111111111111111111111111111111","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]' > "$dir/findings.json"
  printf '{"schema_version":"1","effort":"standard","est_usd":0.05}' > "$dir/cost.json"
  jq -n --arg h "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
    '{schema_version:"1",review:{id:101,html_url:"https://example.invalid/r/101"},head:$h,posted_at:"2026-01-01T00:00:00Z",confidence:4,security_summary:"None."}' \
    > "$dir/post.json"
  printf '{"schema_version":"1","body":"> ✅ Looks fine.","approved":false,"comments":[]}' > "$dir/review.json"
}

mk_clean_bundle() { # $1 = pr; same stores but no open findings (preamble renders)
  mk_full_bundle "$1"
  printf '[]' > "$EVAL_SANDBOX/.review/$1/findings.json"
}

check_refuse() { # $1 = label, $2 = want code, $3 = want invariant
  run_review check-tree --json
  if [ "$EVAL_STATUS" -ne 0 ] \
    && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "$2" ] \
    && grep -Fq "$3" "$EVAL_OUT"; then
    good "check-tree refuses $1 ($2, $3)"
  else
    bad "check-tree $1 gave exit $EVAL_STATUS"
  fi
}

post_refuse() { # $1 = label, $2 = pr, $3 = want code, $4 = want invariant
  run_review post acme/app "$2" --dry-run --json
  if [ "$EVAL_STATUS" -ne 0 ] \
    && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "$3" ] \
    && grep -Fq "$4" "$EVAL_OUT"; then
    good "post refuses $1 ($3, $4)"
  else
    bad "post $1 gave exit $EVAL_STATUS"
  fi
}

# --- fixtures ---
mk_full_bundle 4801
mk_clean_bundle 4802

# --- V1: absent-fragment parity vs pre-P5 HEAD (report bytes + dry-run stdout) ---
mkdir -p "$EVAL_SANDBOX/xdg-empty/verdict" "$EVAL_SANDBOX/oldrun/scripts"
export XDG_CONFIG_HOME="$EVAL_SANDBOX/xdg-empty"
git -C "$EVAL_ROOT" show HEAD:scripts/review.sh > "$EVAL_SANDBOX/oldrun/scripts/review.sh"
chmod +x "$EVAL_SANDBOX/oldrun/scripts/review.sh"
cp "$EVAL_ROOT/package.json" "$EVAL_SANDBOX/oldrun/package.json"
run_review report acme/app 4802 --output "$EVAL_SANDBOX/out-new.md"
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "parity baseline exits $EVAL_STATUS"
else
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_SANDBOX/oldrun/scripts/review.sh" \
      report acme/app 4802 --output "$EVAL_SANDBOX/out-old.md" \
      >"$EVAL_SANDBOX/old-out.txt" 2>"$EVAL_SANDBOX/old-err.txt"
  )
  if cmp -s "$EVAL_SANDBOX/out-old.md" "$EVAL_SANDBOX/out-new.md"; then
    good "no overrides: report --output byte-identical to pre-P5 HEAD"
  else
    bad "report parity broke (old vs new differ)"
  fi
fi
run_review post acme/app 4802 --dry-run
if [ "$EVAL_STATUS" -ne 0 ]; then
  bad "dry-run baseline exits $EVAL_STATUS"
else
  cp "$EVAL_OUT" "$EVAL_SANDBOX/new-dry.txt"
  (
    cd "$EVAL_SANDBOX" || exit 99
    PATH="$EVAL_STUBBIN:$PATH" bash "$EVAL_SANDBOX/oldrun/scripts/review.sh" \
      post acme/app 4802 --dry-run >"$EVAL_SANDBOX/old-dry.txt" 2>/dev/null
  )
  if cmp -s "$EVAL_SANDBOX/old-dry.txt" "$EVAL_SANDBOX/new-dry.txt"; then
    good "no overrides: human dry-run byte-identical to pre-P5 HEAD"
  else
    bad "dry-run parity broke"
  fi
fi
cp "$EVAL_SANDBOX/out-new.md" "$EVAL_SANDBOX/out-base.md"
unset XDG_CONFIG_HOME || true

# --- V2: valid default fragments render byte-identically ---
mkdir -p "$OV"
printf '## What we checked\n' > "$OV/preamble.md"
printf 'tier\ncost\nfiles\nlink\n' > "$OV/footer-order.md"
printf 'files: 8\nbytes: 24576\n' > "$OV/walkthrough-threshold.md"
printf 'Reviewed with [**Verdict {version}**]({product_url})\n' > "$OV/attribution.md"
run_review report acme/app 4802 --output "$EVAL_SANDBOX/out-frag.md"
if [ "$EVAL_STATUS" -eq 0 ] && cmp -s "$EVAL_SANDBOX/out-base.md" "$EVAL_SANDBOX/out-frag.md"; then
  good "default fragments render byte-identically to built-ins"
else
  bad "default-fragment parity gave exit $EVAL_STATUS"
fi
run_review post acme/app 4802 --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "valid fragments pass post-time revalidation"
else
  bad "valid post gave exit $EVAL_STATUS"
fi
run_review check-tree --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "valid fragments pass authoring-time validation (clean tree)"
elif [ -z "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" ] \
  && grep -q "check-tree:" "$EVAL_ERR"; then
  good "valid fragments pass authoring-time validation (only the tree gate trips)"
else
  bad "valid check-tree gave exit $EVAL_STATUS"
fi

# --- E1: custom attribution fragment renders substituted ---
printf 'House style {version} on {tier}\n' > "$OV/attribution.md"
run_review report acme/app 4801 --output "$EVAL_SANDBOX/out-e1.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "House style $VER on standard" "$EVAL_SANDBOX/out-e1.md"; then
  good "custom attribution fragment renders substituted"
else
  bad "attribution effect gave exit $EVAL_STATUS"
fi

# --- E2: custom footer-order reorders/drops receipt fields ---
printf 'files\ncost\ntier\n' > "$OV/footer-order.md"
printf 'Reviewed with [**Verdict {version}**]({product_url})\n' > "$OV/attribution.md"
run_review report acme/app 4801 --output "$EVAL_SANDBOX/out-e2.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '4 files · cost $0.05 · standard tier' "$EVAL_SANDBOX/out-e2.md" \
  && ! grep -Fq '[review' "$EVAL_SANDBOX/out-e2.md"; then
  good "custom footer-order reorders and drops the link field"
else
  bad "footer-order effect gave exit $EVAL_STATUS"
fi

# --- E3: custom threshold shows the overview below the defaults ---
ov_rm; mkdir -p "$OV"
printf 'files: 0\nbytes: 0\n' > "$OV/walkthrough-threshold.md"
run_review report acme/app 4801 --output "$EVAL_SANDBOX/out-e3.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '<summary>Files changed (4)</summary>' "$EVAL_SANDBOX/out-e3.md"; then
  good "threshold files=0 shows the overview on a small bundle"
else
  bad "threshold effect gave exit $EVAL_STATUS"
fi
ov_rm

# --- E4: custom preamble rewords the clean-review heading ---
mkdir -p "$OV"
printf '## Walkthrough ({files_reviewed})\n' > "$OV/preamble.md"
run_review report acme/app 4802 --output "$EVAL_SANDBOX/out-e4.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq '## Walkthrough (4 files)' "$EVAL_SANDBOX/out-e4.md" \
  && ! grep -Fq '## What we checked' "$EVAL_SANDBOX/out-e4.md"; then
  good "custom preamble rewords the heading with substitution"
else
  bad "preamble effect gave exit $EVAL_STATUS"
fi

# --- E5: full v1 inventory substitutes in one line ---
printf '## Walk ({version}|{tier}|{product_url}|{files_reviewed}|{head_sha_short}|{model}|{cost})\n' > "$OV/preamble.md"
run_review report acme/app 4802 --output "$EVAL_SANDBOX/out-e5.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "## Walk ($VER|standard|https://stainless-code.com/verdict|4 files|aaaaaaa|unrecorded|\$0.05)" "$EVAL_SANDBOX/out-e5.md"; then
  good "v1 inventory substitutes every placeholder"
else
  bad "inventory effect gave exit $EVAL_STATUS"
fi
printf '## Uses {attribution}\n' > "$OV/preamble.md"
run_review report acme/app 4802 --output "$EVAL_SANDBOX/out-e5b.md"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "## Uses Reviewed with [**Verdict $VER**](https://stainless-code.com/verdict)" "$EVAL_SANDBOX/out-e5b.md"; then
  good "{attribution} resolves to the default sentence"
else
  bad "attribution placeholder gave exit $EVAL_STATUS"
fi
ov_rm

# --- L1: marker in a fragment refuses ---
mkdir -p "$OV"
printf '> %s Stale marker\n' "$(printf '\342\234\205')" > "$OV/preamble.md"
check_refuse "marker fragment" "bad-override" "marker-first-line"
post_refuse "marker fragment" 4801 "bad-override" "marker-first-line"
ov_rm

# --- L2: ### in a fragment refuses ---
mkdir -p "$OV"
printf '### Custom section\n' > "$OV/preamble.md"
check_refuse "findings fragment" "bad-override" "findings-structure"
post_refuse "findings fragment" 4801 "bad-override" "findings-structure"
ov_rm

# --- L3: bad threshold refuses ---
mkdir -p "$OV"
printf 'files: -1\n' > "$OV/walkthrough-threshold.md"
check_refuse "bad threshold" "bad-override" "degenerate-guard"
post_refuse "bad threshold" 4801 "bad-override" "degenerate-guard"
printf 'files: many\nbytes: 24576\n' > "$OV/walkthrough-threshold.md"
check_refuse "non-numeric threshold" "bad-override" "degenerate-guard"
post_refuse "non-numeric threshold" 4801 "bad-override" "degenerate-guard"
ov_rm

# --- L4: metadata comment in a fragment refuses ---
mkdir -p "$OV"
printf '<!-- report-machine:start -->\n' > "$OV/preamble.md"
check_refuse "metadata fragment" "bad-override" "metadata-presence"
post_refuse "metadata fragment" 4801 "bad-override" "metadata-presence"
ov_rm

# --- L5: divider in a fragment refuses ---
mkdir -p "$OV"
printf 'note <!-- verdict-footer --> tail\n' > "$OV/attribution.md"
check_refuse "divider fragment" "bad-override" "divider-shape"
post_refuse "divider fragment" 4801 "bad-override" "divider-shape"
ov_rm

# --- U1: unknown placeholder refuses typed bad-settings ---
mkdir -p "$OV"
printf 'Hello {bogus}\n' > "$OV/attribution.md"
check_refuse "unknown placeholder" "bad-settings" "{bogus}"
post_refuse "unknown placeholder" 4801 "bad-settings" "{bogus}"
ov_rm

# --- F1: unknown footer field refuses ---
mkdir -p "$OV"
printf 'tier\nbogus_field\n' > "$OV/footer-order.md"
check_refuse "unknown footer field" "bad-override" "footer-order-subset"
post_refuse "unknown footer field" 4801 "bad-override" "footer-order-subset"
printf 'tier\ntier\n' > "$OV/footer-order.md"
check_refuse "duplicate footer field" "bad-override" "footer-order-subset"
ov_rm

# --- P1: --attribution-text flag wins over the fragment ---
mkdir -p "$OV"
printf 'Fragment loses {version}\n' > "$OV/attribution.md"
run_review report acme/app 4801 --output "$EVAL_SANDBOX/out-p1.md" --attribution-text "Flag wins {version}"
if [ "$EVAL_STATUS" -eq 0 ] \
  && grep -Fq "Flag wins $VER" "$EVAL_SANDBOX/out-p1.md" \
  && ! grep -Fq 'Fragment loses' "$EVAL_SANDBOX/out-p1.md"; then
  good "flag --attribution-text wins over the fragment"
else
  bad "flag-over-fragment gave exit $EVAL_STATUS"
fi
ov_rm

# --- L3b: 20-digit threshold refuses (64-bit wrap guard) ---
mkdir -p "$OV"
printf 'files: 99999999999999999999\n' > "$OV/walkthrough-threshold.md"
check_refuse "huge threshold" "bad-override" "degenerate-guard"
post_refuse "huge threshold" 4801 "bad-override" "degenerate-guard"
ov_rm

# --- L6: innocent prose mentioning a machine comment passes (anchored scan) ---
mkdir -p "$OV"
printf 'We follow the report-machine comment convention documented in the manual.\n' > "$OV/preamble.md"
run_review post acme/app 4801 --dry-run --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "prose mentioning report-machine without markup passes"
else
  bad "anchored scan over-refused prose (exit $EVAL_STATUS)"
fi
ov_rm

# --- C1: check-tree --json envelopes on native paths (temp repo, removed after) ---
# NOTE: run_review truncates $EVAL_OUT/$EVAL_ERR inside the cwd before the CLI
# runs, so a sandbox repo always sees its own capture files as dirt. The clean
# path is therefore exercised via --expect (only-expected-dirt, exit 0); the
# not-a-repo path is exercised before git init (guarded: another case may own
# a repo first one day, and then this leg must be revisited, not silenced).
if [ ! -d "$EVAL_SANDBOX/.git" ]; then
  run_review check-tree --json
  if [ "$EVAL_STATUS" -eq 1 ] \
    && [ "$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)" = "not-a-repo" ]; then
    good "check-tree --json outside a repo emits the typed envelope"
  else
    bad "not-a-repo gave exit $EVAL_STATUS"
  fi
fi
git -C "$EVAL_SANDBOX" init -q
git -C "$EVAL_SANDBOX" add -A 2>/dev/null
git -C "$EVAL_SANDBOX" -c user.email=t@t -c user.name=t commit -qm probe 2>/dev/null
run_review check-tree --json --expect eval-
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.data.expected_only // empty' <"$EVAL_OUT" 2>/dev/null)" = "true" ]; then
  good "check-tree --json with only expected dirt exits 0 with envelope"
else
  bad "expected-only gave exit $EVAL_STATUS"
fi
touch "$EVAL_SANDBOX/ct-probe.txt"
run_review check-tree --json
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.data.clean' <"$EVAL_OUT" 2>/dev/null)" = "false" ] \
  && [ "$(jq -r '.data.files | length' <"$EVAL_OUT" 2>/dev/null)" != "0" ]; then
  good "check-tree --json dirty emits files and exit 1"
else
  bad "check-tree dirty envelope gave exit $EVAL_STATUS :: OUT $(head -c 600 "$EVAL_OUT") :: ERR $(head -c 300 "$EVAL_ERR")"
fi
run_review check-tree --json --expect zzz-no-match
if [ "$EVAL_STATUS" -eq 1 ] \
  && [ "$(jq -r '.data.unexpected | length' <"$EVAL_OUT" 2>/dev/null)" != "0" ]; then
  good "check-tree --json unexpected-dirt emits the unexpected set"
else
  bad "check-tree unexpected envelope gave exit $EVAL_STATUS"
fi
rm -rf "$EVAL_SANDBOX/.git" "$EVAL_SANDBOX/ct-probe.txt"

# --- M1: manual carries paths, lists, inventory, fail-closed behavior ---
if grep -Fq '.review/overrides/' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'preamble.md' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'footer-order.md' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'walkthrough-threshold.md' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'attribution.md' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'marker-first-line' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'findings-structure' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'degenerate-guard' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'metadata-presence' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'divider-shape' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'footer-order-subset' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq '{files_reviewed}' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq '{head_sha_short}' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq '{model}' "$EVAL_ROOT/docs/manual.md" \
  && grep -Fq 'refuses loudly' "$EVAL_ROOT/docs/manual.md"; then
  good "manual carries paths, locked/open lists, inventory, fail-closed behavior"
else
  bad "overrides paragraph missing from docs/manual.md"
fi

# --- cleanup: no knob or fragment state leaks into later cases ---
unset XDG_CONFIG_HOME || true
unset REVIEW_PUBLISH_FOOTER REVIEW_PUBLISH_ATTRIBUTION || true
unset REVIEW_PUBLISH_ATTRIBUTION_TEXT REVIEW_PUBLISH_COPYRIGHT || true
unset REVIEW_PUBLISH_LOGO REVIEW_PUBLISH_LOGO_URL || true
unset REVIEW_PUBLISH_OVERVIEW || true
unset REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL REVIEW_PUBLISH_DIAGRAMS_EXPANDED || true
unset REVIEW_MODEL || true
rm -rf "$EVAL_SANDBOX/xdg-empty" "$EVAL_SANDBOX/oldrun" "$EVAL_SANDBOX/.review/overrides"

if [ "$fails" -ne 0 ]; then echo "case publish-overrides: FAIL ($fails)"; exit 1; fi
echo "case publish-overrides: PASS"
