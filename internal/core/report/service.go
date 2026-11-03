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
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Shared vocabulary: filter values, tier names, refusal codes, flags, and
// bundle paths repeated across the report-family verbs.
const (
	statusAll         = "all"
	statusOpen        = "open"
	kindInline        = "inline"
	kindSection       = "section"
	tierCritical      = "critical"
	tierImportant     = "important"
	tierInformational = "informational"
	codeCanceled      = "canceled"
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

// state carries one loaded report run.
type state struct { //nolint:govet // field grouping documents the loaded dimensions; alignment is irrelevant here
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

// liveState carries the live-thread probe outcome.
type liveState struct {
	status string
	err    string
	open   int
}

// live statuses, mirroring the shell's live_status values.
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
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before report completed"}
	}
	st := &state{
		req:      req,
		flags:    flags,
		warnings: append([]string{}, req.Warnings...),
		gate:     flags.Check != "" || flags.Completeness,
	}
	if err := loadState(st); err != nil {
		return nil, err
	}
	runLiveProbe(ctx, st)
	return buildResponse(st), nil
}

// normalizeFlags applies the shell's flag defaults to a resolved request.
func normalizeFlags(req *core.ReportRequest) Flags {
	flags := req.Flags
	flags.Status = orDefault(flags.Status, statusAll)
	flags.Kind = orDefault(flags.Kind, statusAll)
	flags.Live = orDefault(flags.Live, liveAuto)
	return flags
}

// orDefault mirrors ${2:-default}: a missing or empty value reads the
// default.
func orDefault(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// withWarnings attaches the warnings accumulated so far to a refusal.
func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

// loadState resolves the bundle, guards the store, and loads every local
// dimension: heads, panel, patch, ranked findings, counts, cost, and the
// completeness record.
func loadState(st *state) *core.RefusalError {
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
	st.ranked = readcommon.Rank(entries, st.flags.Status, st.flags.Kind)
	st.nOpen = readcommon.CountStatus(st.ranked, statusOpen)
	st.nAllOpen = readcommon.CountOpen(entries)
	st.counts = storeCounts(entries)
	loadStateCost(st)
	loadStateCompleteness(st)
	return nil
}

// loadRefusal recovers the typed refusal carried by a store-guard error.
// LoadFindings and ValidateOverrides only fail typed, so the As-chain
// always hits; the fallback is defensive and never fires in practice.
func loadRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: "internal", Message: err.Error()}
}

// loadStateCost fills the cost rollup when --cost is set, warning on
// partial receipts like the shell's cost_rollup warns.
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

// loadStateCompleteness fills the completeness record when --completeness
// is set.
func loadStateCompleteness(st *state) {
	if !st.flags.Completeness {
		return
	}
	st.comp, st.compOK, st.compPosted = completeness(st.dir, st.headSHA)
}

// storeCounts ports the counts object over the whole findings store.
func storeCounts(entries []any) core.ReportCounts {
	return core.ReportCounts{
		Inline:     countKind(entries, kindInline),
		Section:    countKind(entries, kindSection),
		Anchored:   countInlineAnchored(entries),
		Unanchored: countInlineLoose(entries),
	}
}

// countKind counts entries of one kind.
func countKind(entries []any, kind string) int {
	return countWhere(entries, func(e any) bool { return readcommon.KindOf(e) == kind })
}

// countInlineAnchored counts anchored inline findings.
func countInlineAnchored(entries []any) int {
	return countWhere(entries, func(e any) bool { return inlineAnchored(e, true) })
}

// countInlineLoose counts unanchored inline findings.
func countInlineLoose(entries []any) int {
	return countWhere(entries, func(e any) bool { return inlineAnchored(e, false) })
}

// countWhere counts entries matching match.
func countWhere(entries []any, match func(any) bool) int {
	n := 0
	for _, e := range entries {
		if match(e) {
			n++
		}
	}
	return n
}

// inlineAnchored reports whether e is an inline finding whose anchored flag
// equals want.
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

// bundleHeads reads the fetched/base heads from PR.json or source.json.
func bundleHeads(dir string) (head, base string) {
	if isRegular(dir + prJSONSuffix) {
		return readcommon.PrField(dir, "head_sha"), readcommon.PrField(dir, "base_sha")
	}
	if isRegular(dir + "/source.json") {
		return jsonField(dir+"/source.json", "head_sha"), jsonField(dir+"/source.json", "base_sha")
	}
	return "", ""
}

// bundlePanel reads the bundle panel members ([] when absent).
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

// bundlePatch reads the patch record; the caller checks isRegular first, so
// a missing store never reaches here. R1: an unreadable patch store refuses
// typed corrupt-store instead of dying in the envelope jq. The record
// decodes ordered: the envelope re-emits it in file key order like the
// shell's --argjson passthrough (R2).
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

// corruptPatch builds the patch-store refusal.
func corruptPatch(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code:    "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (re-run post to rebuild, or restore the file)", dir),
	}
}
