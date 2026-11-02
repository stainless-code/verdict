#!/usr/bin/env bash
# Case dispatch-record: the shell dispatches the record verb to the Go binary.
#
# Grades (all hermetic — crafted local bundles in the temp sandbox,
# network-dead stub gh, binary built from the checkout into the sandbox):
#   G1  default (VERDICT_IMPL unset) and VERDICT_IMPL=bash run the bash
#       implementation byte-identically (the switch changes nothing by default).
#   G2  VERDICT_IMPL=go with a built binary produces byte-identical stdout,
#       stderr, exit code, and store files to bash on representative record
#       fixtures: text mode, --json with global presentation/publish flags,
#       typed refusals (corrupt review, missing review file), the re-review
#       merge path, and the missing-positionals usage edge.
#   G3  go-selected-but-missing-binary refuses typed (exit 1, missing-go-binary
#       envelope under --json, make-build hint) — never a silent bash fallback.
#   G4  unknown VERDICT_IMPL values are a usage error (exit 2) on positional
#       and non-positional verb paths alike.
#   G5  non-record verbs stay bash under VERDICT_IMPL=go (byte-identical).
#   G6  the existing record-exercising eval cases pass unchanged under
#       VERDICT_IMPL=go (the strongest parity proof: the contract suite grades
#       the Go implementation through the shell entry point).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

# Toolchain gate: build hermetically into the sandbox, or skip with a notice
# (ticket 03 installs Go in CI — a missing toolchain is never a failure).
if ! command -v go >/dev/null 2>&1; then
  echo "case dispatch-record: SKIP (no go toolchain on PATH; ticket 03 installs Go in CI)"
  exit 0
fi

GOBIN_DIR="$EVAL_SANDBOX/dispatch-bin"
mkdir -p "$GOBIN_DIR/prompts"
if ! (cd "$EVAL_ROOT" && GOTOOLCHAIN=local GOPROXY=off go build -o "$GOBIN_DIR/verdict" ./cmd/verdict); then
  bad "hermetic go build failed"
  echo "case dispatch-record: FAIL ($fails)"; exit 1
fi
# Provenance inputs live beside the binary (mirrors the internal/record parity
# harness): a sandbox binary has no checkout above it, so plant the files the
# checkout-root fallback resolves beside it.
cp "$EVAL_ROOT/package.json" "$EVAL_ROOT/SKILL.md" "$GOBIN_DIR/"
cp "$EVAL_ROOT/prompts/system.md" "$EVAL_ROOT/prompts/review.md" "$EVAL_ROOT/prompts/diagrams.md" "$GOBIN_DIR/prompts/"
export VERDICT_BIN="$GOBIN_DIR/verdict"
good "built verdict binary hermetically into the sandbox"

# scrub_ts | scrub_slug <slug-b> <slug-g> — normalize timestamps and the two
# parallel slugs so bash/go captures compare byte-exact (R6: scrubbed only
# for timestamps; slugs differ by construction).
scrub() { # $1 = bash slug, $2 = go slug; stdin -> stdout scrubbed
  sed -E -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z/TS/g' -e "s/$1/SLUG/g" -e "s/$2/SLUG/g"
}

# mk_bundle <slug> — stdin review.json becomes a minimal local bundle.
mk_bundle() { # $1 = slug
  local dir="$EVAL_SANDBOX/.review/$1"
  rm -rf "$dir"; mkdir -p "$dir"
  cat > "$dir/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}
EOF
  cat > "$dir/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
  cat > "$dir/review.json"
}

VALID_REVIEW='{"schema_version":"1","body":"> marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"x body with enough substance.","rigor":"pointed"}]}'

# run_impl <impl> <outfile-prefix> -- <review args...>: run with VERDICT_IMPL
# forced (unset for bash, go for go), cwd=sandbox, stub gh. Sets EVAL_STATUS.
run_impl() { # $1 = impl, $2 = prefix, rest after -- = review args
  local impl="$1" prefix="$2"; shift 2
  [ "${1:-}" = "--" ] && shift
  set +e
  (
    cd "$EVAL_SANDBOX" || exit 99
    if [ "$impl" = "go" ]; then
      PATH="$EVAL_STUBBIN:$PATH" VERDICT_IMPL=go VERDICT_BIN="${RUN_BIN:-$GOBIN_DIR/verdict}" \
        bash "$EVAL_ROOT/scripts/review.sh" "$@" >"$prefix.out" 2>"$prefix.err"
    else
      PATH="$EVAL_STUBBIN:$PATH" env -u VERDICT_IMPL \
        bash "$EVAL_ROOT/scripts/review.sh" "$@" >"$prefix.out" 2>"$prefix.err"
    fi
  )
  EVAL_STATUS=$?
  set -e
  return 0
}

# expect_ident <label> <bash-slug> <go-slug> <bash-status> <go-status>
#   <bash-prefix> <go-prefix> — compare one paired run: exit, scrubbed
# stdout/stderr, scrubbed findings/record stores, exact hunks snapshot +
# bundle file list.
expect_ident() { # $1 = label, $2 = bslug, $3 = gslg, $4 = bstatus, $5 = gstatus, $6 = bpre, $7 = gpre
  local label="$1" bslug="$2" gslg="$3" bstatus="$4" gstatus="$5" bpre="$6" gpre="$7"
  if [ "$bstatus" = "$gstatus" ]; then
    good "$label exits match ($bstatus)"
  else
    bad "$label exits differ (bash $bstatus, go $gstatus)"
  fi
  if cmp -s <(scrub "$bslug" "$gslg" <"$bpre.out") <(scrub "$bslug" "$gslg" <"$gpre.out"); then
    good "$label stdout byte-identical"
  else
    bad "$label stdout differs"; diff <(scrub "$bslug" "$gslg" <"$bpre.out") <(scrub "$bslug" "$gslg" <"$gpre.out") | head -10 || true
  fi
  if cmp -s <(scrub "$bslug" "$gslg" <"$bpre.err") <(scrub "$bslug" "$gslg" <"$gpre.err"); then
    good "$label stderr byte-identical"
  else
    bad "$label stderr differs"; diff <(scrub "$bslug" "$gslg" <"$bpre.err") <(scrub "$bslug" "$gslg" <"$gpre.err") | head -10 || true
  fi
  local bf="$EVAL_SANDBOX/.review/$bslug" gf="$EVAL_SANDBOX/.review/$gslg" f
  for f in findings.json record.json; do
    if [ -e "$bf/$f" ] || [ -e "$gf/$f" ]; then
      if [ -e "$bf/$f" ] && [ -e "$gf/$f" ] && cmp -s <(scrub "$bslug" "$gslg" <"$bf/$f") <(scrub "$bslug" "$gslg" <"$gf/$f"); then
        good "$label $f byte-identical"
      else
        bad "$label $f differs (or written on one side only)"
      fi
    fi
  done
  if [ -e "$bf/hunks.tsv" ] || [ -e "$gf/hunks.tsv" ]; then
    if cmp -s "$bf/hunks.tsv" "$gf/hunks.tsv" 2>/dev/null; then
      good "$label hunks.tsv byte-identical"
    else
      bad "$label hunks.tsv differs (or written on one side only)"
    fi
  fi
  if [ "$(cd "$bf" 2>/dev/null && ls | sort | tr '\n' ' ')" = "$(cd "$gf" 2>/dev/null && ls | sort | tr '\n' ' ')" ]; then
    good "$label bundle file lists match"
  else
    bad "$label bundle file lists differ (bash: $(cd "$bf" 2>/dev/null && ls | tr '\n' ' ') vs go: $(cd "$gf" 2>/dev/null && ls | tr '\n' ' '))"
  fi
}

# paired <label> <review-json> -- <extra record args...>: craft identical
# bundles under two slugs, run bash vs go, grade byte-identity.
paired() { # $1 = label, $2 = review json, rest after -- = extra args
  local label="$1" doc="$2"; shift 2
  [ "${1:-}" = "--" ] && shift
  local bslug="local-disp-b" gslg="local-disp-g"
  printf '%s' "$doc" | mk_bundle "$bslug"
  printf '%s' "$doc" | mk_bundle "$gslg"
  run_impl bash "$EVAL_SANDBOX/par-b" -- record acme/app "$bslug" "$@"
  local bstatus="$EVAL_STATUS"
  run_impl go "$EVAL_SANDBOX/par-g" -- record acme/app "$gslg" "$@"
  local gstatus="$EVAL_STATUS"
  expect_ident "$label" "$bslug" "$gslg" "$bstatus" "$gstatus" "$EVAL_SANDBOX/par-b" "$EVAL_SANDBOX/par-g"
  rm -rf "$EVAL_SANDBOX/.review/$bslug" "$EVAL_SANDBOX/.review/$gslg"
}

# --- G1: default stays bash ---
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-b"
run_impl bash "$EVAL_SANDBOX/g1-def" -- record acme/app local-disp-b --json
def_status="$EVAL_STATUS"
rm -rf "$EVAL_SANDBOX/.review/local-disp-b"
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-b"
(
  cd "$EVAL_SANDBOX" || exit 99
  set +e
  PATH="$EVAL_STUBBIN:$PATH" VERDICT_IMPL=bash bash "$EVAL_ROOT/scripts/review.sh" record acme/app local-disp-b --json \
    >"$EVAL_SANDBOX/g1-exp.out" 2>"$EVAL_SANDBOX/g1-exp.err"
  echo "$?" >"$EVAL_SANDBOX/g1-exp.status"
  set -e
)
exp_status="$(cat "$EVAL_SANDBOX/g1-exp.status")"
if [ "$def_status" = "$exp_status" ] \
  && cmp -s <(scrub "local-disp-b" "local-disp-b" <"$EVAL_SANDBOX/g1-def.out") <(scrub "local-disp-b" "local-disp-b" <"$EVAL_SANDBOX/g1-exp.out") \
  && cmp -s "$EVAL_SANDBOX/g1-def.err" "$EVAL_SANDBOX/g1-exp.err"; then
  good "G1 default and VERDICT_IMPL=bash agree byte-exactly"
else
  bad "G1 default vs explicit bash differ (status $def_status vs $exp_status)"
fi
rm -rf "$EVAL_SANDBOX/.review/local-disp-b"

# --- G2: go parity on representative fixtures ---
paired "G2 text-mode record" "$VALID_REVIEW"
paired "G2 json record with global flags" "$VALID_REVIEW" -- --compact --color=never --no-footer --attribution off --json
paired "G2 corrupt review refusal" '{"broken":' -- --json
paired "G2 corrupt review refusal with color" '{"broken":' -- --color=always --json
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-b"
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-g"
rm "$EVAL_SANDBOX/.review/local-disp-b/review.json" "$EVAL_SANDBOX/.review/local-disp-g/review.json"
run_impl bash "$EVAL_SANDBOX/par-b" -- record acme/app local-disp-b --json
bstatus="$EVAL_STATUS"
run_impl go "$EVAL_SANDBOX/par-g" -- record acme/app local-disp-g --json
gstatus="$EVAL_STATUS"
expect_ident "G2 missing review.json refusal" "local-disp-b" "local-disp-g" "$bstatus" "$gstatus" "$EVAL_SANDBOX/par-b" "$EVAL_SANDBOX/par-g"
rm -rf "$EVAL_SANDBOX/.review/local-disp-b" "$EVAL_SANDBOX/.review/local-disp-g"
# Re-review merge path: record twice, compare the second (merging) run.
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-b"
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-g"
run_impl bash "$EVAL_SANDBOX/par-b" -- record acme/app local-disp-b --json >/dev/null 2>&1
run_impl go "$EVAL_SANDBOX/par-g" -- record acme/app local-disp-g --json >/dev/null 2>&1
printf '%s' '{"schema_version":"1","body":"> second pass with a changed body that still passes the guard fine.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"second comment with enough substance here.","rigor":"pointed"}]}' > "$EVAL_SANDBOX/.review/local-disp-b/review.json"
printf '%s' '{"schema_version":"1","body":"> second pass with a changed body that still passes the guard fine.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"second comment with enough substance here.","rigor":"pointed"}]}' > "$EVAL_SANDBOX/.review/local-disp-g/review.json"
run_impl bash "$EVAL_SANDBOX/par-b" -- record acme/app local-disp-b --json
bstatus="$EVAL_STATUS"
run_impl go "$EVAL_SANDBOX/par-g" -- record acme/app local-disp-g --json
gstatus="$EVAL_STATUS"
expect_ident "G2 re-review merge" "local-disp-b" "local-disp-g" "$bstatus" "$gstatus" "$EVAL_SANDBOX/par-b" "$EVAL_SANDBOX/par-g"
rm -rf "$EVAL_SANDBOX/.review/local-disp-b" "$EVAL_SANDBOX/.review/local-disp-g"

# --- G3: go-selected-but-missing-binary refuses typed, never falls back ---
RUN_BIN=/nonexistent-verdict-bin run_impl go "$EVAL_SANDBOX/g3" -- record acme/app local-disp-b --json
if [ "$EVAL_STATUS" -eq 1 ] && [ "$(jq -r '.error.code // empty' "$EVAL_SANDBOX/g3.out" 2>/dev/null)" = "missing-go-binary" ] \
  && grep -q "make build" "$EVAL_SANDBOX/g3.out"; then
  good "G3 missing VERDICT_BIN refuses typed missing-go-binary with build hint"
else
  bad "G3 missing VERDICT_BIN exits $EVAL_STATUS: $(head -c 200 "$EVAL_SANDBOX/g3.err")"
fi
(
  cd "$EVAL_SANDBOX" || exit 99
  set +e
  PATH="$EVAL_STUBBIN:$PATH" VERDICT_IMPL=go VERDICT_BIN=/nonexistent-verdict-bin \
    bash "$EVAL_ROOT/scripts/review.sh" record acme/app local-disp-b >"$EVAL_SANDBOX/g3t.out" 2>"$EVAL_SANDBOX/g3t.err"
  echo "$?" >"$EVAL_SANDBOX/g3t.status"
  set -e
)
if [ "$(cat "$EVAL_SANDBOX/g3t.status")" = "1" ] && grep -q "make build" "$EVAL_SANDBOX/g3t.err" \
  && [ ! -e "$EVAL_SANDBOX/.review/local-disp-b/findings.json" ]; then
  good "G3 text-mode missing binary refuses exit 1 with hint, writes nothing"
else
  bad "G3 text-mode missing binary exits $(cat "$EVAL_SANDBOX/g3t.status")"
fi
if [ ! -e "$EVAL_ROOT/bin/verdict" ]; then
  (
    cd "$EVAL_SANDBOX" || exit 99
    set +e
    PATH="$EVAL_STUBBIN:$PATH" env -u VERDICT_BIN VERDICT_IMPL=go \
      bash "$EVAL_ROOT/scripts/review.sh" record acme/app local-disp-b --json >"$EVAL_SANDBOX/g3d.out" 2>"$EVAL_SANDBOX/g3d.err"
    echo "$?" >"$EVAL_SANDBOX/g3d.status"
    set -e
  )
  if [ "$(cat "$EVAL_SANDBOX/g3d.status")" = "1" ] \
    && [ "$(jq -r '.error.code // empty' "$EVAL_SANDBOX/g3d.out" 2>/dev/null)" = "missing-go-binary" ] \
    && grep -q "bin/verdict" "$EVAL_SANDBOX/g3d.out"; then
    good "G3 default bin/verdict path refuses typed naming the build output"
  else
    bad "G3 default bin/verdict path exits $(cat "$EVAL_SANDBOX/g3d.status")"
  fi
else
  echo "  note: G3 default-path leg skipped ($EVAL_ROOT/bin/verdict exists in this checkout)"
fi

# --- G4: unknown VERDICT_IMPL is a usage error (exit 2) ---
for bad_impl in bogus GO "bash "; do
  (
    cd "$EVAL_SANDBOX" || exit 99
    set +e
    PATH="$EVAL_STUBBIN:$PATH" VERDICT_IMPL="$bad_impl" \
      bash "$EVAL_ROOT/scripts/review.sh" record acme/app local-disp-b --json >"$EVAL_SANDBOX/g4.out" 2>"$EVAL_SANDBOX/g4.err"
    echo "$?" >"$EVAL_SANDBOX/g4.status"
    set -e
  )
  if [ "$(cat "$EVAL_SANDBOX/g4.status")" = "2" ] && grep -q "unknown VERDICT_IMPL" "$EVAL_SANDBOX/g4.err"; then
    good "G4 VERDICT_IMPL='$bad_impl' usage-errors (exit 2) on the record path"
  else
    bad "G4 VERDICT_IMPL='$bad_impl' exits $(cat "$EVAL_SANDBOX/g4.status")"
  fi
done
(
  cd "$EVAL_SANDBOX" || exit 99
  set +e
  PATH="$EVAL_STUBBIN:$PATH" VERDICT_IMPL=bogus \
    bash "$EVAL_ROOT/scripts/review.sh" check-tree >"$EVAL_SANDBOX/g4c.out" 2>"$EVAL_SANDBOX/g4c.err"
  echo "$?" >"$EVAL_SANDBOX/g4c.status"
  set -e
)
if [ "$(cat "$EVAL_SANDBOX/g4c.status")" = "2" ] && grep -q "unknown VERDICT_IMPL" "$EVAL_SANDBOX/g4c.err"; then
  good "G4 VERDICT_IMPL=bogus usage-errors (exit 2) on the no-positional path"
else
  bad "G4 no-positional path exits $(cat "$EVAL_SANDBOX/g4c.status")"
fi

# --- G5: non-record verbs stay bash under VERDICT_IMPL=go ---
printf '%s' "$VALID_REVIEW" | mk_bundle "local-disp-r"
run_impl bash "$EVAL_SANDBOX/par-b" -- record acme/app local-disp-r --json >/dev/null 2>&1
run_impl bash "$EVAL_SANDBOX/g5-b" -- report acme/app local-disp-r --json
bstatus="$EVAL_STATUS"
run_impl go "$EVAL_SANDBOX/g5-g" -- report acme/app local-disp-r --json
gstatus="$EVAL_STATUS"
if [ "$bstatus" = "$gstatus" ] \
  && cmp -s <(scrub "local-disp-r" "local-disp-r" <"$EVAL_SANDBOX/g5-b.out") <(scrub "local-disp-r" "local-disp-r" <"$EVAL_SANDBOX/g5-g.out") \
  && cmp -s "$EVAL_SANDBOX/g5-b.err" "$EVAL_SANDBOX/g5-g.err"; then
  good "G5 report stays bash under VERDICT_IMPL=go (byte-identical)"
else
  bad "G5 report differs under VERDICT_IMPL=go (status $bstatus vs $gstatus)"
fi
rm -rf "$EVAL_SANDBOX/.review/local-disp-r"

# --- G6: existing record-exercising cases pass under VERDICT_IMPL=go ---
# Each nested case gets a FRESH sandbox: the cases clean only their own
# bundles and assume an empty .review (e.g. kb expects absent kb stores), so
# sharing this case's sandbox would pollute the suite's later cases.
for c in record-input-refusals kb schema-contract review-provenance chunk-coverage chunk-incremental publish-links verify-allowlist; do
  nested_sb="$(mktemp -d "${TMPDIR:-/tmp}/verdict-disp-nested.XXXXXX")"
  if EVAL_SANDBOX="$nested_sb" VERDICT_IMPL=go VERDICT_BIN="$GOBIN_DIR/verdict" bash "$EVAL_ROOT/evals/cases/$c.sh" >"$EVAL_SANDBOX/g6-$c.log" 2>&1; then
    good "G6 $c passes under VERDICT_IMPL=go"
  else
    bad "G6 $c fails under VERDICT_IMPL=go (see $EVAL_SANDBOX/g6-$c.log)"
    tail -15 "$EVAL_SANDBOX/g6-$c.log" || true
  fi
  rm -rf "$nested_sb"
done

rm -rf "$EVAL_SANDBOX/dispatch-bin" "$EVAL_SANDBOX"/par-b.out "$EVAL_SANDBOX"/par-g.out \
  "$EVAL_SANDBOX"/par-b.err "$EVAL_SANDBOX"/par-g.err "$EVAL_SANDBOX"/g1-* "$EVAL_SANDBOX"/g3* \
  "$EVAL_SANDBOX"/g4* "$EVAL_SANDBOX"/g5-* "$EVAL_SANDBOX"/g6-*.log 2>/dev/null || true

if [ "$fails" -ne 0 ]; then echo "case dispatch-record: FAIL ($fails)"; exit 1; fi
echo "case dispatch-record: PASS"
