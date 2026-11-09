package core

import (
	"github.com/stainless-code/verdict/internal/jq"
)

// PostFlags carries the resolved post options: the adapter parses argv and
// the service receives values only.
type PostFlags struct {
	Approve        string `enum:"true|false"`
	ReviewFile     string
	RequestChanges bool
	Bind           bool
	Force          bool
	DryRun         bool
	// RequireCoverage refuses an absent or partial diff-coverage
	// attestation; --force never bypasses it.
	RequireCoverage bool
}

// PostRequest is the post service invocation: the read runtime and
// positionals, the resolved options, the render configuration, the JSON-mode
// flag, and the warnings the adapter already accumulated (prefs).
type PostRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	Flags     PostFlags
	Effective Effective
	JSONMode  bool
	Warnings  []string
}

// PostResponse carries the post outcome: the envelope identity, the typed
// data, and the accumulated warnings. Human lines stream live through the
// runtime progress observer, so the response carries no rendered lines.
type PostResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *PostData
	Warnings []string
	Next     string
}

// PostData is the post verb's --json data profile (dry-run and real post).
// Field order is the real-post wire order: the dry-run wire swaps
// confidence/security before items (a shell quirk preserved in Wire), and
// the dry-run findings carry the file while the real findings carry the
// accepted review. Review rides before provenance on the real-post wire.
type PostData struct { //nolint:govet // field order is the published property order
	DryRun          bool             `json:"dry_run"`
	Event           string           `json:"event"`
	CommitID        string           `json:"commit_id"`
	Body            PostBody         `inline:"true"           json:"body"`
	Anchors         AnchorsSummary   `json:"anchors"`
	Suggestions     Suggestions      `inline:"true"           json:"suggestions"`
	Findings        PostFindings     `inline:"true"           json:"findings"`
	Items           []FindingItem    `json:"items"`
	Confidence      *int             `json:"confidence"       max:"5"                   min:"1"`
	SecuritySummary *string          `json:"security_summary"`
	PayloadHash     string           `json:"payload_hash"`
	Review          map[string]any   `json:"review,omitempty"`
	Provenance      Provenance       `json:"provenance"       optional:"true"`
	Coverage        *CoverageSummary `anyOf:"null"            json:"coverage,omitempty" optional:"true"`
	ItemsRaw        []any            `json:"-"`
	ReviewRaw       any              `json:"-"`
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
	PatchID         *string        `json:"patch_id,omitempty"`
	PatchIDKind     string         `json:"patch_id_kind,omitempty"`
	Forced          []ForcedPost   `json:"forced,omitempty"`
	// PostMinSeverity is the global posting floor. PostFloorScoped records
	// different path-specific floors that withheld findings, and
	// BelowFloorExcluded counts all withheld findings.
	PostMinSeverity    *string          `enum:"critical|important|informational" json:"post_min_severity,omitempty"`
	BelowFloorExcluded int              `json:"below_floor_excluded,omitempty"   min:"0"`
	PostFloorScoped    []FloorScope     `json:"post_floor_scoped,omitempty"`
	Coverage           *CoverageSummary `anyOf:"null"                            json:"coverage,omitempty"          optional:"true"`
	ReviewRaw          any              `json:"-"`
}

// FloorScope is one path-scoped severity floor that actually withheld a
// finding, recorded when it differs from the global baseline. Paths are
// sorted and unique, so the receipt never describes a single floor as if it
// governed every path.
type FloorScope struct {
	Path  string `json:"path"`
	Floor string `enum:"critical|important|informational" json:"floor"`
}

// ForcedPost is one explicit --force bypass recorded in post.json: the fired
// guard plus the prior bundle and head when a same-patch sibling refused.
type ForcedPost struct { //nolint:govet // field order is the published property order
	Reason         string  `json:"reason"`
	PatchID        *string `json:"patch_id,omitempty"`
	PreviousBundle *string `json:"previous_bundle,omitempty"`
	PreviousHead   *string `json:"previous_head,omitempty"`
}

// Wire renders the post data object in wire order. The dry-run wire swaps
// confidence/security before items (the published order);
// the real-post wire carries items before confidence plus the accepted
// review before provenance. ItemsRaw wins over the typed items so the
// envelope re-emits the ordered summaries verbatim; ReviewRaw wins over the
// typed review map so the accepted review keeps its field order.
//
//nolint:revive // the two shell construction orders branch in one place
func (d *PostData) Wire() *jq.Obj {
	items := d.ItemsRaw
	if items == nil {
		items = findingSummariesWire(d.Items)
	}
	review := d.ReviewRaw
	if review == nil && d.Review != nil {
		review = d.Review
	}
	body := d.Body.Wire(d.DryRun)
	provenance := Wire(&d.Provenance)
	if d.DryRun {
		pairs := []any{
			"dry_run", d.DryRun,
			"event", d.Event,
			"commit_id", d.CommitID,
			keyBody, body,
			"anchors", jq.NewObj("kept", d.Anchors.Kept, "moved", d.Anchors.Moved),
			"suggestions", jq.NewObj("embedded", d.Suggestions.Embedded),
			"findings", d.Findings.Wire(),
		}
		pairs = appendCoverageWire(pairs, d.Coverage)
		return jq.NewObj(append(pairs,
			"confidence", Wire(d.Confidence),
			"security_summary", Wire(d.SecuritySummary),
			"items", items,
			"payload_hash", d.PayloadHash,
			"provenance", provenance,
		)...)
	}
	pairs := []any{
		"dry_run", d.DryRun,
		"event", d.Event,
		"commit_id", d.CommitID,
		keyBody, body,
		"anchors", jq.NewObj("kept", d.Anchors.Kept, "moved", d.Anchors.Moved),
		"suggestions", jq.NewObj("embedded", d.Suggestions.Embedded),
		"findings", d.Findings.Wire(),
	}
	pairs = appendCoverageWire(pairs, d.Coverage)
	o := jq.NewObj(append(pairs,
		"items", items,
		"confidence", Wire(d.Confidence),
		"security_summary", Wire(d.SecuritySummary),
		"payload_hash", d.PayloadHash,
	)...)
	if review != nil {
		o.Set("review", review)
	}
	o.Set("provenance", provenance)
	return o
}

// Wire renders the post body: the dry-run wire always carries
// marker/footer/diagram (nulls when absent); the real-post wire carries the
// marker alone.
func (b *PostBody) Wire(dryRun bool) *jq.Obj {
	if dryRun {
		return jq.NewObj("marker", b.Marker, "footer", b.Footer, "diagram", b.Diagram)
	}
	return jq.NewObj("marker", b.Marker)
}

// Wire renders the post findings tally: the file rides last when present.
func (f *PostFindings) Wire() *jq.Obj {
	o := jq.NewObj("open", f.Open, "deferred", f.Deferred, "addressed", f.Addressed)
	if f.File != "" {
		o.Set("file", f.File)
	}
	return o
}

// Wire renders the post receipt in wire order: the reviewed head, the
// timestamps, the normalized confidence, the provenance triple, then the
// patch identity and the explicit --force audit. Review wins from the raw
// ordered object so GitHub's field order survives.
func (p *Post) Wire() *jq.Obj {
	review := p.ReviewRaw
	if review == nil && p.Review != nil {
		review = p.Review
	}
	o := jq.NewObj(
		"schema_version", p.SchemaVersion,
		"review", review,
		"head", p.Head,
		"posted_at", p.PostedAt,
		"confidence", Wire(p.Confidence),
		"security_summary", Wire(p.SecuritySummary),
		"tool_version", p.ToolVersion,
		"prompts_hash", p.PromptsHash,
		"model", Wire(p.Model),
	)
	setPostPatchWire(o, p)
	if p.Forced != nil {
		o.Set("forced", p.Forced)
	}
	setPostFloorWire(o, p)
	if p.Coverage != nil {
		o.Set("coverage", p.Coverage)
	}
	return o
}

// setPostFloorWire appends the payload-floor accounting last: the global
// baseline the receipt may claim, the excluded count even when no global floor
// was set, then the path-scoped floors that did the withholding.
func setPostFloorWire(o *jq.Obj, p *Post) {
	if p.PostMinSeverity != nil {
		o.Set("post_min_severity", *p.PostMinSeverity)
	}
	if p.PostMinSeverity != nil || p.BelowFloorExcluded > 0 {
		o.Set("below_floor_excluded", p.BelowFloorExcluded)
	}
	if len(p.PostFloorScoped) > 0 {
		o.Set("post_floor_scoped", p.PostFloorScoped)
	}
}

// setPostPatchWire appends the optional patch identity after the provenance
// triple: both keys ride only when the bundle had an identity, so legacy
// receipts keep their bytes.
func setPostPatchWire(o *jq.Obj, p *Post) {
	if p.PatchID != nil {
		o.Set("patch_id", *p.PatchID)
	}
	if p.PatchIDKind != "" {
		o.Set("patch_id_kind", p.PatchIDKind)
	}
}

// findingSummariesWire renders typed post items in wire order.
func findingSummariesWire(items []FindingItem) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, jq.NewObj(
			"sig", item.Sig,
			"status", item.Status,
			"anchored", item.Anchored,
			"locator", item.Locator,
		))
	}
	return out
}

// appendCoverageWire inserts the optional coverage receipt after findings and
// before items: the published property order, and an absent key when nothing
// was evaluated so a legacy envelope stays byte-identical.
func appendCoverageWire(pairs []any, c *CoverageSummary) []any {
	if c == nil {
		return pairs
	}
	return append(pairs, "coverage", c)
}
