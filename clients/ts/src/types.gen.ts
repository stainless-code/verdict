// GENERATED from schemas/envelope.schema.json ($defs) by clients/ts/scripts/gen-ts-types.mjs. Do not edit.
/** CLI-only launcher response. A captured two-finder execution includes its id, completion counts and phase failures; this profile adds no MCP or SDK launch method. */
export interface RunData {
  agent: string;
  binary: string;
  argv: Array<string>;
  bundle: string;
  artifact: string;
  recipe?: RunRecipe | null;
  stage?: RunRecipeStageRender | null;
  started_at?: string;
  ended_at?: string;
  duration_ms?: number;
  exit: number;
  timed_out: boolean;
  timeout: number;
  artifact_fresh: boolean;
  printed?: boolean;
  record?: string;
  execution?: RunExecution | null;
  repair?: RunRepair | null;
  /** The resolved review-depth decision, present only when --budget or defaults.budget participated; a run that named no depth omits it, so its envelope stays byte-identical. The same object is launcher.json's budget key. */
  budget?: ReviewBudget | null;
  [key: string]: unknown;
}

export interface RunRecipe {
  name: string;
  version: string;
  source: "builtin" | "repo";
  sha256: string;
  budget_usd?: number;
  stages: Array<RunRecipeStage>;
  [key: string]: unknown;
}

export interface RunRecipeStage {
  kind: "lenses" | "synthesize" | "dedupe" | "rank";
  lens_source?: "declared" | "bundle";
  lenses?: Array<RunRecipeLens>;
  model?: string;
  [key: string]: unknown;
}

export interface RunRecipeStageRender {
  index: number;
  kind: "lenses" | "synthesize" | "dedupe" | "rank";
  [key: string]: unknown;
}

export interface RunRecipeLens {
  id: string;
  model?: string;
  max_files?: number;
  max_findings?: number;
  [key: string]: unknown;
}

export interface RunExecution {
  id: string;
  status: "in-progress" | "complete" | "partial";
  counts: FinderSummary | null;
  failures: Array<RunPhaseFailure>;
  [key: string]: unknown;
}

export interface RunPhaseFailure {
  phase: string;
  code: string;
  [key: string]: unknown;
}

export interface RunRepair {
  rounds: number;
  rounds_to_valid: number;
  final_valid: boolean;
  attempts: Array<RunAttempt>;
  [key: string]: unknown;
}

/** The review budget's recorded decision: the tier that won, which layer asked for it, the requested value, the bundle measurements it read, the dispatch it bought, and the signals that decided it. Three tier names map onto two launch shapes: standard and deep both run two finders with opposite-finder cross-checks, and deep additionally records the risk signal that escalated it. */
export interface ReviewBudget {
  /** The resolved depth: light (one finder), standard or deep (two finders plus cross-checks). */
  tier: "light" | "standard" | "deep";
  /** Which lever asked for the depth: the --budget flag or the captured defaults.budget. */
  source: "flag" | "config";
  /** The value as requested; auto means the classifier decided the tier. */
  requested: "auto" | "light" | "standard" | "deep";
  /** The gate's file count the classifier read. */
  files: number;
  /** The full diff's byte count the classifier read. */
  diff_bytes: number;
  /** The finder count that actually launched (1 or 2). */
  finders: number;
  /** off for a single-finder run, opposite-finder when two finders cross-check each other. */
  cross_check: "off" | "opposite-finder";
  /** The closed-vocabulary signals that selected the tier, in rule order, e.g. sensitive-path: ** /migrations/**. */
  reasons: Array<string>;
  /** finders when an explicit --finders outranked the budget; the effective finder count beside it is the one that launched. */
  overridden_by?: "finders";
  [key: string]: unknown;
}

export interface RunAttempt {
  round: number;
  prompt_sha256: string;
  review_sha256: string;
  evidence?: RunEvidence | null;
  problems: Array<Problem>;
  truncated: number;
  check_ok: boolean;
  wall_s: number;
  [key: string]: unknown;
}

export interface RunEvidence {
  settled?: Array<string>;
  quote_gaps: number;
  receipt_gaps: number;
  spec_gaps: number;
  errors: number;
  insufficient: boolean;
  [key: string]: unknown;
}

export interface Truncation {
  truncated: boolean;
  truncation_reason:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned: number;
  total: number | null;
  next_offset: number | null;
  [key: string]: unknown;
}

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
  /** JSONPath into review.json, for example $.findings[3].brief.questions. */
  path: string;
  /** Published problem-code enum; the envelope error.code stays bad-review-input. */
  code:
    | "anchor-outside-hunk"
    | "bad-enum"
    | "empty-findings-needs-summary"
    | "enum-synonym"
    | "invalid-json"
    | "key-alias"
    | "missing-field"
    | "normalized-extracted"
    | "normalized-fence"
    | "normalized-trivia"
    | "out-of-range"
    | "path-not-in-diff"
    | "quote-mismatch"
    | "quote-not-checkable"
    | "receipt-hunk-unknown"
    | "receipt-mismatch"
    | "receipt-missing"
    | "rigor-unset"
    | "risk-stale-head"
    | "scalar-to-array"
    | "spec-check-missing"
    | "spec-check-unmet"
    | "spec-evidence-mismatch"
    | "spec-evidence-not-code"
    | "spec-item-unknown"
    | "spec-na-unjustified"
    | "spec-note-missing"
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

/** What a clean dry run would record: the finding count plus the anchors that would leave the inline set for the rendered body. */
export interface CheckSummary {
  findings: number;
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

/** Fetched bundle provenance receipt persisted in provenance.json and exposed via --provenance --json. */
export interface ProvenanceReceipt {
  schema_version: "1";
  run_id: string;
  prompts_hash: string;
  panel?: Array<string>;
  inputs: Array<ProvenanceInput>;
  truncated: Array<ProvenanceTruncatedRow>;
  [key: string]: unknown;
}

/** One resolved input, bundle artifact, KB candidate, override, control, config, or exclusion entry without rule bodies or secret values. */
export interface ProvenanceInput {
  kind: string;
  name: string;
  id?: string;
  status: string;
  reason?: string;
  sha256?: string;
  bytes?: number | null;
  source?: string;
  layer?: string;
  members?: Array<string>;
  included?: number | null;
  omitted?: number | null;
  chars_included?: number | null;
  chars_total?: number | null;
  cap?: number | null;
  excluded?: number | null;
  excluded_bytes?: number | null;
  kept_bytes?: number | null;
  detail?: string;
  [key: string]: unknown;
}

/** One bounded or omitted input row in data.truncated[] when --provenance is active. */
export interface ProvenanceTruncatedRow {
  input: string;
  id?: string;
  status: string;
  reason: string;
  truncated: boolean;
  truncation_reason:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned: number;
  total: number | null;
  next_offset: number | null;
  included?: number | null;
  omitted?: number | null;
  chars_included?: number | null;
  chars_total?: number | null;
  cap?: number | null;
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
  finders?: FinderSummary | null;
  verifiers: number;
  confirmed: number;
  contested: number;
  single: number;
  [key: string]: unknown;
}

export interface FinderSummary {
  completed: number;
  confirmed: number;
  contested: number;
  pending: number;
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
  risk?: ReportRisk | null;
  validation?: ValidationData | null;
  gate?: ReportGate | null;
  receipt?: ProvenanceReceipt;
  truncated?: boolean | Array<ProvenanceTruncatedRow>;
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
  withheld?: number;
  withheld_by_source?: unknown;
  /** Whole-store finding counts by provenance source (model and tool). */
  by_source?: unknown;
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
  include_withheld?: boolean;
  [key: string]: unknown;
}

/** Cost rollup by effort tier. Captured two-finder executions sum bound launch phases and ignore root aggregate claims; missing phase usage leaves tokens and USD null, never zero. */
export interface ReportCost {
  runs: number;
  runs_with_cost: number;
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  /** Summed cache tokens for model records. Null for phase handoffs without cache attribution; cached tokens are a subset of input tokens. */
  cached_input_tokens: number | null;
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
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  runs: number;
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

export interface ReportRisk {
  claimed: "unknown" | "low" | "medium" | "high" | "critical";
  floor: "low" | "medium" | "high" | "critical";
  reported: "unknown" | "low" | "medium" | "high" | "critical";
  assessed_head?: string | null;
  rationale?: string | null;
  inputs: RiskInputs;
  receipt_reasons?: Array<string>;
  max_risk?: "low" | "medium" | "high" | "critical";
  exceeded: boolean;
  gate_reason?: "risk-exceeded" | "risk-unknown";
  score?: RiskScore | null;
  [key: string]: unknown;
}

export interface RiskInputs {
  open_critical: number;
  open_important: number;
  deferred: number;
  unanchored: number;
  unreviewed_files?: number | null;
  stale_head: boolean;
  missing_coverage: boolean;
  [key: string]: unknown;
}

export interface RiskScore {
  value: number;
  measured: number;
  reach: RiskReach;
  churn: RiskChurn;
  category: RiskCategory;
  rung: RiskRung;
  [key: string]: unknown;
}

export interface RiskReach {
  measured: boolean;
  symbols?: number | null;
  callers?: number | null;
  stale?: boolean | null;
  points: number;
  weight: number;
  [key: string]: unknown;
}

export interface RiskChurn {
  measured: boolean;
  files: number;
  additions: number;
  deletions: number;
  hunks: number;
  points: number;
  weight: number;
  [key: string]: unknown;
}

export interface RiskCategory {
  measured: boolean;
  worst?:
    | "logic"
    | "edge-case"
    | "security"
    | "performance"
    | "accidental-commit"
    | "style"
    | "docs"
    | "other"
    | "untagged";
  open: number;
  points: number;
  weight: number;
  [key: string]: unknown;
}

export interface RiskRung {
  measured: boolean;
  weakest?: "said" | "pointed" | "walked" | "ran" | "reproduced";
  open: number;
  points: number;
  weight: number;
  [key: string]: unknown;
}

/** risk --json data: the advisory triage queue over the bundles under .verdict/state. bundles counts the queued bundles (by_band sums to it) and rows are ranked by band, then score, then bundle name; a hidden row adds the response_cap receipt. */
export interface RiskData {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  generated_at: string;
  bundles: number;
  needs_human: number;
  by_band: RiskBands;
  rows: Array<RiskRow>;
  [key: string]: unknown;
}

/** The queued bundles per recomputed risk band, worst first. Every band is always present, zeros included. */
export interface RiskBands {
  critical: number;
  high: number;
  medium: number;
  unknown: number;
  low: number;
  [key: string]: unknown;
}

/** One ranked bundle: its host state, its recomputed band and receipt reasons, the advisory score with each component's points and weight, and whether it needs a human (any band but low). */
export interface RiskRow {
  rank: number;
  bundle: string;
  pr: number | null;
  /** open and local are live and stay in the default queue; merged and closed are named as skipped unless --status all asks for them. A state token the queue does not know reads open. */
  state: "open" | "merged" | "closed" | "local";
  head: string | null;
  claimed: "unknown" | "low" | "medium" | "high" | "critical";
  floor: "low" | "medium" | "high" | "critical";
  reported: "unknown" | "low" | "medium" | "high" | "critical";
  receipt_reasons: Array<string>;
  inputs: RiskInputs;
  /** The 0-100 triage score: reach 35, churn 25, category 25, rung 15, integer-divided by 100. measured counts the components whose artifact was read, so a missing one reads unmeasured at 0 points instead of a clean zero. */
  score: RiskScore;
  needs_human: boolean;
  [key: string]: unknown;
}

/** Composed gate result: every requested dimension in findings, head, coverage, live_threads, compliance, risk, spec, evidence order. passed is true iff no dimension failed or read unknown, so a skip and an auto-posture unreachable probe stay neutral. */
export interface ReportGate {
  /** The gate verdict the exit code follows: exit 0 when true, exit 1 when false. */
  passed: boolean;
  dimensions: Array<GateDimension>;
  [key: string]: unknown;
}

/** One gate dimension: its id, verdict status, human detail text, and its own failing tally (null where the dimension has no count to report). */
export interface GateDimension {
  /** Which dimension read: findings (open findings at-or-above the --max-severity tier), head (a completed post for the fetched HEAD under --require-head), coverage (the stored diff-coverage receipt for that HEAD under --coverage, default require; a chunked bundle is graded on the chunk attestation its receipt stores), live_threads (unresolved threads on the PR under --live), compliance, risk, spec (the spec checks the stored record graded, always on: a violated check withholds the derived approval, so no gate passes over it), or evidence (the stored evidence receipt for that HEAD, always on under --check). */
  id:
    | "findings"
    | "head"
    | "coverage"
    | "live_threads"
    | "compliance"
    | "risk"
    | "spec"
    | "evidence";
  /** pass, fail, unknown (the dimension could not be read and the posture fails closed), or skip (not requested, or unreachable under --live auto). Only fail and unknown fail the gate. */
  status: "pass" | "fail" | "skip" | "unknown";
  detail: string;
  /** The dimension's own tally: offending findings for findings, unattested ranges for coverage (unattested chunks on a chunked bundle), versus-thread count for live_threads, violated spec checks for spec, evidence gaps for evidence, null for head and for a skipped, unreachable, or unknown probe. */
  count: number | null;
  [key: string]: unknown;
}

export interface RiskClaim {
  level: "low" | "medium" | "high" | "critical";
  rationale: string;
  assessed_head: string;
  stale?: boolean;
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
  risk?: RiskClaim | null;
  payload_hash: string;
  /** Real-post only: the accepted GitHub review object. */
  review?: unknown;
  provenance?: Provenance;
  coverage?: CoverageSummary | null;
  withheld?: WithheldSummary | null;
  /** Planned (on dry-run) or executed (when --summary comment is active) PR-level summary comment action. */
  summary?: PostSummary | null;
  validation?: ValidationData | null;
  receipt?: ProvenanceReceipt;
  truncated?: boolean | Array<ProvenanceTruncatedRow>;
  [key: string]: unknown;
}

export interface ValidationData {
  hash: string | null;
  counts: ValidationCounts;
  mode: "warn" | "gate";
  floors: RigorFloors;
  [key: string]: unknown;
}

export interface ValidationCounts {
  confirmed: number;
  rejected: number;
  downgraded: number;
  unvalidated: number;
  [key: string]: unknown;
}

export interface RigorFloors {
  critical: "said" | "pointed" | "walked" | "ran" | "reproduced";
  important: "said" | "pointed" | "walked" | "ran" | "reproduced";
  informational: "said" | "pointed" | "walked" | "ran" | "reproduced";
  [key: string]: unknown;
}

/** PR-level summary comment action and metadata. */
export interface PostSummary {
  action: "create" | "update" | "none" | "failed";
  comment_id?: string;
  url?: string;
  marker?: string;
  duplicate_count?: number;
  note?: string;
  [key: string]: unknown;
}

/** record --json data: the local-bundle merge (post stays refused, no PR target). */
export interface RecordData {
  dir: string;
  anchors: AnchorsSummary;
  findings: RecordDataFindings;
  confidence: number | null;
  security_summary: string | null;
  risk?: RiskClaim | null;
  verification?: VerificationSummary | null;
  items: Array<FindingItem>;
  /** True only on record --dry-run; the run wrote no findings.json, record.json, or any new file. */
  dry_run?: boolean;
  /** Present only on a clean dry run: the review-input summary. */
  check?: CheckSummary | null;
  coverage?: CoverageSummary | null;
  approval?: ApprovalReceipt | null;
  /** Deterministic evidence verification receipt over findings quotes, hunk receipts, and spec checks. */
  evidence?: EvidenceReceipt | null;
  spec_checks?: Array<SpecCheck>;
  receipts?: Array<Receipt>;
  /** Present on record --dry-run: the exact review object the checker read after safe repairs, as an object. */
  normalized?: unknown;
  /** Present on record --dry-run: every safe review.json rewrite applied, in document order. */
  repairs?: Array<Repair>;
  [key: string]: unknown;
}

export interface ApprovalReceipt {
  reason: string;
  tier: "critical" | "important" | "informational";
  gating_sigs: Array<string>;
  clauses?: Array<string>;
  approved: boolean;
  insufficient?: boolean;
  [key: string]: unknown;
}

/** Deterministic evidence verification receipt pinned to the bundle head and diff SHA-256. */
export interface EvidenceReceipt {
  head: string;
  diff_sha256: string;
  quotes?: Array<EvidenceQuote>;
  gaps?: Array<string>;
  receipts: EvidenceReceipts;
  spec: EvidenceSpec;
  insufficient: boolean;
  [key: string]: unknown;
}

/** Per-finding deterministic quote verification status and machine reason. */
export interface EvidenceQuote {
  path: string;
  side: "RIGHT" | "LEFT";
  status: "verified" | "unverified";
  reason?: string;
  line: number;
  [key: string]: unknown;
}

/** Changed-hunk reading receipt coverage summary. */
export interface EvidenceReceipts {
  total: number;
  covered: number;
  exempt: number;
  complete: boolean;
  [key: string]: unknown;
}

/** Specification checklist coverage summary against defaults.spec_coverage_min. */
export interface EvidenceSpec {
  total: number;
  covered: number;
  min: number;
  met: boolean;
  [key: string]: unknown;
}

export interface SpecCheck {
  finding_sig?: string;
  item: string;
  status: "met" | "violated" | "unmet" | "n/a";
  note?: string;
  evidence?: SpecEvidence;
  [key: string]: unknown;
}

export interface SpecEvidence {
  path: string;
  quote: string;
  line: number;
  [key: string]: unknown;
}

export interface Receipt {
  hunk: string;
  quote: string;
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
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  item: unknown;
  fix: ShowFix | null;
  [key: string]: unknown;
}

/** serve --json data (and the start envelope): one profile across health, roots, inbox, detail, and the start line, so every view-specific key is optional while partial, sources, and errors are always present. A product refusal is data null with the typed error; a corrupt bundle in a listing keeps its row with null counts and an errors[] entry, and any total over it is null and partial. */
export interface ServeData {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  version?: string;
  url?: string;
  listen?: string;
  read_only?: boolean | null;
  watch?: boolean | null;
  interval?: string;
  cache?: ServeCache | null;
  roots?: Array<ServeRoot>;
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
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
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
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
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
  /** Present when a bounded build wait was requested; timeout leaves readiness false. */
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

/** Bounded foreground PR wait outcome; timeout is data with error null. */
export interface PrWaitData {
  snapshot: PrWaitSnapshot | null;
  outcome: "condition-met" | "condition-unmet" | "wait-timeout";
  until:
    "merged" | "closed" | "checks-green" | "checks-done" | "review-changed";
  /** Snapshots that differed from the previous observation; repeated identical reads do not count. */
  observations: number;
  /** Reads a bounded wait retried because the host disagreed with itself, such as a GraphQL head that is not yet the REST head right after a push. --once never retries, and a wait still inconsistent at its timeout refuses pr-read-failed instead of claiming an outcome. */
  inconsistent_observations: number;
  [key: string]: unknown;
}

/** One observed PR state, including canonical JSON evidence for checks, reviews, and threads. */
export interface PrWaitSnapshot {
  state: string;
  head: string;
  reviews: string;
  threads: string;
  checks: string;
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

/** ingest --json data: the target bundle, distinct tool names ingested, and per-run finding counts. */
export interface IngestData {
  dir: string;
  tools: Array<string>;
  ingested: number;
  added: number;
  updated: number;
  unchanged: number;
  anchored: number;
  withheld: number;
  open: number;
  [key: string]: unknown;
}

/** next --json data: the ranked finding to fix next, or null when the store has nothing open. item keeps every stored record key beside the ranked locator. */
export interface NextData {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  item: FindingItem | null;
  [key: string]: unknown;
}

/** fix --json data: one pass over the named finding, one --all batch, or the completion shape. The pass carries the finding identity, its anchors, the pass budget, and the open/fixed/stopped tallies; a batch leaves those null and fills min_severity, verify_cmd, and one batch row per selected sig; completion leaves them null and sets complete. */
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
  min_severity?: string | null;
  verify_cmd?: string | null;
  /** Path of fix.patch beside the ledger: the worktree's unified diff against HEAD, written before the pass consumes its budget and absent when the worktree is clean. Nothing is applied or pushed — the operator runs git apply. */
  patch?: string | null;
  /** The --paths globs the batch selected with, in the order given. Absent when the batch read the whole store. */
  paths?: Array<string>;
  selection?: string | null;
  /** One row per open inline finding at or above min_severity, in rank order. Absent on a single pass and on a completion. */
  batch?: Array<FixBatchRow>;
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

/** One sig's outcome in a fix --all batch: the sig, its locator and severity, the pass the batch consumed (0 when it consumed none), the outcome word, and the reason a skipped row gives. A ceiling, scope-escape, or no-anchor row is reported rather than refused, so one stuck finding cannot block the batch. */
export interface FixBatchRow {
  sig: string;
  locator: string;
  severity: "critical" | "important" | "informational" | "untagged";
  pass: number;
  outcome: "ready" | "ceiling" | "scope-escape" | "no-anchor";
  reason: string;
  [key: string]: unknown;
}

/** handoff --json data: one projection of the open inline findings a fix run would take, selected through the same selector fix --all uses. count is the row count; floor and paths are the selection that produced it, and caution heads the quoted finding prose. */
export interface HandoffData {
  generated_at: string;
  bundle: string;
  /** The severity floor the projection applied: critical, important, informational, or off for no floor. An unset request reads important, the floor fix --all takes. */
  floor: "critical" | "important" | "informational" | "off";
  /** The portable --paths globs the projection was scoped to, in the order given; empty when it read the whole store. */
  paths: Array<string>;
  count: number;
  /** The untrusted-data line both projections open with: finding prose quotes attacker-controlled PR content, so it reads as evidence, never as instructions. */
  caution: string;
  rows: Array<HandoffRow>;
  [key: string]: unknown;
}

/** One open finding in the handoff: where it is (sig, locator), its severity and evidence rung, the fix text and code suggestion, the cited KB rule ids, the brief text fix prints for it verbatim, and the verify command the bundle records (null when none). */
export interface HandoffRow {
  sig: string;
  locator: string;
  severity: "critical" | "important" | "informational" | "untagged";
  rigor: "said" | "pointed" | "walked" | "ran" | "reproduced" | "untagged";
  fix_text: string;
  kb_rules: Array<string>;
  /** The stored code suggestion, null when the finding carries none. */
  suggestion: string | null;
  /** Exactly the block fix prints for this finding (outcome, sites, approach, questions), empty when the store holds no brief. */
  brief_text: string;
  /** The rerun command recorded for this sig by verification.json or patch.json, null when neither names one. */
  verify_cmd: string | null;
  [key: string]: unknown;
}

export interface JournalData {
  bundle: string;
  truncation: JournalTruncatedInfo;
  steps: Array<JournalStep>;
  unfinished: Array<JournalUnfinishedStep>;
  chain_breaks: Array<JournalChainBreak>;
  unreadable_lines: number;
  [key: string]: unknown;
}

export interface JournalStep {
  step: string;
  verb: string;
  channel: "cli" | "mcp";
  version: string;
  started_at: string;
  ended_at: string;
  refusal: string | null;
  next: string;
  bundle: JournalBundleDigests;
  argv: Array<string>;
  wrote: Array<JournalFileRecord>;
  removed: Array<string>;
  pid: number;
  exit: number;
  warnings: number;
  duration_ms: number;
  [key: string]: unknown;
}

export interface JournalUnfinishedStep {
  step: string;
  verb: string;
  channel: "cli" | "mcp";
  version: string;
  started_at: string;
  argv: Array<string>;
  pid: number;
  [key: string]: unknown;
}

export interface JournalChainBreak {
  step: string;
  before: string;
  previous_after: string;
  [key: string]: unknown;
}

export interface JournalTruncatedInfo {
  at: string | null;
  kept_from: string | null;
  dropped: number;
  [key: string]: unknown;
}

export interface JournalBundleDigests {
  before: string | null;
  after: string;
  unobserved?: boolean;
  [key: string]: unknown;
}

export interface JournalFileRecord {
  path: string;
  sha256: string;
  bytes: number;
  [key: string]: unknown;
}

/** learn --json data: exactly one of rule (one appended rule), rules (the status listing), or incident (one attached revert or incident). */
export interface LearnData {
  rule?: unknown;
  rules?: unknown[];
  incident?: LearnIncident | null;
  [key: string]: unknown;
}

/** learn --incident data: the bundle and merge the incident was attached to, the paths it touched, the optional note and link, the detection time, and whether the ledger gained a row (appended) or already carried it (existing). */
export interface LearnIncident {
  bundle: string;
  sha: string;
  paths: Array<string>;
  note: string | null;
  url: string | null;
  detected_at: string;
  attached: "appended" | "existing";
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
  ci?: string;
  workflow_path?: string;
  workflow_status?: string;
  workflow?: string;
  root_path?: string;
  root_status?: string;
  root_workflow?: string;
  include_snippet?: string;
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
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
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

/** trend --json data: the portfolio rollup, one row per reviewed bundle, and the opt-in overlap, corpus, and incident joins. Every tally key is always present. */
export interface TrendData {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
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
  by_risk?: TrendRiskBands;
  /** Per author/reviewer pair tallies, keyed "<author>/<reviewer>" and sorted: the fingerprint's agent against each model the record priced, with unknown for either half the bundle never recorded. */
  by_pair?: Record<string, TrendPairCounts>;
  /** Portfolio finding counts by provenance source (model and tool). */
  by_source?: TrendSources;
  withheld?: unknown;
  provenance: TrendProvenance;
  periods: Array<TrendPeriod>;
  per_bundle: Array<TrendBundleRow>;
  overlap?: unknown;
  corpus?: unknown;
  incidents?: TrendIncidents | null;
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

export interface TrendRiskBands {
  low: TrendRiskBand;
  medium: TrendRiskBand;
  high: TrendRiskBand;
  critical: TrendRiskBand;
  unknown: TrendRiskBand;
  [key: string]: unknown;
}

export interface TrendRiskBand {
  bundles: number;
  findings: number;
  open: number;
  addressed: number;
  closed: number;
  [key: string]: unknown;
}

/** One author/reviewer pair's outcomes across the bundles that carried it. */
export interface TrendPairCounts {
  bundles: number;
  findings: number;
  open: number;
  addressed: number;
  closed: number;
  [key: string]: unknown;
}

/** A two-source finding tally: model and tool. */
export interface TrendSources {
  model: number;
  tool: number;
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
  /** The agent the bundle's record.json stamped, or unknown when it recorded no fingerprint. Advisory: it groups history and never decides a review. */
  author: string;
  /** The reviewer models the bundle's record.json stamped, empty when it recorded none. */
  review_models: Array<string>;
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
  risk?: "low" | "medium" | "high" | "critical" | "unknown";
  /** One bundle's finding counts by provenance source (model and tool). */
  by_source?: TrendSources;
  withheld?: unknown;
  run_id?: string;
  truncated_inputs?: number;
  [key: string]: unknown;
}

/** trend --incidents data: the attached incidents, how many a bundle findings store could measure, the hit and missed split, and miss_rate (4 dp, null when nothing measured). */
export interface TrendIncidents {
  bundles: number;
  incidents: number;
  measured: number;
  unmeasured: number;
  hit: number;
  missed: number;
  miss_rate: number | null;
  per_bundle: Array<TrendIncidentBundle>;
  [key: string]: unknown;
}

/** One bundle share of the incident miss rate: its attached, measured, hit, and missed incidents and its own miss_rate. */
export interface TrendIncidentBundle {
  bundle: string;
  incidents: number;
  measured: number;
  hit: number;
  missed: number;
  miss_rate: number | null;
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

export interface PackCheckData {
  path: string;
  config: string;
  disabled: Array<string>;
  packs: Array<PackCheckPack>;
  lenses: Array<PackCheckLens>;
  rules: Array<PackCheckRule>;
  [key: string]: unknown;
}

export interface PackCheckPack {
  name: string;
  version: string;
  layer: "builtin" | "org" | "repo";
  source: string;
  hash: string;
  replace: Array<string>;
  [key: string]: unknown;
}

export interface PackCheckLens {
  id: string;
  pack: string;
  layer: "builtin" | "org" | "repo";
  hash: string;
  replaced: Array<string>;
  [key: string]: unknown;
}

export interface PackCheckRule {
  id: string;
  severity: "critical" | "important" | "informational";
  pack: string;
  layer: "builtin" | "org" | "repo";
  globs: Array<string>;
  replaced: Array<string>;
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
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
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

/** The changed declarations: each declaration whose span the diff touched, or every declaration in a --paths file. count is how many were measured (null when no index answered, never 0); truncated is true only when the result list was capped. */
export interface IndexBlastSymbols {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  count: number | null;
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

/** The callers of the changed declarations within depth 2, walked once per target and deduplicated by call site. count and unresolved are null when no index answered; truncated is true only when the result list was capped. */
export interface IndexBlastCallers {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  count: number | null;
  unresolved: number | null;
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

/** The candidate tests to read for the changed declarations, strongest mechanism per file. A candidate is never a coverage claim; count is null when no index answered and truncated is true only when the result list was capped. */
export interface IndexBlastTests {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  count: number | null;
  rows: Array<IndexRow>;
  [key: string]: unknown;
}

/** The paths that moved with each changed path in one bounded co-change window, read at HEAD. count is the number of (changed path, partner) rows and partners the distinct partner paths across them: history needs no index; truncated is true only when the result list was capped. */
export interface IndexBlastPartners {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  count: number;
  partners: number;
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

/** One ownership row per changed path, from the CODEOWNERS file at the queried tree. count is always measured; truncated is true only when the result list was capped. */
export interface IndexBlastOwners {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  count: number;
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

export interface WithheldSummary {
  by_reason: unknown;
  total: number;
  unlabelled: number;
  [key: string]: unknown;
}

/** fetch --json data: the bundle location, the diff statistics the toc counted, the panel, and the conditional summaries the run armed. estimate and budget travel together, cap and chunk follow only when armed, prompt_dump lands last when staged, and preview replaces every one of them when the run stopped before writing a bundle. */
export interface FetchData {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  dir?: string;
  head_sha?: string;
  base_sha?: string;
  files?: number;
  additions?: number;
  deletions?: number;
  hunks?: number;
  omitted?: unknown[];
  raw_diff_bytes?: number | null;
  diff_bytes?: number;
  panel?: FetchPanel;
  capabilities?: FetchCapabilities;
  /** The review controls the run read from controls.json; shape owned by the controls document. */
  controls?: unknown;
  /** Bundle file paths by name (PR.json, full.diff, toc.txt, roles.txt, reviews.json, inline.json, threads.json, sensitive.txt, incremental.diff, plus kb.txt and kb-recall.json when present); files the run did not write read null. */
  artifacts?: unknown;
  incremental_base?: string | null;
  estimate?: FetchEstimate;
  /** The resolved budget summary; present when the run resolved an ordinary budget (a grouped run reports its per-group guards and personal ceiling under estimate instead). */
  budget?: unknown;
  /** The armed cost cap summary; absent when no cap is armed. */
  cap?: unknown;
  /** The chunk plan summary; absent when the run was not chunked. */
  chunk?: unknown;
  /** Where the staged prompt was written; absent unless a dump was requested. */
  prompt_dump?: string | null;
  /** The preview payload: the reviewed file list, the withheld sections, the byte totals, and the resolved budget. Present only under --preview, where it is the whole data object: a preview writes no bundle directory, so no other key has a value to carry. */
  preview?: PreviewData | null;
  [key: string]: unknown;
}

/** fetch-local --json data: the same statistics without a remote base, plus the worktree kind and no incremental_base. preview replaces the statistics the same way it does for fetch. */
export interface FetchLocalData {
  truncated?: boolean;
  truncation_reason?:
    "work_budget" | "response_cap" | "time_budget" | "scan_cap" | "chunk_cap";
  returned?: number;
  total?: number | null;
  next_offset?: number | null;
  dir?: string;
  kind?: string;
  head_sha?: string;
  base_sha?: string;
  files?: number;
  additions?: number;
  deletions?: number;
  hunks?: number;
  omitted?: unknown[];
  raw_diff_bytes?: number | null;
  diff_bytes?: number;
  panel?: FetchPanel;
  capabilities?: FetchCapabilities;
  /** The review controls the run read from controls.json; shape owned by the controls document. */
  controls?: unknown;
  /** Bundle file paths by name (PR.json, full.diff, toc.txt, roles.txt, reviews.json, inline.json, threads.json, sensitive.txt, incremental.diff, plus kb.txt and kb-recall.json when present); files the run did not write read null. */
  artifacts?: unknown;
  estimate?: FetchEstimate;
  /** The resolved budget summary; present when the run resolved an ordinary budget (a grouped run reports its per-group guards and personal ceiling under estimate instead). */
  budget?: unknown;
  /** The armed cost cap summary; absent when no cap is armed. */
  cap?: unknown;
  /** The chunk plan summary; absent when the run was not chunked. */
  chunk?: unknown;
  /** Where the staged prompt was written; absent unless a dump was requested. */
  prompt_dump?: string | null;
  /** The preview payload: the reviewed file list, the withheld sections, the byte totals, and the resolved budget. Present only under --preview, where it is the whole data object: a preview writes no bundle directory, so no other key has a value to carry. */
  preview?: PreviewData | null;
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

/** --preview data: the exact files the review would read, every section the exclusion stack withheld with its rule, the byte totals either side of the filter, and the review budget the deterministic classifier resolves for that filtered diff. Rows come from the filtered diff, never from the PR's file list, so a withheld path can appear only under excluded. */
export interface PreviewData {
  schema_version: "1";
  /** One row per changed file the filter kept, in diff order. */
  included: Array<PreviewFile>;
  /** One row per withheld diff section, with the rule that removed it and the bytes it carried. */
  excluded: Array<ExcludedPath>;
  /** Bytes of the filtered diff the review would read. */
  included_bytes: number;
  /** Bytes the filter withheld, measured on the raw sections. */
  excluded_bytes: number;
  /** How many files the filter kept; the same count the budget saw. */
  files: number;
  /** The resolved review depth for the filtered diff. */
  budget: PreviewBudget;
  [key: string]: unknown;
}

/** One file a preview would send for review: its path, the role the bundle would assign, and the bytes its sections carry. */
export interface PreviewFile {
  path: string;
  /** The role the bundle would assign from the path's shape; a preview writes no roles.txt, so the shape decides. */
  role: "source" | "test" | "docs" | "config" | "generated";
  /** Section bytes, each line counting its newline. */
  bytes: number;
  [key: string]: unknown;
}

/** The advisory review depth: the tier, why it was chosen, the finders the tier buys, and the cross-check mode derived from it. */
export interface PreviewBudget {
  /** light runs one finder, standard and deep run two with cross-checks. */
  tier: "light" | "standard" | "deep";
  /** The classifier's own reasons, in its order; empty when no rule fired. */
  reasons: Array<string>;
  /** The finder count the tier buys. */
  finders: number;
  /** The cross-check mode derived from the finder count. */
  cross_check: string;
  [key: string]: unknown;
}

/** One withheld diff section: its path, the rule id and pattern that removed it, the reason the rule carries, and the bytes it held. The same row appears in excluded.json, so one reading covers the preview and the bundle receipt. */
export interface ExcludedPath {
  path: string;
  old_path?: string;
  rule_id: string;
  pattern: string;
  reason: "default" | "attribute" | "ignore" | "cli" | "secret-builtin";
  bytes: number;
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
