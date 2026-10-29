#!/usr/bin/env bash
# review.sh — local PR fetch/post/triage/report using gh CLI only.
# Verbs: fetch (network+files), post (validates, merges store, posts once),
#        triage (local store mutation, no network), report (pure read, no network).
set -euo pipefail

usage() {
  cat <<'EOF'
usage: review.sh fetch|post|triage|report|next|show|verify|doctor|calibrate|fetch-local|record|adopt [opts]
  fetch <owner/repo> <pr> [--base-sha|--since <sha>] [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] [--json]
    write .review/<pr>/{PR.json,full.diff,toc.txt,roles.txt,reviews.json,inline.json[,incremental.diff]} (PR.json normalized for non-github providers)
  fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]
        [--stash [n]] [-- paths...] [--json]
    local review bundle from git (no GitHub, no PR): writes
    .review/local-<name>/{source.json,full.diff,toc.txt,roles.txt}.
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
        [--output <path>] [--sarif <path>] [--json]
    pure read over the store: ranked findings + verdict + counts. No network.
    --sarif writes a SARIF 2.1.0 file (severity→level, sig as fingerprint) for scanners/CI.
  next <owner/repo> <pr> [--status open] [--json]
    top-ranked actionable finding + the command to act on it. Pure read.
  show <owner/repo> <pr> --sig <sig> [--json]
    one full finding record. Pure read.
  check-tree [--expect <prefix,...>]
    refuse on unexpected dirt (own .review/ scratch ignored). No network. Machine clean-tree gate for mutating modes.
  verify <owner/repo> <pr> [--timeout <secs, default 300>] [--json]
    execute patch.json verifyCmd with a watchdog, record exit + tail into the patch record.
    Runs the agent's claimed proof — exit code becomes machine fact. No GitHub I/O.
  doctor [--json]
    preflight the harness: gh auth + scopes, jq, hash tooling, git, API reachability.
    Exit 0 only when everything the skill needs is present. No repo/PR args, no network writes.
  calibrate <owner/repo> <pr> [--json]
    join findings against live ground truth (thread states + files changed since the
    review head). Read-only; reports outcomes + tier precision, never auto-tunes.
  --json on fetch/post/triage/report/next/show/verify/doctor/calibrate: stdout is exactly one JSON envelope (schema_version 1);
  all human progress goes to stderr. Errors emit {data:null,error:{code,message}} non-zero.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then usage; exit 0; fi
if [[ "${1:-}" == "--version" ]]; then cat "$(dirname "${BASH_SOURCE[0]:-$0}")/../VERSION" 2>/dev/null || echo "0.0.0-unknown"; exit 0; fi
cmd="${1:-}"; shift $(( $# > 0 ? 1 : 0 ))
repo=""; pr=""
if [[ "$cmd" != "check-tree" && "$cmd" != "doctor" ]]; then
  repo="${1:-}"; pr="${2:-}"; shift $(( $# >= 2 ? 2 : $# ))
fi

need() { command -v "$1" >/dev/null || { echo "missing: $1" >&2; exit 1; }; }
need gh; need jq; need shasum
warn() { echo "warn: $*" >&2; WARNINGS_JSON=$(jq --arg w "$*" '. + [$w]' <<<"$WARNINGS_JSON"); }
WARNINGS_JSON="[]"; export WARNINGS_JSON
json_mode="false"

# Typed failure: JSON envelope on stdout (original) in --json mode, else stderr text.
fail() {
  local code="$1"; local msg="$2"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg c "$code" --arg m "$msg" --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"",warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:null,error:{code:$c,message:$m}}' >&9
  else
    echo "$msg" >&2
  fi
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
      (($ch.changes // $ch // []) | if type=="array" then . else [] end) as $files
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
normalize_gitcafe() { # $1 = meta, $2 = changes items
  jq -n --argjson m "$1" --argjson it "$2" '
      (($it.items // $it) | if type=="array" then . else [] end) as $files
      | {schema_version:"1", provider:"gitcafe",
         number:$m.number, title:($m.title // ""), body:($m.description // ""),
         base_ref:($m.targetBranch // ""), head_ref:($m.sourceBranch // ""),
         base_sha:($m.observedBaseOid // ""), head_sha:($m.headOid // ""),
         author:($m.author.handle // $m.author.login // $m.author.displayName // ""),
         url:null,
         state:(if $m.state == "open" or $m.state == "closed" or $m.state == "merged" then $m.state else "unknown" end),
         draft:($m.draft // false),
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
      | ([ $files[] | (.item.path // "") as $p
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
    changes=$(glab api "projects/$pid/merge_requests/$pr/changes" --paginate 2>/dev/null || echo '{"changes":[]}')
    echo "» glab mr diff -> $dir/full.diff"
    glab mr diff "$pr" -R "$repo" --raw > "$dir/full.diff" 2>"$dir/.diff.err" || fail "fetch-failed" "glab mr diff failed: $(head -c 200 "$dir/.diff.err")"
    notes=$(glab mr note list "$pr" -R "$repo" -F json 2>/dev/null || echo "[]")
    nn=$(jq 'if type=="array" then . else (.notes // .discussions // []) end' <<<"$notes" 2>/dev/null || echo "[]")
    printf '%s' "$nn" | jq '[.[] | select(.type == "DiffNote")]' > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
    printf '%s' "$nn" | jq '[.[] | select(.type != "DiffNote")]' > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
    ns=$(diff_numstat_map "$dir/full.diff")
    normalize_gitlab "$meta" "$changes" "$ns" > "$dir/PR.json" || fail "fetch-failed" "could not normalize GitLab MR $pr"
    ;;
  gitcafe)
    need cafe
    meta=$(cafe pr view "$pr" -R "$repo" --json 2>"$dir/.meta.err") || fail "fetch-failed" "cafe pr view failed: $(head -c 200 "$dir/.meta.err") (auth? number?)"
    items=$(cafe pr diff "$pr" -R "$repo" --patch --json 2>"$dir/.diff.err") || fail "fetch-failed" "cafe pr diff failed: $(head -c 200 "$dir/.diff.err")"
    jq -r '[(.items // . // [])[] | "diff --git a/\(.path) b/\(.path)", (if ((.patch // "") | contains("@@")) then .patch else "" end)] | join("\n")' <<<"$items" > "$dir/full.diff"
    grep -q '^@@' "$dir/full.diff" 2>/dev/null || warn "no hunks indexed from cafe patch — anchors unchecked, everything kept"
    revs=$(cafe pr reviews "$pr" -R "$repo" --json 2>/dev/null || echo "[]")
    jq 'if type=="array" then . else (.reviews // []) end' <<<"$revs" > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
    cmts=$(cafe pr comments "$pr" -R "$repo" --filter all --json 2>/dev/null || echo "[]")
    jq 'if type=="array" then . else (.comments // []) end' <<<"$cmts" > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
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
      page=$(curl -sf "${auth[@]}" "$url" 2>/dev/null || echo '{}')
      ds=$(jq -n --argjson a "$ds" --argjson b "$(jq '.values // []' <<<"$page")" '$a + $b')
      url=$(jq -r '.next // empty' <<<"$page")
    done
    cm="[]"; url="$base/comments?pagelen=100"
    while [[ -n "$url" && "$url" != "null" ]]; do
      page=$(curl -sf "${auth[@]}" "$url" 2>/dev/null || echo '{}')
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
    git init -q "$tmp" && git -C "$tmp" -c http.extraHeader="AUTHORIZATION: Basic $enc" fetch -q "$remote" "$hs" "$bs" 2>"$dir/.diff.err"       || fail "fetch-failed" "azure git fetch failed: $(head -c 200 "$dir/.diff.err")"
    git -C "$tmp" diff "$bs" "$hs" > "$dir/full.diff" 2>/dev/null || fail "fetch-failed" "azure diff failed"
    rm -rf "$tmp"; trap - EXIT
    threads=$(az rest --method get --url "$org/$project/_apis/git/repositories/$rname/pullRequests/$pr/threads?api-version=7.1" --query value -o json 2>/dev/null || echo "[]")
    jq '[.[] | select(.threadContext == null)]' <<<"$threads" > "$dir/reviews.json" 2>/dev/null || echo "[]" > "$dir/reviews.json"
    jq '[.[] | select(.threadContext != null)]' <<<"$threads" > "$dir/inline.json" 2>/dev/null || echo "[]" > "$dir/inline.json"
    iid=$(az rest --method get --url "$org/$project/_apis/git/repositories/$rname/pullRequests/$pr/iterations?api-version=7.1" --query "max_by(value, &id).id" -o tsv 2>/dev/null || true)
    chg=$(if [[ -n "$iid" ]]; then az rest --method get --url "$org/$project/_apis/git/repositories/$rname/pullRequests/$pr/iterations/$iid/changes?api-version=7.1" --query value -o json 2>/dev/null || echo "[]"; else echo "[]"; fi)
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

if [[ -z "$cmd" ]]; then
  usage >&2; exit 2
fi
if [[ "$cmd" != "check-tree" && "$cmd" != "doctor" ]] && [[ -z "$repo" || -z "$pr" ]]; then
  usage >&2; exit 2
fi

# ---------------------------------------------------------------- fetch ---
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
    (grep '^diff --git' "$dir/full.diff" || true) | sed 's/.* b\///' | sort -u > "$dir/.difffiles"
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
  local inc_base="$1" files
  files=$(jq '.files | length' "$dir/PR.json" 2>/dev/null || echo "?")
  echo "done: $dir/PR.json $dir/full.diff $dir/toc.txt $dir/roles.txt ($files files)"
    echo "next: feed SKILL.md + prompts/system.md + prompts/review.md + $dir/PR.json + $dir/full.diff (+ $dir/roles.txt) to any agent; ask it to write $dir/review.json"
    if [[ "$json_mode" == "true" ]]; then
      jq -n --arg d "$dir" --arg hs "$(jq -r '.headRefOid // empty' "$dir/PR.json")" \
        --arg bs "$(jq -r '.baseRefOid // empty' "$dir/PR.json")" --argjson files "$(jq '.files | length' "$dir/PR.json")" \
        --argjson adds "$(jq '.additions // 0' "$dir/PR.json")" --argjson dels "$(jq '.deletions // 0' "$dir/PR.json")" \
        --argjson hunks "$({ grep '^@@' "$dir/full.diff" || true; } | wc -l | tr -d ' ')" \
        --argjson omitted "$(to_json_array "$omitted")" \
        --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
        --arg inc "$inc_base" --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$(now_utc)" \
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
          next:"feed SKILL.md + prompts/system.md + prompts/review.md + \($d)/PR.json + full.diff to any agent; ask it to write \($d)/review.json",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,head_sha:$hs,base_sha:$bs,files:$files,additions:$adds,deletions:$dels,
          hunks:$hunks,omitted:$omitted,panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
          artifacts:{"PR.json":"\($d)/PR.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt","reviews.json":"\($d)/reviews.json","inline.json":"\($d)/inline.json","incremental.diff":(if $inc=="null" then null else "\($d)/incremental.diff" end)},
          incremental_base:(if $inc=="null" then null else $inc end)},error:null}' >&9
    fi
  exit 0
}

if [[ "$cmd" == "fetch" ]]; then
  base_sha=""; provider="github"
  while [[ $# -gt 0 ]]; do case "$1" in
    --base-sha|--since) base_sha="${2:-}"; shift 2;;
    --provider) provider="${2:-github}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  case "$provider" in github|gitlab|gitcafe|forgejo|bitbucket|azure) ;;
    *) fail "bad-provider" "unknown --provider '$provider' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)";; esac
  dir=".review/$pr"; mkdir -p "$dir"
  if [[ "$provider" != "github" ]]; then
    [[ -n "$base_sha" ]] && fail "bad-flags" "--base-sha/--since is GitHub-only for now (providers have no incremental re-review yet)"
    fetch_provider_bundle
    write_toc_roles "$dir" "# TOC for $provider #$pr ($repo) — read ranges as coverage checklist"
    emit_context "$dir"
    ensure_store "$dir"
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
  write_toc_roles "$dir" "# TOC for PR #$pr ($repo) — read ranges as coverage checklist"
  emit_context "$dir"
  ensure_store "$dir"
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
#             [--stash [n]] [-- paths...] [--json]
# Modes: worktree-vs-HEAD (default) | staged | range | branch-vs-base | stash.
# Writes .review/local-<name>/ (source.json, full.diff, toc.txt, roles.txt).
if [[ "$cmd" == "fetch-local" ]]; then
  staged="false"; range=""; base_ref=""; stash_n=""; json_mode="false"; paths=()
  while [[ $# -gt 0 ]]; do case "$1" in
    --staged) staged="true"; shift;;
    --range) range="${2:-}"; shift 2;;
    --base) base_ref="${2:-}"; shift 2;;
    --stash) if [[ -n "${2:-}" && "${2:-}" != -* ]]; then stash_n="$2"; shift 2; else stash_n="0"; shift; fi;;
    --json) json_mode="true"; shift;;
    --) shift; while [[ $# -gt 0 ]]; do paths+=("$1"); shift; done;;
    -*) echo "unknown flag $1" >&2; exit 2;;
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
  mkdir -p "$dir"
  "${diff_cmd[@]}" > "$dir/full.diff" 2>"$dir/.diff.err" || fail "diff-failed" "git diff failed: $(head -c 200 "$dir/.diff.err")"
  if ! grep -q '^@@' "$dir/full.diff" 2>/dev/null; then
    rm -f "$dir/full.diff"
    rmdir "$dir" 2>/dev/null || true
    echo "nothing to review: empty diff (no hunks)"
    exit 0
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
  total=$(printf '%s\n' "$file_list" | grep -c . || echo 0)
  adds=$(printf '%s\n' "$numstat" | awk '$1 ~ /^[0-9]+$/ {a+=$1} END {print a+0}')
  dels=$(printf '%s\n' "$numstat" | awk '$2 ~ /^[0-9]+$/ {d+=$2} END {print d+0}')
  { echo "# TOC for local $slug ($repo) — read ranges as coverage checklist"; echo
    grep -n "^diff --git" "$dir/full.diff" || echo "(no diff headers — empty diff?)"
    echo; echo "## stat"
    printf '%s\n' "$numstat" | awk 'NF==3 {print $3" (+"$1"/-"$2")"}'
    echo; echo "## coverage manifest"
    (grep '^diff --git' "$dir/full.diff" || true) | sed 's/.* b\///' | sort -u > "$dir/.difffiles"
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
  tool_version=$(cat "$(dirname "${BASH_SOURCE[0]:-$0}")/../VERSION" 2>/dev/null || echo "0.0.0-unknown")
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
    jq -n --arg d "$dir" --arg hs "$head_sha" --arg bs "$base_sha" --argjson files "$total" \
      --argjson adds "$adds" --argjson dels "$dels" \
      --argjson hunks "$(grep -c '^@@' "$dir/full.diff" || true)" \
      --argjson omitted "$(to_json_array "$omitted")" \
      --arg psrc "$panel_src" --argjson members "$(to_json_array "$panel_list")" \
      --arg v "$cmd" --arg r "$repo" --arg p "$slug" --arg t "$now" --arg kind "$kind" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:("feed SKILL.md + prompts/system.md + prompts/review.md + \($d)/source.json + full.diff to any agent; ask it to write \($d)/review.json"),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,kind:$kind,head_sha:$hs,base_sha:$bs,
        files:$files,additions:$adds,deletions:$dels,hunks:$hunks,omitted:$omitted,
        panel:{source:(if $psrc=="" then null else $psrc end),members:$members},
        artifacts:{"source.json":"\($d)/source.json","full.diff":"\($d)/full.diff","toc.txt":"\($d)/toc.txt","roles.txt":"\($d)/roles.txt"}},error:null}' >&9
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
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
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
        '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,next:"payload NOT posted (dry-run)",
          warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dry_run:true,event:$e,commit_id:$c,body:{marker:$mk},anchors:{kept:$k,moved:$m},
          suggestions:{embedded:$emb},findings:{open:$o,deferred:$d,addressed:$a,file:$f},
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
    '{schema_version:"1",review:$rv,head:$c,posted_at:$t}' > "$dir/post.json"
  if [[ "$json_mode" == "true" ]]; then
    posted_json="$posted"
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg e "$event" --arg c "$commit_id" \
      --argjson k "$n_kept" --argjson m "$n_moved" --argjson o "$n_open" --argjson d "$n_def" --argjson a "$n_addr" \
      --argjson items "$(jq '[.[] | {sig, status, anchored, locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)}]' <<<"$entries")" \
      --arg h "$phash" --argjson emb "$embedded" --arg mk "$marker" --argjson rv "$posted_json" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $o > 0 then "address open threads" else "none open — nothing to address" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dry_run:false,event:$e,commit_id:$c,body:{marker:$mk},anchors:{kept:$k,moved:$m},
        suggestions:{embedded:$emb},findings:{open:$o,deferred:$d,addressed:$a},items:$items,
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
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  case "$pr" in local-*) dir=".review/$pr"; prov="local";;
    *) dir=".review/$pr"
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
    '{schema_version:"1",status:$st,head:$h,diff_sha256:$dsha,recorded_at:$t,findings:$f,open:$o}' > "$dir/record.json"
  echo "recorded: $dir/findings.json ($n_open open) + $dir/record.json — report/triage read it; post stays refused (no PR target)"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --arg d "$dir" \
      --argjson k "$n_kept" --argjson m "$n_moved" --argjson o "$n_open" --argjson dl "$n_def" --argjson a "$n_addr" \
      --argjson items "$(jq '[.[] | {sig, status, anchored, locator: (if .path then "\(.path)\(if .line then ":\(.line)" else "" end)" else (.heading // "?") end)}]' <<<"$entries")" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:"report or triage the local bundle; post stays refused (no PR target)",
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{dir:$d,anchors:{kept:$k,moved:$m},
        findings:{open:$o,deferred:$dl,addressed:$a,file:($d + "/findings.json")},items:$items},error:null}' >&9
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
    *) echo "unknown flag $1" >&2; exit 2;;
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
    if [[ -z "$p" || -z "$line" ]] || ! [[ "$line" =~ ^[0-9]+$ ]]; then printf '%s\n' "$e"; continue; fi
    key="$p"; [[ "$key" == a/* || "$key" == b/* ]] && key="${key:2}"
    if awk -F'\t' -v f="$key" -v a="$line" -v b="$line" '$1==f && $2=="NEW" && $3<=a && b<=$4 {found=1; exit} END{exit !found}' "$dir/hunks.tsv"; then
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
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  dir=".review/$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post first)"
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
  status_f="all"; kind_f="all"; output=""; sarif=""
  while [[ $# -gt 0 ]]; do case "$1" in
    --status) status_f="${2:-all}"; shift 2;;
    --kind) kind_f="${2:-all}"; shift 2;;
    --output) output="${2:-}"; shift 2;;
    --sarif) sarif="${2:-}"; shift 2;;
    --json) json_mode="true"; shift;;
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  case "$status_f" in all|open|addressed|deferred|wont-fix|false-positive) ;;
    *) fail "bad-status" "unknown --status '$status_f' (want all|open|addressed|deferred|wont-fix|false-positive)";; esac
  case "$kind_f" in all|inline|section) ;;
    *) fail "bad-kind" "unknown --kind '$kind_f' (want all|inline|section)";; esac
  dir=".review/$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post first)"
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
      --arg o "$output" --arg sf "$status_f" --arg kf "$kind_f" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $o == "" then (if ([$items[] | select(.status=="open")] | length) > 0 then "address open threads" else "none open — nothing to address" end) else "wrote \($o)" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{generated_at:$t,dir:$d,head:{fetched:$hs,base:$bs,live:null,stale:false},
        verdict:{open:([$items[] | select(.status=="open")] | length),deferred:([$items[] | select(.status=="deferred")] | length),addressed:([$items[] | select(.status=="addressed")] | length),terminal:([$items[] | select(.status=="wont-fix" or .status=="false-positive")] | length)},
        counts:$counts,panel:{source:(if $psrc=="" then null else $psrc end),members:($panel // [])},
        patch:$patch,output:(if $o=="" then null else $o end),filters:{status:$sf,kind:$kf},items:$items},error:null}' >&9
  else
    echo "report: $dir ($(jq 'length' <<<"$ranked") shown) open=$n_open head=${head_sha:0:7}"
    jq -r '.[] | "\(.rank). [\(.status)] \(.locator) (\(.sig[0:8]))"' <<<"$ranked"
    if [[ -n "$output" ]]; then echo "wrote $output"; else
      if (( n_open > 0 )); then echo "next: address open threads"; else echo "next: none open — nothing to address"; fi
    fi
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
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  now=$(now_utc)
  rep_out=$(bash "${BASH_SOURCE[0]:-$0}" report "$repo" "$pr" --status "$status_f" --json 2>/dev/null) || true
  if [[ -z "$rep_out" ]] || jq -e '.error != null' <<<"$rep_out" >/dev/null 2>&1; then
    if [[ "$json_mode" == "true" ]]; then
      if [[ -n "$rep_out" ]]; then echo "$rep_out" >&9; else fail "report-failed" "next: report produced no output"; fi
    else echo "next: cannot read store (run post first)" >&2; fi
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
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  dir=".review/$pr"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post first)"
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
    *) echo "unknown flag $1" >&2; exit 2;;
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
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  dir=".review/$pr"
  [[ -f "$dir/patch.json" ]] || fail "missing-patch" "missing $dir/patch.json (nothing claimed to verify)"
  now=$(now_utc)
  vcmd=$(jq -r '.verifyCmd // empty' "$dir/patch.json")
  [[ -z "$vcmd" ]] && fail "no-verify-cmd" "patch.json carries no verifyCmd to execute"
  if ! [[ "$timeout_s" =~ ^[0-9]+$ ]] || (( timeout_s <= 0 )); then fail "bad-timeout" "timeout must be a positive number of seconds"; fi
  echo "» verify: $vcmd (timeout ${timeout_s}s)"
  : > "$dir/.verify.out"; : > "$dir/.verify.timeout"
  bash -c "$vcmd" >"$dir/.verify.out" 2>&1 & vpid=$!
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
      kill -9 "$vpid" 2>/dev/null || true
      timed_out="true"; echo "timeout after ${timeout_s}s" > "$dir/.verify.timeout"
    fi
  else
    warn "no ps(1) — timeout unenforced for this run"
  fi
  vcode=0; wait "$vpid" || vcode=$?
  tail4k=$(tail -c 4000 "$dir/.verify.out")
  echo "» exit=$vcode timed_out=$timed_out"
  jq --argjson exit "$vcode" --argjson to "$timed_out" --arg now "$now" --arg tail "$tail4k" \
    '. + {verifyExit:$exit, verifiedAt:$now, timedOut:$to, verifiedTail:$tail}' \
    "$dir/patch.json" > "$dir/patch.json.tmp" && mv "$dir/patch.json.tmp" "$dir/patch.json"
  echo "recorded into $dir/patch.json"
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg r "$repo" --arg p "$pr" --arg t "$now" --argjson e "$vcode" \
      --argjson to "$timed_out" --arg tail "$tail4k" --arg d "$dir" \
      '{schema_version:"1",verb:$v,repo:$r,pr:($p|tonumber? // null),at:$t,
        next:(if $e == 0 then "proof holds — revalidator may VERIFY" else "proof failed — fix, do not claim" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{exit:$e,timedOut:$to,outputTail:$tail,patch:$d},error:null}' >&9
  fi
  exit 0
fi

# ----------------------------------------------------------------- doctor ---
# Preflight the harness: gh auth + scopes, jq, hash tooling, git, API reachability.
# Exit 0 only when everything the skill needs is present. No repo/PR args.
if [[ "$cmd" == "doctor" ]]; then
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  now=$(now_utc)
  results="[]"
  add() { # name, true/false, detail
    results=$(jq --arg n "$1" --argjson ok "$2" --arg d "$3" '. + [{name:$n, ok:$ok, detail:$d}]' <<<"$results")
    if [[ "$2" == "true" ]]; then echo "ok   $1 — $3"; else echo "FAIL $1 — $3"; fi
  }
  if command -v gh >/dev/null 2>&1; then add "gh-cli" true "$(gh --version 2>/dev/null | head -n 1)"; else add "gh-cli" false "missing — install github.com/cli/cli"; fi
  if gh auth status >/dev/null 2>&1; then add "gh-auth" true "authenticated"; else add "gh-auth" false "run gh auth login"; fi
  scopes=$(gh api user --include 2>/dev/null | grep -i '^x-oauth-scopes:' | head -n 1 || true)
  if [[ "$scopes" =~ repo ]]; then add "token-scopes" true "$scopes";
  elif [[ -z "$scopes" ]]; then add "token-scopes" true "no scopes header — fine-grained token, scopes proven at post time";
  else add "token-scopes" false "needs repo (private) or public_repo scope — ${scopes:-unreadable}"; fi
  if command -v jq >/dev/null 2>&1; then add "jq" true "$(jq --version 2>/dev/null)"; else add "jq" false "missing — install jq"; fi
  if command -v shasum >/dev/null 2>&1; then add "shasum" true "sig hashing available"; else add "shasum" false "sig hashing hard-requires shasum (startup gate)"; fi
  if command -v ps >/dev/null 2>&1; then add "ps" true "verify timeout enforced"; else add "ps" false "verify runs without timeout enforcement"; fi
  if command -v git >/dev/null 2>&1; then add "git" true "check-tree/verify available"; else add "git" false "check-tree and verify degraded without git"; fi
  remaining=$(gh api rate_limit --jq '.resources.core.remaining' 2>/dev/null || true)
  if [[ -n "$remaining" ]]; then add "api-reachable" true "core quota remaining: $remaining"; else add "api-reachable" false "cannot reach api.github.com with this token"; fi
  healthy=$(jq -e 'map(select(.ok == false)) | length == 0' <<<"$results" >/dev/null 2>&1 && echo "true" || echo "false")
  if [[ "$json_mode" == "true" ]]; then
    jq -n --arg v "$cmd" --arg t "$now" --argjson h "$healthy" --argjson c "$results" \
      '{schema_version:"1",verb:$v,repo:"",pr:null,at:$t,
        next:(if $h then "harness healthy" else "fix FAIL lines above, then re-run" end),
        warnings:(($ENV.WARNINGS_JSON // "[]") | fromjson),data:{healthy:$h,checks:$c},error:null}' >&9
  fi
  [[ "$healthy" == "true" ]]
  exit 0
fi

# ------------------------------------------------------------ calibrate ---
# Join findings against live ground truth: thread states + files changed since the
# review head. Read-only (3 reads: threads, PR state, one compare). Reports outcomes
# + tier precision; never auto-tunes. Terminal findings are skipped, not judged.
if [[ "$cmd" == "calibrate" ]]; then
  while [[ $# -gt 0 ]]; do case "$1" in
    --json) json_mode="true"; shift;;
    *) echo "unknown flag $1" >&2; exit 2;;
  esac; done
  if [[ "$json_mode" == "true" ]]; then exec 9>&1; exec 1>&2; fi
  dir=".review/$pr"
  [[ "$pr" == local-* ]] && fail "no-threads" "calibrate joins live threads — nothing to settle on a local bundle (adopt + post first, then calibrate the PR)"
  prov=$(jq -r '.provider // "github"' "$dir/PR.json" 2>/dev/null || echo "github")
  if [[ "$prov" != "github" ]]; then fail "provider-unimplemented" "calibrate on $prov is not implemented yet (no live thread join for it)"; fi
  [[ -f "$dir/post.json" ]] || fail "missing-post" "missing $dir/post.json (post for real first — calibration needs a posted review id)"
  [[ -f "$dir/findings.json" ]] || fail "missing-findings" "missing $dir/findings.json (run post first)"
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

echo "unknown cmd $cmd" >&2; exit 2
