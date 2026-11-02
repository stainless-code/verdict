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
