package schemagen

import (
	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/core/threads"
)

// This file is the single source of truth for the published JSON Schemas.
// The structs below mirror the JSON objects the Go writers build with
// internal/jq.Obj; struct tags carry the structural keywords and overlay.json
// carries the prose. Nothing here is encoded by the CLI: changing a type only
// changes the generated schema, never runtime JSON output.
//
// Mapping conventions (see build.go):
//   - a field without omitempty or optional:"true" is required;
//   - a pointer field is nullable (type gains "null");
//   - inline:"true" inlines a named struct instead of a $ref;
//   - anyOf:"null" emits the null-or-$ref union;
//   - enum/const tags emit enum/const without a type, exactly like the
//     committed schemas;
//   - items_min:"N" puts minimum N on integer array items, never the array;
//   - named structs become $ref entries in $defs; anonymous structs inline;
//   - map[string]any is an open object; any is an empty (accept-anything)
//     schema; []any is an array without items.

// CorpusCase is the benchmark corpus case manifest, generated as
// schemas/corpus.schema.json.
type CorpusCase = core.CorpusCase

// CorpusProvenance is the corpus case's origin record.
type CorpusProvenance = core.CorpusProvenance

// CorpusBug is one known defect in a corpus case.
type CorpusBug = core.CorpusBug

// ControlsFile is the bundle's resolved-controls snapshot manifest.
type ControlsFile = core.ControlsFile

// ControlsFileEntry is one control path's snapshot status and hash.
type ControlsFileEntry = core.ControlsFileEntry

// Review is core's agent-written review input.
type Review = core.Review

// Comment is core's inline review input.
type Comment = core.Comment

// TriageClaim is core's review status claim.
type TriageClaim = core.TriageClaim

// SectionTag is core's review body section tag.
type SectionTag = core.SectionTag

// FindingEntry is core's merged finding.
type FindingEntry = core.FindingEntry

// Brief is core's per-finding repair brief.
type Brief = core.Brief

// BriefSite is one affected site inside a repair brief.
type BriefSite = core.BriefSite

// HistoryEvent is core's findings history entry.
type HistoryEvent = core.HistoryEvent

// KBCredit is core's knowledge-base credit.
type KBCredit = core.KBCredit

// CostRecord is core's agent self-reported spend record (or one element of
// the array a re-reviewed bundle writes).
type CostRecord = core.CostRecord

// ModelSpend is core's one-model token record inside a cost record.
type ModelSpend = core.ModelSpend

// Envelope is core's wire envelope.
type Envelope = core.Envelope

// SchemaError is core's typed envelope refusal.
type SchemaError = core.SchemaError

// Candidate is core's refusal suggestion.
type Candidate = core.Candidate

// Problem is one review-input problem inside an envelope refusal.
type Problem = core.Problem

// Repair is one safe review.json rewrite the record path applied.
type Repair = core.Repair

// CheckSummary is what a clean review-input dry run would record.
type CheckSummary = core.CheckSummary

// AnchorsSummary is core's anchor tally.
type AnchorsSummary = core.AnchorsSummary

// FindingsCounts is core's finding-state tally.
type FindingsCounts = core.FindingsCounts

// FindingItem is core's ranked/merged finding locator.
type FindingItem = core.FindingItem

// Provenance is core's run provenance triple.
type Provenance = core.Provenance

// ReportData is the report verb's --json data profile.
type ReportData struct { //nolint:govet // field order is the published property order
	GeneratedAt  string             `format:"date-time"  json:"generated_at"`
	Dir          string             `json:"dir"`
	Head         ReportHead         `json:"head"`
	Verdict      ReportVerdict      `json:"verdict"`
	Counts       ReportCounts       `json:"counts"`
	Panel        ReportPanel        `json:"panel"`
	Patch        *map[string]any    `json:"patch"`
	Output       *string            `json:"output"`
	Filters      ReportFilters      `json:"filters"`
	Items        []FindingItem      `json:"items"`
	Cost         ReportCost         `json:"cost"         optional:"true"`
	Completeness ReportCompleteness `json:"completeness" optional:"true"`
	Gate         *ReportGate        `anyOf:"null"        json:"gate"         optional:"true"`
}

// ReportHead is core's fetched/base/live head triple.
type ReportHead = core.ReportHead

// ReportVerdict is core's displayed-ranking tally.
type ReportVerdict = core.ReportVerdict

// ReportCounts is core's finding-kind tally.
type ReportCounts = core.ReportCounts

// ReportPanel is core's panel source and members.
type ReportPanel = core.ReportPanel

// ReportFilters is core's active filter echo.
type ReportFilters = core.ReportFilters

// ReportCost is core's cost rollup.
type ReportCost = core.ReportCost

// CostTiers is core's three tier rollups.
type CostTiers = core.CostTiers

// CostTier is core's one-tier token rollup.
type CostTier = core.CostTier

// ReportCompleteness is core's head comparison.
type ReportCompleteness = core.ReportCompleteness

// ReportGate is core's composed gate result on a gating report run.
type ReportGate = core.ReportGate

// GateDimension is core's one gate dimension verdict.
type GateDimension = core.GateDimension

// PostData is the post verb's --json data profile (dry-run and real post).
type PostData = core.PostData

// PostBody is the rendered post body summary.
type PostBody = core.PostBody

// Suggestions counts embedded review suggestions.
type Suggestions = core.Suggestions

// PostFindings counts the findings a post reports.
type PostFindings = core.PostFindings

// RecordData is core's record --json data profile.
type RecordData = core.RecordData

// RecordFindings is core's record finding tally.
type RecordFindings = core.RecordFindings

// Post is schemas/post.schema.json: the completed-post record.
type Post = core.Post

// Record is core's completed-record artifact.
type Record = core.Record

// ShowFix is core's per-finding repair directive on the show read path.
type ShowFix = core.ShowFix

// VerifyHint is core's patch/verification rerun hint inside a fix block.
type VerifyHint = core.VerifyHint

// ShowData is the show verb's --json data profile: the raw finding record
// plus the read-only fix block.
type ShowData struct {
	Item any     `json:"item"`
	Fix  ShowFix `json:"fix"`
}

// TriageData is the triage verb's --json data profile: the flipped finding's
// signature and new status, plus dry_run when the pass changed nothing.
type TriageData struct {
	Sig    string `json:"sig"`
	Status string `enum:"open|addressed|wont-fix|false-positive|deferred" json:"status"`
	DryRun bool   `json:"dry_run"                                         optional:"true"`
}

// ReadyData is the ready verb's --json data profile: the gate verdict, every
// blocker with its recovery action, and the live PR state the gate read.
type ReadyData struct { //nolint:govet // field order is the published property order
	Ready            bool           `json:"ready"`
	Recommendation   string         `json:"recommendation"`
	Blockers         []ReadyBlocker `json:"blockers"`
	State            string         `json:"state"`
	Draft            bool           `json:"draft"`
	Mergeable        string         `json:"mergeable"`
	MergeState       string         `json:"mergeStateStatus"`
	CI               ReadyCI        `json:"ci"`
	ThreadsOpen      int            `json:"threads_open"      min:"0"`
	DescriptionChars int            `json:"description_chars" min:"0"`
}

// ReadyBlocker is one readiness blocker and the action that clears it.
type ReadyBlocker struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Action string `json:"action"`
}

// ReadyCI is the live CI check tally the gate read.
type ReadyCI struct {
	Failing int `json:"failing" min:"0"`
	Total   int `json:"total"   min:"0"`
}

// AdoptData is the adopt verb's --json data profile: the target directory,
// where the findings came from, and the adopted and still-open counts.
type AdoptData struct {
	Dir     string `json:"dir"`
	From    string `json:"from"`
	Adopted int    `json:"adopted" min:"0"`
	Open    int    `json:"open"    min:"0"`
}

// NextData is the next verb's --json data profile: the ranked finding to fix
// next, or null when the store has nothing open.
type NextData struct {
	Item *FindingItem `anyOf:"null" json:"item"`
}

// FixData is the fix verb's --json data profile. The pass and completion
// shapes share it: completion leaves the pass fields null and sets complete.
type FixData struct { //nolint:govet // field order is the published property order
	Dir        string      `json:"dir"`
	Ledger     string      `json:"ledger"`
	Sig        *string     `json:"sig"`
	Locator    *string     `json:"locator"`
	Severity   *string     `json:"severity"`
	Body       *string     `json:"body"`
	Anchors    *FixAnchors `anyOf:"null"       json:"anchors"`
	Pass       *int        `json:"pass"`
	MaxPasses  int         `json:"max_passes"  min:"0"`
	KB         any         `json:"kb"`
	OpenInline int         `json:"open_inline" min:"0"`
	Fixed      int         `json:"fixed"       min:"0"`
	Open       int         `json:"open"        min:"0"`
	Stopped    int         `json:"stopped"     min:"0"`
	Complete   bool        `json:"complete"`
}

// FixAnchors is the fix pass's anchor block, read verbatim from the record.
type FixAnchors struct {
	Path      string `json:"path"`
	Line      string `json:"line"`
	StartLine string `json:"start_line"`
	Side      string `json:"side"`
	Anchored  string `json:"anchored"`
}

// LearnData is the learn verb's --json data profile: one appended rule, or
// the status listing of every rule in the knowledge base.
type LearnData struct {
	Rule  any   `json:"rule"  optional:"true"`
	Rules []any `json:"rules" optional:"true"`
}

// InitData is the init verb's --json data profile: the applied preset values
// and the files they were written to.
type InitData struct { //nolint:govet // field order is the published property order
	Preset         string   `json:"preset"`
	BudgetUSD      string   `json:"budget_usd"`
	KBMinJudged    float64  `json:"kb_min_judged"`
	KBMaxFPRate    float64  `json:"kb_max_fp_rate"`
	Files          []string `json:"files"`
	CheckTreeClean bool     `json:"check_tree_clean"`
}

// DoctorData is the doctor verb's --json data profile: every preflight row
// and the healthy verdict over them.
type DoctorData struct { //nolint:govet // field order is the published property order
	Healthy bool          `json:"healthy"`
	Checks  []DoctorCheck `json:"checks"`
}

// DoctorCheck is one doctor preflight row.
type DoctorCheck struct { //nolint:govet // field order is the published property order
	Name     string `json:"name"`
	Severity string `enum:"ok|warn|fail" json:"severity"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
}

// CheckTreeData is the check-tree verb's --json data profile: the clean
// verdict plus exactly one of the four shapes that explains it.
type CheckTreeData struct { //nolint:govet // field order is the published property order
	Clean      bool     `json:"clean"`
	Ignored    int      `json:"ignored_review_lines" min:"0"         optional:"true"`
	Files      []string `json:"files"                optional:"true"`
	Expected   bool     `json:"expected_only"        optional:"true"`
	Unexpected []string `json:"unexpected"           optional:"true"`
}

// ThreadsData is the threads verb's --json data profile: where the refreshed
// feed was written plus a tally over it. The feed document itself is
// threads.schema.json (ThreadsFile).
type ThreadsData = fetchcore.ThreadsData

// VerifyData is core's verify --json data profile: the command's exit status,
// whether the watchdog timed it out, the redacted tail, and the patch read
// back from the bundle.
type VerifyData = core.VerifyData

// TrendData is the trend verb's --json data profile: the portfolio rollup,
// one row per reviewed bundle, and the opt-in overlap and corpus joins.
type TrendData struct { //nolint:govet // field order is the published property order
	GeneratedAt string           `format:"date-time" json:"generated_at"`
	Bundles     int              `json:"bundles"     min:"0"`
	Findings    int              `json:"findings"    min:"0"`
	Open        int              `json:"open"        min:"0"`
	Addressed   int              `json:"addressed"   min:"0"`
	Closed      int              `json:"closed"      min:"0"`
	ByStatus    TrendStatuses    `json:"by_status"`
	ByTier      TrendTiers       `json:"by_tier"`
	Provenance  TrendProvenance  `json:"provenance"`
	Periods     []TrendPeriod    `json:"periods"`
	PerBundle   []TrendBundleRow `json:"per_bundle"`
	Overlap     any              `json:"overlap"     optional:"true"`
	Corpus      any              `json:"corpus"      optional:"true"`
}

// TrendStatuses is a five-status finding tally; every key is always present.
type TrendStatuses struct {
	Open          int `json:"open"`
	Addressed     int `json:"addressed"`
	WontFix       int `json:"wont-fix"`
	FalsePositive int `json:"false-positive"`
	Deferred      int `json:"deferred"`
}

// TrendTiers is a four-tier finding tally; every key is always present.
type TrendTiers struct {
	Critical      int `json:"critical"`
	Important     int `json:"important"`
	Informational int `json:"informational"`
	Untagged      int `json:"untagged"`
}

// TrendProvenance is the rollup's distinct tools, models, and prompt hashes;
// a bundle that recorded none contributes a null entry.
type TrendProvenance struct {
	Tools         []any `json:"tools"`
	Models        []any `json:"models"`
	PromptsHashes []any `json:"prompts_hashes"`
}

// TrendPeriod is one time bucket of the rollup.
type TrendPeriod struct {
	Period    *string    `json:"period"`
	Bundles   int        `json:"bundles"`
	Findings  int        `json:"findings"`
	Open      int        `json:"open"`
	Addressed int        `json:"addressed"`
	Closed    int        `json:"closed"`
	ByTier    TrendTiers `json:"by_tier"`
}

// TrendBundleRow is one reviewed bundle's contribution to the rollup.
type TrendBundleRow struct { //nolint:govet // field order is the published property order
	Bundle     string           `json:"bundle"`
	At         *string          `json:"at"`
	Models     []string         `json:"models"`
	Efforts    []string         `json:"efforts"`
	Provenance ProvenanceTriple `json:"provenance"`
	Findings   int              `json:"findings"`
	Open       int              `json:"open"`
	Addressed  int              `json:"addressed"`
	Closed     int              `json:"closed"`
	ByStatus   TrendStatuses    `json:"by_status"`
	ByTier     TrendTiers       `json:"by_tier"`
}

// CalibrateData is the calibrate verb's --json data profile. The three modes
// (live join, corpus join, probe) share one object, so every mode-specific key
// is optional and a consumer narrows on the keys it asked for.
type CalibrateData struct { //nolint:govet // field order is the published property order
	ReviewID          any                `json:"review_id"           optional:"true"`
	Head              *CalibrateHead     `anyOf:"null"               json:"head"       optional:"true"`
	PR                *CalibratePR       `anyOf:"null"               json:"pr"         optional:"true"`
	Outcomes          []any              `json:"outcomes"            optional:"true"`
	Precision         any                `json:"precision"           optional:"true"`
	Provenance        *ProvenanceTriple  `anyOf:"null"               json:"provenance" optional:"true"`
	Bundle            string             `json:"bundle"              optional:"true"`
	Case              string             `json:"case"                optional:"true"`
	Kind              string             `json:"kind"                optional:"true"`
	Model             string             `json:"model"               optional:"true"`
	Panel             []string           `json:"panel"               optional:"true"`
	LineTolerance     int                `json:"line_tolerance"      min:"0"           optional:"true"`
	GroundTruth       int                `json:"ground_truth"        min:"0"           optional:"true"`
	Reported          int                `json:"reported"            min:"0"           optional:"true"`
	Matched           int                `json:"matched"             min:"0"           optional:"true"`
	FalsePositives    int                `json:"false_positives"     min:"0"           optional:"true"`
	Missed            int                `json:"missed"              min:"0"           optional:"true"`
	Recall            *float64           `json:"recall"              min:"0"           optional:"true"`
	FalsePositiveRate *float64           `json:"false_positive_rate" min:"0"           optional:"true"`
	Grades            []any              `json:"grades"              optional:"true"`
	Counts            *CalibrateCounts   `anyOf:"null"               json:"counts"     optional:"true"`
	Judgment          *CalibrateJudgment `anyOf:"null"               json:"judgment"   optional:"true"`
	MissedBugs        []any              `json:"missed_bugs"         optional:"true"`
}

// CalibrateHead is the reviewed and live head the live join compared.
type CalibrateHead struct {
	Reviewed string `json:"reviewed"`
	Live     string `json:"live"`
}

// CalibratePR is the live PR state the join read.
type CalibratePR struct { //nolint:govet // field order is the published property order
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
}

// ProvenanceTriple is the per-bundle tooling triple; any part the bundle never
// recorded reads null rather than an empty string.
type ProvenanceTriple struct {
	ToolVersion *string `json:"tool_version"`
	PromptsHash *string `json:"prompts_hash"`
	Model       *string `json:"model"`
}

// CalibrateCounts is the probe's verdict tally over the bundle's findings.
type CalibrateCounts struct {
	Findings int `json:"findings"`
	Real     int `json:"real"`
	False    int `json:"false"`
	Unknown  int `json:"unknown"`
}

// CalibrateJudgment is the probe's second-sample agreement tally.
type CalibrateJudgment struct { //nolint:govet // field order is the published property order
	Sampled       int      `json:"sampled"`
	Judged        int      `json:"judged"`
	Agreed        int      `json:"agreed"`
	Disagreed     int      `json:"disagreed"`
	AgreementRate *float64 `json:"agreement_rate"`
}

// FetchEstimate is the fetch verb's optional data.estimate object: the
// projected per-run cost plus, when an explicitly handed usage record priced
// a comparable run, the measured basis it was built from.
type FetchEstimate struct { //nolint:govet // field order is the published property order
	Tier           *string  `json:"tier"`
	ProjectedUSD   *float64 `json:"projected_usd"`
	ComparableRuns int      `json:"comparable_runs"`
	Basis          string   `json:"basis"           optional:"true"`
	Harness        []string `json:"harness"         optional:"true"`
	MeasuredRuns   int      `json:"measured_runs"   optional:"true"`
}

// FetchData is the fetch verb's --json data profile: the bundle location, the
// diff statistics the toc counted, and the conditional summaries the run
// armed (estimate and budget, cap, chunk, prompt dump).
type FetchData struct { //nolint:govet // field order is the published property order
	Dir             string        `json:"dir"`
	HeadSHA         string        `json:"head_sha"`
	BaseSHA         string        `json:"base_sha"`
	Files           int           `json:"files"            min:"0"`
	Additions       int           `json:"additions"        min:"0"`
	Deletions       int           `json:"deletions"        min:"0"`
	Hunks           int           `json:"hunks"            min:"0"`
	Omitted         []any         `json:"omitted"`
	DiffBytes       int64         `json:"diff_bytes"       min:"0"`
	Panel           FetchPanel    `json:"panel"`
	Capabilities    any           `json:"capabilities"     optional:"true"`
	Controls        any           `json:"controls"         optional:"true"`
	Artifacts       any           `json:"artifacts"`
	IncrementalBase *string       `json:"incremental_base"`
	Estimate        FetchEstimate `json:"estimate"         optional:"true"`
	Budget          any           `json:"budget"           optional:"true"`
	Cap             any           `json:"cap"              optional:"true"`
	Chunk           any           `json:"chunk"            optional:"true"`
	PromptDump      *string       `json:"prompt_dump"      optional:"true"`
}

// FetchLocalData is the fetch-local verb's --json data profile: the same
// statistics without a remote base, plus the worktree kind and no
// incremental base.
type FetchLocalData struct { //nolint:govet // field order is the published property order
	Dir          string        `json:"dir"`
	Kind         string        `json:"kind"`
	HeadSHA      string        `json:"head_sha"`
	BaseSHA      string        `json:"base_sha"`
	Files        int           `json:"files"        min:"0"`
	Additions    int           `json:"additions"    min:"0"`
	Deletions    int           `json:"deletions"    min:"0"`
	Hunks        int           `json:"hunks"        min:"0"`
	Omitted      []any         `json:"omitted"`
	DiffBytes    int64         `json:"diff_bytes"   min:"0"`
	Panel        FetchPanel    `json:"panel"`
	Capabilities any           `json:"capabilities" optional:"true"`
	Controls     any           `json:"controls"     optional:"true"`
	Artifacts    any           `json:"artifacts"`
	Estimate     FetchEstimate `json:"estimate"     optional:"true"`
	Budget       any           `json:"budget"       optional:"true"`
	Cap          any           `json:"cap"          optional:"true"`
	Chunk        any           `json:"chunk"        optional:"true"`
	PromptDump   *string       `json:"prompt_dump"  optional:"true"`
}

// FetchPanel is the panel source and member list written into the bundle.
type FetchPanel struct {
	Source  *string  `json:"source"`
	Members []string `json:"members"`
}

// Usage is one explicitly handed spend record (or one element of an array),
// read from VERDICT_USAGE and published as schemas/usage.schema.json. The
// agent or a usage adapter writes it; the budget gate joins it against past
// cost records by window. Tokens without a priced total never become a
// measured claim.
type Usage struct {
	SchemaVersion string       `const:"1"              json:"schema_version"`
	Model         string       `json:"model,omitempty"`
	Tokens        *UsageTokens `anyOf:"null"           json:"tokens,omitempty"`
	USD           *float64     `json:"usd,omitempty"   min:"0"`
	Source        string       `json:"source"`
	StartedAt     string       `format:"date-time"     json:"started_at"`
	EndedAt       string       `format:"date-time"     json:"ended_at"`
}

// UsageTokens is one usage record's token counts: input and output when the
// measurer knows them, cache subsets when the provider reports them.
type UsageTokens struct { //nolint:govet // field order is the published property order
	Input      float64  `json:"input"                 min:"0"`
	Output     float64  `json:"output"                min:"0"`
	CacheRead  *float64 `json:"cache_read,omitempty"  min:"0"`
	CacheWrite *float64 `json:"cache_write,omitempty" min:"0"`
}

// ThreadsFile is one provider bundle's normalized PR-conversation feed,
// published as schemas/threads.schema.json. It is additive beside the raw
// provider files: every host's payload is filtered and flattened to the same
// shape, so re-review, addressing, merge readiness, and calibration can read
// prior feedback without reverse-engineering six formats.
type ThreadsFile = threadscore.File

// Thread is one normalized root review thread or conversation comment plus
// its replies: the host thread id, diff anchor and state, and the author and
// finding link of the root comment.
type Thread = threadscore.Thread

// ThreadComment is one comment inside a thread, body capped with a truncated
// flag (the raw provider file keeps the full text).
type ThreadComment = threadscore.Comment
