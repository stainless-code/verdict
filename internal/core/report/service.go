// Package reportcore is the report family's domain service: `report`,
// `next`, and `show` behind typed (ctx, request) → (response, refusal)
// calls. It owns store reads, ranking, the gate dimensions, and the pure
// render inputs; internal/report owns argv parsing, stream writes, and exit
// mapping.
package reportcore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	statusAll         = "all"
	statusOpen        = "open"
	kindInline        = "inline"
	kindSection       = "section"
	tierCritical      = "critical"
	tierImportant     = "important"
	tierInformational = "informational"
	codeCanceled      = "canceled"
	codeBadCategory   = "bad-category"
	codeReportFailed  = "report-failed"
	prJSONSuffix      = "/PR.json"
	fullDiffSuffix    = "/full.diff"
	strTrue           = "true"
	strFalse          = "false"
	patchSuffix       = "/patch.json"
	liveAuto          = "auto"
	newline           = "\n"
	quoteMark         = "\""
	slashSep          = "/"
	tabSep            = "\t"
	newMarker         = "NEW"
	nullPath          = "null"
	hunksTSVSuffix    = "/hunks.tsv"
	rolesTxtSuffix    = "/roles.txt"
	hashSpace         = "## "
	dashSpace         = "- "
)

// Flags aliases core's resolved report options.
type Flags = core.ReportFlags

type state struct { // field grouping documents the loaded dimensions; alignment is irrelevant here
	req        *core.ReportRequest
	flags      Flags
	warnings   []string
	counts     core.ReportCounts
	cost       *jq.Obj
	receipts   []any
	entries    []any
	ranked     []readcommon.Ranked
	panel      []any
	patch      any
	comp       *core.ReportCompleteness
	dir        string
	now        string
	headSHA    string
	baseSHA    string
	panelSrc   string
	compPosted string
	live       liveState
	nOpen      int
	nAllOpen   int
	gate       bool
	compOK     bool
}

type liveState struct {
	status string
	err    string
	open   int
}

const (
	liveUnset         = "unset"
	liveFail          = "fail"
	livePass          = "pass"
	liveUnknown       = "unknown"
	liveUnknownStrict = "unknown-strict"
	liveSkip          = "skip"
	liveSkipExplicit  = "skip-explicit"
)

// Run loads the report dimensions and returns the typed response. It never
// writes stdout/stderr or exits; artifacts are returned as bytes for the
// adapter to write.
func Run(ctx context.Context, req *core.ReportRequest) (*core.ReportResponse, *core.RefusalError) {
	flags := normalizeFlags(req)
	if ref := validateCategory(flags.Category); ref != nil {
		return nil, ref
	}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before report completed"}
	}
	st := &state{
		req:      req,
		flags:    flags,
		warnings: append([]string{}, req.Warnings...),
		gate:     flags.Check != "" || flags.Completeness,
	}
	if err := loadState(ctx, st); err != nil {
		return nil, err
	}
	runLiveProbe(ctx, st)
	return buildResponse(st), nil
}

func normalizeFlags(req *core.ReportRequest) Flags {
	flags := req.Flags
	flags.Status = orDefault(flags.Status, statusAll)
	flags.Kind = orDefault(flags.Kind, statusAll)
	flags.Live = orDefault(flags.Live, liveAuto)
	return flags
}

// validateCategory refuses an unknown category filter in core so direct Go
// API, MCP, and service callers get the same typed bad-category refusal the
// CLI adapter already reports as exit 2: a filter typo must never read as a
// passing gate with zero findings.
func validateCategory(category string) *core.RefusalError {
	if category == "" || core.ValidCategory(category) {
		return nil
	}
	return &core.RefusalError{
		Code: codeBadCategory,
		Message: fmt.Sprintf("report: unknown category '%s' (want %s)",
			category, strings.Join(core.CategoryValues, "|")),
	}
}

func orDefault(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func loadState(ctx context.Context, st *state) *core.RefusalError {
	st.dir = readcommon.ResolveBundleDir(st.req.Pr)
	entries, err := readcommon.LoadFindings(st.dir)
	if err != nil {
		return st.withWarnings(loadRefusal(err))
	}
	if err := readcommon.ValidateOverrides(); err != nil {
		return st.withWarnings(loadRefusal(err))
	}
	st.now = st.req.TimeUTC()
	st.entries = entries
	st.headSHA, st.baseSHA = bundleHeads(st.dir)
	st.panel, st.panelSrc = bundlePanel(st.dir)
	if isRegular(st.dir + patchSuffix) {
		patch, err := bundlePatch(st.dir)
		if err != nil {
			return st.withWarnings(err)
		}
		st.patch = patch
	}
	st.ranked = readcommon.Rank(filterCategory(st.entries, st.flags.Category), st.flags.Status, st.flags.Kind)
	st.nOpen = readcommon.CountStatus(st.ranked, statusOpen)
	st.nAllOpen = readcommon.CountOpen(entries)
	st.counts = storeCounts(entries)
	loadStateCost(st)
	loadStateCompleteness(ctx, st)
	return nil
}

func loadRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: "internal", Message: err.Error()}
}

func loadStateCost(st *state) {
	if !st.flags.Cost {
		return
	}
	res := readcommon.CostRollup(st.dir)
	for _, w := range res.Warns {
		st.warn(w)
	}
	st.cost = res.Cost
	st.receipts = res.Receipts
}

func loadStateCompleteness(ctx context.Context, st *state) {
	if !st.flags.Completeness {
		return
	}
	st.comp, st.compOK, st.compPosted = completeness(ctx, st.commander(), st.dir, st.headSHA, st.baseSHA)
	if _, ok := identity.Verify(ctx, st.commander(), st.dir, st.headSHA, st.baseSHA); !ok {
		st.warn("patch identity unavailable — completeness compares heads")
	}
}

func storeCounts(entries []any) core.ReportCounts {
	return core.ReportCounts{
		Inline:     countKind(entries, kindInline),
		Section:    countKind(entries, kindSection),
		Anchored:   countInlineAnchored(entries),
		Unanchored: countInlineLoose(entries),
		ByCategory: core.CategoryCounts(entries),
	}
}

// filterCategory narrows entries to one category for the ranked display; an
// empty filter reads everything. The gate and the counts keep the whole
// store and apply the category themselves.
func filterCategory(entries []any, category string) []any {
	if category == "" {
		return entries
	}
	kept := make([]any, 0, len(entries))
	for _, e := range entries {
		if core.CategoryOf(e) == category {
			kept = append(kept, e)
		}
	}
	return kept
}

func countKind(entries []any, kind string) int {
	return countWhere(entries, func(e any) bool { return readcommon.KindOf(e) == kind })
}

func countInlineAnchored(entries []any) int {
	return countWhere(entries, func(e any) bool { return inlineAnchored(e, true) })
}

func countInlineLoose(entries []any) int {
	return countWhere(entries, func(e any) bool { return inlineAnchored(e, false) })
}

func countWhere(entries []any, match func(any) bool) int {
	n := 0
	for _, e := range entries {
		if match(e) {
			n++
		}
	}
	return n
}

func inlineAnchored(e any, want bool) bool {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	if readcommon.KindOf(e) != kindInline {
		return false
	}
	anchored, _ := m.Get("anchored")
	return (anchored == true) == want
}

func bundleHeads(dir string) (head, base string) {
	if isRegular(dir + prJSONSuffix) {
		return readcommon.PrField(dir, "head_sha"), readcommon.PrField(dir, "base_sha")
	}
	if isRegular(dir + "/source.json") {
		return readcommon.JSONStr(dir+"/source.json", "head_sha"), readcommon.JSONStr(dir+"/source.json", "base_sha")
	}
	return "", ""
}

func bundlePanel(dir string) ([]any, string) {
	path := dir + "/panel"
	if !isRegular(path) {
		return nil, ""
	}
	raw, err := readFile(path)
	if err != nil {
		return nil, ""
	}
	var members []any
	for line := range strings.SplitSeq(string(raw), newline) {
		if line == "" {
			continue
		}
		members = append(members, line)
	}
	return members, "panel"
}

func bundlePatch(dir string) (any, *core.RefusalError) {
	raw, err := readFile(dir + patchSuffix)
	if err != nil {
		return nil, corruptPatch(dir)
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, corruptPatch(dir)
	}
	return doc, nil
}

func corruptPatch(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code:    "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (re-run post to rebuild, or restore the file)", dir),
	}
}
