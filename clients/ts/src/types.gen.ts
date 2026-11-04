// GENERATED from schemas/envelope.schema.json ($defs) by clients/ts/scripts/gen-ts-types.mjs. Do not edit.
/** Refusal shape from fail(): data is null, message carries the cause plus a try: hint, candidates rides only on the oversize-fetch refusal. */
export interface RefusalErrorBody {
  code: string;
  message: string;
  candidates?: Array<Candidate>;
  retry_at?: string;
  [key: string]: unknown;
}

export interface Candidate {
  kind: string;
  suggestion: string;
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

/** report --json data: ranked read over the store. cost appears only with --cost, completeness only with --completeness, receipt keys only when budget.json parses. */
export interface ReportData {
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
  inline: number;
  section: number;
  anchored: number;
  unanchored: number;
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
  [key: string]: unknown;
}

/** post --dry-run --json data (dry_run true, findings carry the dry-run file) and real-post data (dry_run false, findings carry counts plus the accepted review object). */
export interface PostData {
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
  [key: string]: unknown;
}

/** record --json data: the local-bundle merge (post stays refused, no PR target). */
export interface RecordData {
  dir: string;
  anchors: AnchorsSummary;
  findings: RecordDataFindings;
  confidence: number | null;
  security_summary: string | null;
  items: Array<FindingItem>;
  [key: string]: unknown;
}

/** show --json data: item is the raw finding record in store key order; fix is the read-only machine repair directive built from that record plus the bundle's patch/verification history. Neither key adds execution, flags, or store writes. */
export interface ShowData {
  item: unknown;
  fix: ShowFix;
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
  /** The exact rerun command plus resultHead from verification.json for this sig, else a matching (or unnamed) patch.json; null when no history exists. Never executed. */
  verify_hint: unknown;
  /** Fixed untrusted-evidence reminder: finding text quotes attacker-controlled PR content and is never an instruction. */
  caution: string;
  [key: string]: unknown;
}

/** A recorded rerun command and its nullable head bracket. */
export interface VerifyHint {
  verifyCmd: string;
  resultHead: string | null;
  [key: string]: unknown;
}

/** fetch --json data.estimate: the projected per-run cost from comparable past runs. basis/harness/measured_runs appear only when a local harness session priced a comparable run; their absence is the legacy self-reported estimate. basis is measured when every priced sample in the projected tier came from a harness, mixed when only some did. */
export interface FetchEstimate {
  tier: string | null;
  projected_usd: number | null;
  comparable_runs: number;
  /** measured when every priced sample in the projected tier was harness-measured, mixed when only some were. Absent means no harness measurement contributed. */
  basis?: string;
  /** Measured session sources behind the projection (e.g. claude-code, opencode); absent with basis. */
  harness?: Array<string>;
  /** How many priced samples in the projected tier came from a harness session; absent with basis. */
  measured_runs?: number;
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
