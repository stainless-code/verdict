package core

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// This file holds the wire shapes the published JSON Schemas are generated
// from (aliased in internal/schemagen/model.go) plus the typed report
// response. Struct field order is wire order: core renders through the
// ordered jq printer, never through encoding/json map order.

// Envelope is the single JSON object every --json stdout emits.
type Envelope struct { //nolint:govet // field order is the published property order
	SchemaVersion string          `const:"1"          json:"schema_version"`
	Verb          string          `json:"verb"`
	Repo          string          `json:"repo"`
	Pr            *int            `json:"pr"`
	At            string          `format:"date-time" json:"at"`
	Next          string          `json:"next"`
	Warnings      []string        `json:"warnings"`
	Data          *map[string]any `json:"data"`
	Error         *SchemaError    `anyOf:"null"       json:"error"`
}

// SchemaError is a typed refusal inside the envelope.
type SchemaError struct { //nolint:govet // field order is the published property order
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	Candidates []Candidate `json:"candidates,omitempty"`
	RetryAt    string      `format:"date-time"          json:"retry_at,omitempty"`
}

// Candidate is one refusal suggestion (oversize-fetch refusals only).
type Candidate struct {
	Kind       string `json:"kind"`
	Suggestion string `json:"suggestion"`
}

// AnchorsSummary counts kept and moved anchors.
type AnchorsSummary struct {
	Kept  int `json:"kept"  min:"0"`
	Moved int `json:"moved" min:"0"`
}

// FindingsCounts counts the finding states a post reports.
type FindingsCounts struct {
	Open      int `json:"open"      min:"0"`
	Deferred  int `json:"deferred"  min:"0"`
	Addressed int `json:"addressed" min:"0"`
}

// FindingItem is one ranked or merged finding locator. Raw is the ordered
// store record (unknown keys included); the locator and rank are appended by
// [FindingItem.Wire], matching the merge order.
type FindingItem struct {
	Raw      *jq.Obj `json:"-"`
	Sig      string  `json:"sig"`
	Status   string  `enum:"open|addressed|wont-fix|false-positive|deferred" json:"status"`
	Anchored any     `json:"anchored,omitempty"                              type:"boolean|string|null"`
	Locator  string  `json:"locator,omitempty"`
	Rank     int     `json:"-"`
}

// Wire renders the item in wire order: the store record's keys first, then
// locator and rank.
func (f *FindingItem) Wire() *jq.Obj {
	if f.Raw == nil {
		return jq.NewObj("locator", f.Locator, "rank", f.Rank)
	}
	o := f.Raw.Clone()
	o.Set("locator", f.Locator)
	o.Set("rank", f.Rank)
	return o
}

// Provenance is the tool_version/prompts_hash/model triple on a run.
type Provenance struct { //nolint:govet // field order is the published property order
	ToolVersion string  `json:"tool_version"`
	PromptsHash string  `json:"prompts_hash"`
	Model       *string `json:"model"`
}

// ReportHead is the fetched/base/live head triple.
type ReportHead struct {
	Fetched *string `json:"fetched"`
	Base    *string `json:"base"`
	Live    *string `json:"live"`
	Stale   bool    `json:"stale"`
}

// ReportVerdict counts statuses across the displayed ranking.
type ReportVerdict struct {
	Open      int `json:"open"      min:"0"`
	Deferred  int `json:"deferred"  min:"0"`
	Addressed int `json:"addressed" min:"0"`
	Terminal  int `json:"terminal"  min:"0"`
}

// ReportCounts counts finding kinds and anchoring.
type ReportCounts struct {
	Inline     int `json:"inline"     min:"0"`
	Section    int `json:"section"    min:"0"`
	Anchored   int `json:"anchored"   min:"0"`
	Unanchored int `json:"unanchored" min:"0"`
}

// ReportPanel is the panel source plus its member list.
type ReportPanel struct {
	Source  *string `json:"source"`
	Members []any   `json:"members"`
}

// ReportFilters echoes the active status/kind filters.
type ReportFilters struct {
	Status string `json:"status"`
	Kind   string `json:"kind"`
}

// ReportCost is the summed cost rollup carried by report --cost.
type ReportCost struct { //nolint:govet // field order is the published property order
	Runs              int       `json:"runs"                          min:"0"`
	RunsWithCost      int       `json:"runs_with_cost"                min:"0"`
	InputTokens       int       `json:"input_tokens"                  min:"0"`
	OutputTokens      int       `json:"output_tokens"                 min:"0"`
	TotalTokens       int       `json:"total_tokens"                  min:"0"`
	CachedInputTokens int       `json:"cached_input_tokens,omitempty" min:"0"`
	EstUSD            *float64  `json:"est_usd"`
	ByTier            CostTiers `json:"by_tier"`
	Receipts          []any     `json:"receipts,omitempty"`
}

// CostTiers carries the three named effort-tier rollups.
type CostTiers struct {
	Cheap    CostTier `json:"cheap"`
	Standard CostTier `json:"standard"`
	Deep     CostTier `json:"deep"`
}

// CostTier is one effort tier's token rollup.
type CostTier struct {
	Runs         int `json:"runs"          min:"0"`
	InputTokens  int `json:"input_tokens"  min:"0"`
	OutputTokens int `json:"output_tokens" min:"0"`
	TotalTokens  int `json:"total_tokens"  min:"0"`
}

// ReportCompleteness compares fetched, posted and reviewed heads.
type ReportCompleteness struct { //nolint:govet // field order is the published property order
	Reviewed    bool    `json:"reviewed"`
	FetchedHead *string `json:"fetched_head"`
	PostedHead  *string `json:"posted_head"`
}

// ReportGate is the composed gate result: every dimension this run requested,
// in `findings, head, live_threads` order. Passed is true iff no dimension
// failed or read unknown, so a skip stays neutral.
type ReportGate struct { //nolint:govet // published wire order: passed before dimensions
	Passed     bool            `json:"passed"`
	Dimensions []GateDimension `json:"dimensions"`
}

// GateDimension is one gate dimension's verdict. Detail is human text no
// script parses; Count is the dimension's own tally, absent when the
// dimension has none to report.
type GateDimension struct { //nolint:govet // field order is the published property order
	ID     string `enum:"findings|head|live_threads" json:"id"`
	Status string `enum:"pass|fail|skip|unknown"     json:"status"`
	Detail string `json:"detail"`
	Count  *int   `json:"count"                      min:"0"`
}

// Wire renders the gate result in the published key order.
func (g *ReportGate) Wire() *jq.Obj {
	dims := make([]any, 0, len(g.Dimensions))
	for i := range g.Dimensions {
		dims = append(dims, g.Dimensions[i].wire())
	}
	return jq.NewObj("passed", g.Passed, "dimensions", dims)
}

// wire renders one dimension in the published key order.
func (d *GateDimension) wire() *jq.Obj {
	var count any
	if d.Count != nil {
		count = *d.Count
	}
	return jq.NewObj("id", d.ID, "status", d.Status, "detail", d.Detail, "count", count)
}

// ReportData is the report verb's typed --json data profile. Field order is
// the published insertion order; CostRaw carries the ordered rollup
// (including receipts and unknown keys) so the envelope re-emits it verbatim.
type ReportData struct { //nolint:govet // field order is the published property order
	GeneratedAt  string              `format:"date-time"            json:"generated_at"`
	Dir          string              `json:"dir"`
	Head         ReportHead          `json:"head"`
	Verdict      ReportVerdict       `json:"verdict"`
	Counts       ReportCounts        `json:"counts"`
	Panel        ReportPanel         `json:"panel"`
	Patch        any                 `json:"patch"`
	Output       *string             `json:"output"`
	Filters      ReportFilters       `json:"filters"`
	Items        []FindingItem       `json:"items"`
	Cost         *ReportCost         `json:"cost,omitempty"`
	Completeness *ReportCompleteness `json:"completeness,omitempty"`
	Gate         *ReportGate         `json:"gate,omitempty"`
	CostRaw      *jq.Obj             `json:"-"`
}

// Wire renders the data object in wire order. CostRaw wins over the typed
// cost so the ordered rollup (receipts included) re-emits byte-identically.
func (d *ReportData) Wire() *jq.Obj {
	o := structObj(reflect.ValueOf(d).Elem())
	if d.CostRaw != nil {
		o.Set("cost", d.CostRaw)
	}
	return o
}

// Wirable lets a type override its reflected wire representation.
type Wirable interface {
	Wire() *jq.Obj
}

// Wire converts a typed value to the ordered jq representation, using each
// struct's field order and json/omitempty tags. Nil pointers and interfaces
// read null; nil slices read null; omitted empty fields drop out. Plain maps
// render sorted (they never carry a frozen order); *jq.Obj values pass
// through untouched.
func Wire(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) && rv.IsNil() {
		return nil
	}
	return wireValue(rv)
}

// wireValue converts one reflected value.
//
//nolint:revive,cyclop // one branch per JSON kind keeps the mapping auditable
func wireValue(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	if w, ok := wirable(rv); ok {
		return w.Wire()
	}
	if rv.Type() == reflect.TypeFor[*jq.Obj]() {
		if rv.IsNil() {
			return nil
		}
		return rv.Interface()
	}
	if rv.Type() == reflect.TypeFor[jq.Number]() || rv.Type() == reflect.TypeFor[json.Number]() {
		return rv.Interface()
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return wireValue(rv.Elem())
	case reflect.Struct:
		return structObj(rv)
	case reflect.Slice:
		if rv.IsNil() {
			return nil
		}
		out := make([]any, rv.Len())
		for i := range rv.Len() {
			out[i] = wireValue(rv.Index(i))
		}
		return out
	case reflect.Map:
		return mapValue(rv)
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	default:
		return rv.Interface()
	}
}

// wirable finds the Wirable implementation for v (value or pointer receiver).
func wirable(rv reflect.Value) (Wirable, bool) {
	if w, ok := reflect.TypeAssert[Wirable](rv); ok {
		return w, true
	}
	if rv.CanAddr() {
		return reflect.TypeAssert[Wirable](rv.Addr())
	}
	return nil, false
}

// structObj renders one struct in field order, honoring json and omitempty
//
// tags. Fields tagged json:"-" (including the FindingItem raw passthrough)
// stay out of the wire object.
//
//nolint:revive // the field-tag walk reads clearer inline than behind a helper
func structObj(rv reflect.Value) *jq.Obj {
	o := jq.NewObj()
	for i := range rv.NumField() {
		field := rv.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, skip, omit := fieldTag(&field)
		if skip {
			continue
		}
		fv := rv.Field(i)
		if omit && emptyValue(fv) {
			continue
		}
		o.Set(name, wireValue(fv))
	}
	return o
}

// fieldTag parses one field's json tag: name, skip, and omitempty.
func fieldTag(field *reflect.StructField) (name string, skip, omit bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", true, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		return "", true, false
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omit = true
		}
	}
	return name, false, omit
}

// emptyValue mirrors encoding/json's omitempty test for the kinds the wire
// shapes use (pointers, interfaces, slices, maps, strings, bools, numbers).
func emptyValue(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	case reflect.String:
		return rv.Len() == 0
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() == 0
	default:
		return false
	}
}

// mapValue converts a string-keyed map, preserving unknown keys.
func mapValue(rv reflect.Value) any {
	if rv.Type().Key().Kind() != reflect.String {
		return rv.Interface()
	}
	out := make(map[string]any, rv.Len())
	for _, key := range rv.MapKeys() {
		out[key.String()] = wireValue(rv.MapIndex(key))
	}
	return out
}

// StateWire, when installed, renders the top-level `state` key between `at`
// and `next`. It is a prototype seam: the statedir package installs it from
// main, so package unit tests keep rendering the historical envelope.
var StateWire func() any

// RenderEnvelope builds the frozen envelope object: schema_version, verb,
// repo, pr, at, [state], next, warnings, data, error, in that order. errObj
// is a [*SchemaError] or an already-ordered [*jq.Obj] (the CLI refusal
// object, which carries candidates); both render through the same wire
// walker.
func RenderEnvelope(verb, repo, pr, at, next string, warnings []string, data any, errObj any) *jq.Obj {
	o := jq.NewObj(
		"schema_version", "1",
		"verb", verb,
		"repo", repo,
		"pr", PrJSON(pr),
		"at", at,
	)
	if StateWire != nil {
		o.Set("state", StateWire())
	}
	o.Set("next", next)
	o.Set("warnings", StringValues(warnings))
	o.Set("data", data)
	o.Set("error", wireValue(reflect.ValueOf(errObj)))
	return o
}

// StringValues converts warnings to a non-nil []any JSON array.
func StringValues(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// PrJSON reads a PR positional: a valid JSON number literal reads as a
// number, everything else reads null.
func PrJSON(pr string) any {
	if isJSONNumber(pr) {
		return jq.Number(pr)
	}
	return nil
}

// isJSONNumber reports whether s is a valid JSON number literal.
func isJSONNumber(s string) bool {
	return numberRe.MatchString(s)
}

// numberRe matches a JSON number literal.
var numberRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
