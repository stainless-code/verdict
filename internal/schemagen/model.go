package schemagen

import (
	"github.com/stainless-code/verdict/internal/core"
	backtestcore "github.com/stainless-code/verdict/internal/core/backtest"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/identity"
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

// BacktestData is the backtest score report: report.json and the score
// envelope's data object, generated as schemas/backtest.schema.json. The two
// are one shape, so a consumer reads one schema for both.
type BacktestData = backtestcore.ScoreReport

// BacktestSpend is the score's priced spend over the scored reviews.
type BacktestSpend = backtestcore.ScoreSpend

// BacktestSignal is one row of the score's fix/revert catch table.
type BacktestSignal = backtestcore.ScoreSignal

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

// Config is the typed repository configuration, generated as
// schemas/config.schema.json. The Go structs shape the structural
// keywords; the hand-written parser in internal/config owns the semantic
// checks the schema cannot express.
type Config = core.Config

// ConfigDefaults is the configuration's ordinary review defaults.
type ConfigDefaults = core.ConfigDefaults

// ConfigKBPolicy is the configuration's knowledge-base policy.
type ConfigKBPolicy = core.ConfigKBPolicy

// ConfigIndex is the configuration's local Go index caps.
type ConfigIndex = core.ConfigIndex

// ConfigRequirements is the configuration's conjunctive requirements.
type ConfigRequirements = core.ConfigRequirements

// BlockClause is one per-source, per-path blocking predicate.
type BlockClause = core.BlockClause

// ConfigScope is one path-scoped configuration entry.
type ConfigScope = core.ConfigScope

// ConfigProfile is one named work-profile description.
type ConfigProfile = core.ConfigProfile

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

// LensUsage is core's agent-reported per-group lens attestation entry.
type LensUsage = core.LensUsage

// GroupCost is core's agent-reported per-group spend entry on a cost record.
type GroupCost = core.GroupCost

// FloorScope is core's per-path posting floor entry on a post record.
type FloorScope = core.FloorScope

// CoverageSummary is core's stored diff-coverage receipt on post and record.
type CoverageSummary = core.CoverageSummary

// ChunkCoverage is core's stored chunk attestation on a chunked post or record.
type ChunkCoverage = core.ChunkCoverage

// CoverageClaim is core's review.json coverage[] entry.
type CoverageClaim = core.CoverageClaim

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
	Compliance   *ReportCompliance  `anyOf:"null"        json:"compliance"   optional:"true"`
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

// ReportCompliance is the captured-policy compliance view. It mirrors
// core.ReportCompliance with the count bounds the parser enforces; the
// field-set test keeps the mirror identical to the source DTO.
type ReportCompliance struct { //nolint:govet // field order is the published property order
	Policy     bool `json:"policy"`
	Verified   bool `json:"verified"`
	Blockers   int  `json:"blockers"   min:"0"`
	Conflicted int  `json:"conflicted" min:"0"`
	Pending    int  `json:"pending"    min:"0"`
	Gaps       int  `json:"gaps"       min:"0"`
	Satisfied  bool `json:"satisfied"`
}

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

// ForcedPost is one explicit --force bypass recorded in post.json.
type ForcedPost = core.ForcedPost

// WithheldCapability is one capability a host withheld from a post, with the
// published reason code recorded in post.json.
type WithheldCapability = core.WithheldCapability

// Identity is schemas/identity.schema.json: the bundle's stable patch id.
type Identity = identity.Identity

// PackManifest is a lens pack manifest, generated as schemas/pack.schema.json.
type PackManifest = pack.Manifest

// PackLens is one lens row inside a pack manifest.
type PackLens = pack.LensSpec

// PackRule is one rule row inside a pack manifest.
type PackRule = pack.RuleSpec

// PackData is the pack verb's --json data profile: one row per layer pack.
type PackData = pack.ListData

// PackEntry is one pack list row.
type PackEntry = pack.Entry

// Record is core's completed-record artifact.
type Record = core.Record

// ShowFix is core's per-finding repair directive on the show read path.
type ShowFix = core.ShowFix

// VerifyHint is core's patch/verification rerun hint inside a fix block.
type VerifyHint = core.VerifyHint

// ConfigData is the config verb's --json data profile: the read-only
// policy surface (explain renders the stored bundle, preview resolves
// the proposed or trusted inputs with no live calls).
type ConfigData = core.ConfigData

// ConfigFile is one reviewed path's effective configuration in the config
// verb's data profile.
type ConfigFile = core.ConfigFile

// ConfigValue is one effective default with its provenance.
type ConfigValue = core.ConfigValue

// ConfigRequirement is one captured requirement with its sources.
type ConfigRequirement = core.ConfigRequirement

// ConfigPlanGroup is one execution group on the plan surface.
type ConfigPlanGroup = core.ConfigPlanGroup

// ConfigSpend separates the ordinary guard from the personal ceiling.
type ConfigSpend = core.ConfigSpend

// ConfigCheck is one observed check execution.
type ConfigCheck = core.ConfigCheck

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
// whether the watchdog timed it out, the redacted tail, the patch read back
// from the bundle, and whether the run was sandboxed.
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
	ByCategory  map[string]int   `json:"by_category"`
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
	ByCategory map[string]int   `json:"by_category"`
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

// FetchEstimate is the fetch verb's optional data.estimate object. A whole-run
// resolution prices the run's tier and projection from comparable past runs;
// a grouped resolution prices each plan group against its own guard and states
// the personal ceiling as an exact total or a known lower bound. Exactly one
// shape is present.
type FetchEstimate struct { //nolint:govet // field order is the published property order
	Tier           *string              `json:"tier"            optional:"true"`
	ProjectedUSD   *float64             `json:"projected_usd"   optional:"true"`
	ComparableRuns int                  `json:"comparable_runs" optional:"true"`
	Basis          string               `json:"basis"           optional:"true"`
	Harness        []string             `json:"harness"         optional:"true"`
	MeasuredRuns   int                  `json:"measured_runs"   optional:"true"`
	Groups         []FetchGroupEstimate `json:"groups"          optional:"true"`
	Personal       *FetchGroupPersonal  `anyOf:"null"           json:"personal" optional:"true"`
}

// FetchGroupEstimate is one plan group's public projection: its effort tier,
// its own guard when one resolved, and projected_usd only when the bound
// history priced it. An absent guard is unset, never zero.
type FetchGroupEstimate struct { //nolint:govet // field order is the published property order
	Group        string   `json:"group"`
	Effort       string   `json:"effort"`
	GuardUSD     *float64 `json:"guard_usd"     optional:"true"`
	ProjectedUSD *float64 `json:"projected_usd" optional:"true"`
	OverGuard    bool     `json:"over_guard"`
}

// FetchGroupPersonal is the whole-run personal ceiling comparison: the basis
// (exact-total, known-lower-bound, or no-personal-limit), the limit when a
// ceiling resolved, and the exact total or the known subtotal, never both.
type FetchGroupPersonal struct { //nolint:govet // field order is the published property order
	LimitUSD         *float64 `json:"limit_usd"          optional:"true"`
	Basis            string   `json:"basis"`
	TotalUSD         *float64 `json:"total_usd"          optional:"true"`
	KnownSubtotalUSD *float64 `json:"known_subtotal_usd" optional:"true"`
}

// FetchCapabilities is the fetch envelope's provider capability summary: the
// boolean table an agent plans with, the compatibility sum a pre-split consumer
// still reads, and one machine-readable reason per absent capability.
type FetchCapabilities struct { //nolint:govet // field order is the published property order
	Provider         string            `json:"provider"`
	Fetch            bool              `json:"fetch"`
	Post             bool              `json:"post"              optional:"true"`
	InlineAnchors    bool              `json:"inline_anchors"    optional:"true"`
	Approve          bool              `json:"approve"           optional:"true"`
	RequestChanges   bool              `json:"request_changes"   optional:"true"`
	ThreadResolution bool              `json:"thread_resolution" optional:"true"`
	ReviewVerdicts   bool              `json:"review_verdicts"   optional:"true"`
	Unsupported      map[string]string `json:"unsupported"       optional:"true"`
	// Post, anchors, verdicts, and reasons are optional so an envelope written
	// before the capability split still validates: only provider and fetch were
	// universal. New writers always emit every key.
}

// FetchData is the fetch verb's --json data profile: the bundle location, the
// diff statistics the toc counted, and the conditional summaries the run
// armed (estimate and budget, cap, chunk, prompt dump).
type FetchData struct { //nolint:govet // field order is the published property order
	Dir             string            `json:"dir"`
	HeadSHA         string            `json:"head_sha"`
	BaseSHA         string            `json:"base_sha"`
	Files           int               `json:"files"            min:"0"`
	Additions       int               `json:"additions"        min:"0"`
	Deletions       int               `json:"deletions"        min:"0"`
	Hunks           int               `json:"hunks"            min:"0"`
	Omitted         []any             `json:"omitted"`
	DiffBytes       int64             `json:"diff_bytes"       min:"0"`
	Panel           FetchPanel        `json:"panel"`
	Capabilities    FetchCapabilities `json:"capabilities"     optional:"true"`
	Controls        any               `json:"controls"         optional:"true"`
	Artifacts       any               `json:"artifacts"`
	IncrementalBase *string           `json:"incremental_base"`
	Estimate        FetchEstimate     `json:"estimate"         optional:"true"`
	Budget          any               `json:"budget"           optional:"true"`
	Cap             any               `json:"cap"              optional:"true"`
	Chunk           any               `json:"chunk"            optional:"true"`
	PromptDump      *string           `json:"prompt_dump"      optional:"true"`
}

// FetchLocalData is the fetch-local verb's --json data profile: the same
// statistics without a remote base, plus the worktree kind and no
// incremental base.
type FetchLocalData struct { //nolint:govet // field order is the published property order
	Dir          string            `json:"dir"`
	Kind         string            `json:"kind"`
	HeadSHA      string            `json:"head_sha"`
	BaseSHA      string            `json:"base_sha"`
	Files        int               `json:"files"        min:"0"`
	Additions    int               `json:"additions"    min:"0"`
	Deletions    int               `json:"deletions"    min:"0"`
	Hunks        int               `json:"hunks"        min:"0"`
	Omitted      []any             `json:"omitted"`
	DiffBytes    int64             `json:"diff_bytes"   min:"0"`
	Panel        FetchPanel        `json:"panel"`
	Capabilities FetchCapabilities `json:"capabilities" optional:"true"`
	Controls     any               `json:"controls"     optional:"true"`
	Artifacts    any               `json:"artifacts"`
	Estimate     FetchEstimate     `json:"estimate"     optional:"true"`
	Budget       any               `json:"budget"       optional:"true"`
	Cap          any               `json:"cap"          optional:"true"`
	Chunk        any               `json:"chunk"        optional:"true"`
	PromptDump   *string           `json:"prompt_dump"  optional:"true"`
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

// ThreadsResolveReceipt is the receipt `threads resolve` writes, published as
// schemas/threads-resolve.schema.json: the host, head, and timestamp of the
// mutation plus the ids it closed and the candidates it skipped.
type ThreadsResolveReceipt = threadscore.ResolveReceipt

// ThreadsResolveSkip is one candidate a resolve run left open and why.
type ThreadsResolveSkip = threadscore.ResolveSkip

// ThreadsResolveData is the resolve half of the threads --json data profile.
type ThreadsResolveData = fetchcore.ThreadsResolveData

// IndexData is the index verb's --json data profile. One object serves four
// subcommands, so every subcommand-specific key is optional and a consumer
// branches on kind.
type IndexData struct { //nolint:govet // field order is the published property order
	Kind string `enum:"build|status|prune|callers|tests-for|co-changed|owners|blast-radius" json:"kind"`

	Tree         string             `json:"tree"          optional:"true"`
	Commit       string             `json:"commit"        optional:"true"`
	Built        bool               `json:"built"         optional:"true"`
	Stale        bool               `json:"stale"         optional:"true"`
	FilesIndexed int                `json:"files_indexed" min:"0"         optional:"true"`
	Skipped      int                `json:"skipped"       min:"0"         optional:"true"`
	Parsed       int                `json:"parsed"        min:"0"         optional:"true"`
	Reused       int                `json:"reused"        min:"0"         optional:"true"`
	TreeBytes    int64              `json:"tree_bytes"    min:"0"         optional:"true"`
	Languages    []IndexLangRow     `json:"languages"     optional:"true"`
	Shards       IndexShards        `json:"shards"        optional:"true"`
	Trees        []IndexTreeRow     `json:"trees"         optional:"true"`
	Kept         []string           `json:"kept"          optional:"true"`
	Removed      IndexRemoved       `json:"removed"       optional:"true"`
	Query        IndexQuery         `json:"query"         optional:"true"`
	Index        IndexStatus        `json:"index"         optional:"true"`
	Counts       IndexCounts        `json:"counts"        optional:"true"`
	Limit        int                `json:"limit"         min:"1"         optional:"true"`
	Truncated    int                `json:"truncated"     min:"0"         optional:"true"`
	Rows         []IndexRow         `json:"rows"          optional:"true"`
	Window       IndexWindow        `json:"window"        optional:"true"`
	Target       *IndexTarget       `anyOf:"null"         json:"target"   optional:"true"`
	Input        IndexBlastInput    `json:"input"         optional:"true"`
	Symbols      IndexBlastSymbols  `json:"symbols"       optional:"true"`
	Callers      IndexBlastCallers  `json:"callers"       optional:"true"`
	Tests        IndexBlastTests    `json:"tests"         optional:"true"`
	CoChanged    IndexBlastPartners `json:"co_changed"    optional:"true"`
	Owners       IndexBlastOwners   `json:"owners"        optional:"true"`
}

// IndexBlastInput is the blast-radius query input: the fetched PR bundle slug
// or the resolved changed paths, and the bundle head when the query read one.
type IndexBlastInput struct {
	Pr    *string  `json:"pr"`
	Head  *string  `json:"head"`
	Paths []string `json:"paths"`
}

// IndexBlastSymbols is the changed-declaration list. count and truncated are
// null when the index could not answer.
type IndexBlastSymbols struct {
	Count     *int               `json:"count"     min:"0"`
	Truncated *int               `json:"truncated" min:"0"`
	Rows      []IndexBlastSymbol `json:"rows"`
}

// IndexBlastSymbol is one changed declaration: the file, the declared name and
// kind, and its first line.
type IndexBlastSymbol struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `enum:"func|method|type|var|const" json:"kind"`
	Line int    `json:"line"                       min:"1"`
}

// IndexBlastCallers is the caller list within the depth cap. count,
// unresolved, and truncated are null when the index could not answer.
type IndexBlastCallers struct {
	Count      *int               `json:"count"      min:"0"`
	Unresolved *int               `json:"unresolved" min:"0"`
	Truncated  *int               `json:"truncated"  min:"0"`
	Rows       []IndexBlastCaller `json:"rows"`
}

// IndexBlastCaller is one caller reached from a changed symbol: the root
// symbol, the call site, and the depth it was reached at.
type IndexBlastCaller struct {
	Symbol     string `json:"symbol"`
	Path       string `json:"path"`
	Kind       string `enum:"call|method|type-ref|value-ref|embed" json:"kind"`
	Resolution string `enum:"exact|lexical"                        json:"resolution"`
	Line       int    `json:"line"                                 min:"1"`
	Depth      int    `json:"depth"                                max:"2"           min:"1"`
}

// IndexBlastTests is the candidate-test list for the changed declarations.
type IndexBlastTests struct {
	Count     *int       `json:"count"     min:"0"`
	Truncated *int       `json:"truncated" min:"0"`
	Rows      []IndexRow `json:"rows"`
}

// IndexBlastPartners is the co-change list for the changed paths: count is the
// number of (target, partner) rows and partners the distinct partner paths.
type IndexBlastPartners struct { //nolint:govet // field order is the published property order
	Count     int                 `json:"count"     min:"0"`
	Partners  int                 `json:"partners"  min:"0"`
	Truncated int                 `json:"truncated" min:"0"`
	Rows      []IndexBlastPartner `json:"rows"`
}

// IndexBlastPartner is one path that moved with one changed path.
type IndexBlastPartner struct {
	Target  string  `json:"target"`
	Path    string  `json:"path"`
	Commits int     `json:"commits" min:"0"`
	Jaccard float64 `json:"jaccard" max:"1" min:"0"`
}

// IndexBlastOwners is one ownership row per changed path.
type IndexBlastOwners struct { //nolint:govet // field order is the published property order
	Count     int           `json:"count"     min:"0"`
	Truncated int           `json:"truncated" min:"0"`
	Rows      []IndexTarget `json:"rows"`
}

// IndexLangRow is one scanned language and the files it covers.
type IndexLangRow struct {
	Language string `enum:"go|typescript|javascript|python" json:"language"`
	Files    int    `json:"files"                           min:"0"`
}

// IndexShards is the local shard store's count and byte total.
type IndexShards struct {
	Count int   `json:"count" min:"0"`
	Bytes int64 `json:"bytes" min:"0"`
}

// IndexTreeRow is one stored tree file in the status inventory.
type IndexTreeRow struct {
	Tree  string `json:"tree"`
	Files int    `json:"files" min:"0"`
	Bytes int64  `json:"bytes" min:"0"`
}

// IndexRemoved is the prune receipt's removed counts.
type IndexRemoved struct {
	Trees  int `json:"trees"  min:"0"`
	Shards int `json:"shards" min:"0"`
}

// IndexQuery is the query's input: the caller or tests-for symbol, or the
// path tests-for, co-changed, and owners read.
type IndexQuery struct {
	Symbol string `json:"symbol" optional:"true"`
	Path   string `json:"path"   optional:"true"`
}

// IndexWindow is the bounded history window a co-change query read.
type IndexWindow struct {
	First   string `json:"first"   optional:"true"`
	Last    string `json:"last"    optional:"true"`
	Commits int    `json:"commits" min:"0"`
}

// IndexTarget is one queried path and the CODEOWNERS answer for it. Owners and
// owners_source are nullable and always published, so an absent rule can never
// read as an owner named nobody.
type IndexTarget struct { //nolint:govet // field order is the published property order
	Path         string    `json:"path"`
	Owners       *[]string `json:"owners"`
	OwnersSource *string   `json:"owners_source"`
}

// IndexStatus is the caller envelope's index state.
type IndexStatus struct {
	Tree     string        `json:"tree"`
	Commit   string        `json:"commit"   optional:"true"`
	Stale    bool          `json:"stale"`
	Coverage IndexCoverage `json:"coverage"`
}

// IndexCoverage is the indexed and skipped file counts.
type IndexCoverage struct {
	FilesIndexed int `json:"files_indexed" min:"0"`
	Skipped      int `json:"skipped"       min:"0"`
}

// IndexCounts is the query's measured counts; the kind decides which keys are
// present.
type IndexCounts struct {
	Callers       int `json:"callers"        min:"0" optional:"true"`
	Unresolved    int `json:"unresolved"     min:"0" optional:"true"`
	Tests         int `json:"tests"          min:"0" optional:"true"`
	Partners      int `json:"partners"       min:"0" optional:"true"`
	TargetCommits int `json:"target_commits" min:"0" optional:"true"`
}

// IndexRow is one row of a caller, candidate-test, or co-change query; the
// kind decides which fields the row carries.
type IndexRow struct { //nolint:govet // field order is the published property order
	Path       string  `json:"path"`
	Line       int     `json:"line"                                   min:"1"           optional:"true"`
	Kind       string  `enum:"call|method|type-ref|value-ref|embed"   json:"kind"       optional:"true"` //nolint:revive // published enum tag exceeds the line limit
	Resolution string  `enum:"exact|lexical"                          json:"resolution" optional:"true"` //nolint:revive // published enum tag exceeds the line limit
	Language   string  `enum:"go|typescript|javascript|python"        json:"language"   optional:"true"` //nolint:revive // published enum tag exceeds the line limit
	Via        string  `enum:"same-package|references|imports-module" json:"via"        optional:"true"` //nolint:revive // published enum tag exceeds the line limit
	Commits    int     `json:"commits"                                min:"0"           optional:"true"`
	Jaccard    float64 `json:"jaccard"                                max:"1"           min:"0"         optional:"true"`
}

// IndexTree is the stored per-tree index document under
// .verdict/index/trees/<tree>.json.
type IndexTree struct {
	SchemaVersion string          `const:"1"               json:"schema_version"`
	Tree          string          `json:"tree"`
	Module        string          `json:"module,omitempty" optional:"true"`
	Files         []IndexTreeFile `json:"files"`
}

// IndexTreeFile is one file's parsed contribution to a tree index.
type IndexTreeFile struct { //nolint:govet // field order is the published property order
	Path          string        `json:"path"`
	Blob          string        `json:"blob"`
	Language      string        `enum:"go|typescript|javascript|python" json:"language" optional:"true"` //nolint:revive // published enum tag exceeds the line limit
	Size          int64         `json:"size"                            min:"0"`
	Oversize      bool          `json:"oversize,omitempty"              optional:"true"`
	Package       string        `json:"package,omitempty"               optional:"true"`
	Imports       []IndexImport `json:"imports,omitempty"               optional:"true"`
	Decls         []IndexDecl   `json:"decls,omitempty"                 optional:"true"`
	Refs          []IndexRef    `json:"refs,omitempty"                  optional:"true"`
	Error         string        `json:"error,omitempty"                 optional:"true"`
	RefsTruncated bool          `json:"refs_truncated,omitempty"        optional:"true"`
}

// IndexImport is one import binding in an indexed file.
type IndexImport struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// IndexDecl is one package-level declaration in an indexed file.
type IndexDecl struct { //nolint:govet // field order is the published property order
	Name     string `json:"name"`
	Kind     string `enum:"func|method|type|var|const" json:"kind"`
	Line     int    `json:"line"                       min:"1"`
	Exported bool   `json:"exported,omitempty"         optional:"true"`
	Recv     string `json:"recv,omitempty"             optional:"true"`
}

// IndexRef is one identifier reference in an indexed file.
type IndexRef struct {
	Name       string `json:"name"`
	Kind       string `enum:"call|method|type-ref|value-ref|embed" json:"kind"`
	Qualifier  string `json:"qualifier,omitempty"                  optional:"true"`
	Line       int    `json:"line"                                 min:"1"`
	Col        int    `json:"col"                                  min:"1"`
	Selector   bool   `json:"selector,omitempty"                   optional:"true"`
	Unresolved bool   `json:"unresolved,omitempty"                 optional:"true"`
}
