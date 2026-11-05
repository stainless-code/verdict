package core

// This file holds the findings/record wire shapes the published JSON Schemas
// are generated from (aliased in internal/schemagen/model.go) plus their
// ordered wire renderers. Struct field order is wire order for freshly
// constructed values; the merge preserves prior store order through the
// ordered clone-and-overlay path, so merged entries are never reordered
// through these structs.

import (
	"github.com/stainless-code/verdict/internal/jq"
)

// keyAnchored and keyStatus are the FindingEntry wire keys touched in
// multiple render and decode paths.
const (
	keyAnchored = "anchored"
	keyStatus   = "status"
)

// Review is schemas/review.schema.json: the agent-written review input.
type Review struct { //nolint:govet // field order is the published property order
	SchemaVersion   string        `const:"1"                         json:"schema_version,omitempty"`
	Body            string        `json:"body"`
	Approved        bool          `json:"approved,omitempty"`
	Comments        []Comment     `json:"comments"`
	Resolved        []string      `json:"resolved,omitempty"`
	Triage          []TriageClaim `json:"triage,omitempty"`
	Sections        []SectionTag  `json:"sections,omitempty"`
	Confidence      *int          `json:"confidence,omitempty"       max:"5"                         min:"1"`
	SecuritySummary *string       `json:"security_summary,omitempty"`
	Chunks          []string      `json:"chunks,omitempty"`
}

// Comment is one inline finding in review.json input.
type Comment struct { //nolint:govet // field order is the published property order
	Path       string    `json:"path"`
	Line       any       `json:"line,omitempty"                          type:"integer|string|null"`
	StartLine  any       `json:"start_line,omitempty"                    type:"integer|string|null"`
	Side       string    `json:"side,omitempty"`
	Body       string    `json:"body"`
	Suggestion *string   `json:"suggestion,omitempty"`
	Brief      *Brief    `anyOf:"null"                                   json:"brief,omitempty"`
	Rigor      any       `enum:"said|pointed|walked|ran|reproduced|null" json:"rigor,omitempty"`
	Severity   any       `enum:"critical|important|informational|null"   json:"severity,omitempty"`
	InBounds   *bool     `json:"in-bounds,omitempty"`
	KB         *[]string `json:"kb,omitempty"`
}

// TriageClaim is one status claim in review.json input.
type TriageClaim struct {
	Sig       string `json:"sig"`
	Status    string `enum:"open|addressed|wont-fix|false-positive|deferred" json:"status"`
	Rationale string `json:"rationale"`
}

// SectionTag is one severity tag for a review body section.
type SectionTag struct { //nolint:govet // field order is the published property order
	Heading  string `json:"heading"`
	Severity any    `enum:"critical|important|informational|null" json:"severity,omitempty"`
}

// FindingEntry is one merged finding in findings.json. The ordered Extra
// carries unknown store keys past the typed decode so a re-emit keeps their
// bytes and positions; the merge itself preserves prior order through the
// ordered clone-and-overlay path.
type FindingEntry struct { //nolint:govet // field order is the published property order
	Sig        string         `json:"sig"`
	SigVersion string         `const:"1"                                              json:"sig_version"`
	Kind       string         `enum:"inline|section"                                  json:"kind"`
	Path       any            `json:"path,omitempty"                                  type:"string|null"`
	Heading    any            `json:"heading,omitempty"                               type:"string|null"`
	Line       any            `json:"line,omitempty"                                  type:"integer|null"`
	Side       any            `json:"side,omitempty"                                  type:"string|null"`
	Anchored   any            `json:"anchored,omitempty"                              type:"boolean|string|null"`
	Body       string         `json:"body,omitempty"`
	Suggestion *string        `json:"suggestion,omitempty"`
	Brief      *Brief         `anyOf:"null"                                           json:"brief,omitempty"`
	KB         *[]string      `json:"kb,omitempty"`
	InBounds   any            `json:"inBounds,omitempty"                              type:"boolean|null"`
	Rigor      any            `enum:"said|pointed|walked|ran|reproduced|null"         json:"rigor,omitempty"`
	Severity   any            `enum:"critical|important|informational|null"           json:"severity,omitempty"`
	Status     string         `enum:"open|addressed|wont-fix|false-positive|deferred" json:"status"`
	FirstSeen  string         `json:"firstSeen"`
	LastSeen   string         `json:"lastSeen"`
	Rationale  string         `json:"rationale,omitempty"`
	TriagedAt  string         `json:"triagedAt,omitempty"`
	TriagedBy  string         `json:"triagedBy,omitempty"`
	History    []HistoryEvent `json:"history"`
	Extra      *jq.Obj        `json:"-"`
}

// Brief is the per-finding repair brief: the affected sites plus the outcome,
// approach, and open questions a fix agent reads without parsing the body.
// It enriches a finding and is not part of its sig.
type Brief struct {
	Sites     []BriefSite `json:"sites,omitempty"`
	Outcome   string      `json:"outcome"`
	Approach  string      `json:"approach,omitempty"`
	Questions []string    `json:"questions,omitempty"`
}

// BriefSite is one affected site: a changed path, plus the reviewed line when
// the brief names one (absent means the whole file).
type BriefSite struct { //nolint:govet // field order is the published property order
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

// HistoryEvent is one append-only findings history entry.
type HistoryEvent struct {
	At       string     `json:"at"`
	Event    string     `enum:"reported|claim|reopened|revalidation|addressed|triaged" json:"event"`
	Actor    string     `json:"actor,omitempty"`
	Note     string     `json:"note,omitempty"`
	SHA      string     `json:"sha,omitempty"`
	KBCredit []KBCredit `json:"kb_credit,omitempty"`
}

// KBCredit is one knowledge-base credit carried on a history event.
type KBCredit struct {
	Rule string `json:"rule"`
	To   string `json:"to"`
}

// RecordData is the record verb's --json data profile. ConfidenceRaw wins
// over the typed confidence so an exotic agent value (a numeric string the
// shell passes through raw) re-emits byte-identically; ItemsRaw wins over
// the typed items so the envelope re-emits the ordered summaries verbatim.
type RecordData struct { //nolint:govet // field order is the published property order
	Dir             string         `json:"dir"`
	Anchors         AnchorsSummary `json:"anchors"`
	Findings        RecordFindings `inline:"true"            json:"findings"`
	Confidence      *int           `json:"confidence"        max:"5"                min:"1"`
	SecuritySummary *string        `json:"security_summary"`
	Items           []FindingItem  `json:"items"`
	DryRun          bool           `json:"dry_run,omitempty"`
	Check           *CheckSummary  `anyOf:"null"             json:"check,omitempty"`
	ConfidenceRaw   any            `json:"-"`
	ItemsRaw        []any          `json:"-"`
}

// RecordFindings counts the findings a record run merged.
type RecordFindings struct { //nolint:govet // field order is the published property order
	Open      int    `json:"open"      min:"0"`
	Deferred  int    `json:"deferred"  min:"0"`
	Addressed int    `json:"addressed" min:"0"`
	File      string `json:"file"`
}

// Record is schemas/record.schema.json: the completed-record artifact.
// ConfidenceRaw wins over the typed confidence like RecordData does.
type Record struct { //nolint:govet // field order is the published property order
	SchemaVersion   string  `const:"1"                         json:"schema_version"`
	Status          string  `json:"status"`
	Head            string  `json:"head"`
	DiffSHA256      string  `json:"diff_sha256"`
	RecordedAt      string  `format:"date-time"                json:"recorded_at"`
	Findings        int     `json:"findings"                   min:"0"`
	Open            int     `json:"open"                       min:"0"`
	Confidence      *int    `json:"confidence,omitempty"       max:"5"               min:"1"`
	SecuritySummary *string `json:"security_summary,omitempty"`
	ToolVersion     string  `json:"tool_version,omitempty"`
	PromptsHash     string  `json:"prompts_hash,omitempty"`
	Model           *string `json:"model,omitempty"`
	ConfidenceRaw   any     `json:"-"`
}

// Wire renders one finding entry in wire order: the fixed construction order
// for fresh entries (section entries carry kb after rationale, inline
// entries carry it after suggestion), then any unknown keys in file order.
func (e *FindingEntry) Wire() *jq.Obj {
	var o *jq.Obj
	if e.Kind == "section" {
		o = e.wireSection()
	} else {
		o = e.wireInline()
	}
	o.Set("sig", e.Sig)
	o.Set("sig_version", e.SigVersion)
	o.Set("firstSeen", e.FirstSeen)
	o.Set("lastSeen", e.LastSeen)
	o.Set(keyStatus, e.Status)
	o.Set("rationale", e.Rationale)
	wireTail(e, o)
	return o
}

// wireTail appends the lifecycle stamps, history, and unknown keys: the
// section kb rides here (after rationale), then triage attribution,
// history, then Extra in file order.
func wireTail(e *FindingEntry, o *jq.Obj) {
	if e.Kind == "section" {
		o.Set("kb", kbWire(e.KB))
	}
	if e.TriagedAt != "" {
		o.Set("triagedAt", e.TriagedAt)
	}
	if e.TriagedBy != "" {
		o.Set("triagedBy", e.TriagedBy)
	}
	o.Set("history", historyWire(e.History))
	wireExtra(e, o)
}

// wireExtra appends unknown store keys in file order so a re-emit keeps
// their bytes and positions.
func wireExtra(e *FindingEntry, o *jq.Obj) {
	if e.Extra == nil {
		return
	}
	for _, k := range e.Extra.Keys {
		if !o.Has(k) {
			v, _ := e.Extra.Get(k)
			o.Set(k, v)
		}
	}
}

// wireSection builds the head of a section entry: heading, anchor marker,
// body, severity.
func (e *FindingEntry) wireSection() *jq.Obj {
	return jq.NewObj(
		"kind", e.Kind,
		"heading", e.Heading,
		keyAnchored, e.Anchored,
		"body", e.Body,
		"severity", e.Severity,
	)
}

// wireInline builds the head of an inline entry: locator fields, anchor
// marker, body, and the optional enrichment tail.
func (e *FindingEntry) wireInline() *jq.Obj {
	o := jq.NewObj(
		"kind", e.Kind,
		"path", e.Path,
		"side", e.Side,
		"line", e.Line,
		keyAnchored, e.Anchored,
		"body", e.Body,
		"suggestion", strOrNull(e.Suggestion),
		"kb", kbWire(e.KB),
		"inBounds", e.InBounds,
		"rigor", e.Rigor,
		"severity", e.Severity,
	)
	briefWire(e.Brief, o)
	return o
}

// briefWire appends the optional machine brief after severity: a finding
// without one keeps its legacy bytes, never a null key. An unmodeled brief
// already carried by Extra keeps its own slot instead of being written twice.
func briefWire(b *Brief, o *jq.Obj) {
	if b != nil && !o.Has("brief") {
		o.Set("brief", b.Wire())
	}
}

// Wire renders the brief in the published key order: sites, outcome, then the
// optional approach and questions. A present-but-empty sites list stays [],
// so a brief whose sites all dropped still reads as a brief.
func (b *Brief) Wire() *jq.Obj {
	o := jq.NewObj("sites", briefSitesWire(b.Sites), "outcome", b.Outcome)
	if b.Approach != "" {
		o.Set("approach", b.Approach)
	}
	if b.Questions != nil {
		o.Set("questions", stringList(b.Questions))
	}
	return o
}

// briefSitesWire renders the site list ([] when none survived validation).
func briefSitesWire(sites []BriefSite) []any {
	out := make([]any, 0, len(sites))
	for _, site := range sites {
		s := jq.NewObj("path", site.Path)
		if site.Line != nil {
			s.Set("line", *site.Line)
		}
		out = append(out, s)
	}
	return out
}

// Wire renders one history event in wire order: the fixed construction order
// for fresh events. Actor, note, and sha travel together (every coded
// event sets all three; the reported event sets none), so any one present
// emits all three, preserving an explicitly empty note.
func (e *HistoryEvent) Wire() *jq.Obj {
	o := jq.NewObj("at", e.At, "event", e.Event)
	if e.Actor != "" || e.Note != "" || e.SHA != "" {
		o.Set("actor", e.Actor)
		o.Set("note", e.Note)
		o.Set("sha", e.SHA)
	}
	if e.KBCredit != nil {
		o.Set("kb_credit", kbCreditWire(e.KBCredit))
	}
	return o
}

// Wire renders one knowledge-base credit in wire order.
func (c *KBCredit) Wire() *jq.Obj {
	return jq.NewObj("rule", c.Rule, "to", c.To)
}

// Wire renders the record data object in wire order. ConfidenceRaw wins
// over the typed confidence so an exotic agent value re-emits verbatim;
// ItemsRaw wins over the typed items so the envelope re-emits the ordered
// summaries verbatim. Explicit nulls stay in position (never omitted).
func (d *RecordData) Wire() *jq.Obj {
	items := d.ItemsRaw
	if items == nil {
		items = findingItemsWire(d.Items)
	}
	o := jq.NewObj(
		"dir", d.Dir,
		"anchors", jq.NewObj(
			"kept", d.Anchors.Kept,
			"moved", d.Anchors.Moved,
		),
		"findings", jq.NewObj(
			"open", d.Findings.Open,
			"deferred", d.Findings.Deferred,
			"addressed", d.Findings.Addressed,
			"file", d.Findings.File,
		),
		"confidence", d.ConfidenceRaw,
		"security_summary", strOrNull(d.SecuritySummary),
		"items", items,
	)
	if d.DryRun {
		o.Set("dry_run", true)
	}
	if d.Check != nil {
		o.Set("check", d.Check)
	}
	return o
}

// findingItemsWire renders typed envelope summaries.
func findingItemsWire(items []FindingItem) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, jq.NewObj(
			"sig", item.Sig,
			keyStatus, item.Status,
			keyAnchored, item.Anchored,
			"locator", item.Locator,
		))
	}
	return out
}

// Wire renders the record artifact in wire order. ConfidenceRaw wins over
// the typed confidence like RecordData does. Explicit nulls stay in
// position (never omitted).
func (r *Record) Wire() *jq.Obj {
	return jq.NewObj(
		"schema_version", r.SchemaVersion,
		"status", r.Status,
		"head", r.Head,
		"diff_sha256", r.DiffSHA256,
		"recorded_at", r.RecordedAt,
		"findings", r.Findings,
		"open", r.Open,
		"confidence", r.ConfidenceRaw,
		"security_summary", strOrNull(r.SecuritySummary),
		"tool_version", r.ToolVersion,
		"prompts_hash", r.PromptsHash,
		"model", strOrNull(r.Model),
	)
}

// strOrNull renders an optional string (nil reads null).
func strOrNull(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// kbWire renders an optional kb id list (nil reads null; the backfill turns
// it into [] for fresh stores).
func kbWire(kb *[]string) any {
	if kb == nil {
		return nil
	}
	out := make([]any, 0, len(*kb))
	for _, id := range *kb {
		out = append(out, id)
	}
	return out
}

// historyWire renders one entry history.
func historyWire(events []HistoryEvent) []any {
	out := make([]any, 0, len(events))
	for _, e := range events {
		out = append(out, e.Wire())
	}
	return out
}

// kbCreditWire renders one history credit list.
func kbCreditWire(credits []KBCredit) []any {
	out := make([]any, 0, len(credits))
	for _, c := range credits {
		out = append(out, c.Wire())
	}
	return out
}

// findingKeys are the known findings-entry keys; everything else decodes
// into Extra in file order.
var findingKeys = map[string]bool{
	"sig": true, "sig_version": true, "kind": true, "path": true,
	"heading": true, "line": true, "side": true, keyAnchored: true,
	"body": true, "suggestion": true, "kb": true, "inBounds": true,
	"rigor": true, "severity": true, keyStatus: true, "firstSeen": true,
	"lastSeen": true, "rationale": true, "triagedAt": true, "triagedBy": true,
	"history": true,
}

// FindingEntryOf decodes one ordered store entry into the typed shape,
// keeping unknown keys in Extra in file order. Nil-valued known keys read
// as untyped nils so the re-emit renders null exactly.
func FindingEntryOf(obj *jq.Obj) *FindingEntry {
	e := &FindingEntry{}
	if obj == nil {
		return e
	}
	entryLocator(e, obj)
	entryOptions(e, obj)
	entryStamps(e, obj)
	entryExtra(e, obj)
	return e
}

// entryLocator decodes the locator head: kind, path/heading/line/side,
// the anchor marker, and the body.
func entryLocator(e *FindingEntry, obj *jq.Obj) {
	e.Kind, _ = fieldString(obj, "kind")
	e.Path, _ = obj.Get("path")
	e.Heading, _ = obj.Get("heading")
	e.Line, _ = obj.Get("line")
	e.Side, _ = obj.Get("side")
	e.Anchored, _ = obj.Get(keyAnchored)
	if b, _ := obj.Get("body"); b != nil {
		e.Body = jq.StrOf(b)
	}
}

// entryOptions decodes the optional enrichment tail: suggestion, kb,
// in-bounds, rigor, and severity.
func entryOptions(e *FindingEntry, obj *jq.Obj) {
	e.Suggestion = fieldOptString(obj, "suggestion")
	e.KB = fieldOptStrings(obj, "kb")
	e.InBounds, _ = obj.Get("inBounds")
	e.Rigor, _ = obj.Get("rigor")
	e.Severity, _ = obj.Get("severity")
}

// entryStamps decodes identity, lifecycle, and history: sig, version,
// status, stamps, triage attribution, and the append-only history.
func entryStamps(e *FindingEntry, obj *jq.Obj) {
	e.Sig, _ = fieldString(obj, "sig")
	e.SigVersion, _ = fieldString(obj, "sig_version")
	e.Status, _ = fieldString(obj, keyStatus)
	e.FirstSeen, _ = fieldString(obj, "firstSeen")
	e.LastSeen, _ = fieldString(obj, "lastSeen")
	if r, _ := obj.Get("rationale"); r != nil {
		e.Rationale = jq.StrOf(r)
	}
	if t, _ := obj.Get("triagedAt"); t != nil {
		e.TriagedAt = jq.StrOf(t)
	}
	if t, _ := obj.Get("triagedBy"); t != nil {
		e.TriagedBy = jq.StrOf(t)
	}
	e.History = historyOf(obj)
}

// entryExtra keeps unknown store keys in Extra in file order so a re-emit
// preserves them byte-identically.
func entryExtra(e *FindingEntry, obj *jq.Obj) {
	for _, k := range obj.Keys {
		if !findingKeys[k] {
			if e.Extra == nil {
				e.Extra = jq.NewObj()
			}
			v, _ := obj.Get(k)
			e.Extra.Set(k, v)
		}
	}
}

// fieldString reads one required string field.
func fieldString(obj *jq.Obj, key string) (string, bool) {
	v, _ := obj.Get(key)
	s, ok := v.(string)
	return s, ok
}

// fieldOptString reads one optional string field (nil reads nil).
func fieldOptString(obj *jq.Obj, key string) *string {
	v, _ := obj.Get(key)
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// fieldOptStrings reads one optional string-list field (nil reads nil).
func fieldOptStrings(obj *jq.Obj, key string) *[]string {
	v, _ := obj.Get(key)
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return &out
}

// historyOf decodes one entry history.
func historyOf(obj *jq.Obj) []HistoryEvent {
	v, _ := obj.Get("history")
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]HistoryEvent, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		out = append(out, historyEventOf(m))
	}
	return out
}

// historyEventOf decodes one history event.
func historyEventOf(m *jq.Obj) HistoryEvent {
	e := HistoryEvent{}
	e.At, _ = fieldString(m, "at")
	e.Event, _ = fieldString(m, "event")
	if v, _ := m.Get("actor"); v != nil {
		e.Actor = jq.StrOf(v)
	}
	if v, _ := m.Get("note"); v != nil {
		e.Note = jq.StrOf(v)
	}
	if v, _ := m.Get("sha"); v != nil {
		e.SHA = jq.StrOf(v)
	}
	if v, _ := m.Get("kb_credit"); v != nil {
		e.KBCredit = kbCreditOf(v)
	}
	return e
}

// kbCreditOf decodes one history credit list.
func kbCreditOf(v any) []KBCredit {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]KBCredit, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		rule, _ := fieldString(m, "rule")
		to, _ := fieldString(m, "to")
		out = append(out, KBCredit{Rule: rule, To: to})
	}
	return out
}
