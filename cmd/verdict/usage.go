package main

// Front-door usage and arity (go-port 08, F11): the binary owns every
// verb, every usage error, and the arity matrix. A missing verb or
// positional fails with the full usage block on stderr and exit 2 before
// any settings, store, or network read. Unknown verbs with positionals
// present keep the shell unknown-cmd message (exit 2).

// usageText is the frozen CLI usage block: the shell usage text
// with the program name changed to verdict and no rewritten prose
// (go-port 08, operator ruling: substitution only). Printed to
// stdout by -h/--help (exit 0) and to stderr by the arity gate
// (exit 2).
func usageText() string {
	return "usage: verdict fetch|post|triage|report|next|show|verify|doctor|calibrate|" +
		"fetch-local|record|adopt|fix|check-tree|ready|trend|learn|init|install [opts]\n" +
		"  fetch <owner/repo> <pr> [--base-sha|--since <sha>] [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] " +
		"[--consent-over-budget] [--show-prompts] [--chunk] [--json]\n" +
		"    write .review/<pr>/{PR.json,full.diff,toc.txt,roles.txt,reviews.json,inline.json[,incremental.diff]} " +
		"(PR.json normalized for non-github providers)\n" +
		"    diffs over 512000 bytes refuse with a split-or-narrow hint (empty diffs warn, never fail).\n" +
		"    Deliberate exceptions only: REVIEW_MAX_BYTES=<n> (or .review/max_bytes) overrides the cap.\n" +
		"    Chunking opt-in only: --chunk (or .review/chunk holding true) marks over-cap fetches for " +
		"chunked slicing (override chain wins; absent reads today's refusal).\n" +
		"    With REVIEW_BUDGET_USD (or .review/budget) set, fetch prints a pre-run\n" +
		"    cost estimate from comparable past runs and refuses projected-over-budget\n" +
		"    runs unless --consent-over-budget is given (consent lands in budget.json,\n" +
		"    surfaced by report --cost). Unset: estimate off, fetch as today.\n" +
		"  fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]\n" +
		"        [--stash [n]] [--consent-over-budget] [--show-prompts] [--chunk] [-- paths...] [--json]\n" +
		"    local review bundle from git (no GitHub, no PR): writes\n" +
		"    .review/local-<name>/{source.json,full.diff,toc.txt,roles.txt}.\n" +
		"    Same diff cap as fetch (512000 bytes, overridable as above); untracked/binary exclusions warn, never fail.\n" +
		"    Same budget gate as fetch (estimate + --consent-over-budget); unset: as today.\n" +
		"    downstream verbs address it as local-<name>; post/calibrate refuse it.\n" +
		"    --show-prompts (fetch and fetch-local) renders the assembled agent prompt —\n" +
		"    SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md (verbatim) + trust-boundary\n" +
		"    prologue + nonce-fenced bundle meta + toc.txt + roles.txt + full.diff +\n" +
		"    kb.txt/sensitive.txt/panel when present (one fence per file, in that order) —\n" +
		"    to stdout (human; progress moves to stderr) or data.prompt_dump (--json).\n" +
		"    No agent runs, no network beyond the fetch, no store writes beyond the bundle.\n" +
		"  record <owner/repo> <local-slug> [--json]\n" +
		"    validate + merge a local review.json into findings.json + record.json. No GitHub, no post.\n" +
		"  adopt <owner/repo> <pr> --from <local-slug> [--json]\n" +
		"    carry a local bundle's findings into a fresh PR fetch for posting (re-anchors, stamps handoff).\n" +
		"  fix <owner/repo> <local-slug> [--sig <s>] [--json]\n" +
		"    one bounded pass over a local bundle: picks the top open inline sig\n" +
		"    (or --sig), prints finding + kb.txt context with verify/land/re-run\n" +
		"    instructions. Max 3 passes per sig (ledger .review/<bundle>/fix.json);\n" +
		"    touching files outside the anchor stops the loop (scope-escape).\n" +
		"  post  <owner/repo> <pr> [--approve=true|false] [--request-changes] [--bind] [--force]\n" +
		"        [--review-file .review/<pr>/review.json] [--dry-run] [--json]\n" +
		"    validate anchors (partitioned, keep-biased), embed suggestions, merge findings.json,\n" +
		"    guard stale-SHA + duplicates, post exactly one review.\n" +
		"    Binding APPROVE/REQUEST_CHANGES needs --bind (else downgraded to COMMENT);\n" +
		"    APPROVE is refused with open or deferred findings unless --force.\n" +
		"    Agent status claims (resolved[]/triage[]) are proposals: addressed flips only on\n" +
		"    verification.json evidence (VERIFIED + resultHead matching HEAD).\n" +
		"    --dry-run prints validation + payload summary, writes findings.dryrun.json, posts nothing.\n" +
		"  triage <owner/repo> <pr> --sig <sig> --status <open|addressed|wont-fix|false-positive|deferred>\n" +
		"        --rationale \"...\" [--force] [--dry-run]\n" +
		"    mutate .review/<pr>/findings.json directly. No network. Rationale required unless --force.\n" +
		"  report <owner/repo> <pr> [--status open|addressed|all] [--kind inline|section|all]\n" +
		"        [--output <path>] [--sarif <path>] [--cost] [--check <critical|important|informational>] [--completeness] " +
		"[--live auto|strict|skip] [--json]\n" +
		"    pure read over the store: ranked findings + verdict + counts. Advisory runs\n" +
		"    touch no network; gate runs (--check/--completeness) end with one live-thread recheck read.\n" +
		"    --sarif writes a SARIF 2.1.0 file (severity→level, sig as fingerprint) for scanners/CI.\n" +
		"    --cost adds a spend rollup from .review/<pr>/cost.json (absent/unreadable -> zeros, never fails; " +
		"stratified by effort tier).\n" +
		"    Consented over-budget receipts from .review/<pr>/budget.json surface here when present.\n" +
		"    --check <tier> opts into gating (same output, machine exits): 0 no open\n" +
		"    findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool\n" +
		"    error (missing/corrupt store). Untagged findings never meet a tier; the\n" +
		"    gate counts open findings only, whatever --status/--kind show. Without\n" +
		"    --check the run stays advisory: exit 0 with findings listed (typed\n" +
		"    errors exit 1, usage exits 2).\n" +
		"    --completeness opts into a second gate dimension answering \"is HEAD\n" +
		"    reviewed?\" from the local completed-post record (local post-record\n" +
		"    read): pass iff post.json exists, parses, and carries a non-empty\n" +
		"    head equal to the bundle's fetched HEAD (PR.json head SHA, else\n" +
		"    source.json head_sha for local bundles). Alone it gates on HEAD-reviewed\n" +
		"    only; combined with --check the run fails if tier-hits OR stale/missing\n" +
		"    post. Every gate run (--check and/or --completeness) ends with a third,\n" +
		"    network-backed live-thread dimension: more live opens than local opens\n" +
		"    fails the gate (unreachable lookups warn and stay neutral; local and\n" +
		"    non-GitHub bundles warn-skip with zero network). --live selects the\n" +
		"    offline posture for that dimension: auto (default) keeps today's\n" +
		"    fail-open behavior; strict fails the gate when the lookup is\n" +
		"    unreachable; skip pays zero network and gates on local dimensions\n" +
		"    only, saying so loudly. Gate failure exits 1\n" +
		"    with a check: line naming the re-fetch command; tool errors keep exit 3.\n" +
		"    Without a gate flag output and exits are byte-identical to today\n" +
		"    (--live is only consulted on gate runs).\n" +
		"  next <owner/repo> <pr> [--status open] [--json]\n" +
		"    top-ranked actionable finding + the command to act on it. Pure read.\n" +
		"  show <owner/repo> <pr> --sig <sig> [--json]\n" +
		"    one full finding record. Pure read.\n" +
		"  check-tree [--expect <prefix,...>] [--json]\n" +
		"    refuse on unexpected dirt (own .review/ scratch ignored). No network. Machine clean-tree gate for mutating modes.\n" +
		"  verify <owner/repo> <pr> [--timeout <secs, default 300>] [--json]\n" +
		"    execute patch.json verifyCmd with a watchdog, record exit + tail into the patch record.\n" +
		"    Runs in its own process group (timeout kills the tree); known secret values\n" +
		"    are redacted from the recorded tail.\n" +
		"    Runs the agent's claimed proof — exit code becomes machine fact. No GitHub I/O.\n" +
		"    Destructive, networked, credential-reading, and repo-escaping commands\n" +
		"    refuse before execution as verify-destructive, verify-networked,\n" +
		"    verify-credential-read, and verify-repo-escape.\n" +
		"  doctor [--json]\n" +
		"    preflight the harness: gh auth, jq (>= 1.7), shasum hard-fail the run; scopes, git,\n" +
		"    ps, API reachability warn only (never fail); .review storage writability\n" +
		"    hard-fails. A token granting more than runs need warns on a\n" +
		"    token-scopes-excess row naming the excess scopes. jq below 1.7 warns on a\n" +
		"    jq-version row. Exit 0 unless a hard\n" +
		"    check fails. No repo/PR args, no writes, no network beyond the existing\n" +
		"    gh reads.\n" +
		"  calibrate <owner/repo> <pr> [--probe [--judge SIG:VERDICT[:HELPFUL]]... [--judge-file PATH]] [--json]\n" +
		"    join findings against live ground truth (thread states + files changed since the\n" +
		"    review head). Read-only; reports outcomes + tier precision, never auto-tunes.\n" +
		"    --probe grades closed findings offline against recorded triage dispositions\n" +
		"    (addressed = real, false-positive = false, else unknown — never forced)\n" +
		"    into additive .review/<bundle>/kb-grades.jsonl rows; writes nothing else,\n" +
		"    needs no post record, touches no network. Baseline columns\n" +
		"    (verdict/rule/helpful) regenerate from findings.json every run; agent\n" +
		"    judgments arrive via --judge/--judge-file, are validated against known\n" +
		"    sigs (unknown sigs refuse typed), and merge as agent_verdict/agent_helpful/\n" +
		"    agreement columns that may diverge from the baseline — the agreement rate\n" +
		"    over judged rows is the M4 evidence number.\n" +
		"  ready <owner/repo> <pr> [--json] [--check]\n" +
		"    advisory only (exit 0, never a gate): CI state, open threads, draft/conflict,\n" +
		"    description completeness. Prints blockers with actions + recommendation\n" +
		"    (review/wait/nudge-author). PR bundles only; local bundles refused.\n" +
		"    --check opts into gating (same output, machine exits): 0 ready, 1 blockers\n" +
		"    or wait, 2 usage, 3 tool error. Without --check the run stays advisory:\n" +
		"    exit 0 with blockers listed (typed errors exit 1, usage exits 2).\n" +
		"  trend <owner/repo> [--overlap] [--json]\n" +
		"    portfolio rollup over existing bundle stores (no network, writes nothing):\n" +
		"    findings opened/addressed/closed over time, broken down by severity tier\n" +
		"    and status, with per-bundle models/effort for the calibration join.\n" +
		"    Recomputed every run (manual edits stay truthful); missing/unreadable\n" +
		"    stores warn and skip, never fail. `closed` means no longer actionable\n" +
		"    (wont-fix/false-positive/deferred); null severity reads untagged.\n" +
		"    --overlap adds the KB overlap audit: an offline join of kb-recall.json\n" +
		"    manifests, findings kb cites, and kb-joins.jsonl into recall precision,\n" +
		"    recall coverage of cites, and the cited-finding outcome split\n" +
		"    (addressed vs false-positive); missing manifests read unmeasured (never\n" +
		"    zero) and stay out of both denominators. Default output is byte-identical\n" +
		"    without the flag.\n" +
		"  learn --rule <text> --scope <globs> [--id <id>] [--provenance <s>] [--rigor <level>] [--json]\n" +
		"    append one rule to .review/kb.jsonl (zeros + status active; provenance\n" +
		"    defaults to manual, rigor defaults to pointed). Refuses exact-duplicate\n" +
		"    rule text and taken ids.\n" +
		"  learn --status [--json]\n" +
		"    list rules with counters + precision. No kb.jsonl reads as no rules.\n" +
		"  learn --retire <id> --reason <text> [--json]\n" +
		"    retire a rule (tombstone kept, counters untouched, history-logged);\n" +
		"    reason needs 30+ chars.\n" +
		"  learn --restore <id> --reason <text> [--json]\n" +
		"    restore a retired rule to active (tombstone history kept);\n" +
		"    reason needs 30+ chars.\n" +
		"  init [--preset chill|standard|strict] [--yes] [--force] [--json]\n" +
		"    guided offline scaffolder: writes the standard local control files\n" +
		"    (.review/budget, .review/panel, .review/kb-policy, .review/kb.jsonl\n" +
		"    starter) with style presets, then runs check-tree validation.\n" +
		"    Presets map to panel strictness + KB retirement thresholds:\n" +
		"    chill (no pinned panel, MIN_JUDGED=10 MAX_FP_RATE=0.7),\n" +
		"    standard (one pinned member, 5 / 0.5), strict (lead + 2 lenses, 3 / 0.3).\n" +
		"    Never touches network. Existing files print a preview and need\n" +
		"    --yes/--force (or an interactive confirm) before overwrite.\n" +
		"  install [--yes] [--force] [--json]\n" +
		"    copy-based skill installer (offline): detects the de-facto agent skill\n" +
		"    dirs (.agents/skills, .claude/skills, .codex/skills at the repo root\n" +
		"    and $HOME), previews the copy plan for the tool-owned verdict/\n" +
		"    payload (SKILL.md + prompts/ + scripts/), applies only on explicit\n" +
		"    confirm. The installed copy carries the CLI, so SKILL.md command paths\n" +
		"    resolve inside it. Only installer-owned verdict/ dirs (carrying a\n" +
		"    .verdict-managed receipt) are ever written; foreign skills and unowned\n" +
		"    verdict/ dirs are left untouched. An up-to-date re-run reads keep\n" +
		"    (idempotent); a receipt/tool version skew warns, never fails. Same\n" +
		"    preview-then-confirm idiom as init: writes need --yes/--force (or, in\n" +
		"    human mode, an interactive confirm when stdin is a tty; --json never\n" +
		"    prompts and refuses typed needs-confirm). Run the CLI from the repo\n" +
		"    under review: `.review/` is created in the current directory, never\n" +
		"    beside the tool. The CLI lives at `<tool>/scripts/verdict` — the\n" +
		"    checkout for symlink installs, the installed `verdict/` skill dir for\n" +
		"    registry and copy installs (both ship `scripts/`). The install-skill.sh\n" +
		"    symlink route stays the wide-coverage sibling for git checkouts only\n" +
		"    (nine agents, --detect/--all).\n" +
		"  Global presentation flags (any verb; stripped before verb parsing):\n" +
		"  --color <auto|always|never>, --compact / --no-compact, --no-json.\n" +
		"  Global publish flags (any verb; stripped before verb parsing):\n" +
		"  --footer / --no-footer, --attribution [off|product|full] / --no-attribution,\n" +
		"  --attribution-text <text>, --copyright <text>, --logo / --no-logo,\n" +
		"  --logo-url <url>, --overview / --no-overview,\n" +
		"  --diagram-behavioral / --no-diagram-behavioral,\n" +
		"  --diagram-structural / --no-diagram-structural,\n" +
		"  --diagram-expanded / --no-diagram-expanded.\n" +
		"  Flags win over the settings file, which wins over env\n" +
		"  (REVIEW_OUTPUT_FORMAT, REVIEW_LAYOUT_COMPACT, REVIEW_COLOR_MODE,\n" +
		"  REVIEW_PUBLISH_FOOTER, REVIEW_PUBLISH_ATTRIBUTION,\n" +
		"  REVIEW_PUBLISH_ATTRIBUTION_TEXT, REVIEW_PUBLISH_COPYRIGHT,\n" +
		"  REVIEW_PUBLISH_LOGO, REVIEW_PUBLISH_LOGO_URL, REVIEW_PUBLISH_OVERVIEW,\n" +
		"  REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL, REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL,\n" +
		"  REVIEW_PUBLISH_DIAGRAMS_EXPANDED),\n" +
		"  which wins over defaults. The settings file holds presentation (output,\n" +
		"  layout, color) plus publish (footer receipt + leveled attribution + logo +\n" +
		"  overview + diagrams) namespaces\n" +
		"  (~/.config/verdict/settings.json, $XDG_CONFIG_HOME respected).\n" +
		"  --json on fetch/post/triage/report/next/show/verify/doctor/calibrate/" +
		"fetch-local/record/adopt/fix/check-tree/ready/trend/learn/init/install: " +
		"stdout is exactly one JSON envelope (schema_version 1);\n" +
		"  all human progress goes to stderr. Errors emit {data:null,error:{code,message}} non-zero.\n"
}

// bareVerbs lists the verbs that take no repo/pr positionals.
var bareVerbs = map[string]bool{
	"check-tree": true,
	"doctor":     true,
	"learn":      true,
	"init":       true,
	"install":    true,
}

// arityUsage reports whether the invocation fails the positional-arity
// gate: no verb, a missing repo for trend, or a missing repo/pr for
// every other verb that takes them. Bare verbs never fail.
func arityUsage(cmd, repo, pr string) bool {
	if cmd == "" {
		return true
	}
	if bareVerbs[cmd] {
		return false
	}
	if cmd == cmdTrend {
		return repo == ""
	}
	return repo == "" || pr == ""
}

// unknownCmdMessage ports the shell tail-dispatch refusal for a verb it
// does not know, reached only when the arity gate passed (both positionals
// present, or the verb is bare).
func unknownCmdMessage(cmd string) string {
	return "unknown cmd '" + cmd + "' (want " + verbsHint + ")"
}
