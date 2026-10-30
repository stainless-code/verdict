#!/usr/bin/env bash
# review.sh — local PR fetch/post/triage/report using gh CLI only.
# Verbs: fetch (network+files), post (validates, merges store, posts once),
#        triage (local store mutation, no network), report (pure read, no network).
set -euo pipefail

usage() {
  cat <<'EOF'
usage: review.sh fetch|post|triage|report|next|show|verify|doctor|calibrate|fetch-local|record|adopt|check-tree|ready|trend [opts]
  fetch <owner/repo> <pr> [--base-sha|--since <sha>] [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] [--consent-over-budget] [--json]
    write .review/<pr>/{PR.json,full.diff,toc.txt,roles.txt,reviews.json,inline.json[,incremental.diff]} (PR.json normalized for non-github providers)
    diffs over 512000 bytes refuse with a split-or-narrow hint (empty diffs warn, never fail).
    With REVIEW_BUDGET_USD (or .review/budget) set, fetch prints a pre-run
    cost estimate from comparable past runs and refuses projected-over-budget
    runs unless --consent-over-budget is given (consent lands in budget.json,
    surfaced by report --cost). Unset: estimate off, fetch as today.
  fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]
        [--stash [n]] [--consent-over-budget] [-- paths...] [--json]
    local review bundle from git (no GitHub, no PR): writes
    .review/local-<name>/{source.json,full.diff,toc.txt,roles.txt}.
    Same diff cap as fetch (512000 bytes); untracked/binary exclusions warn, never fail.
    Same budget gate as fetch (estimate + --consent-over-budget); unset: as today.
    downstream verbs address it as local-<name>; post/calibrate refuse it.
  record <owner/repo> <local-slug> [--json]
    validate + merge a local review.json into findings.json + record.json. No GitHub, no post.
  adopt <owner/repo> <pr> --from <local-slug> [--json]
    carry a local bundle's findings into a fresh PR fetch for posting (re-anchors, stamps handoff).
  post  <owner/repo> <pr> [--approve=true|false] [--request-changes] [--bind] [--force]
        [--review-file .review/<pr>/review.json] [--dry-run] [--json]
    validate anchors (partitioned, keep-biased), embed suggestions, merge findings.json,
    guard stale-SHA + duplicates, post exactly one review.
    Binding APPROVE/REQUEST_CHANGES needs --bind (else downgraded to COMMENT);
    APPROVE is refused with open findings unless --force.
    Agent status claims (resolved[]/triage[]) are proposals: addressed flips only on
    verification.json evidence (VERIFIED + resultHead matching HEAD).
    --dry-run prints validation + payload summary, writes findings.dryrun.json, posts nothing.
  triage <owner/repo> <pr> --sig <sig> --status <open|addressed|wont-fix|false-positive|deferred>
        --rationale "..." [--force] [--dry-run]
    mutate .review/<pr>/findings.json directly. No network. Rationale required unless --force.
  report <owner/repo> <pr> [--status open|addressed|all] [--kind inline|section|all]
        [--output <path>] [--sarif <path>] [--cost] [--check <critical|important|informational>] [--json]
    pure read over the store: ranked findings + verdict + counts. No network.
    --sarif writes a SARIF 2.1.0 file (severity→level, sig as fingerprint) for scanners/CI.
    --cost adds a spend rollup from .review/<pr>/cost.json (absent/unreadable -> zeros, never fails; stratified by effort tier).
    Consented over-budget receipts from .review/<pr>/budget.json surface here when present.
    --check <tier> opts into gating (same output, machine exits): 0 no open
    findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool
    error (missing/corrupt store). Untagged findings never meet a tier; the
    gate counts open findings only, whatever --status/--kind show. Without
    --check the run stays advisory: exit 0 with findings listed (typed
    errors exit 1, usage exits 2).
  next <owner/repo> <pr> [--status open] [--json]
    top-ranked actionable finding + the command to act on it. Pure read.
  show <owner/repo> <pr> --sig <sig> [--json]
    one full finding record. Pure read.
  check-tree [--expect <prefix,...>]
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
  calibrate <owner/repo> <pr> [--json]
    join findings against live ground truth (thread states + files changed since the
    review head). Read-only; reports outcomes + tier precision, never auto-tunes.
  ready <owner/repo> <pr> [--json] [--check]
    advisory only (exit 0, never a gate): CI state, open threads, draft/conflict,
    description completeness. Prints blockers with actions + recommendation
    (review/wait/nudge-author). PR bundles only; local bundles refused.
    --check opts into gating (same output, machine exits): 0 ready, 1 blockers
    or wait, 2 usage, 3 tool error. Without --check the run stays advisory:
    exit 0 with blockers listed (typed errors exit 1, usage exits 2).
  trend <owner/repo> [--json]
    portfolio rollup over existing bundle stores (no network, writes nothing):
    findings opened/addressed/closed over time, broken down by severity tier
    and status, with per-bundle models/effort for the calibration join.
    Recomputed every run (manual edits stay truthful); missing/unreadable
    stores warn and skip, never fail. `closed` means no longer actionable
    (wont-fix/false-positive/deferred); null severity reads untagged.
  --json on fetch/post/triage/report/next/show/verify/doctor/calibrate/fetch-local/record/adopt/check-tree/ready/trend: stdout is exactly one JSON envelope (schema_version 1);
  all human progress goes to stderr. Errors emit {data:null,error:{code,message}} non-zero.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then usage; exit 0; fi
if [[ "${1:-}" == "--version" ]]; then jq -r .version "$(dirname "${BASH_SOURCE[0]:-$0}")/../package.json" 2>/dev/null || echo "0.0.0-unknown"; exit 0; fi
cmd="${1:-}"; shift $(( $# > 0 ? 1 : 0 ))
repo=""; pr=""
if [[ "$cmd" != "check-tree" && "$cmd" != "doctor" && "$cmd" != "trend" ]]; then
  repo="${1:-}"; pr="${2:-}"; shift $(( $# >= 2 ? 2 : $# ))
fi
if [[ "$cmd" == "trend" ]]; then
  repo="${1:-}"; shift $(( $# >= 1 ? 1 : $# ))
fi

need() { command -v "$1" >/dev/null || { echo "missing: $1" >&2; exit 1; }; }
need gh; need jq; need shasum
warn() { echo "warn: $*" >&2; WARNINGS_JSON=$(jq --arg w "$*" '. + [$w]' <<<"$WARNINGS_JSON"); }
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
FETCH_MAX_BYTES=512000
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
    missing-*|not-a-repo|not-local|no-pr-target|no-threads|no-verify-cmd|report-failed) hint="try: run the fetch/record step named above first, then re-run";;
    corrupt-store) hint="try: re-run post/record to rebuild the store, or restore findings.json from backup, then re-run";;
    bad-timeout) hint="try: pass --timeout <positive-seconds> (default 300)";;
    bad-approve) hint="try: pass --approve=true|false";;
    bad-review-schema) hint="try: set \"schema_version\": \"1\" in review.json";;
    approve-blocked) hint="try: triage the findings or re-run with --force";;
    budget-exceeded) hint="try: re-run with --consent-over-budget once the spend is approved, or raise REVIEW_BUDGET_USD/.review/budget";;
    duplicate-payload) hint="try: re-run with --force to repost the identical payload";;
    degenerate-body) hint="try: open with a verdict marker (see prompts/review.md), then re-run";;
    pr-not-open) hint="try: re-fetch the PR (bundle may be stale)";;
    unknown-sig|ambiguous-sig) hint="try: copy the full sig prefix from report output";;
    rationale-required) hint="try: add --rationale with 30+ chars, or --force";;
    already-adopted|provider-unimplemented) hint="try: use the verb named above for this bundle kind";;
    bad-*) hint="try: re-run with one of the valid values named above";;
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
    echo "error: $msg" >&2
  fi
  if [[ -n "$rentry_cmd" ]]; then echo "re-enter: $rentry_cmd" >&2; fi
  # Ready --check turns typed errors into exit 3 so hooks can tell "tool
  # broke" apart from "PR not ready" (exit 1). check_gate stays false on every
  # other path, so all existing fail-contract exits are untouched.
  if [[ "$check_gate" == "true" ]]; then exit 3; fi
  exit 1
}

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

# Validate anchors (partitioned, keep-biased) + merge findings + claims.
# Shared by post (PR bundles) and record (local bundles): identical behavior,
# no GitHub I/O. Reads $dir/{incremental.diff,verification.json,findings.json}
# when present. Sets globals kept/moved/body/entries/n_* /marker/hunk_n.
now_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

validate_and_merge() { # $1 = review.json, $2 = dir, $3 = head full sha ("" ok), $4 = now, $5 = findings file
  local review_file="$1" dir="$2" head_full="$3" now="$4" findings_file="$5"
  # review.json schema: agents stamp schema_version; unknown versions refuse
  # (uninterpretable), missing versions warn-and-proceed (grandfathered agents).
  rv=$(jq -r '.schema_version // empty' "$review_file")
  case "$rv" in "") warn "review.json carries no schema_version — assuming 1 (agents: write \"schema_version\":\"1\")";; 1) ;; *) fail "bad-review-schema" "review.json schema_version '$rv' unknown (script knows 1)";; esac
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
    if ! awk -F'\t' -v f="$key" '$1==f{found=1; exit} END{exit !found}' "$dir/hunks.tsv"; then
      warn "kept despite unknown path (not in diff index, ambiguity keeps): $p"; keep; continue
    fi
    [[ -z "$start" ]] && start="$line"
    if ! [[ "$line" =~ ^[0-9]+$ && "$start" =~ ^[0-9]+$ ]]; then
      warn "kept despite non-numeric anchor: $p $start-$line"; keep; continue
    fi
    if (( start > line )); then t="$start"; start="$line"; line="$t"; fi
    want="NEW"; [[ "$side" == "LEFT" ]] && want="OLD"
    if awk -F'\t' -v f="$key" -v w="$want" -v a="$start" -v b="$line" \
        '$1==f && $2==w && $3<=a && b<=$4 {found=1; exit} END{exit !found}' "$dir/hunks.tsv"; then
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
    move_section=$(jq -r '"### ℹ️ Unanchored observations\n\nThe following points fell outside the diff hunks for the reviewed SHA, so they post here instead of inline:\n" + ([.[] | "- `\(.path):\(.start_line // .line)-\(.line)` — \(.body // "")"] | join("\n"))' <<<"$moved")
    body="$body

$move_section"
  fi
  # Findings store: stable sigs (line-number-free, so deltas merge), append-only history.
  all_comments=$(jq -s 'add' <(printf '%s' "$kept") <(printf '%s' "$moved"))
  new_entries=$(jq -c -n --argjson comments "$all_comments" --arg body "$body" --argjson secs "$(jq '.sections // []' "$review_file")" '
    [($comments[] | {kind:"inline", path, side:(.side // "RIGHT"), line,
        anchored:(.dropReason == null), body:(.body // ""), suggestion:(.suggestion // null),
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
    entries=$(jq --argjson e "$e" --arg sig "$sig" --arg now "$now" --arg sha "$hs" \
      '. + [$e + {sig:$sig, sig_version:"1", firstSeen:$sha, lastSeen:$sha, status:"open", rationale:"", history:[{at:$now, event:"reported"}]} | del(.sigKey)]' <<<"$entries")
  done <<<"$new_entries"
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
                    inBounds:(if ($n.inBounds == null) then ($prev.inBounds // true) else $n.inBounds end)})
             end])
         + [.[] | . as $n | select([$o[] | select(.sig == $n.sig)] | length == 0)])' <<<"$entries")
  fi
  # sig_version backfill: entries that predate versioning (or arrive without one)
  # get "1" — never clobber a version a newer normalization stamped.
  entries=$(jq 'map(if has("sig_version") then . else . + {sig_version:"1"} end)' <<<"$entries")
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
  done < <(jq -c '.triage // [] | .[]' "$review_file")
  echo "$entries" | jq '.' > "$findings_file"
  echo "» findings: $(jq 'length' <<<"$entries") sigs -> $findings_file"
  jq -r '.[] | "\(.sig[0:8])  \(.status)  \(.anchored)  \(.path // .heading // "?")"' <<<"$entries" | sed 's/^/  /'
  n_open=$(jq '[.[] | select(.status == "open")] | length' <<<"$entries")
  n_def=$(jq '[.[] | select(.status == "deferred")] | length' <<<"$entries")
  n_addr=$(jq '[.[] | select(.status == "addressed")] | length' <<<"$entries")
  n_term=$(jq '[.[] | select(.status == "wont-fix" or .status == "false-positive")] | length' <<<"$entries")
  marker="plain"
  if printf '%s' "$body" | grep -qE '^> ✅|^> ℹ️|^> \[!IMPORTANT\]|^> \[!CAUTION\]'; then marker="verdict"; fi
  hunk_n=$(wc -l < "$dir/hunks.tsv" | tr -d ' ')
  if (( hunk_n == 0 )); then warn "no hunks indexed — anchors unchecked, everything kept"; fi
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
  else
    echo "» panel: none pinned (set REVIEW_PANEL or .review/panel for comparable, cost-capped runs)"
  fi
  if [[ -f .review/learnings.md ]]; then
    echo "» learnings: .review/learnings.md ($(wc -l < .review/learnings.md | tr -d ' ') lines — read headings first, not the full file)"
  fi
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

if [[ -z "$cmd" ]]; then
  usage >&2; exit 2
fi
if [[ "$cmd" == "trend" ]] && [[ -z "$repo" ]]; then
  usage >&2; exit 2
fi
if [[ "$cmd" != "check-tree" && "$cmd" != "doctor" && "$cmd" != "trend" ]] && [[ -z "$repo" || -z "$pr" ]]; then
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

check_diff_size() { # $1 = full.diff path
  local f="$1" bytes
  bytes=$(wc -c < "$f" | tr -d ' ')
  case "$bytes" in ''|*[!0-9]*) bytes="0";; esac
  if (( bytes > FETCH_MAX_BYTES )); then
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
    fail "diff-too-large" "diff $bytes bytes exceeds cap $FETCH_MAX_BYTES bytes — split the range (fetch-local --range/--base, fetch --base-sha) or narrow with a paths filter, then re-fetch"
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
  local inc_base="$1" files sens_file db
  files=$(jq '.files | length' "$dir/PR.json" 2>/dev/null || echo "?")
  sens_file="null"; [[ -f "$dir/sensitive.txt" ]] && sens_file="$dir/sensitive.txt"
  db=$(wc -c < "$dir/full.diff" 2>/dev/null | tr -d ' ' || true)
  case "$db" in ''|*[!0-9]*) db="0";; esac
  echo "done: $dir/PR.json $dir/full.diff $dir/toc.txt $dir/roles.txt ($files files)"
    echo "next: feed SKILL.md + prompts/system.md + prompts/review.md + $dir/PR.json + $dir/full.diff (+ $dir/roles.txt) to any agent; ask it to write $dir/review.json"
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg d "$dir" --arg hs "$(pr_field "$dir" head_sha)" \
        --arg bs "$(pr_field "$dir" base_sha)" --argjson files "$(jq '.files | length' "$dir/PR.json")" \
        --argjson adds "$(jq '.additions // 0' "$dir/PR.json")" --argjson dels "$(jq '.deletions // 0' "$dir/PR.json")" \
        --argjson hunks "$({ grep '^@@' "$dir/full.diff" || true; } | wc -l | tr -d ' ')" \
        --argjson db "$db" \
        --argjson omitted "$(to_json_array "$omitted")" \
        --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
        --arg inc "$inc_base" --arg sens "$sens_file" --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$(now_utc)" \
        --argjson estimate "${estimate_json:-null}" --argjson budget "${budget_json:-null}" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
          next:"feed SKILL.md + prompts/system.md + prompts/review.md + \($d)/PR.json + full.diff to any agent; ask it to write \($d)/review.json",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,head_sha:$hs,base_sha:$bs,files:$files,additions:$adds,deletions:$dels,
          hunks:$hunks,omitted:$omitted,diff_bytes:$db,panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
          artifacts:{"PR.json":"\($d)/PR.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","reviews.json":"\($d)/reviews.json","inline.json":"\($d)/inline.json","sensitive.txt":(if $sens=="null" then null else $sens end),"incremental.diff":(if $inc=="null" then null else "\($d)/incremental.diff" end)},
          incremental_base:(if $inc=="null" then null else $inc end)}
          + (if $estimate == null then {} else {estimate:$estimate} end)
          + (if $budget == null then {} else {budget:$budget} end),error:null}' >&9
    fi
  exit 0
}

if [[ "$cmd" == "fetch" ]]; then
  base_sha=""; provider="github"; budget_consent="false"
  while [[ $# -gt 0 ]]; do case "$1" in
    --base-sha|--since) base_sha="${2:-}"; shift 2;;
    --provider) provider="${2:-github}"; shift 2;;
    --consent-over-budget) budget_consent="true"; shift;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for fetch (want --base-sha|--since <sha>, --provider <name>, --consent-over-budget, --json)" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  case "$provider" in github|gitlab|gitcafe|forgejo|bitbucket|azure) ;;
    *) fail "bad-provider" "unknown --provider '$provider' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)";; esac
  budget_gate "$budget_consent"
  dir=".review/$pr"; mkdir -p "$dir"
  write_budget_receipt "$dir"
  if [[ "$provider" != "github" ]]; then
    [[ -n "$base_sha" ]] && fail "bad-flags" "--base-sha/--since is GitHub-only for now (providers have no incremental re-review yet)"
    fetch_provider_bundle
    check_diff_size "$dir/full.diff"
    write_toc_roles "$dir" "# TOC for $provider #$pr ($repo) — read ranges as coverage checklist"
    emit_context "$dir"
    ensure_store "$dir"
    write_sensitive_manifest "$dir" "$(jq -r '.files[] | .path, (.previous_filename // empty)' "$dir/PR.json" 2>/dev/null || true)"
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
  write_toc_roles "$dir" "# TOC for PR #$pr ($repo) — read ranges as coverage checklist"
  emit_context "$dir"
  ensure_store "$dir"
  write_sensitive_manifest "$dir" "$(jq -r '.files[] | .path, (.previous_filename // empty)' "$dir/PR.json" 2>/dev/null || true)"
  echo "» reviews + inline comments"
  gh api "repos/$repo/pulls/$pr/reviews" --paginate > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
  gh api "repos/$repo/pulls/$pr/comments" --paginate > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
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
  staged="false"; range=""; base_ref=""; stash_n=""; json_mode="false"; budget_consent="false"; paths=()
  while [[ $# -gt 0 ]]; do case "$1" in
    --staged) staged="true"; shift;;
    --range) range="${2:-}"; shift 2;;
    --base) base_ref="${2:-}"; shift 2;;
    --stash) if [[ -n "${2:-}" && "${2:-}" != -* ]]; then stash_n="$2"; shift 2; else stash_n="0"; shift; fi;;
    --consent-over-budget) budget_consent="true"; shift;;
    --json) json_mode="true"; shift;;
    --) shift; while [[ $# -gt 0 ]]; do paths+=("$1"); shift; done;;
    -*) echo "unknown flag '$1' for fetch-local (want --staged, --range <A..B>, --base <ref>, --stash [n], --consent-over-budget, -- <paths>, --json)" >&2; exit 2;;
    *) paths+=("$1"); shift;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  echo "next: feed SKILL.md + prompts/system.md + prompts/review.md + $dir/source.json + full.diff to any agent; ask it to write $dir/review.json"
  if [[ "$json_mode" == "true" ]]; then
    sens_file="null"; [[ -f "$dir/sensitive.txt" ]] && sens_file="$dir/sensitive.txt"
    db=$(wc -c < "$dir/full.diff" 2>/dev/null | tr -d ' ' || true)
    case "$db" in ''|*[!0-9]*) db="0";; esac
    jq -n --arg d "$dir" --arg hs "$head_sha" --arg bs "$base_sha" --argjson files "$total" \
      --argjson adds "$adds" --argjson dels "$dels" \
      --argjson hunks "$(grep -c '^@@' "$dir/full.diff" || true)" \
      --argjson db "$db" \
      --argjson omitted "$(to_json_array "$omitted")" \
      --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
      --arg sens "$sens_file" \
      --arg v "$cmd" --arg r "$repo" --arg p "$slug" --arg t "$now" --arg kind "$kind" \
      --argjson estimate "${estimate_json:-null}" --argjson budget "${budget_json:-null}" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:("feed SKILL.md + prompts/system.md + prompts/review.md + \($d)/source.json + full.diff to any agent; ask it to write \($d)/review.json"),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,kind:$kind,head_sha:$hs,base_sha:$bs,
        files:$files,additions:$adds,deletions:$dels,hunks:$hunks,omitted:$omitted,diff_bytes:$db,
        panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
        artifacts:{"source.json":"\($d)/source.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","sensitive.txt":(if $sens=="null" then null else $sens end)}}
        + (if $estimate == null then {} else {estimate:$estimate} end)
        + (if $budget == null then {} else {budget:$budget} end),error:null}' >&9
  fi
  exit 0
fi

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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  # Stale-SHA guard: anchors validate against the FETCHED hunks — if HEAD moved since
  # fetch, say so in the body and treat anchors as potentially stale. Lookup failure
  # degrades to a warning, never a refusal.
  fetched_sha=$(pr_field "$dir" head_sha)
  live_sha=$(gh api "repos/$repo/pulls/$pr" --jq '.head.sha' 2>/dev/null || true)
  if [[ -n "$live_sha" && -n "$fetched_sha" && "$live_sha" != "$fetched_sha" ]]; then
    warn "HEAD moved since fetch (fetched ${fetched_sha:0:7}, live ${live_sha:0:7}) — anchors potentially stale"
    body="> ⚠️ Note: commits landed after this review's fetch (fetched \`${fetched_sha:0:7}\`, live \`${live_sha:0:7}\`); treat line anchors as potentially stale.

$body"
  elif [[ -z "$live_sha" ]]; then
    warn "live SHA lookup failed — proceeding on fetched hunks"
  fi
  build_hunks_tsv "$dir/full.diff" "$dir/hunks.tsv"
  # approved flag: CLI flag wins, else review.json approved. Binding decided at the gate below.
  json_approved=$(jq -r '.approved // false' "$review_file")
  if [[ "$approve" == "false" && "$json_approved" == "true" ]]; then approve="true"; fi
  event="COMMENT"
  commit_id=$(pr_field "$dir" head_sha)
  now=$(now_utc)
  findings_file="$dir/findings.json"; [[ "$dry_run" == "true" ]] && findings_file="$dir/findings.dryrun.json"
  validate_and_merge "$review_file" "$dir" "$commit_id" "$now" "$findings_file"
  # Binding gate: binding verdicts need --bind (else COMMENT); APPROVE needs zero
  # open AND zero deferred findings unless --force. REQUEST_CHANGES with findings is normal.
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
      event="APPROVE"
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
    if [[ "$json_mode" == "true" ]]; then
      phash=$(printf '%s' "$payload" | shasum -a 1 | cut -d' ' -f1)
      jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg e "$event" --arg c "$commit_id" \
        --argjson k "$n_kept" --argjson m "$n_moved" --argjson o "$n_open" --argjson d "$n_def" --argjson a "$n_addr" \
        --argjson items "$(jq '[.[] | {sig, status, anchored, locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)}]' <<<"$entries")" \
        --arg h "$phash" --argjson emb "$embedded" --arg mk "$marker" --arg f "$findings_file" \
        --argjson conf "$rev_confidence" --argjson sec "$rev_security" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"payload NOT posted (dry-run)",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dry_run:true,event:$e,commit_id:$c,body:{marker:$mk},anchors:{kept:$k,moved:$m},
          suggestions:{embedded:$emb},findings:{open:$o,deferred:$d,addressed:$a,file:$f},confidence:$conf,security_summary:$sec,
          items:$items,payload_hash:$h},error:null}' >&9
    else
      echo "dry-run: payload NOT posted. body chars: $(printf '%s' "$body" | wc -c | tr -d ' '), payload comment keys: $(jq -r '.[0] // {} | keys_unsorted | join(",")' <<<"$post_comments")"
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  build_hunks_tsv "$dir/full.diff" "$dir/hunks.tsv"
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  build_hunks_tsv "$dir/full.diff" "$dir/hunks.tsv"
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  jq --arg s "$tfull" --arg st "$tstatus" --arg rat "$rat_out" --arg now "$now" \
    'map(if .sig == $s then . + {status:$st, rationale:$rat, triagedAt:$now, triagedBy:"human", history:(.history + [{at:$now, event:"triaged", actor:"human", note:$rat}])} else . end)' \
    "$dir/findings.json" > "$dir/findings.json.tmp" && mv "$dir/findings.json.tmp" "$dir/findings.json"
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
  status_f="all"; kind_f="all"; output=""; sarif=""; cost="false"; check_sev=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --status) status_f="${2:-all}"; case "$status_f" in all|open|addressed|deferred|wont-fix|false-positive) ;; *) echo "unknown --status '$status_f' for report (want all|open|addressed|deferred|wont-fix|false-positive)" >&2; exit 2;; esac; shift 2;;
    --kind) kind_f="${2:-all}"; case "$kind_f" in all|inline|section) ;; *) echo "unknown --kind '$kind_f' for report (want all|inline|section)" >&2; exit 2;; esac; shift 2;;
    --output) output="${2:-}"; shift 2;;
    --sarif) sarif="${2:-}"; shift 2;;
    --cost) cost="true"; shift;;
    --check) check_sev="${2:-}"; case "$check_sev" in critical|important|informational) ;; *) echo "unknown --check tier '$check_sev' for report (want critical|important|informational)" >&2; exit 2;; esac; check_gate="true"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for report (want --status, --kind, --output, --sarif, --cost, --check, --json)" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  resolve_bundle_dir "$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post or record first)"
  jq -e 'type == "array"' "$dir/findings.json" >/dev/null 2>&1 \
    || fail "corrupt-store" "findings store $dir/findings.json is unreadable or not a findings array (re-run post/record to rebuild, or restore the file)"
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
    if [[ -f "$dir/cost.json" ]]; then
      cost_json=$(jq '
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
        | .total_tokens = (.input_tokens + .output_tokens)' "$dir/cost.json" 2>/dev/null) \
        || { warn "cost.json unreadable — spend unrecorded for this rollup"; cost_json="$cost_zeros"; }
    else
      cost_json="$cost_zeros"
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
  if [[ -n "$output" ]]; then
    { echo "# Review report — $repo#$pr"; echo
      echo "<!-- report-machine:start -->"
      echo "## Counts — open $n_all_open · head ${head_sha:0:7}"
      jq -r '.[] | "\(.rank). [\(.status)] \(.locator) (\(.sig[0:8]), \(.rigor // "?")) — \((.body // "" | gsub("\n"; " ") | .[0:200]))"' <<<"$ranked"
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
      --argjson cost "$cost_json" \
      --arg o "$output" --arg sf "$status_f" --arg kf "$kind_f" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $o == "" then (if ([$items[] | select(.status=="open")] | length) > 0 then "address open threads" else "none open — nothing to address" end) else "wrote \($o)" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:({generated_at:$t,dir:$d,head:{fetched:$hs,base:$bs,live:null,stale:false},
        verdict:{open:([$items[] | select(.status=="open")] | length),deferred:([$items[] | select(.status=="deferred")] | length),addressed:([$items[] | select(.status=="addressed")] | length),terminal:([$items[] | select(.status=="wont-fix" or .status=="false-positive")] | length)},
        counts:$counts,panel:{source:(if $psrc=="" then null else $psrc end),members:($panel // [])},
        patch:$patch,output:(if $o=="" then null else $o end),filters:{status:$sf,kind:$kf},items:$items}
        + (if $cost == null then {} else {cost:$cost} end)),error:null}' >&9
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
  # not narrow the gate). Output is identical with or without --check — only
  # the exit differs, mirroring ready --check. The store parsed above, so a
  # non-numeric count here is a tool error, never a pass.
  if [[ "$check_gate" == "true" ]]; then
    n_hit=$(jq --arg t "$check_sev" '
      ({"critical":0,"important":1,"informational":2}[$t]) as $th
      | [ .[] | select(.status == "open")
        | ({"critical":0,"important":1,"informational":2}[.severity // ""] // 3) as $r
        | select($r <= $th) ] | length' "$dir/findings.json")
    case "$n_hit" in ''|*[!0-9]*) fail "report-failed" "could not evaluate --check gate over $dir/findings.json";; esac
    if (( n_hit > 0 )); then
      echo "check: $n_hit open finding(s) at-or-above $check_sev — gate fails" >&2
      exit 1
    fi
    echo "check: no open findings at-or-above $check_sev — gate passes" >&2
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
if [[ "$cmd" == "check-tree" ]]; then
  expect=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --expect) expect="${2:-}"; shift 2;;
    *) echo "unknown flag '$1' for check-tree (want --expect <prefix,...>)" >&2; exit 2;;
  esac; done
  porcelain=$(git status --porcelain 2>/dev/null) || { echo "check-tree: not a git repo" >&2; exit 1; }
  # The skill's own .review/ scratch never trips the gate (fetch writes it by design).
  own=$(grep -c '^.. \.review/' <<<"$porcelain" || true)
  porcelain=$(grep -v '^.. \.review/' <<<"$porcelain" || true)
  scratch=""; (( own > 0 )) && scratch=" ($own .review/ scratch line(s) ignored)" || true
  if [[ -z "$porcelain" ]]; then echo "check-tree: clean$scratch"; exit 0; fi
  if [[ -z "$expect" ]]; then
    echo "check-tree: dirty worktree — commit or stash first:"; echo "$porcelain"; exit 1
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
  if [[ -n "$stray" ]]; then echo "check-tree: unexpected dirt:"; printf '%s' "$stray"; exit 1; fi
  echo "check-tree: only expected files dirty"
  exit 0
fi

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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  resolve_bundle_dir "$pr"
  [[ -f "$dir/patch.json" ]] || fail "missing-patch" "missing $dir/patch.json (nothing claimed to verify)"
  now=$(now_utc)
  vcmd=$(jq -r '.verifyCmd // empty' "$dir/patch.json")
  [[ -z "$vcmd" ]] && fail "no-verify-cmd" "patch.json carries no verifyCmd to execute"
  if ! [[ "$timeout_s" =~ ^[0-9]+$ ]] || (( timeout_s <= 0 )); then fail "bad-timeout" "timeout must be a positive number of seconds, got '$timeout_s'"; fi
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for calibrate (want --json)" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  resolve_bundle_dir "$pr"
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
         outcome:$o.outcome, detail:$o.detail}]' "$dir/findings.json")
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
  jq -r '.[] | "\(.sig[0:8]) [\(.outcome)] \(.locator) — \(.detail)"' <<<"$outcomes"
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
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    *) echo "unknown flag '$1' for trend (want --json)" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
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
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg t "$now" --argjson roll "$rollup" --argjson rows "$rows" \
      '{schema_version:"1",verb:$v,repo:$r,pr:null,at:$t,
        next:(if $roll.bundles > 0 then "calibrate recent bundles for per-model tier precision" else "no reviewed bundles yet — fetch + post/record first" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:({generated_at:$t} + $roll + {per_bundle:$rows}),error:null}' >&9
  else
    if (( n_bundles == 0 )); then
      echo "trend: $repo — no reviewed bundles (no .review/*/findings.json yet)"
    else
      echo "trend: $repo — $n_bundles bundle(s), $(jq -r '.findings' <<<"$rollup") findings (open=$(jq -r '.open' <<<"$rollup") addressed=$(jq -r '.addressed' <<<"$rollup") closed=$(jq -r '.closed' <<<"$rollup"))"
      jq -r '.periods[] | "\(.period // "undated")  bundles=\(.bundles) findings=\(.findings) open=\(.open) addressed=\(.addressed) closed=\(.closed) tiers critical=\(.by_tier.critical) important=\(.by_tier.important) informational=\(.by_tier.informational) untagged=\(.by_tier.untagged)"' <<<"$rollup"
      echo "tiers: $(jq -r '"critical=\(.by_tier.critical) important=\(.by_tier.important) informational=\(.by_tier.informational) untagged=\(.by_tier.untagged)"' <<<"$rollup")"
      echo "status: $(jq -r '"open=\(.by_status.open) addressed=\(.by_status.addressed) wont-fix=\(.by_status."wont-fix") false-positive=\(.by_status."false-positive") deferred=\(.by_status.deferred)"' <<<"$rollup")"
    fi
  fi
  exit 0
fi

echo "unknown cmd '$cmd' (want fetch|fetch-local|record|adopt|post|triage|report|next|show|check-tree|verify|doctor|calibrate|ready|trend)" >&2; exit 2
