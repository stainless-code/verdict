package main

func usageText() string {
	return "usage: verdict fetch|post|triage|report|next|show|verify|doctor|calibrate|" +
		"fetch-local|run|panel|record|adopt|fix|check-tree|ready|gate|threads|trend|index|" +
		"backtest|learn|pack|init|skill|upgrade|uninstall|mcp|serve|config [opts]\n" +
		"  fetch <owner/repo> <pr> [--base-sha|--since <sha>] [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] " +
		"[--index <ref|dir>] [--context <file|dir>] [--consent-over-budget] [--show-prompts] [--chunk] [--json]\n" +
		"    write .verdict/state/<pr>/{PR.json,full.diff,toc.txt,roles.txt,reviews.json,inline.json[,incremental.diff]} " +
		"(PR.json normalized for non-github providers)\n" +
		"    --index <ref|dir> hands one index the operator built to the bundle: impact.json plus a\n" +
		"    capped \"## impact\" section on toc.txt (nothing is built or discovered; omit for no section).\n" +
		"    Provider scope: record/report/triage/show/next run on any provider bundle; post submits to GitHub,\n" +
		"    GitLab, and Forgejo (GitLab takes an approval but has no request-changes event — a bound\n" +
		"    request-changes posts as a comment; post.json records the withheld capability in withheld[]); live\n" +
		"    calibrate and ready run on GitHub and GitLab; adopt needs one verdict can post to (forgejo, github,\n" +
		"    gitlab); the rest refuse typed (provider-unimplemented; a local bundle has its own code — post/ready\n" +
		"    no-pr-target, calibrate no-threads, adopt bad-target). calibrate --probe is offline and runs on any\n" +
		"    bundle. Incremental re-review (--base-sha/--since) runs on GitHub and GitLab; other providers write\n" +
		"    incremental.diff.note.\n" +
		"    diffs over 512000 bytes refuse with a split-or-narrow hint (empty diffs warn, never fail).\n" +
		"    Deliberate exceptions only: REVIEW_MAX_BYTES=<n> (or the typed max_bytes default) overrides the cap.\n" +
		"    Chunking opt-in only: --chunk (or the typed chunk default) marks over-cap fetches for " +
		"chunked slicing (override chain wins; absent reads today's refusal).\n" +
		"    With REVIEW_BUDGET_USD (or the typed budget_usd default) set, fetch prints a pre-run\n" +
		"    cost estimate from comparable past runs and refuses projected-over-budget\n" +
		"    runs unless --consent-over-budget is given (consent lands in budget.json,\n" +
		"    surfaced by report --cost). Unset: estimate off, fetch as today.\n" +
		"  fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]\n" +
		"        [--stash [n]] [--worktree] [--index <ref|dir>] [--context <file|dir>]\n" +
		"        [--consent-over-budget] [--show-prompts] [--chunk]\n" +
		"        [-- paths...] [--json]\n" +
		"    local review bundle from git (no GitHub, no PR): writes\n" +
		"    .verdict/state/local-<name>/{source.json,full.diff,toc.txt,roles.txt}.\n" +
		"    --worktree checks the review target into a temporary isolated worktree and\n" +
		"    builds the bundle from that checkout (removed on exit, even on refusal); without\n" +
		"    it the bundle builds from the live tree exactly as before.\n" +
		"    Same diff cap as fetch (512000 bytes, overridable as above); untracked/binary exclusions warn, never fail.\n" +
		"    Same budget gate as fetch (estimate + --consent-over-budget); unset: as today.\n" +
		"    downstream verbs address it as local-<name>; post and live calibrate refuse it, while\n" +
		"    calibrate --probe grades it offline.\n" +
		"    --show-prompts (fetch and fetch-local) renders the assembled agent prompt —\n" +
		"    SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md (verbatim) + trust-boundary\n" +
		"    prologue + nonce-fenced bundle meta + toc.txt + roles.txt + full.diff +\n" +
		"    kb.txt/sensitive.txt/panel when present (one fence per file, in that order) —\n" +
		"    to stdout (human; progress moves to stderr) or data.prompt_dump (--json).\n" +
		"    No agent runs, no network beyond the fetch, no store writes beyond the bundle.\n" +
		"  run <owner/repo> <pr|local-name> --agent <claude|codex|cursor|opencode>\n" +
		"      [--timeout <secs, default 300>] [--rounds <n, default 1>] [--print] [-- <agent args>...] [--json]\n" +
		"    launch YOUR agent against an existing bundle: one process per round, one shot by default.\n" +
		"    It requires exactly one fresh review.json (absent refuses no-review-artifact, older\n" +
		"    than the launch refuses stale-review-artifact), names the next command, and by default never\n" +
		"    reads the agent's prose, retries, repairs, and never posts. With --rounds N (2-5) an\n" +
		"    invalid review.json re-prompts the agent with the shape checker's fix list, up to\n" +
		"    N launch-to-check rounds; exhaustion refuses repair-exhausted. The agent inherits your\n" +
		"    environment (your key) and may use the network; verdict itself makes no calls.\n" +
		"    A non-zero agent exit is the process exit code (agent-failed); the watchdog\n" +
		"    timeout (agent-timeout), a missing binary (agent-missing), and a spawn failure\n" +
		"    (agent-spawn-failed) refuse typed with a hint. Provenance lands in\n" +
		"    .verdict/state/<bundle>/launcher.json (agent, binary, argv shape, timings, exit)\n" +
		"    and the full child output in .run.out. --print renders the exact command and\n" +
		"    exits 0 without spawning; everything after '--' goes to the agent unchanged.\n" +
		"    --print refuses options outside the safe model/cost display allowlist.\n" +
		"    run passes a short tool-generated directive, never bundle bytes.\n" +
		"  config <owner/repo> <pr> (--explain | --preview [--file <path>]) [--json]\n" +
		"    read-only policy surface: --explain renders the stored effective policy of a fetched run\n" +
		"    (values with origins, requirements, pending plan, spend, privacy, observed checks);\n" +
		"    --preview resolves a proposed file (or the trusted inventory, else the no-policy layers)\n" +
		"    with current personal settings and environment — no live calls, no writes, and\n" +
		"    never an approval. One git rev-parse root probe runs outside the caller commander.\n" +
		"  panel <owner/repo> <pr> [--json]\n" +
		"    plan independent verification in verification/; never calls a model.\n" +
		"  record <owner/repo> <local-slug> [--dry-run] [--strict] [--cost-names[=<names>]]\n" +
		"         [--require-coverage] [--json]\n" +
		"    validate + merge a local review.json into findings.json + record.json. No GitHub, no post.\n" +
		"    --dry-run runs every check and writes nothing; --strict promotes warnings to errors.\n" +
		"    --cost-names names the cost record's models[] entries from the panel, in order, and\n" +
		"    changes no count; bare, it reads the panel pinned in the bundle and refuses without one.\n" +
		"  adopt <owner/repo> <pr> --from <local-slug> [--json]\n" +
		"    carry a local bundle's findings into a fresh PR fetch for posting (re-anchors, stamps handoff).\n" +
		"    Adopt-into needs a bundle verdict can post to (forgejo, github, gitlab): other hosts refuse\n" +
		"    typed (provider-unimplemented, naming the host).\n" +
		"  fix <owner/repo> <local-slug> [--sig <s>] [--json]\n" +
		"    one bounded pass over a local bundle: picks the top open inline sig\n" +
		"    (or --sig), prints finding + kb.txt context with verify/land/re-run\n" +
		"    instructions. Max 3 passes per sig (ledger .verdict/state/<bundle>/fix.json);\n" +
		"    touching files outside the anchor stops the loop (scope-escape).\n" +
		"  post  <owner/repo> <pr> [--approve=true|false] [--request-changes] [--bind] [--force]\n" +
		"        [--review-file .verdict/state/<pr>/review.json] [--dry-run] [--require-coverage]\n" +
		"        [--min-severity critical|important|informational|off] [--allow-secret-values] [--json]\n" +
		"    validate anchors (partitioned, keep-biased), embed suggestions, merge findings.json,\n" +
		"    guard stale-SHA + duplicates, post exactly one review.\n" +
		"    Binding APPROVE/REQUEST_CHANGES needs --bind (else downgraded to COMMENT);\n" +
		"    APPROVE is refused with open or deferred findings unless --force.\n" +
		"    Agent status claims (resolved[]/triage[]) are proposals: addressed flips only on\n" +
		"    verification.json evidence (VERIFIED + resultHead matching HEAD).\n" +
		"    --dry-run prints validation + payload summary, writes findings.dryrun.json, posts nothing.\n" +
		"    A secret-shaped value in the review text refuses post-secret-shaped before any host\n" +
		"    call (--dry-run warns instead); --allow-secret-values publishes it and post.json\n" +
		"    records the shapes it let through.\n" +
		"    GitHub, GitLab, and Forgejo PR bundles submit; local bundles refuse typed (no-pr-target) and\n" +
		"    the other providers refuse typed (provider-unimplemented).\n" +
		"  triage <owner/repo> <pr> --sig <sig> --status <open|addressed|wont-fix|false-positive|deferred>\n" +
		"        --rationale \"...\" [--force] [--dry-run]\n" +
		"    mutate .verdict/state/<pr>/findings.json directly. No network. Rationale required unless --force.\n" +
		"  report --html <dir> [--root <dir>]\n" +
		"    export the six dashboard views as static HTML and data.json; no listener or host calls.\n" +
		"  report <owner/repo> <pr> [--status all|open|addressed|wont-fix|false-positive|deferred] " +
		"[--kind inline|section|all]\n" +
		"[--category logic|edge-case|security|performance|accidental-commit|style|docs|other]\n" +
		"        [--output <path>] [--sarif <path>] [--cost] [--check <critical|important|informational>] [--completeness] " +
		"[--live auto|strict|skip] [--json]\n" +
		"    pure read over the store: ranked findings + verdict + counts. Advisory runs\n" +
		"    touch no network; gate runs (--check/--completeness) end with one live-thread recheck read.\n" +
		"    --sarif writes a SARIF 2.1.0 file (severity→level, category in properties, sig as\n" +
		"    fingerprint) for scanners/CI.\n" +
		"    --cost adds a spend rollup from .verdict/state/<pr>/cost.json (absent/unreadable -> zeros, never fails; " +
		"stratified by effort tier).\n" +
		"    Consented over-budget receipts from .verdict/state/<pr>/budget.json surface here when present.\n" +
		"    --check <tier> opts into gating (same output, machine exits): 0 no open\n" +
		"    findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool\n" +
		"    error (missing/corrupt store). `--category <cat>` filters the ranked\n" +
		"    findings; `--check <tier> --category <cat>` gates only that class (an unknown\n" +
		"    category is a usage error). Untagged findings never meet a tier; the\n" +
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
		"    fails the gate (unreachable lookups warn and stay neutral under auto;\n" +
		"    local bundles warn-skip with zero network, and strict fails closed). --live selects the\n" +
		"    offline posture for that dimension: auto (default) keeps today's\n" +
		"    fail-open behavior; strict fails the gate when the lookup is\n" +
		"    unreachable; skip pays zero network and gates on local dimensions\n" +
		"    only, saying so loudly. Gate failure exits 1\n" +
		"    with a check: line naming the re-fetch command; tool errors keep exit 3.\n" +
		"    Without a gate flag output and exits are byte-identical to today\n" +
		"    (--live is only consulted on gate runs).\n" +
		"  next <owner/repo> <pr> [--status open|--coverage] [--json]\n" +
		"    top-ranked actionable finding + the command to act on it. Pure read.\n" +
		"  show <owner/repo> <pr> --sig <sig> [--json]\n" +
		"    one full finding record. Pure read.\n" +
		"  check-tree [--expect <prefix,...>] [--json]\n" +
		"    refuse on unexpected dirt (own .verdict/state/ scratch ignored). No network. Machine clean-tree gate for mutating modes.\n" +
		"  verify <owner/repo> <pr> [--timeout <secs, default 300>] [--group <stableID> --command <cmd>]\n" +
		"        [--sandbox] [--json]\n" +
		"    With neither --group nor --command, execute patch.json verifyCmd with a watchdog and\n" +
		"    record exit + tail in the patch record. --group and --command are required together:\n" +
		"    --command runs for the named deep plan group at the reviewed head and writes\n" +
		"    validation-<group>.json (patch.json stays untouched).\n" +
		"    Runs in its own process group (timeout kills the tree); known secret values\n" +
		"    are redacted from the recorded tail.\n" +
		"    --sandbox runs the command under bwrap (Linux only) with no network, empty\n" +
		"    ~/.aws, ~/.ssh, and ~/.config/gh, a writable reviewed root, and a private /tmp;\n" +
		"    when bwrap cannot enforce that it refuses sandbox-unavailable and runs nothing.\n" +
		"    Runs the agent's claimed proof — exit code becomes machine fact. No GitHub I/O.\n" +
		"    Destructive, networked, credential-reading, and repo-escaping commands\n" +
		"    refuse before execution as verify-destructive, verify-networked,\n" +
		"    verify-credential-read, and verify-repo-escape.\n" +
		"  doctor [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] [--json]\n" +
		"    preflight the configured host: its CLI and auth, plus .verdict storage\n" +
		"    writability, hard-fail the run; scopes, git, ps, and API reachability warn\n" +
		"    only (never fail). --provider picks the host (default github); github reads\n" +
		"    through gh, gitlab through glab, gitcafe through cafe, and\n" +
		"    forgejo/bitbucket/azure check curl or az plus the credential variables\n" +
		"    their fetch paths read (presence only, never a value). A token granting\n" +
		"    more than runs need warns on a token-scopes-excess row naming the excess\n" +
		"    scopes. Exit 0 unless a hard check fails. No repo/PR args, no writes, no\n" +
		"    network beyond the host CLI reads.\n" +
		"  calibrate <owner/repo> <pr> [--reactions [--reaction-reason TEXT]] " +
		"[--probe [--judge SIG:VERDICT[:HELPFUL]]... [--judge-file PATH]] [--json]\n" +
		"    join findings against live ground truth (thread states + files changed since the\n" +
		"    review head). Read-only; reports outcomes + tier precision, never auto-tunes.\n" +
		"    --probe grades closed findings offline against recorded triage dispositions\n" +
		"    (addressed = real, false-positive = false, else unknown — never forced)\n" +
		"    into additive .verdict/state/<bundle>/kb-grades.jsonl rows; writes nothing else,\n" +
		"    needs no post record, touches no network. Baseline columns\n" +
		"    (verdict/rule/helpful) regenerate from findings.json every run; agent\n" +
		"    judgments arrive via --judge/--judge-file, are validated against known\n" +
		"    sigs (unknown sigs refuse typed), and merge as agent_verdict/agent_helpful/\n" +
		"    agreement columns that may diverge from the baseline — the agreement rate\n" +
		"    over judged rows is the evidence number.\n" +
		"  ready <owner/repo> <pr> [--json] [--check]\n" +
		"    advisory only (exit 0, never a gate): CI state, open threads, draft/conflict,\n" +
		"    description completeness. Prints blockers with actions + recommendation\n" +
		"    (review/wait/nudge-author). GitHub and GitLab PR bundles; local bundles refuse\n" +
		"    (no-pr-target) and other providers refuse typed (provider-unimplemented).\n" +
		"    --check opts into gating (same output, machine exits): 0 ready, 1 blockers\n" +
		"    or wait, 2 usage, 3 tool error. Without --check the run stays advisory:\n" +
		"    exit 0 with blockers listed (typed errors exit 1, usage exits 2).\n" +
		"  gate <owner/repo> <pr> [--max-severity critical|important|informational] [--require-head]\n" +
		"        [--live auto|strict|skip] [--coverage require|warn] [--json]\n" +
		"    one composed local gate over the dimensions report already computes: open\n" +
		"    findings at-or-above a tier (--max-severity), a completed post for the fetched\n" +
		"    HEAD (--require-head), the stored diff-coverage receipt for that HEAD\n" +
		"    (--coverage, default require), live unresolved threads (--live, default auto),\n" +
		"    and the captured compliance verdict when the bundle carries a typed policy.\n" +
		"    At least one dimension is required; --live and --coverage alone are not.\n" +
		"    Exits 0 pass, 1 gate failed or unknown, 2 usage, 3 tool error; a gate failure\n" +
		"    carries data.passed false with error null. data.dimensions[] lists findings,\n" +
		"    head, coverage, live_threads, compliance with id, status, detail, count.\n" +
		"    --coverage require fails on an absent or partial attestation for the gated\n" +
		"    HEAD; --coverage warn reports the same and never fails the gate. --live skip\n" +
		"    makes zero network calls; auto keeps the fail-open skip. Never posts,\n" +
		"    approves, merges, or writes a store: the exit code is the whole integration,\n" +
		"    and the merge stays the human's. Use report for the ranked items, cost, and\n" +
		"    artifacts.\n" +
		"  threads <owner/repo> <pr> [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] [--json]\n" +
		"    refresh .verdict/state/<pr>/threads.json from the host (no diff refetch): host-resolved\n" +
		"    thread state, anchors, authors, bodies, and finding links for every review\n" +
		"    conversation. fetch writes the same feed beside the bundle; this verb re-reads\n" +
		"    it after replies/resolutions so a re-review sees current state. Writes only\n" +
		"    threads.json and needs an existing bundle (fetch first).\n" +
		"  threads resolve <owner/repo> <pr> [--thread <id>]... [--dry-run] [--json]\n" +
		"    close verdict-authored review threads: without --thread, every open thread\n" +
		"    whose finding the local store marks addressed; with --thread, only the named\n" +
		"    ids, and each must still be verdict-authored (a human thread refuses).\n" +
		"    --dry-run lists what would be resolved and makes zero writes; a real run\n" +
		"    makes one host write per thread and writes threads-resolve.json. GitHub and\n" +
		"    GitLab carry it; Forgejo refuses typed (no documented resolve endpoint).\n" +
		"  trend <owner/repo> [--rules] [--overlap] [--resolution] [--json]\n" +
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
		"  index build [--from <dir>] [--at <ref>] [--json]\n" +
		"    build the code index for one tree under .verdict/index/: Go parsed with\n" +
		"    go/parser, TypeScript, JavaScript, and Python by two lexical scanners.\n" +
		"    --from reads another checkout; --at names a ref (default HEAD). Shards are\n" +
		"    content-addressed by blob, so a rename parses nothing and an edit parses one.\n" +
		"  index status [--json]\n" +
		"    stored trees and shards, and whether HEAD's tree is indexed.\n" +
		"  index prune [--json]\n" +
		"    keep the newest 4 trees and the shards they reference.\n" +
		"  index callers <symbol> [--at <tree>] [--limit <n, default 50, max 200>] [--json]\n" +
		"    call sites of one symbol: exact within its package, lexical across packages\n" +
		"    only through an import edge. A qualified symbol names the package (a.Target);\n" +
		"    rows are truncated with a count, never silently, and unresolved candidates\n" +
		"    (methods, dynamic dispatch, generic instantiation) are counted, never guessed.\n" +
		"  index tests-for <path>|--symbol <pkg.Name> [--at <tree>] [--depth <n, default 3, max 8>]\n" +
		"    [--limit <n, default 50, max 200>] [--json]\n" +
		"    candidate tests to read for one source file or qualified symbol, labeled\n" +
		"    same-package, references, imports-module, or transitive-import (a test whose\n" +
		"    own package reaches the declaring package through imports of its own, within\n" +
		"    --depth import hops and counted past the cap). Candidates are not coverage:\n" +
		"    a row names a test worth reading, not proof, and only that language's own\n" +
		"    test names appear.\n" +
		"  index co-changed <path> [--commits <n, default 2000>] [--since <90d|12w|18mo|2y|ref>]\n" +
		"    [--min-count <n, default 2>] [--limit <n, default 20, max 200>] [--json]\n" +
		"    paths that moved with one path, with their co-change count and jaccard, from\n" +
		"    one bounded git log window (merges excluded; a rename counts as two paths).\n" +
		"    The window's first/last commit and count are echoed; a shallow clone warns.\n" +
		"  index owners <path> [--at <tree>] [--json]\n" +
		"    the CODEOWNERS rule that owns one path at HEAD, read from the git tree (never\n" +
		"    the worktree): GitHub's file order, last matching rule wins, no rule reads as\n" +
		"    owners null, an ownerless rule reads as owners [] (it clears an earlier\n" +
		"    match), never nobody.\n" +
		"  index blast-radius --pr <n>|--paths <path> [--paths <path>...] [--at <tree>]\n" +
		"      [--limit <n, default 50, max 200>] [--commits <n, default 2000>]\n" +
		"      [--since <90d|12w|18mo|2y|ref>] [--min-count <n, default 2>] [--json]\n" +
		"    the changed declarations and their callers to depth 2, plus candidate tests,\n" +
		"    co-change partners, and owners. --pr reads the fetched bundle's full.diff (no\n" +
		"    host call); --paths needs no bundle. Each list carries its count and truncation\n" +
		"    count; without an index the symbol, caller, and test counts are null, never 0.\n" +
		"  backtest mine --since <90d|12w|18mo|2y|<ref>> [--until <ref>] [--run <slug>] [--json]\n" +
		"    mine SZZ bug-introducing PR candidates from the local clone: seeds are fix:,\n" +
		"    bugfix:, hotfix:, Fixes #N, and Revert \"...\" commits in the window; each locked\n" +
		"    line is blamed to its introducing commit and mapped to the merge or squash PR\n" +
		"    that carried it. Zero model calls, zero network. Writes\n" +
		"    .verdict/backtest/<run>/{run.json,mine.json}; a rerun of the same window is\n" +
		"    byte-identical. --until defaults to HEAD and must be an ancestor of HEAD;\n" +
		"    --since takes a duration against --until's commit date or a ref. Skipped fixes are\n" +
		"    counted with reasons, never refused. Candidates are not citable: SZZ-derived,\n" +
		"    unverified.\n" +
		"  backtest replay --run <id> --agent <name> [--max-reviews <n>] [--budget-usd <usd>]\n" +
		"        [--timeout <secs>] [--rounds <n>] [-- <agent args>] [--json]\n" +
		"    replay the mined candidates, newest merge first: each one gets a fresh local\n" +
		"    bundle at its own base...head, a detached checkout of that head, one agent\n" +
		"    launch (the same table run uses; everything after -- goes to it), and a\n" +
		"    record pass, so findings.json carries the historical review. This is the\n" +
		"    backtest step that spends: the ceiling is --max-reviews, --budget-usd (stops\n" +
		"    before a review whose projected cost would cross it), and --timeout (stops a\n" +
		"    review that runs too long).\n" +
		"    A priced review is the agent-reported estimate; an agent that reports none is\n" +
		"    unknown spend, never $0. Writes .verdict/backtest/<run>/prs/<NNN>/replay.json\n" +
		"    plus a backtest.json marker in every bundle it builds: trend skips a marked\n" +
		"    bundle and names the count, so the rollup stays your own history. Rerunning\n" +
		"    the same --run replays only what produced no review; a stored PR replayed\n" +
		"    with another agent or other pass-through arguments refuses run-mismatch.\n" +
		"    Checkouts live under .verdict/state and are removed after each review.\n" +
		"    Replays are not citable.\n" +
		"  backtest score --run <id> [--json]\n" +
		"    grade a replay's bundles against the SZZ bugs the run mined: catch rate,\n" +
		"    off-target findings per PR, cost per review, and a fix/revert signal table.\n" +
		"    A pure read of the run directory and the bundles it names: no agent, no\n" +
		"    bundle build, no corpus store. Writes .verdict/backtest/<run>/report.json.\n" +
		"    Catch rate is a match rate over SZZ candidates, not a measured catch rate:\n" +
		"    a deleted cleanup line beside the real bug is a candidate that was never\n" +
		"    defective, and an unmatched finding is not proven false. Scores are not\n" +
		"    citable.\n" +
		"  backtest --since <90d|12w|18mo|2y|<ref>> --agent <name> [replay flags] [--json]\n" +
		"    mine, replay, and score in one run: the miner resolves --run (or derives it),\n" +
		"    then the replay and the score read that id. --agent is required because the\n" +
		"    composed run replays; every replay flag and the bare -- pass-through apply.\n" +
		"  learn --rule <text> --scope <globs> [--id <id>] [--provenance <s>] [--rigor <level>] [--json]\n" +
		"    append one rule to .verdict/kb.jsonl (zeros + status active; provenance\n" +
		"    defaults to manual, rigor defaults to pointed). Refuses exact-duplicate\n" +
		"    rule text and taken ids.\n" +
		"  learn --propose --from-bundle <repo> <pr|local-name> [--dry-run] [--json]\n" +
		"  learn --accept <id> [--dry-run] [--json]\n" +
		"  learn --reject <id> --reason <text> [--dry-run] [--json]\n" +
		"    propose inactive rule packs; accept into KB; reject permanently (30+ char reason).\n" +
		"  learn --pack <directory> --id <rule-id> [--json]\n" +
		"  learn --status [--json]\n" +
		"    list rules with counters + precision. No kb.jsonl reads as no rules.\n" +
		"  learn --retire <id> --reason <text> [--json]\n" +
		"    retire a rule (tombstone kept, counters untouched, history-logged);\n" +
		"    reason needs 30+ chars.\n" +
		"  learn --restore <id> --reason <text> [--json]\n" +
		"    restore a retired rule to active (tombstone history kept);\n" +
		"    reason needs 30+ chars.\n" +
		"  init [--preset chill|standard|strict] [--yes] [--force] [--json]\n" +
		"    guided offline scaffolder: writes the typed controls (tracked\n" +
		"    .verdict/config.json defaults, .verdict/kb.jsonl starter, and the\n" +
		"    .verdict/.gitignore /state/ line) with style presets, then runs\n" +
		"    check-tree validation.\n" +
		"    Presets map to panel strictness + KB retirement thresholds:\n" +
		"    chill (no pinned panel, MIN_JUDGED=10 MAX_FP_RATE=0.7),\n" +
		"    standard (one pinned member, 5 / 0.5), strict (lead + 2 lenses, 3 / 0.3).\n" +
		"    Never touches network. Existing files print a preview and need\n" +
		"    --yes/--force (or an interactive confirm) before overwrite.\n" +
		"  skill [show [<path>]|files|status|install|uninstall] [--format md|json] [--dir <skills-dir>|--agent <name>...]\n" +
		"        [--detect] [--diff] [--force] [--dry-run] [--json]\n" +
		"    serve, inspect, or remove the skill embedded in this binary (never a skew):\n" +
		"    show prints SKILL.md (default --format md), show <path> prints one\n" +
		"    embedded file byte-identical, --format json emits one envelope with\n" +
		"    name/version/files, files lists every embedded path.\n" +
		"    status is a read-only drift report per target (create/update/keep/\n" +
		"    blocked) with receipt vs embedded version; it exits 0 in sync and 1\n" +
		"    when drifted. --diff adds a capped unified diff per changed file.\n" +
		"    install atomically writes SKILL.md, prompts/, schemas/, and\n" +
		"    scripts/verdict plus a .verdict-managed receipt; it previews\n" +
		"    create/update/keep/blocked, never prompts, and refuses a foreign\n" +
		"    target unless --force (foreign files are never touched).\n" +
		"    uninstall removes only the payload paths the receipt owns, then the\n" +
		"    receipt; foreign files survive, a target without a receipt refuses\n" +
		"    unless --force, and --dry-run deletes nothing.\n" +
		"    --agent repeats on install, status, and uninstall: targets are deduped\n" +
		"    and handled in argv order. --detect installs or reports every known\n" +
		"    agent whose skills dir already exists and is non-empty; with none\n" +
		"    present it refuses typed and asks for --agent <name>.\n" +
		"    In a skill copy, scripts/verdict resolves VERDICT_BIN, then\n" +
		"    <skill>/bin/verdict, then verdict on PATH; otherwise run the one-time\n" +
		"    verified <skill>/scripts/verdict bootstrap (no review-time fetch).\n" +
		"  pack <list|add|test> [--json]\n" +
		"    list: read-only lens-pack inventory in precedence order: built-in, org\n" +
		"    (explicit packs[] paths in personal settings), and repo\n" +
		"    (.verdict/packs/<name>/). Lists the packs in this checkout; a review\n" +
		"    reads repo packs from the PR's base, so a pack the PR adds or edits\n" +
		"    is listed here but not used. Rows carry name, version, layer,\n" +
		"    source, tree hash, lens and rule ids, and drift (null when the\n" +
		"    pack matches .verdict/verdict.lock, else the refusal code).\n" +
		"    Duplicate ids inside one layer refuse bad-pack; overriding a lower\n" +
		"    layer needs replace[] naming it, else pack-conflict.\n" +
		"    add <dir|git-url>[@ref]: validate with the same loader, vendor the\n" +
		"    loaded tree under .verdict/packs/<slug>/, and pin it in\n" +
		"    .verdict/verdict.lock (strict JSON, entries sorted by name).\n" +
		"    dir:<path> records the directory as given; git:<url>@<sha> records\n" +
		"    a hermetic shallow clone at the resolved HEAD commit. https URLs\n" +
		"    carry no userinfo; use ssh://git@host/path, git@host:path, or a\n" +
		"    public https URL. Re-adding a\n" +
		"    name replaces its tree and prints old -> new. A source inside\n" +
		"    .verdict/packs/ refuses: it is already vendored. fetch, post, and\n" +
		"    record refuse pack-lock-missing (a repo pack with no entry) and\n" +
		"    pack-lock-drift (a tree hash, gone entry, or sig mismatch) from the\n" +
		"    trusted-base snapshot; the lock joins the controls inventory, so an\n" +
		"    edited snapshot copy refuses controls-stale. Built-in packs are\n" +
		"    pinned by the binary and org packs live in personal settings, so\n" +
		"    neither is locked.\n" +
		"    test <name> runs the pack's offline eval cases. Use --dir <path> for an\n" +
		"    unpacked pack. Each evals/<case>/ directory has diff.patch,\n" +
		"    findings.json (review comments[]), and expect.json with rules[] and\n" +
		"    optional not_rules[]. A missing case file or required command skips\n" +
		"    the case; any skip or failure makes the command exit 1.\n" +
		"  upgrade [--version <v>] [--dry-run] [--json]\n" +
		"    explicit, channel-aware self-update. Installer-managed installs verify the\n" +
		"    release (cosign bundle when present, SHA-256 always), smoke-test, and\n" +
		"    atomically replace; npm/pnpm/bun/yarn/brew installs run the owning manager's\n" +
		"    global update (printed first, output streamed, no prompt); project-local,\n" +
		"    skill, sudo/admin, and unidentifiable installs refuse typed with the exact\n" +
		"    command. --dry-run prints the channel and planned action and touches nothing.\n" +
		"    Windows never overwrites the running exe: a helper replaces it after exit.\n" +
		"  uninstall [--remove-path] [--dry-run] [--json]\n" +
		"    remove an installer-managed verdict plus its receipt; only with --remove-path\n" +
		"    also remove the marked PATH line. npm/brew/project-local/skill copies refuse\n" +
		"    with the owning removal command.\n" +
		"  serve [--root <dir>] [--depth <n>] [--max-visits <n>] [--max-repos <n>]\n" +
		"      [--rediscover | --no-registry] [--host <addr>] [--port <n>] [--token <s>] [--check <tier>]\n" +
		"      [--max-bundles <n>] [--interval <duration>] [--sweep <duration>] [--no-watch] [--json]\n" +
		"    read-only loopback HTTP surface over the current repository, or every repository below --root.\n" +
		"    Binds 127.0.0.1 on a kernel-assigned port and prints one tokenised start URL.\n" +
		"    GET /?token=<t> sets verdict_session_<port> (HttpOnly, SameSite=Strict, Path=/v1)\n" +
		"    and 303s to /; /v1 refuses ?token= and requires that cookie or Authorization: Bearer.\n" +
		"    Routes: /v1/health, /v1/roots, /v1/bundles, /v1/roots/{root}/bundles/{bundle},\n" +
		"    .../findings/{sig}, /v1/events (--no-watch removes it). GET only, no CORS, no writes; Ctrl-C exits 3.\n" +
		"    Discovery defaults to depth 6, 20000 directory visits, 500 repositories;\n" +
		"    --rediscover forces a walk, --no-registry skips the 24-hour local cache.\n" +
		"  mcp [--read-only | --operator] (experimental)\n" +
		"    serve the review verbs as MCP tools over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"    stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  mcp config --agent <claude|cursor|codex|gemini|qwen|windsurf|opencode|goose|copilot>\n" +
		"       [--write] [--dry-run] [--json] [--read-only] [--operator] (experimental)\n" +
		"    print or idempotently merge the exact host config snippet; never prompts.\n" +
		"  Global presentation flags (any verb, before or after the verb, repo, and pr;\n" +
		"  global flags are stripped before the positionals are read):\n" +
		"  --color <auto|always|never>, --compact / --no-compact, --no-json.\n" +
		"  Global publish flags (same any-verb, any-position rule):\n" +
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
		"fetch-local/run/record/adopt/fix/check-tree/ready/gate/trend/index/backtest/learn/pack/init/" +
		"skill/upgrade/uninstall/serve/config: " +
		"stdout is exactly one JSON envelope (schema_version 1);\n" +
		"  all human progress goes to stderr. Errors emit {data:null,error:{code,message}} non-zero.\n" +
		"  Glossary: https://stainless-code.com/verdict/docs/glossary\n"
}

var bareVerbs = map[string]bool{
	"check-tree": true,
	verbDoctor:   true,
	"learn":      true,
	"init":       true,
	verbSkill:    true,
	"upgrade":    true,
	"uninstall":  true,
	"index":      true,
	"pack":       true,
	"serve":      true,
}

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
	if cmd == cmdBacktest {
		return false
	}
	return repo == "" || pr == ""
}

func unknownCmdMessage(cmd string) string {
	return "unknown cmd '" + cmd + "' (want " + verbsHint + ")"
}

func arityText(cmd string) string {
	if cmd == "next" {
		return "error: next requires <owner/repo> and <pr|local-name>\n" + usageText()
	}
	return usageText()
}
