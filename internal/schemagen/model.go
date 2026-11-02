package schemagen

import "github.com/stainless-code/verdict/internal/core"

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
//   - named structs become $ref entries in $defs; anonymous structs inline;
//   - map[string]any is an open object; any is an empty (accept-anything)
//     schema; []any is an array without items.

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

// HistoryEvent is core's findings history entry.
type HistoryEvent = core.HistoryEvent

// KBCredit is core's knowledge-base credit.
type KBCredit = core.KBCredit

// CostRecord is one agent self-reported spend record (or one element of the
// array a re-reviewed bundle writes).
type CostRecord struct {
	SchemaVersion string       `const:"1"                     json:"schema_version,omitempty"`
	Effort        string       `json:"effort,omitempty"`
	Models        []ModelSpend `json:"models,omitempty"`
	EstUSD        *float64     `json:"est_usd,omitempty"`
	WallSeconds   *float64     `json:"wall_seconds,omitempty"`
	DiffBytes     *float64     `json:"diff_bytes,omitempty"`
	Lenses        []string     `json:"lenses,omitempty"`
}

// ModelSpend is one model's token record inside a cost record.
type ModelSpend struct { //nolint:govet // field order is the published property order
	Name                     string   `json:"name"`
	InputTokens              float64  `json:"input_tokens,omitempty"`
	OutputTokens             float64  `json:"output_tokens,omitempty"`
	CacheReadInputTokens     *float64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *float64 `json:"cache_creation_input_tokens,omitempty"`
}

// Envelope is core's wire envelope.
type Envelope = core.Envelope

// SchemaError is core's typed envelope refusal.
type SchemaError = core.SchemaError

// Candidate is core's refusal suggestion.
type Candidate = core.Candidate

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

// PostData is the post verb's --json data profile (dry-run and real post).
type PostData struct { //nolint:govet // field order is the published property order
	DryRun          bool           `json:"dry_run"`
	Event           string         `json:"event"`
	CommitID        string         `json:"commit_id"`
	Body            PostBody       `inline:"true"           json:"body"`
	Anchors         AnchorsSummary `json:"anchors"`
	Suggestions     Suggestions    `inline:"true"           json:"suggestions"`
	Findings        PostFindings   `inline:"true"           json:"findings"`
	Items           []FindingItem  `json:"items"`
	Confidence      *int           `json:"confidence"       max:"5"            min:"1"`
	SecuritySummary *string        `json:"security_summary"`
	PayloadHash     string         `json:"payload_hash"`
	Provenance      Provenance     `json:"provenance"       optional:"true"`
	Review          map[string]any `json:"review,omitempty"`
}

// PostBody is the rendered post body summary.
type PostBody struct { //nolint:govet // field order is the published property order
	Marker  string `json:"marker"`
	Footer  any    `json:"footer,omitempty"`
	Diagram any    `json:"diagram,omitempty"`
}

// Suggestions counts embedded review suggestions.
type Suggestions struct {
	Embedded int `json:"embedded" min:"0"`
}

// PostFindings counts the findings a post reports.
type PostFindings struct { //nolint:govet // field order is the published property order
	Open      int    `json:"open"           min:"0"`
	Deferred  int    `json:"deferred"       min:"0"`
	Addressed int    `json:"addressed"      min:"0"`
	File      string `json:"file,omitempty"`
}

// RecordData is core's record --json data profile.
type RecordData = core.RecordData

// RecordFindings is core's record finding tally.
type RecordFindings = core.RecordFindings

// Post is schemas/post.schema.json: the completed-post record.
type Post struct { //nolint:govet // field order is the published property order
	SchemaVersion   string         `const:"1"                         json:"schema_version"`
	Review          map[string]any `json:"review"`
	Head            string         `json:"head"`
	PostedAt        string         `format:"date-time"                json:"posted_at"`
	Confidence      *int           `json:"confidence,omitempty"       max:"5"               min:"1"`
	SecuritySummary *string        `json:"security_summary,omitempty"`
	ToolVersion     string         `json:"tool_version,omitempty"`
	PromptsHash     string         `json:"prompts_hash,omitempty"`
	Model           *string        `json:"model,omitempty"`
}

// Record is core's completed-record artifact.
type Record = core.Record
