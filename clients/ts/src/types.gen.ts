// GENERATED from schemas/envelope.schema.json ($defs) by clients/ts/scripts/gen-ts-types.mjs. Do not edit.
/** Refusal shape from fail(): data is null, message carries the cause plus a try: hint, candidates rides only on the oversize-fetch refusal. */
export interface RefusalErrorBody {
  code: string;
  message: string;
  candidates?: Array<Candidate>;
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
  provenance?: Provenance;
  /** Real-post only: the accepted GitHub review object. */
  review?: unknown;
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
