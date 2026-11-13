// GENERATED from skills/verdict/schemas/envelope.schema.json ($defs) by clients/ts/scripts/gen-ts-types.mjs. Do not edit.
/** Refusal shape from fail(): data is null, message carries the cause plus a try: hint, candidates rides only on the oversize-fetch refusal. */
export interface RefusalErrorBody {
  code: string;
  message: string;
  candidates?: Array<Candidate>;
  retry_at?: string;
  /** Review-input problems in document order, errors first (at most 50); present on a bad-review-input refusal and on a repair-exhausted refusal, with the exact fix for each. */
  problems?: Array<Problem>;
  /** How many problems were past the 50-problem cap; 0 when the list is complete. */
  problems_truncated?: number;
  [key: string]: unknown;
}

export interface Candidate {
  kind: string;
  suggestion: string;
  [key: string]: unknown;
}

/** One review-input problem: its JSONPath in review.json, the published code, the level, the message, the exact fix, and an optional example of the accepted shape. --strict and controls strict_input promote warnings to errors. */
export interface Problem {
  /** JSONPath into review.json, for example $.comments[3].brief.questions. */
  path: string;
  /** Published problem-code enum; the envelope error.code stays bad-review-input. */
  code:
    | "anchor-outside-hunk"
    | "bad-enum"
    | "basis-replaced"
    | "enum-synonym"
    | "invalid-json"
    | "key-alias"
    | "missing-field"
    | "normalized-extracted"
    | "normalized-fence"
    | "normalized-trivia"
    | "out-of-range"
    | "path-not-in-diff"
    | "rigor-unset"
    | "scalar-to-array"
    | "unknown-field"
    | "wrong-type";
  /** error blocks record; warning lets it proceed and report what it did, unless strict input promotes it. */
  level: "error" | "warning";
  message: string;
  /** The exact change that clears the problem. */
  fix: string;
  /** Optional example of the accepted shape at this path. */
  example?: unknown;
  [key: string]: unknown;
}

/** What a clean dry run would record: comment and section counts plus anchors that would move to the body. */
export interface CheckSummary {
  comments: number;
  sections: number;
  anchors_moved_to_body: number;
  [key: string]: unknown;
}

export interface AnchorsSummary {
  kept: number;
  moved: number;
  [key: string]: unknown;
}

export interface FindingsCounts {
  open: number;
  deferred: number;
  addressed: number;
  [key: string]: unknown;
}

/** Ranked or merged finding locator shared by report items and post/record items. */
export interface FindingItem {
  sig: string;
  status: "open" | "addressed" | "wont-fix" | "false-positive" | "deferred";
  anchored?: (boolean | string) | null;
  locator?: string;
  [key: string]: unknown;
}

/** Review provenance triple: the tooling behind a run, so mixed-version histories never read as one series. The writer always emits all three keys; model is null when no cost record names one. */
export interface Provenance {
  tool_version: string;
  prompts_hash: string;
  model: string | null;
  [key: string]: unknown;
}

export interface CoverageSummary {
  percent: number | null;
  attested: number;
  total: number;
  files: number;
  exempt: number;
  gaps: number;
  [key: string]: unknown;
}

export interface VerificationSummary {
  verifiers: number;
  confirmed: number;
  contested: number;
  single: number;
  [key: string]: unknown;
}

/** report --json data: ranked read over the store. cost appears only with --cost, completeness only with --completeness, gate only when a gating flag (--max-severity, --require-head, --live) is set (gate itself always carries the coverage dimension), receipt keys only when budget.json parses. */
export interface ReportData {
  verification?: VerificationSummary | null;
  generated_at: string;
  dir: string;
  head: ReportHead;
  verdict: ReportVerdict;
  counts: ReportCounts;
  panel: ReportPanel;
  patch: unknown | null;
  output: string | null;
  filters: ReportFilters;
  items: Array<FindingItem>;
  cost?: ReportCost;
  completeness?: ReportCompleteness;
  compliance?: ReportCompliance | null;
  gate?: ReportGate | null;
  [key: string]: unknown;
}

export interface ReportHead {
  fetched: string | null;
  base: string | null;
  live: string | null;
  stale: boolean;
  [key: string]: unknown;
}

export interface ReportVerdict {
  open: number;
  deferred: number;
  addressed: number;
  terminal: number;
  [key: string]: unknown;
}

export interface ReportCounts {
  clean?: boolean;
  checked_files?: number;
  inline: number;
  section: number;
  anchored: number;
  unanchored: number;
  /** Whole-store finding counts by published category plus untagged; every key is always present with zero defaults. */
  by_category: unknown;
  [key: string]: unknown;
}

export interface ReportPanel {
  source: string | null;
  members: unknown[];
  [key: string]: unknown;
}

export interface ReportFilters {
  status: string;
  kind: string;
  /** The active --category filter, or null when the run did not filter by category. */
  category: string | null;
  [key: string]: unknown;
}

/** Summed rollup from cost.json plus per-chunk records; by_tier always carries all three tiers. */
export interface ReportCost {
  runs: number;
  runs_with_cost: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  /** Summed per-model cache_read_input_tokens plus cache_creation_input_tokens; totals stay input + output exactly. */
  cached_input_tokens?: number;
  est_usd: number | null;
  by_tier: CostTiers;
  receipts?: unknown[];
  [key: string]: unknown;
}

export interface CostTiers {
  cheap: CostTier;
  standard: CostTier;
  deep: CostTier;
  [key: string]: unknown;
}

export interface CostTier {
  runs: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  [key: string]: unknown;
}

export interface ReportCompleteness {
  reviewed: boolean;
  fetched_head: string | null;
  posted_head: string | null;
  /** True when the reviewed bundle identity and the posted post.json patch id share id and kind (rebase/amend with the same change); absent when either side has no patch id. */
  same_patch?: boolean | null;
  /** Patch id the bundle identity carried at review time; absent when the bundle had no identity. */
  reviewed_patch_id?: string | null;
  /** Patch id the completed post receipt carried; absent when the post predates patch identity. */
  posted_patch_id?: string | null;
  [key: string]: unknown;
}

/** The captured-policy compliance view: whether the bundle captured a policy and its provenance verified, how many findings blocked and how many required checks are still pending for the saved head, and whether the work therefore satisfies the policy. Unknown or pending never reads as satisfied. */
export interface ReportCompliance {
  policy: boolean;
  verified: boolean;
  /** Blocking findings under the captured clauses; never negative. */
  blockers: number;
  /** Applicable clauses whose reviewed material is withheld by an accepted privacy exclusion, so the clause cannot be satisfied by absence of findings; never negative. */
  conflicted: number;
  /** Required checks still pending for the saved head; never negative. */
  pending: number;
  /** Accepted conflicts and pending required work counted together as the standing gap tally; never negative. */
  gaps: number;
  satisfied: boolean;
  [key: string]: unknown;
}

/** Composed gate result: every requested dimension in findings, head, coverage, live_threads, compliance order. passed is true iff no dimension failed or read unknown, so a skip and an auto-posture unreachable probe stay neutral. */
export interface ReportGate {
  /** The gate verdict the exit code follows: exit 0 when true, exit 1 when false. */
  passed: boolean;
  dimensions: Array<GateDimension>;
  [key: string]: unknown;
}

/** One gate dimension: its id, verdict status, human detail text, and its own failing tally (null where the dimension has no count to report). */
export interface GateDimension {
  /** Which dimension read: findings (open findings at-or-above the --max-severity tier), head (a completed post for the fetched HEAD under --require-head), coverage (the stored diff-coverage receipt for that HEAD under --coverage, default require; a chunked bundle is graded on the chunk attestation its receipt stores), or live_threads (unresolved threads on the PR under --live). */
  id: "findings" | "head" | "coverage" | "live_threads" | "compliance";
  /** pass, fail, unknown (the dimension could not be read and the posture fails closed), or skip (not requested, or unreachable under --live auto). Only fail and unknown fail the gate. */
  status: "pass" | "fail" | "skip" | "unknown";
  detail: string;
  /** The dimension's own tally: offending findings for findings, unattested ranges for coverage (unattested chunks on a chunked bundle), versus-thread count for live_threads, null for head and for a skipped or unreachable live probe. */
  count: number | null;
  [key: string]: unknown;
}

/** post --dry-run --json data (dry_run true, findings carry the dry-run file) and real-post data (dry_run false, findings carry counts plus the accepted review object). */
export interface PostData {
  rearmed?: boolean;
  previous_head?: string;
  verification?: VerificationSummary | null;
  dry_run: boolean;
  event: string;
  commit_id: string;
  body: PostDataBody;
  anchors: AnchorsSummary;
  suggestions: PostDataSuggestions;
  findings: PostDataFindings;
  items: Array<FindingItem>;
  confidence: number | null;
  security_summary: string | null;
  payload_hash: string;
  /** Real-post only: the accepted GitHub review object. */
  review?: unknown;
  provenance?: Provenance;
  coverage?: CoverageSummary | null;
  [key: string]: unknown;
}

/** record --json data: the local-bundle merge (post stays refused, no PR target). */
export interface RecordData {
  dir: string;
  anchors: AnchorsSummary;
  findings: RecordDataFindings;
  confidence: number | null;
  security_summary: string | null;
  verification?: VerificationSummary | null;
  items: Array<FindingItem>;
  /** True only on record --dry-run; the run wrote no findings.json, record.json, or any new file. */
  dry_run?: boolean;
  /** Present only on a clean dry run: the review-input summary. */
  check?: CheckSummary | null;
  coverage?: CoverageSummary | null;
  /** Present on record --dry-run: the exact review object the checker read after safe repairs, as an object. */
  normalized?: unknown;
  /** Present on record --dry-run: every safe review.json rewrite applied, in document order. */
  repairs?: Array<Repair>;
  [key: string]: unknown;
}

/** One safe review.json rewrite: the published repair code and the JSONPath it touched. */
export interface Repair {
  /** The repair family: fence unwrap, prose extraction, scalar-to-array, key alias, enum synonym, or trivia removal. */
  code:
    | "enum-synonym"
    | "key-alias"
    | "normalized-extracted"
    | "normalized-fence"
    | "normalized-trivia"
    | "scalar-to-array";
  /** JSONPath of the repaired value in the normalized review. */
  path: string;
  [key: string]: unknown;
}

/** show --json data: item is the raw finding record in store key order; fix is the read-only machine repair directive built from that record plus the bundle's patch/verification history. Neither key adds execution, flags, or store writes. */
export interface ShowData {
  item: unknown;
  fix: ShowFix;
  [key: string]: unknown;
}

/** serve --json data (and the start envelope): one profile across health, roots, inbox, detail, and the start line, so every view-specific key is optional while partial, sources, and errors are always present. A product refusal is data null with the typed error; a corrupt bundle in a listing keeps its row with null counts and an errors[] entry, and any total over it is null and partial. */
export interface ServeData {
  version?: string;
  url?: string;
  listen?: string;
  read_only?: boolean | null;
  watch?: boolean | null;
  interval?: string;
  cache?: ServeCache | null;
  roots?: Array<ServeRoot>;
  truncated?: boolean | null;
  skipped?: ServeSkipped | null;
  discovery?: ServeDiscovery | null;
  cost?: ServeCostData | null;
  precision?: ServePrecisionData | null;
  root?: string | null;
  /** The detector that found this invalidation: tick for directory stamps or sweep for entry fingerprints. */
  via?: "tick" | "sweep";
  /** Bundle names whose views should be fetched again; a truncated event requires refetching the full inbox. */
  changed?: Array<string>;
  /** Bundle names removed from the served root. */
  removed?: Array<string>;
  rows?: Array<ServeRow>;
  counts?: ServeCounts | null;
  report?: ReportData | null;
  gate?: ReportGate | null;
  threads?: ThreadsFile | null;
  artifacts?: Array<ServeArtifact>;
  partial: boolean;
  sources: Array<ServeSource>;
  errors: Array<ServeErrorRow>;
  [key: string]: unknown;
}

/** One artifact a composed view read: the serving root id, the bundle name (empty for a root-level override fragment), the root-relative slash path, its SHA-256, and the byte count. Every inventory read is capped. */
export interface ServeSource {
  root: string;
  bundle: string;
  path: string;
  sha256: string;
  bytes: number;
  [key: string]: unknown;
}

/** One tolerated bundle failure: the root id, bundle name, machine code, and message. A row is never dropped for a refused bundle. */
export interface ServeErrorRow {
  root: string;
  bundle: string;
  code: string;
  message: string;
  [key: string]: unknown;
}

/** One served root: its 12-hex id, absolute path, and addressable bundle count. */
export interface ServeRoot {
  id: string;
  path: string;
  bundles: number;
  [key: string]: unknown;
}

/** The portfolio rollup over the inbox rows. Null when any input bundle refuses, so a partial set never reads as a smaller total. */
export interface ServeCounts {
  bundles: number;
  open: number;
  deferred: number;
  addressed: number;
  terminal: number;
  [key: string]: unknown;
}

/** One inbox row: the bundle identity, its post receipt's model, and the verdict counts. Every numeric count is null for a refused bundle, never zero-for-unknown. */
export interface ServeRow {
  root: string;
  bundle: string;
  dir: string;
  repo: string;
  pr: number | null;
  model: string | null;
  head: string | null;
  open: number | null;
  deferred: number | null;
  addressed: number | null;
  terminal: number | null;
  [key: string]: unknown;
}

/** One evidence-ledger row: the bundle-relative path, its presence, and the hash and size when present. */
export interface ServeArtifact {
  path: string;
  present: boolean;
  sha256: string | null;
  bytes: number | null;
  [key: string]: unknown;
}

/** The in-memory composition cache's size; derived state only, never written to disk. */
export interface ServeCache {
  entries: number;
  [key: string]: unknown;
}

/** Discovery counts directory symlinks not followed and unsupported .gitignore patterns skipped. */
export interface ServeSkipped {
  symlinks: number;
  gitignore_patterns?: number;
  [key: string]: unknown;
}

/** Whether the root list came from the registry. S1 reports cached false with an empty scanned_at. */
export interface ServeDiscovery {
  cached: boolean;
  scanned_at: string;
  [key: string]: unknown;
}

/** A cost view for one allowlisted dimension. Model rows carry tokens but no per-model price; corrupt inputs make totals null and partial true. */
export interface ServeCostData {
  by: "repo" | "week" | "model";
  rows: Array<ServeCostBucket>;
  totals: ServeCostTotals | null;
  [key: string]: unknown;
}

/** One repository, ISO week, or model bucket. runs counts record membership; model_runs counts model entries, which can be greater than runs for by=model. */
export interface ServeCostBucket {
  dimension: string;
  runs: number;
  model_runs: number;
  /** Null when any contributing model has no input-token measurement. */
  input_tokens: number | null;
  /** Null when any contributing model has no output-token measurement. */
  output_tokens: number | null;
  /** Null when either token measurement is unknown. */
  total_tokens: number | null;
  est_usd: number | null;
  [key: string]: unknown;
}

/** The rollup for a cost view. est_usd is null for by=model because cost records price only the whole run. */
export interface ServeCostTotals {
  runs: number;
  model_runs: number;
  /** Null when any contributing bucket has an unknown input-token measurement. */
  input_tokens: number | null;
  /** Null when any contributing bucket has an unknown output-token measurement. */
  output_tokens: number | null;
  /** Null when any contributing bucket has an unknown token measurement. */
  total_tokens: number | null;
  est_usd: number | null;
  [key: string]: unknown;
}

/** Precision from the stored threads feed. fixed is not measured and remains null; no rate is published. as_of comes from fetched_at. */
export interface ServePrecisionData {
  by: "category" | "tier";
  outcomes: Array<ServePrecisionBucket> | null;
  totals: ServePrecisionTotals | null;
  fixed: number | null;
  as_of: string | null;
  stale: boolean | null;
  next: string;
  [key: string]: unknown;
}

/** Counts from the calibrate join. fixed stays null without changed-file evidence. */
export interface ServePrecisionBucket {
  dimension: string;
  open: number;
  resolved_unfixed: number;
  unknown: number;
  fixed: number | null;
  [key: string]: unknown;
}

/** The stored-join outcome counts across all dimensions; fixed is always null. */
export interface ServePrecisionTotals {
  open: number;
  resolved_unfixed: number;
  unknown: number;
  fixed: number | null;
  [key: string]: unknown;
}

/** The bundle's threads.json feed; null when the bundle carries none. Shape shared with threads.schema.json. */
export interface ThreadsFile {
  schema_version: "1";
  provider: "github" | "gitlab" | "gitcafe" | "forgejo" | "bitbucket" | "azure";
  pr: number;
  head_sha: string;
  fetched_at: string;
  truncated: boolean;
  threads: Array<Thread>;
  [key: string]: unknown;
}

/** One root review thread or conversation comment plus its replies. */
export interface Thread {
  id: string;
  path: string;
  line: number | null;
  start_line: number | null;
  side: string | null;
  resolved: boolean;
  outdated: boolean;
  url: string | null;
  author: string;
  author_kind: "human" | "bot" | "verdict";
  finding_sig: string | null;
  kind: "issue-comment" | "review-comment";
  comments: Array<Comment>;
  [key: string]: unknown;
}

/** One comment inside a thread, body capped and flagged. */
export interface Comment {
  id: string;
  author: string;
  author_kind: "human" | "bot" | "verdict";
  body: string;
  truncated: boolean;
  created_at: string;
  [key: string]: unknown;
}

export interface ConfigData {
  mode: "explain" | "preview";
  trusted: boolean;
  source: string;
  values: Array<ConfigValue>;
  requirements: Array<ConfigRequirement>;
  plan: Array<ConfigPlanGroup>;
  plan_status: string;
  spend: ConfigSpend;
  privacy: Array<string>;
  checks?: Array<ConfigCheck>;
  shadowed?: Array<ConfigValue>;
  files?: Array<ConfigFile>;
  [key: string]: unknown;
}

export interface ConfigFile {
  path: string;
  old_path?: string;
  scopes: Array<string>;
  values: Array<ConfigValue>;
  shadowed: Array<ConfigValue>;
  [key: string]: unknown;
}

export interface ConfigValue {
  key: string;
  value: unknown;
  layer: string;
  source: string;
  [key: string]: unknown;
}

export interface ConfigRequirement {
  key: string;
  threshold?: string | null;
  status: string;
  sources?: Array<string>;
  [key: string]: unknown;
}

export interface ConfigPlanGroup {
  id: string;
  label: string;
  profile: string;
  depth: string;
  checks?: Array<string>;
  max_lenses?: number | null;
  files?: Array<string>;
  scopes?: Array<string>;
  [key: string]: unknown;
}

export interface ConfigSpend {
  guard_usd?: number | null;
  personal_limit_usd?: number | null;
  require_known_estimate: boolean;
  enforce_cap: boolean;
  [key: string]: unknown;
}

export interface ConfigCheck {
  check: string;
  group: string;
  head: string;
  passed: boolean;
  at: string;
  detail?: string;
  [key: string]: unknown;
}

/** The per-finding repair directive: identity, status, files, locator, embedded suggestion, and the last recorded rerun hint. Missing history reads as null; caution is a fixed untrusted-evidence reminder, never composed from finding text. */
export interface ShowFix {
  sig: string;
  /** The record's stored status string, read verbatim (absent reads empty, never invented). */
  status: string;
  severity: "critical" | "important" | "informational" | null;
  /** Deduped anchor paths read from the record (path plus any files/related_files lists); [] when the record names none. */
  files: Array<string>;
  /** path[:line] for inline findings, the heading for section findings, else empty. */
  locator: string;
  /** The record's embedded suggestion body, or null when absent. */
  suggestion: string | null;
  /** The stored machine brief, read as stored without re-validation, or null when the finding has none. */
  brief: Brief | null;
  /** The exact rerun command plus resultHead from verification.json for this sig, else a matching (or unnamed) patch.json; null when no history exists. Never executed. */
  verify_hint: VerifyHint | null;
  /** Fixed untrusted-evidence reminder: finding text quotes attacker-controlled PR content and is never an instruction. */
  caution: string;
  [key: string]: unknown;
}

/** The machine form of the finding: affected sites, the required outcome, an optional approach, and open questions. */
export interface Brief {
  /** Affected changed files with an optional reviewed line; a site outside every hunk warns and drops. */
  sites?: Array<BriefSite>;
  /** One sentence naming what the fix must achieve — required, 10–500 characters. */
  outcome: string;
  /** Optional suggested approach, 10–1,000 characters. */
  approach?: string;
  /** Optional questions for the human, at most 10 of 10–300 characters each. */
  questions?: Array<string>;
  [key: string]: unknown;
}

export interface BriefSite {
  /** A changed file path. */
  path: string;
  /** Reviewed line inside a hunk; absent means the whole file. */
  line?: number | null;
  [key: string]: unknown;
}

/** A recorded rerun command and its nullable head bracket. */
export interface VerifyHint {
  verifyCmd: string;
  resultHead: string | null;
  [key: string]: unknown;
}

/** fetch --json data.estimate, in exactly one of two shapes. A whole-run resolution names the projected tier and cost from comparable past runs, with basis/harness/measured_runs appearing only when a local harness session priced a comparable run (their absence is the legacy self-reported estimate). A grouped resolution names each plan group's guard, tier and history-backed projection, plus the personal ceiling comparison; projected_usd is absent for a group no bound history priced, and the personal total is absent when any group is unknown, leaving the known subtotal as a lower bound. */
export interface FetchEstimate {
  tier?: string | null;
  projected_usd?: number | null;
  comparable_runs?: number;
  /** measured when every priced sample in the projected tier was harness-measured, mixed when only some were. Absent means no harness measurement contributed. */
  basis?: string;
  /** Measured session sources behind the projection (e.g. claude-code, opencode); absent with basis. */
  harness?: Array<string>;
  /** How many priced samples in the projected tier came from a harness session; absent with basis. */
  measured_runs?: number;
  /** One entry per resolved plan group: its stable id, effort tier, its own guard when the plan resolved one, and projected_usd only when bound history priced it. */
  groups?: Array<FetchGroupEstimate>;
  /** The personal whole-run ceiling comparison: exact-total when every group is priced, known-lower-bound when some are unknown, no-personal-limit when no ceiling was set. limit_usd is absent when unset and never zero; total_usd is absent unless the aggregate is exact. */
  personal?: FetchGroupPersonal | null;
  [key: string]: unknown;
}

/** One plan group's public projection. projected_usd is omitted for an unknown group rather than reported as zero. */
export interface FetchGroupEstimate {
  group: string;
  effort: string;
  /** The group's resolved guard. Absent when the plan set none — never reported as zero. */
  guard_usd?: number | null;
  projected_usd?: number | null;
  over_guard: boolean;
  [key: string]: unknown;
}

/** The personal ceiling comparison. total_usd appears only with an exact aggregate, known_subtotal_usd only as a lower bound. */
export interface FetchGroupPersonal {
  /** The resolved personal ceiling. Absent when none was set — never reported as zero. */
  limit_usd?: number | null;
  basis: string;
  total_usd?: number | null;
  known_subtotal_usd?: number | null;
  [key: string]: unknown;
}

/** triage --json data: the finding that moved and its new status. dry_run appears only when the pass changed nothing. */
export interface TriageData {
  sig: string;
  status: "open" | "addressed" | "wont-fix" | "false-positive" | "deferred";
  dry_run?: boolean;
  [key: string]: unknown;
}

/** ready --json data: the gate verdict, every blocker with the action that clears it, and the live PR state the gate read. recommendation is review when nothing blocks, hold otherwise. */
export interface ReadyData {
  wait?: PrWaitData;
  ready: boolean;
  recommendation: string;
  blockers: Array<ReadyBlocker>;
  state: string;
  draft: boolean;
  mergeable: string;
  mergeStateStatus: string;
  ci: ReadyCI;
  threads_open: number;
  description_chars: number;
  [key: string]: unknown;
}

export interface PrWaitData {
  snapshot: PrWaitSnapshot | null;
  outcome: string;
  until: string;
  observations: number;
  [key: string]: unknown;
}

export interface PrWaitSnapshot {
  state: string;
  head: string;
  reviews: string;
  threads: string;
  checks_known: boolean;
  checks_done: boolean;
  checks_green: boolean;
  [key: string]: unknown;
}

/** One readiness blocker: the rule that fired, the detail that fired it, and the action that clears it. */
export interface ReadyBlocker {
  kind: string;
  detail: string;
  action: string;
  [key: string]: unknown;
}

/** The live CI check tally: failing rows out of every check the head reported. */
export interface ReadyCI {
  failing: number;
  total: number;
  [key: string]: unknown;
}

/** adopt --json data: the target bundle, its source, and how many findings were adopted and left open. */
export interface AdoptData {
  dir: string;
  from: string;
  adopted: number;
  open: number;
  [key: string]: unknown;
}

/** next --json data: the ranked finding to fix next, or null when the store has nothing open. item keeps every stored record key beside the ranked locator. */
export interface NextData {
  item: FindingItem | null;
  [key: string]: unknown;
}

/** fix --json data: one pass over the named finding, or the completion shape. The pass carries the finding identity, its anchors, the pass budget, and the open/fixed/stopped tallies; completion leaves those null and sets complete. */
export interface FixData {
  dir: string;
  ledger: string;
  sig: string | null;
  locator: string | null;
  severity: string | null;
  body: string | null;
  anchors: FixAnchors | null;
  pass: number | null;
  max_passes: number;
  kb: unknown;
  open_inline: number;
  fixed: number;
  open: number;
  stopped: number;
  complete: boolean;
  [key: string]: unknown;
}

/** The pass's anchor block, read verbatim from the record (line and start_line stay strings). */
export interface FixAnchors {
  path: string;
  line: string;
  start_line: string;
  side: string;
  anchored: string;
  [key: string]: unknown;
}

/** learn --json data: exactly one of rule (one appended rule) or rules (the status listing). */
export interface LearnData {
  rule?: unknown;
  rules?: unknown[];
  [key: string]: unknown;
}

/** init --json data: the applied preset values and the files they were written to. */
export interface InitData {
  preset: string;
  budget_usd: string;
  kb_min_judged: number;
  kb_max_fp_rate: number;
  files: Array<string>;
  check_tree_clean: boolean;
  [key: string]: unknown;
}

/** doctor --json data: every preflight row and the healthy verdict over them. Row values are environment-shaped, so the goldens, not this schema, pin them. */
export interface DoctorData {
  healthy: boolean;
  checks: Array<DoctorCheck>;
  [key: string]: unknown;
}

/** One preflight row: the check name, its severity, whether it passed, and the observed detail. */
export interface DoctorCheck {
  name: string;
  severity: "ok" | "warn" | "fail";
  ok: boolean;
  detail: string;
  [key: string]: unknown;
}

/** check-tree --json data: the clean verdict plus exactly one of four explanations — ignored_review_lines (clean, with ignored .verdict/state paths), files (dirty), expected_only (only expected paths changed), unexpected (unexpected paths changed). */
export interface CheckTreeData {
  clean: boolean;
  ignored_review_lines?: number;
  files?: Array<string>;
  expected_only?: boolean;
  unexpected?: Array<string>;
  [key: string]: unknown;
}

/** threads --json data: where the refreshed feed was written plus a tally over it. The feed document itself is threads.schema.json. */
export interface ThreadsData {
  dir: string;
  file: string;
  provider: string;
  head_sha: string;
  fetched_at: string;
  truncated: boolean;
  threads: number;
  open: number;
  outdated: number;
  verdict: number;
  [key: string]: unknown;
}

/** threads resolve --json data: the receipt path, the host and head it proves, the ids it closed, and the candidates it left open with a reason. A dry run carries the same plan with dry_run true and writes nothing. */
export interface ThreadsResolveData {
  dir: string;
  file: string;
  provider: string;
  head_sha: string;
  resolved_at: string;
  dry_run: boolean;
  resolved: Array<string>;
  skipped: Array<ThreadsResolveSkip>;
  [key: string]: unknown;
}

/** One candidate a resolve run left open: its host thread id and the machine-readable reason (already-resolved, no-finding-link, finding-not-addressed). */
export interface ThreadsResolveSkip {
  id: string;
  reason: string;
  [key: string]: unknown;
}

/** verify --json data: the command's exit status, whether the watchdog timed it out, the redacted output tail, the patch read back from the bundle, and whether the run was sandboxed (absent means unsandboxed). A timed-out run is never proof. */
export interface VerifyData {
  exit: number;
  timedOut: boolean;
  outputTail: string;
  patch: string;
  group?: string;
  head?: string;
  receipt?: string;
  sandboxed?: boolean;
  [key: string]: unknown;
}

/** trend --json data: the portfolio rollup, one row per reviewed bundle, and the opt-in overlap and corpus joins. Every tally key is always present. */
export interface TrendData {
  rules?: unknown;
  generated_at: string;
  bundles: number;
  findings: number;
  open: number;
  addressed: number;
  closed: number;
  by_status: TrendStatuses;
  by_tier: TrendTiers;
  /** Portfolio finding counts by observed category with untagged for null entries; only observed categories appear. */
  by_category: unknown;
  provenance: TrendProvenance;
  periods: Array<TrendPeriod>;
  per_bundle: Array<TrendBundleRow>;
  overlap?: unknown;
  corpus?: unknown;
  [key: string]: unknown;
}

/** A five-status finding tally: open, addressed, wont-fix, false-positive, deferred. */
export interface TrendStatuses {
  open: number;
  addressed: number;
  "wont-fix": number;
  "false-positive": number;
  deferred: number;
  [key: string]: unknown;
}

/** A four-tier finding tally: critical, important, informational, untagged. */
export interface TrendTiers {
  critical: number;
  important: number;
  informational: number;
  untagged: number;
  [key: string]: unknown;
}

/** The distinct tools, models, and prompt hashes behind the rollup; a bundle that recorded none contributes a null entry. */
export interface TrendProvenance {
  tools: unknown[];
  models: unknown[];
  prompts_hashes: unknown[];
  [key: string]: unknown;
}

/** One time bucket of the rollup, with the same tallies as the whole. */
export interface TrendPeriod {
  period: string | null;
  bundles: number;
  findings: number;
  open: number;
  addressed: number;
  closed: number;
  by_tier: TrendTiers;
  [key: string]: unknown;
}

/** One reviewed bundle's contribution: its model/effort inventory, provenance triple, status and tier tallies, and bundle-local counts. */
export interface TrendBundleRow {
  bundle: string;
  at: string | null;
  models: Array<string>;
  efforts: Array<string>;
  provenance: ProvenanceTriple;
  findings: number;
  open: number;
  addressed: number;
  closed: number;
  by_status: TrendStatuses;
  by_tier: TrendTiers;
  /** One bundle's finding counts by observed category with untagged for null entries. */
  by_category: unknown;
  [key: string]: unknown;
}

/** pack list --json data: one row per loaded pack in precedence order (built-in, org, repo). */
export interface PackData {
  packs: Array<PackEntry>;
  [key: string]: unknown;
}

/** One pack row: its manifest identity, layer, source, canonical tree hash, declared lens and rule ids, and drift (null when a repo pack matches verdict.lock, else pack-lock-missing or pack-lock-drift; built-in and org rows are never locked). */
export interface PackEntry {
  name: string;
  version: string;
  layer: "builtin" | "org" | "repo";
  source: string;
  hash: string;
  lenses: Array<string>;
  rules: Array<string>;
  drift: string | null;
  [key: string]: unknown;
}

/** pack add --json data: the added pack row as it now lists from the repo layer, whether it replaced an existing entry, and the previous tree hash (null on a first add). */
export interface PackAddData {
  pack: PackEntry;
  previous_hash: string | null;
  replaced: boolean;
  [key: string]: unknown;
}

/** pack test --json data: the resolved pack name, one PASS, SKIP, or FAIL row per sorted eval case, and the status counts. */
export interface PackTestData {
  pack: string;
  cases: Array<PackTestCase>;
  summary: PackTestSummary;
  [key: string]: unknown;
}

/** One offline pack eval case and its result. A reason is present for SKIP and FAIL. */
export interface PackTestCase {
  case: string;
  status: "PASS" | "SKIP" | "FAIL";
  reason?: string;
  [key: string]: unknown;
}

/** Counts the passing, skipped, and failing pack eval cases. */
export interface PackTestSummary {
  pass: number;
  skip: number;
  fail: number;
  [key: string]: unknown;
}

/** index --json data: one object across build, status, prune, callers, tests-for, co-changed, owners, and blast-radius, so kind names the subcommand and every other key is optional. */
export interface IndexData {
  kind:
    | "build"
    | "status"
    | "prune"
    | "callers"
    | "tests-for"
    | "co-changed"
    | "owners"
    | "blast-radius";
  tree?: string;
  commit?: string;
  built?: boolean;
  stale?: boolean;
  files_indexed?: number;
  skipped?: number;
  parsed?: number;
  reused?: number;
  tree_bytes?: number;
  languages?: Array<IndexLangRow>;
  shards?: IndexShards;
  trees?: Array<IndexTreeRow>;
  kept?: Array<string>;
  removed?: IndexRemoved;
  query?: IndexQuery;
  /** Present only when the queried tree has a stored index; blast-radius omits it when the symbol, caller, and test lists are unmeasured. */
  index?: IndexStatus;
  counts?: IndexCounts;
  limit?: number;
  depth?: number;
  truncated?: number;
  depth_truncated?: number;
  rows?: Array<IndexRow>;
  window?: IndexWindow;
  target?: IndexTarget | null;
  input?: IndexBlastInput;
  symbols?: IndexBlastSymbols;
  callers?: IndexBlastCallers;
  tests?: IndexBlastTests;
  co_changed?: IndexBlastPartners;
  owners?: IndexBlastOwners;
  [key: string]: unknown;
}

export interface IndexLangRow {
  language: "go" | "typescript" | "javascript" | "python";
  files: number;
  [key: string]: unknown;
}

/** The local shard store: file count and byte total. */
export interface IndexShards {
  count: number;
  bytes: number;
  [key: string]: unknown;
}

/** One stored tree file: its tree SHA, file count, and bytes. */
export interface IndexTreeRow {
  tree: string;
  files: number;
  bytes: number;
  [key: string]: unknown;
}

/** The prune receipt: trees and shards removed. */
export interface IndexRemoved {
  trees: number;
  shards: number;
  [key: string]: unknown;
}

/** The query input: the symbol as written for callers or tests-for --symbol, or the repository path for tests-for, co-changed, and owners. */
export interface IndexQuery {
  symbol?: string;
  path?: string;
  [key: string]: unknown;
}

/** The queried tree's index state, published by callers and tests-for: tree, commit, staleness, and file coverage. */
export interface IndexStatus {
  tree: string;
  commit?: string;
  stale: boolean;
  coverage: IndexCoverage;
  [key: string]: unknown;
}

/** The indexed and skipped file counts for the queried tree. */
export interface IndexCoverage {
  files_indexed: number;
  skipped: number;
  [key: string]: unknown;
}

/** The query counts for the kind: caller rows and candidates left unresolved, candidate tests, or co-change partners and the commits that touched the target. */
export interface IndexCounts {
  callers?: number;
  unresolved?: number;
  tests?: number;
  partners?: number;
  target_commits?: number;
  [key: string]: unknown;
}

/** One row of a caller, candidate-test, or co-change query: path plus the fields that kind measures (line/kind/resolution for callers, via for candidates, commits/jaccard for partners). */
export interface IndexRow {
  path: string;
  line?: number;
  kind?: "call" | "method" | "type-ref" | "value-ref" | "embed";
  resolution?: "exact" | "lexical";
  language?: "go" | "typescript" | "javascript" | "python";
  via?: "same-package" | "references" | "imports-module" | "transitive-import";
  depth?: number;
  commits?: number;
  jaccard?: number;
  [key: string]: unknown;
}

/** The bounded history window a co-change query read: oldest and newest commit sha, and how many commits it counted (empty window names no bounds). */
export interface IndexWindow {
  first?: string;
  last?: string;
  commits: number;
  [key: string]: unknown;
}

/** One queried path and its CODEOWNERS answer. owners is null when the tree has no CODEOWNERS file (owners_source is null too) or when no rule matches (owners_source names the file); a matching ownerless rule cleared an earlier match, so owners is [] with owners_source named. Never an owner named nobody. */
export interface IndexTarget {
  path: string;
  owners: Array<string> | null;
  owners_source: string | null;
  [key: string]: unknown;
}

/** blast-radius's query input: the fetched PR bundle slug and its head when --pr read one (both null for --paths), and the resolved changed paths — every path the bundle diff names, or every --paths entry. */
export interface IndexBlastInput {
  pr: string | null;
  head: string | null;
  paths: Array<string>;
  [key: string]: unknown;
}

/** The changed declarations: each declaration whose span the diff touched, or every declaration in a --paths file. count is how many were measured and truncated is how many the limit hid; both are null when no index answered, never 0. */
export interface IndexBlastSymbols {
  count: number | null;
  truncated: number | null;
  rows: Array<IndexBlastSymbol>;
  [key: string]: unknown;
}

/** One changed declaration: the indexed file, the declared name and kind, and its first line. A declaration owns its source from that line to the line before the next declaration. */
export interface IndexBlastSymbol {
  path: string;
  name: string;
  kind: "func" | "method" | "type" | "var" | "const";
  line: number;
  [key: string]: unknown;
}

/** The callers of the changed declarations within depth 2, walked once per target and deduplicated by call site. count, unresolved, and truncated are null when no index answered. */
export interface IndexBlastCallers {
  count: number | null;
  unresolved: number | null;
  truncated: number | null;
  rows: Array<IndexBlastCaller>;
  [key: string]: unknown;
}

/** One caller row: the changed symbol it is reached from (import path and name), the call site, and the depth — 1 for a direct caller, 2 for a caller of a direct caller. resolution is exact within the declaring package and lexical through an import edge, as `index callers` defines it. */
export interface IndexBlastCaller {
  symbol: string;
  path: string;
  kind: "call" | "method" | "type-ref" | "value-ref" | "embed";
  resolution: "exact" | "lexical";
  line: number;
  depth: number;
  [key: string]: unknown;
}

/** The candidate tests to read for the changed declarations, strongest mechanism per file. A candidate is never a coverage claim, and count/truncated are null when no index answered. */
export interface IndexBlastTests {
  count: number | null;
  truncated: number | null;
  depth_truncated: number | null;
  rows: Array<IndexRow>;
  [key: string]: unknown;
}

/** The paths that moved with each changed path in one bounded co-change window, read at HEAD. count is the number of (changed path, partner) rows and partners the distinct partner paths across them; truncated is always measured: history needs no index. */
export interface IndexBlastPartners {
  count: number;
  partners: number;
  truncated: number;
  rows: Array<IndexBlastPartner>;
  [key: string]: unknown;
}

/** One partner path, the changed path it moved with, its co-change count, and the Jaccard ratio. */
export interface IndexBlastPartner {
  target: string;
  path: string;
  commits: number;
  jaccard: number;
  [key: string]: unknown;
}

/** One ownership row per changed path, from the CODEOWNERS file at the queried tree. count and truncated are always measured. */
export interface IndexBlastOwners {
  count: number;
  truncated: number;
  rows: Array<IndexTarget>;
  [key: string]: unknown;
}

/** calibrate --json data: one object shared by the three modes, so every mode-specific key is optional. The live join fills review_id, head, pr, outcomes, precision (an object with by_tier), and provenance; the corpus join fills bundle, case, kind, model, panel, line_tolerance, ground_truth, reported, matched, false_positives, missed, precision (a rate or null), recall, false_discovery_rate, outcomes, and missed_bugs; the probe fills bundle, grades, counts, and judgment. */
export interface CalibrateData {
  unmatched?: number;
  excluded?: unknown;
  proposals?: unknown[];
  metrics?: unknown;
  sampling?: unknown;
  sampling_hits?: Array<number>;
  review_id?: unknown;
  head?: CalibrateHead | null;
  pr?: CalibratePR | null;
  outcomes?: unknown[];
  /** Live-join precision: totals plus by_tier counts and by_category cells (fixed, resolved_unfixed, open, unknown, judged, resolution_rate; a zero judged denominator reads null). Outcome rows carry the finding's category, null when untagged. */
  precision?: unknown;
  provenance?: ProvenanceTriple | null;
  bundle?: string;
  case?: string;
  kind?: string;
  model?: string;
  panel?: Array<string>;
  line_tolerance?: number;
  ground_truth?: number;
  reported?: number;
  matched?: number;
  false_positives?: number;
  missed?: number;
  recall?: number | null;
  false_discovery_rate?: number | null;
  grades?: unknown[];
  counts?: CalibrateCounts | null;
  judgment?: CalibrateJudgment | null;
  missed_bugs?: unknown[];
  [key: string]: unknown;
}

/** The reviewed and live head the live join compared. */
export interface CalibrateHead {
  reviewed: string;
  live: string;
  [key: string]: unknown;
}

/** The live PR state the join read; merged_at is null until the PR merges. */
export interface CalibratePR {
  state: string;
  merged_at: string | null;
  [key: string]: unknown;
}

/** The per-bundle tooling triple; any part the bundle never recorded reads null rather than an empty string. */
export interface ProvenanceTriple {
  tool_version: string | null;
  prompts_hash: string | null;
  model: string | null;
  [key: string]: unknown;
}

/** The probe's verdict tally over the bundle's findings: how many graded real, false, or unknown. */
export interface CalibrateCounts {
  findings: number;
  real: number;
  false: number;
  unknown: number;
  [key: string]: unknown;
}

/** The probe's second-sample agreement tally; agreement_rate is null until at least one finding is judged. */
export interface CalibrateJudgment {
  sampled: number;
  judged: number;
  agreed: number;
  disagreed: number;
  agreement_rate: number | null;
  [key: string]: unknown;
}

/** fetch --json data: the bundle location, the diff statistics the toc counted, the panel, and the conditional summaries the run armed. estimate and budget travel together, cap and chunk follow only when armed, and prompt_dump lands last when staged. */
export interface FetchData {
  dir: string;
  head_sha: string;
  base_sha: string;
  files: number;
  additions: number;
  deletions: number;
  hunks: number;
  omitted: unknown[];
  diff_bytes: number;
  panel: FetchPanel;
  capabilities?: FetchCapabilities;
  /** The review controls the run read from controls.json; shape owned by the controls document. */
  controls?: unknown;
  /** Bundle file paths by name (PR.json, full.diff, toc.txt, roles.txt, reviews.json, inline.json, threads.json, sensitive.txt, incremental.diff, plus kb.txt and kb-recall.json when present); files the run did not write read null. */
  artifacts: unknown;
  incremental_base: string | null;
  estimate?: FetchEstimate;
  /** The resolved budget summary; present when the run resolved an ordinary budget (a grouped run reports its per-group guards and personal ceiling under estimate instead). */
  budget?: unknown;
  /** The armed cost cap summary; absent when no cap is armed. */
  cap?: unknown;
  /** The chunk plan summary; absent when the run was not chunked. */
  chunk?: unknown;
  /** Where the staged prompt was written; absent unless a dump was requested. */
  prompt_dump?: string | null;
  [key: string]: unknown;
}

/** fetch-local --json data: the same statistics without a remote base, plus the worktree kind and no incremental_base. */
export interface FetchLocalData {
  dir: string;
  kind: string;
  head_sha: string;
  base_sha: string;
  files: number;
  additions: number;
  deletions: number;
  hunks: number;
  omitted: unknown[];
  diff_bytes: number;
  panel: FetchPanel;
  capabilities?: FetchCapabilities;
  /** The review controls the run read from controls.json; shape owned by the controls document. */
  controls?: unknown;
  /** Bundle file paths by name (PR.json, full.diff, toc.txt, roles.txt, reviews.json, inline.json, threads.json, sensitive.txt, incremental.diff, plus kb.txt and kb-recall.json when present); files the run did not write read null. */
  artifacts: unknown;
  estimate?: FetchEstimate;
  /** The resolved budget summary; present when the run resolved an ordinary budget (a grouped run reports its per-group guards and personal ceiling under estimate instead). */
  budget?: unknown;
  /** The armed cost cap summary; absent when no cap is armed. */
  cap?: unknown;
  /** The chunk plan summary; absent when the run was not chunked. */
  chunk?: unknown;
  /** Where the staged prompt was written; absent unless a dump was requested. */
  prompt_dump?: string | null;
  [key: string]: unknown;
}

/** What verdict can do through one host's own CLI or API: the boolean table an agent plans with, the compatibility sum review_verdicts, and one machine-readable reason per absent capability. */
export interface FetchCapabilities {
  provider: string;
  fetch: boolean;
  post?: boolean;
  inline_anchors?: boolean;
  approve?: boolean;
  request_changes?: boolean;
  thread_resolution?: boolean;
  /** approve && request_changes, kept so a consumer written before the split reads the same truth; new readers branch on approve and request_changes. */
  review_verdicts?: boolean;
  /** One machine-readable reason code per absent capability, keyed by capability name. Only an absent capability appears; a host that carries everything carries an empty object. */
  unsupported?: Record<string, string>;
  [key: string]: unknown;
}

/** The panel source (null when unpinned) and its member list. */
export interface FetchPanel {
  source: string | null;
  members: Array<string>;
  [key: string]: unknown;
}

export interface PostDataBody {
  marker: string;
  footer?: unknown;
  diagram?: unknown;
  [key: string]: unknown;
}

export interface PostDataSuggestions {
  embedded: number;
  [key: string]: unknown;
}

export interface PostDataFindings {
  open: number;
  deferred: number;
  addressed: number;
  file?: string;
  [key: string]: unknown;
}

export interface RecordDataFindings {
  open: number;
  deferred: number;
  addressed: number;
  file: string;
  [key: string]: unknown;
}
