package main

func usageText() string {
	return "usage: verdict demo|fetch|post|triage|report|next|show|verify|doctor|calibrate|" +
		"fetch-local|run|panel|record|adopt|ingest|fix|check-tree|ready|pr-wait|gate|threads|trend|risk|handoff|index|journal|" +
		"backtest|learn|pack|init|skill|upgrade|uninstall|mcp|serve|config|drain|acp [opts]\n" +
		"  demo [<public-github-pr-url>] [--agent <name>] [--keep] [--timeout <secs>] [--json]\n" +
		"    one review, zero setup: a public GitHub pull request by URL (gh when signed in for\n" +
		"    github.com, the anonymous API otherwise) or your own working tree when the argument\n" +
		"    is omitted. Every artifact lands in one disposable temp directory, removed on exit\n" +
		"    unless --keep, so nothing is written in your repository. The first installed agent\n" +
		"    CLI on PATH is used with a single finder; --agent names one instead. It records the\n" +
		"    review and prints it — posting is structurally out of reach. Exit 2 is usage, 1 a\n" +
		"    typed refusal, 3 a cancel.\n" +
		"  fetch <owner/repo> <pr> [--base-sha|--since <sha>] [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure] " +
		"[--index <ref|dir>] [--context <file|dir>] [--spec <file>] [--issue <ref>] [--ignore <glob>] [--no-default-excludes] " +
		"[--preview] [--consent-over-budget] [--show-prompts] [--chunk] [--json]\n" +
		"    write .verdict/state/<pr>/{PR.json,full.diff,toc.txt,roles.txt,reviews.json,inline.json[,incremental.diff]} " +
		"(PR.json normalized for non-github providers)\n" +
		"    --index <ref|dir> hands one index the operator built to the bundle: impact.json plus a\n" +
		"    capped \"## impact\" section on toc.txt (nothing is built or discovered; omit for no section).\n" +
		"    --preview prints the reviewed file list, every withheld section with its rule, the byte\n" +
		"    totals, and the resolved review budget tier (light|standard|deep), then stops: no bundle\n" +
		"    directory is written and no agent runs. Built-in secret paths stay withheld whatever\n" +
		"    --ignore and --no-default-excludes say.\n" +
		"    Provider scope: record/report/triage/show/next run on any provider bundle; post submits to GitHub,\n" +
		"    GitLab, and Forgejo (GitLab takes an approval but has no request-changes event — a bound\n" +
		"    request-changes posts as a comment; post.json records the withheld capability in withheld[]); live\n" +
		"    calibrate and ready run on GitHub and GitLab; adopt needs one verdict can post to (forgejo, github,\n" +
		"    gitlab); the rest refuse typed (provider-unimplemented; a local bundle has its own code — post/ready\n" +
		"    no-pr-target, calibrate no-threads, adopt bad-target). calibrate --probe is offline and runs on any\n" +
		"    bundle. Incremental re-review (--base-sha/--since) runs on GitHub and GitLab; other providers write\n" +
		"    incremental.diff.note.\n" +
		"    diffs over 512000 bytes refuse with a split, narrow, or exclude hint, then the two exclusions that\n" +
		"    fit the review (--ignore <glob>, or a .verdict/ignore pattern committed on the base revision; empty\n" +
		"    diffs warn, never fail). Paths after -- on fetch-local narrow the review to the matching files — they\n" +
		"    select, they never exclude.\n" +
		"    Deliberate exceptions only: REVIEW_MAX_BYTES=<n> (or the typed max_bytes default) overrides the cap.\n" +
		"    Linked issues: on GitHub, a PR body or title naming an issue with a closing keyword (\"Fixes #12\")\n" +
		"    pulls that issue's title and body into the bundle as a spec source, beside --spec. --issue <ref>\n" +
		"    (repeatable) adds one explicitly: owner/repo#12 or a GitHub issue URL (read with gh on any\n" +
		"    provider), or a bare #12 (GitHub only). An unreadable issue warns (issue-unavailable,\n" +
		"    issue-truncated, issue-over-cap) and never fails the fetch; a malformed ref refuses issue-invalid.\n" +
		"    A GitHub link that carries no closing keyword (the sidebar \"Linked issues\" form) needs\n" +
		"    --issue '#12'. Other hosts have no linked-issue read in this build: a PR body with closing keywords\n" +
		"    warns issue-unavailable (unsupported). An issue in another tracker (Linear, Jira, ...)\n" +
		"    reaches the review as text through --spec <file>, written by that tracker's own CLI.\n" +
		"    Chunking opt-in only: --chunk (or the typed chunk default) marks over-cap fetches for " +
		"chunked slicing (override chain wins; absent reads today's refusal).\n" +
		"    With REVIEW_BUDGET_USD (or the typed budget_usd default) set, fetch prints a pre-run\n" +
		"    cost estimate from comparable past runs and refuses projected-over-budget\n" +
		"    runs unless --consent-over-budget is given (consent lands in budget.json,\n" +
		"    surfaced by report --cost). Unset: estimate off, fetch as today.\n" +
		"  fetch-local <owner/repo> <name> [--staged] [--range A[..|...]B] [--base R]\n" +
		"        [--stash [n]] [--worktree] [--scan <path>] [--index <ref|dir>] [--context <file|dir>] [--spec <file>] " +
		"[--ignore <glob>] [--no-default-excludes] [--preview]\n" +
		"        [--consent-over-budget] [--allow-head-controls] [--show-prompts] [--chunk]\n" +
		"        [-- paths...] [--json]\n" +
		"    local review bundle from git (no GitHub, no PR): writes\n" +
		"    .verdict/state/local-<name>/{source.json,full.diff,toc.txt,roles.txt}.\n" +
		"    --worktree checks the review target into a temporary isolated worktree and\n" +
		"    builds the bundle from that checkout (removed on exit, even on refusal); without\n" +
		"    it the bundle builds from the live tree exactly as before.\n" +
		"    --scan <path> (repeatable) reviews files instead of a git diff: a file or a\n" +
		"    directory inside the repository becomes an added-file diff, so a clean tree needs\n" +
		"    no change to review (--scan . reads the whole repository). .git/, .verdict/,\n" +
		"    symlinks, and binary content are skipped, while the built-in secret paths, ignore\n" +
		"    layers, and cap/chunk gates apply exactly as in a diff run. It refuses a path\n" +
		"    outside the repository or through a symlink, and cannot combine with a diff\n" +
		"    source, --worktree, or -- paths.\n" +
		"    Same diff cap as fetch (512000 bytes, overridable as above); untracked/binary exclusions warn, never fail.\n" +
		"    Same budget gate as fetch (estimate + --consent-over-budget); unset: as today.\n" +
		"    downstream verbs address it as local-<name>; post and live calibrate refuse it, while\n" +
		"    calibrate --probe grades it offline.\n" +
		"    --show-prompts (fetch and fetch-local) renders the assembled agent prompt —\n" +
		"    the binary's review Procedure (PROCEDURE.md + prompts/system.md + prompts/review.md +\n" +
		"    prompts/diagrams.md, verbatim) + trust-boundary\n" +
		"    prologue + nonce-fenced bundle meta + toc.txt + roles.txt + full.diff +\n" +
		"    kb.txt/sensitive.txt/panel when present (one fence per file, in that order) —\n" +
		"    to stdout (human; progress moves to stderr) or data.prompt_dump (--json).\n" +
		"    No agent runs, no network beyond the fetch, no store writes beyond the bundle.\n" +
		"  run <owner/repo> <pr|local-name>\n" +
		"      --agent <adal|agy|amp|auggie|autohand|bob|claude|cline|codearts|codebuddy|\n" +
		"        codex|commandcode|continue|copilot|cortex|crush|cursor|dcode|devin|dexto|\n" +
		"        droid|forge|fx|gemini|goose|grok|hermes|jazz|junie|kilo|kimchi|kimi|kiro|\n" +
		"        kode|mcode|neovate|openclaw|opencode|pa|pi|qoder|qodercn|qwen|rovodev|\n" +
		"        vibe|zcode>\n" +
		"      [--finders <1|2, default 2>] [--budget <auto|light|standard|deep>] [--recipe <slug>]\n" +
		"      [--stage <n>] [--timeout <secs, default 300>] [--rounds <n, default 2>]\n" +
		"      [--print] [--resume] [--allow-shell] [-- <agent args>...] [--json]\n" +
		"    launch YOUR agent as two isolated whole-diff finders with opposite-finder cross-checks.\n" +
		"    --finders 1 restores one finder; --print/--stage require it. Recipes supply declarative context.\n" +
		"    --resume continues the two-finder execution launcher.json records instead of\n" +
		"    starting a new one: it verifies the recorded fingerprint (full.diff, run.json and the\n" +
		"    controls it seals, prompts_hash, agent, finder count) and the bound artifacts, reuses\n" +
		"    every finder and cross-check that still passes, re-runs only the incomplete phases,\n" +
		"    and fails closed with resume-drift or cannot-resume before launching anything.\n" +
		"    Discovery is read-only: every launch -- finders, cross-checks, repair rounds, drain --\n" +
		"    carries the row's documented tool restrictions (claude: --disallowedTools=Bash,PowerShell, bob:\n" +
		"    --disable-tool-groups execute,mcp, pi: --exclude-tools bash,powershell), which block shell\n" +
		"    execution while leaving file reads and the review.json write. --allow-shell restores the\n" +
		"    row's own tools; your own tool flag after '--' replaces the restriction rather than\n" +
		"    duplicating it. verify --sandbox is untouched.\n" +
		"    --budget auto classifies the diff before launching: light runs one finder, standard and\n" +
		"    deep run two with cross-checks, and a sensitive path escalates to deep. --finders N always\n" +
		"    wins; the tier and its reasons land in launcher.json and --json data.budget.\n" +
		"    Each finder requires one fresh review.json (absent refuses no-review-artifact, older\n" +
		"    than the launch refuses stale-review-artifact), names the next command, never posts, and\n" +
		"    reads the agent's prose only through the shape checker's problem list. With --rounds N\n" +
		"    (1-5) an invalid review.json re-prompts the agent with the checker's fix list, up to N\n" +
		"    launch-to-check rounds per finder; --rounds 1 disables repair. Evidence gaps ride in that same list: a\n" +
		"    wrong quote gets one repair round, and a review whose evidence stays insufficient after\n" +
		"    the rounds refuses review-insufficient instead of publishing an unbacked approval.\n" +
		"    The agent inherits your environment (your key) and may use the network; verdict\n" +
		"    itself makes no calls.\n" +
		"    Operators can define more names under the 'agents' key of their own settings\n" +
		"    file (XDG_CONFIG_HOME/verdict/settings.json); a repository config cannot.\n" +
		"    A flag that takes the prompt places it immediately before the directive.\n" +
		"    A binary whose name clashes with another tool answers a harmless help probe\n" +
		"    before launch; a mismatch refuses agent-mismatch without starting an agent.\n" +
		"    A non-zero agent exit is the process exit code (agent-failed); the watchdog\n" +
		"    timeout (agent-timeout), a missing binary (agent-missing), and a spawn failure\n" +
		"    (agent-spawn-failed) refuse typed with a hint. Provenance lands in\n" +
		"    .verdict/state/<bundle>/launcher.json (agent, binary, argv shape, timings, exit)\n" +
		"    and the full child output in .run.out. --print renders the exact command and\n" +
		"    exits 0 without spawning; everything after '--' goes to the agent unchanged.\n" +
		"    --print refuses options outside the safe model/cost display allowlist.\n" +
		"    run passes a short tool-generated directive, never bundle bytes.\n" +
		"    --recipe names a pipeline declared in .verdict/recipes.json at the PR base (default:\n" +
		"    the built-in 'default'), records it in data.recipe and launcher.json, and declares\n" +
		"    lens, model, and budget hints the agent may follow; verdict dispatches no stage.\n" +
		"    --stage <n> (with --print and --recipe) renders one stage's command instead of the\n" +
		"    whole pipeline: its declared model hint becomes the agent's model argument and its\n" +
		"    directive names that stage and the review.json it reads and writes, so your own CI\n" +
		"    chains the stages under different models; verdict still launches nothing and never\n" +
		"    sequences them.\n" +
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
		"  ingest <owner/repo> <pr> --from sarif <file|-> [--json]\n" +
		"    import OASIS SARIF 2.1.0 results from any static analyzer into the target bundle's\n" +
		"    findings.json through the shared dedupe, hunk anchoring, and path-exclusion\n" +
		"    withholding paths. No network, no URI followed, input capped at 8 MiB. A result the\n" +
		"    pinned diff cannot anchor (no location, no usable line, a path outside the\n" +
		"    repository, or a line the changed hunks do not carry) comes back in data.skipped[]\n" +
		"    with a typed reason instead of being dropped or stored at line 0; an anchored\n" +
		"    finding grades pointed against the verified diff line, never verified.\n" +
		"    --sarif <file|-> is the same source under its older spelling.\n" +
		"  fix <owner/repo> <local-slug|pr> [--sig <s> | --all|--selected [--min-severity <s>]\n" +
		"        [--verify-cmd <cmd>] [--paths <glob>]... | --test <sig> [--commit]] [--json]\n" +
		"    one bounded pass over a local bundle: picks the top open inline sig\n" +
		"    (or --sig), prints finding + kb.txt context with verify/land/re-run\n" +
		"    instructions. Max 3 passes per sig (ledger .verdict/state/<bundle>/fix.json);\n" +
		"    touching files outside the anchor stops the loop (scope-escape).\n" +
		"    --all takes one pass on every open inline finding at or above --min-severity\n" +
		"    (default important; off reads no floor) and names each sig's outcome: ready,\n" +
		"    ceiling, scope-escape, or no-anchor. It needs the command that proves the\n" +
		"    passes: --verify-cmd <cmd>, or verifyCmd in the bundle's patch.json.\n" +
		"    --paths <glob> (repeatable) scopes the batch to matching finding paths.\n" +
		"    --selected takes the same batch shape over the sigs a github fetch recorded in\n" +
		"    the bundle's selected.json — the root review comments a human reacted 👍 to —\n" +
		"    and prints the selection it read. It is the one batch that runs on a PR bundle:\n" +
		"    absent selected.json refuses missing-selection, and a selection that names no\n" +
		"    open inline finding refuses no-selected-findings. Its default floor is off, so\n" +
		"    the reaction, not the severity, decides; --min-severity narrows it.\n" +
		"    --test <sig> promotes instead of passing: it copies one reproduced inline\n" +
		"    finding's retained artifact into the worktree at\n" +
		"    .verdict/tests/<sig>-<hash>.<ext> with the receipt command rewritten, and\n" +
		"    writes no ledger. --commit stages that one file and commits it as\n" +
		"    'test: regression for <sig>' through a pathspec commit, so work already\n" +
		"    staged in the index stays staged and out of the commit. It needs --test.\n" +
		"    An existing target holding different bytes refuses (test-conflict), and so\n" +
		"    does a .verdict or .verdict/tests that is a symlink.\n" +
		"    Every pass writes fix.patch beside the ledger — the worktree's diff against HEAD,\n" +
		"    never applied and never pushed — and refuses (fix-patch-secret) rather than write\n" +
		"    a secret-shaped value into the bundle.\n" +
		"  post  <owner/repo> <pr> [--approve=true|false] [--request-changes] [--bind] [--force]\n" +
		"        [--review-file .verdict/state/<pr>/review.json] [--summary comment|off] [--dry-run]\n" +
		"        [--require-coverage] [--min-severity critical|important|informational|off]\n" +
		"        [--allow-secret-values] [--json]\n" +
		"    validate anchors (partitioned, keep-biased), embed suggestions, merge findings.json,\n" +
		"    guard stale-SHA + duplicates, post exactly one review.\n" +
		"    --summary comment maintains one separate PR-level summary comment (updated in place).\n" +
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
		"        [--output <path>] [--sarif <path>] [--include-withheld] [--cost] [--risk] " +
		"[--check <critical|important|informational>] [--compare <bundle-dir|pr|slug>] [--completeness] " +
		"[--max-risk <low|medium|high|critical>] [--policy <path>] " +
		"[--live auto|strict|skip] [--json]\n" +
		"    pure read over the store: ranked findings + verdict + counts. Advisory runs\n" +
		"    touch no network; gate runs (--check/--completeness) end with one live-thread recheck read.\n" +
		"    --sarif writes a SARIF 2.1.0 file (severity→level, category in properties, sig as\n" +
		"    fingerprint) for scanners/CI.\n" +
		"    --policy replays the store under an alternative policy file: the same findings, recorded\n" +
		"    checks, and receipts, so only the policy differs. Prints the gate and approval under the\n" +
		"    captured policy and under the candidate, plus every dimension the candidate added,\n" +
		"    removed, or moved. Writes no artifact, contacts no host, and exits 0 whatever the\n" +
		"    candidate decides — so the gate flags and --compare are refused rather than silently\n" +
		"    swallowed.\n" +
		"    --cost adds spend by effort tier. Captured two-finder runs sum bound phase usage;\n" +
		"    missing phase tokens or USD stay null (unknown), never zero.\n" +
		"    Consented over-budget receipts from .verdict/state/<pr>/budget.json surface here when present.\n" +
		"    --risk prints the advisory risk assessment (claimed risk or unknown, deterministic store floor\n" +
		"    inputs, and reported = max(claimed, floor)).\n" +
		"    --max-risk <level> gates on reported risk (exits 1 when exceeded or unknown; exit 0 pass,\n" +
		"    exit 2 usage, exit 3 tool error).\n" +
		"    Every gate run also carries the evidence dimension: pass needs the receipt\n" +
		"    recorded for the fetched HEAD, an insufficient receipt fails, and a\n" +
		"    missing or stale one reads unknown and fails the gate.\n" +
		"    --check <tier> opts into gating (same output, machine exits): 0 no open\n" +
		"    findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool\n" +
		"    error (missing/corrupt store). `--category <cat>` filters the ranked\n" +
		"    findings; `--check <tier> --category <cat>` gates only that class (an unknown\n" +
		"    category is a usage error). Untagged findings never meet a tier; the\n" +
		"    gate counts open findings only, whatever --status/--kind show. Without\n" +
		"    --check the run stays advisory: exit 0 with findings listed (typed\n" +
		"    errors exit 1, usage exits 2).\n" +
		"    --compare groups the same stable finding signatures against a prior bundle (a directory, or\n" +
		"    a PR/local-<name> in the same state directory) into new, persisting, resolved, and not\n" +
		"    reviewed. A missing finding reads resolved only when the current bundle proves the reviewer\n" +
		"    saw it (attested coverage of its hunk) or a provider PR's full diff no longer touches its\n" +
		"    file; an incremental --since delta, an excluded file, an unattested hunk, a path-filtered\n" +
		"    local bundle, and a missing snapshot all read not reviewed, and each row names its reason.\n" +
		"    A baseline that is missing, symlinked, corrupt, unrecorded, the same bundle, or from another\n" +
		"    repository refuses typed (bad-compare, missing/unsafe/corrupt/unrecorded-compare-bundle).\n" +
		"    Advisory only: it never changes the gate or the exit code.\n" +
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
		"        [--sandbox] [--env-allowlist <name[,name...]>] [--json]\n" +
		"    With neither --group nor --command, execute patch.json verifyCmd with a watchdog and\n" +
		"    record exit + tail in the patch record. --group and --command are required together:\n" +
		"    --command runs for the named deep plan group at the reviewed head and writes\n" +
		"    validation-<group>.json (patch.json stays untouched).\n" +
		"    Child env is default-deny (allowlist + LC_*; secret-named vars dropped unless opted in\n" +
		"    via --env-allowlist or base-snapshotted .verdict/verify-env; dropped names warn).\n" +
		"    Runs in its own process group (timeout kills the tree); known secret values\n" +
		"    are redacted from the recorded tail.\n" +
		"    --sandbox runs the command under an enforced sandbox: bwrap on Linux (no network,\n" +
		"    empty ~/.aws and ~/.ssh, a writable reviewed root, a private /tmp), and\n" +
		"    sandbox-exec on macOS (no network, loopback included; credentials stay visible).\n" +
		"    A host that cannot enforce it refuses sandbox-unavailable and runs nothing.\n" +
		"    Runs the agent's claimed proof — exit code becomes machine fact. No GitHub I/O.\n" +
		"    Destructive, networked, credential-reading, and repo-escaping commands\n" +
		"    refuse before execution as verify-destructive, verify-networked,\n" +
		"    verify-credential-read, and verify-repo-escape; a command word built from\n" +
		"    a variable refuses as verify-indirection.\n" +
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
		"  pr-wait <owner/repo> <N> --until merged|closed|checks-green|checks-done|review-changed --timeout D\n" +
		"    [--once] [--interval D] [--json] [--provider github|gitlab|forgejo]\n" +
		"    Foreground wait: timeout 1s–30m; interval >=1s. Exit 0 on condition,\n" +
		"    exit 1 with wait-timeout or condition-unmet data; no local writes.\n" +
		"\n" +
		"  ready <owner/repo> <pr> [--json] [--check] [--filter] [--wait-for build --timeout D]\n" +
		"    advisory by default (exit 0): CI state, open threads, draft/conflict,\n" +
		"    description completeness. Prints blockers with actions + recommendation\n" +
		"    (review/wait/nudge-author). GitHub and GitLab PR bundles; local bundles refuse\n" +
		"    (no-pr-target) and other providers refuse typed (provider-unimplemented).\n" +
		"    --check opts into gating (same output, machine exits): 0 ready, 1 blockers\n" +
		"    or wait, 2 usage, 3 tool error. Without --check the run stays advisory:\n" +
		"    exit 0 with blockers listed (typed errors exit 1, usage exits 2).\n" +
		"    --filter applies the closed trigger in .verdict/config.json at the PR base\n" +
		"    before any live read: a non-matching PR refuses filtered-out naming the\n" +
		"    predicate; both outcomes are recorded in the bundle's run.json.\n" +
		"  gate <owner/repo> <pr> [--max-severity critical|important|informational] [--require-head]\n" +
		"        [--live auto|strict|skip] [--coverage require|warn] [--json]\n" +
		"    one composed local gate over the dimensions report already computes: open\n" +
		"    findings at-or-above a tier (--max-severity), a completed post for the fetched\n" +
		"    HEAD (--require-head), the stored diff-coverage receipt for that HEAD\n" +
		"    (--coverage, default require), live unresolved threads (--live, default auto),\n" +
		"    the captured compliance verdict when the bundle carries a typed policy, the\n" +
		"    spec checks the stored record graded (always on: a violated check withholds\n" +
		"    the derived approval, so the gate fails on it), and\n" +
		"    the evidence receipt for the gated HEAD (always on: no gate passes without it).\n" +
		"    At least one dimension is required; --live and --coverage alone are not.\n" +
		"    Exits 0 pass, 1 gate failed or unknown, 2 usage, 3 tool error; a gate failure\n" +
		"    carries data.passed false with error null. data.dimensions[] lists findings,\n" +
		"    head, coverage, live_threads, compliance, risk, spec, evidence with id, status,\n" +
		"    detail, count.\n" +
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
		"  drain <owner/repo> <pr> [--provider <p>] [--max-threads <1-100>] [--json]\n" +
		"        [--agent <name> [--timeout <secs>] [-- <agent args>...]] [--post] [--dry-run]\n" +
		"    answer every review thread waiting on verdict: refresh threads, then write\n" +
		"    .verdict/state/<pr>/drain.json with the pending list, typed skips, and a plan digest.\n" +
		"    Zero pending is success and launches no agent. --max-threads defaults to 20;\n" +
		"    older questions are taken first and the rest are deferred. --agent drafts one\n" +
		"    drain.replies.json through the run launcher. --post validates those drafts,\n" +
		"    re-reads threads, posts one marked reply per draft, and appends replies.json\n" +
		"    after each write; withdraw also resolves its thread. GitHub and GitLab carry\n" +
		"    replies; other hosts refuse provider-unimplemented. A failed write records\n" +
		"    failed[] and refuses drain-incomplete so a re-run finishes the rest. A\n" +
		"    truncated feed or unreadable login refuses drain-failed before any write.\n" +
		"    --dry-run requires --post: it runs every post gate and live read, writes\n" +
		"    drain.dryrun.json with planned[] (final marked bodies) and skipped[], and\n" +
		"    posts nothing. replies.json, drain.json and threads.json keep their bytes.\n" +
		"    --agent --post --dry-run plans, drafts, then previews. Human output reads\n" +
		"    `» N planned, M skipped (dry run; nothing posted) -> <path>`.\n" +
		"  trend <owner/repo> [--rules] [--overlap] [--incidents] [--resolution] [--json]\n" +
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
		"    --incidents joins .verdict/incidents.jsonl to the bundle stores and reports\n" +
		"    the incident miss rate: an incident is a hit when one non-false-positive\n" +
		"    finding covers one of its paths (exact, or the finding under it by\n" +
		"    directory), a miss otherwise. A bundle whose store will not read is\n" +
		"    unmeasured, out of the rate, never a miss; miss_rate is null until something\n" +
		"    measures. Default output is byte-identical without the flag.\n" +
		"  risk <owner/repo> [--status open|all] [--limit <n, default 50, max 200>] [--json]\n" +
		"    advisory triage queue over every bundle under .verdict/state (no host read,\n" +
		"    nothing written): each row's recomputed band, its 0-100 score (reach 35,\n" +
		"    churn 25, category 25, rung 15), and whether it needs a human. Rows sort by\n" +
		"    band, then score, then bundle name; --status open keeps live bundles and\n" +
		"    names the merged/closed ones it skipped, --status all keeps everything. A\n" +
		"    bundle with no findings.json is queued, never dropped; a missing or unusable\n" +
		"    impact.json reads reach unmeasured with a counted warning. Gating one bundle\n" +
		"    stays report --max-risk.\n" +
		"  handoff <owner/repo> <pr> [--min-severity <s, default important>] [--paths <glob>]...\n" +
		"        [--format md] [--json]\n" +
		"    one markdown projection of the open inline findings fix would take (no host\n" +
		"    read, nothing written): per finding its sig, path:line, severity, rigor, fix\n" +
		"    text, cited kb rules, and the verify command the bundle records, plus the\n" +
		"    brief fix prints for it, verbatim from the same builder. Selection is the\n" +
		"    fix --all selector: open inline at or above --min-severity, narrowed by\n" +
		"    repeatable portable --paths globs. Nothing at the floor refuses\n" +
		"    no-open-findings, naming the floor and the globs; --json carries the same\n" +
		"    rows and the same brief text, and both open with the untrusted-data\n" +
		"    caution: finding prose quotes PR content an attacker controls. --format\n" +
		"    md wins over REVIEW_OUTPUT_FORMAT=json (flags outrank env); both --json\n" +
		"    and --format md on one command line is a usage error.\n" +
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
		"  backtest reverts [--since <90d|12w|18mo|2y|<ref>>] [--until <ref>] [--json]\n" +
		"    record the window's revert commits as memory, not as candidates: subject\n" +
		"    Revert \"...\", no blame, no model call. --since defaults to 12mo and --until to\n" +
		"    HEAD. Writes .verdict/reverts.jsonl (tracked, one row per revert, upserted by\n" +
		"    sha so an older window survives a narrower scan) and .verdict/reverts.md, where\n" +
		"    the subject and paths are quoted inside an untrusted fence. A revert of a\n" +
		"    merge, one wider than 200 files or 5000 lines, a binary one, and one git cannot\n" +
		"    read are counted by reason, never refused. Attach one deliberately with\n" +
		"    learn --incident.\n" +
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
		"  backtest fixtures [--fixture <id>] [--recordings <dir>] [--json]\n" +
		"    grade the shipped recordings against hand-known defects: recall per run,\n" +
		"    mean and union over N, bonus union, false-green rate, hallucination rate\n" +
		"    and review-insufficient count. Read-only: no agent, no host, no corpus\n" +
		"    store. --fixture limits the report to one catalog id; --recordings is an\n" +
		"    opt-in root of live recordings to score, relative to the checkout by\n" +
		"    default. The eight shipped recordings are always the calibration, even\n" +
		"    with --recordings: 8/8 rows must reproduce. Writes\n" +
		"    .verdict/backtest/fixtures/report.json. Rates describe recorded reviews,\n" +
		"    not a model claim.\n" +
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
		"  learn --incident <bundle> --sha <merge> --path <path> [--note <text>] [--url <url>]\n" +
		"        [--at <RFC3339>] [--dry-run] [--json]\n" +
		"    attach one revert or incident to a recorded bundle in\n" +
		"    .verdict/incidents.jsonl (one row per bundle + sha, sorted; --path repeats);\n" +
		"    the bundle must already carry findings.json. trend --incidents measures the\n" +
		"    miss rate over those rows. A repeat attach reports existing and moves no\n" +
		"    byte; --dry-run writes nothing.\n" +
		"  init [--preset chill|standard|strict] [--ci github|forgejo|gitlab] [--agent <name>]\n" +
		"      [--key-secret <NAME>] [--comment-trigger] [--print] [--yes] [--force] [--json]\n" +
		"    guided offline scaffolder: writes the typed controls (tracked\n" +
		"    .verdict/config.json defaults, .verdict/kb.jsonl starter, and the\n" +
		"    .verdict/.gitignore /state/ line) with style presets, then runs\n" +
		"    check-tree validation. Bare on a terminal, it asks three questions\n" +
		"    (preset, workflow, agent) with the detected host and the installed agent\n" +
		"    CLIs that document a key variable as the defaults; Enter keeps each\n" +
		"    default. Any selection flag, --json, or a redirected stream keeps it\n" +
		"    non-interactive.\n" +
		"    Presets map to panel strictness + KB retirement thresholds:\n" +
		"    chill (no pinned panel, MIN_JUDGED=10 MAX_FP_RATE=0.7),\n" +
		"    standard (one pinned member, 5 / 0.5), strict (lead + 2 lenses, 3 / 0.3).\n" +
		"    Never touches network. Existing files print a preview and need\n" +
		"    --yes/--force (or an interactive confirm) before overwrite.\n" +
		"    --ci github also writes .github/workflows/verdict-review.yml and\n" +
		"    --ci forgejo writes .forgejo/workflows/verdict-review.yml: one workflow\n" +
		"    whose steps fetch, review with your agent, post, report, and gate on\n" +
		"    every pull request from this repository. It pins the release,\n" +
		"    reads VERDICT_VERSION and VERDICT_MODEL from repository variables,\n" +
		"    and reads the model key from one repository secret. Fork pull\n" +
		"    requests are skipped: they get no secrets.\n" +
		"    --ci gitlab writes .gitlab/ci/verdict-review.yml as a merge-request\n" +
		"    pipeline and, only when the project has no root pipeline, a minimal\n" +
		"    .gitlab-ci.yml that includes it. An existing root is never touched,\n" +
		"    with or without --force; the include to add is printed instead.\n" +
		"    --comment-trigger adds GitHub's opt-in `/verdict` pull-request comment\n" +
		"    re-run: issue_comment on a pull request, gated on a maintainer's\n" +
		"    comment body and keyed so any other comment cancels nothing. It needs\n" +
		"    --ci github (the event is GitHub's) and never checks out a fork head.\n" +
		"    --agent names the agent to launch (default claude, any run --agent\n" +
		"    name); --key-secret overrides the secret name, which defaults to the\n" +
		"    agent's documented key variable. --print prints the workflow and\n" +
		"    writes nothing.\n" +
		"  skill [show [<path>]|files|status|install|uninstall] [--format md|json] [--dir <skills-dir>|--agent <name>...]\n" +
		"        [--detect] [--diff] [--force] [--dry-run] [--json]\n" +
		"    serve, inspect, or remove the skill embedded in this binary (never a skew):\n" +
		"    show prints PROCEDURE.md (default --format md), show <path> prints one\n" +
		"    embedded file byte-identical, --format json emits one envelope with\n" +
		"    name/version/files, files lists every embedded path.\n" +
		"    status is a read-only drift report per target (create/update/keep/\n" +
		"    blocked) comparing the installed stub_version and the receipt against\n" +
		"    this binary; it exits 0 in sync and 1 when drifted. A matching\n" +
		"    stub_version forgives SKILL.md byte edits only; NOTICE.md and\n" +
		"    scripts/verdict still drift when missing or changed. A missing or\n" +
		"    different version reports SKILL.md. --diff adds a capped unified diff.\n" +
		"    install atomically writes SKILL.md, NOTICE.md, and scripts/verdict\n" +
		"    plus a .verdict-managed receipt; the Procedure and schemas stay in the\n" +
		"    binary. It previews create/update/keep/blocked, never prompts, and\n" +
		"    refuses a foreign target unless --force (foreign files are untouched).\n" +
		"    uninstall removes only the payload paths the receipt owns, then the\n" +
		"    receipt; foreign files survive, a target without a receipt refuses\n" +
		"    unless --force, and --dry-run deletes nothing.\n" +
		"    --agent repeats on install, status, and uninstall: targets are deduped\n" +
		"    and handled in argv order. --detect installs or reports every known\n" +
		"    agent whose skills dir already exists and is non-empty; with none\n" +
		"    present it refuses typed and asks for --agent <name>.\n" +
		"    In a skill copy, scripts/verdict resolves VERDICT_BIN, then\n" +
		"    <skill>/bin/verdict, then verdict on PATH; otherwise run the\n" +
		"    one-time verified\n" +
		"    <skill>/scripts/verdict bootstrap (no review-time fetch).\n" +
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
		"  journal <owner/repo> <pr|local-name> [--json]\n" +
		"    read the bundle's append-only invocation journal (<state>/verdict/journal/<repo-key>/<pr>.jsonl), folding\n" +
		"    start/end lines into ordered steps, listing unfinished (crashed or in-flight) calls\n" +
		"    and content-digest chain breaks between steps. Read-only: takes no lock, writes nothing;\n" +
		"    an absent journal on an existing bundle exits 0 with zero steps.\n" +
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
		"  acp [--root <dir>] [--check <tier>] [--max-bundles <n>] [--json]\n" +
		"    serve read-only queries as ACP v1 over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"    stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"    Protocol: initialize, ping, verdict/bundles/list, verdict/findings/list,\n" +
		"    verdict/findings/get, verdict/findings/copy, $/cancel_request.\n" +
		"    No daemon, no HTTP, no model calls, no writes; exits when stdin closes.\n" +
		"  Global presentation flags (any verb, before or after the verb, repo, and pr;\n" +
		"  global flags are stripped before the positionals are read):\n" +
		"  --color <auto|always|never>, --compact / --no-compact, --no-json.\n" +
		"  Global publish flags (same any-verb, any-position rule):\n" +
		"  --footer / --no-footer, --attribution [off|product|full] / --no-attribution,\n" +
		"  --attribution-text <text>, --copyright <text>, --logo / --no-logo,\n" +
		"  --logo-url <url>, --overview / --no-overview,\n" +
		"  --diagram-structural / --no-diagram-structural, --fix-prompt / --no-fix-prompt.\n" +
		"  Flags win over the settings file, which wins over env\n" +
		"  (REVIEW_OUTPUT_FORMAT, REVIEW_LAYOUT_COMPACT, REVIEW_COLOR_MODE,\n" +
		"  REVIEW_PUBLISH_FOOTER, REVIEW_PUBLISH_ATTRIBUTION,\n" +
		"  REVIEW_PUBLISH_ATTRIBUTION_TEXT, REVIEW_PUBLISH_COPYRIGHT,\n" +
		"  REVIEW_PUBLISH_LOGO, REVIEW_PUBLISH_LOGO_URL, REVIEW_PUBLISH_OVERVIEW,\n" +
		"  REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL, REVIEW_PUBLISH_FIX_PROMPT),\n" +
		"  which wins over defaults. The settings file holds presentation (output,\n" +
		"  layout, color) plus publish (footer receipt + leveled attribution + logo +\n" +
		"  overview + diagrams + fix prompt) namespaces\n" +
		"  (~/.config/verdict/settings.json, $XDG_CONFIG_HOME respected).\n" +
		"  --json on demo/fetch/post/triage/report/next/show/verify/doctor/calibrate/" +
		"fetch-local/run/record/adopt/fix/check-tree/ready/gate/trend/index/journal/backtest/learn/pack/init/" +
		"skill/upgrade/uninstall/serve/config/acp: " +
		"stdout is exactly one JSON envelope (schema_version 1);\n" +
		"  all human progress goes to stderr. Errors emit {data:null,error:{code,message}} non-zero.\n" +
		"  Glossary: https://stainless-code.com/verdict/docs/glossary\n"
}

// optionals take at most one optional positional and no repo/pr pair, so the
// split must not read the first token as a repository and the second as a PR.
var optionals = map[string]bool{cmdDemo: true}

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
	verbACP:      true,
}

// repoOnlyVerbs take one <owner/repo> and nothing else positional: every other
// argument is one of their own flags, so the split must not read the second
// token as a PR.
var repoOnlyVerbs = map[string]bool{cmdTrend: true, cmdRisk: true}

func arityUsage(cmd, repo, pr string) bool {
	if cmd == "" {
		return true
	}
	if bareVerbs[cmd] || optionals[cmd] {
		return false
	}
	if repoOnlyVerbs[cmd] {
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
