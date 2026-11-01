#!/usr/bin/env bash
# review.sh — local PR fetch/post/triage/report using gh CLI only.
# Verbs: fetch (network+files), post (validates, merges store, posts once),
#        triage (local store mutation, no network), report (pure read, no network),
#        fix (bounded verify-gated pass over local bundles, no network).
set -euo pipefail

usage() {
  cat <<'EOF'
usage: review.sh fetch|post|triage|report|next|show|verify|doctor|calibrate|fetch-local|record|adopt|fix|check-tree|ready|trend|learn|init|install [opts]
  fetch <owner/repo> <pr> [--base-sha|--since <sha>] [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] [--consent-over-budget] [--show-prompts] [--chunk] [--json]
    write .review/<pr>/{PR.json,full.diff,toc.txt,roles.txt,reviews.json,inline.json[,incremental.diff]} (PR.json normalized for non-github providers)
    diffs over 512000 bytes refuse with a split-or-narrow hint (empty diffs warn, never fail).
    Deliberate exceptions only: REVIEW_MAX_BYTES=<n> (or .review/max_bytes) overrides the cap.
    Chunking opt-in only: --chunk (or .review/chunk holding true) marks over-cap fetches for chunked slicing (override chain wins; absent reads today's refusal).
    With REVIEW_BUDGET_USD (or .review/budget) set, fetch prints a pre-run
    cost estimate from comparable past runs and refuses projected-over-budget
    runs unless --consent-over-budget is given (consent lands in budget.json,
    surfaced by report --cost). Unset: estimate off, fetch as today.
  fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]
        [--stash [n]] [--consent-over-budget] [--show-prompts] [--chunk] [-- paths...] [--json]
    local review bundle from git (no GitHub, no PR): writes
    .review/local-<name>/{source.json,full.diff,toc.txt,roles.txt}.
    Same diff cap as fetch (512000 bytes, overridable as above); untracked/binary exclusions warn, never fail.
    Same budget gate as fetch (estimate + --consent-over-budget); unset: as today.
    downstream verbs address it as local-<name>; post/calibrate refuse it.
    --show-prompts (fetch and fetch-local) renders the assembled agent prompt —
    SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + bundle meta + toc.txt + roles.txt +
    full.diff + kb.txt/sensitive.txt/panel when present, concatenated in that order —
    to stdout (human; progress moves to stderr) or data.prompt_dump (--json).
    No agent runs, no network beyond the fetch, no store writes beyond the bundle.
  record <owner/repo> <local-slug> [--json]
    validate + merge a local review.json into findings.json + record.json. No GitHub, no post.
  adopt <owner/repo> <pr> --from <local-slug> [--json]
    carry a local bundle's findings into a fresh PR fetch for posting (re-anchors, stamps handoff).
  fix <owner/repo> <local-slug> [--sig <s>] [--json]
    one bounded pass over a local bundle: picks the top open inline sig
    (or --sig), prints finding + kb.txt context with verify/land/re-run
    instructions. Max 3 passes per sig (ledger .review/<bundle>/fix.json);
    touching files outside the anchor stops the loop (scope-escape).
  post  <owner/repo> <pr> [--approve=true|false] [--request-changes] [--bind] [--force]
        [--review-file .review/<pr>/review.json] [--dry-run] [--json]
    validate anchors (partitioned, keep-biased), embed suggestions, merge findings.json,
    guard stale-SHA + duplicates, post exactly one review.
    Binding APPROVE/REQUEST_CHANGES needs --bind (else downgraded to COMMENT);
    APPROVE is refused with open or deferred findings unless --force.
    Agent status claims (resolved[]/triage[]) are proposals: addressed flips only on
    verification.json evidence (VERIFIED + resultHead matching HEAD).
    --dry-run prints validation + payload summary, writes findings.dryrun.json, posts nothing.
  triage <owner/repo> <pr> --sig <sig> --status <open|addressed|wont-fix|false-positive|deferred>
        --rationale "..." [--force] [--dry-run]
    mutate .review/<pr>/findings.json directly. No network. Rationale required unless --force.
  report <owner/repo> <pr> [--status open|addressed|all] [--kind inline|section|all]
        [--output <path>] [--sarif <path>] [--cost] [--check <critical|important|informational>] [--completeness] [--json]
    pure read over the store: ranked findings + verdict + counts. Advisory runs
    touch no network; gate runs (--check/--completeness) end with one live-thread recheck read.
    --sarif writes a SARIF 2.1.0 file (severity→level, sig as fingerprint) for scanners/CI.
    --cost adds a spend rollup from .review/<pr>/cost.json (absent/unreadable -> zeros, never fails; stratified by effort tier).
    Consented over-budget receipts from .review/<pr>/budget.json surface here when present.
    --check <tier> opts into gating (same output, machine exits): 0 no open
    findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool
    error (missing/corrupt store). Untagged findings never meet a tier; the
    gate counts open findings only, whatever --status/--kind show. Without
    --check the run stays advisory: exit 0 with findings listed (typed
    errors exit 1, usage exits 2).
    --completeness opts into a second gate dimension answering "is HEAD
    reviewed?" from the local completed-post record (local post-record
    read): pass iff post.json exists, parses, and carries a non-empty
    head equal to the bundle's fetched HEAD (PR.json head SHA, else
    source.json head_sha for local bundles). Alone it gates on HEAD-reviewed
    only; combined with --check the run fails if tier-hits OR stale/missing
    post. Every gate run (--check and/or --completeness) ends with a third,
    network-backed live-thread dimension: more live opens than local opens
    fails the gate (unreachable lookups warn and stay neutral; local and
    non-GitHub bundles warn-skip with zero network). Gate failure exits 1
    with a check: line naming the re-fetch command; tool errors keep exit 3.
    Without a gate flag output and exits are byte-identical to today.
  next <owner/repo> <pr> [--status open] [--json]
    top-ranked actionable finding + the command to act on it. Pure read.
  show <owner/repo> <pr> --sig <sig> [--json]
    one full finding record. Pure read.
  check-tree [--expect <prefix,...>] [--json]
    refuse on unexpected dirt (own .review/ scratch ignored). No network. Machine clean-tree gate for mutating modes.
  verify <owner/repo> <pr> [--timeout <secs, default 300>] [--json]
    execute patch.json verifyCmd with a watchdog, record exit + tail into the patch record.
    Runs in its own process group (timeout kills the tree); known secret values
    are redacted from the recorded tail.
    Runs the agent's claimed proof — exit code becomes machine fact. No GitHub I/O.
  doctor [--json]
    preflight the harness: gh auth, jq, shasum hard-fail the run; scopes, git,
    ps, API reachability warn only (never fail); .review storage writability
    hard-fails. Exit 0 unless a hard check fails. No repo/PR args, no writes,
    no network beyond the existing gh reads.
  calibrate <owner/repo> <pr> [--probe [--judge SIG:VERDICT[:HELPFUL]]... [--judge-file PATH]] [--json]
    join findings against live ground truth (thread states + files changed since the
    review head). Read-only; reports outcomes + tier precision, never auto-tunes.
    --probe grades closed findings offline against recorded triage dispositions
    (addressed = real, false-positive = false, else unknown — never forced)
    into additive .review/<bundle>/kb-grades.jsonl rows; writes nothing else,
    needs no post record, touches no network. Baseline columns
    (verdict/rule/helpful) regenerate from findings.json every run; agent
    judgments arrive via --judge/--judge-file, are validated against known
    sigs (unknown sigs refuse typed), and merge as agent_verdict/agent_helpful/
    agreement columns that may diverge from the baseline — the agreement rate
    over judged rows is the M4 evidence number.
  ready <owner/repo> <pr> [--json] [--check]
    advisory only (exit 0, never a gate): CI state, open threads, draft/conflict,
    description completeness. Prints blockers with actions + recommendation
    (review/wait/nudge-author). PR bundles only; local bundles refused.
    --check opts into gating (same output, machine exits): 0 ready, 1 blockers
    or wait, 2 usage, 3 tool error. Without --check the run stays advisory:
    exit 0 with blockers listed (typed errors exit 1, usage exits 2).
  trend <owner/repo> [--overlap] [--json]
    portfolio rollup over existing bundle stores (no network, writes nothing):
    findings opened/addressed/closed over time, broken down by severity tier
    and status, with per-bundle models/effort for the calibration join.
    Recomputed every run (manual edits stay truthful); missing/unreadable
    stores warn and skip, never fail. `closed` means no longer actionable
    (wont-fix/false-positive/deferred); null severity reads untagged.
    --overlap adds the KB overlap audit: an offline join of kb-recall.json
    manifests, findings kb cites, and kb-joins.jsonl into recall precision,
    recall coverage of cites, and the cited-finding outcome split
    (addressed vs false-positive); missing manifests read unmeasured (never
    zero) and stay out of both denominators. Default output is byte-identical
    without the flag.
  learn --rule <text> --scope <globs> [--id <id>] [--provenance <s>] [--rigor <level>] [--json]
    append one rule to .review/kb.jsonl (zeros + status active; provenance
    defaults to manual, rigor defaults to pointed). Refuses exact-duplicate
    rule text and taken ids.
  learn --status [--json]
    list rules with counters + precision. No kb.jsonl reads as no rules.
  learn --retire <id> --reason <text> [--json]
    retire a rule (tombstone kept, counters untouched, history-logged);
    reason needs 30+ chars.
  learn --restore <id> --reason <text> [--json]
    restore a retired rule to active (tombstone history kept);
    reason needs 30+ chars.
  init [--preset chill|standard|strict] [--yes] [--force] [--json]
    guided offline scaffolder: writes the standard local control files
    (.review/budget, .review/panel, .review/kb-policy, .review/kb.jsonl
    starter) with style presets, then runs check-tree validation.
    Presets map to panel strictness + KB retirement thresholds:
    chill (no pinned panel, MIN_JUDGED=10 MAX_FP_RATE=0.7),
    standard (one pinned member, 5 / 0.5), strict (lead + 2 lenses, 3 / 0.3).
    Never touches network. Existing files print a preview and need
    --yes/--force (or an interactive confirm) before overwrite.
  install [--yes] [--force] [--json]
    copy-based skill installer (offline): detects the de-facto agent skill
    dirs (.agents/skills, .claude/skills, .codex/skills at the repo root
    and $HOME), previews the copy plan for the tool-owned verdict/
    payload (SKILL.md + prompts/), applies only on explicit confirm.
    Only installer-owned verdict/ dirs (carrying a .verdict-managed
    receipt) are ever written; foreign skills and unowned verdict/ dirs
    are left untouched. An up-to-date re-run reads keep (idempotent); a
    receipt/tool version skew warns, never fails. Same preview-then-confirm
    idiom as init: writes need --yes/--force (or an interactive confirm
    when stdin is a tty). The install.sh symlink route stays the
    wide-coverage sibling (nine agents, --detect/--all).
  Global presentation flags (any verb; stripped before verb parsing):
  --color <auto|always|never>, --compact / --no-compact, --no-json.
  Global publish flags (any verb; stripped before verb parsing):
  --footer / --no-footer, --attribution [off|product|full] / --no-attribution,
  --attribution-text <text>, --copyright <text>, --logo / --no-logo,
  --logo-url <url>, --overview / --no-overview,
  --diagram-behavioral / --no-diagram-behavioral,
  --diagram-structural / --no-diagram-structural,
  --diagram-expanded / --no-diagram-expanded.
  Flags win over the settings file, which wins over env
  (REVIEW_OUTPUT_FORMAT, REVIEW_LAYOUT_COMPACT, REVIEW_COLOR_MODE,
  REVIEW_PUBLISH_FOOTER, REVIEW_PUBLISH_ATTRIBUTION,
  REVIEW_PUBLISH_ATTRIBUTION_TEXT, REVIEW_PUBLISH_COPYRIGHT,
  REVIEW_PUBLISH_LOGO, REVIEW_PUBLISH_LOGO_URL, REVIEW_PUBLISH_OVERVIEW,
  REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL, REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL,
  REVIEW_PUBLISH_DIAGRAMS_EXPANDED),
  which wins over defaults. The settings file holds presentation (output,
  layout, color) plus publish (footer receipt + leveled attribution + logo +
  overview + diagrams) namespaces
  (~/.config/verdict/settings.json, $XDG_CONFIG_HOME respected).
  --json on fetch/post/triage/report/next/show/verify/doctor/calibrate/fetch-local/record/adopt/fix/check-tree/ready/trend/learn/init/install: stdout is exactly one JSON envelope (schema_version 1);
  all human progress goes to stderr. Errors emit {data:null,error:{code,message}} non-zero.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then usage; exit 0; fi
if [[ "${1:-}" == "--version" ]]; then jq -r .version "$(dirname "${BASH_SOURCE[0]:-$0}")/../package.json" 2>/dev/null || echo "0.0.0-unknown"; exit 0; fi
cmd="${1:-}"; shift $(( $# > 0 ? 1 : 0 ))
repo=""; pr=""
if [[ "$cmd" != "check-tree" && "$cmd" != "doctor" && "$cmd" != "trend" && "$cmd" != "learn" && "$cmd" != "init" && "$cmd" != "install" ]]; then
  repo="${1:-}"; pr="${2:-}"; shift $(( $# >= 2 ? 2 : $# ))
fi
if [[ "$cmd" == "trend" ]]; then
  repo="${1:-}"; shift $(( $# >= 1 ? 1 : $# ))
fi

need() { command -v "$1" >/dev/null || { echo "missing: $1" >&2; exit 1; }; }
need gh; need jq; need shasum
warn() {
  if [[ "${EFFECTIVE_COLOR:-auto}" == "always" ]]; then printf '\033[33mwarn: %s\033[0m\n' "$*" >&2;
  else echo "warn: $*" >&2; fi
  WARNINGS_JSON=$(jq --arg w "$*" '. + [$w]' <<<"$WARNINGS_JSON");
}
WARNINGS_JSON="[]"; export WARNINGS_JSON
# Machine-actionable narrower-scope candidates for the next refusal (fail()
# carries them in the JSON envelope; additive-only — absent reads null).
CANDIDATES_JSON="null"
json_mode="false"
# Gate switch for ready --check and report --check only: fail() exits 3 (tool
# broke) so hooks can tell it apart from a failed gate (exit 1). Stays false
# everywhere else, so every other verb keeps its fail-contract exits untouched.
check_gate="false"

# Fetch diff cap (bytes): fetched diffs above this refuse with a split-or-narrow
# hint instead of burning review tokens. Empty diffs warn and proceed.
# Overridable for deliberate exceptions only (release diffs, vendor drops):
# REVIEW_MAX_BYTES env wins, else first usable line of .review/max_bytes,
# else the 512000 default. Unset = default cap, same as today.
resolve_fetch_cap() { # sets FETCH_MAX_BYTES (bytes), fetch_cap_src ("" when default), cap_json (null unless overridden)
  FETCH_MAX_BYTES="512000"; fetch_cap_src=""; cap_json="null"
  if [[ -n "${REVIEW_MAX_BYTES:-}" ]]; then
    FETCH_MAX_BYTES=$(printf '%s' "$REVIEW_MAX_BYTES" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    fetch_cap_src="REVIEW_MAX_BYTES"
  elif [[ -f .review/max_bytes ]]; then
    FETCH_MAX_BYTES=$(grep -v '^[[:space:]]*#' .review/max_bytes | grep -v '^[[:space:]]*$' | head -n 1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)
    [[ -n "$FETCH_MAX_BYTES" ]] && fetch_cap_src=".review/max_bytes"
  fi
  [[ -z "$FETCH_MAX_BYTES" ]] && { FETCH_MAX_BYTES="512000"; fetch_cap_src=""; cap_json="null"; return 0; }
  [[ "$FETCH_MAX_BYTES" =~ ^[0-9]+$ ]] && (( FETCH_MAX_BYTES > 0 )) || fail "bad-max-bytes" "cap '$FETCH_MAX_BYTES' from ${fetch_cap_src:-default} is not a positive byte count (want e.g. REVIEW_MAX_BYTES=1000000 or a .review/max_bytes file holding 1000000)"
  if [[ -n "$fetch_cap_src" ]]; then
    echo "» cap: $FETCH_MAX_BYTES bytes (from $fetch_cap_src, default 512000)"
    cap_json=$(jq -n --argjson b "$FETCH_MAX_BYTES" --arg s "$fetch_cap_src" '{bytes:$b, source:$s}')
  fi
}
# Chunking opt-in predicate (settled here; the slicing itself lands in a later
# slice): explicit --chunk wins, else first usable line of .review/chunk, else
# off. Knob files hold sizes + flags only, never credentials. The existing
# cap-override chain keeps precedence: any overridden cap (env or file)
# disables chunking outright, so deliberate exceptions stay deliberate. Silent
# when absent/off, and never consulted on the under-cap path — so absent-knob
# runs read today's bytes exactly.
# Sets CHUNK_OPT_IN ("true"/"false") and chunk_src ("--chunk"/".review/chunk"/"").
resolve_chunk_opt_in() { # no args; reads chunk_flag + fetch_cap_src; sets CHUNK_OPT_IN, chunk_src
  local knob=""
  CHUNK_OPT_IN="false"; chunk_src=""
  if [[ -n "$fetch_cap_src" ]]; then return 0; fi
  if [[ "${chunk_flag:-false}" == "true" ]]; then CHUNK_OPT_IN="true"; chunk_src="--chunk"; return 0; fi
  if [[ -f .review/chunk ]]; then
    knob=$(grep -v '^[[:space:]]*#' .review/chunk | grep -v '^[[:space:]]*$' | head -n 1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | tr '[:upper:]' '[:lower:]' || true)
    case "$knob" in
      ""|false|off|0|no) return 0;;
      true|on|1|yes) CHUNK_OPT_IN="true"; chunk_src=".review/chunk"; return 0;;
      *) fail "bad-chunk-knob" "chunk knob '$knob' from .review/chunk is not an opt-in flag (want true|false, on|off, 1|0, yes|no — first usable line wins, absent reads off)";;
    esac
  fi
}
# Review-level confidence/security globals, set by validate_and_merge from the
# optional review.json fields (absent stays null). Callers persist them.
rev_confidence="null"; rev_security="null"
# Exact re-run command for the current long verb (post/record set it after flag
# parse); fail() prints it so failures name re-entry.
rentry_cmd=""

# Typed failure: JSON envelope on stdout (original) in --json mode, else stderr text.
fail() {
  local code="$1"; local msg="$2"
  local hint=""
  # Remediation hint: one try: line naming the fix. Codes unchanged.
  case "$code" in
    auth-missing) hint="try: export the credential named above, then re-run";;
    fetch-failed|pr-read-failed|threads-failed) hint="try: run 'gh auth status', confirm the repo/PR spelling, then re-run";;
    post-failed) hint="try: narrow anchor drift or body size (see above), then re-run";;
    diff-too-large) hint="try: split the range (--range/--base/--base-sha) or narrow with a paths filter, then re-fetch";;
    missing-chunk-coverage|missing-chunk-snapshot) hint="try: re-fetch the bundle with --chunk, then re-synthesize across all chunks and re-run";;
    missing-*|not-a-repo|not-local|no-pr-target|no-threads|no-verify-cmd|report-failed) hint="try: run the fetch/record step named above first, then re-run";;
    corrupt-store) hint="try: re-run post/record to rebuild the store, or restore findings.json from backup, then re-run";;
    bad-timeout) hint="try: pass --timeout <positive-seconds> (default 300)";;
    bad-approve) hint="try: pass --approve=true|false";;
    bad-review-schema) hint="try: set \"schema_version\": \"1\" in review.json";;
    approve-blocked) hint="try: triage the findings or re-run with --force";;
    budget-exceeded) hint="try: re-run with --consent-over-budget once the spend is approved, or raise REVIEW_BUDGET_USD/.review/budget";;
    duplicate-payload) hint="try: re-run with --force to repost the identical payload";;
    already-posted) hint="try: re-run with --force to override, or fetch --base-sha <posted-head> for the delta and review it instead";;
    degenerate-body) hint="try: open with a verdict marker (see prompts/review.md), then re-run";;
    pr-not-open) hint="try: re-fetch the PR (bundle may be stale)";;
    unknown-sig|ambiguous-sig|unknown-judgment) hint="try: copy the full sig prefix from report output";;
    bad-sig) hint="try: list open inline sigs with report --status open --kind inline, then re-run with the full prefix";;
    conflicted-tree) hint="try: resolve the conflicts (or abort the merge), then re-run";;
    fix-ceiling) hint="try: triage the sig or split the fix — the 3-pass ceiling is hard-coded";;
    scope-escape) hint="try: revert or stash files outside the anchor path, then re-run fix";;
    rationale-required) hint="try: add --rationale with 30+ chars, or --force";;
    already-adopted|provider-unimplemented) hint="try: use the verb named above for this bundle kind";;
    needs-confirm) hint="try: re-run with --yes after reviewing the preview, then re-run";;
    bad-settings-credentials) hint="try: remove the credential key from the settings file (credentials never live there), then re-run";;
    bad-settings) hint="try: keep only output/layout/color/publish keys (see docs/manual.md), then re-run";;
    bad-*) hint="try: re-run with one of the valid values named above";;
    already-learned) hint="try: cite the existing rule id instead of learning it twice";;
    verify-destructive) hint="try: rewrite verifyCmd to stay in-repo and non-destructive (run the test runner, not rm/mkfs/dd), then re-run";;
    verify-networked) hint="try: rewrite verifyCmd without network fetches or pipe-to-shell (fetch fixtures first, run offline), then re-run";;
    verify-credential-read) hint="try: rewrite verifyCmd without reading credential paths or piping env into the network, then re-run";;
    verify-repo-escape) hint="try: rewrite verifyCmd with repo-relative paths (/tmp for scratch), then re-run";;
    id-taken) hint="try: pass a different --id";;
    unknown-rule) hint="try: list rules with learn --status, then re-run";;
    already-retired|not-retired) hint="try: check the rule status with learn --status, then re-run";;
    *) hint="try: address the cause above, then re-run the same command";;
  esac
  if [[ -n "$hint" ]]; then msg="$msg
$hint"; fi
  if [[ "$json_mode" == "true" ]]; then
    # Candidates ride along on the refusal that sets them (oversize fetch);
    # every other error keeps its byte-identical envelope (additive-only).
    jq -n --arg c "$code" --arg m "$msg" --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      --argjson cand "${CANDIDATES_JSON:-null}" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"",warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:null,error:{code:$c,message:$m} + (if $cand == null then {} else {candidates:$cand} end)}' >&9
  else
    if [[ "${EFFECTIVE_COLOR:-auto}" == "always" ]]; then printf '\033[31merror: %s\033[0m\n' "$msg" >&2;
    else echo "error: $msg" >&2; fi
  fi
  if [[ -n "$rentry_cmd" ]]; then echo "re-enter: $rentry_cmd" >&2; fi
  # Ready --check turns typed errors into exit 3 so hooks can tell "tool
  # broke" apart from "PR not ready" (exit 1). check_gate stays false on every
  # other path, so all existing fail-contract exits are untouched.
  if [[ "$check_gate" == "true" ]]; then exit 3; fi
  exit 1
}

# ---- prefs (presentation + publish settings file) ----
# CLI survey (this file): the only presentation surfaces are the machine
# envelope (--json), file outputs (report --output/--sarif), and the
# prompt-dump destination (--show-prompts stdout vs envelope). No color,
# layout, theme, pager, or verbosity knobs exist. The settings file therefore
# exposes exactly three presentation namespaces — output (format, the --json
# default), layout (compact, advisory progress verbosity), color (mode;
# the tool emits no ANSI today, so auto/never read plain and only always
# adds codes). Top-level keys are limited to $schema, output, layout,
# color; anything else (including server-shaped or org.* namespaces)
# refuses with bad-settings. Any credential-shaped key at any depth
# refuses with bad-settings-credentials. Precedence: flags > file > env
# (REVIEW_OUTPUT_FORMAT, REVIEW_LAYOUT_COMPACT, REVIEW_COLOR_MODE) >
# defaults. Absent file keeps the default surface (footer on): parity means
# the gates stay byte-identical (marker-first, degenerate-body), not the bytes.
# XDG home: ${XDG_CONFIG_HOME:-$HOME/.config}/verdict/settings.json.
# Attribution identity (P1b): single source of truth for the footer line,
# the default logo asset, and the P5 {product_url} placeholder value.
# The default logo URL names the tracked in-repo asset
# apps/docs/public/logo.svg (git-tracked, scheme-independent, so no
# dark/light pair is needed).
PRODUCT_URL="https://stainless-code.com/verdict"
MAKER_URL="https://stainless-code.com"
MAKER_NAME="Stainless Code"
DEFAULT_LOGO_URL="https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg"
FOOTER_DIVIDER="<!-- verdict-footer -->"
PREF_SETTINGS_FILE=""; EFFECTIVE_OUTPUT="text"; EFFECTIVE_COMPACT="false"; EFFECTIVE_COLOR="auto"
FLAG_OUTPUT_FORCE=""; FLAG_COMPACT_FORCE=""; FLAG_COLOR_FORCE=""
EFFECTIVE_PUBLISH_FOOTER="true"; EFFECTIVE_PUBLISH_ATTRIBUTION="product"
EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT=""; EFFECTIVE_PUBLISH_COPYRIGHT=""
EFFECTIVE_PUBLISH_LOGO="true"; EFFECTIVE_PUBLISH_LOGO_URL="$DEFAULT_LOGO_URL"
EFFECTIVE_PUBLISH_OVERVIEW="true"
EFFECTIVE_PUBLISH_DIAGRAMS_STRUCTURAL="true"; EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL="false"
EFFECTIVE_PUBLISH_DIAGRAMS_EXPANDED="false"
FLAG_PUBLISH_FOOTER_FORCE=""; FLAG_PUBLISH_ATTRIBUTION_FORCE=""
FLAG_PUBLISH_ATTRIBUTION_TEXT_FORCE=""; FLAG_PUBLISH_ATTRIBUTION_TEXT_SET="false"
FLAG_PUBLISH_COPYRIGHT_FORCE=""; FLAG_PUBLISH_COPYRIGHT_SET="false"
FLAG_PUBLISH_LOGO_FORCE=""; FLAG_PUBLISH_LOGO_URL_FORCE=""; FLAG_PUBLISH_LOGO_URL_SET="false"
FLAG_PUBLISH_OVERVIEW_FORCE=""
FLAG_PUBLISH_DIAGRAMS_STRUCTURAL_FORCE=""; FLAG_PUBLISH_DIAGRAMS_BEHAVIORAL_FORCE=""
FLAG_PUBLISH_DIAGRAMS_EXPANDED_FORCE=""
PREF_COLOR_SET="false"
# Envelope redirect, run once: the original stdout moves to fd 9 for the JSON
# envelope, human progress moves to stderr. Verbs call this after flag
# parsing; prefs calls it early on a --json peek so typed prefs errors land
# in the envelope too. Guarded — a second call is a no-op (re-execing would
# repoint fd 9 at stderr and lose the envelope).
json_redirect() {
  [[ "$json_mode" == "true" && "${_FD9_READY:-false}" != "true" ]] || return 0
  exec 9>&1; exec 1>&2; _FD9_READY="true"
}
resolve_prefs() {
  local base="${HOME:-$PWD}/.config"
  local v="" t="" top="" k="" ok="" cred_n=""
  if [[ -n "${XDG_CONFIG_HOME:-}" ]]; then base="$XDG_CONFIG_HOME"; fi
  PREF_SETTINGS_FILE="$base/verdict/settings.json"
  case "${REVIEW_OUTPUT_FORMAT:-}" in
    "") ;;
    text|json) EFFECTIVE_OUTPUT="${REVIEW_OUTPUT_FORMAT}";;
    *) warn "ignoring invalid REVIEW_OUTPUT_FORMAT '${REVIEW_OUTPUT_FORMAT}' (want text|json)";;
  esac
  case "${REVIEW_LAYOUT_COMPACT:-}" in
    "") ;;
    true|false) EFFECTIVE_COMPACT="${REVIEW_LAYOUT_COMPACT}";;
    *) warn "ignoring invalid REVIEW_LAYOUT_COMPACT '${REVIEW_LAYOUT_COMPACT}' (want true|false)";;
  esac
  case "${REVIEW_COLOR_MODE:-}" in
    "") ;;
    auto|always|never) EFFECTIVE_COLOR="${REVIEW_COLOR_MODE}";;
    *) warn "ignoring invalid REVIEW_COLOR_MODE '${REVIEW_COLOR_MODE}' (want auto|always|never)";;
  esac
  # Sync the envelope default early (env layer): a bad settings file under an
  # env json default must still fail typed in the envelope. The file layer
  # re-syncs below when it sets output.format; --no-json/--json flags apply
  # after (flags win), and verbs re-assert --json, so a peek is never lost.
  if [[ "$EFFECTIVE_OUTPUT" == "json" ]]; then json_mode="true"; fi
  json_redirect
  resolve_publish
  [[ -f "$PREF_SETTINGS_FILE" ]] || return 0
  jq empty "$PREF_SETTINGS_FILE" >/dev/null 2>&1 \
    || fail "bad-settings" "settings file $PREF_SETTINGS_FILE is not valid JSON (fix it or delete it)"
  [[ "$(jq -r 'type' "$PREF_SETTINGS_FILE")" == "object" ]] \
    || fail "bad-settings" "settings file $PREF_SETTINGS_FILE must hold a JSON object (want only \$schema, output, layout, color, publish)"
  cred_n=$(jq -r '[.. | objects | keys[] | ascii_downcase] | map(select(test("token|secret|passwd|password|pwd|auth|bearer|credential|pat|key"))) | length' "$PREF_SETTINGS_FILE" 2>/dev/null) \
    || fail "bad-settings" "settings file $PREF_SETTINGS_FILE is unreadable (fix it or delete it)"
  [[ "$cred_n" == "0" ]] \
    || fail "bad-settings-credentials" "settings file $PREF_SETTINGS_FILE names a credential key (token/key/auth/secret/password/pat/credential/bearer) — credentials never live in the settings file (export them as env instead)"
  top=$(jq -r 'keys[]' "$PREF_SETTINGS_FILE") \
    || fail "bad-settings" "settings file $PREF_SETTINGS_FILE is unreadable (fix it or delete it)"
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    case "$k" in '$schema'|output|layout|color|publish) ;;
      *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE carries unknown top-level key '$k' (want only \$schema, output, layout, color, publish; no server-shaped or org.* namespaces)";;
    esac
  done <<<"$top"
  if [[ -n "$top" ]] && jq -e 'has("$schema")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    [[ "$(jq -r '."$schema" | type' "$PREF_SETTINGS_FILE")" == "string" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: \$schema must be a string (schema hint only, never fetched)"
  fi
  if jq -e 'has("output")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    [[ "$(jq -r '.output | type' "$PREF_SETTINGS_FILE")" == "object" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: output must be an object (want {\"format\": \"text\"|\"json\"})"
    ok=$(jq -r '.output | keys[]' "$PREF_SETTINGS_FILE" 2>/dev/null || true)
    while IFS= read -r k; do
      [[ -z "$k" ]] && continue
      case "$k" in format) ;;
        *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: unknown output key '$k' (want only format)";;
      esac
    done <<<"$ok"
    if jq -e '.output | has("format")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
      v=$(jq -r '.output.format' "$PREF_SETTINGS_FILE")
      case "$v" in
        text|json) EFFECTIVE_OUTPUT="$v";;
        *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: output.format '$v' unknown (want text|json)";;
      esac
      # A peeked --json flag survives the file sync (flags win over the file);
      # verbs re-assert it afterwards, so the peek is never lost either way.
      if [[ "$EFFECTIVE_OUTPUT" == "json" || "${_JSON_PEEKED:-false}" == "true" ]]; then json_mode="true"; else json_mode="false"; fi
      json_redirect
    fi
  fi
  if jq -e 'has("layout")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    [[ "$(jq -r '.layout | type' "$PREF_SETTINGS_FILE")" == "object" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: layout must be an object (want {\"compact\": true|false})"
    ok=$(jq -r '.layout | keys[]' "$PREF_SETTINGS_FILE" 2>/dev/null || true)
    while IFS= read -r k; do
      [[ -z "$k" ]] && continue
      case "$k" in compact) ;;
        *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: unknown layout key '$k' (want only compact)";;
      esac
    done <<<"$ok"
    if jq -e '.layout | has("compact")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
      t=$(jq -r '.layout.compact | type' "$PREF_SETTINGS_FILE")
      [[ "$t" == "boolean" ]] \
        || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: layout.compact must be a boolean (want true|false)"
      EFFECTIVE_COMPACT=$(jq -r '.layout.compact' "$PREF_SETTINGS_FILE")
    fi
  fi
  if jq -e 'has("color")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    [[ "$(jq -r '.color | type' "$PREF_SETTINGS_FILE")" == "object" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: color must be an object (want {\"mode\": \"auto\"|\"always\"|\"never\"})"
    ok=$(jq -r '.color | keys[]' "$PREF_SETTINGS_FILE" 2>/dev/null || true)
    while IFS= read -r k; do
      [[ -z "$k" ]] && continue
      case "$k" in mode) ;;
        *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: unknown color key '$k' (want only mode)";;
      esac
    done <<<"$ok"
    if jq -e '.color | has("mode")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
      v=$(jq -r '.color.mode' "$PREF_SETTINGS_FILE")
      case "$v" in
        auto|always|never) EFFECTIVE_COLOR="$v"; PREF_COLOR_SET="true";;
        *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: color.mode '$v' unknown (want auto|always|never)";;
      esac
    fi
  fi
}
# Publish knobs (P1 footer receipt + P1b leveled attribution + logo). One
# `publish` namespace: footer (bool, default true), attribution (enum
# off|product|full, default product — P1b migration: the day-old P1 bool is
# refused in the file, use "off" for false and "product" for true),
# attribution_text (string, default factual, empty reads default;
# {version}, {tier} and {product_url} substituted, anything else refused),
# copyright (string, default empty = off), logo (bool, default true),
# logo_url (string, default the tracked docs asset; empty reads text-only,
# never a broken image), overview (bool, default true — gates the P3 collapsed
# file-table + topology block; the size threshold still decides each run
# unless a global --overview/--no-overview flag forces it), diagrams (object,
# default all-off-safe: {structural, behavioral, expanded} — structural (bool,
# default true) gates the topology half of the overview block (ANDed with
# publish.overview plus the P3 threshold); behavioral (bool, default false)
# gates the agent-drawn P4 block (renders only when opted in); expanded (bool,
# default false) renders the kept block open instead of collapsed. Global
# --diagram-<key>/--no-diagram-<key> flags force each one (flags win)).
# Precedence flags > file > env (REVIEW_PUBLISH_*
# below) > defaults. Unknown keys refuse bad-settings; the credential scan
# in resolve_prefs already covers this depth. Called from resolve_prefs so
# the env layer applies even with no file.
# Attribution placeholder inventory v1 (P1b; P5 grows it for overrides):
# {version}, {tier}, {product_url}, {files_reviewed}, {head_sha_short}, {model},
# {cost}, {attribution}. Anything else refuses typed bad-settings so typos
# surface instead of rendering literally. The settings/flag attribution text and
# the P5 fragments share this one validator (single mechanism, extended inventory).
# Overview knob (P3): publish.overview (bool, default true) gates the
# collapsed file-table + topology block in report --output; the size
# threshold (render_overview) still decides each run unless a global
# --overview/--no-overview flag forces the outcome (flags win).
validate_attribution_placeholders() { # $1 = text, $2 = source description
  local found="" ph=""
  found=$(printf '%s' "$1" | grep -o '{[A-Za-z_][A-Za-z0-9_]*}' 2>/dev/null || true)
  while IFS= read -r ph; do
    [[ -z "$ph" ]] && continue
    case "$ph" in "{version}"|"{tier}"|"{product_url}"|"{files_reviewed}"|"{head_sha_short}"|"{model}"|"{cost}"|"{attribution}") ;;
      *) fail "bad-settings" "$2 carries unknown placeholder '$ph' (want one of {version}, {tier}, {product_url}, {files_reviewed}, {head_sha_short}, {model}, {cost}, {attribution})";;
    esac
  done <<<"$found"
}
resolve_publish() {
  local t="" ok="" k="" v=""
  EFFECTIVE_PUBLISH_FOOTER="true"; EFFECTIVE_PUBLISH_ATTRIBUTION="product"
  EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT=""; EFFECTIVE_PUBLISH_COPYRIGHT=""
  EFFECTIVE_PUBLISH_LOGO="true"; EFFECTIVE_PUBLISH_LOGO_URL="$DEFAULT_LOGO_URL"
  EFFECTIVE_PUBLISH_OVERVIEW="true"
  EFFECTIVE_PUBLISH_DIAGRAMS_STRUCTURAL="true"; EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL="false"
  EFFECTIVE_PUBLISH_DIAGRAMS_EXPANDED="false"
  case "${REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL:-}" in
    "") ;;
    true|false) EFFECTIVE_PUBLISH_DIAGRAMS_STRUCTURAL="${REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL}";;
    *) warn "ignoring invalid REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL '${REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL}' (want true|false)";;
  esac
  case "${REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL:-}" in
    "") ;;
    true|false) EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL="${REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL}";;
    *) warn "ignoring invalid REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL '${REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL}' (want true|false)";;
  esac
  case "${REVIEW_PUBLISH_DIAGRAMS_EXPANDED:-}" in
    "") ;;
    true|false) EFFECTIVE_PUBLISH_DIAGRAMS_EXPANDED="${REVIEW_PUBLISH_DIAGRAMS_EXPANDED}";;
    *) warn "ignoring invalid REVIEW_PUBLISH_DIAGRAMS_EXPANDED '${REVIEW_PUBLISH_DIAGRAMS_EXPANDED}' (want true|false)";;
  esac
  case "${REVIEW_PUBLISH_FOOTER:-}" in
    "") ;;
    true|false) EFFECTIVE_PUBLISH_FOOTER="${REVIEW_PUBLISH_FOOTER}";;
    *) warn "ignoring invalid REVIEW_PUBLISH_FOOTER '${REVIEW_PUBLISH_FOOTER}' (want true|false)";;
  esac
  case "${REVIEW_PUBLISH_ATTRIBUTION:-}" in
    "") ;;
    off|product|full) EFFECTIVE_PUBLISH_ATTRIBUTION="${REVIEW_PUBLISH_ATTRIBUTION}";;
    true) warn "REVIEW_PUBLISH_ATTRIBUTION 'true' is deprecated (P1 bool migrated to off|product|full) — treating as product"; EFFECTIVE_PUBLISH_ATTRIBUTION="product";;
    false) warn "REVIEW_PUBLISH_ATTRIBUTION 'false' is deprecated (P1 bool migrated to off|product|full) — treating as off"; EFFECTIVE_PUBLISH_ATTRIBUTION="off";;
    *) warn "ignoring invalid REVIEW_PUBLISH_ATTRIBUTION '${REVIEW_PUBLISH_ATTRIBUTION}' (want off|product|full)";;
  esac
  [[ -n "${REVIEW_PUBLISH_ATTRIBUTION_TEXT:-}" ]] && EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT="${REVIEW_PUBLISH_ATTRIBUTION_TEXT}"
  [[ -n "${REVIEW_PUBLISH_COPYRIGHT:-}" ]] && EFFECTIVE_PUBLISH_COPYRIGHT="${REVIEW_PUBLISH_COPYRIGHT}"
  case "${REVIEW_PUBLISH_LOGO:-}" in
    "") ;;
    true|false) EFFECTIVE_PUBLISH_LOGO="${REVIEW_PUBLISH_LOGO}";;
    *) warn "ignoring invalid REVIEW_PUBLISH_LOGO '${REVIEW_PUBLISH_LOGO}' (want true|false)";;
  esac
  [[ -n "${REVIEW_PUBLISH_LOGO_URL:-}" ]] && EFFECTIVE_PUBLISH_LOGO_URL="${REVIEW_PUBLISH_LOGO_URL}"
  case "${REVIEW_PUBLISH_OVERVIEW:-}" in
    "") ;;
    true|false) EFFECTIVE_PUBLISH_OVERVIEW="${REVIEW_PUBLISH_OVERVIEW}";;
    *) warn "ignoring invalid REVIEW_PUBLISH_OVERVIEW '${REVIEW_PUBLISH_OVERVIEW}' (want true|false)";;
  esac
  [[ -f "$PREF_SETTINGS_FILE" ]] || { validate_attribution_placeholders "$EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT" "publish.attribution_text"; return 0; }
  jq -e 'has("publish")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1 || { validate_attribution_placeholders "$EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT" "publish.attribution_text"; return 0; }
  [[ "$(jq -r '.publish | type' "$PREF_SETTINGS_FILE")" == "object" ]] \
    || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish must be an object (want {footer, attribution, attribution_text, copyright, logo, logo_url, overview, diagrams})"
  ok=$(jq -r '.publish | keys[]' "$PREF_SETTINGS_FILE" 2>/dev/null || true)
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    case "$k" in footer|attribution|attribution_text|copyright|logo|logo_url|overview|diagrams) ;;
      *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: unknown publish key '$k' (want only footer, attribution, attribution_text, copyright, logo, logo_url, overview, diagrams)";;
    esac
  done <<<"$ok"
  if jq -e '.publish | has("footer")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.footer | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "boolean" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.footer must be a boolean (want true|false)"
    EFFECTIVE_PUBLISH_FOOTER=$(jq -r '.publish.footer' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish | has("attribution")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.attribution | type' "$PREF_SETTINGS_FILE")
    if [[ "$t" == "boolean" ]]; then
      fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.attribution is now off|product|full (P1b migration from the day-old P1 bool — use \"off\" for false, \"product\" for true)"
    fi
    [[ "$t" == "string" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.attribution must be a string (want off|product|full)"
    v=$(jq -r '.publish.attribution' "$PREF_SETTINGS_FILE")
    case "$v" in
      off|product|full) EFFECTIVE_PUBLISH_ATTRIBUTION="$v";;
      *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.attribution '$v' unknown (want off|product|full)";;
    esac
  fi
  if jq -e '.publish | has("attribution_text")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.attribution_text | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "string" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.attribution_text must be a string"
    EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT=$(jq -r '.publish.attribution_text' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish | has("copyright")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.copyright | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "string" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.copyright must be a string"
    EFFECTIVE_PUBLISH_COPYRIGHT=$(jq -r '.publish.copyright' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish | has("logo")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.logo | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "boolean" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.logo must be a boolean (want true|false)"
    EFFECTIVE_PUBLISH_LOGO=$(jq -r '.publish.logo' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish | has("logo_url")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.logo_url | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "string" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.logo_url must be a string (empty reads text-only)"
    EFFECTIVE_PUBLISH_LOGO_URL=$(jq -r '.publish.logo_url' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish | has("overview")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.overview | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "boolean" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.overview must be a boolean (want true|false)"
    EFFECTIVE_PUBLISH_OVERVIEW=$(jq -r '.publish.overview' "$PREF_SETTINGS_FILE")
  fi
  resolve_publish_diagrams
  validate_attribution_placeholders "$EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT" "settings file $PREF_SETTINGS_FILE: publish.attribution_text"
}
# Diagrams namespace (P4): publish.diagrams holds exactly {structural,
# behavioral, expanded}, all booleans. structural (default true) gates the
# P3 topology half of the overview block; behavioral (default false) gates
# the agent-drawn diagram (renders only when opted in); expanded (default
# false) renders the kept block open instead of collapsed. Absent file (or
# no diagrams key) keeps the P3 bytes: topology on, behavioral off,
# collapsed. Unknown sub-keys and non-boolean values refuse bad-settings.
resolve_publish_diagrams() { # no args; reads PREF_SETTINGS_FILE, sets EFFECTIVE_PUBLISH_DIAGRAMS_*
  local t="" ok="" k=""
  [[ -f "$PREF_SETTINGS_FILE" ]] || return 0
  jq -e 'has("publish")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1 || return 0
  jq -e '.publish | has("diagrams")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1 || return 0
  [[ "$(jq -r '.publish.diagrams | type' "$PREF_SETTINGS_FILE")" == "object" ]] \
    || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.diagrams must be an object (want {structural, behavioral, expanded})"
  ok=$(jq -r '.publish.diagrams | keys[]' "$PREF_SETTINGS_FILE" 2>/dev/null || true)
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    case "$k" in structural|behavioral|expanded) ;;
      *) fail "bad-settings" "settings file $PREF_SETTINGS_FILE: unknown publish.diagrams key '$k' (want only structural, behavioral, expanded)";;
    esac
  done <<<"$ok"
  if jq -e '.publish.diagrams | has("structural")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.diagrams.structural | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "boolean" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.diagrams.structural must be a boolean (want true|false)"
    EFFECTIVE_PUBLISH_DIAGRAMS_STRUCTURAL=$(jq -r '.publish.diagrams.structural' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish.diagrams | has("behavioral")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.diagrams.behavioral | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "boolean" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.diagrams.behavioral must be a boolean (want true|false)"
    EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL=$(jq -r '.publish.diagrams.behavioral' "$PREF_SETTINGS_FILE")
  fi
  if jq -e '.publish.diagrams | has("expanded")' "$PREF_SETTINGS_FILE" >/dev/null 2>&1; then
    t=$(jq -r '.publish.diagrams.expanded | type' "$PREF_SETTINGS_FILE")
    [[ "$t" == "boolean" ]] \
      || fail "bad-settings" "settings file $PREF_SETTINGS_FILE: publish.diagrams.expanded must be a boolean (want true|false)"
    EFFECTIVE_PUBLISH_DIAGRAMS_EXPANDED=$(jq -r '.publish.diagrams.expanded' "$PREF_SETTINGS_FILE")
  fi
}

# Section overrides (P5): operator-owned per-block template fragments.
# Path choice (one line): .review/overrides/ — bundle-adjacent and operator-owned
# like .review/panel, .review/budget and .review/kb.jsonl (repo-local, committable,
# check-tree-ignored), NOT the skill install tree (prompts/ and skill dirs stay
# tool-owned) nor XDG-global settings (machine-scoped, not per-repo style).
# One fragment per overridable block, stable names: preamble.md (report clean-review
# preamble prose), footer-order.md (receipt field order, subset), walkthrough-
# threshold.md (overview show thresholds), attribution.md (attribution sentence).
# Locked (refuse typed bad-override naming the invariant): marker-first-line,
# findings-structure, degenerate-guard, metadata-presence, divider-shape — plus the
# open-block rules footer-order-subset and attribution-single-line. Unknown
# placeholders refuse typed bad-settings via the extended P1b validator above
# (single mechanism). Asymmetry vs garnish is intentional: basis/evidence/diagram
# mismatches warn-and-keep (agent prose, keep-biased), but overrides are explicit
# operator intent — a stale fragment failing silent into defaults would surprise,
# so every override violation refuses loudly instead.
OVERRIDES_DIR=".review/overrides"
OVERRIDE_PREAMBLE="$OVERRIDES_DIR/preamble.md"
OVERRIDE_FOOTER_ORDER="$OVERRIDES_DIR/footer-order.md"
OVERRIDE_THRESHOLD="$OVERRIDES_DIR/walkthrough-threshold.md"
OVERRIDE_ATTRIBUTION="$OVERRIDES_DIR/attribution.md"

# Locked-invariant scan over one fragment's raw text. Fails closed bad-override
# naming the invariant — never warn-and-keep (see the asymmetry note above).
override_check_locked() { # $1 = path, $2 = raw text
  local path="$1" text="$2"
  if grep -qE '^> ✅|^> ℹ️|^> \[!IMPORTANT\]|^> \[!CAUTION\]' <<<"$text"; then
    fail "bad-override" "override $path violates locked invariant 'marker-first-line' (the verdict marker stays the body first line — fragments never carry it)"
  fi
  if grep -qE '^### ' <<<"$text"; then
    fail "bad-override" "override $path violates locked invariant 'findings-structure' (body ### sections stay agent-owned — fragments never add them)"
  fi
  if grep -qE '<!--[[:space:]]*report-machine' <<<"$text"; then
    fail "bad-override" "override $path violates locked invariant 'metadata-presence' (machine metadata comments stay script-owned — fragments never carry them)"
  fi
  # Bare-substring by design: the Review metadata comment wraps its keyword
  # onto its own line, so anchoring on '<!--' would miss the real block and
  # fail open. A loud refusal of edge-case prose is the safe direction.
  if grep -qF 'Review metadata' <<<"$text"; then
    fail "bad-override" "override $path violates locked invariant 'metadata-presence' (machine metadata comments stay script-owned — fragments never name them)"
  fi
  if grep -qE '<!--[[:space:]]*verdict-' <<<"$text"; then
    fail "bad-override" "override $path violates locked invariant 'divider-shape' (machine dividers stay byte-exact — fragments never carry them)"
  fi
}

# Footer receipt order (subset, never new): one field per line from
# tier|cost|files|link, unique, non-empty; # comments and blanks skipped.
# Sets OV_FOOTER_ORDER (space-separated). Fails closed bad-override.
override_footer_order() { # no args; sets OV_FOOTER_ORDER
  local line="" seen=""
  OV_FOOTER_ORDER="tier cost files link"
  [[ -f "$OVERRIDE_FOOTER_ORDER" ]] || return 0
  OV_FOOTER_ORDER=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=$(printf '%s' "$line" | tr -d '\r' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -z "$line" ]] && continue
    case "$line" in \#*) continue;; esac
    case "$line" in tier|cost|files|link) ;;
      *) fail "bad-override" "override $OVERRIDE_FOOTER_ORDER violates 'footer-order-subset' (unknown field '$line' — want only tier, cost, files, link)";;
    esac
    case " $seen " in *" $line "*) fail "bad-override" "override $OVERRIDE_FOOTER_ORDER violates 'footer-order-subset' (duplicate field '$line' — list each field once)";; esac
    seen="$seen $line"
    OV_FOOTER_ORDER="${OV_FOOTER_ORDER:+$OV_FOOTER_ORDER }$line"
  done < "$OVERRIDE_FOOTER_ORDER"
  [[ -n "$OV_FOOTER_ORDER" ]] \
    || fail "bad-override" "override $OVERRIDE_FOOTER_ORDER violates 'footer-order-subset' (no fields listed — want a non-empty subset of tier, cost, files, link)"
}

# Walkthrough (overview show) thresholds. Lines `files: N` / `bytes: N` (`=` works
# too), # comments and blanks skipped; absent keys read defaults (8 files, 24576
# bytes). Values must be non-negative integers or the show/hide guard cannot be
# satisfied deterministically. Sets OV_THRESH_FILES / OV_THRESH_BYTES.
# Fails closed bad-override naming the degenerate-guard invariant.
override_thresholds() { # no args; sets OV_THRESH_FILES / OV_THRESH_BYTES
  local line="" flat="" key="" val="" seen_files="false" seen_bytes="false"
  OV_THRESH_FILES="8"; OV_THRESH_BYTES="24576"
  [[ -f "$OVERRIDE_THRESHOLD" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=$(printf '%s' "$line" | tr -d '\r' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -z "$line" ]] && continue
    case "$line" in \#*) continue;; esac
    flat=$(printf '%s' "$line" | tr -d '[:space:]' | tr ':' '=')
    case "$flat" in
      files=*) key="files"; val="${flat#files=}";;
      bytes=*) key="bytes"; val="${flat#bytes=}";;
      *) fail "bad-override" "override $OVERRIDE_THRESHOLD violates locked invariant 'degenerate-guard' (unknown key in '$line' — want only 'files: N' and 'bytes: N')";;
    esac
    case "$val" in ''|*[!0-9]*) fail "bad-override" "override $OVERRIDE_THRESHOLD violates locked invariant 'degenerate-guard' (value '$val' for '$key' is not a non-negative integer — thresholds must stay satisfiable)";; esac
    # 15 digits max: bash-3.2 arithmetic wraps past 64-bit, silently inverting
    # huge-means-never-show into always-show. Fail closed, same invariant.
    if (( ${#val} > 15 )); then fail "bad-override" "override $OVERRIDE_THRESHOLD violates locked invariant 'degenerate-guard' (value '$val' for '$key' exceeds 15 digits — thresholds must fit 64-bit arithmetic)"; fi
    if [[ "$key" == "files" ]]; then
      [[ "$seen_files" == "true" ]] && fail "bad-override" "override $OVERRIDE_THRESHOLD violates locked invariant 'degenerate-guard' (duplicate '$key' — list each key once)"
      seen_files="true"; OV_THRESH_FILES="$val"
    else
      [[ "$seen_bytes" == "true" ]] && fail "bad-override" "override $OVERRIDE_THRESHOLD violates locked invariant 'degenerate-guard' (duplicate '$key' — list each key once)"
      seen_bytes="true"; OV_THRESH_BYTES="$val"
    fi
  done < "$OVERRIDE_THRESHOLD"
}

# Attribution sentence fragment. Single line (the footer receipt line stays one
# line); empty reads omit-the-sentence (receipt kept). Placeholders come from the
# v1 inventory. Echoes the raw text (substitution happens at render time with
# bundle values so check-tree validation needs no bundle).
override_attribution_text() { # no args; echoes raw fragment text or nothing
  local text=""
  [[ -f "$OVERRIDE_ATTRIBUTION" ]] || return 0
  text=$(tr -d '\r' < "$OVERRIDE_ATTRIBUTION")
  if [[ "$(printf '%s' "$text" | wc -l | tr -d ' ')" != "0" ]]; then
    fail "bad-override" "override $OVERRIDE_ATTRIBUTION violates 'attribution-single-line' (want one sentence on one line)"
  fi
  printf '%s' "$text"
}

# Preamble prose fragment. Multi-line prose allowed (it replaces the clean-review
# preamble heading); empty reads omit-the-heading (bullets kept). Echoes raw text.
override_preamble_text() { # no args; echoes raw fragment text or nothing
  [[ -f "$OVERRIDE_PREAMBLE" ]] || return 0
  tr -d '\r' < "$OVERRIDE_PREAMBLE"
}

# Authoring-time (check-tree) plus post-time (post/record/report) override gate.
# Cheap: locked-block integrity plus guard satisfiability plus placeholder inventory
# over the raw fragment text — no re-render. Absent dir or absent fragments pass
# silently (parity: no overrides reads today's bytes). Every violation refuses
# loudly typed (bad-override, or bad-settings for unknown placeholders).
validate_overrides() { # no args; fails closed on any violation
  local f="" text=""
  for f in "$OVERRIDE_PREAMBLE" "$OVERRIDE_FOOTER_ORDER" "$OVERRIDE_THRESHOLD" "$OVERRIDE_ATTRIBUTION"; do
    [[ -f "$f" ]] || continue
    [[ -r "$f" ]] || fail "bad-override" "override $f is unreadable (fix permissions or remove it)"
    text=$(tr -d '\r' < "$f")
    override_check_locked "$f" "$text"
    if [[ "$f" == "$OVERRIDE_PREAMBLE" || "$f" == "$OVERRIDE_ATTRIBUTION" ]]; then
      validate_attribution_placeholders "$text" "override $f"
    fi
  done
  override_footer_order
  override_thresholds
  if [[ -f "$OVERRIDE_ATTRIBUTION" ]]; then override_attribution_text >/dev/null; fi
}

# Bundle values for the v1 inventory. Never blank: unknown reads unrecorded/dirty.
override_head_short() { # $1 = bundle dir; echoes 7-char head, dirty, or unknown
  local hs="" dirty=""
  if [[ -f "$1/PR.json" ]]; then hs=$(pr_field "$1" head_sha)
  elif [[ -f "$1/source.json" ]]; then
    hs=$(jq -r '.head_sha // empty' "$1/source.json" 2>/dev/null || true)
    dirty=$(jq -r '.dirty // false' "$1/source.json" 2>/dev/null || true)
  fi
  if [[ -z "$hs" ]]; then
    if [[ "$dirty" == "true" ]]; then printf 'dirty'; else printf 'unknown'; fi
  else printf '%s' "${hs:0:7}"; fi
}

override_model() { # $1 = bundle dir; echoes first cost model name, REVIEW_MODEL, or unrecorded
  local m=""
  if [[ -f "$1/cost.json" ]]; then
    m=$(jq -r 'if type == "array" then (.[0].models[0].name // empty) else (.models[0].name // empty) end' "$1/cost.json" 2>/dev/null || true)
  fi
  [[ -z "$m" && -n "${REVIEW_MODEL:-}" ]] && m="$REVIEW_MODEL"
  [[ -z "$m" ]] && m="unrecorded"
  printf '%s' "$m"
}

# Placeholder substitution for fragments (and the P1b attribution text — same
# mechanism, extended inventory). $1 = raw text, $2 = bundle dir. {attribution}
# always means the default sentence (with version/url resolved), so a fragment
# naming it can never recurse into itself.
substitute_override_placeholders() { # $1 = text, $2 = dir; echoes substituted text
  local text="$1" dir="$2" ver="" files="" head="" model="" base_attr=""
  ver=$(tool_version)
  publish_spend "$dir"
  base_attr="Reviewed with [**Verdict {version}**](${PRODUCT_URL})"
  base_attr=${base_attr//\{version\}/${ver}}
  base_attr=${base_attr//\{product_url\}/${PRODUCT_URL}}
  text=${text//\{attribution\}/${base_attr}}
  text=${text//\{version\}/${ver}}
  text=${text//\{tier\}/${PUBLISH_TIER}}
  text=${text//\{product_url\}/${PRODUCT_URL}}
  text=${text//\{cost\}/${PUBLISH_COST}}
  files=$(publish_file_count "$dir")
  text=${text//\{files_reviewed\}/${files}}
  head=$(override_head_short "$dir")
  text=${text//\{head_sha_short\}/${head}}
  model=$(override_model "$dir")
  text=${text//\{model\}/${model}}
  printf '%s' "$text"
}
_FD9_READY="false"
# Peek --json early (stopping at a `--` paths separator) so typed prefs
# errors land in the envelope when the caller asked for JSON. _JSON_PEEKED
# remembers the flag so the file/env sync below never clobbers an explicit
# flag (flags win over file over env); verbs re-assert --json afterwards.
_JSON_PEEKED="false"
if (( $# > 0 )); then
  for _a in "$@"; do
    [[ "$_a" == "--" ]] && break
    if [[ "$_a" == "--json" ]]; then json_mode="true"; _JSON_PEEKED="true"; fi
  done
  unset _a
fi
json_redirect
resolve_prefs
# Global presentation flags (flags win over file over env). Stripped here so
# every verb parser stays untouched; --json itself stays per-verb (it forces
# json and therefore already wins over a text file/env default).
_stripped=(); _seen_dd="false"
while [[ $# -gt 0 ]]; do
  if [[ "$_seen_dd" == "true" ]]; then _stripped+=("$1"); shift; continue; fi
  case "$1" in
    --) _seen_dd="true"; _stripped+=("$1"); shift;;
    --color)
      [[ -n "${2:-}" ]] || { echo "unknown flag '$1' for $cmd (want --color <auto|always|never>)" >&2; exit 2; }
      case "${2:-}" in
        auto|always|never) FLAG_COLOR_FORCE="${2:-}";;
        *) fail "bad-color" "unknown --color '${2:-}' (want auto|always|never)";;
      esac
      shift 2;;
    --color=*)
      _cvm="${1#--color=}"
      case "$_cvm" in
        auto|always|never) FLAG_COLOR_FORCE="$_cvm";;
        *) fail "bad-color" "unknown --color '$_cvm' (want auto|always|never)";;
      esac
      shift;;
    --compact) FLAG_COMPACT_FORCE="true"; shift;;
    --no-compact) FLAG_COMPACT_FORCE="false"; shift;;
    --no-json) FLAG_OUTPUT_FORCE="text"; shift;;
    --footer) FLAG_PUBLISH_FOOTER_FORCE="true"; shift;;
    --no-footer) FLAG_PUBLISH_FOOTER_FORCE="false"; shift;;
    --overview) FLAG_PUBLISH_OVERVIEW_FORCE="true"; shift;;
    --no-overview) FLAG_PUBLISH_OVERVIEW_FORCE="false"; shift;;
    --diagram-behavioral) FLAG_PUBLISH_DIAGRAMS_BEHAVIORAL_FORCE="true"; shift;;
    --no-diagram-behavioral) FLAG_PUBLISH_DIAGRAMS_BEHAVIORAL_FORCE="false"; shift;;
    --diagram-structural) FLAG_PUBLISH_DIAGRAMS_STRUCTURAL_FORCE="true"; shift;;
    --no-diagram-structural) FLAG_PUBLISH_DIAGRAMS_STRUCTURAL_FORCE="false"; shift;;
    --diagram-expanded) FLAG_PUBLISH_DIAGRAMS_EXPANDED_FORCE="true"; shift;;
    --no-diagram-expanded) FLAG_PUBLISH_DIAGRAMS_EXPANDED_FORCE="false"; shift;;
    --attribution)
      case "${2:-}" in
        off|product|full) FLAG_PUBLISH_ATTRIBUTION_FORCE="$2"; shift 2;;
        *) FLAG_PUBLISH_ATTRIBUTION_FORCE="product"; shift;;
      esac;;
    --attribution=*)
      _av="${1#--attribution=}"
      case "$_av" in
        off|product|full) FLAG_PUBLISH_ATTRIBUTION_FORCE="$_av";;
        *) fail "bad-attribution" "unknown --attribution '$_av' (want off|product|full)";;
      esac
      shift;;
    --no-attribution) FLAG_PUBLISH_ATTRIBUTION_FORCE="off"; shift;;
    --logo) FLAG_PUBLISH_LOGO_FORCE="true"; shift;;
    --no-logo) FLAG_PUBLISH_LOGO_FORCE="false"; shift;;
    --logo-url)
      [[ $# -ge 2 ]] || { echo "unknown flag '$1' for $cmd (want --logo-url <url>)" >&2; exit 2; }
      FLAG_PUBLISH_LOGO_URL_FORCE="$2"; FLAG_PUBLISH_LOGO_URL_SET="true"; shift 2;;
    --logo-url=*)
      FLAG_PUBLISH_LOGO_URL_FORCE="${1#--logo-url=}"; FLAG_PUBLISH_LOGO_URL_SET="true"; shift;;
    --attribution-text)
      [[ $# -ge 2 ]] || { echo "unknown flag '$1' for $cmd (want --attribution-text <text>)" >&2; exit 2; }
      FLAG_PUBLISH_ATTRIBUTION_TEXT_FORCE="$2"; FLAG_PUBLISH_ATTRIBUTION_TEXT_SET="true"; shift 2;;
    --attribution-text=*)
      FLAG_PUBLISH_ATTRIBUTION_TEXT_FORCE="${1#--attribution-text=}"; FLAG_PUBLISH_ATTRIBUTION_TEXT_SET="true"; shift;;
    --copyright)
      [[ $# -ge 2 ]] || { echo "unknown flag '$1' for $cmd (want --copyright <text>)" >&2; exit 2; }
      FLAG_PUBLISH_COPYRIGHT_FORCE="$2"; FLAG_PUBLISH_COPYRIGHT_SET="true"; shift 2;;
    --copyright=*)
      FLAG_PUBLISH_COPYRIGHT_FORCE="${1#--copyright=}"; FLAG_PUBLISH_COPYRIGHT_SET="true"; shift;;
    *) _stripped+=("$1"); shift;;
  esac
done
if (( ${#_stripped[@]} > 0 )); then set -- "${_stripped[@]}"; else set --; fi
unset _stripped _seen_dd _cvm _av
[[ -n "$FLAG_OUTPUT_FORCE" ]] && EFFECTIVE_OUTPUT="$FLAG_OUTPUT_FORCE"
[[ -n "$FLAG_COMPACT_FORCE" ]] && EFFECTIVE_COMPACT="$FLAG_COMPACT_FORCE"
[[ -n "$FLAG_COLOR_FORCE" ]] && EFFECTIVE_COLOR="$FLAG_COLOR_FORCE"
[[ -n "$FLAG_PUBLISH_FOOTER_FORCE" ]] && EFFECTIVE_PUBLISH_FOOTER="$FLAG_PUBLISH_FOOTER_FORCE"
[[ -n "$FLAG_PUBLISH_ATTRIBUTION_FORCE" ]] && EFFECTIVE_PUBLISH_ATTRIBUTION="$FLAG_PUBLISH_ATTRIBUTION_FORCE"
[[ "$FLAG_PUBLISH_ATTRIBUTION_TEXT_SET" == "true" ]] && EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT="$FLAG_PUBLISH_ATTRIBUTION_TEXT_FORCE"
[[ "$FLAG_PUBLISH_COPYRIGHT_SET" == "true" ]] && EFFECTIVE_PUBLISH_COPYRIGHT="$FLAG_PUBLISH_COPYRIGHT_FORCE"
[[ -n "$FLAG_PUBLISH_LOGO_FORCE" ]] && EFFECTIVE_PUBLISH_LOGO="$FLAG_PUBLISH_LOGO_FORCE"
[[ "$FLAG_PUBLISH_LOGO_URL_SET" == "true" ]] && EFFECTIVE_PUBLISH_LOGO_URL="$FLAG_PUBLISH_LOGO_URL_FORCE"
[[ -n "$FLAG_PUBLISH_OVERVIEW_FORCE" ]] && EFFECTIVE_PUBLISH_OVERVIEW="$FLAG_PUBLISH_OVERVIEW_FORCE"
[[ -n "$FLAG_PUBLISH_DIAGRAMS_STRUCTURAL_FORCE" ]] && EFFECTIVE_PUBLISH_DIAGRAMS_STRUCTURAL="$FLAG_PUBLISH_DIAGRAMS_STRUCTURAL_FORCE"
[[ -n "$FLAG_PUBLISH_DIAGRAMS_BEHAVIORAL_FORCE" ]] && EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL="$FLAG_PUBLISH_DIAGRAMS_BEHAVIORAL_FORCE"
[[ -n "$FLAG_PUBLISH_DIAGRAMS_EXPANDED_FORCE" ]] && EFFECTIVE_PUBLISH_DIAGRAMS_EXPANDED="$FLAG_PUBLISH_DIAGRAMS_EXPANDED_FORCE"
# Flags land after the file/env layers, so validate the winning text here —
# a bad flag value fails typed instead of rendering literally.
validate_attribution_placeholders "$EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT" "publish.attribution_text"
# NO_COLOR applies only when color is otherwise unset, so the documented
# flag > file > env > defaults order holds (an explicit setting anywhere
# beats the convention; an explicit --color=always flag beats NO_COLOR).
if [[ -n "${NO_COLOR:-}" && "$FLAG_COLOR_FORCE" != "always" && -z "${REVIEW_COLOR_MODE:-}" && "$PREF_COLOR_SET" != "true" ]]; then
  EFFECTIVE_COLOR="never"
fi
if [[ "$FLAG_OUTPUT_FORCE" == "text" ]]; then json_mode="false";
elif [[ "$EFFECTIVE_OUTPUT" == "json" ]]; then json_mode="true"; fi
json_redirect

# stable signature: case-sensitive, whitespace-squashed, line-number-free (caller excludes lines).
sig_of() { printf '%s' "$1" | tr -s '[:space:]' ' ' | sed -e 's/^ //' -e 's/ $//' | shasum -a 1 | cut -d' ' -f1; }

# resolve a sig prefix (agents copy short ids from the printed table) to a unique
# full sig. Echoes the full sig, or the sentinel NONE/MANY — pure function, no warn
# (warns inside $() subshells are lost to the parent; callers warn same-process).
resolve_sig() {
  jq -r --arg s "$2" '[.[] | select(.sig | startswith($s))] | if length == 1 then .[0].sig elif length == 0 then "NONE" else "MANY" end' <<<"$1"
}

# newline-separated input -> JSON string array ([] when empty).
to_json_array() {
  if [[ -z "${1:-}" ]]; then echo "[]"; else printf '%s' "$1" | jq -R . | jq -s .; fi
}

# Publish footer + confidence basis (P1) with leveled attribution (P1b).
# One merged block at the body end, computed from owned store data only —
# never blank, never fails on absent data. Degraded reads stay factual:
# unknown version reads "unknown", unknown spend reads "unrecorded",
# unknown files read "files unrecorded", and the review link renders only
# when a completed post record carries it (never invented pre-post).
tool_version() {
  local v=""
  v=$(jq -r .version "$(dirname "${BASH_SOURCE[0]:-$0}")/../package.json" 2>/dev/null || true)
  [[ -z "$v" || "$v" == "null" ]] && v="unknown"
  printf '%s' "$v"
}

# Spend rollup for the footer. Sets PUBLISH_TIER (latest cost record effort,
# normalized, default standard) and PUBLISH_COST ("$n" summed est_usd, or
# "unrecorded" when no record prices the run). Absent/unreadable cost.json
# degrades silently — the footer is garnish, the rollup stays in report --cost.
publish_spend() { # $1 = dir
  local rec="" tier="" sum=""
  PUBLISH_TIER="standard"; PUBLISH_COST="unrecorded"
  [[ -f "$1/cost.json" ]] || return 0
  rec=$(jq -c 'if type == "array" then (.[-1] // {}) else . end' "$1/cost.json" 2>/dev/null || true)
  [[ -z "$rec" ]] && return 0
  tier=$(jq -r '((.effort // "standard" | tostring | ascii_downcase))' <<<"$rec" 2>/dev/null || true)
  case "$tier" in cheap|standard|deep) PUBLISH_TIER="$tier";; esac
  sum=$(jq -r '[(if type == "array" then .[] else . end) | .est_usd | numbers] | if length == 0 then empty else ((add * 10000 | round) / 10000 | tostring) end' "$1/cost.json" 2>/dev/null || true)
  [[ -n "$sum" ]] && PUBLISH_COST="\$$sum"
}

publish_file_count() { # $1 = dir; echoes "N file(s)" or "files unrecorded" — never blank
  local n=""
  if [[ -f "$1/PR.json" ]]; then
    n=$(jq -r '(.files // null) | if type == "array" then (length | tostring) else empty end' "$1/PR.json" 2>/dev/null || true)
  fi
  if [[ -z "$n" && -f "$1/full.diff" ]]; then
    n=$(grep -c '^diff --git ' "$1/full.diff" 2>/dev/null || true)
  fi
  case "$n" in
    ''|*[!0-9]*) printf 'files unrecorded';;
    1) printf '1 file';;
    *) printf '%s files' "$n";;
  esac
}

publish_review_link() { # $1 = dir; echoes "" (unknown) or a factual review ref — never invented
  local id="" url="" posted="" current=""
  [[ -f "$1/post.json" ]] || return 0
  id=$(jq -r '.review.id // empty' "$1/post.json" 2>/dev/null || true)
  url=$(jq -r '.review.html_url // empty' "$1/post.json" 2>/dev/null || true)
  [[ -z "$id" && -z "$url" ]] && return 0
  [[ -z "$id" ]] && id="posted"
  # Same-head repost (07): a post.json head matching the bundle HEAD means the
  # stored id belongs to the review being superseded (the imminent post mints a
  # fresh one), so omit the link instead of rendering the stale id. A differing
  # head keeps today's link; an unreadable head keeps it too (never wedge).
  posted=$(jq -r '.head // empty' "$1/post.json" 2>/dev/null || true)
  if [[ -n "$posted" ]]; then
    if [[ -f "$1/PR.json" ]]; then current=$(pr_field "$1" head_sha)
    # source.json branch is defensive-only: post refuses local bundles, so only
    # the PR.json path resolves in practice. Kept so any future local-post path
    # degrades the same way instead of linking a stale id.
    elif [[ -f "$1/source.json" ]]; then current=$(jq -r '.head_sha // empty' "$1/source.json" 2>/dev/null || true); fi
    [[ -n "$current" && "$posted" == "$current" ]] && return 0
  fi
  if [[ -n "$url" ]]; then printf '[review `%s`](%s)' "$id" "$url"; else printf 'review `%s`' "$id"; fi
}

render_footer() { # $1 = dir; echoes the merged footer block, or nothing when publish.footer is off
  local receipt="" attr="" logo="" maker="" seg="" segtext=""
  [[ "${EFFECTIVE_PUBLISH_FOOTER:-true}" == "true" ]] || return 0
  publish_spend "$1"
  # NOTE (P1b): the version stays inline and space-separated
  # (Verdict 0.2.0) — never wrapped in brackets or parens. Brackets would
  # collide with the surrounding markdown link text; parens read as an
  # aside. Keep it that way.
  attr="${EFFECTIVE_PUBLISH_ATTRIBUTION_TEXT:-}"
  [[ -z "$attr" ]] && attr="Reviewed with [**Verdict {version}**](${PRODUCT_URL})"
  # P5: the attribution fragment wins over the settings file/env/default above,
  # but a per-run --attribution-text flag still wins over the fragment.
  if [[ -f "$OVERRIDE_ATTRIBUTION" && "$FLAG_PUBLISH_ATTRIBUTION_TEXT_SET" != "true" ]]; then
    attr=$(override_attribution_text)
  fi
  attr=$(substitute_override_placeholders "$attr" "$1")
  # P5: receipt segments join in fragment order (default today's order); unknown
  # links and dropped fields are skipped, never blank — an all-dropped order
  # degrades to unrecorded. The sentence and copyright lines stay pinned so the
  # machine block keeps a stable shape the divider strip-replace can round-trip.
  override_footer_order
  receipt=""
  # shellcheck disable=SC2086 # OV_FOOTER_ORDER is a validated space-separated field list
  for seg in $OV_FOOTER_ORDER; do
    segtext=""
    case "$seg" in
      tier) segtext="$PUBLISH_TIER tier";;
      cost) segtext="cost $PUBLISH_COST";;
      files) segtext=$(publish_file_count "$1");;
      link) segtext=$(publish_review_link "$1");;
    esac
    [[ -n "$segtext" ]] && receipt="${receipt:+$receipt · }$segtext"
  done
  [[ -z "$receipt" ]] && receipt="unrecorded"
  echo "---"
  echo ""
  echo "$FOOTER_DIVIDER"
  # P5: an empty attribution fragment omits the sentence (receipt kept, same as off).
  if [[ "${EFFECTIVE_PUBLISH_ATTRIBUTION:-product}" != "off" && -n "$attr" ]]; then
    logo=""
    if [[ "${EFFECTIVE_PUBLISH_LOGO:-true}" == "true" && -n "${EFFECTIVE_PUBLISH_LOGO_URL:-}" ]]; then
      logo="<a href=\"${PRODUCT_URL}\"><img src=\"${EFFECTIVE_PUBLISH_LOGO_URL}\" alt=\"Verdict\" width=\"18\" height=\"18\"></a> "
    fi
    maker=""
    [[ "${EFFECTIVE_PUBLISH_ATTRIBUTION}" == "full" ]] && maker=" by [${MAKER_NAME}](${MAKER_URL})"
    echo "> ${logo}${attr}${maker} · $receipt"
  else
    echo "> $receipt"
  fi
  if [[ -n "${EFFECTIVE_PUBLISH_COPYRIGHT:-}" ]]; then
    echo "> ${EFFECTIVE_PUBLISH_COPYRIGHT}"
  fi
}

# Strip a previously rendered machine footer (divider line through body end)
# so re-rendering the same body is byte-identical. Only the machine marker
# is stripped — agent prose, including hand-written legacy receipts, passes
# through untouched. The separator render_footer emits with the block (one
# trailing "---" plus surrounding blank lines) goes with it.
strip_rendered_footer() { # stdin body -> stdout body without the machine footer
  awk -v marker="$FOOTER_DIVIDER" '
    $0 == marker { cut = 1; next }
    cut { next }
    { lines[++n] = $0 }
    END {
      while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--
      if (n > 0 && lines[n] == "---") n--
      while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--
      for (i = 1; i <= n; i++) print lines[i]
    }'
}

# Change overview (P3): script-computed collapsed file table + change-topology
# flowchart for large diffs. Zero tokens, zero hallucination surface: every
# cell and edge derives from owned bundle data only (file list + deltas from
# PR.json/diff headers, hunk counts from hunks.tsv, roles from roles.txt;
# edges only where an added diff line names another changed file — unknown
# relations are omitted, never invented). Model one-liners are explicitly out.
# Pinned thresholds: shown collapsed when files > 8 OR full.diff > 24576
# bytes (24KB); omitted below. publish.overview (bool, default true) gates the
# feature; --overview forces show and --no-overview forces hide (flags win
# over file over env). Placement is report --output only, above the footer
# inside the content: posted agent bodies stay untouched (agent-contract
# changes are P5's domain; a second script-written copy would fork one truth
# into two and need footer-style strip-replace to stay idempotent).
overview_role() { # $1 = path, $2 = roles-map file (path<TAB>role); echoes the role, never blank
  local p="$1" map="$2" r=""
  if [[ -f "$map" ]]; then
    r=$(awk -F'\t' -v f="$p" '$1 == f { print $2; exit }' "$map" 2>/dev/null || true)
  fi
  if [[ -z "$r" ]]; then
    r=$(printf '%s\n' "$p" | roles_for_paths 2>/dev/null | awk '/^## /{ print $2; exit }' || true)
  fi
  [[ -n "$r" ]] && printf '%s' "$r" || printf 'source'
}

overview_hunks() { # $1 = path, $2 = dir, $3 = hunks-scan awk; echoes NEW-side hunk count
  local p="$1" dir="$2" prog="$3" n=""
  if [[ -f "$dir/hunks.tsv" ]]; then
    n=$(awk -F'\t' -v f="$p" '$1 == f && $2 == "NEW" { c++ } END { print c + 0 }' "$dir/hunks.tsv" 2>/dev/null || true)
  elif [[ -f "$dir/full.diff" ]]; then
    n=$(awk -v mode=hunks -v f="$p" -f "$prog" "$dir/full.diff" 2>/dev/null | grep -xcF -- "$p" || true)
  fi
  case "$n" in ''|*[!0-9]*) printf '0';; *) printf '%s' "$n";; esac
}

render_overview() { # $1 = dir, $2 = slug; echoes the collapsed overview, or nothing when gated off
  local dir="$1" slug="$2" force="${FLAG_PUBLISH_OVERVIEW_FORCE:-}"
  local ov_files="0" ov_bytes="0" tmp="" prname=""
  if [[ "$force" == "false" ]]; then return 0; fi
  if [[ "$force" != "true" ]]; then
    [[ "${EFFECTIVE_PUBLISH_OVERVIEW:-true}" == "true" ]] || return 0
    # Pinned P3 thresholds: more than 8 files, or a diff over 24576 bytes (24KB).
    # P5: the walkthrough-threshold fragment overrides both numbers (validated).
    override_thresholds
    if [[ -f "$dir/PR.json" ]]; then
      ov_files=$(jq -r '(.files // []) | length' "$dir/PR.json" 2>/dev/null || echo "0")
    fi
    case "$ov_files" in ''|*[!0-9]*) ov_files="0";; esac
    if [[ "$ov_files" == "0" && -f "$dir/full.diff" ]]; then
      ov_files=$(grep -c '^diff --git ' "$dir/full.diff" 2>/dev/null || true)
    fi
    case "$ov_files" in ''|*[!0-9]*) ov_files="0";; esac
    if [[ -f "$dir/full.diff" ]]; then
      ov_bytes=$(wc -c < "$dir/full.diff" 2>/dev/null | tr -d ' ' || echo "0")
    fi
    case "$ov_bytes" in ''|*[!0-9]*) ov_bytes="0";; esac
    if (( 10#$ov_files <= 10#$OV_THRESH_FILES )) && (( 10#$ov_bytes <= 10#$OV_THRESH_BYTES )); then return 0; fi
  fi
  tmp=$(mktemp -d "${TMPDIR:-/tmp}/verdict-overview.XXXXXX") || return 0
  # One shared diff scanner (POSIX awk): header parse mirrors the fetch-time
  # hunk indexer (post-image path wins, quoted headers unquoted the same way);
  # modes: files (ordered unique paths + added/removed line counts), added
  # (path<TAB>added-line, +++ excluded), hunks (one path line per @@ header).
  cat > "$tmp/scan.awk" <<'AWK'
function setcur(hdr,   t, i1, t2, i2) {
  cur = "";
  if (substr(hdr, 1, 1) == "\"") {
    t = substr(hdr, 2); i1 = index(t, "\""); t2 = substr(t, i1 + 1); i2 = index(t2, "\"");
    cur = substr(t2, i2 + 1); sub(/"$/, "", cur); sub(/^b\//, "", cur);
  } else {
    i1 = index(hdr, " b/");
    if (i1 > 0) cur = substr(hdr, i1 + 3);
    else { i1 = index(hdr, " "); cur = substr(hdr, i1 + 1); sub(/^b\//, "", cur); }
  }
}
/^diff --git / { hdr = $0; sub(/^diff --git /, "", hdr); setcur(hdr);
  if (mode == "files" && cur != "" && !(cur in seen)) { seen[cur] = 1; order[++m] = cur }
  next }
mode == "added" && substr($0, 1, 1) == "+" && $0 !~ /^\+\+\+/ { if (cur != "") print cur "\t" substr($0, 2); next }
mode == "files" && substr($0, 1, 1) == "+" && $0 !~ /^\+\+\+/ { if (cur != "") adds[cur]++; next }
mode == "files" && substr($0, 1, 1) == "-" && $0 !~ /^---/ { if (cur != "") dels[cur]++; next }
mode == "hunks" && /^@@ / { if (cur != "") print cur; next }
END { if (mode == "files") for (i = 1; i <= m; i++) printf "%s\t%d\t%d\n", order[i], adds[order[i]] + 0, dels[order[i]] + 0 }
AWK
  # Markdown + mermaid generator (POSIX awk): table rows in file order with
  # stable F1..Fn ids (reserved for future prose annotation — the table
  # renders byte-identically with or without it); edges only for added-line
  # mentions of another changed file (basename, or full path when basenames
  # collide — ambiguous bare mentions are omitted, never guessed). A
  # test/config mentioner reads as validates, any other mentioner as uses.
  # Both relations render as labeled solid arrows: GitHub renders the core
  # flowchart subset only, so the relation rides in the label text rather
  # than in dotted-line syntax the renderer may drop.
  cat > "$tmp/gen.awk" <<'AWK'
function san(s) { gsub(/"/, "'", s); gsub(/\|/, ":", s); gsub(/`/, "'", s); return s }
BEGIN { FS = "\t" }
NR == FNR { n++; fid[n] = $1; fp[n] = $2; fa[n] = $3 + 0; fd[n] = $4 + 0; fh[n] = $5; fr[n] = $6;
  fb[n] = $2; sub(/.*\//, "", fb[n]); freq[fb[n]]++; fidx[$2] = n; next }
{ cur = fidx[$1]; if (cur == 0) next;
  line = substr($0, length($1) + 2);
  for (j = 1; j <= n; j++) { if (j == cur) continue; hit = 0;
    if (freq[fb[j]] == 1 && index(line, fb[j]) > 0) hit = 1;
    if (!hit && index(line, fp[j]) > 0) hit = 1;
    if (hit) { key = cur SUBSEP j;
      if (!(key in edge)) edge[key] = ((fr[cur] == "test" || fr[cur] == "config") ? "validates " : "uses ") (freq[fb[j]] > 1 ? fp[j] : fb[j]) } } }
END {
  ta = 0; td = 0; for (i = 1; i <= n; i++) { ta += fa[i]; td += fd[i] }
  print "<details>"; print "<summary>Files changed (" n ")</summary>"; print "";
  print "| # | File | +/- | Hunks | Role |"; print "|---|---|---|---|---|";
  for (i = 1; i <= n; i++) printf "| %s | `%s` | +%d/-%d | %s | %s |\n", fid[i], san(fp[i]), fa[i], fd[i], fh[i], fr[i];
  print ""; print "</details>"; print "";
  if (topology) {
  print "<details>"; print "<summary>Change topology</summary>"; print "";
  print "```mermaid"; print "flowchart TD";
  printf "    PR[\"%s +%d/-%d\"]\n", san(prname), ta, td;
  for (i = 1; i <= n; i++) printf "    PR --> %s[\"%s %s\"]\n", fid[i], fid[i], san(fb[i]);
  for (i = 1; i <= n; i++) for (j = 1; j <= n; j++) { key = i SUBSEP j;
    if (key in edge) printf "    %s -->|\"%s\"| %s\n", fid[i], san(edge[key]), fid[j] }
  print "```"; print ""; print "</details>" } }
AWK
  : > "$tmp/paths.tsv"
  if [[ -f "$dir/PR.json" ]] && jq -e '(.files // []) | length > 0' "$dir/PR.json" >/dev/null 2>&1; then
    jq -r '.files[] | "\(.path)\t\(.additions // 0)\t\(.deletions // 0)"' "$dir/PR.json" 2>/dev/null > "$tmp/paths.tsv" || : > "$tmp/paths.tsv"
  elif [[ -f "$dir/full.diff" ]]; then
    awk -v mode=files -f "$tmp/scan.awk" "$dir/full.diff" 2>/dev/null > "$tmp/paths.tsv" || : > "$tmp/paths.tsv"
  fi
  if [[ -f "$dir/roles.txt" ]]; then
    awk '/^## /{ r = $2 } /^- /{ p = $0; sub(/^- /, "", p); print p "\t" r }' "$dir/roles.txt" 2>/dev/null > "$tmp/roles.map" || : > "$tmp/roles.map"
  else
    # Batch fallback: one roles_for_paths pass over every path in the same map
    # shape, so the per-file fallback in overview_role stays a rare last resort
    # instead of spawning a pipeline per file.
    cut -f1 "$tmp/paths.tsv" 2>/dev/null | roles_for_paths 2>/dev/null | awk '/^## /{ r = $2 } /^- /{ p = $0; sub(/^- /, "", p); print p "\t" r }' > "$tmp/roles.map" || : > "$tmp/roles.map"
  fi
  : > "$tmp/files.tsv"
  k="0"
  while IFS="$(printf '\t')" read -r p a d; do
    [[ -z "$p" ]] && continue
    k=$(( k + 1 ))
    case "$a" in ''|*[!0-9]*) a="0";; esac
    case "$d" in ''|*[!0-9]*) d="0";; esac
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "F$k" "$p" "$a" "$d" \
      "$(overview_hunks "$p" "$dir" "$tmp/scan.awk")" "$(overview_role "$p" "$tmp/roles.map")" >> "$tmp/files.tsv"
  done < "$tmp/paths.tsv"
  : > "$tmp/added.tsv"
  if [[ -f "$dir/full.diff" ]]; then
    awk -v mode=added -f "$tmp/scan.awk" "$dir/full.diff" 2>/dev/null > "$tmp/added.tsv" || : > "$tmp/added.tsv"
  fi
  case "$slug" in ''|*[!0-9]*) prname="review $slug";; *) prname="PR #$slug";; esac
  [[ "$prname" == "review " ]] && prname="review"
  # P4 structural split: publish.diagrams.structural (default true) gates the
  # topology half; the table above always renders when the overview shows.
  ov_topology="1"
  [[ "${EFFECTIVE_PUBLISH_DIAGRAMS_STRUCTURAL:-true}" == "true" ]] || ov_topology="0"
  awk -v prname="$prname" -v topology="$ov_topology" -f "$tmp/gen.awk" "$tmp/files.tsv" "$tmp/added.tsv" 2>/dev/null || true
  rm -rf "$tmp"
}

# Basis counts from a findings array on stdin: rigor rung counts in evidence
# order, distinct cited kb rule ids, addressed (verified-fixed) findings.
basis_counts() {
  jq -r 'def cnt(x): ([.[] | select((.rigor // "pointed") == x)] | length);
    "\(cnt("reproduced")) \(cnt("ran")) \(cnt("walked")) \(cnt("pointed")) \(cnt("said")) \([.[] | (.kb // []) | if type == "array" then .[] else empty end] | unique | length) \([.[] | select(.status == "addressed")] | length)"' 2>/dev/null || echo "0 0 0 0 0 0 0"
}

basis_line() { # $1 = findings.json path; echoes the exact confidence-lead basis line
  local rep="" ran="" walked="" pointed="" said="" cited="" ver=""
  read -r rep ran walked pointed said cited ver <<<"$(basis_counts < "$1")"
  printf '> Basis: %s reproduced · %s ran · %s walked · %s pointed · %s said · %s rules cited · %s verified fixes\n' "$rep" "$ran" "$walked" "$pointed" "$said" "$cited" "$ver"
}

# Agent-path basis check: the review.json body may carry a "> Basis:" counts
# line; the review's own comments decide (not the merged store, so carried-over
# findings never warn). Mismatch or unparseable warns and keeps
# the body — garnish never fails the run. $1 = review counts, $2 = body.
validate_basis_line() {
  local line="" nums="" want_n=""
  line=$(printf '%s' "$2" | grep -E '^[[:space:]]*> Basis:' | head -n 1 || true)
  if [[ -z "$line" ]]; then
    warn "no basis line under the confidence lead — keeping body as-is (report renders it exactly)"
    return 0
  fi
  nums=$(printf '%s' "$line" | grep -o '[0-9][0-9]*' | tr '\n' ' ' | sed 's/ *$//' || true)
  want_n=$(printf '%s' "$nums" | wc -w | tr -d ' ')
  if [[ "$want_n" != "7" ]]; then
    warn "basis line carries $want_n counts, want 7 (reproduced ran walked pointed said cited verified) — keeping body as-is"
    return 0
  fi
  if [[ "$nums" != "$1" ]]; then
    warn "basis line counts ($nums) differ from this review's comments ($1) — keeping body as-is (garnish never fails the run)"
  fi
}

# Review-path basis counts: expected Basis numbers from the review's own
# comments (exactly computable from review.json at validate time), not the
# merged store — so re-reviews with carried-over open findings stop warning.
# Rigor defaults to pointed (same as the store), cites are distinct raw kb ids,
# verified is 0 on a fresh review (only this run's addressed flips via triage
# or verification.json count, scoped to this review's claims).
review_basis_counts() { # $1 = review.json path, $2 = merged entries JSON, $3 = now; echoes "a b c d e f g"
  local rf="$1" ent="$2" now_ts="$3" rc="" ver=""
  rc=$(jq -r '
    def norm: if . == "reproduced" or . == "ran" or . == "walked" or . == "pointed" or . == "said" then . else "pointed" end;
    def cnt(x): ([(.comments // [])[] | ((.rigor // "pointed") | norm) | select(. == x)] | length);
    "\(cnt("reproduced")) \(cnt("ran")) \(cnt("walked")) \(cnt("pointed")) \(cnt("said")) \([((.comments // [])[] | (.kb // []) | if type == "array" then .[] else empty end)] | unique | length)"
  ' "$rf" 2>/dev/null || echo "0 0 0 0 0 0")
  ver=$(jq -r --arg now "$now_ts" '[.[] | select(.status == "addressed" and ((.history // []) | map(select(.at == $now and (.event == "addressed" or .event == "triaged"))) | length > 0))] | length' <<<"$ent" 2>/dev/null || echo "0")
  case "$ver" in ''|*[!0-9]*) ver="0";; esac
  printf '%s %s\n' "$rc" "$ver"
}

# Permalink evidence links (P2). GitHub findings reference
# `blob/<reviewed-SHA>/<path>#Lx` links in reference style; non-GitHub and
# local bundles render plain `path:line` text (post is GitHub-only, so a
# blob URL anywhere else would 404). The SHA is the reviewed commit
# (commit_id at post, the fetched HEAD) — never live HEAD — so links are
# staleness-proof by construction. Quotes stay capped (at most 3 lines) and
# live only inside collapsed `<details>`; the visible body stays link-first.
# Garnish only, like the basis line: every mismatch warns and keeps the
# body — a review never fails over link shape.
evidence_base_url() { # $1 = dir, $2 = repo (owner/name); echoes the blob base or nothing
  local prov="" url=""
  [[ -f "$1/PR.json" ]] || return 0
  prov=$(jq -r '.provider // empty' "$1/PR.json" 2>/dev/null || true)
  [[ -z "$prov" || "$prov" == "github" ]] || return 0
  url=$(jq -r '.url // empty' "$1/PR.json" 2>/dev/null || true)
  if [[ -n "$url" ]]; then
    printf '%s' "${url%%/pull/*}"
  elif [[ -n "${2:-}" ]]; then
    printf 'https://github.com/%s' "$2"
  fi
}

# Linkable iff this bundle posts to GitHub with a known reviewed SHA:
# a PR.json with no (or github) provider plus a non-empty head SHA. Local
# and provider bundles always read false — plain `path:line` text there.
evidence_linkable() { # $1 = dir, $2 = head sha; echoes "true"/"false"
  local prov=""
  [[ -n "${2:-}" ]] || { echo "false"; return 0; }
  [[ -f "$1/PR.json" ]] || { echo "false"; return 0; }
  prov=$(jq -r '.provider // empty' "$1/PR.json" 2>/dev/null || true)
  if [[ -z "$prov" || "$prov" == "github" ]]; then echo "true"; else echo "false"; fi
}

# Agent-path check: blob SHAs in the body must name the reviewed commit, and
# code fences stay inside collapsed details. Warn-and-keep throughout.
# $1 = body, $2 = reviewed head sha ("" ok), $3 = "true" when linkable.
validate_evidence_links() {
  local body="$1" sha="$2" linkable="$3" blob_shas="" s=""
  blob_shas=$(printf '%s' "$body" | grep -o 'blob/[0-9a-fA-F]\{7,40\}/' 2>/dev/null | sed 's|^blob/||;s|/$||' | LC_ALL=C sort -u || true)
  if [[ -n "$blob_shas" ]]; then
    if [[ "$linkable" != "true" ]]; then
      warn "body carries GitHub blob link(s) on a non-GitHub bundle — keeping body as-is (plain path:line degrades gracefully, blob URLs would 404)"
    elif [[ -z "$sha" ]]; then
      warn "body carries blob link(s) but the reviewed SHA is unknown — keeping body as-is"
    else
      while IFS= read -r s; do
        [[ -z "$s" ]] && continue
        case "$sha" in "$s"*) ;; *) warn "evidence link SHA '$s' differs from the reviewed SHA '${sha:0:7}' — keeping body as-is (links stay pinned to the reviewed commit, never live HEAD)";; esac
      done <<<"$blob_shas"
    fi
  fi
  if printf '%s' "$body" | awk '
    BEGIN { detail = 0; fence = 0; n = 0; bad = 0 }
    index($0, "<details") > 0 { detail++; next }
    index($0, "</details>") > 0 { if (detail > 0) detail--; next }
    ($0 == "```" || $0 ~ /^```[^`]/) {
      if (fence) { if (detail == 0 && n > 3) bad = 1; fence = 0; n = 0 } else { fence = 1; n = 0 }
      next
    }
    { if (fence && detail == 0) n++ }
    END { if (fence && detail == 0 && n > 3) bad = 1; exit bad }'; then
    :
  else
    warn "code quote longer than 3 lines outside collapsed details — keeping body as-is (link-first: quote inside <details> or cut it, the link already points at the lines)"
  fi
}
# Store-schema marker: one version for the whole bundle (findings/review/post/
# record/source shapes evolve together — they're all written by this script).
# Backfills bundles fetched before the marker existed; never fails.
ensure_store() { # $1 = dir
  [[ -f "$1/store.json" ]] || jq -n '{schema_version:"1"}' > "$1/store.json"
}

# Provider-aware PR meta read. GitHub bundles (no provider marker) use gh field
# names; fetch --provider writes the normalized neutral schema. Canonical values:
# state is lowercase open|closed|merged on both paths (OPEN->open for GitHub).
pr_field() { # $1 = dir, $2 = head_sha|base_sha|state|author
  local f="$1/PR.json" prov
  prov=$(jq -r '.provider // empty' "$f" 2>/dev/null || true)
  if [[ -z "$prov" ]]; then
    case "$2" in
      head_sha) jq -r '.headRefOid // empty' "$f" 2>/dev/null || true;;
      base_sha) jq -r '.baseRefOid // empty' "$f" 2>/dev/null || true;;
      state) jq -r '.state // empty' "$f" 2>/dev/null | tr '[:upper:]' '[:lower:]' || true;;
      author) jq -r '.author.login // empty' "$f" 2>/dev/null || true;;
    esac
  else
    case "$2" in
      head_sha) jq -r '.head_sha // empty' "$f" 2>/dev/null || true;;
      base_sha) jq -r '.base_sha // empty' "$f" 2>/dev/null || true;;
      state) jq -r '.state // empty' "$f" 2>/dev/null || true;;
      author) jq -r '.author // empty' "$f" 2>/dev/null || true;;
    esac
  fi
}

# resolve_bundle_dir <slug> — sets dir; local-* slugs and bare slugs with a
# matching .review/local-* dir resolve local (adopt-style); else .review/<slug>.
resolve_bundle_dir() {
  case "$1" in
    local-*) dir=".review/$1";;
    *) if [[ -d ".review/local-$1" && ! -f ".review/$1/PR.json" ]]; then dir=".review/local-$1"; else dir=".review/$1"; fi;;
  esac
}

# Stats derived from the reviewed diff itself (providers without native per-file
# stats). Output: {path: {adds, dels}}. Rename/binary edges approximate — the
# API file list stays authoritative; this only fills +/- counts.
diff_numstat_map() { # $1 = full.diff
  git apply --numstat "$1" 2>/dev/null | awk -F'\t' 'NF==3{a=($1=="-"||$1=="")?0:$1; d=($2=="-"||$2=="")?0:$2; print $3"\t"a"\t"d}'     | jq -R 'split("\t") | select(length==3) | {(.[0]): {adds:(.[1]|tonumber? // 0), dels:(.[2]|tonumber? // 0)}}'     | jq -s 'add // {}'
}

# Non-GitHub fetch: meta + diff + threads per provider, normalized to the same
# PR.json schema post/report understand. Sets $dir/{PR.json,full.diff,reviews.json,inline.json}.
normalize_gitlab() { # $1 = meta, $2 = changes, $3 = numstat map
  jq -n --argjson m "$1" --argjson ch "$2" --argjson ns "$3" '
      ($ch | if type=="array" then . elif type=="object" then (.changes // []) else [] end | if type=="array" then . else [] end) as $files
      | ([ $files[] | . as $f
        | ($ns[($f.new_path)] // $ns[($f.old_path)] // {adds:0,dels:0}) as $s
        | {path:(if $f.new_path == "/dev/null" or $f.new_path == null then $f.old_path else $f.new_path end),
           status:(if ($f.new_file // false) then "added" elif ($f.deleted_file // false) then "deleted" elif ($f.renamed_file // false) then "renamed" else "modified" end),
           additions:$s.adds, deletions:$s.dels} ]) as $fl
      | {schema_version:"1", provider:"gitlab",
         number:($m.iid // $m.id), title:($m.title // ""), body:($m.description // ""),
         base_ref:($m.target_branch // ""), head_ref:($m.source_branch // ""),
         base_sha:($m.diff_refs.base_sha // $m.diff_refs.start_sha // ""),
         head_sha:($m.sha // $m.diff_refs.head_sha // ""),
         author:($m.author.username // $m.author.name // ""),
         url:($m.web_url // ""),
         state:({"opened":"open","closed":"closed","merged":"merged"}[$m.state] // "unknown"),
         draft:($m.draft // false), files:$fl,
         additions:([$fl[].additions] | add // 0), deletions:([$fl[].deletions] | add // 0)} '
}
normalize_gitcafe() { # $1 = meta (cafe envelope: .data.resource), $2 = diff items
  jq -n --argjson m "$1" --argjson it "$2" '
      ($m.data.resource // $m.data // $m) as $r
      | ($it | if type=="array" then . elif type=="object" then (.data.items // .items // []) else [] end) as $files
      | {schema_version:"1", provider:"gitcafe",
         number:$r.number, title:($r.title // ""), body:($r.description // ""),
         base_ref:($r.targetBranch // ""), head_ref:($r.sourceBranch // ""),
         base_sha:($r.observedBaseOid // $r.baseOid // ""), head_sha:($r.sourceOid // $r.headOid // ""),
         author:($r.author.handle // $r.author.login // $r.author.displayName // $r.authorHandle // ""),
         url:($m.data.url // $r.url // null),
         state:(if $r.state == "open" or $r.state == "closed" or $r.state == "merged" then $r.state else "unknown" end),
         draft:($r.draft // false),
         files:([$files[] | {path, status:(.status // "modified"), additions:(.additions // 0), deletions:(.deletions // 0)}]),
         additions:([$files[] | .additions] | add // 0),
         deletions:([$files[] | .deletions] | add // 0)} '
}
normalize_forgejo() { # $1 = meta, $2 = files array
  jq -n --argjson m "$1" --argjson fl "$2" '
      ($fl | if type=="array" then . else [] end) as $files
      | {schema_version:"1", provider:"forgejo",
         number:($m.index // $m.number), title:($m.title // ""), body:($m.body // ""),
         base_ref:($m.base.label // ""), head_ref:($m.head.label // ""),
         base_sha:($m.base.sha // ""), head_sha:($m.head.sha // ""),
         author:($m.user.login // ""),
         url:($m.html_url // ""),
         state:(if ($m.merged // false) then "merged" elif $m.state == "open" then "open" elif $m.state == "closed" then "closed" else "unknown" end),
         draft:(if ($m.draft // null) == null then ((($m.title // "") | test("^WIP:|^\\[DRAFT\\]")) // false) else $m.draft end),
         files:([$files[] | {path:(.filename // ""), status:(.status // "modified"), additions:(.additions // 0), deletions:(.deletions // 0)}]),
         additions:([$files[] | .additions] | add // 0),
         deletions:([$files[] | .deletions] | add // 0)} '
}
normalize_bitbucket() { # $1 = meta, $2 = diffstat array
  jq -n --argjson m "$1" --argjson ds "$2" '
      {schema_version:"1", provider:"bitbucket",
       number:$m.id, title:($m.title // ""), body:($m.summary.raw // $m.description // ""),
       base_ref:($m.destination.branch.name // ""), head_ref:($m.source.branch.name // ""),
       base_sha:($m.destination.commit.hash // ""), head_sha:($m.source.commit.hash // ""),
       author:($m.author.nickname // $m.author.display_name // ""),
       url:($m.links.html.href // ""),
       state:({"OPEN":"open","MERGED":"merged","DECLINED":"closed","SUPERSEDED":"closed"}[$m.state] // "unknown"),
       draft:($m.draft // false),
       files:([$ds[] | {path:(if .new.path == null or .new.path == "" then .old.path else .new.path end),
                        status:(.status // "modified"), additions:(.lines_added // 0), deletions:(.lines_removed // 0)}]),
       additions:([$ds[] | .lines_added] | add // 0),
       deletions:([$ds[] | .lines_removed] | add // 0)} '
}
normalize_azure() { # $1 = meta, $2 = iteration changes, $3 = numstat map
  jq -n --argjson m "$1" --argjson ch "$2" --argjson ns "$3" '
      ($ch | if type=="array" then . else [] end) as $files
      | ([ $files[] | ((.item.path // "") | sub("^/";"")) as $p
        | ($ns[$p] // {adds:0,dels:0}) as $s
        | {path:$p, status:(.changeType // "edit"), additions:$s.adds, deletions:$s.dels} ]) as $fl
      | {schema_version:"1", provider:"azure",
         number:$m.pullRequestId, title:($m.title // ""), body:($m.description // ""),
         base_ref:(($m.targetRefName // "") | sub("^refs/heads/";"")), head_ref:(($m.sourceRefName // "") | sub("^refs/heads/";"")),
         base_sha:($m.lastMergeTargetCommit.commitId // ""), head_sha:($m.lastMergeSourceCommit.commitId // ""),
         author:($m.createdBy.uniqueName // $m.createdBy.displayName // ""),
         url:($m.url // ""),
         state:({"active":"open","abandoned":"closed","completed":"merged"}[$m.status] // "unknown"),
         draft:($m.isDraft // false), files:$fl,
         additions:([$fl[].additions] | add // 0), deletions:([$fl[].deletions] | add // 0)} '
}

fetch_provider_bundle() { # uses globals: provider repo pr dir
  case "$provider" in
  gitlab)
    need glab
    meta=$(glab mr view "$pr" -R "$repo" -F json 2>"$dir/.meta.err") || fail "fetch-failed" "glab mr view failed for $repo!$pr: $(head -c 200 "$dir/.meta.err") (auth? iid?)"
    pid=$(glab api "projects/$(jq -sRr @uri <<<"$repo")" --jq .id 2>/dev/null) || fail "fetch-failed" "cannot resolve GitLab project id for $repo"
    # /changes deprecated since 15.7, removal in API v5: prefer /diffs, keep /changes fallback
    changes=$(glab api "projects/$pid/merge_requests/$pr/diffs" --paginate 2>/dev/null || glab api "projects/$pid/merge_requests/$pr/changes" --paginate 2>/dev/null || echo '{"changes":[]}')
    echo "» glab mr diff -> $dir/full.diff"
    glab mr diff "$pr" -R "$repo" --raw > "$dir/full.diff" 2>"$dir/.diff.err" || fail "fetch-failed" "glab mr diff failed: $(head -c 200 "$dir/.diff.err")"
    notes=$(glab mr note list "$pr" -R "$repo" -F json 2>/dev/null || echo "[]")
    nn=$(jq 'if type=="array" then . else (.notes // .discussions // []) end' <<<"$notes" 2>/dev/null || echo "[]")
    printf '%s' "$nn" | jq '[.[] | select(.type == "DiffNote" or any(.notes[]?; .type == "DiffNote"))]' > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
    printf '%s' "$nn" | jq '[.[] | select(.type != "DiffNote" and all(.notes[]?; .type != "DiffNote"))]' > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
    ns=$(diff_numstat_map "$dir/full.diff")
    normalize_gitlab "$meta" "$changes" "$ns" > "$dir/PR.json" || fail "fetch-failed" "could not normalize GitLab MR $pr"
    ;;
  gitcafe)
    need cafe
    meta=$(cafe pr view "$pr" -R "$repo" --json 2>"$dir/.meta.err") || fail "fetch-failed" "cafe pr view failed: $(head -c 200 "$dir/.meta.err") (auth? number?)"
    items=$(cafe pr diff "$pr" -R "$repo" --patch --json 2>"$dir/.diff.err") || fail "fetch-failed" "cafe pr diff failed: $(head -c 200 "$dir/.diff.err")"
    jq -r '[(. | if type=="array" then . elif type=="object" then (.data.items // .items // []) else [] end)[] | "diff --git a/\(.path) b/\(.path)", (if ((.patch // "") | contains("@@")) then .patch else "" end)] | join("\n")' <<<"$items" > "$dir/full.diff"
    nh=$(jq '[(. | if type=="array" then . elif type=="object" then (.data.items // .items // []) else [] end)[] | select(((.patch // "") | contains("@@") | not) and (.hunks != null))] | length' <<<"$items" 2>/dev/null || echo 0)
    [[ "$nh" != "0" ]] && warn "$nh cafe files carry structured hunks the builder does not render — anchors unchecked for those paths"
    grep -q '^@@' "$dir/full.diff" 2>/dev/null || warn "no hunks indexed from cafe patch — anchors unchecked, everything kept"
    revs="[]"; cursor=""; for _ in $(seq 1 20); do
      rargs=(); [[ -n "$cursor" ]] && rargs=(--cursor "$cursor")
      page=$(cafe pr reviews "$pr" -R "$repo" --json "${rargs[@]}" 2>/dev/null || echo '{}')
      revs=$(jq -n --argjson a "$revs" --argjson b "$(jq '.data.items // .items // []' <<<"$page" 2>/dev/null || echo '[]')" '$a + $b')
      cursor=$(jq -r '.data.next // .next // empty' <<<"$page" 2>/dev/null || true)
      [[ -z "$cursor" ]] && break
    done
    printf '%s' "$revs" > "$dir/reviews.json"
    cmts="[]"; cursor=""; for _ in $(seq 1 20); do
      cargs=(); [[ -n "$cursor" ]] && cargs=(--cursor "$cursor")
      page=$(cafe pr comments "$pr" -R "$repo" --filter all --json "${cargs[@]}" 2>/dev/null || echo '{}')
      cmts=$(jq -n --argjson a "$cmts" --argjson b "$(jq '.data.items // .items // []' <<<"$page" 2>/dev/null || echo '[]')" '$a + $b')
      cursor=$(jq -r '.data.next // .next // empty' <<<"$page" 2>/dev/null || true)
      [[ -z "$cursor" ]] && break
    done
    printf '%s' "$cmts" > "$dir/inline.json"
    normalize_gitcafe "$meta" "$items" > "$dir/PR.json" || fail "fetch-failed" "could not normalize GitCafe PR $pr"
    ;;
  forgejo)
    need curl
    [[ -z "${FORGEJO_HOST:-}" ]] && fail "auth-missing" "forgejo needs FORGEJO_HOST=https://host and FORGEJO_TOKEN set"
    [[ -z "${FORGEJO_TOKEN:-}" ]] && fail "auth-missing" "forgejo needs FORGEJO_TOKEN set (app token with repo read)"
    api="$FORGEJO_HOST/api/v1/repos/$repo/pulls/$pr"
    meta=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" "$api" 2>"$dir/.meta.err") || fail "fetch-failed" "forgejo PR fetch failed: $(head -c 200 "$dir/.meta.err") (host? token? number?)"
    echo "» forgejo diff -> $dir/full.diff"
    curl -sf -H "Authorization: token $FORGEJO_TOKEN" "$api.diff" > "$dir/full.diff" 2>/dev/null       || curl -sf -H "Authorization: token $FORGEJO_TOKEN" "$(jq -r '.diff_url // empty' <<<"$meta")" > "$dir/full.diff" 2>"$dir/.diff.err"       || fail "fetch-failed" "forgejo diff fetch failed: $(head -c 200 "$dir/.diff.err")"
    files=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" "$api/files" 2>/dev/null || echo "[]")
    revs=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" "$api/reviews" 2>/dev/null || echo "[]")
    printf '%s' "$revs" > "$dir/reviews.json"
    jq -r 'if type=="array" then .[].id else empty end' <<<"$revs" 2>/dev/null | while IFS= read -r rid; do
      curl -sf -H "Authorization: token $FORGEJO_TOKEN" "$api/reviews/$rid/comments" 2>/dev/null || echo "[]"
    done | jq -s 'add // []' > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
    normalize_forgejo "$meta" "$files" > "$dir/PR.json" || fail "fetch-failed" "could not normalize Forgejo PR $pr"
    ;;
  bitbucket)
    need curl
    if [[ -n "${BITBUCKET_TOKEN:-}" ]]; then auth=(-H "Authorization: Bearer $BITBUCKET_TOKEN");
    elif [[ -n "${BITBUCKET_USERNAME:-}" && -n "${BITBUCKET_PASSWORD:-}" ]]; then auth=(-u "$BITBUCKET_USERNAME:$BITBUCKET_PASSWORD");
    else fail "auth-missing" "bitbucket needs BITBUCKET_TOKEN (Bearer) or BITBUCKET_USERNAME+BITBUCKET_PASSWORD"; fi
    base="https://api.bitbucket.org/2.0/repos/$repo/pullrequests/$pr"
    meta=$(curl -sf "${auth[@]}" "$base" 2>"$dir/.meta.err") || fail "fetch-failed" "bitbucket PR fetch failed: $(head -c 200 "$dir/.meta.err") (auth? workspace/repo#id?)"
    echo "» bitbucket diff -> $dir/full.diff"
    curl -sfL "${auth[@]}" "$base/diff" > "$dir/full.diff" 2>"$dir/.diff.err" || fail "fetch-failed" "bitbucket diff failed: $(head -c 200 "$dir/.diff.err")"
    ds="[]"; url="$base/diffstat?pagelen=100"
    while [[ -n "$url" && "$url" != "null" ]]; do
      page=$(curl -sfL "${auth[@]}" "$url" 2>/dev/null || echo '{}')
      ds=$(jq -n --argjson a "$ds" --argjson b "$(jq '.values // []' <<<"$page")" '$a + $b')
      url=$(jq -r '.next // empty' <<<"$page")
    done
    cm="[]"; url="$base/comments?pagelen=100"
    while [[ -n "$url" && "$url" != "null" ]]; do
      page=$(curl -sfL "${auth[@]}" "$url" 2>/dev/null || echo '{}')
      cm=$(jq -n --argjson a "$cm" --argjson b "$(jq '.values // []' <<<"$page")" '$a + $b')
      url=$(jq -r '.next // empty' <<<"$page")
    done
    jq '[.[] | select(.inline != null)]' <<<"$cm" > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
    jq '[.[] | select(.inline == null)]' <<<"$cm" > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
    normalize_bitbucket "$meta" "$ds" > "$dir/PR.json" || fail "fetch-failed" "could not normalize Bitbucket PR $pr"
    ;;
  azure)
    need az
    org="${AZURE_DEVOPS_ORG:-}"
    [[ -z "$org" ]] && fail "auth-missing" "azure needs AZURE_DEVOPS_ORG=https://dev.azure.com/{org} (and az login or AZURE_DEVOPS_EXT_PAT)"
    project="${repo%%/*}"; rname="${repo##*/}"
    meta=$(az repos pr show --id "$pr" --org "$org" --project "$project" --repository "$rname" -o json 2>"$dir/.meta.err") || fail "fetch-failed" "az repos pr show failed: $(head -c 200 "$dir/.meta.err") (login? extension? project/repo#id?)"
    remote=$(jq -r '.repository.remoteUrl // empty' <<<"$meta")
    hs=$(jq -r '.lastMergeSourceCommit.commitId // empty' <<<"$meta")
    bs=$(jq -r '.lastMergeTargetCommit.commitId // empty' <<<"$meta")
    [[ -z "$hs" || -z "$bs" || -z "$remote" ]] && fail "fetch-failed" "azure PR $pr carries no merge commits/remote"
    tok="${AZURE_DEVOPS_EXT_PAT:-}"
    if [[ -z "$tok" ]]; then tok=$(az account get-access-token --query accessToken -o tsv 2>/dev/null || true); fi
    [[ -z "$tok" ]] && fail "auth-missing" "azure git fetch needs AZURE_DEVOPS_EXT_PAT or an az login with token access"
    tmp=$(mktemp -d); enc=$(printf ':%s' "$tok" | base64 | tr -d '\n')
    trap 'rm -rf "$tmp"' EXIT
    git init -q "$tmp" || fail "fetch-failed" "azure temp clone init failed"
    { echo '[http]'; echo "extraHeader = AUTHORIZATION: Basic $enc"; } >> "$tmp/.git/config" || fail "fetch-failed" "azure temp clone config failed"
    git -C "$tmp" fetch -q "$remote" "$hs" "$bs" 2>"$dir/.diff.err"       || fail "fetch-failed" "azure git fetch failed: $(head -c 200 "$dir/.diff.err")"
    git -C "$tmp" diff "$bs" "$hs" > "$dir/full.diff" 2>/dev/null || fail "fetch-failed" "azure diff failed"
    rm -rf "$tmp"; trap - EXIT
    threads=$(az rest --method get --url "$org/$project/_apis/git/repositories/$rname/pullRequests/$pr/threads?api-version=7.1" --query value -o json 2>/dev/null || echo "[]")
    jq '[.[] | select(.threadContext == null)]' <<<"$threads" > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
    jq '[.[] | select(.threadContext != null)]' <<<"$threads" > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
    iid=$(az rest --method get --url "$org/$project/_apis/git/repositories/$rname/pullRequests/$pr/iterations?api-version=7.1" --query "max_by(value, &id).id" -o tsv 2>/dev/null || true)
    chg=$(if [[ -n "$iid" ]]; then az rest --method get --url "$org/$project/_apis/git/repositories/$rname/pullRequests/$pr/iterations/$iid/changes?api-version=7.1" --query changeEntries -o json 2>/dev/null || echo "[]"; else echo "[]"; fi)
    ns=$(diff_numstat_map "$dir/full.diff")
    normalize_azure "$meta" "$chg" "$ns" > "$dir/PR.json" || fail "fetch-failed" "could not normalize Azure PR $pr"
    ;;
  esac
}

# Behavioral diagram (P4): agent-drawn mermaid under the ported contract.
# Prompt side: prompts/diagrams.md (7-type selection map, honesty rule,
# copy-exact skeletons, marked wrapper, one-diagram cap, tables-stay-tables).
# Script side: grammar-level only — fenced block present, keyword in the
# GitHub-rendered allowlist, fences balanced, fence non-empty. Unknown/beta
# keywords, unbalanced or empty fences, and cap extras never fail the run:
# they warn and drop with a one-line note. Gating: publish.diagrams.behavioral
# (default false) — the block renders only when opted in; when off, every
# candidate is stripped from the posted body (stderr warn only, no body
# note). publish.diagrams.expanded (default false) normalizes the kept
# wrapper to <details open>; otherwise <details>. Scope: the contract wrapper
# carries an open+close marker pair because a mid-body block cannot reuse the
# footer's cut-to-end divider — same HTML-comment-marker + awk strip-replace
# idiom, bounded variant (not a second mechanism: identical markers,
# identical strip-then-render flow). Markers are agent-written per the
# fragment (copy-exact); unmarked fences are still validated and kept with an
# advisory warn (keep-biased: chrome is not grammar). Idempotent: the source
# (review.json) is immutable per run, and our own drop-note lines are
# signature-stripped before analysis, so re-runs and pasted-output inputs
# never stack. Runs inside validate_and_merge after the store derivation (the
# record keeps everything the agent wrote; only the posted body is curated).
# Sets DIAGRAM_* globals for the dry-run envelope; mutates $body.
DIAGRAM_START="<!-- verdict-diagram:start -->"
DIAGRAM_END="<!-- verdict-diagram:end -->"
DIAGRAM_ALLOWLIST="flowchart graph mindmap sequenceDiagram stateDiagram gitGraph quadrantChart classDiagram erDiagram gantt"
DIAGRAM_NOTE_PREFIX="> Diagram omitted: "
DIAGRAM_STATUS="absent"; DIAGRAM_DETAIL="no diagram candidates"; DIAGRAM_KEYWORD=""
DIAGRAM_WRAPPED="false"; DIAGRAM_COLLAPSED="true"; DIAGRAM_BLOCKS="0"; DIAGRAM_NOTE=""
validate_behavioral_diagram() { # no args; reads/mutates $body, sets DIAGRAM_* globals
  local tmp="" r="" cl="" kind="" o="" c="" rg="" kw="" dl="" cand=""
  local del="" dline="0" dopen="0" n="0" kept="false" wrapped_note="" marked_note=""
  local first_reason="" closed="" rcounts=""
  DIAGRAM_STATUS="absent"; DIAGRAM_DETAIL="no diagram candidates"; DIAGRAM_KEYWORD=""
  DIAGRAM_WRAPPED="false"; DIAGRAM_COLLAPSED="true"; DIAGRAM_BLOCKS="0"; DIAGRAM_NOTE=""
  # Pasted-output idempotency: our own one-line drop notes never stack —
  # strip them before analysis (a no-op on fresh review.json input).
  body=$(printf '%s\n' "$body" | grep -v '^> Diagram omitted: ' || true)
  if ! printf '%s\n' "$body" | grep -qE '^[ \t]{0,3}```mermaid([ \t].*)?$' \
    && ! printf '%s' "$body" | grep -qF "$DIAGRAM_START"; then
    return 0
  fi
  tmp=$(mktemp -d "${TMPDIR:-/tmp}/verdict-diagram.XXXXXX") || return 0
  printf '%s\n' "$body" > "$tmp/body.txt"
  awk -v ms="$DIAGRAM_START" -v me="$DIAGRAM_END" '
    BEGIN { last_details = 0 }
    {
      line = $0
      t = line; sub(/^[ \t]+/, "", t); sub(/[ \t]+$/, "", t)
      if (!in_fence && t == ms) { in_region = 1; region_n++; rstart[region_n] = NR; next }
      if (in_region && !in_fence && t == me) {
        print "REGION " region_n " " rstart[region_n] " " NR; in_region = 0; next }
      if (!in_fence && line ~ /^[ \t]*<details([ \t>]|$)/) { last_details = NR; next }
      if (!in_fence && line ~ /^[ \t]*<\/details[ \t]*>/) { last_details = 0; next }
      if (!in_fence && line ~ /^[ \t]{0,3}```mermaid([ \t].*)?$/) {
        # Single-line fence (open+close on one line) is not a GitHub block:
        # CommonMark forbids backticks in the info string, so it never renders.
        # Skipping it here keeps it from opening a swallow to end of body.
        rest = line; sub(/^[ \t]{0,3}```/, "", rest);
        if (rest ~ /```[ \t]*$/) next;
        in_fence = 1; fopen = NR
        fregion = (in_region ? region_n : 0)
        fdetails = last_details; kw = ""; next }
      if (in_fence) {
        if (line ~ /^[ \t]{0,3}```[ \t]*$/) {
          if (kw == "") kw = "EMPTY"
          print "FENCE " fopen " " NR " " fregion " " kw " " fdetails
          in_fence = 0; next }
        if (kw == "" && line !~ /^[ \t]*$/ && line !~ /^[ \t]*%%/ && line !~ /^[ \t]*---[ \t]*$/) kw = $1
        next }
    }
    END {
      if (in_fence) {
        if (kw == "") kw = "EMPTY"
        print "FENCE " fopen " 0 " fregion " " kw " " fdetails }
      if (in_region) print "REGION " region_n " " rstart[region_n] " 0"
    }' "$tmp/body.txt" > "$tmp/report.txt" || true
  # Candidates in document order: fences by open line, fenceless regions by
  # start line. Internal EXTRA_* lines sort with the fence span sort below.
  cand=""
  while IFS= read -r r; do
    [[ -z "$r" ]] && continue
    # shellcheck disable=SC2086
    set -- $r
    case "$1" in
      FENCE) cand="$cand$2 F $2 $3 $4 $5 $6
";;
      REGION)
        if ! grep -q "^FENCE [0-9]* [0-9]* $2 " "$tmp/report.txt"; then
          cand="$cand$3 R $3 $4
"
        fi;;
    esac
  done < "$tmp/report.txt"
  if [[ -z "$cand" ]]; then rm -rf "$tmp"; return 0; fi
  cand=$(printf '%s' "$cand" | sort -n -k1,1)
  : > "$tmp/notes.txt"
  # Fence count per region (for surgical vs whole-block drops): "idx:count ".
  rcounts=$(awk '$1=="FENCE"{c[$4]++} END{for (k in c) print k":"c[k]}' "$tmp/report.txt" | tr '\n' ' ' || true)
  span_for() { # $1=kind(F/R) $2=o $3=c $4=rg → echoes start:end to delete
    local k="$1" s="$2" e="$3" g="$4" oc="" rs=""
    [[ "$e" == "0" ]] && e="1000000000"
    if [[ "$k" == "F" && -n "$g" && "$g" != "0" ]]; then
      oc=$(printf '%s' "$rcounts" | tr ' ' '\n' | grep "^$g:" | cut -d: -f2 || true)
      if [[ "$oc" == "1" ]]; then
        rs=$(grep "^REGION $g " "$tmp/report.txt" || true)
        if [[ -n "$rs" ]]; then
          # shellcheck disable=SC2086
          set -- $rs
          s="$3"; e="$4"; [[ "$e" == "0" ]] && e="1000000000"
        fi
      fi
    fi
    printf '%s:%s' "$s" "$e"
  }
  add_note() { # $1 = reason; appends the fixed-shape note line, records the first
    printf '%s%s — see prompts/diagrams.md.\n' "$DIAGRAM_NOTE_PREFIX" "$1" >> "$tmp/notes.txt"
    if [[ -z "$first_reason" ]]; then first_reason="$1"; fi
    if [[ -z "$DIAGRAM_NOTE" ]]; then DIAGRAM_NOTE="$DIAGRAM_NOTE_PREFIX$1 — see prompts/diagrams.md."; fi
  }
  if [[ "${EFFECTIVE_PUBLISH_DIAGRAMS_BEHAVIORAL:-false}" != "true" ]]; then
    # Opt-in off: strip every candidate (marked blocks whole, bare fences bare).
    while IFS= read -r cl; do
      [[ -z "$cl" ]] && continue
      # shellcheck disable=SC2086
      set -- $cl
      kind="$2"; o="$3"; c="$4"; rg="${5:-}"
      del="$del$(span_for "$kind" "$o" "$c" "$rg") "
      n=$(( n + 1 ))
    done <<<"$cand"
    warn "stripped $n behavioral diagram block(s) — publish.diagrams.behavioral is off (opt-in knob, renders only when set)"
    DIAGRAM_STATUS="stripped"; DIAGRAM_BLOCKS="$n"
    DIAGRAM_DETAIL="stripped $n block(s): publish.diagrams.behavioral is off"
  else
    # Opt-in on: validate each candidate in order; the first valid block is
    # kept in place, everything else drops with a note.
    first_reason=""
    while IFS= read -r cl; do
      [[ -z "$cl" ]] && continue
      # shellcheck disable=SC2086
      set -- $cl
      kind="$2"; o="$3"; c="$4"; rg="${5:-}"; kw="${6:-}"; dl="${7:-}"
      n=$(( n + 1 ))
      if [[ "$kind" == "R" ]]; then
        del="$del$(span_for "$kind" "$o" "$c" "") "
        add_note "marked block carries no mermaid fence"
        warn "dropping behavioral diagram block: marked block carries no mermaid fence — kept a note"
        continue
      fi
      closed="true"; [[ "$c" == "0" ]] && closed="false"
      if [[ "$closed" == "false" ]]; then
        reason="unbalanced fence (no closing fence)"
        del="$del$(span_for F "$o" "$c" "$rg") "
        add_note "$reason"
        warn "dropping behavioral diagram: unbalanced fence (missing closing fence) — kept a note"
        continue
      fi
      if [[ "$kw" == "EMPTY" ]]; then
        reason="empty fence (no diagram lines)"
        del="$del$(span_for F "$o" "$c" "$rg") "
        add_note "$reason"
        warn "dropping behavioral diagram: empty fence (no diagram lines) — kept a note"
        continue
      fi
      case " $DIAGRAM_ALLOWLIST " in
        *" $kw "*) ;;
        *)
          reason="unknown keyword '$kw' (want one of: $DIAGRAM_ALLOWLIST)"
          del="$del$(span_for F "$o" "$c" "$rg") "
          add_note "$reason"
          warn "dropping behavioral diagram: unknown keyword '$kw' (not in the GitHub-rendered subset) — kept a note"
          continue;;
      esac
      if [[ "$kept" == "true" ]]; then
        reason="one-diagram cap: kept the first block"
        del="$del$(span_for F "$o" "$c" "$rg") "
        add_note "$reason"
        warn "dropping extra behavioral diagram (one-diagram cap, kept the first) — kept a note"
        continue
      fi
      kept="true"; DIAGRAM_KEYWORD="$kw"
      wrapped_note=""; marked_note=""
      if [[ "$dl" != "0" ]]; then
        DIAGRAM_WRAPPED="true"; dline="$dl"
        if [[ "${EFFECTIVE_PUBLISH_DIAGRAMS_EXPANDED:-false}" == "true" ]]; then
          dopen="2"; DIAGRAM_COLLAPSED="false"
        else
          dopen="1"; DIAGRAM_COLLAPSED="true"
        fi
      else
        warn "behavioral diagram not wrapped in collapsed <details> — keeping as-is (wrap it per prompts/diagrams.md)"
        wrapped_note=", unwrapped"
      fi
      if [[ "$rg" == "0" ]]; then
        warn "behavioral diagram outside the marked wrapper — keeping as-is (wrap it per prompts/diagrams.md)"
        marked_note=", unmarked"
      fi
      if [[ "$DIAGRAM_COLLAPSED" == "true" ]]; then
        DIAGRAM_DETAIL="kept $kw (collapsed$wrapped_note$marked_note)"
      else
        DIAGRAM_DETAIL="kept $kw (open$wrapped_note$marked_note)"
      fi
    done <<<"$cand"
    DIAGRAM_BLOCKS="$n"
    if [[ "$kept" == "true" ]]; then
      DIAGRAM_STATUS="kept"
    else
      DIAGRAM_STATUS="dropped"; DIAGRAM_DETAIL="dropped: $first_reason"
    fi
  fi
  body=$(awk -v ranges="$del" -v dline="$dline" -v dopen="$dopen" -v notef="$tmp/notes.txt" -v marker="$FOOTER_DIVIDER" '
    BEGIN {
      nr = 0; nn = 0; noted = 0
      while ((getline l < notef) > 0) { nn++; nlines[nn] = l }
      if (ranges != "") {
        n = split(ranges, rs, " ")
        for (i = 1; i <= n; i++) {
          c = index(rs[i], ":")
          if (c > 0) { nr++; ds[nr] = substr(rs[i], 1, c-1)+0; de[nr] = substr(rs[i], c+1)+0 }
        }
      }
    }
    {
      skip = 0
      for (i = 1; i <= nr; i++) if (NR >= ds[i] && NR <= de[i]) { skip = 1; break }
      if (skip) next
      # Drop-notes land BEFORE a carried footer block: strip-replace cuts
      # from the divider to end-of-body on re-render, so end-appended notes
      # would be deleted with the stale footer. No divider present (fresh
      # run) falls through to the END block below.
      if (!noted && $0 == marker) { for (i = 1; i <= nn; i++) print nlines[i]; noted = 1 }
      if (dline > 0 && NR == dline) {
        if (dopen == 2) print "<details open>"
        else print "<details>"
        next
      }
      print
    }
    END { if (!noted) for (i = 1; i <= nn; i++) print nlines[i] }' "$tmp/body.txt")
  rm -rf "$tmp"
}

# Validate anchors (partitioned, keep-biased) + merge findings + claims.
# Shared by post (PR bundles) and record (local bundles): identical behavior,
# no GitHub I/O. Reads $dir/{incremental.diff,verification.json,findings.json}
# when present. Sets globals kept/moved/body/entries/n_* /marker/hunk_n.
now_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# Cross-chunk merge validation for chunked bundles (manifest present). No-op
# when the manifest is absent — non-chunked bundles validate exactly as today.
# Two new typed states, both fail-closed (existing exits untouched):
#   missing-chunk-snapshot — the manifest is unreadable, carries an unknown
#     schema, lists no chunks, or a pinned chunk diff is missing/empty. Never
#     post partial results off a broken bundle.
#   missing-chunk-coverage — the review attests chunk coverage (top-level
#     "chunks": [...] naming every manifest chunk synthesized) but omits a
#     manifest chunk. Attested-but-partial coverage can never post as
#     complete; unattested chunked reviews warn loudly instead (exit 0).
# Attestation is opt-in and additive: reviews without a "chunks" claim (delta
# re-reviews, single-anchor probes, pre-chunk agents) validate exactly as
# today — keep-biased anchors plus the sig-dedupe merge. Anchors validate
# against the pinned per-chunk snapshots (one hunks file per chunk diff,
# combined here in manifest order); when the chunk snapshots no longer
# concatenate to full.diff (commits landed mid-review) that drift warns loudly
# and validation stays pinned — drift surfaces, never silent mis-anchors.
# Sets the anchor_hunks global (per-chunk pinned hunks on the chunked path,
# else $dir/hunks.tsv).
validate_chunk_merge() { # $1 = review.json, $2 = dir; sets anchor_hunks
  local review_file="$1" dir="$2" man="$2/chunks/manifest.json"
  local schema="" names="" entry="" name="" diffref="" chunkdiff="" diffs=""
  local refetch="" missing="" claimed="" extra_claims="" files="" n_all="" n_miss=""
  anchor_hunks="$dir/hunks.tsv"
  [[ -f "$man" ]] || return 0
  if [[ "$cmd" == "record" ]]; then
    case "$pr" in local-*) refetch="./scripts/review.sh fetch-local $repo ${pr#local-} --chunk -- <paths>";;
      *) refetch="./scripts/review.sh fetch $repo $pr --chunk";; esac
  else
    refetch="./scripts/review.sh fetch $repo $pr --chunk"
  fi
  schema=$(jq -r '.schema_version // empty' "$man" 2>/dev/null || true)
  if [[ "$schema" != "1" ]]; then
    fail "missing-chunk-snapshot" "refusing $cmd: chunk manifest $man is unreadable or schema '${schema:-missing}' unknown (want schema_version 1) — re-fetch with '$refetch' to restore the pinned snapshots, then re-run"
  fi
  names=$(jq -r '.chunks | sort_by(.index) | .[].name // empty' "$man" 2>/dev/null || true)
  if [[ -z "$names" ]]; then
    fail "missing-chunk-snapshot" "refusing $cmd: chunk manifest $man lists no chunks — re-fetch with '$refetch' to restore the pinned snapshots, then re-run"
  fi
  rm -f "$dir/.chunk-hunks.tsv" "$dir/.chunk-hunks.tsv.part"
  : > "$dir/.chunk-hunks.tsv"
  missing=""; diffs=""
  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    name=$(jq -r '.name // empty' <<<"$entry" 2>/dev/null || true)
    diffref=$(jq -r '.diff // empty' <<<"$entry" 2>/dev/null || true)
    chunkdiff=""
    if [[ -n "$diffref" && -s "$diffref" ]]; then chunkdiff="$diffref";
    elif [[ -n "$name" && -s "$dir/chunks/$name.diff" ]]; then chunkdiff="$dir/chunks/$name.diff"; fi
    if [[ -z "$chunkdiff" ]]; then missing="${missing:+$missing }$name"; continue; fi
    diffs="$diffs$chunkdiff
"
    build_hunks_tsv "$chunkdiff" "$dir/.chunk-hunks.tsv.part"
    cat "$dir/.chunk-hunks.tsv.part" >> "$dir/.chunk-hunks.tsv"
  done < <(jq -c '.chunks | sort_by(.index) | .[]' "$man" 2>/dev/null || true)
  rm -f "$dir/.chunk-hunks.tsv.part"
  if [[ -n "$missing" ]]; then
    rm -f "$dir/.chunk-hunks.tsv"
    fail "missing-chunk-snapshot" "refusing $cmd: chunk snapshot(s) missing: $missing (pinned chunk diff unreadable) — re-fetch with '$refetch' to restore the chunk snapshots, then re-run (partial results never post)"
  fi
  if ! while IFS= read -r chunkdiff; do
         [[ -n "$chunkdiff" ]] || continue
         cat -- "$chunkdiff"
       done <<<"$diffs" | cmp -s - "$dir/full.diff"; then
    warn "chunk snapshots drifted from $dir/full.diff (commits landed mid-review?) — validating anchors against the pinned chunk snapshots; re-fetch with --chunk to re-pin"
  fi
  # Coverage obligation: only an attested synthesis is gated. The agent stamps
  # top-level "chunks" (see prompts/review.md) naming every manifest chunk the
  # synthesis covers; the gate refuses when a manifest chunk is missing from
  # that claim. Malformed claims warn and validate as unattested (keep-biased:
  # garnish never fails the run). A chunked bundle with no usable claim at
  # all (absent or null) warns loudly naming the manifest — silent partial
  # synthesis is impossible — while still exiting 0 for delta/single-anchor
  # compatibility.
  if jq -e 'has("chunks") and (.chunks != null)' "$review_file" >/dev/null 2>&1; then
    if ! jq -e '.chunks | type == "array"' "$review_file" >/dev/null 2>&1; then
      warn "ignoring malformed chunks attestation (want an array of manifest chunk names) — validating as unattested"
    else
      claimed=$(jq -r '.chunks[] | if type == "string" then . else empty end' "$review_file" 2>/dev/null || true)
      missing=""
      while IFS= read -r name; do
        [[ -n "$name" ]] || continue
        if ! grep -Fxq -- "$name" <<<"$claimed"; then
          files=$(jq -r --arg n "$name" '.chunks[] | select(.name == $n) | (.files // [] | join(", "))' "$man" 2>/dev/null || true)
          if [[ -n "$files" ]]; then missing="${missing:+$missing, }${name} (${files})";
          else missing="${missing:+$missing, }${name}"; fi
        fi
      done <<<"$names"
      extra_claims=$(printf '%s\n' "$claimed" | grep -v '^$' || true)
      while IFS= read -r name; do
        [[ -n "$name" ]] || continue
        if ! grep -Fxq -- "$name" <<<"$names"; then
          warn "ignoring unknown chunk '$name' in the chunks attestation (not in $man)"
        fi
      done <<<"$extra_claims"
      if [[ -n "$missing" ]]; then
        n_all=$(printf '%s\n' "$names" | grep -c . || true)
        n_miss=$(printf '%s\n' "$names" | while IFS= read -r nm; do grep -Fxq -- "$nm" <<<"$claimed" || printf 'x\n'; done | grep -c . || true)
        rm -f "$dir/.chunk-hunks.tsv"
        fail "missing-chunk-coverage" "refusing $cmd: synthesized review covers $(( n_all - n_miss )) of $n_all manifest chunk(s) — missing $missing — re-fetch with '$refetch', re-synthesize across all chunks, then re-run (partial coverage never posts as complete)"
      fi
    fi
  else
    n_all=$(printf '%s\n' "$names" | grep -c . || true)
    warn "chunked bundle $man lists $n_all chunk(s) but the review carries no chunks attestation — validating as unattested (exit 0); an unattested partial synthesis posts as complete, so re-synthesize across all chunks and stamp every manifest chunk name in top-level \"chunks\""
  fi
  anchor_hunks="$dir/.chunk-hunks.tsv"
}

validate_and_merge() { # $1 = review.json, $2 = dir, $3 = head full sha ("" ok), $4 = now, $5 = findings file
  local review_file="$1" dir="$2" head_full="$3" now="$4" findings_file="$5"
  kb_resolve_policy
  KB_BROKEN="false"
  KB_STAGED="[]"
  if [[ -f "$KB_FILE" ]] && ! jq empty "$KB_FILE" >/dev/null 2>&1; then
    warn "kb store $KB_FILE unreadable — citations ignored for this run (fix the bad line)"
    KB_BROKEN="true"
  fi
  # review.json schema: agents stamp schema_version; unknown versions refuse
  # (uninterpretable), missing versions warn-and-proceed (grandfathered agents).
  rv=$(jq -r '.schema_version // empty' "$review_file")
  case "$rv" in "") warn "review.json carries no schema_version — assuming 1 (agents: write \"schema_version\":\"1\")";; 1) ;; *) fail "bad-review-schema" "review.json schema_version '$rv' unknown (script knows 1)";; esac
  # Cross-chunk merge validation (chunked bundles only; absent manifest reads
  # today's behavior). Refuses unreadable snapshots and attested-but-partial
  # coverage; pins anchors to the per-chunk snapshots. Sets anchor_hunks.
  anchor_hunks="$dir/hunks.tsv"
  validate_chunk_merge "$review_file" "$dir"
  # Review-level confidence + security summary (optional, for later calibration
  # scoring): absent stays absent (null); out-of-range confidence warns and
  # drops, never fails. Non-string security summaries warn and drop too.
  local conf_raw sec_type
  rev_confidence="null"; rev_security="null"
  conf_raw=$(jq -r '.confidence // empty' "$review_file" 2>/dev/null || true)
  if [[ -n "$conf_raw" ]]; then
    case "$conf_raw" in
      1|2|3|4|5) rev_confidence="$conf_raw";;
      *) warn "ignoring confidence '$conf_raw' (want integer 1-5)";;
    esac
  fi
  sec_type=$(jq -r 'if has("security_summary") then (.security_summary | type) else "absent" end' "$review_file" 2>/dev/null || echo "absent")
  case "$sec_type" in
    absent|null) ;;
    string)
      if [[ -n $(jq -r '.security_summary' "$review_file" 2>/dev/null) ]]; then rev_security=$(jq -c '.security_summary' "$review_file"); fi;;
    *) warn "ignoring non-string security_summary (want a short string)";;
  esac
  local hs="${head_full:0:7}"
  # Partitioned anchor validation (keep-biased): a comment posts inline only when its
  # [start_line, line] sits inside ONE hunk on the right side. Anything ambiguous
  # (unknown path, non-numeric line) is KEPT with a warning — only provably outside
  # an anchor moves to the body. One bad anchor never fails the run.
  kept="[]"; moved="[]"
  while IFS= read -r c; do
    p=$(jq -r '.path' <<<"$c")
    line=$(jq -r '.line // empty' <<<"$c"); start=$(jq -r '.start_line // empty' <<<"$c")
    side=$(jq -r '.side // "RIGHT"' <<<"$c" | tr '[:lower:]' '[:upper:]')
    key="$p"; [[ "$key" == a/* || "$key" == b/* ]] && key="${key:2}"
    keep() { kept=$(jq --argjson c "$c" '. + [$c]' <<<"$kept"); }
    if ! awk -F'\t' -v f="$key" '$1==f{found=1; exit} END{exit !found}' "$anchor_hunks"; then
      warn "kept despite unknown path (not in diff index, ambiguity keeps): $p"; keep; continue
    fi
    [[ -z "$start" ]] && start="$line"
    if ! [[ "$line" =~ ^[0-9]+$ && "$start" =~ ^[0-9]+$ ]]; then
      warn "kept despite non-numeric anchor: $p $start-$line"; keep; continue
    fi
    if (( start > line )); then t="$start"; start="$line"; line="$t"; fi
    want="NEW"; [[ "$side" == "LEFT" ]] && want="OLD"
    if awk -F'\t' -v f="$key" -v w="$want" -v a="$start" -v b="$line" \
        '$1==f && $2==w && $3<=a && b<=$4 {found=1; exit} END{exit !found}' "$anchor_hunks"; then
      keep
    else
      moved=$(jq --argjson c "$c" --arg reason "anchor $start-$line ($side) outside reviewed hunks" \
        '. + [$c + {dropReason: $reason}]' <<<"$moved")
    fi
  done < <(jq -c '.comments // [] | .[]' "$review_file")
  # Suggestion fences: GitHub renders only fenced blocks inside body — a suggestion
  # containing a fence would break rendering, so demote those comments to the body.
  bad_n=$(jq '[.[] | select((.suggestion // "") | contains("```"))] | length' <<<"$kept")
  if (( bad_n > 0 )); then
    warn "demoting $bad_n suggestion(s) with fences to body"
    moved=$(jq -n --argjson k "$kept" --argjson m "$moved" \
      '([$k[] | select((.suggestion // "") | contains("```")) | . + {dropReason:"suggestion contains a fence — posted as plain text instead"}] + $m)')
    kept=$(jq '[.[] | select(((.suggestion // "") | contains("```")) | not)]' <<<"$kept")
  fi
  n_kept=$(jq 'length' <<<"$kept"); n_moved=$(jq 'length' <<<"$moved")
  echo "» anchors: $n_kept inline, $n_moved moved to body"
  if (( n_moved > 0 )); then
    jq -r '.[] | "- `\(.path):\(.start_line // .line)-\(.line)` (\(.dropReason)): \(.body // "" | .[0:200])"' <<<"$moved" | sed 's/^/  /' >&2
    move_section=$(jq -r '"### ℹ️ Unanchored observations\n\nThe following points fell outside the diff hunks for the reviewed SHA, so they post here instead of inline:\n" + ([.[] | "- `\(.path):\(.start_line // .line)-\(.line)` — \(.body // "") (\(.dropReason // "outside reviewed hunks"))"] | join("\n"))' <<<"$moved")
    body="$body

$move_section"
  fi
  # Findings store: stable sigs (line-number-free, so deltas merge), append-only history.
  all_comments=$(jq -s 'add' <(printf '%s' "$kept") <(printf '%s' "$moved"))
  new_entries=$(jq -c -n --argjson comments "$all_comments" --arg body "$body" --argjson secs "$(jq '.sections // []' "$review_file")" '
    [($comments[] | {kind:"inline", path, side:(.side // "RIGHT"), line,
        anchored:(.dropReason == null), body:(.body // ""), suggestion:(.suggestion // null),
        kb:(.kb // null),
        inBounds:(if (.["in-bounds"] == null) then true else .["in-bounds"] end),
        rigor:(.rigor as $r | if (["said","pointed","walked","ran","reproduced"] | index($r)) then $r else "pointed" end),
        severity:(.severity as $s | if (["critical","important","informational"] | index($s)) then $s else null end)}),
     ($body | split("\n") | map(select(test("^### "))) | .[] | . as $h
       | {kind:"section", heading:$h, anchored:"body", body:$h,
          severity:([$secs[] | select(.heading == $h) | .severity] | .[0] | . as $s
                    | if (["critical","important","informational"] | index($s)) then $s else null end)})]
    | map(.sigKey = ((.path // "") + "\n" + (.heading // "") + "\n" + .body)) | .[]')
  # sig each entry (bash loop — jq has no sha1)
  entries="[]"
  while IFS= read -r e; do
    [[ -z "$e" ]] && continue
    key=$(jq -r '.sigKey' <<<"$e"); sig=$(sig_of "$key")
    kb_normalize_entry "$e"; kb_list="$KB_NORM_JSON"
    entries=$(jq --argjson e "$e" --arg sig "$sig" --arg now "$now" --arg sha "$hs" \
      --argjson kb "$kb_list" \
      '. + [$e + {sig:$sig, sig_version:"1", firstSeen:$sha, lastSeen:$sha, status:"open", rationale:"", kb:$kb, history:[{at:$now, event:"reported"}]} | del(.sigKey)]' <<<"$entries")
  done <<<"$new_entries"
  # Re-post merge: absent/null kb ($n.kb null) keeps the prior cite via //,
  # explicit [] clears it ([] is a value to //); present arrays normalized
  # above overwrite. Brand-new entries default to [] in the backfill below.
  if [[ -f "$dir/findings.json" ]]; then old="$dir/findings.json"; else old="/dev/null"; fi
  if [[ "$old" != "/dev/null" ]]; then
    entries=$(jq --slurpfile old "$old" --arg now "$now" --arg sha "$hs" '
      ($old[0] // []) as $o
      | ([.[] | {(.sig): .}] | add // {}) as $byNew
      | (([$o[] | . as $prev | ($byNew[$prev.sig]) as $n
           | if $n == null then $prev
             else ($prev + {lastSeen:$sha,
                    path:($n.path // $prev.path), line:($n.line // $prev.line),
                    body:$n.body, anchored:$n.anchored,
                    rigor:($n.rigor // $prev.rigor), severity:($n.severity // $prev.severity), suggestion:($n.suggestion // $prev.suggestion),
                    inBounds:(if ($n.inBounds == null) then ($prev.inBounds // true) else $n.inBounds end),
                    kb:($n.kb // $prev.kb // [])})
             end])
         + [.[] | . as $n | select([$o[] | select(.sig == $n.sig)] | length == 0)])' <<<"$entries")
  fi
  # sig_version backfill: entries that predate versioning (or arrive without one)
  # get "1" — never clobber a version a newer normalization stamped.
  # kb default: brand-new entries whose comment carried no kb cite arrive as
  # null (absent) — they read [] (re-posts merge before this line, so prior
  # cites are already kept by the $n.kb // $prev.kb above).
  entries=$(jq 'map(.kb //= [] | if has("sig_version") then . else . + {sig_version:"1"} end)' <<<"$entries")
  # Auto-reopen: on delta runs (incremental.diff present), addressed entries whose path
  # the delta touches and whose lastSeen predates this run go back to open.
  if [[ -f "$dir/incremental.diff" ]]; then
    inc_files=$(grep '^diff --git' "$dir/incremental.diff" | sed 's/.* b\///' | jq -R . | jq -s . || true)
    entries=$(jq --argjson files "$inc_files" --arg sha "$hs" --arg now "$now" '
      map(. as $e | if $e.status == "addressed" and ($e.path != null)
        and ([$files[] | select(. == $e.path)] | length > 0) and $e.lastSeen != $sha
        then $e + {status:"open", history:($e.history + [{at:$now, event:"reopened", actor:"script", note:"delta touched path", sha:$sha}])}
        else $e end)' <<<"$entries")
  fi
  # Authority: bare resolved[] sigs are proposals (history only, never a flip).
  resolved=$(jq -r '.resolved // [] | .[]' "$review_file" 2>/dev/null || true)
  if [[ -n "$resolved" ]]; then
    while IFS= read -r sig; do
      [[ -z "$sig" ]] && continue
      full=$(resolve_sig "$entries" "$sig")
      case "$full" in
        NONE) warn "resolved claim ${sig:0:8} ignored (not in store)"; continue;;
        MANY) warn "resolved claim ${sig:0:8} ignored (ambiguous prefix)"; continue;;
      esac
      entries=$(jq --arg s "$full" --arg now "$now" --arg sha "$hs" \
        'map(if .sig == $s then . + {history:(.history + [{at:$now, event:"claim", actor:"agent", note:"claimed in review", sha:$sha}])} else . end)' <<<"$entries")
    done <<<"$resolved"
  fi
  # Verification-gated flip: addressed requires verification.json evidence —
  # [{sig, verdict, verifyCmd, resultHead}] with verdict==VERIFIED and resultHead
  # matching HEAD. NOT VERIFIED reopens addressed entries; INCONCLUSIVE only logs.
  if [[ -f "$dir/verification.json" ]]; then
    while IFS= read -r v; do
      [[ -z "$v" ]] && continue
      vsig=$(jq -r '.sig // empty' <<<"$v"); verdict=$(jq -r '.verdict // empty' <<<"$v")
      vrh=$(jq -r '.resultHead // empty' <<<"$v"); vcmd=$(jq -r '.verifyCmd // empty' <<<"$v")
      [[ -z "$vsig" ]] && continue
      vfull=$(resolve_sig "$entries" "$vsig")
      case "$vfull" in
        NONE) warn "verification claim ${vsig:0:8} ignored (not in store)"; continue;;
        MANY) warn "verification claim ${vsig:0:8} ignored (ambiguous prefix)"; continue;;
      esac
      sha_ok="false"
      if [[ ${#vrh} -ge 7 && ${#head_full} -ge 7 ]] && { [[ "$head_full" == "$vrh" ]] || [[ "$head_full" == "$vrh"* ]] || [[ "$vrh" == "$head_full"* ]]; }; then sha_ok="true"; fi
      if [[ "$verdict" == "VERIFIED" && "$sha_ok" == "true" && -n "$vcmd" ]]; then
        entries=$(jq --arg s "$vfull" --arg now "$now" --arg sha "$hs" --arg cmd "$vcmd" \
          'map(if .sig == $s and .status != "addressed" then . + {status:"addressed", history:(.history + [{at:$now, event:"addressed", actor:"revalidator", note:$cmd, sha:$sha}])} else . end)' <<<"$entries")
        if [[ "$findings_file" != *.dryrun.json ]]; then
          kb_apply_credit "$vfull" "addressed" "revalidator" "$cmd" "$now" "$entries"
          if [[ "$KB_CREDITED_JSON" != "[]" ]]; then
            entries=$(jq --arg s "$vfull" --argjson kb "$KB_CREDITED_JSON" \
              'map(if .sig == $s then .history[-1] += {kb_credit:$kb} else . end)' <<<"$entries")
          fi
        fi
        vexit=$(jq -r '.verifyExit // empty' "$dir/patch.json" 2>/dev/null || true)
        if [[ -n "$vexit" && "$vexit" != "0" && "$vexit" != "null" ]]; then
          warn "VERIFIED claim for ${vfull:0:8} contradicts machine verify exit=$vexit — flip stands, contradiction recorded"
        else
          vto=$(jq -r '.timedOut // empty' "$dir/patch.json" 2>/dev/null || true)
          if [[ "$vto" == "true" ]]; then
            warn "VERIFIED claim for ${vfull:0:8} contradicts machine verify timedOut=true (exit=$vexit) — flip stands, contradiction recorded"
          fi
        fi
      elif [[ "$verdict" == "NOT VERIFIED" ]]; then
        entries=$(jq --arg s "$vfull" --arg now "$now" --arg sha "$hs" --arg cmd "$vcmd" \
          'map(if .sig == $s and .status == "addressed" then . + {status:"open", history:(.history + [{at:$now, event:"reopened", actor:"revalidator", note:$cmd, sha:$sha}])} elif .sig == $s then . + {history:(.history + [{at:$now, event:"revalidation", actor:"revalidator", note:$cmd, sha:$sha}])} else . end)' <<<"$entries")
      else
        entries=$(jq --arg s "$vfull" --arg now "$now" --arg sha "$hs" --arg cmd "$vcmd" --arg vd "$verdict" \
          'map(if .sig == $s then . + {history:(.history + [{at:$now, event:"revalidation", actor:"revalidator", note:($vd + if $cmd == "" then "" else ": " + $cmd end), sha:$sha}])} else . end)' <<<"$entries")
      fi
    done < <(jq -c '. // [] | .[]' "$dir/verification.json")
  fi
  # Triage claims: unified {sig, status, rationale}. Unknown enum refuses the post;
  # unknown sig warns; rationale <30 chars keeps prior status. One bad claim never fails.
  while IFS= read -r t; do
    [[ -z "$t" ]] && continue
    tsig=$(jq -r '.sig // empty' <<<"$t"); tst=$(jq -r '.status // empty' <<<"$t"); trat=$(jq -r '.rationale // ""' <<<"$t")
    [[ -z "$tsig" ]] && continue
    case "$tst" in open|addressed|wont-fix|false-positive|deferred) ;;
      *) fail "bad-triage-status" "refusing post: unknown triage status '$tst' (want open|addressed|wont-fix|false-positive|deferred)";; esac
    tfull=$(resolve_sig "$entries" "$tsig")
    case "$tfull" in
      NONE) warn "triage claim ${tsig:0:8} ignored (not in store)"; continue;;
      MANY) warn "triage claim ${tsig:0:8} ignored (ambiguous prefix)"; continue;;
    esac
    if (( ${#trat} < 30 )); then warn "triage claim without rationale, kept prior status: ${tsig:0:8}"; continue; fi
    entries=$(jq --arg s "$tfull" --arg st "$tst" --arg rat "$trat" --arg now "$now" --arg sha "$hs" \
      'map(if .sig == $s then . + {status:$st, rationale:$rat, triagedAt:$now, triagedBy:"agent", history:(.history + [{at:$now, event:"triaged", actor:"agent", note:$rat, sha:$sha}])} else . end)' <<<"$entries")
    case "$tst" in
      addressed|false-positive)
        if [[ "$findings_file" != *.dryrun.json ]]; then
          kb_apply_credit "$tfull" "$tst" "agent" "$cmd" "$now" "$entries"
          if [[ "$KB_CREDITED_JSON" != "[]" ]]; then
            entries=$(jq --arg s "$tfull" --argjson kb "$KB_CREDITED_JSON" \
              'map(if .sig == $s then .history[-1] += {kb_credit:$kb} else . end)' <<<"$entries")
          fi
        fi;;
    esac
  done < <(jq -c '.triage // [] | .[]' "$review_file")
  # Basis-line validation (P1 garnish): the agent-authored body may carry a
  # "> Basis:" counts line under the confidence lead; the review's own comments
  # decide (not the merged store, so carried-over findings never warn).
  # Mismatch or unparseable warns and keeps the body — never fails the run.
  validate_basis_line "$(review_basis_counts "$review_file" "$entries" "$now")" "$body"
  # Permalink garnish (P2): blob SHAs must name the reviewed commit and long
  # quotes stay inside collapsed details. Warn-and-keep, like the basis line.
  validate_evidence_links "$body" "$head_full" "$(evidence_linkable "$dir" "$head_full")"
  # Behavioral diagram (P4): grammar-level validation + opt-in gating of the
  # agent-drawn mermaid block. Warn-and-drop with a note, never a failure —
  # like the basis/evidence garnish above. Runs after the store derivation so
  # the record keeps everything the agent wrote; only the posted body is
  # curated here. Sets DIAGRAM_* for the dry-run envelope.
  validate_behavioral_diagram
  # Footer receipt (P1) with leveled attribution (P1b): one merged block at
  # the body end, computed from the owned stores (version, tier, cost,
  # files, link when known) plus the attribution line. Knob-off emits
  # nothing. Pre-post the review link is unknown, so it stays omitted —
  # never invented, never blank. Re-render is strip-and-replace on the
  # machine divider, so a body that already carries a rendered block comes
  # out byte-identical (no stacking).
  # Section overrides (P5): cheap post-time revalidation of the operator
  # fragments (locked-block integrity plus guard satisfiability — no re-render).
  # A stale edit fails closed here with the broken invariant named.
  validate_overrides
  footer_block=$(render_footer "$dir")
  if [[ -n "$footer_block" ]]; then
    if printf '%s' "$body" | grep -qF "$FOOTER_DIVIDER"; then
      warn "body already carries a rendered footer — replacing it with the canonical block"
      body=$(strip_rendered_footer <<<"$body")
    fi
    if printf '%s' "$body" | grep -qE '^[[:space:]]*> verdict `|^[[:space:]]*> .*Reviewed with \['; then
      warn "body already carries a footer receipt — keeping the agent block, script footer skipped"
    else
      body="$body

$footer_block"
    fi
  fi
  echo "$entries" | jq '.' > "$findings_file"
  kb_commit_staged "$findings_file" "$now"
  echo "» findings: $(jq 'length' <<<"$entries") sigs -> $findings_file"
  jq -r '.[] | "\(.sig[0:8])  \(.status)  \(.anchored)  \(.path // .heading // "?")"' <<<"$entries" | sed 's/^/  /'
  n_open=$(jq '[.[] | select(.status == "open")] | length' <<<"$entries")
  n_def=$(jq '[.[] | select(.status == "deferred")] | length' <<<"$entries")
  n_addr=$(jq '[.[] | select(.status == "addressed")] | length' <<<"$entries")
  n_term=$(jq '[.[] | select(.status == "wont-fix" or .status == "false-positive")] | length' <<<"$entries")
  marker="plain"
  if printf '%s' "$body" | grep -qE '^> ✅|^> ℹ️|^> \[!IMPORTANT\]|^> \[!CAUTION\]'; then marker="verdict"; fi
  hunk_n=$(wc -l < "$anchor_hunks" | tr -d ' ')
  if (( hunk_n == 0 )); then warn "no hunks indexed — anchors unchecked, everything kept"; fi
  # The per-chunk pinned hunks are a derived snapshot for this run only; the
  # fetch-time artifacts (chunk diffs, manifest, hunks.tsv) stay authoritative.
  if [[ "$anchor_hunks" != "$dir/hunks.tsv" ]]; then rm -f "$anchor_hunks"; fi
}

# Hunk index: file, side (NEW = posted file, OLD = base file), start, end — all 1-based inclusive.
build_hunks_tsv() { # $1 = full.diff, $2 = out
  awk '
    /^diff --git / { line=$0; sub(/^diff --git a\/.* b\//, "", line); file=line; next }
    /^rename to / { file=$0; sub(/^rename to /, "", file); next }
    /^@@ / {
      old=$2; new=$3; sub(/^-/, "", old); sub(/^\+/, "", new);
      split(old, o, ","); split(new, n, ",");
      os=o[1]; oc=(o[2]==""?1:o[2]); ns=n[1]; nc=(n[2]==""?1:n[2]);
      if (oc > 0) printf "%s\tOLD\t%d\t%d\n", file, os, os+oc-1;
      if (nc > 0) printf "%s\tNEW\t%d\t%d\n", file, ns, ns+nc-1;
    }' "$1" > "$2"
}

# Hunk index file: the fetch-time commentable snapshot. fetch/fetch-local
# pin it from the fetched diff (per-file, side-aware ranges); post/record
# validate against the pinned file — live HEAD movement is drift context
# only, never the validation target. Rebuilds only when the snapshot is
# missing (pre-snapshot bundles, hand-crafted fixtures): warns loudly that
# validation is best-effort, then rebuilds from the current full.diff.
ensure_hunks_snapshot() { # $1 = dir
  if [[ -f "$1/hunks.tsv" ]]; then return 0; fi
  warn "no fetch-time hunk snapshot ($1/hunks.tsv missing) — rebuilding from full.diff, validation is best-effort (re-fetch to pin the snapshot)"
  build_hunks_tsv "$1/full.diff" "$1/hunks.tsv"
}

# Roles for a newline-separated path list on stdin (grouped, triage order).
roles_for_paths() {
  awk '
    function role(p, b) {
      n = split(p, s, "/"); b = s[n];
      if (p ~ /(^|\/)(test|tests|__tests__|e2e|spec)(s)?\// || b ~ /^(test_|spec_)/ || b ~ /[._](test|spec)\./) return "test";
      if (p ~ /package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lock|Cargo\.lock|Gemfile\.lock|poetry\.lock|\.min\.(js|css)|(^(dist|build|vendor)\/)|__snapshots__\/|\.snap$|\.pb\.go$|\.g\.dart$|\.generated\./) return "generated";
      if (b ~ /\.md$|\.txt$|^LICENSE/ || p ~ /^docs\//) return "docs";
      if (b ~ /^(package\.json|tsconfig.*|Dockerfile.*|Makefile|CMakeLists\.txt|pom\.xml|pyproject\.toml|go\.mod|go\.sum|Cargo\.toml|Gemfile)/ || b ~ /^\..*rc/ || b ~ /\.(yml|yaml|toml)$/) return "config";
      return "source";
    }
    { print role($0) "\t" $0 }' | sort | awk -F'\t' '
    $1 != prev { if (NR > 1) print ""; print "## " $1; prev = $1 } { print "- " $2 }'
}

# Panel pin + learnings pointer (operator-owned context, same for PR and local
# bundles). Sets globals panel_src/panel_list for the caller's envelope.
emit_context() { # $1 = dir
  local dir="$1"
  panel_src=""; panel_list=""
  if [[ -n "${REVIEW_PANEL:-}" ]]; then
    panel_list=$(printf '%s' "$REVIEW_PANEL" | tr ', ' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' || true)
    panel_src="REVIEW_PANEL"
  elif [[ -f .review/panel ]]; then
    panel_list=$(grep -v '^[[:space:]]*#' .review/panel | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' || true)
    panel_src=".review/panel"
  fi
  if [[ -n "$panel_list" ]]; then
    printf '%s\n' "$panel_list" > "$dir/panel"
    echo "» panel ($(printf '%s\n' "$panel_list" | wc -l | tr -d ' ') from $panel_src)"
  elif [[ "${EFFECTIVE_COMPACT:-false}" != "true" ]]; then
    echo "» panel: none pinned (set REVIEW_PANEL or .review/panel for comparable, cost-capped runs)"
  fi
  if [[ -f .review/learnings.md && "${EFFECTIVE_COMPACT:-false}" != "true" ]]; then
    echo "» learnings: .review/learnings.md ($(wc -l < .review/learnings.md | tr -d ' ') lines — read headings first, not the full file)"
  fi
}

# Prompt dump (--show-prompts): render the assembled agent reading list to
# stdout in documented order (SKILL.md, system, mode, diagrams fragment,
# bundle meta, coverage
# manifest, roles, diff, recall, advisories, panel). Pure-local reads only:
# no agent runs, no network, no store writes. Missing/optional files are
# skipped silently, so the dump is exactly the concatenation of what exists —
# except a chunked bundle with an unreadable snapshot, which warns loudly
# before falling back to full.diff.
render_chunk_diffs() { # $1 = bundle dir; cats chunk diffs in manifest order; nonzero when not chunked
  local dir="$1" man="$1/chunks/manifest.json" d=""
  [[ -f "$man" ]] || return 1
  d=$(jq -r '.chunks | sort_by(.index) | .[].diff // empty' "$man" 2>/dev/null) || return 1
  [[ -n "$d" ]] || return 1
  while IFS= read -r f; do
    [[ -n "$f" && -f "$f" ]] || return 1
  done <<<"$d"
  while IFS= read -r f; do
    cat -- "$f"
  done <<<"$d"
}

render_prompt_dump() { # $1 = bundle dir
  local dir="$1" meta="$1/PR.json" f miss
  [[ -f "$1/source.json" ]] && meta="$1/source.json"
  for f in SKILL.md prompts/system.md prompts/review.md prompts/diagrams.md "$meta" "$dir/toc.txt" "$dir/roles.txt"; do
    [[ -f "$f" ]] || continue
    cat -- "$f"
  done
  # Diff section: a chunked bundle concatenates the chunk diffs in manifest
  # order (one ordered document, byte-equal to full.diff); anything else
  # reads full.diff exactly as today, so non-chunked dumps stay byte-identical.
  if ! render_chunk_diffs "$dir"; then
    if [[ -f "$dir/chunks/manifest.json" ]]; then
      miss=""
      while IFS= read -r f; do
        if [[ -z "$f" || ! -f "$f" ]]; then miss="${f:-(no readable chunk diff ref)}"; break; fi
      done < <(jq -r '.chunks | sort_by(.index) | .[].diff // empty' "$dir/chunks/manifest.json" 2>/dev/null || true)
      warn "chunk snapshot '$miss' unreadable — prompt dump falls back to $dir/full.diff (post/record still refuse the broken bundle; re-fetch with --chunk to restore the pinned snapshots)"
    fi
    [[ -f "$dir/full.diff" ]] && cat -- "$dir/full.diff"
  fi
  for f in "$dir/kb.txt" "$dir/sensitive.txt" "$dir/panel"; do
    [[ -f "$f" ]] || continue
    cat -- "$f"
  done
}

# Cost control plane: operator spend budget + pre-run estimate + consent gate.
# Env-first like REVIEW_PANEL: REVIEW_BUDGET_USD wins, else the first usable
# line of .review/budget (plain USD number, # comments and blanks skipped).
# Unset (neither present) disables the whole plane — no estimate line, no
# gate, no receipts — so default runs stay byte-identical to today.
# Estimate: mean est_usd of the most-run effort tier across comparable past
# runs (every .review/*/cost.json record, same tier rules as report --cost);
# ties break toward the deeper (costlier) tier. No history, or no priced run
# in the projected tier, reads as unknown — never invented, never blocking.
# A numeric projection over budget refuses (typed budget-exceeded) unless the
# run carries --consent-over-budget; consented runs append one receipt to the
# bundle's budget.json, which report --cost surfaces.
resolve_budget() { # sets budget_usd ("" when unset), budget_src
  budget_usd=""; budget_src=""
  if [[ -n "${REVIEW_BUDGET_USD:-}" ]]; then
    budget_usd=$(printf '%s' "$REVIEW_BUDGET_USD" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    budget_src="REVIEW_BUDGET_USD"
  elif [[ -f .review/budget ]]; then
    budget_usd=$(grep -v '^[[:space:]]*#' .review/budget | grep -v '^[[:space:]]*$' | head -n 1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^\$//' || true)
    [[ -n "$budget_usd" ]] && budget_src=".review/budget"
  fi
  [[ -z "$budget_usd" ]] && return 0
  [[ "$budget_usd" =~ ^[0-9]+(\.[0-9]+)?$ ]] || fail "bad-budget" "budget '$budget_usd' from $budget_src is not a plain USD number (want e.g. REVIEW_BUDGET_USD=0.50 or a .review/budget file holding 0.50)"
}

cost_estimate() { # prints the » estimate line; sets est_tier/est_usd ("null" when unknown)/est_runs
  est_tier=""; est_usd="null"; est_runs=0
  recs=""; bad=""
  for f in .review/*/cost.json; do
    [[ -f "$f" ]] || continue
    if r=$(jq -c '(if type == "array" then .[] else . end)
      | {tier: ((.effort // "standard" | tostring | ascii_downcase)
        | if . == "cheap" or . == "standard" or . == "deep" then . else "standard" end),
        usd: (if (.est_usd | type) == "number" then .est_usd else null end)}' "$f" 2>/dev/null); then
      recs="$recs$r
"
    else
      bad="$bad$f "
    fi
  done
  [[ -n "$bad" ]] && warn "cost history unreadable — skipped from estimate: $bad"
  summary=$(printf '%s' "$recs" | jq -s '
    map(select(.tier == "cheap" or .tier == "standard" or .tier == "deep")) as $r
    | {runs: ($r | length),
       pick: (["deep","standard","cheap"] | map(. as $t | {tier: $t,
         runs: ([$r[] | select(.tier == $t)] | length),
         mean: ([($r[] | select(.tier == $t and (.usd | type) == "number") | .usd)]
           | if length == 0 then null else ((add / length * 10000 | round) / 10000) end)})
         | max_by([.runs, (.mean // -1)]))}')
  est_runs=$(jq -r '.runs // 0' <<<"$summary")
  case "$est_runs" in ''|*[!0-9]*) est_runs=0;; esac
  if (( est_runs == 0 )); then
    echo "» estimate: no comparable past runs — cost unknown (no usable .review/*/cost.json records yet)"
    return 0
  fi
  est_tier=$(jq -r '.pick.tier' <<<"$summary")
  tier_runs=$(jq -r '.pick.runs' <<<"$summary")
  mean=$(jq -r '.pick.mean' <<<"$summary")
  if [[ "$mean" == "null" ]]; then
    echo "» estimate: tier $est_tier from $tier_runs comparable run(s) — projected cost unknown (none reported est_usd); budget \$$budget_usd ($budget_src) unenforced on unknown"
    return 0
  fi
  est_usd="$mean"
  if awk -v p="$est_usd" -v b="$budget_usd" 'BEGIN{exit !(p > b)}'; then
    echo "» estimate: tier $est_tier ~\$$est_usd/run from $est_runs comparable run(s) — OVER budget \$$budget_usd ($budget_src)"
  else
    echo "» estimate: tier $est_tier ~\$$est_usd/run from $est_runs comparable run(s) — within budget \$$budget_usd ($budget_src)"
  fi
}

budget_gate() { # $1 = "true" when --consent-over-budget passed; refuses over-budget runs without consent
  estimate_json="null"; budget_json="null"; budget_receipt_pending="false"
  resolve_budget
  [[ -z "$budget_usd" ]] && return 0
  cost_estimate
  budget_json=$(jq -n --argjson b "$budget_usd" --arg s "$budget_src" --argjson c "$1" \
    '{usd: $b, source: $s, consented: $c}')
  estimate_json=$(jq -n --arg t "$est_tier" --argjson u "$est_usd" --argjson n "$est_runs" \
    '{tier: (if $t == "" then null else $t end), projected_usd: $u, comparable_runs: $n}')
  [[ "$est_usd" == "null" ]] && return 0
  if awk -v p="$est_usd" -v b="$budget_usd" 'BEGIN{exit !(p > b)}'; then
    if [[ "$1" == "true" ]]; then
      budget_receipt_pending="true"
    else
      fail "budget-exceeded" "projected $est_tier run ~\$$est_usd exceeds budget \$$budget_usd ($budget_src) from $est_runs comparable run(s) — consent required before this run starts"
    fi
  fi
}

write_budget_receipt() { # $1 = bundle dir; no-op unless a consented over-budget gate passed
  [[ "${budget_receipt_pending:-false}" == "true" ]] || return 0
  rc=$(jq -n --arg t "$(now_utc)" --argjson b "$budget_usd" --arg s "$budget_src" \
    --arg tier "$est_tier" --argjson p "$est_usd" --argjson n "$est_runs" \
    '{at: $t, budget_usd: $b, budget_src: $s, projected_tier: $tier, projected_usd: $p, comparable_runs: $n, consent: "--consent-over-budget"}')
  if [[ -f "$1/budget.json" ]]; then
    if merged=$(jq --argjson r "$rc" 'if type == "array" then . + [$r] else [$r] end' "$1/budget.json" 2>/dev/null); then
      printf '%s\n' "$merged" > "$1/budget.json"
    else
      warn "budget.json unreadable — starting a fresh receipt ledger"
      printf '%s\n' "[$rc]" > "$1/budget.json"
    fi
  else
    printf '%s\n' "[$rc]" > "$1/budget.json"
  fi
  echo "» receipt: consented over-budget run recorded in $1/budget.json (projected \$$est_usd $est_tier vs budget \$$budget_usd)"
}

# Sensitive-path tripwire (advisory only — never blocks, never redacts evidence).
# Scans $dir/full.diff plus a newline-separated metadata path list for
# secret-adjacent bundle paths and lists hits in $dir/sensitive.txt, one
# "path (reasons)" line each, with a single stderr warning naming the first
# few ("and N more" past five). No hits: no file, no warning — clean trees
# stay silent. Three signal sets: suspicious filenames (12-name set on the
# lowercased basename; .env.* held unless the suffix is example/sample/
# template/dist/defaults), suspicious extensions (pem key p12 pfx keystore jks
# ppk), and added-line content matching known secret shapes (PEM blocks, AWS
# keys, ghp_/xox-/AIza/Stripe/Anthropic/OpenAI key formats). Old paths are
# checked too — renames surface both sides in diff headers, deletions via the
# metadata list — everywhere the bundle format makes them visible. Never fails.
sens_path_reasons() { # $1 = path — prints filename/extension reasons, one per line
  local b lb sfx e
  b=${1##*/}
  [[ -n "$b" && "$b" != "/dev/null" ]] || return 0
  lb=$(printf '%s' "$b" | tr '[:upper:]' '[:lower:]')
  case "$lb" in
    .env|.envrc|.netrc|.npmrc|.pypirc|.pgpass|.htpasswd|credentials|id_rsa|id_dsa|id_ecdsa|id_ed25519)
      echo "filename";;
    .env.*)
      sfx=${lb#.env.}
      case "$sfx" in example|sample|template|dist|defaults) ;; *) echo "filename";; esac;;
  esac
  case "$lb" in
    *.*)
      e=${lb##*.}
      case "$e" in pem|key|p12|pfx|keystore|jks|ppk) echo "extension";; esac;;
  esac
  return 0
}

sens_content_hit() { # secret shapes over added diff lines on stdin — 0 on hit
  grep -E -e '-----BEGIN (RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----' \
    -e '(AKIA|ASIA)[A-Z0-9]{16}' \
    -e 'gh[pousr]_[A-Za-z0-9]{36,}' \
    -e 'xox[baprs]-[A-Za-z0-9-]{10,}' \
    -e 'AIza[A-Za-z0-9_-]{35}' \
    -e '(sk|rk)_(live|test)_[A-Za-z0-9]{16,}' \
    -e 'sk-ant-[A-Za-z0-9_-]{20,}' \
    -e 'sk-(proj|svcacct|admin)-[A-Za-z0-9]{20,}' \
    -e 'sk-[A-Za-z0-9]{40}' >/dev/null
}

sens_emit_section() { # $1 = diff, $2 = first line, $3 = last line — prints path<TAB>reason hits
  local hline hdr old new rest tmp added r p
  hline=$(sed -n "$2p" "$1")
  hdr=${hline#diff --git }
  if [[ "$hdr" == '"'* ]]; then
    tmp=${hdr#\"}; old=${tmp%%\"*}; old=${old#a/}
    tmp=${tmp#*\" }; tmp=${tmp#\"}; new=${tmp%\"}; new=${new#b/}
  else
    old=${hdr%% *}; old=${old#a/}
    rest=${hdr#* }; new=${rest#b/}
  fi
  for p in "$old" "$new"; do
    [[ -n "$p" && "$p" != "/dev/null" ]] || continue
    sens_path_reasons "$p" | while IFS= read -r r; do printf '%s\t%s\n' "$p" "$r"; done
  done
  if [[ "$3" -ge "$2" ]]; then
    added=$(sed -n "$(( $2 + 1 )),$3p" "$1" | grep '^+' || true)
    if [[ -n "$added" ]] && printf '%s\n' "$added" | sens_content_hit; then
      printf '%s\t%s\n' "$new" "content"
    fi
  fi
  return 0
}

write_sensitive_manifest() { # $1 = dir, $2 = newline-separated extra paths (may be empty)
  local dir="$1" extra="${2:-}" mf="$1/sensitive.txt"
  local raw="" flagged="" count="0" shown="" p r start prev total
  [[ -f "$dir/full.diff" ]] || return 0
  raw=$(
    if [[ -n "$extra" ]]; then
      while IFS= read -r p; do
        [[ -z "$p" ]] && continue
        sens_path_reasons "$p" | while IFS= read -r r; do printf '%s\t%s\n' "$p" "$r"; done
      done <<<"$extra"
    fi
    prev=""
    while IFS= read -r start; do
      [[ -z "$start" ]] && continue
      if [[ -n "$prev" && "$start" -gt "$prev" ]]; then
        sens_emit_section "$dir/full.diff" "$prev" "$((start - 1))"
      fi
      prev="$start"
    done <<<"$(grep -n '^diff --git ' "$dir/full.diff" | cut -d: -f1 || true)"
    if [[ -n "$prev" ]]; then
      total=$(wc -l < "$dir/full.diff" | tr -d ' ')
      sens_emit_section "$dir/full.diff" "$prev" "$total"
    fi
    :
  )
  [[ -n "$raw" ]] || { rm -f "$mf"; return 0; }
  flagged=$(printf '%s\n' "$raw" | awk -F'\t' '
    NF >= 2 && $1 != "" && $1 != "/dev/null" {
      if (index("," r[$1] ",", "," $2 ",") == 0) r[$1] = (r[$1] == "" ? $2 : r[$1] "," $2)
    }
    END { for (p in r) print p " (" r[p] ")" }' | LC_ALL=C sort || true)
  [[ -n "$flagged" ]] || { rm -f "$mf"; return 0; }
  {
    echo "# sensitive-path manifest — advisory only, not a gate."
    echo "# Flagged paths stay reviewable evidence; never quote flagged values"
    echo "# into posted review bodies — name the path, not the secret."
    printf '%s\n' "$flagged"
  } > "$mf"
  count=$(printf '%s\n' "$flagged" | grep -c . || true)
  shown=$(printf '%s\n' "$flagged" | head -n 5 | awk '{ if (NR > 1) printf ", "; printf "%s", $0 } END { printf "\n" }')
  if [[ "$count" -gt 5 ]]; then
    warn "sensitive paths in bundle (see $mf): $shown, and $((count - 5)) more"
  else
    warn "sensitive paths in bundle (see $mf): $shown"
  fi
  return 0
}

# Structured local knowledge base (operator-owned, repo-local).
# Store: .review/kb.jsonl, one JSON object per line:
# {id, rule, scope, provenance, rigor, hits, false_hits, judged, status}
# (retired rows keep the line and gain retiredAt/retireReason tombstones plus
# a history log). Audit: every credit appends one .review/kb-joins.jsonl row
# {at, sig, rule, outcome, actor, verb, bundle, review_id}. Recall (write_kb_recall) mirrors the
# sensitive manifest: advisory context files .review/<bundle>/kb.txt (human note)
# plus .review/<bundle>/kb-recall.json (machine manifest, rank-ordered ids with
# scores) only when active rules match, never a gate, never redacting evidence.
KB_FILE=".review/kb.jsonl"
KB_JOINS=".review/kb-joins.jsonl"
KB_POLICY=".review/kb-policy"
# Corrupt-store short-circuit for a run (warned once); recall/credit then
# skip instead of warning per element.
KB_BROKEN="false"
# Staged (rule, sig, outcome) credit ops for this run; kb_commit_staged
# applies them after the findings file carrying their flips persists.
KB_STAGED="[]"

# slugify rule text -> candidate id (lowercase, non-alnum runs to one dash,
# trimmed, capped at 65 chars so the id regex always holds).
kb_slugify() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' \
    | sed 's/[^a-z0-9][^a-z0-9]*/-/g; s/^-\+//; s/-\+$//' | cut -c1-65
}

# Retirement thresholds: env wins, else .review/kb-policy key=value lines
# (MIN_JUDGED / MAX_FP_RATE, REVIEW_KB_-prefixed aliases accepted), else
# defaults (5 judged, 0.5 false-hit rate). An explicitly-set-but-invalid
# value warns and falls through to the next source (file, then default) —
# never fails the flip. With no kb.jsonl and nothing explicitly set, resolve
# silently to defaults (no warnings on a default path). Sets
# KB_MIN_JUDGED / KB_MAX_FP_RATE.
kb_resolve_policy() {
  local raw="" min_from_env="false" rate_from_env="false"
  KB_MIN_JUDGED="5"; KB_MAX_FP_RATE="0.5"
  if [[ ! -f "$KB_FILE" && -z "${REVIEW_KB_MIN_JUDGED:-}" && -z "${REVIEW_KB_MAX_FP_RATE:-}" ]]; then return 0; fi
  if [[ -n "${REVIEW_KB_MIN_JUDGED:-}" ]]; then
    raw=$(printf '%s' "$REVIEW_KB_MIN_JUDGED" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    if [[ "$raw" =~ ^[0-9]+$ ]] && (( 10#$raw > 0 )); then KB_MIN_JUDGED="$raw"; min_from_env="true";
    else warn "ignoring invalid REVIEW_KB_MIN_JUDGED '$REVIEW_KB_MIN_JUDGED' (want a positive integer) — trying file, then default"; fi
  fi
  if [[ "$min_from_env" == "false" && -f "$KB_POLICY" ]]; then
    raw=$(grep -E '^(REVIEW_KB_MIN_JUDGED|MIN_JUDGED)=' "$KB_POLICY" 2>/dev/null | tail -n 1 | cut -d= -f2- | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)
    if [[ -n "$raw" ]]; then
      if [[ "$raw" =~ ^[0-9]+$ ]] && (( 10#$raw > 0 )); then KB_MIN_JUDGED="$raw";
      else warn "ignoring invalid MIN_JUDGED '$raw' in $KB_POLICY (want a positive integer) — default 5"; fi
    fi
  fi
  if [[ -n "${REVIEW_KB_MAX_FP_RATE:-}" ]]; then
    raw=$(printf '%s' "$REVIEW_KB_MAX_FP_RATE" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    if [[ "$raw" =~ ^[0-9]*\.?[0-9]+$ ]] && awk -v m="$raw" 'BEGIN{exit !(m >= 0 && m <= 1)}'; then KB_MAX_FP_RATE="$raw"; rate_from_env="true";
    else warn "ignoring invalid REVIEW_KB_MAX_FP_RATE '$REVIEW_KB_MAX_FP_RATE' (want 0..1) — trying file, then default"; fi
  fi
  if [[ "$rate_from_env" == "false" && -f "$KB_POLICY" ]]; then
    raw=$(grep -E '^(REVIEW_KB_MAX_FP_RATE|MAX_FP_RATE)=' "$KB_POLICY" 2>/dev/null | tail -n 1 | cut -d= -f2- | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)
    if [[ -n "$raw" ]]; then
      if [[ "$raw" =~ ^[0-9]*\.?[0-9]+$ ]] && awk -v m="$raw" 'BEGIN{exit !(m >= 0 && m <= 1)}'; then KB_MAX_FP_RATE="$raw";
      else warn "ignoring invalid MAX_FP_RATE '$raw' in $KB_POLICY (want 0..1) — default 0.5"; fi
    fi
  fi
}

# kb_normalize_entry <entry-json> — set KB_NORM_JSON to the normalized kb id
# list (JSON array, max 5): absent/null reads null (no re-cite — the merge
# keeps the prior cite), explicit [] reads [] (uncite — the merge clears);
# present non-empty arrays normalize per element with malformed/unknown/
# retired ids warned-and-dropped (mirrors the confidence warn-and-drop
# precedent). Same-process by design: callers need the warns in the envelope
# (warns inside $() are lost to the parent).
kb_normalize_entry() {
  local e="$1" t="" out="[]" n=0 j="" cid="" st="" trunc="false"
  KB_NORM_JSON="[]"
  if ! jq -e 'has("kb") and (.kb != null)' <<<"$e" >/dev/null 2>&1; then
    KB_NORM_JSON="null"
    return 0
  fi
  [[ "$KB_BROKEN" == "true" ]] && return 0
  t=$(jq -c '.kb // []' <<<"$e" 2>/dev/null || echo "BROKEN")
  if [[ "$t" == "BROKEN" ]] || ! jq -e 'type == "array"' <<<"$t" >/dev/null 2>&1; then
    if jq -e 'has("kb") and (.kb != null)' <<<"$e" >/dev/null 2>&1; then
      warn "ignoring malformed kb (want an array of rule ids)"
    fi
    return 0
  fi
  while IFS= read -r j; do
    [[ -z "$j" ]] && continue
    cid=$(jq -r 'if type == "string" then . else empty end' <<<"$j" 2>/dev/null || true)
    if [[ -z "$cid" ]]; then
      warn "ignoring malformed kb id (want a rule id string)"; continue
    fi
    if [[ ! "$cid" =~ ^[a-z0-9][a-z0-9-]{0,64}$ ]]; then
      warn "ignoring malformed kb id '$cid' (want ^[a-z0-9][a-z0-9-]{0,64}\$)"; continue
    fi
    if [[ ! -f "$KB_FILE" ]]; then
      warn "ignoring unknown kb rule '$cid' (no $KB_FILE yet)"; continue
    fi
    st=$(jq -s --arg id "$cid" -r '[.[] | select(.id == $id)][0] | if . == null then "MISSING" else .status end' "$KB_FILE" 2>/dev/null || echo "UNREADABLE")
    case "$st" in
      active) ;;
      retired) warn "ignoring retired kb rule '$cid' (restore it before citing)"; continue;;
      MISSING) warn "ignoring unknown kb rule '$cid'"; continue;;
      *) warn "kb store unreadable — ignoring cited rule '$cid'"; continue;;
    esac
    if jq -e --arg id "$cid" 'index($id) != null' <<<"$out" >/dev/null 2>&1; then continue; fi
    if (( n >= 5 )); then
      if [[ "$trunc" == "false" ]]; then warn "kb list truncated to 5 ids (extras dropped)"; trunc="true"; fi
      continue
    fi
    out=$(jq -c --arg id "$cid" '. + [$id]' <<<"$out"); n=$((n + 1))
  done < <(jq -c '.[]' <<<"$t" 2>/dev/null || true)
  KB_NORM_JSON="$out"
}

# kb_apply_credit <sig> <outcome> <actor> <verb> <now> <entries-json> —
# STAGE a credit for every cited rule not already credited for this finding.
# Staging is memory-only (appends ops to KB_STAGED); the caller stamps
# KB_CREDITED_JSON ([{rule, to}] objects) into the flipping history event
# in-memory, and kb_commit_staged applies everything after the findings file
# persists — so a crash between staging and persistence never mints phantom
# credit. Idempotency key is (rule, sig, outcome): the join log covers
# adopted twins across bundles, the history stamp covers same-run retries.
# Sets KB_CREDITED_JSON (rules actually staged). Never fails the flip.
kb_apply_credit() {
  local sig="$1" outcome="$2" actor="$3" verb="$4" now="$5" entries="$6"
  local cited="" rid="" dup=""
  KB_CREDITED_JSON="[]"
  [[ "$KB_BROKEN" == "true" ]] && return 0
  [[ -f "$KB_FILE" ]] || return 0
  case "$outcome" in addressed|false-positive) ;; *) return 0;; esac
  if [[ -f "$KB_JOINS" ]] && ! jq -s empty "$KB_JOINS" >/dev/null 2>&1; then
    warn "kb joins $KB_JOINS unreadable — credit skipped for this flip (fix or remove the file)"
    return 0
  fi
  cited=$(jq -r --arg s "$sig" '.[] | select(.sig == $s) | ((.kb // [])[])' <<<"$entries" 2>/dev/null || true)
  [[ -z "$cited" ]] && return 0
  while IFS= read -r rid; do
    [[ -z "$rid" ]] && continue
    dup="false"
    if [[ -f "$KB_JOINS" ]]; then
      dup=$(jq -s --arg s "$sig" --arg r "$rid" --arg o "$outcome" -e 'map(select(.sig == $s and .rule == $r and .outcome == $o)) | length > 0' "$KB_JOINS" >/dev/null 2>&1 && echo "true" || echo "false")
    fi
    if [[ "$dup" == "false" ]]; then
      dup=$(jq --arg s "$sig" --arg r "$rid" --arg o "$outcome" -e '[.[] | select(.sig == $s) | (.history // [])[] | select(.kb_credit != null) | (.kb_credit[]? | select(type == "object" and .rule == $r and .to == $o))] | length > 0' <<<"$entries" >/dev/null 2>&1 && echo "true" || echo "false")
    fi
    if [[ "$dup" == "true" ]]; then continue; fi
    if ! jq -s --arg id "$rid" -e '[.[] | select(.id == $id)][0] | .status == "active"' "$KB_FILE" >/dev/null 2>&1; then
      warn "kb credit skipped: rule '$rid' not active in $KB_FILE"; continue
    fi
    KB_STAGED=$(jq -c --arg s "$sig" --arg r "$rid" --arg o "$outcome" --arg a "$actor" --arg v "$verb" --arg t "$now" \
      '. + [{sig:$s, rule:$r, outcome:$o, actor:$a, verb:$v, at:$t}]' <<<"$KB_STAGED")
    KB_CREDITED_JSON=$(jq -c --arg r "$rid" --arg o "$outcome" '. + [{rule:$r, to:$o}]' <<<"$KB_CREDITED_JSON")
  done <<<"$cited"
}

# kb_strip_stamps <findings-file> — remove staged kb_credit stamps so a later
# re-flip can retry dropped credit. Best-effort recovery path only (commit
# failure already warned loudly). Never fails.
kb_strip_stamps() {
  local findings_file="$1" tmp=""
  [[ "$KB_STAGED" == "[]" ]] && return 0
  [[ -f "$findings_file" ]] || return 0
  tmp="$findings_file.tmp.$$"
  if jq --argjson ops "$KB_STAGED" '
      ($ops | map(.sig) | unique) as $sigs
      | ($ops | map({key: (.rule + "" + .outcome), value: true}) | from_entries) as $drop
      | map(. as $e
        | if ([$sigs[] | select(. == $e.sig)] | length) == 0 then .
          else .history |= map(
            if .kb_credit == null then .
            else (.kb_credit |= [ .[] | select(type != "object" or (($drop[(.rule // "") + "" + (.to // "")] // false) | not)) ])
                 | if (.kb_credit | length) == 0 then del(.kb_credit) else . end
            end)
          end)' \
      "$findings_file" > "$tmp" 2>/dev/null; then
    mv "$tmp" "$findings_file"
  else
    rm -f "$tmp"; warn "kb recovery skipped: could not rewrite $findings_file (staged stamps kept — credit for these flips is spent)"
  fi
}

# kb_commit_staged <findings-file> <now> — apply staged KB ops AFTER the
# findings file carrying their flips has persisted. One kb.jsonl rewrite
# (counters + retirements), then join rows, then retirement lines. Join rows
# are re-checked per op (crash-retry safety: a retried flip finds either its
# join row or its history stamp and skips). On failure warns loudly and
# strips the staged stamps so a later re-flip can retry. Never fails the flip.
kb_commit_staged() {
  local findings_file="$1" now="$2" tmp="" live="" op="" rid="" sig="" outcome=""
  local bundle_dir="" bundle="" review_id_json="null"
  [[ "$KB_STAGED" == "[]" ]] && return 0
  [[ "$KB_BROKEN" == "true" ]] && { KB_STAGED="[]"; return 0; }
  if [[ ! -f "$KB_FILE" ]] || ! jq -s empty "$KB_FILE" >/dev/null 2>&1; then
    warn "kb store $KB_FILE unreadable at commit — staged credit dropped (flips stand); re-run the flip to retry"
    kb_strip_stamps "$findings_file"; KB_STAGED="[]"; return 0
  fi
  live="[]"
  while IFS= read -r op; do
    [[ -z "$op" ]] && continue
    rid=$(jq -r '.rule' <<<"$op"); sig=$(jq -r '.sig' <<<"$op"); outcome=$(jq -r '.outcome' <<<"$op")
    if [[ -f "$KB_JOINS" ]] && jq -s --arg s "$sig" --arg r "$rid" --arg o "$outcome" -e 'map(select(.sig == $s and .rule == $r and .outcome == $o)) | length > 0' "$KB_JOINS" >/dev/null 2>&1; then
      continue
    fi
    live=$(jq -c --argjson op "$op" '. + [$op]' <<<"$live")
  done < <(jq -c '.[]' <<<"$KB_STAGED" 2>/dev/null || true)
  if [[ "$live" == "[]" ]]; then KB_STAGED="[]"; return 0; fi
  # Audit scope (additive): every joins row carries the bundle dir basename
  # plus post.json's review.id when that file exists (local bundles read
  # null). Best-effort — resolution never fails the credit.
  bundle_dir=$(dirname "$findings_file")
  bundle=$(basename "$bundle_dir")
  if [[ -f "$bundle_dir/post.json" ]]; then
    review_id_json=$(jq -c '.review.id // null' "$bundle_dir/post.json" 2>/dev/null || echo "null")
  fi
  [[ -z "$review_id_json" ]] && review_id_json="null"
  tmp="$KB_FILE.tmp.$$"
  if ! jq -c -s --argjson ops "$live" '
      (($ops | map(select(.outcome == "addressed")) | sort_by(.rule) | group_by(.rule) | map({key:.[0].rule, value:length}) | from_entries) // {}) as $hits
      | (($ops | map(select(.outcome == "false-positive")) | sort_by(.rule) | group_by(.rule) | map({key:.[0].rule, value:length}) | from_entries) // {}) as $fps
      | map(. as $row
        | (($hits[$row.id] // 0) as $h | ($fps[$row.id] // 0) as $f
        | if ($h + $f) > 0
          then . + {hits: ((.hits // 0) + $h), false_hits: ((.false_hits // 0) + $f), judged: ((.judged // 0) + $h + $f)}
          else . end)) | .[]' \
      "$KB_FILE" > "$tmp" 2>/dev/null; then
    rm -f "$tmp"
    warn "kb commit failed: could not rewrite $KB_FILE (flips stand, staged credit dropped) — re-run the flip to retry"
    kb_strip_stamps "$findings_file"; KB_STAGED="[]"; return 0
  fi
  mv "$tmp" "$KB_FILE"
  while IFS= read -r op; do
    [[ -z "$op" ]] && continue
    rid=$(jq -r '.rule' <<<"$op"); sig=$(jq -r '.sig' <<<"$op"); outcome=$(jq -r '.outcome' <<<"$op")
    jq -n -c --arg t "$now" --arg s "$sig" --arg r "$rid" --arg o "$outcome" --arg a "$(jq -r '.actor' <<<"$op")" --arg v "$(jq -r '.verb' <<<"$op")" \
      --arg b "$bundle" --argjson review_id "$review_id_json" \
      '{at:$t, sig:$s, rule:$r, outcome:$o, actor:$a, verb:$v, bundle:$b, review_id:$review_id}' >> "$KB_JOINS" 2>/dev/null \
      || warn "kb audit row for '$rid' not written ($KB_JOINS)"
  done < <(jq -c '.[]' <<<"$live" 2>/dev/null || true)
  while IFS= read -r rid; do
    [[ -z "$rid" ]] && continue
    kb_maybe_retire "$rid" "$now"
  done < <(jq -r '.[] | .rule' <<<"$live" 2>/dev/null | LC_ALL=C sort -u || true)
  KB_STAGED="[]"
}

# kb_maybe_retire <rule-id> <now> — tombstone an active rule whose false-hit
# rate exceeds the explicit threshold. Warns (never fails). Single rule.
kb_maybe_retire() {
  local rid="$1" now="$2" tmp="" new_fp="" new_judged="" st="" reason="" rate=""
  new_fp=$(jq -s --arg id "$rid" -r '[.[] | select(.id == $id)][0] | .false_hits' "$KB_FILE" 2>/dev/null || echo "x")
  new_judged=$(jq -s --arg id "$rid" -r '[.[] | select(.id == $id)][0] | .judged' "$KB_FILE" 2>/dev/null || echo "x")
  st=$(jq -s --arg id "$rid" -r '[.[] | select(.id == $id)][0] | .status' "$KB_FILE" 2>/dev/null || echo "x")
  [[ "$st" == "active" && "$new_fp" =~ ^[0-9]+$ && "$new_judged" =~ ^[0-9]+$ ]] || return 0
  if (( 10#$new_judged >= 10#$KB_MIN_JUDGED )) \
    && awk -v f="$new_fp" -v j="$new_judged" -v m="$KB_MAX_FP_RATE" 'BEGIN{exit !(j > 0 && f / j > m)}'; then
    rate=$(awk -v f="$new_fp" -v j="$new_judged" 'BEGIN{printf "%.2f", f / j}')
    reason="auto-retired: false-hit rate $new_fp/$new_judged ($rate) exceeds $KB_MAX_FP_RATE at judged >= $KB_MIN_JUDGED"
    tmp="$KB_FILE.tmp.$$"
    if jq -c -s --arg id "$rid" --arg t "$now" --arg r "$reason" \
        'map(if .id == $id then . + {status:"retired", retiredAt:$t, retireReason:$r, history:((.history // []) + [{at:$t, event:"auto-retired", actor:"script", note:$r}])} else . end) | .[]' \
        "$KB_FILE" > "$tmp" 2>/dev/null; then
      mv "$tmp" "$KB_FILE"
      echo "» kb: rule '$rid' auto-retired ($reason)" >&2
      warn "kb rule '$rid' auto-retired ($reason)"
    else
      rm -f "$tmp"; warn "kb auto-retire skipped: could not rewrite $KB_FILE for '$rid'"
    fi
  fi
}

# Recall scoped rules onto a bundle as context (never orders): scope-glob
# gate over the diff file list, score (unjudged reads 0.5) desc, tiebreak
# judged desc then id asc, top 3. Writes $dir/kb.txt (human note) plus
# $dir/kb-recall.json (machine manifest: rank-ordered ids with scores) only
# on a match (clean trees leave neither file — mirrors sensitive.txt);
# retired rules never recall. Never fails.
write_kb_recall() { # $1 = dir, $2 = newline-separated extra paths (may be empty)
  local dir="$1" extra="${2:-}" mf="$1/kb.txt" mfj="$1/kb-recall.json"
  local files="" rules="" cands="[]" row="" rid="" scope="" hit="" g="" f=""
  local h="" hdr="" rest="" new="" total="" shown="" ids="" top="" ranked=""
  [[ -f "$KB_FILE" ]] || { rm -f "$mf" "$mfj"; return 0; }
  files=$(
    if [[ -n "$extra" ]]; then printf '%s\n' "$extra"; fi
    if [[ -f "$dir/full.diff" ]]; then
      grep '^diff --git ' "$dir/full.diff" 2>/dev/null | sed -e 's/^diff --git //' | while IFS= read -r h; do
        hdr="$h"; new=""
        if [[ "$hdr" == '"'* ]]; then
          rest=${hdr#*\" }; rest=${rest#\"}; new=${rest%%\"*}; new=${new#b/}
        else
          rest=${hdr#* }; new=${rest#b/}
        fi
        [[ -n "$new" && "$new" != "/dev/null" ]] && printf '%s\n' "$new"
      done
    fi
    :
  )
  files=$(printf '%s\n' "$files" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' | grep -v '^/dev/null$' | LC_ALL=C sort -u || true)
  [[ -z "$files" ]] && { rm -f "$mf" "$mfj"; return 0; }
  if ! rules=$(jq -c -s '[.[] | select(.status == "active")]' "$KB_FILE" 2>/dev/null); then
    warn "kb recall skipped: $KB_FILE unreadable"; rm -f "$mf" "$mfj"; return 0
  fi
  if [[ "$rules" == "[]" ]]; then rm -f "$mf" "$mfj"; return 0; fi
  while IFS= read -r row; do
    [[ -z "$row" ]] && continue
    rid=$(jq -r '.id' <<<"$row"); scope=$(jq -r '.scope' <<<"$row")
    hit="false"
    while IFS= read -r g; do
      g=$(printf '%s' "$g" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
      [[ -z "$g" ]] && continue
      while IFS= read -r f; do
        [[ -z "$f" ]] && continue
        # shellcheck disable=SC2254 # unquoted $g is the point: scope globs match as globs
        case "$f" in $g) hit="true"; break 2;; esac
      done <<<"$files"
    done < <(printf '%s\n' "$scope" | tr ',' '\n')
    if [[ "$hit" == "true" ]]; then
      cands=$(jq -c --argjson r "$row" '. + [{id:$r.id, scope:$r.scope, rule:$r.rule, hits:($r.hits // 0), judged:($r.judged // 0)}]' <<<"$cands")
    fi
  done < <(jq -c '.[]' <<<"$rules" 2>/dev/null || true)
  total=$(jq 'length' <<<"$cands")
  if [[ "$total" == "0" ]]; then rm -f "$mf" "$mfj"; return 0; fi
  # Single sort, two renderings: ranked holds the top-3 in rank order and both
  # the human note and the machine manifest render from it, so ids+order agree
  # by construction. Sort expression is the prior kb.txt ranking verbatim:
  # score desc, judged desc, id asc.
  ranked=$(jq -c 'sort_by([-(if .judged == 0 then 0.5 else .hits / .judged end), -.judged, .id]) | .[:3]' <<<"$cands" 2>/dev/null || echo "[]")
  if [[ -z "$ranked" || "$ranked" == "[]" ]]; then rm -f "$mf" "$mfj"; return 0; fi
  top=$(jq -r '.[] | "# \(.id) — \(.scope) — precision \(.hits)/\(.judged)\n\(.rule | gsub("\n"; " "))"' <<<"$ranked")
  {
    echo "# kb recall — context, never orders. Cited rules inform the review; evidence still decides."
    printf '%s\n' "$top"
  } > "$mf.tmp" && mv "$mf.tmp" "$mf"
  # Machine manifest beside the human note: rank-ordered ids with the ranker's
  # scores (unjudged reads 0.5). Additive-only: writers never touch agent-owned
  # content, readers tolerate a missing file (old bundles keep working).
  if ! jq -c '[.[] | {id:.id, score:(if .judged == 0 then 0.5 else .hits / .judged end), hits:.hits, judged:.judged}]' <<<"$ranked" > "$mfj.tmp" 2>/dev/null; then
    rm -f "$mfj.tmp" "$mfj"
    warn "kb recall manifest skipped: could not write $mfj (kb.txt kept)"
  else
    mv "$mfj.tmp" "$mfj"
  fi
  shown=$(jq 'length' <<<"$ranked")
  ids=$(jq -r 'map(.id) | join(", ")' <<<"$ranked")
  echo "» kb: $shown rule(s) -> $mf ($ids)"
  return 0
}

if [[ -z "$cmd" ]]; then
  usage >&2; exit 2
fi
if [[ "$cmd" == "trend" ]] && [[ -z "$repo" ]]; then
  usage >&2; exit 2
fi
if [[ "$cmd" != "check-tree" && "$cmd" != "doctor" && "$cmd" != "trend" && "$cmd" != "learn" && "$cmd" != "init" && "$cmd" != "install" ]] && [[ -z "$repo" || -z "$pr" ]]; then
  usage >&2; exit 2
fi

# ---------------------------------------------------------------- fetch ---
# Diff size gate: empty diffs warn-and-proceed (bundle kept, review proceeds on
# an empty diff); diffs over FETCH_MAX_BYTES refuse — an unreviewable slice
# would burn review tokens. The refusal names size, cap, and the split-or-
# narrow escape (range/base slices or a paths filter).
# Oversize-fetch candidates: split $1 (full.diff) on ^diff --git headers,
# size each file slice in bytes, and propose narrower scopes with estimates —
# the largest file alone, everything-but-largest, and a range half-split.
# Prints a JSON array of {kind,files,estimated_bytes,suggestion}. Advisory
# data only: the refusal still fails, the operator picks, nothing auto-narrows.
# Empty array when the diff has no parseable file slices.
suggest_splits() { # $1 = full.diff path, $2 = total bytes
  local f="$1" total="$2" table top_bytes top_path rest_bytes rest_list rest_n half_bytes
  case "$total" in ''|*[!0-9]*) total=0;; esac
  table=$(LC_ALL=C awk '
    /^diff --git / { if (cur != "") print size "\t" cur; cur=$0; sub(/.* b\//, "", cur); size=length($0)+1; next }
    { size += length($0)+1 }
    END { if (cur != "") print size "\t" cur }' "$f" 2>/dev/null | LC_ALL=C sort -t "$(printf '\t')" -k1,1rn || true)
  [[ -z "$table" ]] && { echo "[]"; return 0; }
  top_bytes=$(head -n 1 <<<"$table" | cut -f1)
  top_path=$(head -n 1 <<<"$table" | cut -f2-)
  case "$top_bytes" in ''|*[!0-9]*) echo "[]"; return 0;; esac
  rest_bytes=$(( total - top_bytes ))
  if (( rest_bytes < 0 )); then rest_bytes=0; fi
  rest_list=$(tail -n +2 <<<"$table" | cut -f2-)
  rest_n=$(grep -c . <<<"$rest_list" || true)
  case "$rest_n" in ''|*[!0-9]*) rest_n=0;; esac
  half_bytes=$(( (total + 1) / 2 ))
  jq -n --arg tp "$top_path" --argjson tb "$top_bytes" \
    --arg rl "$rest_list" --argjson rb "$rest_bytes" --argjson rn "$rest_n" \
    --argjson hb "$half_bytes" '
    ($rl | split("\n") | map(select(length > 0))) as $rest
    | [{kind:"paths", files:[$tp], estimated_bytes:$tb,
        suggestion:"re-fetch narrowed to \($tp) (~\($tb) bytes)"},
       (if ($rest | length) == 0 then empty
        else {kind:"paths", files:($rest[:10]), estimated_bytes:$rb,
          suggestion:(if $rn > 10
            then "re-fetch excluding \($tp): first 10 of \($rn) remaining files (~\($rb) bytes total)"
            else "re-fetch excluding \($tp) (~\($rb) bytes across \($rn) file(s))" end)} end),
       {kind:"range", files:[], estimated_bytes:$hb,
        suggestion:"split the range in half (~\($hb) bytes each): fetch-local --range/--base, fetch --base-sha/--since"}]'
}

# Chunked fetch (opt-in over-cap slicing). Reuses the per-file byte-sizing
# pass that feeds the advisory split candidates (chunk_file_table), then
# greedy-packs files in diff order into contiguous chunks each at or under
# the resolved cap (next-fit: seal the chunk when the next file would not
# fit). Deterministic: same diff bytes always yield the same chunk map, so
# re-fetch reproduces it. A single file larger than the cap cannot be sliced
# on file boundaries — that degenerate input keeps today's advisory refusal
# (chunk_plan_ok fails, no chunks written, no half-write).
# Layout (additive beside the existing bundle artifacts): <dir>/chunks/ holds
# chunk-NN.diff (byte-exact diff slices concatenating to full.diff),
# chunk-NN.toc.txt (per-chunk coverage checklist), chunk-NN.roles.txt
# (per-chunk file roles), and manifest.json (versioned coverage obligation).
# The top-level bundle keeps the full diff for anchor pinning and drift
# detection; each chunk mirrors the small-fetch artifact shape
# (diff slice, coverage checklist, file roles).
CHUNK_SCHEMA_VERSION="1"

chunk_file_table() { # $1 = full.diff path; prints "bytes\tpath" in diff order
  LC_ALL=C awk '
    /^diff --git / { if (cur != "") print size "\t" cur; cur=$0; sub(/.* b\//, "", cur); size=length($0)+1; next }
    { size += length($0)+1 }
    END { if (cur != "") print size "\t" cur }' "$1" 2>/dev/null || true
}

chunk_plan_ok() { # $1 = full.diff path; succeeds when every file slice fits the resolved cap
  local table="" max=""
  table=$(chunk_file_table "$1")
  [[ -z "$table" ]] && return 1
  max=$(awk -F'\t' 'NR==1||$1+0>m{m=$1} END{print m+0}' <<<"$table")
  case "$max" in ''|*[!0-9]*) return 1;; esac
  (( max <= FETCH_MAX_BYTES ))
}

maybe_write_chunks() { # $1 = bundle dir; writes chunks/ + manifest when the gate armed chunking; sets chunk_json
  local dir="$1" f="" total="" table="" cdir="" plan="" chunks_json=""
  local n="" cur="" line="" size="" path="" i="" members="" mbytes="" cbytes="" name=""
  local head_sha="" base_sha="" cap_src_json="" part=""
  chunk_json="null"
  [[ "${CHUNK_ACTIVE:-false}" == "true" ]] || return 0
  f="$dir/full.diff"
  [[ -f "$f" ]] || return 0
  total=$(wc -c < "$f" | tr -d ' ')
  case "$total" in ''|*[!0-9]*) total="0";; esac
  table=$(chunk_file_table "$f")
  [[ -z "$table" ]] && return 0
  cdir="$dir/chunks"
  rm -rf "$cdir"
  mkdir -p "$cdir" || return 0
  # Greedy next-fit pack in diff order; every file fits (the gate verified),
  # so every chunk lands at or under the cap.
  plan="$cdir/.plan.tsv"
  : > "$plan"
  n=1; cur=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    size=${line%%$'\t'*}; path=${line#*$'\t'}
    case "$size" in ''|*[!0-9]*) continue;; esac
    if (( cur > 0 && cur + size > FETCH_MAX_BYTES )); then n=$((n + 1)); cur=0; fi
    printf '%d\t%s\t%s\n' "$n" "$size" "$path" >> "$plan"
    cur=$((cur + size))
  done <<<"$table"
  [[ -s "$plan" ]] || { rm -rf "$cdir"; return 0; }
  # Slice full.diff into per-chunk diffs. Chunks are contiguous file runs in
  # diff order, so each chunk file opens once and closes on the switch.
  awk -v plan="$plan" -v outdir="$cdir" '
    function cfile(c) { return outdir "/chunk-" sprintf("%02d", c) ".diff" }
    BEGIN { chunk=1; n=0;
      while ((getline line < plan) > 0) {
        i=index(line, "\t"); c=substr(line, 1, i-1)+0; rest=substr(line, i+1);
        j=index(rest, "\t"); p=substr(rest, j+1); map[p]=c; if (c > n) n=c
      } }
    /^diff --git / { cur=$0; sub(/.* b\//, "", cur);
      if ((cur in map) && map[cur]+0 != chunk+0) { close(cfile(chunk)); chunk=map[cur]+0 } }
    { print > cfile(chunk+0) }
    END { close(cfile(chunk+0)) }' "$f"
  # Per-chunk checklist + roles + manifest entries.
  i=1
  while (( i <= n )); do
    name=$(printf 'chunk-%02d' "$i")
    [[ -f "$cdir/$name.diff" ]] || fail "chunk-write-failed" "chunk $name missing after slicing $f (re-run the fetch)"
    members=$(awk -v c="$i" 'BEGIN{FS="\t"} $1==c{sub(/^[^\t]*\t[^\t]*\t/, ""); print}' "$plan")
    mbytes=$(awk -F'\t' -v c="$i" '$1==c{s+=$2} END{print s+0}' "$plan")
    case "$mbytes" in ''|*[!0-9]*) mbytes="0";; esac
    cbytes=$(wc -c < "$cdir/$name.diff" | tr -d ' ')
    case "$cbytes" in ''|*[!0-9]*) cbytes="0";; esac
    { echo "# Chunk $i/$n for $(basename "$dir") — read ranges as coverage checklist"; echo
      grep -n "^diff --git" "$cdir/$name.diff" || echo "(no diff headers?)"; echo
      echo "## files"
      awk -v c="$i" 'BEGIN{FS="\t"} $1==c{print "- " $3 " (" $2 " bytes)"}' "$plan"
    } > "$cdir/$name.toc.txt"
    printf '%s\n' "$members" | roles_for_paths > "$cdir/$name.roles.txt"
    part="$cdir/.part-$name.json"
    jq -n --argjson index "$i" --arg name "$name" \
      --argjson files "$(printf '%s\n' "$members" | jq -R . | jq -s .)" \
      --argjson estimated_bytes "$mbytes" --argjson bytes "$cbytes" \
      --arg diff "$dir/chunks/$name.diff" \
      --arg toc "$dir/chunks/$name.toc.txt" \
      --arg roles "$dir/chunks/$name.roles.txt" \
      '{index:$index,name:$name,files:$files,estimated_bytes:$estimated_bytes,bytes:$bytes,diff:$diff,toc:$toc,roles:$roles}' > "$part"
    i=$((i + 1))
  done
  chunks_json=$(jq -s '.' "$cdir"/.part-chunk-*.json)
  rm -f "$cdir"/.part-chunk-*.json "$plan"
  if [[ -f "$dir/PR.json" ]]; then
    head_sha=$(pr_field "$dir" head_sha); base_sha=$(pr_field "$dir" base_sha)
  elif [[ -f "$dir/source.json" ]]; then
    head_sha=$(jq -r '.head_sha // empty' "$dir/source.json" 2>/dev/null || true)
    base_sha=$(jq -r '.base_sha // empty' "$dir/source.json" 2>/dev/null || true)
  fi
  if [[ -n "${fetch_cap_src:-}" ]]; then cap_src_json=$(jq -n --arg s "$fetch_cap_src" '$s');
  else cap_src_json="null"; fi
  jq -n --arg v "$CHUNK_SCHEMA_VERSION" --arg b "$dir" \
    --arg hs "$head_sha" --arg bs "$base_sha" \
    --argjson cap "$FETCH_MAX_BYTES" --argjson csrc "$cap_src_json" \
    --argjson total "$total" --argjson count "$n" --argjson chunks "$chunks_json" \
    '{schema_version:$v,bundle:$b,
      head_sha:(if $hs=="" then null else $hs end),
      base_sha:(if $bs=="" then null else $bs end),
      cap:{bytes:$cap,source:$csrc},
      total_bytes:$total,chunk_count:$count,chunks:$chunks}' > "$cdir/manifest.json"
  chunk_json=$(jq -n --argjson count "$n" --arg m "$dir/chunks/manifest.json" --argjson total "$total" \
    '{count:$count,manifest:$m,total_bytes:$total}')
  echo "» chunked: $n chunk(s) at or under $FETCH_MAX_BYTES bytes + $dir/chunks/manifest.json" >&2
}

check_diff_size() { # $1 = full.diff path
  local f="$1" bytes
  resolve_fetch_cap
  bytes=$(wc -c < "$f" | tr -d ' ')
  case "$bytes" in ''|*[!0-9]*) bytes="0";; esac
  if (( bytes > FETCH_MAX_BYTES )); then
    # Chunked fetch (opt-in): an opted-in over-cap fetch is machine-sliced on
    # file boundaries by maybe_write_chunks after the bundle lands — the gate
    # just arms it here. A single over-cap file cannot be sliced, so that
    # degenerate input keeps today's advisory refusal with no chunks written.
    # Absent knob prints nothing here so the refusal below stays
    # byte-identical; an overridden cap wins outright (the knob is never even
    # consulted).
    resolve_chunk_opt_in
    if [[ "$CHUNK_OPT_IN" == "true" ]]; then
      rm -rf "$(dirname "$f")/chunks"
      if chunk_plan_ok "$f"; then
        CHUNK_ACTIVE="true"
        echo "» chunking opted in (from $chunk_src) — slicing over-cap diff ($bytes bytes, cap $FETCH_MAX_BYTES) into cap-sized chunks" >&2
        return 0
      fi
      echo "» chunking opted in (from $chunk_src) — single file over cap, keeping today's advisory refusal (split the range or narrow, or override the cap deliberately)" >&2
    fi
    CANDIDATES_JSON=$(suggest_splits "$f" "$bytes")
    CANDIDATES_JSON=$(jq -c '.' <<<"$CANDIDATES_JSON" 2>/dev/null || echo "null")
    if [[ "$CANDIDATES_JSON" != "null" && "$CANDIDATES_JSON" != "[]" ]]; then
      {
        echo "candidates (suggested scopes — re-run narrowed, nothing auto-narrowed):"
        jq -r '.[] | "- [\(.kind)] \(.suggestion)"' <<<"$CANDIDATES_JSON" 2>/dev/null || true
      } >&2
    else
      CANDIDATES_JSON="null"
    fi
    fail "diff-too-large" "diff $bytes bytes exceeds cap $FETCH_MAX_BYTES bytes — split the range (fetch-local --range/--base, fetch --base-sha) or narrow with a paths filter, then re-fetch (override deliberate exceptions only: REVIEW_MAX_BYTES=<n> or .review/max_bytes)"
  fi
  if (( bytes == 0 )); then
    warn "empty diff (0 bytes) — bundle kept, review proceeds on empty diff"
  fi
}

# Bundle writers shared by fetch (GitHub) and fetch --provider (other hosts).
# Globals set: total, omitted, n_diff (used by the fetch tail + envelope).
write_toc_roles() { # $1 = dir, $2 = toc title line
  local dir="$1"
  { echo "$2"; echo
    grep -n "^diff --git" "$dir/full.diff" || echo "(no diff headers — empty diff?)"
    echo; echo "## stat"
    if [[ "${provider:-github}" == "github" ]]; then
      gh pr view "$pr" --repo "$repo" --json files --jq '.files[] | "\(.path) (+\(.additions)/-\(.deletions))"' 2>/dev/null || true
    else
      jq -r '.files[] | "\(.path) (+\(.additions // 0)/-\(.deletions // 0))"' "$dir/PR.json" 2>/dev/null || true
    fi
    echo; echo "## coverage manifest"
    total=$(jq '.files | length' "$dir/PR.json" 2>/dev/null || echo "?")
    awk '/^diff --git /{if(file!=""){if(has)print file};file=$0;sub(/.* b\//,"",file);has=0} /^@@/{has=1} END{if(file!=""&&has)print file}' "$dir/full.diff" | sort -u > "$dir/.difffiles"
    n_diff=$(wc -l < "$dir/.difffiles" | tr -d ' ')
    echo "files in PR: $total | files with diff hunks: $n_diff"
    omitted=$(jq -r '.files[].path' "$dir/PR.json" 2>/dev/null | while IFS= read -r p; do
      grep -qxF "$p" "$dir/.difffiles" || echo "$p"
    done)
    if [[ -n "$omitted" ]]; then echo "omitted (no hunks — binary, renamed without changes, or mode-only):"; echo "$omitted" | sed 's/^/- /';
    else echo "omitted: (none)"; fi
  } > "$dir/toc.txt"
  echo "» roles -> $dir/roles.txt (heuristic file roles for triage)"
  jq -r '.files[].path' "$dir/PR.json" 2>/dev/null | roles_for_paths > "$dir/roles.txt"
}

# Shared fetch tail: counts, done/next hints, --json envelope. $1 = incremental base ("null" or sha).
fetch_tail() { # $1 = inc_base; runs in fetch scope (dir repo pr cmd json_mode panel_src panel_list omitted)
  local inc_base="$1" files sens_file kb_file kbr_file db pdump_tmp
  files=$(jq '.files | length' "$dir/PR.json" 2>/dev/null || echo "?")
  sens_file="null"; [[ -f "$dir/sensitive.txt" ]] && sens_file="$dir/sensitive.txt"
  kb_file="null"; [[ -f "$dir/kb.txt" ]] && kb_file="$dir/kb.txt"
  kbr_file="null"; [[ -f "$dir/kb-recall.json" ]] && kbr_file="$dir/kb-recall.json"
  db=$(wc -c < "$dir/full.diff" 2>/dev/null | tr -d ' ' || true)
  case "$db" in ''|*[!0-9]*) db="0";; esac
  # Prompt dump payload for --json (human mode streams it to the saved stdout
  # below). Staged through a temp file, never argv: a chunked dump is
  # over-cap by construction and any large dump exceeds ARG_MAX as one argv
  # string (E2BIG), so the dump joins the envelope via --rawfile with
  # prompt_dump last in data — the same key order as the inline shape.
  # Staged only under the flag, so default envelopes are untouched.
  pdump_tmp=""
  if [[ "${show_prompts:-false}" == "true" && "$json_mode" == "true" ]]; then
    pdump_tmp=$(mktemp "${TMPDIR:-/tmp}/verdict-pdump.XXXXXX") || fail "prompt-dump-failed" "could not stage the prompt dump (check '${TMPDIR:-/tmp}' is writable)"
    render_prompt_dump "$dir" > "$pdump_tmp"
  fi
  echo "done: $dir/PR.json $dir/full.diff $dir/toc.txt $dir/roles.txt ($files files)"
    echo "next: feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + $dir/PR.json + $dir/full.diff (+ $dir/roles.txt) to any agent; ask it to write $dir/review.json"
    if [[ "$json_mode" == "true" ]]; then
      if [[ -n "$pdump_tmp" ]]; then
        # Flag-on path: the dump rides a file (--rawfile), never argv, so
        # over-cap chunked dumps survive ARG_MAX (E2BIG). prompt_dump lands
        # last in data, the same key order as the inline shape below.
        jq -n --arg d "$dir" --arg hs "$(pr_field "$dir" head_sha)" \
          --arg bs "$(pr_field "$dir" base_sha)" --argjson files "$(jq '.files | length' "$dir/PR.json")" \
          --argjson adds "$(jq '.additions // 0' "$dir/PR.json")" --argjson dels "$(jq '.deletions // 0' "$dir/PR.json")" \
          --argjson hunks "$({ grep '^@@' "$dir/full.diff" || true; } | wc -l | tr -d ' ')" \
          --argjson db "$db" \
          --argjson omitted "$(to_json_array "$omitted")" \
          --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
          --arg inc "$inc_base" --arg sens "$sens_file" --arg kb "$kb_file" --arg kbr "$kbr_file" --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$(now_utc)" \
          --argjson estimate "${estimate_json:-null}" --argjson budget "${budget_json:-null}" --argjson cap "${cap_json:-null}" \
          --argjson chunk "${chunk_json:-null}" \
          '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
            next:"feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + \($d)/PR.json + full.diff to any agent; ask it to write \($d)/review.json",
            warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,head_sha:$hs,base_sha:$bs,files:$files,additions:$adds,deletions:$dels,
            hunks:$hunks,omitted:$omitted,diff_bytes:$db,panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
            artifacts:({"PR.json":"\($d)/PR.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","reviews.json":"\($d)/reviews.json","inline.json":"\($d)/inline.json","sensitive.txt":(if $sens=="null" then null else $sens end),"incremental.diff":(if $inc=="null" then null else "\($d)/incremental.diff" end)} + (if $kb == "null" then {} else {"kb.txt":$kb} end) + (if $kbr == "null" then {} else {"kb-recall.json":$kbr} end)),
            incremental_base:(if $inc=="null" then null else $inc end)}
            + (if $estimate == null then {} else {estimate:$estimate} end)
            + (if $budget == null then {} else {budget:$budget} end)
            + (if $cap == null then {} else {cap:$cap} end)
            + (if $chunk == null then {} else {chunk:$chunk} end),error:null}' > "$pdump_tmp.env"
        jq --rawfile pd "$pdump_tmp" '.data += {prompt_dump:$pd}' "$pdump_tmp.env" >&9
        rm -f "$pdump_tmp" "$pdump_tmp.env"
      else
      jq -n --arg d "$dir" --arg hs "$(pr_field "$dir" head_sha)" \
        --arg bs "$(pr_field "$dir" base_sha)" --argjson files "$(jq '.files | length' "$dir/PR.json")" \
        --argjson adds "$(jq '.additions // 0' "$dir/PR.json")" --argjson dels "$(jq '.deletions // 0' "$dir/PR.json")" \
        --argjson hunks "$({ grep '^@@' "$dir/full.diff" || true; } | wc -l | tr -d ' ')" \
        --argjson db "$db" \
        --argjson omitted "$(to_json_array "$omitted")" \
        --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
        --arg inc "$inc_base" --arg sens "$sens_file" --arg kb "$kb_file" --arg kbr "$kbr_file" --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$(now_utc)" \
        --argjson estimate "${estimate_json:-null}" --argjson budget "${budget_json:-null}" --argjson cap "${cap_json:-null}" \
        --argjson pdump null \
        --argjson chunk "${chunk_json:-null}" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
          next:"feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + \($d)/PR.json + full.diff to any agent; ask it to write \($d)/review.json",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,head_sha:$hs,base_sha:$bs,files:$files,additions:$adds,deletions:$dels,
          hunks:$hunks,omitted:$omitted,diff_bytes:$db,panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
          artifacts:({"PR.json":"\($d)/PR.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","reviews.json":"\($d)/reviews.json","inline.json":"\($d)/inline.json","sensitive.txt":(if $sens=="null" then null else $sens end),"incremental.diff":(if $inc=="null" then null else "\($d)/incremental.diff" end)} + (if $kb == "null" then {} else {"kb.txt":$kb} end) + (if $kbr == "null" then {} else {"kb-recall.json":$kbr} end)),
          incremental_base:(if $inc=="null" then null else $inc end)}
          + (if $estimate == null then {} else {estimate:$estimate} end)
          + (if $budget == null then {} else {budget:$budget} end)
          + (if $cap == null then {} else {cap:$cap} end)
          + (if $chunk == null then {} else {chunk:$chunk} end)
          + (if $pdump == null then {} else {prompt_dump:$pdump} end),error:null}' >&9
      fi
    fi
  # Human-mode dump: the assembled prompt owns the saved stdout; without the
  # flag this block is dead code and output is byte-identical to today.
  if [[ "${show_prompts:-false}" == "true" && "$json_mode" != "true" ]]; then
    render_prompt_dump "$dir" >&8
  fi
  exit 0
}

if [[ "$cmd" == "fetch" ]]; then
  base_sha=""; provider="github"; budget_consent="false"; show_prompts="false"; chunk_flag="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --base-sha|--since) base_sha="${2:-}"; shift 2;;
    --provider) provider="${2:-github}"; shift 2;;
    --consent-over-budget) budget_consent="true"; shift;;
    --show-prompts) show_prompts="true"; shift;;
    --chunk) chunk_flag="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for fetch (want --base-sha|--since <sha>, --provider <name>, --consent-over-budget, --show-prompts, --chunk, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  # Prompt dump rides the normal fetch: progress moves to stderr so the dump
  # owns stdout. Stores, network, and exits are untouched by the flag itself.
  if [[ "$show_prompts" == "true" && "$json_mode" != "true" ]]; then exec 8>&1; exec 1>&2; fi
  # Chunk state for this fetch: armed by the size gate on the opted-in
  # over-cap path, consumed by maybe_write_chunks before the tail. Absent or
  # under-cap runs keep chunk_json null, so the envelope stays byte-identical.
  CHUNK_ACTIVE="false"; chunk_json="null"
  case "$provider" in github|gitlab|gitcafe|forgejo|bitbucket|azure) ;;
    *) fail "bad-provider" "unknown --provider '$provider' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)";; esac
  budget_gate "$budget_consent"
  dir=".review/$pr"; mkdir -p "$dir"
  write_budget_receipt "$dir"
  if [[ "$provider" != "github" ]]; then
    [[ -n "$base_sha" ]] && fail "bad-flags" "--base-sha/--since is GitHub-only for now (providers have no incremental re-review yet)"
    fetch_provider_bundle
    check_diff_size "$dir/full.diff"
    build_hunks_tsv "$dir/full.diff" "$dir/hunks.tsv"
    write_toc_roles "$dir" "# TOC for $provider #$pr ($repo) — read ranges as coverage checklist"
    emit_context "$dir"
    ensure_store "$dir"
    write_sensitive_manifest "$dir" "$(jq -r '.files[] | .path, (.previous_filename // empty)' "$dir/PR.json" 2>/dev/null || true)"
    write_kb_recall "$dir" "$(jq -r '.files[] | .path, (.previous_filename // empty)' "$dir/PR.json" 2>/dev/null || true)"
    maybe_write_chunks "$dir"
    fetch_tail "null"
  fi
  echo "» gh pr view $repo#$pr"
  gh pr view "$pr" --repo "$repo" \
    --json number,title,body,baseRefName,headRefName,headRefOid,baseRefOid,author,url,commits,files,additions,deletions,isDraft,reviewDecision,state \
    > "$dir/PR.json" || fail "fetch-failed" "gh pr view failed for $repo#$pr (auth? PR number?)"
  echo "» gh pr diff -> $dir/full.diff"
  gh pr diff "$pr" --repo "$repo" > "$dir/full.diff" || {
    echo "gh pr diff failed, trying API diff" >&2
    gh api "repos/$repo/pulls/$pr" -H "Accept: application/vnd.github.diff" > "$dir/full.diff" \
      || fail "fetch-failed" "could not fetch diff for $repo#$pr via CLI or API"
  }
  check_diff_size "$dir/full.diff"
  build_hunks_tsv "$dir/full.diff" "$dir/hunks.tsv"
  write_toc_roles "$dir" "# TOC for PR #$pr ($repo) — read ranges as coverage checklist"
  emit_context "$dir"
  ensure_store "$dir"
  write_sensitive_manifest "$dir" "$(jq -r '.files[] | .path, (.previous_filename // empty)' "$dir/PR.json" 2>/dev/null || true)"
  write_kb_recall "$dir" "$(jq -r '.files[] | .path, (.previous_filename // empty)' "$dir/PR.json" 2>/dev/null || true)"
  echo "» reviews + inline comments"
  gh api "repos/$repo/pulls/$pr/reviews" --paginate > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
  gh api "repos/$repo/pulls/$pr/comments" --paginate > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
  maybe_write_chunks "$dir"
  inc_base="null"
  if [[ -n "$base_sha" ]]; then
    head_sha=$(jq -r .headRefOid "$dir/PR.json")
    echo "» compare $base_sha...$head_sha -> $dir/incremental.diff"
    if ! gh api "repos/$repo/compare/$base_sha...$head_sha" -H "Accept: application/vnd.github.diff" > "$dir/incremental.diff" 2>"$dir/.compare.err"; then
      echo "incrementalUnavailable: $(head -c 300 "$dir/.compare.err")" | tee "$dir/incremental.diff.note"
      rm -f "$dir/incremental.diff"
    else
      inc_base="$base_sha"
    fi
  fi
  fetch_tail "$inc_base"
  exit 0
fi

# ---------------------------------------------------------- fetch-local ---
# Local review bundle from the worktree (no GitHub I/O, no PR needed).
# fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]
#             [--stash [n]] [--consent-over-budget] [-- paths...] [--json]
# Modes: worktree-vs-HEAD (default) | staged | range | branch-vs-base | stash.
# Writes .review/local-<name>/ (source.json, full.diff, toc.txt, roles.txt).
if [[ "$cmd" == "fetch-local" ]]; then
  staged="false"; range=""; base_ref=""; stash_n=""; budget_consent="false"; show_prompts="false"; chunk_flag="false"; paths=()
  # json_mode keeps the prefs default (output.format json); --json below forces it.
  while [[ $# -gt 0 ]]; do case "$1" in
    --staged) staged="true"; shift;;
    --range) range="${2:-}"; shift 2;;
    --base) base_ref="${2:-}"; shift 2;;
    --stash) if [[ -n "${2:-}" && "${2:-}" != -* ]]; then stash_n="$2"; shift 2; else stash_n="0"; shift; fi;;
    --consent-over-budget) budget_consent="true"; shift;;
    --show-prompts) show_prompts="true"; shift;;
    --chunk) chunk_flag="true"; shift;;
    --json) json_mode="true"; shift;;
    --) shift; while [[ $# -gt 0 ]]; do paths+=("$1"); shift; done;;
    -*) echo "unknown flag '$1' for fetch-local (want --staged, --range <A..B>, --base <ref>, --stash [n], --consent-over-budget, --show-prompts, --chunk, -- <paths>, --json)" >&2; exit 2;;
    *) paths+=("$1"); shift;;
  esac; done
  json_redirect
  if [[ "$show_prompts" == "true" && "$json_mode" != "true" ]]; then exec 8>&1; exec 1>&2; fi
  # Chunk state for this fetch (same gate/writer contract as fetch above).
  CHUNK_ACTIVE="false"; chunk_json="null"
  slug=$(printf '%s' "$pr" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9-]/-/g; s/-\+/-/g; s/^-\|-$//g' | cut -c1-48)
  [[ -z "$slug" || "$slug" == "local" ]] && fail "bad-slug" "name '$pr' sanitizes to nothing — pass a usable bundle name"
  if [[ "$slug" =~ ^[0-9]+$ ]]; then fail "bad-slug" "'$slug' looks like a PR number — use fetch for PRs"; fi
  if [[ "$slug" == local-* ]]; then fail "bad-slug" "no local- prefix needed — the store adds it"; fi
  dir=".review/local-$slug"
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "not-a-repo" "fetch-local runs inside a git worktree"
  branch=$(git branch --show-current 2>/dev/null || true)
  # One diff source per invocation; --range/--base/--stash are exclusive.
  modes_n=0; [[ -n "$range" ]] && modes_n=$((modes_n+1)); [[ -n "$base_ref" ]] && modes_n=$((modes_n+1)); [[ "$staged" == "true" ]] && modes_n=$((modes_n+1)); [[ -n "$stash_n" ]] && modes_n=$((modes_n+1))
  if (( modes_n > 1 )); then fail "bad-flags" "--range/--base/--staged/--stash are mutually exclusive"; fi
  kind="worktree"; base_sha=""; head_sha=""; head_ref=""; range_spec=""; dirty="false"
  if [[ -n "$stash_n" ]]; then
    kind="stash"
    git rev-parse -q --verify "stash@{$stash_n}" >/dev/null 2>&1 || fail "bad-stash" "no such stash: stash@{$stash_n} (see git stash list)"
    head_sha=$(git rev-parse "stash@{$stash_n}^{commit}")
    base_sha=$(git rev-parse "stash@{$stash_n}^")
    head_ref="stash@{$stash_n}"; range_spec="stash@{$stash_n}"
    diff_cmd=(git diff "stash@{$stash_n}^" "stash@{$stash_n}")
  elif [[ -n "$range" ]]; then
    kind="range"
    if [[ "$range" == *...* ]]; then
      ra="${range%%...*}"; rb="${range##*...}"
      base_sha=$(git rev-parse --verify "$ra" 2>/dev/null || fail "bad-range" "cannot resolve range base '$ra'")
      mb=$(git merge-base "$ra" "$rb" 2>/dev/null || fail "bad-range" "no merge-base for '$range'")
      head_sha=$(git rev-parse --verify "$rb^{commit}" 2>/dev/null || fail "bad-range" "cannot resolve range head '$rb'")
      base_sha="$mb"; range_spec="$range"
      diff_cmd=(git diff "$mb" "$rb")
    else
      ra="${range%%..*}"; rb="${range##*..}"
      base_sha=$(git rev-parse --verify "$ra^{commit}" 2>/dev/null || fail "bad-range" "cannot resolve range base '$ra'")
      head_sha=$(git rev-parse --verify "$rb^{commit}" 2>/dev/null || fail "bad-range" "cannot resolve range head '$rb'")
      range_spec="$range"
      diff_cmd=(git diff "$ra" "$rb")
    fi
    head_ref="$rb"
  elif [[ -n "$base_ref" ]]; then
    kind="branch"
    mb=$(git merge-base "$base_ref" HEAD 2>/dev/null || fail "bad-base" "no merge-base with '$base_ref'")
    base_sha="$mb"; head_sha=$(git rev-parse HEAD)
    head_ref="${branch:-HEAD}"; range_spec="$base_ref...HEAD"
    diff_cmd=(git diff "$mb" HEAD)
  elif [[ "$staged" == "true" ]]; then
    kind="staged"
    base_sha=$(git rev-parse HEAD); head_sha=$(git write-tree)
    head_ref="index"; range_spec="HEAD:index"
    diff_cmd=(git diff --cached)
  else
    kind="worktree"; dirty="true"
    base_sha=$(git rev-parse HEAD); head_sha=""
    head_ref="${branch:-worktree}"; range_spec="HEAD:worktree"
    diff_cmd=(git diff HEAD)
  fi
  if (( ${#paths[@]} > 0 )); then diff_cmd+=("${paths[@]}"); fi
  budget_gate "$budget_consent"
  mkdir -p "$dir"
  write_budget_receipt "$dir"
  "${diff_cmd[@]}" > "$dir/full.diff" 2>"$dir/.diff.err" || fail "diff-failed" "git diff failed: $(head -c 200 "$dir/.diff.err")"
  check_diff_size "$dir/full.diff"
  build_hunks_tsv "$dir/full.diff" "$dir/hunks.tsv"
  if [[ -s "$dir/full.diff" ]] && ! grep -q '^@@' "$dir/full.diff" 2>/dev/null; then
    warn "no @@ hunks — binary/mode-only changes; bundle kept, anchors unchecked, everything kept"
  fi
  diff_sha=$(shasum -a 256 "$dir/full.diff" | cut -d' ' -f1)
  # File list + stat come from range forms of the same endpoints the diff came
  # from (stash reads as base..stash-commit, identical content to stash show -p).
  case "$kind" in
    worktree) list_ref=(HEAD); stat_ref=(HEAD);;
    staged)   list_ref=(--cached); stat_ref=(--cached);;
    range)    list_ref=("$range"); stat_ref=("$range");;
    branch)   list_ref=("$base_ref...HEAD"); stat_ref=("$base_ref...HEAD");;
    stash)    list_ref=("stash@{$stash_n}^" "stash@{$stash_n}"); stat_ref=("stash@{$stash_n}^" "stash@{$stash_n}");;
  esac
  if (( ${#paths[@]} > 0 )); then list_ref+=(-- "${paths[@]}"); stat_ref+=(-- "${paths[@]}"); fi
  file_list=$(git diff --no-renames --name-only "${list_ref[@]}" 2>/dev/null || git diff --name-only "${list_ref[@]}" 2>/dev/null || true)
  # numstat for the +/- stat block (binary files report - -; counted as files, no hunks).
  numstat=$(git diff --numstat "${stat_ref[@]}" 2>/dev/null || true)
  total=$(printf '%s' "$file_list" | grep -c . || true)
  adds=$(printf '%s\n' "$numstat" | awk '$1 ~ /^[0-9]+$/ {a+=$1} END {print a+0}')
  dels=$(printf '%s\n' "$numstat" | awk '$2 ~ /^[0-9]+$/ {d+=$2} END {print d+0}')
  # Untracked files never reach git diff, so they stay excluded from the bundle:
  # warn with a count (review proceeds). Binary files are counted in the file
  # list but carry no hunks: warn with a count, same mechanism.
  if (( ${#paths[@]} > 0 )); then
    untracked=$(git ls-files --others --exclude-standard -- "${paths[@]}" 2>/dev/null | grep -v '^\.review/' || true)
  else
    untracked=$(git ls-files --others --exclude-standard 2>/dev/null | grep -v '^\.review/' || true)
  fi
  if [[ -n "$untracked" ]]; then
    n_untracked=$(printf '%s\n' "$untracked" | grep -c . || true)
    shown=$(printf '%s\n' "$untracked" | head -n 5 | tr '\n' ' ')
    if (( n_untracked > 5 )); then warn "untracked files excluded from bundle ($n_untracked): ${shown}and $((n_untracked - 5)) more (stage or commit to include)";
    else warn "untracked files excluded from bundle ($n_untracked): $shown(stage or commit to include)"; fi
  fi
  binary_skipped=$(printf '%s\n' "$numstat" | awk -F'\t' '$1 == "-" && $2 == "-" {print $3}')
  if [[ -n "$binary_skipped" ]]; then
    n_binary=$(printf '%s\n' "$binary_skipped" | grep -c . || true)
    shown=$(printf '%s\n' "$binary_skipped" | head -n 5 | tr '\n' ' ')
    if (( n_binary > 5 )); then warn "binary files skipped, no hunks ($n_binary): ${shown}and $((n_binary - 5)) more";
    else warn "binary files skipped, no hunks ($n_binary): $shown"; fi
  fi
  if (( ${#paths[@]} > 0 )) && ! grep -q . <<<"$file_list"; then
    warn "paths filter matched nothing: $(printf '%s ' "${paths[@]}")— bundle empty, review proceeds on empty diff"
  fi
  { echo "# TOC for local $slug ($repo) — read ranges as coverage checklist"; echo
    grep -n "^diff --git" "$dir/full.diff" || echo "(no diff headers — empty diff?)"
    echo; echo "## stat"
    printf '%s\n' "$numstat" | awk 'NF==3 {print $3" (+"$1"/-"$2")"}'
    echo; echo "## coverage manifest"
    awk '/^diff --git /{if(file!=""){if(has)print file};file=$0;sub(/.* b\//,"",file);has=0} /^@@/{has=1} END{if(file!=""&&has)print file}' "$dir/full.diff" | sort -u > "$dir/.difffiles"
    n_diff=$(wc -l < "$dir/.difffiles" | tr -d ' ')
    echo "files changed: $total | files with diff hunks: $n_diff"
    omitted=$(printf '%s\n' "$file_list" | while IFS= read -r p; do
      [[ -z "$p" ]] && continue
      grep -qxF "$p" "$dir/.difffiles" || echo "$p"
    done)
    if [[ -n "$omitted" ]]; then echo "omitted (no hunks — binary, renamed without changes, or mode-only):"; echo "$omitted" | sed 's/^/- /';
    else echo "omitted: (none)"; fi
  } > "$dir/toc.txt"
  echo "» roles -> $dir/roles.txt (heuristic file roles for triage)"
  printf '%s\n' "$file_list" | roles_for_paths > "$dir/roles.txt"
  emit_context "$dir"
  ensure_store "$dir"
  write_sensitive_manifest "$dir" "$file_list"
  write_kb_recall "$dir" "$file_list"
  tool_version=$(jq -r .version "$(dirname "${BASH_SOURCE[0]:-$0}")/../package.json" 2>/dev/null || echo "0.0.0-unknown")
  now=$(now_utc)
  jq -n --arg v "1" --arg kind "$kind" --arg r "$repo" --arg slug "$slug" \
    --arg bref "$base_ref" --arg href "$head_ref" --arg bs "$base_sha" --arg hs "$head_sha" \
    --arg rg "$range_spec" --argjson dirty "$dirty" --arg dsha "$diff_sha" \
    --argjson files "$total" --argjson adds "$adds" --argjson dels "$dels" \
    --arg tv "$tool_version" --arg t "$now" --arg model "${REVIEW_MODEL:-}" \
    '{schema_version:$v,kind:$kind,repo:$r,slug:$slug,
      base_ref:(if $bref=="" then null else $bref end),head_ref:$href,
      base_sha:(if $bs=="" then null else $bs end),head_sha:(if $hs=="" then null else $hs end),
      range:(if $rg=="" then null else $rg end),dirty:$dirty,diff_sha256:$dsha,
      files:$files,additions:$adds,deletions:$dels,tool_version:$tv,
      model:(if $model=="" then null else $model end),created_at:$t}' > "$dir/source.json"
  echo "done: $dir/source.json $dir/full.diff $dir/toc.txt $dir/roles.txt ($total files)"
  echo "next: feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + $dir/source.json + full.diff to any agent; ask it to write $dir/review.json"
  maybe_write_chunks "$dir"
  if [[ "$json_mode" == "true" ]]; then
    sens_file="null"; [[ -f "$dir/sensitive.txt" ]] && sens_file="$dir/sensitive.txt"
    kb_file="null"; [[ -f "$dir/kb.txt" ]] && kb_file="$dir/kb.txt"
    kbr_file="null"; [[ -f "$dir/kb-recall.json" ]] && kbr_file="$dir/kb-recall.json"
    db=$(wc -c < "$dir/full.diff" 2>/dev/null | tr -d ' ' || true)
    case "$db" in ''|*[!0-9]*) db="0";; esac
    # Prompt dump payload staged through a temp file, never argv (see
    # fetch_tail): a chunked dump is over-cap by construction and any large
    # dump exceeds ARG_MAX as one argv string (E2BIG). Staged only under the
    # flag, so default envelopes are untouched.
    pdump_tmp=""
    if [[ "$show_prompts" == "true" ]]; then
      pdump_tmp=$(mktemp "${TMPDIR:-/tmp}/verdict-pdump.XXXXXX") || fail "prompt-dump-failed" "could not stage the prompt dump (check '${TMPDIR:-/tmp}' is writable)"
      render_prompt_dump "$dir" > "$pdump_tmp"
    fi
    if [[ -n "$pdump_tmp" ]]; then
      jq -n --arg d "$dir" --arg hs "$head_sha" --arg bs "$base_sha" --argjson files "$total" \
        --argjson adds "$adds" --argjson dels "$dels" \
        --argjson hunks "$(grep -c '^@@' "$dir/full.diff" || true)" \
        --argjson db "$db" \
        --argjson omitted "$(to_json_array "$omitted")" \
        --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
        --arg sens "$sens_file" --arg kb "$kb_file" --arg kbr "$kbr_file" \
        --arg v "$cmd" --arg r "$repo" --arg p "$slug" --arg t "$now" --arg kind "$kind" \
        --argjson estimate "${estimate_json:-null}" --argjson budget "${budget_json:-null}" --argjson cap "${cap_json:-null}" \
        --argjson chunk "${chunk_json:-null}" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
          next:("feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + \($d)/source.json + full.diff to any agent; ask it to write \($d)/review.json"),
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,kind:$kind,head_sha:$hs,base_sha:$bs,
          files:$files,additions:$adds,deletions:$dels,hunks:$hunks,omitted:$omitted,diff_bytes:$db,
          panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
          artifacts:({"source.json":"\($d)/source.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","sensitive.txt":(if $sens=="null" then null else $sens end)} + (if $kb == "null" then {} else {"kb.txt":$kb} end) + (if $kbr == "null" then {} else {"kb-recall.json":$kbr} end))}
          + (if $estimate == null then {} else {estimate:$estimate} end)
          + (if $budget == null then {} else {budget:$budget} end)
          + (if $cap == null then {} else {cap:$cap} end)
          + (if $chunk == null then {} else {chunk:$chunk} end),error:null}' > "$pdump_tmp.env"
      jq --rawfile pd "$pdump_tmp" '.data += {prompt_dump:$pd}' "$pdump_tmp.env" >&9
      rm -f "$pdump_tmp" "$pdump_tmp.env"
    else
    jq -n --arg d "$dir" --arg hs "$head_sha" --arg bs "$base_sha" --argjson files "$total" \
      --argjson adds "$adds" --argjson dels "$dels" \
      --argjson hunks "$(grep -c '^@@' "$dir/full.diff" || true)" \
      --argjson db "$db" \
      --argjson omitted "$(to_json_array "$omitted")" \
      --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
      --arg sens "$sens_file" --arg kb "$kb_file" --arg kbr "$kbr_file" \
      --arg v "$cmd" --arg r "$repo" --arg p "$slug" --arg t "$now" --arg kind "$kind" \
      --argjson estimate "${estimate_json:-null}" --argjson budget "${budget_json:-null}" --argjson cap "${cap_json:-null}" \
      --argjson pdump null \
      --argjson chunk "${chunk_json:-null}" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:("feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + \($d)/source.json + full.diff to any agent; ask it to write \($d)/review.json"),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,kind:$kind,head_sha:$hs,base_sha:$bs,
        files:$files,additions:$adds,deletions:$dels,hunks:$hunks,omitted:$omitted,diff_bytes:$db,
        panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
        artifacts:({"source.json":"\($d)/source.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","sensitive.txt":(if $sens=="null" then null else $sens end)} + (if $kb == "null" then {} else {"kb.txt":$kb} end) + (if $kbr == "null" then {} else {"kb-recall.json":$kbr} end))}
        + (if $estimate == null then {} else {estimate:$estimate} end)
        + (if $budget == null then {} else {budget:$budget} end)
        + (if $cap == null then {} else {cap:$cap} end)
        + (if $chunk == null then {} else {chunk:$chunk} end)
        + (if $pdump == null then {} else {prompt_dump:$pdump} end),error:null}' >&9
    fi
  fi
  if [[ "$show_prompts" == "true" && "$json_mode" != "true" ]]; then
    render_prompt_dump "$dir" >&8
  fi
  exit 0
fi

# Live open-thread read (single home for the post APPROVE recheck GraphQL).
# Prints the live unresolved-thread count on stdout; exits nonzero on lookup
# failure. No store writes, no posting — pure read. Callers own the
# refuse/downgrade/skip semantics and messages around it.
live_open_thread_count() { # $1 = owner/repo, $2 = pr
  local _lotc_repo="$1" _lotc_pr="$2"
  local _lotc_owner="${_lotc_repo%%/*}" _lotc_name="${_lotc_repo##*/}"
  local _lotc_threads _lotc_open
  if ! _lotc_threads=$(gh api graphql --paginate -F owner="$_lotc_owner" -F name="$_lotc_name" -F pr="$_lotc_pr" -f query='
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }' | jq -s '[.[].data.repository.pullRequest.reviewThreads.nodes[]]'); then
    return 1
  fi
  _lotc_open=$(jq '[.[] | select(.isResolved == false)] | length' <<<"$_lotc_threads" 2>/dev/null || echo 0)
  case "$_lotc_open" in ''|*[!0-9]*) _lotc_open=0;; esac
  printf '%s\n' "$_lotc_open"
}

# ----------------------------------------------------------------- post ---
if [[ "$cmd" == "post" ]]; then
  approve="false"; request_changes="false"; bind="false"; force="false"
  review_file=".review/$pr/review.json"; dry_run="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --approve=*) approve="${1#--approve=}"; shift;;
    --approve) approve="${2:-true}"; shift 2;;
    --request-changes) request_changes="true"; shift;;
    --bind) bind="true"; shift;;
    --force) force="true"; shift;;
    --review-file) review_file="${2:-}"; shift 2;;
    --dry-run) dry_run="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for post (want --approve[=bool], --request-changes, --bind, --force, --review-file <path>, --dry-run, --json)" >&2; exit 2;;
  esac; done
  # Exact re-run command for every failure/interrupt below (flags included, so a
  # retry reproduces this invocation). Interrupt trap is echo-only: safe.
  rentry_cmd="./scripts/review.sh post $repo $pr"
  [[ "$approve" != "false" ]] && rentry_cmd="$rentry_cmd --approve=$approve"
  [[ "$request_changes" == "true" ]] && rentry_cmd="$rentry_cmd --request-changes"
  [[ "$bind" == "true" ]] && rentry_cmd="$rentry_cmd --bind"
  [[ "$force" == "true" ]] && rentry_cmd="$rentry_cmd --force"
  [[ "$review_file" != ".review/$pr/review.json" ]] && rentry_cmd="$rentry_cmd --review-file $review_file"
  [[ "$dry_run" == "true" ]] && rentry_cmd="$rentry_cmd --dry-run"
  [[ "$json_mode" == "true" ]] && rentry_cmd="$rentry_cmd --json"
  trap 'echo "re-enter: $rentry_cmd" >&2; exit 130' INT TERM
  json_redirect
  case "$approve" in true|false) ;;
    *) fail "bad-approve" "--approve takes true|false, got '$approve'";; esac
  dir=".review/$pr"
  [[ "$pr" == local-* ]] && fail "no-pr-target" "post needs a PR bundle (fetch owner/repo N first); local bundles record + report, they never post"
  prov=$(jq -r '.provider // "github"' "$dir/PR.json" 2>/dev/null || echo "github")
  if [[ "$prov" != "github" ]]; then fail "provider-unimplemented" "post to $prov is not implemented yet — fetch, record, report and triage work on it; the manual lists the gaps"; fi
  ensure_store "$dir"
  [[ -f "$review_file" ]] || fail "missing-review-file" "missing $review_file (agent must write {body, approved, comments[]})"
  [[ -f "$dir/full.diff" ]] || fail "missing-diff" "missing $dir/full.diff (run fetch first — anchors validate against it)"
  pr_state=$(pr_field "$dir" state)
  case "$pr_state" in "" ) warn "PR state unknown (re-fetch to record it) — proceeding";; open) ;;
    *) fail "pr-not-open" "PR is $pr_state — refusing post (re-fetch if this bundle is stale)";; esac
  body=$(jq -r '.body // ""' "$review_file")
  # Degenerate-body guard: verdict marker or >=20 chars, no placeholder shorts
  if ! printf '%s' "$body" | grep -qE '^> ✅|^> ℹ️|^> \[!IMPORTANT\]|^> \[!CAUTION\]'; then
    len=$(printf '%s' "$body" | wc -c | tr -d ' ')
    if [[ "$len" -lt 20 ]]; then fail "degenerate-body" "refusing degenerate body (<20 chars, no verdict marker)."; fi
    if [[ "$len" -lt 50 ]] && printf '%s' "$body" | grep -qiE 'test|testing|placeholder|temp|foo|bar|asdf|dummy|sample'; then
      fail "degenerate-body" "refusing placeholder-looking short body."
    fi
  fi
  # Stale-SHA guard: anchors validate against the fetch-time snapshot
  # (hunks.tsv pinned at fetch) — if HEAD moved since fetch, say so in the
  # body as drift context and treat anchors as potentially stale. Lookup
  # failure degrades to a warning, never a refusal. A binding APPROVE
  # requested against known drift is downgraded to COMMENT at the gate
  # below (the review does not cover live HEAD); unknown live SHA keeps
  # the warn-and-proceed path.
  drift_stale="false"
  fetched_sha=$(pr_field "$dir" head_sha)
  live_sha=$(gh api "repos/$repo/pulls/$pr" --jq '.head.sha' 2>/dev/null || true)
  if [[ -n "$live_sha" && -n "$fetched_sha" && "$live_sha" != "$fetched_sha" ]]; then
    drift_stale="true"
    warn "HEAD moved since fetch (fetched ${fetched_sha:0:7}, live ${live_sha:0:7}) — anchors potentially stale; run ./scripts/review.sh fetch $repo $pr --base-sha ${fetched_sha:0:7} and review the delta"
    body="> ⚠️ Note: commits landed after this review's fetch (fetched \`${fetched_sha:0:7}\`, live \`${live_sha:0:7}\`); treat line anchors as potentially stale. Run \`./scripts/review.sh fetch $repo $pr --base-sha ${fetched_sha:0:7}\` and review the delta.

$body"
  elif [[ -z "$live_sha" ]]; then
    warn "live SHA lookup failed — proceeding on fetched hunks"
  fi
  ensure_hunks_snapshot "$dir"
  # approved flag: CLI flag wins, else review.json approved. Binding decided at the gate below.
  json_approved=$(jq -r '.approved // false' "$review_file")
  if [[ "$approve" == "false" && "$json_approved" == "true" ]]; then approve="true"; fi
  event="COMMENT"
  commit_id=$(pr_field "$dir" head_sha)
  now=$(now_utc)
  findings_file="$dir/findings.json"; [[ "$dry_run" == "true" ]] && findings_file="$dir/findings.dryrun.json"
  validate_and_merge "$review_file" "$dir" "$commit_id" "$now" "$findings_file"
  # Binding gate: binding verdicts need --bind (else COMMENT); APPROVE needs zero
  # open AND zero deferred findings unless --force, plus a review that covers
  # live HEAD (known drift downgrades to COMMENT — re-fetch the delta to bind).
  # REQUEST_CHANGES with findings is normal.
  want_bind="false"
  if [[ "$approve" == "true" || "$request_changes" == "true" ]]; then want_bind="true"; fi
  if [[ "$want_bind" == "true" && "$bind" != "true" ]]; then
    warn "binding verdict downgraded to COMMENT (pass --bind to bind)"
  fi
  if [[ "$want_bind" == "true" && "$bind" == "true" ]]; then
    if [[ "$request_changes" == "true" ]]; then
      event="REQUEST_CHANGES"
    elif [[ "$approve" == "true" ]]; then
      if (( n_open + n_def > 0 )) && [[ "$force" != "true" ]]; then
        fail "approve-blocked" "refusing APPROVE with $n_open open + $n_def deferred findings (triage or pass --force)"
      fi
      if [[ "$drift_stale" == "true" ]]; then
        warn "stale fetch — APPROVE downgraded to COMMENT (review covers fetched ${fetched_sha:0:7}, live HEAD is ${live_sha:0:7}; run ./scripts/review.sh fetch $repo $pr --base-sha ${fetched_sha:0:7} and review the delta to bind)"
        body="> ⚠️ Note: posting as COMMENT instead of APPROVE — commits landed after this review's fetch, so the review does not cover live HEAD; run \`./scripts/review.sh fetch $repo $pr --base-sha ${fetched_sha:0:7}\` and review the delta.
\n$body"
        event="COMMENT"
      else
        event="APPROVE"
      fi
    fi
    # Self-authored downgrade: GitHub rejects self-approvals — fail here, not there.
    if [[ "$event" != "COMMENT" ]]; then
      author=$(pr_field "$dir" author)
      me=$(gh api user --jq '.login' 2>/dev/null || true)
      if [[ -n "$author" && -n "$me" && "$author" == "$me" ]]; then
        warn "self-authored PR — binding verdict downgraded to COMMENT"
        event="COMMENT"
      elif [[ -z "$me" ]]; then
        warn "could not verify PR authorship — proceeding, GitHub may refuse a self-verdict"
      fi
    fi
  fi
  # Live open-thread recheck (APPROVE path only): re-list live unresolved
  # threads with the ready verb's query and compare against local opens.
  # COMMENT, REQUEST_CHANGES, dry runs, and local bundles never pay this call
  # (record lives on another verb entirely). Unreachable degrades to COMMENT
  # (warn-and-proceed, stale-SHA idiom — never a silent APPROVE, never a hard
  # failure). More live opens than local opens refuses APPROVE (approve-blocked
  # naming the counts plus the triage-or-override path).
  if [[ "$event" == "APPROVE" && "$dry_run" != "true" ]]; then
    if live_open=$(live_open_thread_count "$repo" "$pr" 2>"$dir/.recheck.err"); then
      case "$live_open" in ''|*[!0-9]*) live_open=0;; esac
      if (( live_open > n_open )) && [[ "$force" != "true" ]]; then
        fail "approve-blocked" "refusing APPROVE: $live_open live open thread(s) vs $n_open local open finding(s) — $(( live_open - n_open )) thread(s) the store does not know about (fetch + triage them, or re-run with --force)"
      fi
    else
      warn "live thread recheck unreachable — downgrading to COMMENT (posting on local findings only)"
      body="> ⚠️ Note: live thread recheck unreachable ($(head -c 120 "$dir/.recheck.err" | tr '\n' ' ')); posting as COMMENT instead of APPROVE.

$body"
      event="COMMENT"
    fi
  fi
  echo "preflight: body=$marker ($(printf '%s' "$body" | wc -c | tr -d ' ') chars) | anchors: $n_kept inline, $n_moved moved | findings: $n_open open, $n_def deferred, $n_addr addressed, $n_term terminal | hunks indexed: $hunk_n | event=$event sha=${commit_id:0:7}"
  if (( n_open > 0 )) && [[ -n "${commit_id:0:7}" ]]; then echo "next: address open threads, then fetch --base-sha ${commit_id:0:7} for the delta"; elif (( n_open > 0 )); then echo "next: address open threads"; else echo "next: none open — nothing to address"; fi
  # Payload carries only GitHub-known comment keys — agent-side triage fields stay in
  # findings.json, and suggestions render as fenced blocks inside body (raw keys are
  # silently dropped by the API, so they never ship as keys).
  post_comments=$(jq '[.[] | {path,line,side,body,start_line} + (if (.suggestion // "") != "" then {body: ((.body // "") + "\n\n```suggestion\n" + .suggestion + "\n```")} else {} end) | with_entries(select(.value != null))]' <<<"$kept")
  embedded=$(jq '[.[] | select((.suggestion // "") != "")] | length' <<<"$kept")
  phash=""
  payload=$(jq -n --arg body "$body" --arg event "$event" --arg commit "$commit_id" --argjson comments "$post_comments" \
    '{body:$body, event:$event, commit_id:(if $commit=="" then null else $commit end), comments:$comments} | with_entries(select(.value!=null))')
  echo "» event=$event inline=$(jq 'length' <<<"$kept") embedded=$embedded resolved_count=$(jq -r '.resolved // [] | length' "$review_file")"
  if [[ "$dry_run" == "true" ]]; then
    # Footer preview for the dry run: the exact block validate_and_merge
    # appended to the payload body above (null when publish.footer is off).
    # Additive-only inside data.body — the posted body already carries it.
    dry_footer_text=$(render_footer "$dir")
    if [[ -n "$dry_footer_text" ]]; then
      dry_footer_json=$(jq -n --arg f "$dry_footer_text" '$f')
    else
      dry_footer_json="null"
    fi
    # Diagram disposition for the dry run: what validate_behavioral_diagram
    # did with the agent block (null when the body carried no candidates).
    # Additive-only inside data.body — absent reads null, like the footer.
    case "${DIAGRAM_BLOCKS:-0}" in ''|*[!0-9]*) DIAGRAM_BLOCKS="0";; esac
    if [[ "${DIAGRAM_STATUS:-absent}" == "absent" ]]; then
      diagram_json="null"
    else
      diagram_json=$(jq -n --arg s "$DIAGRAM_STATUS" --arg d "$DIAGRAM_DETAIL" --arg k "$DIAGRAM_KEYWORD" \
        --argjson b "$DIAGRAM_BLOCKS" --arg w "$DIAGRAM_WRAPPED" --arg c "$DIAGRAM_COLLAPSED" --arg n "$DIAGRAM_NOTE" \
        '{status:$s,blocks:$b,keyword:(if $k=="" then null else $k end),wrapped:($w=="true"),collapsed:($c=="true"),detail:$d,note:(if $n=="" then null else $n end)}')
    fi
    if [[ "$json_mode" == "true" ]]; then
      phash=$(printf '%s' "$payload" | shasum -a 1 | cut -d' ' -f1)
      jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg e "$event" --arg c "$commit_id" \
        --argjson k "$n_kept" --argjson m "$n_moved" --argjson o "$n_open" --argjson d "$n_def" --argjson a "$n_addr" \
        --argjson items "$(jq '[.[] | {sig, status, anchored, locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)}]' <<<"$entries")" \
        --arg h "$phash" --argjson emb "$embedded" --arg mk "$marker" --arg f "$findings_file" \
        --argjson conf "$rev_confidence" --argjson sec "$rev_security" \
        --argjson footer "$dry_footer_json" --argjson diagram "$diagram_json" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"payload NOT posted (dry-run)",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dry_run:true,event:$e,commit_id:$c,body:{marker:$mk,footer:$footer,diagram:$diagram},anchors:{kept:$k,moved:$m},
          suggestions:{embedded:$emb},findings:{open:$o,deferred:$d,addressed:$a,file:$f},confidence:$conf,security_summary:$sec,
          items:$items,payload_hash:$h},error:null}' >&9
    else
      echo "dry-run: payload NOT posted. body chars: $(printf '%s' "$body" | wc -c | tr -d ' '), payload comment keys: $(jq -r '.[0] // {} | keys_unsorted | join(",")' <<<"$post_comments")"
      if [[ -n "$dry_footer_text" ]]; then printf 'footer preview:\n%s\n' "$dry_footer_text"; fi
      # Diagram line only when the body carried a diagram (absent keeps the
      # human dry-run bytes identical for diagram-less reviews).
      if [[ "${DIAGRAM_STATUS:-absent}" != "absent" ]]; then
        echo "diagram: $DIAGRAM_STATUS — $DIAGRAM_DETAIL"
      fi
    fi
    exit 0
  fi
  if [[ "$json_mode" == "true" ]]; then
    echo "dry-run flag missing with --json on a real post is allowed; envelope follows the post" >&2
  fi
  # Double-post guard: refuse exact payload replays (same body+comments+sha hash)
  # unless --force. Legit re-reviews (new sha/content) hash differently and pass.
  phash=$(printf '%s' "$payload" | shasum -a 1 | cut -d' ' -f1)
  marker_file="$dir/.posted_$phash"
  if [[ -f "$marker_file" && "$force" != "true" ]]; then
    fail "duplicate-payload" "identical payload already posted — refusing duplicate (pass --force to repost)"
  fi
  # Single-review finality (SHA-keyed, coarse guard): one completed post per
  # reviewed SHA. post.json (written only by the real-post path below) carries
  # the reviewed head — a repeat post on the same SHA refuses unless --force;
  # any new SHA proceeds and refreshes the record on success. Dry runs exit
  # above and failures below never reach the writer, so neither arms the key.
  # Unlike the marker above (exact replays only), this also refuses same-SHA
  # reposts with different content. Unreadable/head-less records warn and
  # proceed (overwritten on success) — they never wedge the run.
  if [[ -n "$commit_id" && "$force" != "true" && -f "$dir/post.json" ]]; then
    if ! stored_head=$(jq -r '.head // empty' "$dir/post.json" 2>/dev/null); then
      warn "post record unreadable — proceeding, will refresh it on success"
    elif [[ -z "$stored_head" ]]; then
      warn "post record carries no head — proceeding, will refresh it on success"
    elif [[ "$stored_head" == "$commit_id" ]]; then
      stored_at=$(jq -r '.posted_at // "unknown time"' "$dir/post.json" 2>/dev/null || echo "unknown time")
      fail "already-posted" "refusing second post on reviewed SHA ${commit_id:0:7} (already posted at $stored_at — new commits re-arm automatically; pass --force to override, or run ./scripts/review.sh fetch $repo $pr --base-sha ${stored_head:0:7} and review the delta)"
    fi
  fi
  # Bounded transient retry: only the exact GitHub "internal error" body retries (2x,
  # 1s/2s), comments untouched. Anything else fails fast with a narrowed message —
  # never drop comments chasing a server blip.
  attempt=0; posted=""
  while (( attempt < 3 )); do
    if posted=$(echo "$payload" | gh api "repos/$repo/pulls/$pr/reviews" --method POST --input - --jq '{id, state, html_url, submitted_at}' 2>"$dir/.post.err"); then
      break
    elif grep -qi 'internal error occurred, please try again' "$dir/.post.err"; then
      attempt=$(( attempt + 1 ))
      if (( attempt < 3 )); then
        warn "transient GitHub error — retry $attempt/2, comments untouched"
        sleep "$attempt"
        continue
      fi
    fi
    break
  done
  if [[ -z "$posted" ]]; then
    fail "post-failed" "post failed: $(head -c 300 "$dir/.post.err" | tr '\n' ' ') — narrow the cause (anchor drift? body size?) — do not drop comments blindly."
  fi
  touch "$marker_file"
  # Calibration enabler: record what GitHub accepted, so a future calibrate verb can
  # join our findings against ground truth (thread states, merge outcome) by review id.
  # Sole post.json writer in this script (dry runs write findings.dryrun.json only and
  # never touch post.json) — so a present matching head proves a completed post,
  # never an attempt, dry run, or failure. The head doubles as the single-review
  # finalizer key: the guard above refuses a repeat post on the same SHA.
  jq -n --argjson rv "$posted" --arg c "$commit_id" --arg t "$now" \
    --argjson conf "$rev_confidence" --argjson sec "$rev_security" \
    '{schema_version:"1",review:$rv,head:$c,posted_at:$t,confidence:$conf,security_summary:$sec}' > "$dir/post.json"
  if [[ "$json_mode" == "true" ]]; then
    posted_json="$posted"
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg e "$event" --arg c "$commit_id" \
      --argjson k "$n_kept" --argjson m "$n_moved" --argjson o "$n_open" --argjson d "$n_def" --argjson a "$n_addr" \
      --argjson items "$(jq '[.[] | {sig, status, anchored, locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)}]' <<<"$entries")" \
      --arg h "$phash" --argjson emb "$embedded" --arg mk "$marker" --argjson rv "$posted_json" \
      --argjson conf "$rev_confidence" --argjson sec "$rev_security" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $o > 0 then "address open threads" else "none open — nothing to address" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dry_run:false,event:$e,commit_id:$c,body:{marker:$mk},anchors:{kept:$k,moved:$m},
        suggestions:{embedded:$emb},findings:{open:$o,deferred:$d,addressed:$a},items:$items,confidence:$conf,security_summary:$sec,
        payload_hash:$h,review:$rv},error:null}' >&9
  else
    echo "$posted"
  fi
  exit 0
fi

# ---------------------------------------------------------------- record ---
# Validate + merge a local review into findings.json (no GitHub, no post).
# record <owner/repo> <local-slug> [--json]
if [[ "$cmd" == "record" ]]; then
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for record (want --json)" >&2; exit 2;;
  esac; done
  rentry_cmd="./scripts/review.sh record $repo $pr"
  [[ "$json_mode" == "true" ]] && rentry_cmd="$rentry_cmd --json"
  json_redirect
  resolve_bundle_dir "$pr"
  case "$dir" in .review/local-*) prov="local";;
    *)
      prov=$(jq -r '.provider // empty' "$dir/PR.json" 2>/dev/null || true)
      if [[ -z "$prov" ]]; then fail "not-local" "record works on local bundles and provider PRs (fetch-local / fetch --provider first); GitHub PRs use post"; fi
      if [[ "$prov" == "github" ]]; then fail "not-local" "GitHub PRs use post; record is for local bundles and provider PRs"; fi;; esac
  review_file="$dir/review.json"
  [[ -f "$review_file" ]] || fail "missing-review-file" "missing $review_file (agent must write {body, approved, comments[]})"
  [[ -f "$dir/full.diff" ]] || fail "missing-diff" "missing $dir/full.diff (run fetch-local / fetch --provider first)"
  if [[ "$prov" == "local" ]]; then
    [[ -f "$dir/source.json" ]] || fail "missing-source" "missing $dir/source.json (run fetch-local first)"
  else
    [[ -f "$dir/PR.json" ]] || fail "missing-source" "missing $dir/PR.json (run fetch --provider first)"
  fi
  ensure_store "$dir"
  if [[ "$prov" == "local" ]]; then
    head_sha=$(jq -r '.head_sha // empty' "$dir/source.json")
    if [[ -z "$head_sha" ]]; then head_ref=$(jq -r '.head_ref // "worktree"' "$dir/source.json"); head_sha="dirty"; fi
  else
    head_sha=$(pr_field "$dir" head_sha)
    [[ -z "$head_sha" ]] && head_sha="unknown"
  fi
  now=$(now_utc)
  ensure_hunks_snapshot "$dir"
  body=$(jq -r '.body // ""' "$review_file")
  # Degenerate-body guard (same bar as post: no verdict, no post).
  if ! printf '%s' "$body" | grep -qE '^> ✅|^> ℹ️|^> \[!IMPORTANT\]|^> \[!CAUTION\]'; then
    len=$(printf '%s' "$body" | wc -c | tr -d ' ')
    if [[ "$len" -lt 20 ]]; then fail "degenerate-body" "refusing degenerate body (<20 chars, no verdict marker)."; fi
    if [[ "$len" -lt 50 ]] && printf '%s' "$body" | grep -qiE 'test|testing|placeholder|temp|foo|bar|asdf|dummy|sample'; then
      fail "degenerate-body" "refusing placeholder-looking short body."
    fi
  fi
  findings_file="$dir/findings.json"
  validate_and_merge "$review_file" "$dir" "$head_sha" "$now" "$findings_file"
  diff_sha=$(shasum -a 256 "$dir/full.diff" 2>/dev/null | cut -d" " -f1 || true)
  jq -n --arg st "$prov" --arg h "$head_sha" --arg dsha "$diff_sha" --arg t "$now" \
    --argjson f "$(jq 'length' <<<"$entries")" --argjson o "$n_open" \
    --argjson conf "$rev_confidence" --argjson sec "$rev_security" \
    '{schema_version:"1",status:$st,head:$h,diff_sha256:$dsha,recorded_at:$t,findings:$f,open:$o,confidence:$conf,security_summary:$sec}' > "$dir/record.json"
  echo "recorded: $dir/findings.json ($n_open open) + $dir/record.json — report/triage read it; post stays refused (no PR target)"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg d "$dir" \
      --argjson k "$n_kept" --argjson m "$n_moved" --argjson o "$n_open" --argjson dl "$n_def" --argjson a "$n_addr" \
      --argjson items "$(jq '[.[] | {sig, status, anchored, locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)}]' <<<"$entries")" \
      --argjson conf "$rev_confidence" --argjson sec "$rev_security" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:"report or triage the local bundle; post stays refused (no PR target)",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,anchors:{kept:$k,moved:$m},
        findings:{open:$o,deferred:$dl,addressed:$a,file:($d + "/findings.json")},confidence:$conf,security_summary:$sec,items:$items},error:null}' >&9
  fi
  exit 0
fi

# ----------------------------------------------------------------- adopt ---
# Carry a local bundle's findings into a fresh PR bundle for posting.
# adopt <owner/repo> <pr> --from <local-slug> [--json]
if [[ "$cmd" == "adopt" ]]; then
  from=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --from) from="${2:-}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for adopt (want --from <local-slug>, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  [[ "$pr" == local-* ]] && fail "bad-target" "adopt targets a PR bundle, not another local one"
  [[ -z "$from" ]] && fail "bad-from" "adopt needs --from <local-slug> (a .review/local-* dir with findings.json)"
  case "$from" in local-*) from_dir=".review/$from";; *) from_dir=".review/local-$from";; esac
  dir=".review/$pr"
  [[ -f "$from_dir/findings.json" ]] || fail "missing-findings" "missing $from_dir/findings.json (record the local review first)"
  [[ -f "$dir/full.diff" ]] || fail "missing-diff" "missing $dir/full.diff (fetch the PR first)"
  ensure_store "$dir"
  prov=$(jq -r '.provider // "github"' "$dir/PR.json" 2>/dev/null || echo "github")
  if [[ "$prov" != "github" ]]; then fail "provider-unimplemented" "adopt needs a GitHub PR target (only GitHub posts exist to publish into)"; fi
  [[ -f "$dir/findings.json" ]] && fail "already-adopted" "$dir/findings.json exists — adopt merges once into a fresh fetch (re-fetch or post first)"
  commit_id=$(pr_field "$dir" head_sha)
  now=$(now_utc)
  ensure_hunks_snapshot "$dir"
  # Copy findings verbatim, stamp the handoff, re-resolve anchors against PR hunks.
  entries=$(jq --arg now "$now" --arg sha "${commit_id:0:7}" --arg from "$from" \
    '[.[] | . + {lastSeen:$sha, history:(.history + [{at:$now, event:"adopted", actor:"script", note:("from " + $from)}])}]' "$from_dir/findings.json")
  reanchored=$(jq -c '.[]' <<<"$entries" | while IFS= read -r e; do
    p=$(jq -r '.path // empty' <<<"$e"); line=$(jq -r '.line // empty' <<<"$e")
    side=$(jq -r '.side // "RIGHT"' <<<"$e" | tr '[:lower:]' '[:upper:]'); start=$(jq -r '.start_line // empty' <<<"$e")
    if [[ -z "$p" || -z "$line" ]] || ! [[ "$line" =~ ^[0-9]+$ ]]; then printf '%s\n' "$e"; continue; fi
    [[ -z "$start" ]] && start="$line"
    if ! [[ "$start" =~ ^[0-9]+$ ]]; then printf '%s\n' "$e"; continue; fi
    if (( start > line )); then tmp="$start"; start="$line"; line="$tmp"; fi
    want="NEW"; [[ "$side" == "LEFT" ]] && want="OLD"
    key="$p"; [[ "$key" == a/* || "$key" == b/* ]] && key="${key:2}"
    if awk -F'\t' -v f="$key" -v w="$want" -v a="$start" -v b="$line" '$1==f && $2==w && $3<=a && b<=$4 {found=1; exit} END{exit !found}' "$dir/hunks.tsv"; then
      printf '%s\n' "$e"
    else
      jq -c '.anchored = false' <<<"$e"
    fi
  done | jq -s '.')
  entries="$reanchored"
  n_open=$(jq '[.[] | select(.status == "open")] | length' <<<"$entries")
  echo "$entries" | jq '.' > "$dir/findings.json"
  echo "» adopted $(jq 'length' <<<"$entries") findings from $from_dir (open: $n_open) — post to publish, calibrate to settle"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg d "$dir" --arg f "$from" \
      --argjson n "$(jq 'length' <<<"$entries")" --argjson o "$n_open" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:"post the adopted findings, then calibrate to settle them",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,from:$f,adopted:$n,open:$o},error:null}' >&9
  fi
  exit 0
fi

# --------------------------------------------------------------- triage ---
if [[ "$cmd" == "triage" ]]; then
  tsig=""; tstatus=""; trat=""; tforce="false"; tdry="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --sig) tsig="${2:-}"; shift 2;;
    --status) tstatus="${2:-}"; shift 2;;
    --rationale) trat="${2:-}"; shift 2;;
    --force) tforce="true"; shift;;
    --dry-run) tdry="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for triage (want --sig, --status, --rationale, --force, --dry-run, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  resolve_bundle_dir "$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post or record first)"
  case "$tstatus" in open|addressed|wont-fix|false-positive|deferred) ;;
    *) fail "bad-triage-status" "refusing triage: unknown status '$tstatus' (want open|addressed|wont-fix|false-positive|deferred)";; esac
  tfull=$(resolve_sig "$(cat "$dir/findings.json")" "$tsig")
  case "$tfull" in
    NONE) fail "unknown-sig" "refusing triage: sig not in store";;
    MANY) fail "ambiguous-sig" "refusing triage: sig prefix ambiguous";;
  esac
  if (( ${#trat} < 30 )) && [[ "$tforce" != "true" ]]; then
    fail "rationale-required" "refusing triage: rationale required (>=30 chars) unless --force"
  fi
  now=$(now_utc)
  if [[ "$tdry" == "true" ]]; then
    echo "triage --dry-run: would set ${tfull:0:8} -> $tstatus (rationale ${#trat} chars, actor human)"
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg s "$tfull" --arg st "$tstatus" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"none — dry run, nothing written",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{sig:$s,status:$st,dry_run:true},error:null}' >&9
    fi
    exit 0
  fi
 rat_out="$trat"; [[ -z "$rat_out" && "$tforce" == "true" ]] && rat_out="forced without rationale"
  kb_resolve_policy
  KB_BROKEN="false"
  KB_STAGED="[]"
  if [[ -f "$KB_FILE" ]] && ! jq empty "$KB_FILE" >/dev/null 2>&1; then
    warn "kb store $KB_FILE unreadable — no credit for this flip (fix the bad line)"
    KB_BROKEN="true"
  fi
  credited="[]"
  case "$tstatus" in
    addressed|false-positive)
      kb_apply_credit "$tfull" "$tstatus" "human" "$cmd" "$now" "$(cat "$dir/findings.json")"
      credited="$KB_CREDITED_JSON";;
  esac
  jq --arg s "$tfull" --arg st "$tstatus" --arg rat "$rat_out" --arg now "$now" \
    --argjson kb "$credited" \
    'map(if .sig == $s then . + {status:$st, rationale:$rat, triagedAt:$now, triagedBy:"human", history:(.history + [{at:$now, event:"triaged", actor:"human", note:$rat} + (if $kb | length > 0 then {kb_credit:$kb} else {} end)])} else . end)' \
    "$dir/findings.json" > "$dir/findings.json.tmp" && mv "$dir/findings.json.tmp" "$dir/findings.json"
  kb_commit_staged "$dir/findings.json" "$now"
  echo "triaged ${tsig:0:8} -> $tstatus"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg s "$tfull" --arg st "$tstatus" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"none — triage recorded",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{sig:$s,status:$st},error:null}' >&9
  fi
  exit 0
fi

# --------------------------------------------------------------- report ---

# --------------------------------------------------------------- report ---
if [[ "$cmd" == "report" ]]; then
  status_f="all"; kind_f="all"; output=""; sarif=""; cost="false"; check_sev=""; completeness="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --status) status_f="${2:-all}"; case "$status_f" in all|open|addressed|deferred|wont-fix|false-positive) ;; *) echo "unknown --status '$status_f' for report (want all|open|addressed|deferred|wont-fix|false-positive)" >&2; exit 2;; esac; shift 2;;
    --kind) kind_f="${2:-all}"; case "$kind_f" in all|inline|section) ;; *) echo "unknown --kind '$kind_f' for report (want all|inline|section)" >&2; exit 2;; esac; shift 2;;
    --output) output="${2:-}"; shift 2;;
    --sarif) sarif="${2:-}"; shift 2;;
    --cost) cost="true"; shift;;
    --check) check_sev="${2:-}"; case "$check_sev" in critical|important|informational) ;; *) echo "unknown --check tier '$check_sev' for report (want critical|important|informational)" >&2; exit 2;; esac; check_gate="true"; shift 2;;
    --completeness) completeness="true"; check_gate="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for report (want --status, --kind, --output, --sarif, --cost, --check, --completeness, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  resolve_bundle_dir "$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post or record first)"
  jq -e 'type == "array"' "$dir/findings.json" >/dev/null 2>&1 \
    || fail "corrupt-store" "findings store $dir/findings.json is unreadable or not a findings array (re-run post/record to rebuild, or restore the file)"
  # Section overrides (P5): fail-closed render gate — invalid fragments refuse
  # here before any fragment-derived bytes are emitted (report-time half of check+post).
  validate_overrides
  now=$(now_utc)
  head_sha=""; base_sha=""
  if [[ -f "$dir/PR.json" ]]; then
    head_sha=$(pr_field "$dir" head_sha); base_sha=$(pr_field "$dir" base_sha)
  elif [[ -f "$dir/source.json" ]]; then
    head_sha=$(jq -r '.head_sha // empty' "$dir/source.json"); base_sha=$(jq -r '.base_sha // empty' "$dir/source.json")
  fi
  panel_json="null"; panel_src=""
  if [[ -f "$dir/panel" ]]; then
    panel_json=$(jq -R . < "$dir/panel" | jq -s 'map(select(length > 0))'); panel_src="panel"
  fi
  patch_json="null"
  if [[ -f "$dir/patch.json" ]]; then patch_json=$(cat "$dir/patch.json"); fi
  # Completeness dimension (opt-in HEAD-reviewed gate, --completeness only):
  # pass iff a completed-post record exists, parses, and carries the fetched
  # HEAD. Only the real-post path writes post.json (dry runs write
  # findings.dryrun.json, attempts/failures write nothing), so a present
  # matching head proves a completed post. Pure local reads, no network.
  # Stays null without the flag, so default output is byte-identical.
  completeness_json="null"; comp_reviewed="false"; comp_posted=""; comp_refetch=""
  if [[ "$completeness" == "true" ]]; then
    if [[ -f "$dir/post.json" ]] && comp_posted=$(jq -r '.head // empty' "$dir/post.json" 2>/dev/null) && [[ -n "$comp_posted" ]]; then
      : # present + parsed + non-empty head — compared below
    else
      comp_posted=""
    fi
    comp_prov=$(jq -r '.provider // empty' "$dir/PR.json" 2>/dev/null || true)
    if [[ "$dir" == .review/local-* ]]; then
      comp_refetch="./scripts/review.sh fetch-local $repo ${pr#local-}"
    elif [[ -n "$comp_prov" && "$comp_prov" != "github" ]]; then
      comp_refetch="./scripts/review.sh fetch $repo $pr --provider $comp_prov"
    elif [[ -n "$comp_posted" ]]; then
      comp_refetch="./scripts/review.sh fetch $repo $pr --base-sha ${comp_posted:0:7}"
    else
      comp_refetch="./scripts/review.sh fetch $repo $pr"
    fi
    if [[ -n "$head_sha" && -n "$comp_posted" && "$head_sha" == "$comp_posted" ]]; then
      comp_reviewed="true"
    fi
    completeness_json=$(jq -n --argjson reviewed "$comp_reviewed" --arg f "$head_sha" --arg p "$comp_posted" \
      '{reviewed:$reviewed,fetched_head:(if $f == "" then null else $f end),posted_head:(if $p == "" then null else $p end)}')
  fi
  # Rank: open first (deferred second), severity, rigor desc, anchored/inBounds first, path/line/sig.
  ranked=$(jq --arg sf "$status_f" --arg kf "$kind_f" '
    def rw: {"reproduced":4,"ran":3,"walked":2,"pointed":1,"said":0};
    def sk: if .status == "open" then 0 elif .status == "deferred" then 1 else 2 end;
    def sev: {"critical":0,"important":1,"informational":2};
    [ .[]
      | select(if $sf == "all" then true elif $sf == "open" then .status == "open"
               elif $sf == "addressed" then .status == "addressed" else .status == $sf end)
      | select(if $kf == "all" then true else .kind == $kf end)
      | . + {locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)} ]
    | sort_by([sk, (sev[.severity // ""] // 3), (4 - rw[.rigor // "said"]), (if .anchored then 0 else 1 end),
               (if .inBounds then 0 else 1 end), (.path // ""), (.line // 0), .sig])
    | to_entries | map(.value + {rank: (.key + 1)})' "$dir/findings.json")
  n_open=$(jq '[.[] | select(.status == "open")] | length' <<<"$ranked" 2>/dev/null || echo 0)
  counts=$(jq '{inline: ([.[] | select(.kind == "inline")] | length),
    section: ([.[] | select(.kind == "section")] | length),
    anchored: ([.[] | select(.kind == "inline" and .anchored == true)] | length),
    unanchored: ([.[] | select(.kind == "inline" and .anchored != true)] | length)}' "$dir/findings.json")
  n_all_open=$(jq '[.[] | select(.status == "open")] | length' "$dir/findings.json")
  # Cost rollup (--cost only): the agent self-reports spend into cost.json (one
  # record per run; an array when a bundle was reviewed more than once). The
  # script only sums — no price table lives here, it would rot. Absent or
  # unreadable records contribute nothing and never fail the run. Each record
  # carries an effort tier (cheap/standard/deep); missing or unknown values
  # read as standard, so legacy records keep working.
  cost_json="null"
  if [[ "$cost" == "true" ]]; then
    cost_zeros=$(jq -n '{runs:0,runs_with_cost:0,input_tokens:0,output_tokens:0,total_tokens:0,est_usd:null,
      by_tier:(["cheap","standard","deep"] | map({key:.,value:{runs:0,input_tokens:0,output_tokens:0,total_tokens:0}}) | from_entries)}')
    # Shared rollup filter: one cost record per agent run (an object), or an
    # array when a bundle was reviewed more than once. Kept in a variable so
    # the top-level rollup and the per-chunk rollup below stay one filter.
    cost_rollup_jq='
        def tier: (if type == "object" then (.effort // "standard" | tostring | ascii_downcase) else "standard" end
          | if . == "cheap" or . == "standard" or . == "deep" then . else "standard" end);
        (if type == "array" then . else [.] end) as $runs
        | ([$runs[] | .models // [] | .[]]) as $models
        | {runs: ($runs | length),
           runs_with_cost: ([$runs[] | select((.models // []) | length > 0)] | length),
           input_tokens: ([$models[] | .input_tokens | numbers] | add // 0),
           output_tokens: ([$models[] | .output_tokens | numbers] | add // 0),
           est_usd: ([$runs[] | .est_usd | numbers] | if length == 0 then null else (add * 10000 | round / 10000) end),
           by_tier: (["cheap","standard","deep"] | map(. as $t | {key: $t, value: (
             [$runs[] | select(tier == $t)] as $r
             | {runs: ($r | length),
                input_tokens: ([$r[] | (.models // [])[] | .input_tokens | numbers] | add // 0),
                output_tokens: ([$r[] | (.models // [])[] | .output_tokens | numbers] | add // 0)}
             | .total_tokens = (.input_tokens + .output_tokens))}) | from_entries)}
        | .total_tokens = (.input_tokens + .output_tokens)'
    if [[ -f "$dir/cost.json" ]]; then
      cost_json=$(jq "$cost_rollup_jq" "$dir/cost.json" 2>/dev/null) \
        || { warn "cost.json unreadable — spend unrecorded for this rollup"; cost_json="$cost_zeros"; }
    else
      cost_json="$cost_zeros"
    fi
    # Chunk cost rollup (additive read-path only): per-chunk cost records live
    # beside their chunk diffs as <dir>/chunks/<name>.cost.json — same shape
    # as cost.json (one record or an array), keyed by the manifest chunk
    # name. The manifest names the chunks; an unreadable manifest falls back
    # to the on-disk glob. Absent per-chunk records contribute zeros;
    # unreadable ones warn and contribute zeros — neither ever fails the run.
    # Bundles without per-chunk records keep the rollup above byte-identical
    # (this branch runs only when at least one per-chunk file exists).
    cc_files=""
    if [[ -d "$dir/chunks" ]]; then
      if [[ -f "$dir/chunks/manifest.json" ]]; then
        while IFS= read -r cc_name; do
          case "$cc_name" in ""|*[/]*) continue;; esac
          cc_cand="$dir/chunks/$cc_name.cost.json"
          [[ -f "$cc_cand" ]] && cc_files="${cc_files}${cc_cand}
"
        done < <(jq -r '.chunks[]? | .name // empty' "$dir/chunks/manifest.json" 2>/dev/null || true)
      fi
      for cc_cand in "$dir"/chunks/chunk-*.cost.json; do
        [[ -f "$cc_cand" ]] || continue
        cc_files="${cc_files}${cc_cand}
"
      done
      cc_files=$(printf '%s' "$cc_files" | sort -u || true)
    fi
    if [[ -n "$cc_files" ]]; then
      cc_recs="[]"
      if [[ -f "$dir/cost.json" ]]; then
        # An unreadable top-level cost already warned above; here it just
        # contributes nothing to the merged stream.
        cc_one=$(jq '[if type == "array" then .[] else . end]' "$dir/cost.json" 2>/dev/null || true)
        [[ -n "$cc_one" ]] && cc_recs=$(jq -n --argjson a "$cc_recs" --argjson b "$cc_one" '$a + $b')
      fi
      while IFS= read -r cc_f; do
        [[ -n "$cc_f" ]] || continue
        if cc_one=$(jq '[if type == "array" then .[] else . end]' "$cc_f" 2>/dev/null); then
          cc_recs=$(jq -n --argjson a "$cc_recs" --argjson b "$cc_one" '$a + $b')
        else
          warn "chunk cost $(basename "$cc_f") unreadable — spend unrecorded for this chunk"
        fi
      done <<<"$cc_files"
      cost_json=$(jq "$cost_rollup_jq" <<<"$cc_recs")
    fi
    # Receipts for consented over-budget runs (budget.json, written by the
    # fetch-time consent gate). Shown only when the file exists and parses, so
    # rolls without a control plane stay byte-identical.
    receipts_json="null"
    if [[ -f "$dir/budget.json" ]]; then
      if receipts_json=$(jq 'if type == "array" then . else [.] end' "$dir/budget.json" 2>/dev/null); then
        cost_json=$(jq --argjson rc "$receipts_json" '. + {receipts: $rc}' <<<"$cost_json")
      else
        warn "budget.json unreadable — receipts unlisted for this rollup"
        receipts_json="null"
      fi
    fi
  fi
  # Live-thread probe (network, first half of the gate's third dimension):
  # runs ahead of rendering so its warn lands in the --json envelope
  # warnings[] as well as stderr (the envelope emits below, the verdict
  # prints in the gate tail). live_status: pass|fail|skip|unknown;
  # live_open carries the count on pass/fail. Skips and unreachable lookups
  # warn and stay neutral here — never fail(), never exit 3 on network
  # trouble. Advisory runs (no gate flag) skip this entirely: zero network,
  # byte-identical output.
  live_status="unset"; live_open="0"
  if [[ "$check_gate" == "true" ]]; then
    if [[ "$dir" == .review/local-* ]]; then
      warn "live recheck skipped: local bundle threads unreadable here (gating on local dimensions only)"
      live_status="skip"
    else
      live_prov=$(jq -r '.provider // empty' "$dir/PR.json" 2>/dev/null || true)
      if [[ -n "$live_prov" && "$live_prov" != "github" ]]; then
        warn "live recheck skipped: $live_prov threads unreadable here (gating on local dimensions only)"
        live_status="skip"
      elif live_open=$(live_open_thread_count "$repo" "$pr" 2>"$dir/.recheck.err"); then
        case "$live_open" in ''|*[!0-9]*) live_open=0;; esac
        if (( live_open > n_all_open )); then live_status="fail"; else live_status="pass"; fi
        rm -f "$dir/.recheck.err"
      else
        live_err=$(head -c 120 "$dir/.recheck.err" 2>/dev/null | tr '\n' ' ')
        warn "live thread recheck unreachable (${live_err:-lookup failed} — gating on local dimensions only)"
        rm -f "$dir/.recheck.err"
        live_status="unknown"
      fi
    fi
  fi
  if [[ -n "$output" ]]; then
    # Confidence-led lead: review-level confidence + safety assessment from the
    # already-stored completed-post/machine records (post.json for PR bundles,
    # record.json for local ones). Reads only — absent/unparsable records read
    # neutral, never fail.
    rep_conf="unrecorded"; rep_sec="not assessed"
    for rep_rec in "$dir/post.json" "$dir/record.json"; do
      [[ -f "$rep_rec" ]] || continue
      rep_c=$(jq -r '.confidence // empty' "$rep_rec" 2>/dev/null || true)
      case "$rep_c" in 1|2|3|4|5) rep_conf="$rep_c/5";; esac
      rep_s=$(jq -r '.security_summary // empty' "$rep_rec" 2>/dev/null || true)
      [[ -n "$rep_s" ]] && rep_sec="$rep_s"
      break
    done
    # Bounded coverage list on clean reports (no open findings): up to 7 files
    # from the stored bundle (PR.json file list, else diff headers) — no new
    # data collection. Full reasoning stays in the machine envelope (--json
    # items); the file keeps 200-char summaries.
    rep_checked=""
    if (( n_all_open == 0 )); then
      rep_files=""
      if [[ -f "$dir/PR.json" ]]; then
        rep_files=$(jq -r '.files[]? | "\(.path) (+\(.additions // 0)/-\(.deletions // 0))"' "$dir/PR.json" 2>/dev/null || true)
      fi
      if [[ -z "$rep_files" && -f "$dir/full.diff" ]]; then
        rep_files=$(grep '^diff --git ' "$dir/full.diff" 2>/dev/null | sed -e 's/^diff --git //' -e 's/.* b\///' || true)
      fi
      if [[ -n "$rep_files" ]]; then
        rep_n=$(printf '%s\n' "$rep_files" | grep -c . || true)
        rep_checked=$(printf '%s\n' "$rep_files" | head -n 7 | sed 's/^/- /')
        if (( rep_n > 7 )); then
          rep_checked="$rep_checked
(+$(( rep_n - 7 )) more — see toc.txt for the full coverage manifest)"
        fi
      fi
    fi
    # Permalink locators (P2): GitHub bundles render each anchored finding
    # as a reference-style `[path:line][ev-N]` link pinned to the reviewed
    # SHA, with the definitions collected below the list (invisible when
    # rendered, clickable everywhere). Non-GitHub and local bundles keep
    # plain `path:line` text — a blob URL there would 404.
    rep_linkable=$(evidence_linkable "$dir" "$head_sha")
    rep_base=""
    if [[ "$rep_linkable" == "true" ]]; then rep_base=$(evidence_base_url "$dir" "$repo"); fi
    [[ -n "$rep_base" ]] || rep_linkable="false"
    rep_defs=""
    if [[ "$rep_linkable" == "true" ]]; then
      rep_defs=$(jq -r --arg base "$rep_base" --arg sha "$head_sha" \
        '.[] | select(.path != null and .line != null) | "[ev-\(.rank)]: \($base)/blob/\($sha)/\(.path)#L\(.line)"' <<<"$ranked" 2>/dev/null || true)
    fi
    { echo "# Review report — $repo#$pr"; echo
      echo "<!-- report-machine:start -->"
      echo "> Confidence: $rep_conf · Safety: $rep_sec"
      basis_line "$dir/findings.json"
      echo "## Counts — open $n_all_open · head ${head_sha:0:7}"
      jq -r --arg linkable "$rep_linkable" '.[] | "\(.rank). [\(.status)] " + (if $linkable == "true" and .path != null and .line != null then "[\(.locator)][ev-\(.rank)]" else .locator end) + " (\(.sig[0:8]), \(.rigor // "?")) — \((.body // "" | gsub("\n"; " ") | .[0:200]))"' <<<"$ranked"
      if [[ -n "$rep_defs" ]]; then printf '%s\n' "$rep_defs"; fi
      rep_overview=$(render_overview "$dir" "$pr")
      if [[ -n "$rep_overview" ]]; then printf '%s\n' "$rep_overview"; fi
      if [[ -n "$rep_checked" ]]; then
        # P5: the preamble fragment rewords this clean-review preamble heading
        # (prose style, v1 placeholders); absent reads today's heading (parity).
        if [[ -f "$OVERRIDE_PREAMBLE" ]]; then
          rep_heading=$(substitute_override_placeholders "$(override_preamble_text)" "$dir")
          [[ -n "$rep_heading" ]] && printf '%s\n' "$rep_heading"
        else
          echo "## What we checked"
        fi
        printf '%s\n' "$rep_checked"
      fi
      rep_footer=$(render_footer "$dir")
      if [[ -n "$rep_footer" ]]; then printf '%s\n' "$rep_footer"; fi
      echo "<!-- report-machine:end -->"
    } > "$output"
  fi
  if [[ -n "$sarif" ]]; then
    jq -n --argjson items "$ranked" --arg r "$repo" --arg p "$pr" '
      ([$items[] | .kind] | unique) as $kinds
      | {version:"2.1.0", "$schema":"https://json.schemastore.org/sarif-2.1.0.json",
         runs:[{tool:{driver:{name:"verdict", version:"1",
                  informationUri:"https://github.com",
                  rules:[$kinds[] | {id:("verdict/" + .), name:.,
                    shortDescription:{text:("verdict " + . + " finding")}}]}},
                results:[$items[]
                  | {ruleId:("verdict/" + .kind),
                     level:({"critical":"error","important":"warning"}[.severity // ""] // "note"),
                     message:{text:((if .heading then ("[" + .heading + "] ") else "" end) + (.body // ""))},
                     locations:[{physicalLocation:{
                       artifactLocation:{uri:(.path // "PR")},
                       region:(if .line then {startLine:.line} else {} end)}}],
                     partialFingerprints:{"verdict/sig":.sig}}]}]}' > "$sarif"
    echo "wrote $sarif"
  fi
  if [[ "$json_mode" == "true" ]]; then
    [[ -n "$output" ]] && echo "wrote $output"
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg d "$dir" \
      --arg hs "$head_sha" --arg bs "$base_sha" --argjson items "$ranked" --argjson counts "$counts" \
      --argjson panel "$panel_json" --arg psrc "$panel_src" --argjson patch "$patch_json" \
      --argjson cost "$cost_json" --argjson comp "$completeness_json" \
      --arg o "$output" --arg sf "$status_f" --arg kf "$kind_f" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $o == "" then (if ([$items[] | select(.status=="open")] | length) > 0 then "address open threads" else "none open — nothing to address" end) else "wrote \($o)" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:({generated_at:$t,dir:$d,head:{fetched:$hs,base:$bs,live:null,stale:false},
        verdict:{open:([$items[] | select(.status=="open")] | length),deferred:([$items[] | select(.status=="deferred")] | length),addressed:([$items[] | select(.status=="addressed")] | length),terminal:([$items[] | select(.status=="wont-fix" or .status=="false-positive")] | length)},
        counts:$counts,panel:{source:(if $psrc=="" then null else $psrc end),members:($panel // [])},
        patch:$patch,output:(if $o=="" then null else $o end),filters:{status:$sf,kind:$kf},items:$items}
        + (if $cost == null then {} else {cost:$cost} end)
        + (if $comp == null then {} else {completeness:$comp} end)),error:null}' >&9
  else
    echo "report: $dir ($(jq 'length' <<<"$ranked") shown) open=$n_open head=${head_sha:0:7}"
    jq -r '.[] | "\(.rank). [\(.status)] \(.locator) (\(.sig[0:8]))"' <<<"$ranked"
    if [[ "$cost" == "true" ]]; then
      if [[ "$(jq -r '.runs' <<<"$cost_json")" == "0" ]]; then
        echo "cost: unrecorded (no usable $dir/cost.json — the agent writes one per prompts/review.md)"
      else
        echo "cost: $(jq -r '"\(.runs) run(s), \(.runs_with_cost) with records — in=\(.input_tokens) out=\(.output_tokens) total=\(.total_tokens) est_usd=\(.est_usd // "n/a")"' <<<"$cost_json")"
        echo "tiers: $(jq -r '"cheap=\(.by_tier.cheap.runs // 0) standard=\(.by_tier.standard.runs // 0) deep=\(.by_tier.deep.runs // 0)"' <<<"$cost_json")"
      fi
      if [[ "$receipts_json" != "null" ]]; then
        n_rc=$(jq 'length' <<<"$receipts_json")
        lp=$(jq -r '.[-1].projected_usd // "?"' <<<"$receipts_json")
        lt=$(jq -r '.[-1].projected_tier // "?"' <<<"$receipts_json")
        lb=$(jq -r '.[-1].budget_usd // "?"' <<<"$receipts_json")
        la=$(jq -r '.[-1].at // "?"' <<<"$receipts_json")
        echo "receipts: $n_rc consented over-budget run(s) (latest projected \$$lp $lt vs budget \$$lb @ $la)"
      fi
    fi
    if [[ -n "$output" ]]; then echo "wrote $output"; else
      if (( n_open > 0 )); then echo "next: address open threads"; else echo "next: none open — nothing to address"; fi
    fi
  fi
  # Gate tail: --check turns the ranked read into a machine gate over OPEN
  # findings at-or-above the tier (critical outranks important outranks
  # informational; untagged findings never meet a tier; display filters do
  # not narrow the gate). --completeness adds a HEAD-reviewed dimension from
  # the local post record (match passes; missing/unparsable/empty/mismatch
  # fails with a typed check: line naming the re-fetch command). A third,
  # network-backed live-thread dimension verdicts last: more live opens than
  # local opens (whole-store n_all_open — display filters never narrow a
  # gate, same rule as the tier dimension) fails the gate with a check: line
  # naming both counts plus the fetch-and-triage path; unreachable lookups
  # warn and stay neutral, and local/non-GitHub bundles skip with a warn —
  # zero network either way. The lookup runs in the probe ahead of rendering
  # so the warn lands in the --json envelope warnings[] too. Output is
  # identical with or without any flag — only the exit differs, mirroring
  # ready --check. The store parsed above,
  # so a non-numeric count here is a tool error, never a pass. Any dimension
  # failing exits 1; tool errors exit 3 via fail(). There is no --force on
  # report: fetch + triage + re-check is the recovery, advisory report
  # without a gate flag is the escape hatch.
  if [[ "$check_gate" == "true" ]]; then
    gate_fail="false"
    if [[ -n "$check_sev" ]]; then
      n_hit=$(jq --arg t "$check_sev" '
        ({"critical":0,"important":1,"informational":2}[$t]) as $th
        | [ .[] | select(.status == "open")
          | ({"critical":0,"important":1,"informational":2}[.severity // ""] // 3) as $r
          | select($r <= $th) ] | length' "$dir/findings.json")
      case "$n_hit" in ''|*[!0-9]*) fail "report-failed" "could not evaluate --check gate over $dir/findings.json";; esac
      if (( n_hit > 0 )); then
        echo "check: $n_hit open finding(s) at-or-above $check_sev — gate fails" >&2
        gate_fail="true"
      else
        echo "check: no open findings at-or-above $check_sev — gate passes" >&2
      fi
    fi
    if [[ "$completeness" == "true" ]]; then
      if [[ "$comp_reviewed" == "true" ]]; then
        echo "check: HEAD ${head_sha:0:7} reviewed (post matches fetch) — completeness passes" >&2
      else
        gate_fail="true"
        if [[ ! -f "$dir/post.json" ]]; then
          echo "check: incomplete — no completed post for HEAD ${head_sha:0:7} (missing-post: run $comp_refetch, then review + post, then re-check)" >&2
        elif [[ -z "$comp_posted" ]]; then
          echo "check: incomplete — post record carries no head SHA (corrupt-post: re-run $comp_refetch, then review + post, then re-check)" >&2
        else
          echo "check: incomplete — HEAD ${head_sha:0:7} != posted ${comp_posted:0:7} (stale-head: re-run $comp_refetch, then review + post, then re-check)" >&2
        fi
      fi
    fi
    # Live-thread verdict (second half of the third dimension): the probe
    # above already warned and stayed neutral on skips/unreachable; here the
    # fail/pass just prints its check: line. fail accumulates into the single
    # exit 1 below, keeping the tier → completeness → live order.
    if [[ "$live_status" == "fail" ]]; then
      echo "check: $live_open live open thread(s) vs $n_all_open local open finding(s) — gate fails (fetch + triage them, then re-check)" >&2
      gate_fail="true"
    elif [[ "$live_status" == "pass" ]]; then
      echo "check: $live_open live open thread(s) vs $n_all_open local open finding(s) — live recheck passes" >&2
    fi
    if [[ "$gate_fail" == "true" ]]; then exit 1; fi
  fi
  exit 0
fi

# ------------------------------------------------------------------- next ---
# Top-ranked actionable finding + the command to act on it. Pure read (via report).
if [[ "$cmd" == "next" ]]; then
  status_f="open"
  while [[ $# -gt 0 ]]; do case "$1" in
    --status) status_f="${2:-open}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for next (want --status, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  now=$(now_utc)
  rep_out=$(bash "${BASH_SOURCE[0]:-$0}" report "$repo" "$pr" --status "$status_f" --json 2>/dev/null) || true
  if [[ -z "$rep_out" ]] || jq -e '.error != null' <<<"$rep_out" >/dev/null 2>&1; then
    if [[ "$json_mode" == "true" ]]; then
      if [[ -n "$rep_out" ]]; then echo "$rep_out" >&9; else fail "report-failed" "next: report produced no output"; fi
    else echo "next: cannot read store (run post or record first)" >&2; fi
    exit 1
  fi
  top=$(jq -c '.data.items[0] // null' <<<"$rep_out")
  if [[ "$top" == "null" ]]; then
    echo "next: none ${status_f} — nothing to act on"
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"none — nothing to act on",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{item:null},error:null}' >&9
    fi
    exit 0
  fi
  loc=$(jq -r '.locator // "?"' <<<"$top")
  echo "next: $(jq -r '.sig[0:8]' <<<"$top") [$(jq -r '.status' <<<"$top")] $loc — address via address-reviews mode, then post"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --argjson item "$top" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"address via address-reviews mode, then post",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{item:$item},error:null}' >&9
  fi
  exit 0
fi

# ------------------------------------------------------------------- show ---
# One full finding record. Pure read.
if [[ "$cmd" == "show" ]]; then
  ssig=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --sig) ssig="${2:-}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for show (want --sig, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  resolve_bundle_dir "$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post or record first)"
  [[ -z "$ssig" ]] && fail "missing-sig" "show needs --sig <sig> (short prefix ok)"
  now=$(now_utc)
  sfull=$(resolve_sig "$(cat "$dir/findings.json")" "$ssig")
  case "$sfull" in
    NONE) fail "unknown-sig" "show: sig not in store";;
    MANY) fail "ambiguous-sig" "show: sig prefix ambiguous";;
  esac
  rec=$(jq --arg s "$sfull" '[.[] | select(.sig == $s)][0]' "$dir/findings.json")
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --argjson item "$rec" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"address via address-reviews mode, then post",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{item:$item},error:null}' >&9
  else
    jq '.' <<<"$rec"
  fi
  exit 0
fi

# ------------------------------------------------------------- check-tree ---
# Machine clean-tree gate for address/fix-ci/build prompts. No network.
# Refuses on unexpected dirt (lists files); --expect takes comma-separated path prefixes.
# Also the authoring-time half of override validation (P5): a stale fragment fails
# closed here with the broken invariant named, before any post/report render.
if [[ "$cmd" == "check-tree" ]]; then
  expect=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --expect) expect="${2:-}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for check-tree (want --expect <prefix,...>, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  validate_overrides
  porcelain=$(git status --porcelain 2>/dev/null) || {
    echo "check-tree: not a git repo" >&2
    if [[ "$json_mode" == "true" ]]; then
      now=$(now_utc)
      jq -n --arg v "$cmd" --arg t "$now" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:"run inside a git repository",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{clean:false,files:[]},
          error:{code:"not-a-repo",message:"check-tree needs a git repository (git status failed)"}}' >&9
    fi
    exit 1
  }
  # The skill's own .review/ scratch never trips the gate (fetch writes it by design).
  own=$(grep -c '^.. \.review/' <<<"$porcelain" || true)
  case "$own" in ''|*[!0-9]*) own="0";; esac
  porcelain=$(grep -v '^.. \.review/' <<<"$porcelain" || true)
  scratch=""; (( own > 0 )) && scratch=" ($own .review/ scratch line(s) ignored)" || true
  if [[ -z "$porcelain" ]]; then
    msg="check-tree: clean$scratch"
    echo "$msg"
    if [[ "$json_mode" == "true" ]]; then
      now=$(now_utc)
      jq -n --arg v "$cmd" --arg t "$now" --arg m "$msg" --argjson ign "$own" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:$m,
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{clean:true,ignored_review_lines:$ign},error:null}' >&9
    fi
    exit 0
  fi
  if [[ -z "$expect" ]]; then
    msg="check-tree: dirty worktree — commit or stash first:"
    echo "$msg"; echo "$porcelain"
    if [[ "$json_mode" == "true" ]]; then
      now=$(now_utc)
      files_json=$(printf '%s\n' "$porcelain" | jq -R -s 'split("\n") | map(select(length > 0))' 2>/dev/null || echo "[]")
      jq -n --arg v "$cmd" --arg t "$now" --arg m "$msg" --argjson f "$files_json" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:$m,
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{clean:false,files:$f},error:null}' >&9
    fi
    exit 1
  fi
  stray=""
  while IFS= read -r line; do
    f="${line:3}"
    [[ "$f" == *" -> "* ]] && f="${f##* -> }"   # renames match on the new path
    ok=""
    IFS=',' read -ra prefs <<<"$expect"
    for p in "${prefs[@]}"; do p="${p#"${p%%[![:space:]]*}"}"; p="${p%"${p##*[![:space:]]}"}"; [[ "$f" == "$p"* ]] && ok="1"; done
    if [[ -z "$ok" ]]; then stray="$stray$line
"; fi
  done <<<"$porcelain"
  if [[ -n "$stray" ]]; then
    msg="check-tree: unexpected dirt:"
    echo "$msg"; printf '%s' "$stray"
    if [[ "$json_mode" == "true" ]]; then
      now=$(now_utc)
      stray_json=$(printf '%s\n' "$stray" | jq -R -s 'split("\n") | map(select(length > 0))' 2>/dev/null || echo "[]")
      jq -n --arg v "$cmd" --arg t "$now" --arg m "$msg" --argjson s "$stray_json" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:$m,
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{clean:false,unexpected:$s},error:null}' >&9
    fi
    exit 1
  fi
  msg="check-tree: only expected files dirty"
  echo "$msg"
  if [[ "$json_mode" == "true" ]]; then
    now=$(now_utc)
    jq -n --arg v "$cmd" --arg t "$now" --arg m "$msg" \
      '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:$m,
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{clean:true,expected_only:true},error:null}' >&9
  fi
  exit 0
fi

# ---- verify allowlist gate (verify-harden slice 01; spec D2/D3) ----
# Pre-exec text scan over the raw verifyCmd, refusing four classes with typed
# errors before the `bash -c` spawn in the verify verb below. The gate binds
# the `verify` verb only (NOT `record` — D1: damage happens at execution, so
# the check lives at execution), and it binds the command surface, not the
# operator: the human shell is unaffected (documented bypass honesty; no
# --force flag in v1, D5).
#
# Matching is case-insensitive on whitespace-collapsed text and fail-closed:
# doubt refuses; the refusal quotes the matched fragment so narrowing is
# obvious. Deliberate carve-outs, all documented here: /tmp writes stay
# allowed (test scratch) — only fetched-payload execution via /tmp (curl -o
# … && chmod +x) refuses through the networked rule; /dev/null stays allowed
# (the discard sink) while every other /dev|/proc|/sys access refuses; plain
# `curl|wget <url>` GET stays allowed (out of scope in v1 — the gate cannot
# tell a local-fixture GET from any other GET; the recipe says to prefer no
# network in verifyCmd). Recursive chmod/chown refuses regardless of target
# (the story says "outside repo", but target parsing is unreliable — fail
# closed) and `git rm -r` counts as rm for the same reason. Static text
# analysis only: variable indirection (X=POST curl -X $X) is out of scope.
verify_gate_check() { # $1 = raw verifyCmd; fail()s typed on match, returns 0 when clean
  local raw flat lc scan bare barepaths frag seg cands c lroot bt
  raw="$1"; bt='`'
  flat=$(printf '%s' "$raw" | tr '\n\t\r' ' ' | tr -s ' ' | sed 's/^ //; s/ $//')
  lc=$(printf '%s' "$flat" | tr '[:upper:]' '[:lower:]')
  # @@null@@ keeps the discard sink out of the /dev rule; =op keeps the bash
  # [[ =~ ]] regex operator out of the tilde rule.
  scan=$(printf '%s' "$lc" | sed -e 's|/dev/null|@@null@@|g' -e 's/=~[[:space:]][[:space:]]*/=op /g')
  # Quotes/backticks become spaces so quoted commands stay visible without
  # quote-aware boundaries below.
  bare=$(printf '%s' "$scan" | sed "s/[\`\"']/ /g" | tr -s ' ')
  # --- destructive (verify-destructive) ---
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])rm[[:space:]]+(-[a-z]*r[a-z]*|--recursive)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (rm with a recursive flag destroys without recovery)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])find[[:space:]]+[^;|&]*-delete([^a-z]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (find -delete destroys without recovery — treat as rm)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])find[[:space:]]+[^;|&]*-exec(dir)?[[:space:]]+rm([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (find -exec rm destroys without recovery — treat as rm)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])mkfs[a-z0-9._-]*' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (mkfs destroys filesystems)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])dd[[:space:]]+[^;|&]*[[:space:]]of=/dev/' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (dd writing a device destroys without recovery)"; fi
  frag=$(grep -E -m1 -o -e ':[[:space:]]*\(\)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (fork-bomb shape)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])ch(mod|own)[[:space:]]+(-[a-z]*r[a-z]*|--recursive)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (recursive chmod/chown refuses regardless of target — fail closed)"; fi
  seg=$(grep -E -m1 -o -e 'git[[:space:]]+clean[^;|&]*' <<<"$bare" || true)
  if [[ -n "$seg" ]]; then
    if [[ "$seg" == *"-f"* ]] && grep -E -q -e '~|\$home| /' <<<"$seg"; then
      fail "verify-destructive" "refusing verifyCmd: matched '$seg' (git clean -f with a / or ~ pathspec destroys outside the repo)"
    fi
  fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])(sudo|doas|runuser)([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (privilege change refuses regardless of payload)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])su([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (privilege change refuses regardless of payload)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])eval([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (eval with non-literal args refuses — fail closed)"; fi
  frag=$(grep -E -m1 -o -e 'exec[[:space:]]*\(' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-destructive" "refusing verifyCmd: matched '$frag' (exec( with non-literal args refuses — fail closed)"; fi
  # --- credential reads (verify-credential-read; before networked so env-exfil
  # shapes land here, where the story lists them) ---
  frag=$(grep -E -m1 -o -e '/\.(ssh|aws|gnupg|pki)|\.config/gh|/\.netrc|\.docker/config\.json' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-credential-read" "refusing verifyCmd: matched '$frag' (credential-path read)"; fi
  if grep -E -q -e '(^|[[:space:];|&(){}"=,])(env|printenv)([[:space:];|&]|$)' <<<"$bare"; then
    frag=$(grep -E -m1 -o -e '\|[[:space:]]*(curl|wget|ncat|netcat|socat|ssh|nc)([[:space:];|&]|$)' <<<"$bare" || true)
    if [[ -n "$frag" ]]; then fail "verify-credential-read" "refusing verifyCmd: matched '$frag' (env piped into the network exfils credentials)"; fi
  fi
  # --- networked (verify-networked) ---
  if grep -E -q -e '(^|[[:space:];|&(){}"=,])(curl|wget|ncat|netcat|socat|ssh|scp)([[:space:];|&]|$)' <<<"$bare"; then
    frag=$(grep -E -m1 -o -e '\|[[:space:]]*(sudo[[:space:]]+)?([^[:space:];|&]*/)?(sh|bash|zsh|fish|dash)([[:space:];|&]|$)' <<<"$bare" || true)
    if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (pipe-to-shell executes a network fetch)"; fi
  fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])curl[[:space:]]+[^;|&]*(-X[[:space:]]*(post|put)|--data|-d([^a-z0-9]|$)|--output|-o[[:space:]]|-O([[:space:];|&]|$))' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (curl exfil/download-to-file shape — fetch fixtures first, run offline)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])wget[[:space:]]+[^;|&]*(-O([^a-z0-9-]|$)|--output-document|--post-data|--post-file)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (wget download/post shape — fetch fixtures first, run offline)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])(nc|ncat|netcat|socat)([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (netcat connect/listen shape)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,])(ssh|scp|sftp)([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (ssh outbound shape)"; fi
  frag=$(grep -E -m1 -o -e '\$[(](curl|wget|ncat|netcat|socat|ssh|nc)([[:space:];|&]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (command substitution over a network fetch)"; fi
  # Backtick form runs against $scan (bare already strips backticks away).
  frag=$(grep -E -m1 -o -e "${bt}(curl|wget|ncat|netcat|socat|ssh|nc)([[:space:];|&]|$)" <<<"$scan" || true)
  if [[ -n "$frag" ]]; then fail "verify-networked" "refusing verifyCmd: matched '$frag' (command substitution over a network fetch)"; fi
  # --- repo escape (verify-repo-escape) ---
  frag=$(grep -E -m1 -o -e '/(dev|proc|sys)([^a-z0-9_]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-repo-escape" "refusing verifyCmd: matched '$frag' (/dev|/proc|/sys access escapes the repo)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,/])\.\.([[:space:];|&(){}"=,/]|$)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-repo-escape" "refusing verifyCmd: matched '$frag' (.. traversal escapes the repo)"; fi
  frag=$(grep -E -m1 -o -e '(^|[[:space:];|&(){}"=,:/])~([[:space:];|&(){}"=,:/]|$|[a-z0-9_.-]+/)' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-repo-escape" "refusing verifyCmd: matched '$frag' (home-directory target escapes the repo)"; fi
  frag=$(grep -E -m1 -o -e '\$home([^a-z0-9_]|$)|\$\{home\}' <<<"$bare" || true)
  if [[ -n "$frag" ]]; then fail "verify-repo-escape" "refusing verifyCmd: matched '$frag' (home-directory target escapes the repo)"; fi
  # Absolute paths outside the repo (and outside /tmp scratch) refuse. Known
  # path-list plumbing (PATH=… and friends) is stripped first — it names
  # lookup dirs, it does not access them.
  barepaths=$(printf ' %s' "$bare" | sed -E 's/ (path|pythonpath|node_path|ld_library_path)=[^[:space:];|&]*/ varpath/g')
  cands=$(grep -E -o -e "(^|[[:space:];|&(){}=,<>])/[^[:space:];|&(){}]*" <<<"$barepaths" || true)
  if [[ -n "$cands" ]]; then
    lroot=$(pwd); lroot=$(printf '%s' "$lroot" | tr '[:upper:]' '[:lower:]')
    while IFS= read -r c; do
      [[ -z "$c" ]] && continue
      case "$c" in (/*) ;; (*) c="${c#?}";; esac
      case "$c" in ("/tmp"|"/tmp/"*) continue;; esac
      if [[ -n "$lroot" && "$lroot" != "/" ]]; then
        case "$c" in ("$lroot"|"$lroot"/*) continue;; esac
      fi
      fail "verify-repo-escape" "refusing verifyCmd: matched '$c' (absolute path escapes the repo — use repo-relative paths, /tmp for scratch)"
    done <<<"$cands"
  fi
  return 0
}

# ----------------------------------------------------------------- verify ---
# Execute patch.json's verifyCmd under a watchdog and record the outcome into the
# patch record. The command comes from the agent; the exit code becomes machine fact.
# No GitHub I/O. Timeouts kill the command, never hang the run.
if [[ "$cmd" == "verify" ]]; then
  timeout_s="300"
  while [[ $# -gt 0 ]]; do case "$1" in
    --timeout) timeout_s="${2:-300}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for verify (want --timeout <secs>, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  resolve_bundle_dir "$pr"
  [[ -f "$dir/patch.json" ]] || fail "missing-patch" "missing $dir/patch.json (nothing claimed to verify)"
  now=$(now_utc)
  vcmd=$(jq -r '.verifyCmd // empty' "$dir/patch.json")
  [[ -z "$vcmd" ]] && fail "no-verify-cmd" "patch.json carries no verifyCmd to execute"
  if ! [[ "$timeout_s" =~ ^[0-9]+$ ]] || (( timeout_s <= 0 )); then fail "bad-timeout" "timeout must be a positive number of seconds, got '$timeout_s'"; fi
  # Allowlist gate (D1/D5): refuse destructive/networked/credential/escape
  # shapes BEFORE any write (.verify.out) or mutation (patch.json). Refusal
  # exits here via fail(); clean commands fall through to the spawn below.
  verify_gate_check "$vcmd"
  echo "» verify: $vcmd (timeout ${timeout_s}s)"
  : > "$dir/.verify.out"; : > "$dir/.verify.timeout"
  # Own process group so the timeout kill stops the whole tree: a backgrounded
  # child would otherwise survive and its exit 0 get recorded as proof.
  # Monitor mode assigns the background job its own pgid on Linux and macOS
  # (no setsid(1) on macOS); restored immediately after spawn.
  set -m
  bash -c "$vcmd" >"$dir/.verify.out" 2>&1 & vpid=$!
  set +m
  # Polling watchdog (portable, orphan-free): no background sleeper exists to outlive
  # us holding pipes open. Needs ps(1) to spot zombies (finished-but-unreaped reads
  # alive under kill -0); without ps the timeout cannot be enforced — say so loudly.
  timed_out="false"
  if command -v ps >/dev/null 2>&1; then
    elapsed=0
    while true; do
      alive="1"
      kill -0 "$vpid" 2>/dev/null || alive=""
      if [[ -n "$alive" ]] && ps -p "$vpid" -o stat= 2>/dev/null | grep -q 'Z'; then alive=""; fi
      if [[ -z "$alive" ]] || (( elapsed >= timeout_s )); then break; fi
      sleep 1; elapsed=$(( elapsed + 1 ))
    done
    if [[ -n "$alive" ]]; then
      kill -9 -- "-$vpid" 2>/dev/null || true
      timed_out="true"; echo "timeout after ${timeout_s}s" > "$dir/.verify.timeout"
    fi
  else
    warn "no ps(1) — timeout unenforced for this run"
  fi
  vcode=0; wait "$vpid" || vcode=$?
  # Post-reap sweep: a parent that exits fast can leave the tree running
  # (decoy exit), which the poll loop never sees. If the group outlives the
  # parent, stop it and refuse the proof — an unverified tree is not a verified
  # one. Grace period lets legitimate stragglers (log flushers) finish instead
  # of tripping the mark. Deliberate setsid(1) escapers leave the group and stay
  # out of reach; containment beyond this is documented as out of scope.
  if kill -0 -- "-$vpid" 2>/dev/null; then
    sleep 2
    if kill -0 -- "-$vpid" 2>/dev/null; then
      kill -9 -- "-$vpid" 2>/dev/null || true
      timed_out="true"
      echo "orphaned command tree killed after exit" > "$dir/.verify.timeout"
    fi
  fi
  tail4k=$(tail -c 4000 "$dir/.verify.out")
  # Redact known secret values from the recorded tail (hygiene, not a boundary:
  # the command itself runs with full env). Names are matched, values replaced —
  # names never appear in output. Empty and short (<8 char) values skipped: an
  # empty pattern would match everywhere, short ones collide with prose. The value
  # is quoted, so it matches literally — glob characters in secrets are inert.
  # The full .verify.out keeps raw output for operator debugging; only the
  # recorded tail is filtered. compgen enumerates names (multiline-safe).
  # Suffixes already cover GH_TOKEN-style names; only non-matching stragglers
  # are listed exactly. Names match case-insensitively with trailing rotation
  # digits stripped, so lower/numbered variants are covered too.
  shopt -s nocasematch
  while IFS= read -r _sv; do
    _norm="$_sv"
    while [[ "$_norm" == *[0-9] || "$_norm" == *_ ]]; do _norm=${_norm%?}; done
    case "$_norm" in
      *_TOKEN|*_SECRET|*_KEY|*_PASSWORD|*_PASSPHRASE|*_PAT|*_AUTH|*_CREDENTIAL|*_CREDENTIALS|*_PASS|*_PWD|TOKEN|SECRET|KEY|PASSWORD|AWS_ACCESS_KEY_ID|SSH_AUTH_SOCK|SSH_AGENT_PID)
        _vv="${!_sv}"
        if [[ -n "$_vv" && "${#_vv}" -ge 8 ]]; then tail4k=${tail4k//"$_vv"/'***REDACTED***'}; fi;;
    esac
  done < <(compgen -A variable)
  shopt -u nocasematch
  unset _sv _vv _norm
  echo "» exit=$vcode timed_out=$timed_out"
  jq --argjson exit "$vcode" --argjson to "$timed_out" --arg now "$now" --arg tail "$tail4k" \
    '. + {verifyExit:$exit, verifiedAt:$now, timedOut:$to, verifiedTail:$tail}' \
    "$dir/patch.json" > "$dir/patch.json.tmp" && mv "$dir/patch.json.tmp" "$dir/patch.json"
  echo "recorded into $dir/patch.json"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --argjson e "$vcode" \
      --argjson to "$timed_out" --arg tail "$tail4k" --arg d "$dir" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $e == 0 and $to == false then "proof holds — revalidator may VERIFY" else "proof failed — fix, do not claim" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{exit:$e,timedOut:$to,outputTail:$tail,patch:$d},error:null}' >&9
  fi
  exit 0
fi

# ----------------------------------------------------------------- doctor ---
# Preflight the harness. Hard checks fail the run; warn-severity misses print
# WARN, land in warnings, and never fail the run. Storage covers the dirs the
# tool writes (.review/ bundles). No network beyond the two pre-existing gh
# reads (scopes header, rate limit); never phones home. No repo/PR args.
if [[ "$cmd" == "doctor" ]]; then
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for doctor (want --json)" >&2; exit 2;;
  esac; done
  json_redirect
  now=$(now_utc)
  results="[]"
  add() { # $1 = name, $2 = fail|warn, $3 = true|false, $4 = detail
    results=$(jq --arg n "$1" --arg s "$2" --argjson ok "$3" --arg d "$4" '. + [{name:$n, severity:$s, ok:$ok, detail:$d}]' <<<"$results")
    if [[ "$3" == "true" ]]; then echo "ok   $1 — $4";
    elif [[ "$2" == "warn" ]]; then
      echo "WARN $1 — $4"
      WARNINGS_JSON=$(jq --arg w "$1: $4" '. + [$w]' <<<"$WARNINGS_JSON")
    else echo "FAIL $1 — $4"; fi
  }
  if command -v gh >/dev/null 2>&1; then add "gh-cli" "fail" "true" "$(gh --version 2>/dev/null | head -n 1)"; else add "gh-cli" "fail" "false" "missing — install github.com/cli/cli"; fi
  if gh auth status >/dev/null 2>&1; then add "gh-auth" "fail" "true" "authenticated"; else add "gh-auth" "fail" "false" "run gh auth login"; fi
  scopes=$(gh api user --include 2>/dev/null | grep -i '^x-oauth-scopes:' | head -n 1 || true)
  if [[ "$scopes" =~ repo ]]; then add "token-scopes" "warn" "true" "$scopes";
  elif [[ -z "$scopes" ]]; then add "token-scopes" "warn" "true" "no scopes header — fine-grained token, scopes proven at post time";
  else add "token-scopes" "warn" "false" "needs repo (private) or public_repo scope — ${scopes:-unreadable}"; fi
  # Token-scope excess audit (verify-harden slice 02; spec D4): compare the
  # granted scopes from the single header read above against what runs need
  # — no new network calls, this parses $scopes only. Baseline is the repo
  # family plus read:* (public-only reads accept public_repo); anything
  # beyond it (delete_repo, admin:*, workflow, gist, …) gets a WARN-only
  # token-scopes-excess row naming the excess scopes with a least-privilege
  # try: hint. Warn-only by construction: ok false at warn severity lands in
  # warnings without touching healthy, so the exit stays 0. Minimal grants
  # emit no row; the insufficient-scope and fine-grained rows above stay as-is.
  if [[ -n "$scopes" ]]; then
    granted=$(printf '%s' "$scopes" | sed -e 's/^[Xx]-[Oo][Aa][Uu][Tt][Hh]-[Ss][Cc][Oo][Pp][Ee][Ss]:[[:space:]]*//' | tr ',' '\n' | tr -d ' \t\r' | tr '[:upper:]' '[:lower:]')
    excess=""
    while IFS= read -r scope; do
      case "$scope" in ""|repo|public_repo|read:*) continue;; esac
      if [[ -z "$excess" ]]; then excess="$scope"; else excess="$excess, $scope"; fi
    done <<<"$granted"
    if [[ -n "$excess" ]]; then
      add "token-scopes-excess" "warn" "false" "token grants more than runs need: $excess — try: re-mint with only the repo scope (least privilege) and re-run"
    fi
  fi
  if command -v jq >/dev/null 2>&1; then add "jq" "fail" "true" "$(jq --version 2>/dev/null)"; else add "jq" "fail" "false" "missing — install jq"; fi
  if command -v shasum >/dev/null 2>&1; then add "shasum" "fail" "true" "sig hashing available"; else add "shasum" "fail" "false" "sig hashing hard-requires shasum (startup gate)"; fi
  if command -v ps >/dev/null 2>&1; then add "ps" "warn" "true" "verify timeout enforced"; else add "ps" "warn" "false" "verify runs without timeout enforcement"; fi
  if command -v git >/dev/null 2>&1; then add "git" "warn" "true" "check-tree/verify available"; else add "git" "warn" "false" "check-tree and verify degraded without git"; fi
  remaining=$(gh api rate_limit --jq '.resources.core.remaining' 2>/dev/null || true)
  if [[ -n "$remaining" ]]; then add "api-reachable" "warn" "true" "core quota remaining: $remaining"; else add "api-reachable" "warn" "false" "cannot reach api.github.com with this token"; fi
  # Storage the tool writes is .review/ bundles (fetch/record/post receipts).
  # Permission metadata only — no probe files, no writes, no network.
  if [[ -d .review ]]; then
    if [[ -w .review ]]; then add "storage-writable" "fail" "true" ".review/ writable";
    else add "storage-writable" "fail" "false" ".review/ not writable — fetch/record cannot write bundles here"; fi
  elif [[ -w . ]]; then add "storage-writable" "fail" "true" "cwd writable — .review/ created on first fetch";
  else add "storage-writable" "fail" "false" "cwd not writable — fetch/record cannot create .review/ here"; fi
  healthy=$(jq -e 'map(select(.severity == "fail" and .ok == false)) | length == 0' <<<"$results" >/dev/null 2>&1 && echo "true" || echo "false")
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg t "$now" --argjson h "$healthy" --argjson c "$results" \
      '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,
        next:(if $h then "harness healthy" else "fix FAIL lines above (WARN lines are advisory), then re-run" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{healthy:$h,checks:$c},error:null}' >&9
  fi
  if [[ "$healthy" == "true" ]]; then exit 0; fi
  exit 1
fi

# ------------------------------------------------------------ calibrate ---
# Join findings against live ground truth: thread states + files changed since the
# review head. Read-only (3 reads: threads, PR state, one compare). Reports outcomes
# + tier precision; never auto-tunes. Terminal findings are skipped, not judged.
if [[ "$cmd" == "calibrate" ]]; then
  probe="false"
  judge_file=""
  judge_specs=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --probe) probe="true"; shift;;
    --judge)
      [[ $# -ge 2 && -n "${2:-}" ]] || { echo "missing value for --judge (want SIG:VERDICT[:HELPFUL], verdict real|false|unknown, helpful true|false|null)" >&2; exit 2; }
      judge_specs+="$2"$'\n'; shift 2;;
    --judge=*)
      [[ -n "${1#--judge=}" ]] || { echo "empty value for --judge (want SIG:VERDICT[:HELPFUL])" >&2; exit 2; }
      judge_specs+="${1#--judge=}"$'\n'; shift;;
    --judge-file)
      [[ $# -ge 2 && -n "${2:-}" ]] || { echo "missing value for --judge-file (want PATH to a JSON/JSONL judgments fragment)" >&2; exit 2; }
      [[ -z "$judge_file" ]] || { echo "duplicate --judge-file (pass one fragment)" >&2; exit 2; }
      judge_file="$2"; shift 2;;
    --judge-file=*)
      [[ -n "${1#--judge-file=}" ]] || { echo "empty value for --judge-file (want PATH)" >&2; exit 2; }
      [[ -z "$judge_file" ]] || { echo "duplicate --judge-file (pass one fragment)" >&2; exit 2; }
      judge_file="${1#--judge-file=}"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for calibrate (want --probe, --judge, --judge-file, --json)" >&2; exit 2;;
  esac; done
  if [[ "$probe" != "true" ]]; then
    if [[ -n "$judge_specs" || -n "$judge_file" ]]; then
      echo "--judge and --judge-file need --probe (judgments only merge on a probe run)" >&2; exit 2
    fi
  fi
  json_redirect
  resolve_bundle_dir "$pr"
  if [[ "$probe" == "true" ]]; then
    # Self-grading probe (X3): offline baseline of recorded findings against
    # the recorded triage dispositions, plus an agent judgment loop that CAN
    # diverge from the baseline. Baseline columns (verdict/rule/helpful) are
    # regenerated from findings.json every run — agents never hand-edit them;
    # direct edits to kb-grades.jsonl are overwritten on the next probe run.
    # Agent judgments arrive machine-readably via repeatable
    # --judge SIG:VERDICT[:HELPFUL] or --judge-file PATH (a JSON array or
    # JSONL of {"sig", "agent_verdict", "agent_helpful"} objects); every sig
    # is validated against the findings store and unknown sigs refuse typed.
    # Judgments are sticky: re-runs keep prior agent columns for surviving
    # sigs unless overlapped by new input. Writes only kb-grades.jsonl
    # (deterministic apart from the timestamp, idempotent); findings.json,
    # kb.jsonl, and kb-joins.jsonl stay byte-identical. No network, no post
    # record needed — local bundles and post-less PR bundles grade alike. The
    # M4-consumable number is the agreement rate over doubly-judged rows
    # (baseline known AND agent judged real/false), with the sample size
    # recorded alongside.
    [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post or record first)"
    jq -e 'type == "array"' "$dir/findings.json" >/dev/null 2>&1 \
      || fail "corrupt-store" "findings store $dir/findings.json is unreadable or not a findings array (re-run post/record to rebuild, or restore the file)"
    now=$(now_utc)
    bname=${dir#.review/}
    known_sigs=$(jq -c '[.[].sig]' "$dir/findings.json")
    new_judgments="[]"
    if [[ -n "$judge_file" ]]; then
      [[ -f "$judge_file" && -r "$judge_file" ]] \
        || fail "bad-judgment" "judge file '$judge_file' is missing or unreadable (write one {\"sig\": ..., \"agent_verdict\": ...} object per line, or a JSON array of them)"
      file_arr=$(jq -cs '.' "$judge_file" 2>/dev/null) \
        || fail "bad-judgment" "judge file '$judge_file' is not valid JSON or JSONL (one {\"sig\": ..., \"agent_verdict\": ...} per line, or a JSON array)"
      file_arr=$(jq -c 'if length == 1 and (.[0] | type) == "array" then .[0] else . end' <<<"$file_arr")
      bad_entry=$(jq -r '[to_entries[] | (.value) as $v
        | if ($v | type) != "object" then "entry \(.key): not an object (want {\"sig\", \"agent_verdict\"})"
          elif ($v.sig | type) != "string" or ($v.sig | length) == 0 then "entry \(.key): sig must be a non-empty string"
          elif (["real","false","unknown"] | index($v.agent_verdict) | not) then "entry \(.key) (\($v.sig)): agent_verdict must be real|false|unknown"
          elif (($v.agent_helpful // null) as $h | $h != true and $h != false and $h != null) then "entry \(.key) (\($v.sig)): agent_helpful must be true|false|null"
          else empty end] | .[0] // empty' <<<"$file_arr")
      [[ -z "$bad_entry" ]] \
        || fail "bad-judgment" "judge file '$judge_file' $bad_entry"
      unknown_fsig=$(jq -r --argjson known "$known_sigs" \
        '$known as $k | [.[].sig] | map(select(. as $s | $k | index($s) | not)) | .[0] // empty' <<<"$file_arr")
      [[ -z "$unknown_fsig" ]] \
        || fail "unknown-judgment" "judge file '$judge_file' sig '$unknown_fsig' matches no finding in $dir/findings.json (copy the full sig from report output)"
      new_judgments=$(jq -c --argjson f "$file_arr" \
        '. + [$f[] | {sig: .sig, agent_verdict: .agent_verdict, agent_helpful: (.agent_helpful // null)}]' <<<"$new_judgments")
    fi
    if [[ -n "$judge_specs" ]]; then
      while IFS= read -r jg_spec; do
        [[ -z "$jg_spec" ]] && continue
        jg_sig="${jg_spec%%:*}"; jg_rest="${jg_spec#*:}"
        [[ "$jg_spec" != "$jg_sig" && -n "$jg_sig" ]] \
          || fail "bad-judgment" "malformed --judge '$jg_spec' (want SIG:VERDICT[:HELPFUL], e.g. --judge abc123:real:true)"
        jg_verdict="${jg_rest%%:*}"
        if [[ "$jg_rest" == *:* ]]; then jg_helpful="${jg_rest#*:}"; else jg_helpful="null"; fi
        [[ -z "$jg_helpful" ]] && jg_helpful="null"
        case "$jg_verdict" in real|false|unknown) ;; *) fail "bad-judgment" "malformed --judge '$jg_spec': verdict '$jg_verdict' (want real|false|unknown)";; esac
        case "$jg_helpful" in true|false|null) ;; *) fail "bad-judgment" "malformed --judge '$jg_spec': helpful '$jg_helpful' (want true|false|null)";; esac
        jq -e --arg s "$jg_sig" 'index($s) != null' <<<"$known_sigs" >/dev/null \
          || fail "unknown-judgment" "judgment sig '$jg_sig' matches no finding in $dir/findings.json (copy the full sig from report output)"
        new_judgments=$(jq -c --arg s "$jg_sig" --arg v "$jg_verdict" --argjson h "$jg_helpful" \
          '. + [{sig: $s, agent_verdict: $v, agent_helpful: $h}]' <<<"$new_judgments")
      done <<<"$judge_specs"
    fi
    prior_judgments="[]"
    if [[ -f "$dir/kb-grades.jsonl" ]]; then
      prior_judgments=$(jq -cs '[.[] | select(type == "object" and (.sig | type) == "string") | (.agent_verdict) as $av | select(($av | type) == "string" and (["real","false","unknown"] | index($av))) | {sig: .sig, agent_verdict: $av, agent_helpful: (.agent_helpful // null)}]' "$dir/kb-grades.jsonl" 2>/dev/null || echo "[]")
    fi
    merged_judgments=$(jq -c -n --argjson p "$prior_judgments" --argjson n "$new_judgments" \
      '$p + $n | reduce .[] as $j ({}; .[$j.sig] = $j)')
    grades=$(jq -c --arg b "$bname" --arg t "$now" --argjson m "$merged_judgments" '
      [.[] | ((.kb // []) | map(select(type == "string"))) as $cited
        | (if .status == "addressed" then "real"
           elif .status == "false-positive" then "false"
           else "unknown" end) as $d
        | ($m[.sig] // {}) as $j
        | {at: $t, bundle: $b, sig: .sig,
           verdict: $d,
           rule: ($cited[0] // null),
           helpful: (if ($cited | length) == 0 then null
                     elif .status == "addressed" then true
                     elif .status == "false-positive" then false
                     else null end),
           agent_verdict: ($j.agent_verdict // null),
           agent_helpful: ($j.agent_helpful // null),
           agreement: (if ($j.agent_verdict == "real" or $j.agent_verdict == "false") and ($d == "real" or $d == "false")
                       then ($j.agent_verdict == $d) else null end),
           grader: "calibrate-probe"}]' "$dir/findings.json")
    jq -c '.[]' <<<"$grades" > "$dir/kb-grades.jsonl"
    n_r=$(jq '[.[] | select(.verdict == "real")] | length' <<<"$grades")
    n_f=$(jq '[.[] | select(.verdict == "false")] | length' <<<"$grades")
    n_u=$(jq '[.[] | select(.verdict == "unknown")] | length' <<<"$grades")
    n_g=$(jq 'length' <<<"$grades")
    n_sampled=$(jq '[.[] | select(.agent_verdict == "real" or .agent_verdict == "false")] | length' <<<"$grades")
    n_judged=$(jq '[.[] | select((.agent_verdict == "real" or .agent_verdict == "false") and (.verdict == "real" or .verdict == "false"))] | length' <<<"$grades")
    n_agree=$(jq '[.[] | select(.agreement == true)] | length' <<<"$grades")
    n_dis=$(jq '[.[] | select(.agreement == false)] | length' <<<"$grades")
    agree_rate=$(jq -n --argjson a "$n_agree" --argjson j "$n_judged" 'if $j == 0 then null else ($a / $j) end')
    rate_txt="n/a"; [[ "$n_judged" != "0" ]] && rate_txt="$agree_rate"
    judge_txt=""; [[ "$n_sampled" != "0" ]] && judge_txt=" judgments: sampled=$n_sampled judged=$n_judged agreed=$n_agree disagreed=$n_dis rate=$rate_txt"
    echo "probe: $repo#$bname — $n_g finding(s): real=$n_r false=$n_f unknown=$n_u$judge_txt (grades: $dir/kb-grades.jsonl)"
    jq -r '.[] | "\(.sig[0:8]) [\(.verdict)]\(if .rule == null then " uncited" else " rule " + .rule + " helpful=" + (.helpful | tostring) end)\(if .agent_verdict == null then "" elif .agreement == true then " agent=\(.agent_verdict) agree" elif .agreement == false then " agent=\(.agent_verdict) DISAGREE" else " agent=\(.agent_verdict) (no baseline)" end)"' <<<"$grades"
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg b "$bname" --arg t "$now" --argjson g "$grades" \
        --argjson nr "$n_r" --argjson nf "$n_f" --argjson nu "$n_u" \
        --argjson ns "$n_sampled" --argjson nj "$n_judged" --argjson na "$n_agree" --argjson nd "$n_dis" --argjson rate "$agree_rate" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
          next:(if $nr > 0 then "adopted-pair signal present — feed X3 into the H2 evidence gate" else "no real findings graded — nothing to weigh" end),
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),
          data:{bundle:$b,grades:$g,counts:{findings:($g | length),real:$nr,false:$nf,unknown:$nu},
            judgment:{sampled:$ns,judged:$nj,agreed:$na,disagreed:$nd,agreement_rate:$rate}},error:null}' >&9
    fi
    exit 0
  fi
  [[ "$dir" == .review/local-* ]] && fail "no-threads" "calibrate joins live threads — nothing to settle on a local bundle (adopt + post first, then calibrate the PR)"
  prov=$(jq -r '.provider // "github"' "$dir/PR.json" 2>/dev/null || echo "github")
  if [[ "$prov" != "github" ]]; then fail "provider-unimplemented" "calibrate on $prov is not implemented yet (no live thread join for it)"; fi
  [[ -f "$dir/post.json" ]] || fail "missing-post" "missing $dir/post.json (post for real first — calibration needs a posted review id)"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post or record first)"
  now=$(now_utc)
  review_id=$(jq -r '.review.id // empty' "$dir/post.json")
  head0=$(jq -r '.head // empty' "$dir/post.json")
  [[ -z "$review_id" || -z "$head0" ]] && fail "missing-post" "post.json carries no review id/head"
  owner="${repo%%/*}"; name="${repo##*/}"
  threads_json=$(gh api graphql --paginate -F owner="$owner" -F name="$name" -F pr="$pr" -f query='
    query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
      repository(owner:$owner, name:$name) {
        pullRequest(number:$pr) {
          reviewThreads(first:100, after:$endCursor) {
            nodes { isResolved comments(first:30) { nodes { body path } } }
          }
        }
      }
    }' 2>/dev/null | jq -s '[.[].data.repository.pullRequest.reviewThreads.nodes[]]') \
    || fail "threads-failed" "could not read live review threads"
  live=$(gh pr view "$pr" --repo "$repo" --json headRefOid,state,mergedAt 2>/dev/null) \
    || fail "pr-read-failed" "could not read live PR state"
  live_head=$(jq -r '.headRefOid' <<<"$live"); pr_state=$(jq -r '.state' <<<"$live")
  merged_at=$(jq -r '.mergedAt // empty' <<<"$live")
  changed="[]"
  if [[ "$head0" != "$live_head" ]]; then
    changed=$(gh api "repos/$repo/compare/$head0...$live_head" --jq '[.files[].filename]' 2>/dev/null || echo "COMPARE_FAIL")
    if [[ "$changed" == "COMPARE_FAIL" ]]; then
      warn "compare unavailable (rewritten history?) — changed-file evidence off, resolved reads as resolved-unfixed"
      changed="[]"
    fi
  fi
  outcomes=$(jq --slurpfile th <(printf '%s' "$threads_json") --argjson changed "$changed" '
    ($th[0] | map({resolved:.isResolved, comments:[.comments.nodes[] | {body, path}]})) as $threads
    | [.[] | select(.status == "open" or .status == "addressed" or .status == "deferred") | . as $f
      | (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end) as $loc
      | (if $f.kind == "inline"
         then ($f.body + (if $f.suggestion then "\n\n```suggestion\n" + $f.suggestion + "\n```" else "" end)) as $pb
              | ([$threads[] | select(any(.comments[]; .body == $pb and .path == $f.path))][0]) as $hit
              | if $hit == null then {outcome:"unknown", detail:"comment not found live"}
                elif ($hit.resolved | not) then {outcome:"open", detail:"thread still open"}
                elif ([$changed[] | select(. == $f.path)] | length) > 0
                then {outcome:"fixed", detail:"thread resolved + cited file changed since review head"}
                else {outcome:"resolved-unfixed", detail:"thread resolved but cited file unchanged since review head"} end
         else {outcome:"unknown", detail:"body section — not a thread comment"} end) as $o
      | {sig:$f.sig, status:$f.status, severity:($f.severity // "untagged"), locator:$loc,
         outcome:$o.outcome, detail:$o.detail}
        + (if (($f.kb // []) | length) > 0 then {kb: $f.kb} else {} end)]' "$dir/findings.json")
  precision=$(jq '{fixed: ([.[] | select(.outcome == "fixed")] | length),
      resolved_unfixed: ([.[] | select(.outcome == "resolved-unfixed")] | length),
      open: ([.[] | select(.outcome == "open")] | length),
      unknown: ([.[] | select(.outcome == "unknown")] | length),
      judged: ([.[] | select(.outcome != "unknown")] | length),
      by_tier: ([group_by(.severity)[] | {(.[0].severity):
        {fixed: ([.[] | select(.outcome == "fixed")] | length),
         resolved_unfixed: ([.[] | select(.outcome == "resolved-unfixed")] | length),
         open: ([.[] | select(.outcome == "open")] | length),
         unknown: ([.[] | select(.outcome == "unknown")] | length)}}] | add)}' <<<"$outcomes")
  n_o=$(jq '.open' <<<"$precision"); n_u=$(jq '.unknown' <<<"$precision")
  stale="current"; [[ "$head0" != "$live_head" ]] && stale="stale — ${head0:0:7} → ${live_head:0:7}"
  echo "calibrate: $repo#$pr — review $review_id @ ${head0:0:7} ($stale), PR $pr_state${merged_at:+, merged $merged_at}"
  jq -r '.[] | "\(.sig[0:8]) [\(.outcome)] \(.locator) — \(.detail)\(if (.kb // []) | length > 0 then " kb:" + ((.kb // []) | join(",")) else "" end)"' <<<"$outcomes"
  echo "precision: fixed=$(jq -r '.fixed' <<<"$precision") resolved-unfixed=$(jq -r '.resolved_unfixed' <<<"$precision") open=$n_o unknown=$n_u (judged $(jq -r '.judged' <<<"$precision"))"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg rid "$review_id" \
      --arg h0 "$head0" --arg lh "$live_head" --arg st "$pr_state" --arg ma "$merged_at" \
      --argjson oc "$outcomes" --argjson pc "$precision" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $pc.open > 0 then "address open threads" elif $pc.unknown > 0 then "re-run after threads settle" else "calibration recorded" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{review_id:($rid | tonumber? // $rid),head:{reviewed:$h0,live:$lh},
        pr:{state:$st,merged_at:(if $ma == "" then null else $ma end)},
        outcomes:$oc,precision:$pc},error:null}' >&9
  fi
  exit 0
fi

# ----------------------------------------------------------------- ready ---
# Readiness advisory: is this PR worth a review right now? Read-only (one live
# PR read, one checks read, one thread read). Default is advisory — exit 0
# always, advice never a gate. --check opts into gating for hooks/CI: same
# output, exit 0 ready, 1 blockers-or-wait, 2 usage, 3 tool error.
# PR bundles only; local bundles have no live checks to advise on.
if [[ "$cmd" == "ready" ]]; then
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    --check) check_gate="true"; shift;;
    *) echo "unknown flag '$1' for ready (want --json, --check)" >&2; exit 2;;
  esac; done
  json_redirect
  resolve_bundle_dir "$pr"
  [[ "$dir" == .review/local-* ]] && fail "no-pr-target" "ready needs a PR bundle (fetch owner/repo N first); local bundles have no live checks to advise on"
  prov=$(jq -r '.provider // "github"' "$dir/PR.json" 2>/dev/null || echo "github")
  if [[ "$prov" != "github" ]]; then fail "provider-unimplemented" "ready on $prov is not implemented yet (no live gh reads for it)"; fi
  [[ -f "$dir/PR.json" ]] || fail "missing-bundle" "missing $dir/PR.json (run fetch $repo $pr first)"
  now=$(now_utc)
  # Staleness context from the bundle: advice reads live, the bundle only says
  # whether fetch is behind.
  fetched_sha=$(pr_field "$dir" head_sha)
  live=$(gh pr view "$pr" --repo "$repo" --json isDraft,mergeable,mergeStateStatus,body,state,headRefOid 2>"$dir/.ready.err") \
    || fail "pr-read-failed" "could not read live PR state for $repo#$pr: $(head -c 200 "$dir/.ready.err" | tr '\n' ' ')"
  live_head=$(jq -r '.headRefOid // empty' <<<"$live")
  if [[ -n "$fetched_sha" && -n "$live_head" && "$fetched_sha" != "$live_head" ]]; then
    warn "bundle stale (fetched ${fetched_sha:0:7}, live ${live_head:0:7}) — re-fetch before review, advice reads live"
  fi
  # CI state (live). Unreadable checks are unknown, never blocking — post's
  # preflight stays the fail-closed backstop, not this advisory.
  checks=$(gh pr checks "$pr" --repo "$repo" --json bucket,name,state 2>/dev/null || echo "[]")
  ci_total=$(jq '[.[]] | length' <<<"$checks" 2>/dev/null || echo 0)
  ci_fail=$(jq '[.[] | select(.bucket == "fail")] | length' <<<"$checks" 2>/dev/null || echo 0)
  ci_fail_names=$(jq -r '[.[] | select(.bucket == "fail") | .name] | join(", ")' <<<"$checks" 2>/dev/null || true)
  # Open threads (live GraphQL, same read as calibrate): the bundle's
  # reviews.json/inline.json carry no resolution flags, so counting them would
  # mark green PRs unready — resolution state only exists live.
  owner="${repo%%/*}"; name="${repo##*/}"
  threads_json=$(gh api graphql --paginate -F owner="$owner" -F name="$name" -F pr="$pr" -f query='
    query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
      repository(owner:$owner, name:$name) {
        pullRequest(number:$pr) {
          reviewThreads(first:100, after:$endCursor) {
            nodes { isResolved }
          }
        }
      }
    }' 2>/dev/null | jq -s '[.[].data.repository.pullRequest.reviewThreads.nodes[]]') \
    || fail "threads-failed" "could not read live review threads for $repo#$pr"
  threads_open=$(jq '[.[] | select(.isResolved == false)] | length' <<<"$threads_json" 2>/dev/null || echo 0)
  is_draft=$(jq -r '.isDraft // false' <<<"$live")
  mergeable=$(jq -r '.mergeable // "UNKNOWN"' <<<"$live")
  mss=$(jq -r '.mergeStateStatus // "UNKNOWN"' <<<"$live")
  state=$(jq -r '.state // "UNKNOWN"' <<<"$live")
  # Description completeness: strip HTML-comment boilerplate (templates,
  # bot summaries), squeeze whitespace, count what the author actually wrote.
  # The awk removes <!-- --> spans (including multiline) but keeps text outside
  # them — a range-delete would drop whole lines carrying inline comments.
  body_len=$(jq -r '.body // ""' <<<"$live" | awk 'BEGIN{in_c=0} {line=""; rest=$0; while (length(rest)>0) { if (in_c) {e=index(rest,"-->"); if (e) {rest=substr(rest,e+3); in_c=0} else {rest=""}} else {s=index(rest,"<!--"); if (s) {line=line substr(rest,1,s-1); rest=substr(rest,s+4); in_c=1} else {line=line rest; rest=""}}} print line}' | tr -s '[:space:]' ' ' | wc -c | tr -d ' ')
  [[ "$ci_total" =~ ^[0-9]+$ ]] || ci_total=0
  [[ "$ci_fail" =~ ^[0-9]+$ ]] || ci_fail=0
  [[ "$threads_open" =~ ^[0-9]+$ ]] || threads_open=0
  [[ "$body_len" =~ ^[0-9]+$ ]] || body_len=0
  blockers="[]"
  if (( ci_fail > 0 )); then
    blockers=$(jq --arg d "$ci_fail failing check(s): $ci_fail_names" --arg a "fix CI, then re-run ready" \
      '. + [{kind:"ci-failing", detail:$d, action:$a}]' <<<"$blockers")
  fi
  if (( threads_open > 0 )); then
    blockers=$(jq --arg d "$threads_open unresolved thread(s)" --arg a "address or resolve open threads, then re-run ready" \
      '. + [{kind:"threads-open", detail:$d, action:$a}]' <<<"$blockers")
  fi
  if [[ "$is_draft" == "true" ]]; then
    blockers=$(jq '. + [{kind:"draft", detail:"PR is still a draft", action:"mark ready for review when done, then re-run ready"}]' <<<"$blockers")
  fi
  if [[ "$mergeable" == "CONFLICTING" || "$mss" == "DIRTY" ]]; then
    blockers=$(jq --arg d "mergeable=$mergeable mergeStateStatus=$mss" \
      '. + [{kind:"conflict", detail:$d, action:"rebase onto base and resolve conflicts, then re-run ready"}]' <<<"$blockers")
  fi
  if (( body_len < 50 )); then
    blockers=$(jq --arg d "description ~${body_len} chars (<50)" \
      '. + [{kind:"description-thin", detail:$d, action:"expand the description (what/why/testing), then re-run ready"}]' <<<"$blockers")
  fi
  if [[ "$mergeable" == "UNKNOWN" && "$mss" == "UNKNOWN" ]]; then
    warn "mergeability UNKNOWN (GitHub still computing?) — conflict treated as absent, re-run ready if this persists"
  fi
  n_block=$(jq 'length' <<<"$blockers")
  ready="true"; rec="review"
  if [[ "$state" != "OPEN" ]]; then
    ready="false"; rec="wait"
  elif (( n_block > 0 )); then
    ready="false"
    if [[ "$is_draft" == "true" ]]; then rec="wait"; else rec="nudge-author"; fi
  fi
  if [[ "$ready" == "true" ]]; then
    echo "ready: yes — $repo#$pr looks reviewable (CI $ci_total checks, 0 failing; $threads_open open threads; mergeable $mergeable; body ${body_len} chars)"
    echo "recommendation: review — fetch + review"
  else
    if [[ "$state" != "OPEN" ]]; then
      echo "ready: no — $repo#$pr is $state, nothing to review"
    else
      echo "ready: no — $repo#$pr has $n_block blocker(s):"
      jq -r '.[] | "- [\(.kind)] \(.detail) — action: \(.action)"' <<<"$blockers" | sed 's/^/  /'
    fi
    case "$rec" in
      wait) echo "recommendation: wait — hold the review, re-run ready later";;
      *) echo "recommendation: nudge-author — nudge the author instead of reviewing";;
    esac
  fi
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" \
      --argjson ready "$ready" --arg rec "$rec" --argjson blockers "$blockers" \
      --arg st "$state" --argjson draft "$is_draft" --arg m "$mergeable" --arg mss "$mss" \
      --argjson ci_fail "$ci_fail" --argjson ci_total "$ci_total" \
      --argjson threads_open "$threads_open" --argjson body_len "$body_len" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $rec == "review" then "fetch + review"
              elif $rec == "wait" then "hold the review — re-run ready later"
              else "nudge the author instead of reviewing" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),
        data:{ready:$ready,recommendation:$rec,blockers:$blockers,state:$st,draft:$draft,
          mergeable:$m,mergeStateStatus:$mss,
          ci:{failing:$ci_fail,total:$ci_total},threads_open:$threads_open,description_chars:$body_len},
        error:null}' >&9
  fi
  # Gate exit: advisory default always lands here with exit 0; --check turns
  # not-ready into exit 1 (output identical either way).
  if [[ "$check_gate" == "true" && "$ready" == "false" ]]; then exit 1; fi
  exit 0
fi

# ------------------------------------------------------------------ trend ---
# Portfolio rollup: findings opened/addressed/closed over time across every
# reviewed bundle (.review/*/findings.json). Pure read, writes nothing, no
# network — rescan semantics, so hand-edited stores stay truthful. Each bundle
# is one point in time (post.json posted_at, else record.json recorded_at);
# bundles without either land in an undated bucket, so series sums still match
# totals. Severity is the tier (critical/important/informational; null reads
# untagged, mirroring report --check); closed means no longer actionable
# (wont-fix/false-positive/deferred). Per-bundle models/effort come from
# cost.json when the agent wrote one — the calibration join key. Counts are
# per store: adopted findings appear in both the local and the PR bundle.
if [[ "$cmd" == "trend" ]]; then
  overlap="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --overlap) overlap="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for trend (want --overlap, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  now=$(now_utc)
  rows="[]"
  if [[ -d .review ]]; then
    for b in .review/*/; do
      [[ -d "$b" ]] || continue
      [[ -f "${b}findings.json" ]] || continue
      name=${b#.review/}; name=${name%/}
      fjson=$(cat "${b}findings.json" 2>/dev/null || true)
      if ! jq -e 'type == "array"' <<<"$fjson" >/dev/null 2>&1; then
        warn "trend skips $b (findings.json unreadable or not a findings array)"
        continue
      fi
      at=""
      if [[ -f "${b}post.json" ]]; then at=$(jq -r '.posted_at // empty' "${b}post.json" 2>/dev/null || true); fi
      if [[ -z "$at" && -f "${b}record.json" ]]; then at=$(jq -r '.recorded_at // empty' "${b}record.json" 2>/dev/null || true); fi
      models="[]"; efforts="[]"
      if [[ -f "${b}cost.json" ]]; then
        models=$(jq '[if type == "array" then .[] else . end | (.models // [])[] | .name // empty] | unique' "${b}cost.json" 2>/dev/null || echo "[]")
        efforts=$(jq '[(if type == "array" then .[] else . end) | ((.effort // "standard" | tostring | ascii_downcase) | if . == "cheap" or . == "standard" or . == "deep" then . else "standard" end)] | unique' "${b}cost.json" 2>/dev/null || echo "[]")
      fi
      row=$(jq -n --argjson f "$fjson" --arg b "$name" --arg at "$at" --argjson models "$models" --argjson efforts "$efforts" '
        {bundle: $b, at: (if $at == "" then null else $at end), models: $models, efforts: $efforts,
         findings: ($f | length),
         open: ([$f[] | select(.status == "open")] | length),
         addressed: ([$f[] | select(.status == "addressed")] | length),
         closed: ([$f[] | select(.status == "wont-fix" or .status == "false-positive" or .status == "deferred")] | length),
         by_status: {open: ([$f[] | select(.status == "open")] | length),
           addressed: ([$f[] | select(.status == "addressed")] | length),
           "wont-fix": ([$f[] | select(.status == "wont-fix")] | length),
           "false-positive": ([$f[] | select(.status == "false-positive")] | length),
           deferred: ([$f[] | select(.status == "deferred")] | length)},
         by_tier: {critical: ([$f[] | select(.severity == "critical")] | length),
           important: ([$f[] | select(.severity == "important")] | length),
           informational: ([$f[] | select(.severity == "informational")] | length),
           untagged: ([$f[] | select(.severity != "critical" and .severity != "important" and .severity != "informational")] | length)}}')
      rows=$(jq --argjson r "$row" '. + [$r]' <<<"$rows")
    done
  fi
  rollup=$(jq -n --argjson rows "$rows" '
    {bundles: ($rows | length),
     findings: ([$rows[].findings] | add // 0),
     open: ([$rows[].open] | add // 0),
     addressed: ([$rows[].addressed] | add // 0),
     closed: ([$rows[].closed] | add // 0),
     by_status: {open: ([$rows[].by_status.open] | add // 0),
       addressed: ([$rows[].by_status.addressed] | add // 0),
       "wont-fix": ([$rows[].by_status."wont-fix"] | add // 0),
       "false-positive": ([$rows[].by_status."false-positive"] | add // 0),
       deferred: ([$rows[].by_status.deferred] | add // 0)},
     by_tier: {critical: ([$rows[].by_tier.critical] | add // 0),
       important: ([$rows[].by_tier.important] | add // 0),
       informational: ([$rows[].by_tier.informational] | add // 0),
       untagged: ([$rows[].by_tier.untagged] | add // 0)},
     periods: ([$rows[] | . + {period: (if .at == null then null else .at[0:10] end)}]
       | group_by(.period)
       | map({period: .[0].period, bundles: length,
           findings: ([.[].findings] | add // 0),
           open: ([.[].open] | add // 0),
           addressed: ([.[].addressed] | add // 0),
           closed: ([.[].closed] | add // 0),
           by_tier: {critical: ([.[].by_tier.critical] | add // 0),
             important: ([.[].by_tier.important] | add // 0),
             informational: ([.[].by_tier.informational] | add // 0),
             untagged: ([.[].by_tier.untagged] | add // 0)}})
       | sort_by(if .period == null then "~" else .period end))}')
  n_bundles=$(jq -r '.bundles' <<<"$rollup")
  # Overlap audit (--overlap only): offline join of recalled x cited x
  # outcomes into the three arena-binding numbers. Recall precision =
  # |recalled n cited| / |recalled|; coverage = |recalled n cited| /
  # |cited|; outcome split = addressed vs false-positive over cited
  # findings. The cited set comes from findings.json kb arrays with the same
  # (.kb // []) extraction credit uses (normalized at merge time, so
  # unknown/retired ids never count); review.json is transient agent input
  # and may be absent, so the store is the auditable cite set. Missing or
  # unreadable manifests read unmeasured (never zero) and stay out of both
  # denominators; the outcome split still counts cited findings from
  # unmeasured bundles. Joins rows corroborate credited flips;
  # calibrate_eligible marks bundles carrying a posted review id + head that
  # calibrate could settle against live threads (no recomputation, no
  # network). Pure read: writes nothing.
  overlap_json="null"
  if [[ "$overlap" == "true" ]]; then
    orows="[]"
    joins_all="[]"
    if [[ -f "$KB_JOINS" ]]; then
      if ! joins_all=$(jq -c -s '[.[] | select(type == "object")]' "$KB_JOINS" 2>/dev/null); then
        warn "kb joins $KB_JOINS unreadable — joins counts off for this rollup (fix or remove the file)"
        joins_all="[]"
      fi
    fi
    if [[ -d .review ]]; then
      for b in .review/*/; do
        [[ -d "$b" ]] || continue
        [[ -f "${b}findings.json" ]] || continue
        name=${b#.review/}; name=${name%/}
        fjson=$(cat "${b}findings.json" 2>/dev/null || true)
        if ! jq -e 'type == "array"' <<<"$fjson" >/dev/null 2>&1; then
          continue
        fi
        measured_json="false"; recalled="[]"
        if [[ -f "${b}kb-recall.json" ]]; then
          if jq -e 'type == "array"' "${b}kb-recall.json" >/dev/null 2>&1; then
            measured_json="true"
            recalled=$(jq -c '[.[] | select(type == "object") | .id | select(type == "string")]' "${b}kb-recall.json" 2>/dev/null || echo "[]")
          else
            warn "trend skips ${b}kb-recall.json (unreadable or not a manifest array)"
          fi
        fi
        cel="false"
        case "$b" in
          .review/local-*) ;;
          *)
            if [[ -f "${b}post.json" ]] \
              && [[ -n "$(jq -r '.review.id // empty' "${b}post.json" 2>/dev/null || true)" ]] \
              && [[ -n "$(jq -r '.head // empty' "${b}post.json" 2>/dev/null || true)" ]]; then
              cel="true"
            fi;;
        esac
        orow=$(jq -n --arg b "$name" --argjson m "$measured_json" --argjson rec "$recalled" \
          --argjson f "$fjson" --argjson j "$joins_all" --argjson cel "$cel" '
          ([$f[] | ((.kb // [])[]?) | select(type == "string")] | unique) as $cited
          | (($rec - ($rec - $cited))) as $inter
          | ([$f[] | select(((.kb // []) | length) > 0)]) as $cf
          | {bundle: $b, measured: $m, recalled: $rec, cited: $cited, overlap: $inter,
             precision: (if $m and ($rec | length) > 0 then ($inter | length) / ($rec | length) else null end),
             coverage: (if $m and ($cited | length) > 0 then ($inter | length) / ($cited | length) else null end),
             cited_findings: ($cf | length),
             addressed: ([$cf[] | select(.status == "addressed")] | length),
             false_positive: ([$cf[] | select(.status == "false-positive")] | length),
             pending: ([$cf[] | select(.status == "open" or .status == "deferred")] | length),
             wont_fix: ([$cf[] | select(.status == "wont-fix")] | length),
             joins_rows: ([$j[] | select(.bundle == $b)] | length),
             calibrate_eligible: $cel}')
        orows=$(jq --argjson r "$orow" '. + [$r]' <<<"$orows")
      done
    fi
    overlap_json=$(jq -n --argjson rows "$orows" --argjson j "$joins_all" '
      ([$rows[] | select(.measured) | .recalled[]] | unique) as $rec
      | ([$rows[] | select(.measured) | .cited[]] | unique) as $cit
      | (($rec - ($rec - $cit))) as $inter
      | {measured_bundles: ([$rows[] | select(.measured)] | length),
         unmeasured_bundles: ([$rows[] | select(.measured | not)] | length),
         unmeasured: ([$rows[] | select(.measured | not) | .bundle]),
         bundles: ($rows | length),
         recalled_rules: $rec, cited_rules: $cit, overlap_rules: $inter,
         precision: (if ($rec | length) > 0 then ($inter | length) / ($rec | length) else null end),
         coverage: (if ($cit | length) > 0 then ($inter | length) / ($cit | length) else null end),
         cited_findings: ([$rows[].cited_findings] | add // 0),
         addressed: ([$rows[].addressed] | add // 0),
         false_positive: ([$rows[].false_positive] | add // 0),
         pending: ([$rows[].pending] | add // 0),
         wont_fix: ([$rows[].wont_fix] | add // 0),
         joins: {rows: ($j | length),
           addressed: ([$j[] | select(.outcome == "addressed")] | length),
           false_positive: ([$j[] | select(.outcome == "false-positive")] | length)},
         calibrate_eligible_bundles: ([$rows[] | select(.calibrate_eligible)] | length),
         per_bundle: $rows}')
  fi
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg t "$now" --argjson roll "$rollup" --argjson rows "$rows" --argjson ov "$overlap_json" \
      '{schema_version:"1",verb:$v,repo:$r,pr:null,at:$t,
        next:(if $roll.bundles > 0 then "calibrate recent bundles for per-model tier precision" else "no reviewed bundles yet — fetch + post/record first" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:({generated_at:$t} + $roll + {per_bundle:$rows} + (if $ov == null then {} else {overlap:$ov} end)),error:null}' >&9
  else
    if (( n_bundles == 0 )); then
      echo "trend: $repo — no reviewed bundles (no .review/*/findings.json yet)"
    else
      echo "trend: $repo — $n_bundles bundle(s), $(jq -r '.findings' <<<"$rollup") findings (open=$(jq -r '.open' <<<"$rollup") addressed=$(jq -r '.addressed' <<<"$rollup") closed=$(jq -r '.closed' <<<"$rollup"))"
      jq -r '.periods[] | "\(.period // "undated")  bundles=\(.bundles) findings=\(.findings) open=\(.open) addressed=\(.addressed) closed=\(.closed) tiers critical=\(.by_tier.critical) important=\(.by_tier.important) informational=\(.by_tier.informational) untagged=\(.by_tier.untagged)"' <<<"$rollup"
      echo "tiers: $(jq -r '"critical=\(.by_tier.critical) important=\(.by_tier.important) informational=\(.by_tier.informational) untagged=\(.by_tier.untagged)"' <<<"$rollup")"
      echo "status: $(jq -r '"open=\(.by_status.open) addressed=\(.by_status.addressed) wont-fix=\(.by_status."wont-fix") false-positive=\(.by_status."false-positive") deferred=\(.by_status.deferred)"' <<<"$rollup")"
    fi
    if [[ "$overlap" == "true" ]]; then
      jq -r '"overlap: \(.measured_bundles) measured, \(.unmeasured_bundles) unmeasured — precision=\(.precision // "n/a") coverage=\(.coverage // "n/a") (recalled=\(.recalled_rules | length) cited=\(.cited_rules | length) overlap=\(.overlap_rules | length))",
        "cited outcomes: \(.cited_findings) cited findings — addressed=\(.addressed) false-positive=\(.false_positive) pending=\(.pending) wont-fix=\(.wont_fix); joins rows=\(.joins.rows) (addressed=\(.joins.addressed) false-positive=\(.joins.false_positive))",
        (.per_bundle[] | "  \(.bundle): measured=\(.measured) precision=\(.precision // "n/a") coverage=\(.coverage // "n/a") (recalled=\(.recalled | length) cited=\(.cited | length) overlap=\(.overlap | length)) outcomes addressed=\(.addressed) false-positive=\(.false_positive) pending=\(.pending)")' <<<"$overlap_json"
    fi
  fi
  exit 0
fi

# ------------------------------------------------------------------ learn ---
# Structured local knowledge base: append scoped rules, list them with
# counters, retire/restore by human veto. Store is .review/kb.jsonl (one JSON
# object per line); no repo/PR args, no network.
if [[ "$cmd" == "learn" ]]; then
  lrule=""; lscope=""; lid=""; lprov="manual"; lrigor="pointed"; lreason=""
  lretire=""; lrestore=""; lstatus="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --rule) lrule="${2:-}"; shift 2;;
    --scope) lscope="${2:-}"; shift 2;;
    --id) lid="${2:-}"; shift 2;;
    --provenance) lprov="${2:-}"; shift 2;;
    --rigor) lrigor="${2:-}"; shift 2;;
    --status) lstatus="true"; shift;;
    --retire) lretire="${2:-}"; shift 2;;
    --restore) lrestore="${2:-}"; shift 2;;
    --reason) lreason="${2:-}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for learn (want --rule, --scope, --id, --provenance, --rigor, --status, --retire, --restore, --reason, --json)" >&2; exit 2;;
  esac; done
  rentry_cmd="./scripts/review.sh learn"
  [[ -n "$lrule" ]] && rentry_cmd="$rentry_cmd --rule ..."
  [[ -n "$lscope" ]] && rentry_cmd="$rentry_cmd --scope ..."
  [[ "$lstatus" == "true" ]] && rentry_cmd="$rentry_cmd --status"
  [[ -n "$lretire" ]] && rentry_cmd="$rentry_cmd --retire ..."
  [[ -n "$lrestore" ]] && rentry_cmd="$rentry_cmd --restore ..."
  [[ "$json_mode" == "true" ]] && rentry_cmd="$rentry_cmd --json"
  json_redirect
  now=$(now_utc)
  modes=0
  [[ -n "$lrule" ]] && modes=$((modes + 1))
  [[ "$lstatus" == "true" ]] && modes=$((modes + 1))
  [[ -n "$lretire" ]] && modes=$((modes + 1))
  [[ -n "$lrestore" ]] && modes=$((modes + 1))
  if (( modes != 1 )); then fail "bad-flags" "learn needs exactly one of --rule <text>, --status, --retire <id>, --restore <id>"; fi
  if [[ -z "$lrule" && -n "$lscope" ]]; then fail "bad-flags" "learn: --scope belongs to --rule (append mode)"; fi
  if [[ -z "$lretire" && -z "$lrestore" && -n "$lreason" ]]; then fail "bad-flags" "learn: --reason belongs to --retire/--restore"; fi
  if [[ "$lstatus" == "true" ]]; then
    rows="[]"
    if [[ -f "$KB_FILE" ]]; then
      jq empty "$KB_FILE" 2>/dev/null || fail "corrupt-store" "kb store $KB_FILE is unreadable (restore from backup or fix the bad line)"
      rows=$(jq -s -c '[.[] | . + {precision: (if ((.judged // 0) == 0) then "n/a" else "\(.hits // 0)/\(.judged)" end)}]' "$KB_FILE")
    fi
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg t "$now" --argjson rules "$rows" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,
          next:"learn --rule ... to append; learn --retire/--restore to veto",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{rules:$rules},error:null}' >&9
    else
      n=$(jq 'length' <<<"$rows")
      if [[ "$n" == "0" ]]; then echo "learn: no rules yet (pass --rule to append the first)";
      else jq -r '.[] | "\(.id) [\(.status)] \(.rigor) hits=\(.hits // 0) false_hits=\(.false_hits // 0) judged=\(.judged // 0) precision=\(.precision)"' <<<"$rows"; fi
    fi
    exit 0
  fi
  if [[ -n "$lrule" ]]; then
    lrule_t=$(printf '%s' "$lrule" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -z "$lrule_t" ]] && fail "bad-rule" "refusing learn: --rule is empty (pass the one-line rule text)"
    lscope_t=$(printf '%s' "$lscope" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ -z "$lscope_t" ]] && fail "bad-scope" "refusing learn: --scope is empty (pass a comma-separated glob list, e.g. 'src/*.ts')"
    empty_elem="false"
    while IFS= read -r _e; do
      _et=$(printf '%s' "$_e" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
      if [[ -z "$_et" ]]; then empty_elem="true"; break; fi
    done < <(printf '%s\n' "$lscope_t" | tr ',' '\n')
    [[ "$empty_elem" == "true" ]] && fail "bad-scope" "refusing learn: --scope holds an empty element (comma-separated globs, no blanks)"
    case "$lrigor" in said|pointed|walked|ran|reproduced) ;;
      *) fail "bad-rigor" "refusing learn: unknown rigor '$lrigor' (want said|pointed|walked|ran|reproduced)";; esac
    [[ -z "$lprov" ]] && lprov="manual"
    if [[ -z "$lid" ]]; then lid=$(kb_slugify "$lrule_t"); fi
    [[ "$lid" =~ ^[a-z0-9][a-z0-9-]{0,64}$ ]] || fail "bad-id" "refusing learn: id '$lid' is not ^[a-z0-9][a-z0-9-]{0,64}$ (pass --id explicitly)"
    mkdir -p .review
    if [[ -f "$KB_FILE" ]]; then
      jq empty "$KB_FILE" 2>/dev/null || fail "corrupt-store" "kb store $KB_FILE is unreadable (restore from backup or fix the bad line)"
      if jq -s -e --arg r "$lrule_t" '[.[] | select(.rule == $r)] | length > 0' "$KB_FILE" >/dev/null 2>&1; then
        fail "already-learned" "rule text already learned (cite the existing rule id instead of learning it twice)"
      fi
      if jq -s -e --arg id "$lid" '[.[] | select(.id == $id)] | length > 0' "$KB_FILE" >/dev/null 2>&1; then
        fail "id-taken" "id '$lid' is taken (pass a different --id)"
      fi
    fi
    row=$(jq -n -c --arg id "$lid" --arg r "$lrule_t" --arg s "$lscope_t" --arg p "$lprov" --arg g "$lrigor" \
      '{id:$id, rule:$r, scope:$s, provenance:$p, rigor:$g, hits:0, false_hits:0, judged:0, status:"active"}')
    tmp="$KB_FILE.tmp.$$"
    if [[ -f "$KB_FILE" ]]; then cat "$KB_FILE" > "$tmp"; else : > "$tmp"; fi
    printf '%s\n' "$row" >> "$tmp" && mv "$tmp" "$KB_FILE"
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg t "$now" --argjson rule "$row" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,
          next:"cite the rule id in review.json kb lists",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{rule:$rule},error:null}' >&9
    else
      echo "learned $lid -> $KB_FILE (scope $lscope_t, rigor $lrigor)"
    fi
    exit 0
  fi
  if [[ -n "$lretire" ]]; then
    [[ "$lretire" =~ ^[a-z0-9][a-z0-9-]{0,64}$ ]] || fail "bad-id" "refusing learn: id '$lretire' is not ^[a-z0-9][a-z0-9-]{0,64}$"
    [[ -f "$KB_FILE" ]] || fail "unknown-rule" "learn: no rule '$lretire' (no $KB_FILE yet — see learn --status)"
    jq empty "$KB_FILE" 2>/dev/null || fail "corrupt-store" "kb store $KB_FILE is unreadable (restore from backup or fix the bad line)"
    cur=$(jq -s --arg id "$lretire" '[.[] | select(.id == $id)][0] // null' "$KB_FILE")
    [[ "$cur" == "null" ]] && fail "unknown-rule" "learn: no rule '$lretire' (see learn --status)"
    [[ "$(jq -r '.status' <<<"$cur")" == "retired" ]] && fail "already-retired" "learn: rule '$lretire' is already retired"
    if (( ${#lreason} < 30 )); then fail "bad-reason" "learn --retire needs --reason with 30+ chars (human veto, on the record)"; fi
    tmp="$KB_FILE.tmp.$$"
    if jq -c -s --arg id "$lretire" --arg t "$now" --arg r "$lreason" \
        'map(if .id == $id then . + {status:"retired", retiredAt:$t, retireReason:$r, history:((.history // []) + [{at:$t, event:"retired", actor:"human", note:$r}])} else . end) | .[]' \
        "$KB_FILE" > "$tmp" 2>/dev/null; then
      mv "$tmp" "$KB_FILE"
    else
      rm -f "$tmp"; fail "corrupt-store" "kb store $KB_FILE unreadable mid-retire (restore from backup)"
    fi
    updated=$(jq -s -c --arg id "$lretire" '[.[] | select(.id == $id)][0]' "$KB_FILE")
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg t "$now" --argjson rule "$updated" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:"retired rules never recall; restore with learn --restore",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{rule:$rule},error:null}' >&9
    else
      echo "retired $lretire (tombstone kept, counters untouched)"
    fi
    exit 0
  fi
  if [[ -n "$lrestore" ]]; then
    [[ "$lrestore" =~ ^[a-z0-9][a-z0-9-]{0,64}$ ]] || fail "bad-id" "refusing learn: id '$lrestore' is not ^[a-z0-9][a-z0-9-]{0,64}$"
    [[ -f "$KB_FILE" ]] || fail "unknown-rule" "learn: no rule '$lrestore' (no $KB_FILE yet — see learn --status)"
    jq empty "$KB_FILE" 2>/dev/null || fail "corrupt-store" "kb store $KB_FILE is unreadable (restore from backup or fix the bad line)"
    cur=$(jq -s --arg id "$lrestore" '[.[] | select(.id == $id)][0] // null' "$KB_FILE")
    [[ "$cur" == "null" ]] && fail "unknown-rule" "learn: no rule '$lrestore' (see learn --status)"
    [[ "$(jq -r '.status' <<<"$cur")" == "active" ]] && fail "not-retired" "learn: rule '$lrestore' is already active"
    if (( ${#lreason} < 30 )); then fail "bad-reason" "learn --restore needs --reason with 30+ chars (human veto, on the record)"; fi
    tmp="$KB_FILE.tmp.$$"
    if jq -c -s --arg id "$lrestore" --arg t "$now" --arg r "$lreason" \
        'map(if .id == $id then . + {status:"active", history:((.history // []) + [{at:$t, event:"restored", actor:"human", note:$r}])} else . end) | .[]' \
        "$KB_FILE" > "$tmp" 2>/dev/null; then
      mv "$tmp" "$KB_FILE"
    else
      rm -f "$tmp"; fail "corrupt-store" "kb store $KB_FILE unreadable mid-restore (restore from backup)"
    fi
    updated=$(jq -s -c --arg id "$lrestore" '[.[] | select(.id == $id)][0]' "$KB_FILE")
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg v "$cmd" --arg t "$now" --argjson rule "$updated" \
        '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,next:"active rules recall again on scope match",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{rule:$rule},error:null}' >&9
    else
      echo "restored $lrestore (status active, tombstone history kept)"
    fi
    exit 0
  fi
  exit 0
fi

# -------------------------------------------------------------------- fix ---
# fix_worktree_files — sorted unique worktree-changed paths (one per line).
# NUL-parsed (porcelain -z) so spaces/non-ASCII never quote-split; renames
# contribute both sides (either side outside the anchor is escape-worthy).
# Empty when clean or (defensively) unreadable.
fix_worktree_files() {
  local rec="" st="" p=""
  while IFS= read -r -d '' rec; do
    [[ -z "$rec" ]] && continue
    st="${rec:0:2}"; p="${rec:3}"
    case "$st" in
      R*|C*) printf '%s\n' "$p"; IFS= read -r -d '' rec && printf '%s\n' "$rec";;
      *) printf '%s\n' "$p";;
    esac
  done < <(git status --porcelain -z 2>/dev/null || true) | LC_ALL=C sort -u
}

# Bounded verify-gated fix loop over a LOCAL bundle, one pass per invocation.
# fix <owner/repo> <local-slug> [--sig <s>] [--json]
# Picks the top open inline sig (severity critical>important>informational,
# then path asc) unless --sig names one (must be open + inline). Prints the
# finding locator/body/severity/anchors plus the full kb.txt context when
# present, with patch -> verification.json -> record/post -> re-run fix
# instructions. The agent runs the verify command itself; this script never
# executes it (no new exec surface — the resultHead==HEAD match in the
# existing record/post verification gate is the freshness proof). Ceilings are
# hard-coded: MAX 3 passes per sig (ledger $dir/fix.json); touching any
# worktree file outside the finding's anchor path stops the loop
# (scope-escape, an effective files ceiling of 1). Local bundles only in v1.
if [[ "$cmd" == "fix" ]]; then
  fix_sig=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --sig) fix_sig="${2:-}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for fix (want --sig <sig>, --json)" >&2; exit 2;;
  esac; done
  rentry_cmd="./scripts/review.sh fix $repo $pr"
  [[ -n "$fix_sig" ]] && rentry_cmd="$rentry_cmd --sig $fix_sig"
  if [[ "$json_mode" == "true" ]]; then rentry_cmd="$rentry_cmd --json"; json_redirect; fi
  resolve_bundle_dir "$pr"
  case "$dir" in .review/local-*) ;; *) fail "not-local" "fix works on local bundles; fetch-local first";; esac
  # Start gates, refused before any work. Local bundles carry source.json, not
  # PR.json, so the PR-state gate skips when no PR.json rides along; a bundle
  # that does carry one and reads closed/merged refuses like post.
  if [[ -f "$dir/PR.json" ]]; then
    fix_state=$(pr_field "$dir" state)
    case "$fix_state" in closed|merged) fail "pr-not-open" "bundle PR is $fix_state — refusing fix (re-fetch if this bundle is stale)";; esac
  fi
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if [[ -n "$(git ls-files -u 2>/dev/null)" ]]; then
      fail "conflicted-tree" "worktree holds unresolved merge conflicts (git ls-files -u non-empty) — resolve or abort the merge, then re-run fix"
    fi
  else
    fail "not-a-repo" "fix runs inside a git worktree (it audits worktree files against the anchor)"
  fi
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run record first, then fix)"
  jq -e 'type == "array"' "$dir/findings.json" >/dev/null 2>&1 \
    || fail "corrupt-store" "findings store $dir/findings.json is unreadable or not a findings array (re-run record to rebuild, or restore the file)"
  now=$(now_utc)
  # Readiness summary from STORED bundle data only (no network): PR state when
  # a PR.json rides along, stored thread counts when present, open counts.
  # Advisory — never gates beyond the start gates above.
  fix_open=$(jq '[.[] | select(.status == "open")] | length' "$dir/findings.json")
  fix_open_inline=$(jq '[.[] | select(.status == "open" and .kind == "inline")] | length' "$dir/findings.json")
  fix_nr="0"; fix_ni="0"
  if [[ -f "$dir/reviews.json" ]]; then fix_nr=$(jq 'if type == "array" then length else 0 end' "$dir/reviews.json" 2>/dev/null || echo 0); fi
  if [[ -f "$dir/inline.json" ]]; then fix_ni=$(jq 'if type == "array" then length else 0 end' "$dir/inline.json" 2>/dev/null || echo 0); fi
  case "$fix_nr" in ''|*[!0-9]*) fix_nr=0;; esac
  case "$fix_ni" in ''|*[!0-9]*) fix_ni=0;; esac
  fix_threads="none stored"
  if [[ -f "$dir/reviews.json" || -f "$dir/inline.json" ]]; then fix_threads="reviews.json=$fix_nr inline.json=$fix_ni"; fi
  fix_prinfo="n/a (local bundle, PR check skipped)"
  if [[ -f "$dir/PR.json" ]]; then
    fix_prinfo="state $(pr_field "$dir" state)"
    if [[ "$(jq -r '.draft // false' "$dir/PR.json" 2>/dev/null || echo false)" == "true" ]]; then fix_prinfo="$fix_prinfo draft"; fi
  fi
  echo "» readiness: $dir — findings $fix_open open ($fix_open_inline inline open); threads (stored): $fix_threads; PR $fix_prinfo"
  # Ledger: .review/<bundle>/fix.json {sigs: {<sig>: {passes, last_at, baseline, stopped}}}.
  # baseline = worktree files present at pass 1 (pre-existing dirt is
  # grandfathered; only NEW dirt can trip the escape audit). Fix writes only
  # this file (plus terminal output) — never findings.json.
  fix_ledger="$dir/fix.json"
  fix_store='{"sigs":{}}'
  if [[ -f "$fix_ledger" ]]; then
    fix_read=$(jq -c '{sigs: (.sigs // {})}' "$fix_ledger" 2>/dev/null || true)
    if [[ -n "$fix_read" ]]; then fix_store="$fix_read";
    else warn "fix ledger $fix_ledger unreadable — starting fresh (prior pass counts lost)"; fi
  else
    printf '%s\n' "$fix_store" > "$fix_ledger.tmp.$$" && mv "$fix_ledger.tmp.$$" "$fix_ledger"
  fi
  # Explicit sig: must name an open inline finding (closed/section/unknown refuse).
  fix_full=""
  if [[ -n "$fix_sig" ]]; then
    fix_full=$(resolve_sig "$(cat "$dir/findings.json")" "$fix_sig")
    case "$fix_full" in
      NONE) fail "bad-sig" "fix: sig prefix '${fix_sig:0:12}' not in store (list open inline sigs with report --status open --kind inline)";;
      MANY) fail "ambiguous-sig" "fix: sig prefix '${fix_sig:0:12}' is ambiguous (copy the full prefix from report output)";;
    esac
    fix_rec=$(jq -c --arg s "$fix_full" '[.[] | select(.sig == $s)][0]' "$dir/findings.json")
    fix_kind=$(jq -r '.kind' <<<"$fix_rec")
    fix_status=$(jq -r '.status' <<<"$fix_rec")
    if [[ "$fix_kind" != "inline" ]]; then fail "bad-sig" "fix: sig ${fix_full:0:8} is a $fix_kind finding (fix picks inline only)"; fi
    if [[ "$fix_status" != "open" ]]; then fail "bad-sig" "fix: sig ${fix_full:0:8} is $fix_status (fix iterates open sigs only)"; fi
  fi
  if [[ -z "$fix_full" ]]; then
    # Terminal completion: no open inline sigs remain — fixed/open/stopped plus
    # the ledger path, then exit 0 (nothing left to iterate).
    if (( fix_open_inline == 0 )); then
      fix_fixed=$(jq '[.[] | select(.status == "addressed")] | length' "$dir/findings.json")
      fix_stopped=$(jq -r '[(.sigs // {})[] | select(.stopped != null)] | length' <<<"$fix_store")
      case "$fix_stopped" in ''|*[!0-9]*) fix_stopped=0;; esac
      echo "fix complete: $dir — fixed=$fix_fixed open=$fix_open stopped=$fix_stopped (ledger $fix_ledger)"
      if (( fix_open > 0 )); then
        echo "next: $fix_open open finding(s) left are non-inline or dispositioned — triage or record to settle them"
      else
        echo "next: done — no open findings"
      fi
      if [[ "$json_mode" == "true" ]]; then
        jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg d "$dir" \
          --arg ledger "$fix_ledger" --argjson fixed "$fix_fixed" --argjson open "$fix_open" \
          --argjson stopped "$fix_stopped" \
          '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
            next:(if $open == 0 then "done — no open findings" else "triage or record to settle the remaining open findings" end),
            warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),
            data:{dir:$d,ledger:$ledger,sig:null,locator:null,severity:null,body:null,anchors:null,pass:null,max_passes:3,kb:null,open_inline:0,fixed:$fixed,open:$open,stopped:$stopped,complete:true},error:null}' >&9
      fi
      exit 0
    fi
    fix_full=$(jq -r '[.[] | select(.status == "open" and .kind == "inline")] | sort_by([({"critical":0,"important":1,"informational":2}[.severity // ""] // 3), (.path // ""), (.line // 0), .sig]) | .[0].sig // empty' "$dir/findings.json")
    [[ -z "$fix_full" ]] && fail "report-failed" "fix: open-inline count disagrees with the store (re-run record to rebuild $dir/findings.json)"
    fix_rec=$(jq -c --arg s "$fix_full" '[.[] | select(.sig == $s)][0]' "$dir/findings.json")
  fi
  fix_passes=$(jq -r --arg s "$fix_full" '(.sigs[$s].passes // 0)' <<<"$fix_store")
  case "$fix_passes" in ''|*[!0-9]*) fix_passes=0;; esac
  # Ceiling: MAX 3 passes per sig. Tombstone once (first stop wins); passes
  # never advance on a refusal.
  if (( fix_passes >= 3 )); then
    fix_store=$(jq -c --arg s "$fix_full" --arg t "$now" '.sigs[$s] = ((.sigs[$s] // {}) | {passes: (.passes // 3), last_at: $t, stopped: (.stopped // "ceiling")})' <<<"$fix_store")
    printf '%s\n' "$fix_store" > "$fix_ledger.tmp.$$" && mv "$fix_ledger.tmp.$$" "$fix_ledger"
    fix_fixed=$(jq '[.[] | select(.status == "addressed")] | length' "$dir/findings.json")
    fix_stopped=$(jq -r '[(.sigs // {})[] | select(.stopped != null)] | length' <<<"$fix_store")
    case "$fix_stopped" in ''|*[!0-9]*) fix_stopped=0;; esac
    echo "fix complete: $dir — fixed=$fix_fixed open=$fix_open stopped=$fix_stopped (ledger $fix_ledger)"
    echo "next: sig ${fix_full:0:8} hit the 3-pass ceiling — triage it or split the fix"
    fail "fix-ceiling" "fix: sig ${fix_full:0:8} already used $fix_passes/3 passes — ceiling reached (stopped: ceiling)"
  fi
  fix_anchor=$(jq -r '.path // empty' <<<"$fix_rec")
  [[ -z "$fix_anchor" ]] && fail "bad-sig" "fix: sig ${fix_full:0:8} carries no anchor path (cannot scope the pass)"
  # Scope-escape audit: past the first pass, any worktree file outside the
  # anchor stops the loop (effective files ceiling of 1). The first pass only
  # records its baseline (pre-existing dirt is grandfathered); the audit flags
  # only files that appeared since. Own .review/ scratch never counts.
  if (( fix_passes >= 1 )); then
    fix_base=$(jq -r --arg s "$fix_full" '(.sigs[$s].baseline // [])[]' <<<"$fix_store" 2>/dev/null || true)
    fix_first=""; fix_n=0
    while IFS= read -r fix_f; do
      [[ -z "$fix_f" ]] && continue
      case "$fix_f" in .review/*) continue;; esac
      [[ "$fix_f" == "$fix_anchor" ]] && continue
      if [[ -z "$fix_first" ]]; then fix_first="$fix_f"; fi
      fix_n=$(( fix_n + 1 ))
    done < <(comm -23 <(fix_worktree_files) <(printf '%s\n' "$fix_base" | grep -v '^$' | LC_ALL=C sort -u))
    if (( fix_n > 0 )); then
      fix_store=$(jq -c --arg s "$fix_full" --arg t "$now" '.sigs[$s] = ((.sigs[$s] // {}) | {passes: (.passes // 1), last_at: $t, stopped: (.stopped // "scope-escape")})' <<<"$fix_store")
      printf '%s\n' "$fix_store" > "$fix_ledger.tmp.$$" && mv "$fix_ledger.tmp.$$" "$fix_ledger"
      fix_fixed=$(jq '[.[] | select(.status == "addressed")] | length' "$dir/findings.json")
      fix_stopped=$(jq -r '[(.sigs // {})[] | select(.stopped != null)] | length' <<<"$fix_store")
      case "$fix_stopped" in ''|*[!0-9]*) fix_stopped=0;; esac
      echo "fix complete: $dir — fixed=$fix_fixed open=$fix_open stopped=$fix_stopped (ledger $fix_ledger)"
      echo "next: worktree escaped the anchor — revert or stash files outside '$fix_anchor', then re-run fix"
      if (( fix_n > 1 )); then
        fail "scope-escape" "fix: worktree touches '$fix_first' and $(( fix_n - 1 )) more outside anchor '$fix_anchor' — revert or stash them, then re-run fix"
      else
        fail "scope-escape" "fix: worktree touches '$fix_first' outside anchor '$fix_anchor' — revert or stash it, then re-run fix"
      fi
    fi
  fi
  fix_pass=$(( fix_passes + 1 ))
  fix_base_now=$(fix_worktree_files | jq -R -s -c 'split("\n") | map(select(length > 0))' 2>/dev/null || echo "[]")
  # Successful pass clears a prior stopped flag (recovery is real); the first
  # pass also snapshots the baseline for later escape audits.
  fix_store=$(jq -c --arg s "$fix_full" --arg t "$now" --argjson p "$fix_pass" --argjson base "$fix_base_now" \
    '.sigs[$s] = ((.sigs[$s] // {}) | {passes: $p, last_at: $t, baseline: (.baseline // $base)})' <<<"$fix_store")
  printf '%s\n' "$fix_store" > "$fix_ledger.tmp.$$" && mv "$fix_ledger.tmp.$$" "$fix_ledger"
  echo "» ledger: $fix_ledger — sig ${fix_full:0:8} pass $fix_pass/3"
  fix_locator=$(jq -r 'if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end' <<<"$fix_rec")
  fix_sev=$(jq -r '.severity // "untagged"' <<<"$fix_rec")
  fix_body=$(jq -r '.body // ""' <<<"$fix_rec")
  fix_side=$(jq -r '.side // "RIGHT"' <<<"$fix_rec")
  fix_ln=$(jq -r '.line // empty' <<<"$fix_rec")
  fix_sl=$(jq -r '.start_line // empty' <<<"$fix_rec")
  fix_an=$(jq -r '.anchored // empty' <<<"$fix_rec")
  [[ -z "$fix_ln" ]] && fix_ln="?"
  [[ -z "$fix_sl" ]] && fix_sl="$fix_ln"
  printf '%s\n' "fix: ${fix_full:0:8} [$fix_sev] $fix_locator (pass $fix_pass/3, ledger $fix_ledger)"
  printf '%s\n' "finding: $fix_body"
  printf '%s\n' "anchors: $fix_anchor:$fix_sl-$fix_ln side=$fix_side anchored=$fix_an"
  fix_kb="null"
  if [[ -f "$dir/kb.txt" ]]; then
    printf '%s\n' "--- kb.txt ($dir/kb.txt, full context) ---"
    cat "$dir/kb.txt"
    printf '%s\n' "--- end kb.txt ---"
    fix_kb=$(jq -n --arg f "$dir/kb.txt" --arg c "$(cat "$dir/kb.txt")" '{file:$f, content:$c}')
  fi
  printf '%s\n' "instructions: patch the anchor file ($fix_anchor), then run its verify command yourself and write $dir/verification.json [{sig, verdict, resultHead, verifyCmd}] with verdict VERIFIED and resultHead matching HEAD (this script never runs it — the resultHead==HEAD match in the record/post verification gate is the freshness proof); land it with record $repo $pr; re-run fix $repo $pr for the next pass"
  printf '%s\n' "gate: keep the verify command repo-scoped, non-destructive, and offline — the verify verb refuses destructive/networked/credential-reading/repo-escaping shapes before running anything (typed verify-* errors with a try: hint), so run anything stronger in your own shell instead"
  printf '%s\n' "loops multiply agent spend — cap review spend with REVIEW_BUDGET_USD or .review/budget (see docs/manual.md)"
  printf '%s\n' "next: patch $fix_anchor, write verification.json, run record to land it, then re-run fix"
  if [[ "$json_mode" == "true" ]]; then
    fix_anchors=$(jq -n --arg p "$fix_anchor" --arg ln "$fix_ln" --arg sl "$fix_sl" --arg side "$fix_side" --arg an "$fix_an" '{path:$p,line:$ln,start_line:$sl,side:$side,anchored:$an}')
    fix_stopped_now=$(jq -r '[(.sigs // {})[] | select(.stopped != null)] | length' <<<"$fix_store")
    case "$fix_stopped_now" in ''|*[!0-9]*) fix_stopped_now=0;; esac
    fix_fixed_now=$(jq '[.[] | select(.status == "addressed")] | length' "$dir/findings.json")
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg d "$dir" \
      --arg ledger "$fix_ledger" --arg s "$fix_full" --arg loc "$fix_locator" --arg sev "$fix_sev" \
      --arg body "$fix_body" --argjson anchors "$fix_anchors" --argjson pass "$fix_pass" \
      --argjson kb "$fix_kb" --argjson oi "$fix_open_inline" --argjson fixed "$fix_fixed_now" \
      --argjson open "$fix_open" --argjson stopped "$fix_stopped_now" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:"patch the anchor file, write verification.json, run record to land it, then re-run fix",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),
        data:{dir:$d,ledger:$ledger,sig:$s,locator:$loc,severity:$sev,body:$body,anchors:$anchors,pass:$pass,max_passes:3,kb:$kb,open_inline:$oi,fixed:$fixed,open:$open,stopped:$stopped,complete:false},error:null}' >&9
  fi
  exit 0
fi

# ------------------------------------------------------------------- init ---
# Guided offline scaffolder (new `init` verb — chosen over `fetch --init`
# because fetch owns a network/fetch contract and init must never touch the
# network; one discoverable verb keeps that contract untouched). Writes the
# standard local control files (.review/budget, .review/panel,
# .review/kb-policy, .review/kb.jsonl starter) with style presets, then runs
# check-tree validation (same filter: own .review/ scratch ignored).
# Existing files print a preview and need --yes/--force (or an interactive
# confirm) before overwrite — the same preview-then-confirm idiom the
# installer uses, so operators learn it once.
if [[ "$cmd" == "init" ]]; then
  preset="standard"; assume_yes="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --preset) preset="${2:-}"; shift 2;;
    --preset=*) preset="${1#--preset=}"; shift;;
    --yes|-y|--force) assume_yes="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for init (want --preset <chill|standard|strict>, --yes, --force, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  rentry_cmd="./scripts/review.sh init --preset $preset"
  [[ "$assume_yes" == "true" ]] && rentry_cmd="$rentry_cmd --yes"
  [[ "$json_mode" == "true" ]] && rentry_cmd="$rentry_cmd --json"
  case "$preset" in
    chill|standard|strict) ;;
    *) fail "bad-preset" "unknown --preset '$preset' (want chill|standard|strict)";;
  esac
  # Preset mapping (documented in usage + docs/manual.md): panel strictness
  # grows chill -> strict (no pinning -> one synthesizer -> lead + 2 lenses);
  # KB retirement thresholds tighten the same way (rarely -> defaults ->
  # aggressively); the spend budget tightens too (lenient -> default -> tight).
  case "$preset" in
    chill) budget_val="2.00"; kb_min="10"; kb_rate="0.7"; panel_body="";;
    standard) budget_val="0.50"; kb_min="5"; kb_rate="0.5"; panel_body="main";;
    strict) budget_val="0.10"; kb_min="3"; kb_rate="0.3"; panel_body=$'lead\nlens-1\nlens-2';;
  esac
  budget_new=$(printf '# Verdict spend budget (USD) — written by `review.sh init --preset %s`.\n# Env REVIEW_BUDGET_USD wins over this file. Edit or delete to change.\n%s\n' "$preset" "$budget_val")
  if [[ -z "$panel_body" ]]; then
    panel_new=$(printf '# Verdict panel — written by `review.sh init --preset %s` (chill: no pinning).\n# One model slug per line, first synthesizes. REVIEW_PANEL wins over this file.\n# Replace these comments with real slugs to pin a formation; empty (all comments)\n# means the operator picks per run.\n' "$preset")
  else
    panel_new=$(printf '# Verdict panel — written by `review.sh init --preset %s`.\n# One model slug per line, first synthesizes. REVIEW_PANEL wins over this file.\n# Replace these placeholders with real slugs; same panel every run keeps verdicts comparable.\n%s\n' "$preset" "$panel_body")
  fi
  kbpolicy_new=$(printf '# Verdict KB retirement policy — written by `review.sh init --preset %s`.\n# Env REVIEW_KB_MIN_JUDGED / REVIEW_KB_MAX_FP_RATE win over this file.\nMIN_JUDGED=%s\nMAX_FP_RATE=%s\n' "$preset" "$kb_min" "$kb_rate")
  kb_new=$(printf '%s\n' \
    '{"id":"check-error-paths","rule":"Check error paths on every awaited call before approving","scope":"src/*.ts,src/*.js","provenance":"starter","rigor":"pointed","hits":0,"false_hits":0,"judged":0,"status":"active"}' \
    '{"id":"verify-before-claim","rule":"Verify every addressed claim with the repo'"'"'s own verify command before marking addressed","scope":"*","provenance":"starter","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}')
  mkdir -p .review
  targets=("budget" "panel" "kb-policy" "kb.jsonl")
  t_status=()
  need_overwrite="false"
  for t in "${targets[@]}"; do
    f=".review/$t"; cur=""; st="create"
    if [[ -f "$f" ]]; then
      case "$t" in
        budget) want="$budget_new";; panel) want="$panel_new";; kb-policy) want="$kbpolicy_new";; kb.jsonl) want="$kb_new";;
      esac
      cur=$(cat "$f" 2>/dev/null || true)
      if [[ "$cur" == "$want" ]]; then st="keep";
      else st="overwrite"; need_overwrite="true"; fi
    fi
    t_status+=("$t:$st")
  done
  echo "init: preset $preset (chill: unpinned/10/0.7, standard: 1 member/5/0.5, strict: lead+2 lenses/3/0.3)"
  for t in "${targets[@]}"; do
    case "$t" in
      budget) want="$budget_new";; panel) want="$panel_new";; kb-policy) want="$kbpolicy_new";; kb.jsonl) want="$kb_new";;
    esac
    st="create"
    if [[ -f ".review/$t" ]]; then
      if [[ "$(cat ".review/$t" 2>/dev/null || true)" == "$want" ]]; then st="keep"; else st="overwrite"; fi
    fi
    echo "--- .review/$t ($st) ---"
    printf '%s\n' "$want"
  done
  if [[ "$need_overwrite" == "true" && "$assume_yes" != "true" ]]; then
    if [[ -t 0 ]]; then
      printf 'Proceed? [y/N] ' >&2; read -r ans || ans=""
      case "$ans" in y|Y|yes|YES) ;;
        *) echo "init: aborted (re-run with --yes to overwrite after reviewing the preview)" >&2; exit 1;;
      esac
    else
      fail "needs-confirm" "init refuses to overwrite existing control files without confirm (re-run with --yes after reviewing the preview above)"
    fi
  fi
  for t in "${targets[@]}"; do
    case "$t" in
      budget) want="$budget_new";; panel) want="$panel_new";; kb-policy) want="$kbpolicy_new";; kb.jsonl) want="$kb_new";;
    esac
    if [[ -f ".review/$t" ]] && [[ "$(cat ".review/$t" 2>/dev/null || true)" == "$want" ]]; then
      echo "keep: .review/$t (already up to date)"
    else
      printf '%s\n' "$want" > ".review/$t"
      if [[ -f ".review/$t" ]]; then echo "wrote: .review/$t";
      else fail "init-failed" "init could not write .review/$t (storage writable?)"; fi
    fi
  done
  # Validate what was written (file-first, not env-first: env wins at runtime
  # but the scaffolded files must parse on their own).
  budget_line=$(grep -v '^[[:space:]]*#' .review/budget | grep -v '^[[:space:]]*$' | head -n 1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^\$//' || true)
  [[ "$budget_line" =~ ^[0-9]+(\.[0-9]+)?$ ]] || fail "bad-budget" "scaffolded .review/budget '$budget_line' is not a plain USD number"
  [[ -f .review/panel ]] || fail "init-failed" "scaffolded .review/panel missing after write"
  [[ -f .review/kb-policy ]] || fail "init-failed" "scaffolded .review/kb-policy missing after write"
  jq empty .review/kb.jsonl >/dev/null 2>&1 || fail "bad-settings" "scaffolded .review/kb.jsonl is not valid JSONL"
  [[ "$(jq -s 'map(select(type == "object")) | length' .review/kb.jsonl 2>/dev/null || echo 0)" -gt 0 ]] \
    || fail "bad-settings" "scaffolded .review/kb.jsonl carries no rule rows"
  # Check-tree validation (same filter as the verb: own .review/ scratch ignored).
  tree_out=""; tree_clean="true"
  if porcelain=$(git status --porcelain 2>/dev/null); then
    own=$(grep -c '^.. \.review/' <<<"$porcelain" || true)
    rest=$(grep -v '^.. \.review/' <<<"$porcelain" || true)
    if [[ -z "$rest" ]]; then tree_out="check-tree: clean ($own .review/ scratch line(s) ignored)"; tree_clean="true";
    else tree_out="check-tree: dirty worktree — commit or stash first:"; tree_clean="false"; fi
  else
    tree_out="check-tree: not a git repo"; tree_clean="false"
  fi
  echo "$tree_out"
  if [[ "$tree_clean" == "false" && -n "${rest:-}" ]]; then printf '%s\n' "$rest"; fi
  if [[ "$tree_clean" == "false" ]]; then warn "init wrote the control files but the worktree carries non-.review dirt (see check-tree above)"; fi
  echo "next: replace panel placeholders with real slugs, tune .review/budget, then fetch"
  if [[ "$json_mode" == "true" ]]; then
    files_json=$(jq -n --arg p "$preset" --arg b "$budget_val" --arg m "$kb_min" --arg r "$kb_rate" \
      --argjson st "$(printf '%s\n' "${t_status[@]}" | jq -R . | jq -s .)" \
      --argjson clean "$tree_clean" \
      '{preset:$p,budget_usd:$b,kb_min_judged:($m|tonumber),kb_max_fp_rate:($r|tonumber),files:$st,check_tree_clean:$clean}')
    now=$(now_utc)
    jq -n --arg v "$cmd" --arg t "$now" --argjson f "$files_json" \
      '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,
        next:"replace panel placeholders with real slugs, tune .review/budget, then fetch",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:$f,error:null}' >&9
  fi
  exit 0
fi

# -------------------------------------------------------------- install ---
# Copy-based skill installer (the `install.sh` symlink route stays the
# wide-coverage sibling: nine agents, --detect/--all, doctor-gated). This verb
# is the minimal offline copy route for the three de-facto agent skill dirs
# the comp-study documents (greptile 3.8.0 ships .agents/skills +
# .claude/skills + .codex/skills; coderabbit 0.9.1 names no paths), each at
# the repo root (project-local) and $HOME (global): six candidate roots.
# Only the tool-owned `verdict/` namespace is ever written, receipted by
# `.verdict-managed`; foreign skills and unowned `verdict/` dirs are never
# modified or removed. Preview-then-confirm reuses the init idiom exactly
# (--yes/--force or an interactive confirm; non-tty refuses needs-confirm).
if [[ "$cmd" == "install" ]]; then
  assume_yes="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --yes|-y|--force) assume_yes="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for install (want --yes, --force, --json)" >&2; exit 2;;
  esac; done
  json_redirect
  rentry_cmd="./scripts/review.sh install"
  [[ "$assume_yes" == "true" ]] && rentry_cmd="$rentry_cmd --yes"
  [[ "$json_mode" == "true" ]] && rentry_cmd="$rentry_cmd --json"
  tool_root="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)"
  [[ -f "$tool_root/SKILL.md" ]] || fail "install-failed" "tool payload missing: $tool_root/SKILL.md not found (not a verdict checkout?)"
  [[ -d "$tool_root/prompts" ]] || fail "install-failed" "tool payload missing: $tool_root/prompts not found (not a verdict checkout?)"
  tool_version=$(jq -r .version "$tool_root/package.json" 2>/dev/null || echo "0.0.0-unknown")
  payload_files=("SKILL.md")
  for pf in "$tool_root"/prompts/*.md; do
    [[ -f "$pf" ]] || continue
    payload_files+=("prompts/$(basename "$pf")")
  done
  (( ${#payload_files[@]} > 1 )) || fail "install-failed" "tool payload missing: no prompts found in $tool_root/prompts (not a verdict checkout?)"
  # Candidate roots: the three de-facto skill dirs at repo root + $HOME.
  # A root is detected when its skills dir or its agent parent exists;
  # anything else reads skip — the verb never invents agent homes.
  cand_roots=("$PWD/.agents/skills" "$PWD/.claude/skills" "$PWD/.codex/skills")
  cand_scopes=("repo" "repo" "repo")
  if [[ -n "${HOME:-}" ]]; then
    cand_roots+=("$HOME/.agents/skills" "$HOME/.claude/skills" "$HOME/.codex/skills")
    cand_scopes+=("home" "home" "home")
  fi
  roots=(); scopes=()
  for i in "${!cand_roots[@]}"; do
    cr="${cand_roots[$i]}"; dup="false"
    if (( ${#roots[@]} > 0 )); then
      for r in "${roots[@]}"; do [[ "$r" == "$cr" ]] && dup="true"; done
    fi
    [[ "$dup" == "true" ]] && continue
    roots+=("$cr"); scopes+=("${cand_scopes[$i]}")
  done
  # Phase 1: classify every root, print the preview plan (no writes).
  t_status=(); t_skew=(); t_diffs=()
  need_write="false"
  echo "install: tool v$tool_version, payload ${#payload_files[@]} files (SKILL.md + $(( ${#payload_files[@]} - 1 )) prompts) -> ${#roots[@]} candidate root(s)"
  for i in "${!roots[@]}"; do
    root="${roots[$i]}"; scope="${scopes[$i]}"; target="$root/verdict"; receipt="$target/.verdict-managed"
    st=""; rec_v=""; diffs=""; skew="false"
    if [[ ! -d "$root" && ! -d "$(dirname "$root")" ]]; then
      st="skip"
    elif [[ -e "$target" || -L "$target" ]]; then
      if [[ -f "$receipt" ]] && jq -e '.tool == "verdict"' "$receipt" >/dev/null 2>&1; then
        rec_v=$(jq -r '.version // empty' "$receipt" 2>/dev/null || true)
        [[ "$rec_v" != "$tool_version" ]] && skew="true"
        for rel in "${payload_files[@]}"; do
          if [[ -f "$target/$rel" ]] && cmp -s "$tool_root/$rel" "$target/$rel"; then continue; fi
          diffs="$diffs$rel
"
        done
        if [[ -z "$diffs" ]]; then st="keep"; else st="update"; need_write="true"; fi
      else
        st="blocked"
      fi
    else
      st="create"; need_write="true"
      for rel in "${payload_files[@]}"; do diffs="$diffs$rel
"; done
    fi
    t_status+=("$st"); t_skew+=("$skew"); t_diffs+=("$diffs")
    case "$st" in
      create)
        echo "--- $target [$scope] (create) ---"
        while IFS= read -r rel; do
          [[ -z "$rel" ]] && continue
          echo "  + $rel ($(wc -c < "$tool_root/$rel" | tr -d '[:space:]') bytes)"
        done <<<"$diffs";;
      update)
        echo "--- $target [$scope] (update, installed v${rec_v:-unknown}) ---"
        while IFS= read -r rel; do
          [[ -z "$rel" ]] && continue
          if [[ -f "$target/$rel" ]]; then
            echo "  ~ $rel (differs: installed $(wc -c < "$target/$rel" | tr -d '[:space:]') -> payload $(wc -c < "$tool_root/$rel" | tr -d '[:space:]') bytes)"
          else
            echo "  + $rel (new, $(wc -c < "$tool_root/$rel" | tr -d '[:space:]') bytes)"
          fi
        done <<<"$diffs"
        echo "  = $(( ${#payload_files[@]} - $(printf '%s' "$diffs" | grep -c .) )) file(s) unchanged";;
      keep)
        echo "--- $target [$scope] (keep, already up to date) ---"
        [[ "$skew" == "true" ]] && warn "version skew at $target: receipt v$rec_v vs tool v$tool_version (nudge only — re-run with --yes to refresh the receipt)";;
      blocked)
        echo "--- $target [$scope] (blocked: no installer receipt — foreign-owned, left untouched) ---"
        warn "leaving foreign-owned $target untouched (no .verdict-managed receipt — remove it by hand to let the installer own this slot, nothing was written)";;
      skip)
        echo "--- $root [$scope] (skip: no agent dir) ---";;
    esac
  done
  if [[ "$need_write" == "true" && "$assume_yes" != "true" ]]; then
    if [[ -t 0 ]]; then
      printf 'Proceed? [y/N] ' >&2; read -r ans || ans=""
      case "$ans" in y|Y|yes|YES) ;;
        *) echo "install: aborted (re-run with --yes to apply after reviewing the preview)" >&2; exit 1;;
      esac
    else
      fail "needs-confirm" "install refuses to write skill files without confirm (re-run with --yes after reviewing the preview above)"
    fi
  fi
  # Phase 2: apply. Only owned namespaces are written; unknown extra files
  # inside verdict/ are left alone (never deleted), foreign slots untouched.
  wrote="0"
  for i in "${!roots[@]}"; do
    root="${roots[$i]}"; target="$root/verdict"; receipt="$target/.verdict-managed"
    st="${t_status[$i]}"; diffs="${t_diffs[$i]}"
    case "$st" in
      create|update)
        mkdir -p "$target/prompts" || fail "install-failed" "install could not create $target/prompts (storage writable?)"
        n="0"
        while IFS= read -r rel; do
          [[ -z "$rel" ]] && continue
          cp -- "$tool_root/$rel" "$target/$rel" || fail "install-failed" "install could not write $target/$rel (storage writable?)"
          n=$(( n + 1 ))
        done <<<"$diffs"
        jq -n --arg v "$tool_version" --arg t "$(now_utc)" \
          '{tool:"verdict", version:$v, installed_at:$t}' > "$receipt.tmp" \
          && mv "$receipt.tmp" "$receipt" \
          || fail "install-failed" "install could not write $receipt (storage writable?)"
        echo "wrote: $target ($n file(s), receipt v$tool_version)"
        wrote=$(( wrote + 1 ));;
      keep)
        echo "keep: $target (already up to date)"
        if [[ "${t_skew[$i]}" == "true" && "$assume_yes" == "true" ]]; then
          jq -n --arg v "$tool_version" --arg t "$(now_utc)" \
            '{tool:"verdict", version:$v, installed_at:$t}' > "$receipt.tmp" \
            && mv "$receipt.tmp" "$receipt" \
            || fail "install-failed" "install could not refresh $receipt (storage writable?)"
          echo "receipt: $target refreshed to v$tool_version"
        fi;;
      blocked) echo "skip: $target (foreign-owned, left untouched)";;
      skip) echo "skip: $root (no agent dir)";;
    esac
  done
  if (( wrote > 0 )); then next_str="reload skills in the agent so it picks up verdict/";
  else next_str="already up to date (foreign dirs untouched)"; fi
  echo "install: $wrote target(s) written"
  echo "next: $next_str"
  if [[ "$json_mode" == "true" ]]; then
    targets_json="[]"
    for i in "${!roots[@]}"; do
      fw="0"; [[ "${t_status[$i]}" == "create" || "${t_status[$i]}" == "update" ]] && fw=$(printf '%s' "${t_diffs[$i]}" | grep -c .)
      targets_json=$(jq -n --argjson t "$targets_json" --arg r "${roots[$i]}" --arg s "${scopes[$i]}" \
        --arg st "${t_status[$i]}" --argjson fw "$fw" --argjson sk "${t_skew[$i]}" \
        '$t + [{root:$r, scope:$s, status:$st, files_written:$fw, version_skew:$sk}]')
    done
    data_json=$(jq -n --arg v "$tool_version" --argjson n "${#payload_files[@]}" --argjson t "$targets_json" \
      '{tool_version:$v, payload_files:$n, targets:$t}')
    now=$(now_utc)
    jq -n --arg v "$cmd" --arg t "$now" --arg nx "$next_str" --argjson f "$data_json" \
      '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,
        next:$nx,
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:$f,error:null}' >&9
  fi
  exit 0
fi

echo "unknown cmd '$cmd' (want fetch|fetch-local|record|adopt|fix|post|triage|report|next|show|check-tree|verify|doctor|calibrate|ready|trend|learn|init|install)" >&2; exit 2
