// Package trendcore is the trend verb's domain service: the portfolio
// rollup over every reviewed bundle's findings store, plus the opt-in KB
// overlap audit. Pure read, writes nothing, no network — rescan semantics,
// so hand-edited stores stay truthful. internal/trend owns argv parsing,
// progress routing, envelope rendering, and exit mapping.
package trendcore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// skipWarn heads every warning a trend scan renders about an entry it passed
// over: one bundle, one line, so the rollup never silently drops a review.
const skipWarn = "trend skips "

// Request carries the resolved trend invocation: the shared read runtime
// plus the --overlap audit flag.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Rules      bool
	Resolution bool
	Overlap    bool
	Warnings   []string
}

// Response carries the completed rollup: the envelope fields and the
// typed data profile. Human carries the human-mode summary (one line per
// element); warnings stream live through the runtime progress observer,
// so human lines buffer here to preserve their after-warnings position.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	At       string
	Next     string
	Warnings []string
	Human    []string
	Data     *Data
}

// Data is the trend verb's --json data profile: the generated_at stamp,
// the portfolio rollup keys, the per-bundle rows, the optional overlap
// join, and the optional corpus measurement. Field order is the published
// envelope construction order.
type Data struct { //nolint:govet // field order is the published property order
	Rules               *jq.Obj
	Resolution          any
	ResolutionRequested bool
	GeneratedAt         string
	Rollup              rollup
	Rows                []row
	Overlap             *overlapRollup
	Corpus              *corpusRollup
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	middle := rollupDoc(&d.Rollup)
	data := jq.NewObj("generated_at", d.GeneratedAt)
	for _, k := range middle.Keys {
		v, _ := middle.Get(k)
		data.Set(k, v)
	}
	perBundle := make([]any, 0, len(d.Rows))
	for i := range d.Rows {
		perBundle = append(perBundle, rowDoc(&d.Rows[i]))
	}
	data.Set("per_bundle", perBundle)
	if d.Rules != nil {
		data.Set("rules", d.Rules)
	}
	if d.ResolutionRequested {
		data.Set("resolution", d.Resolution)
	}
	if d.Overlap != nil {
		data.Set("overlap", overlapDoc(d.Overlap))
	}
	if d.Corpus != nil {
		data.Set("corpus", corpusDoc(d.Corpus))
	}
	return data
}

type state struct {
	req      *Request
	warnings []string
	lines    []string
	corpora  []corpusPoint
}

// Run executes the portfolio rollup over every reviewed bundle.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before trend completed"}
	}
	now := st.req.TimeUTC()
	rows := collectRows(st)
	rollup := rollupRows(rows)
	corpus := corpusRollupOf(st.corpora)
	var overlap *overlapRollup
	if req.Overlap {
		overlap = overlapAudit(st, rows)
	}
	st.emitHuman(&rollup, overlap, corpus)
	var resolution any
	if req.Resolution {
		resolution = resolutionAudit(st, rows)
	}
	var rules *jq.Obj
	if req.Rules {
		rules = ruleAudit(st, rows)
	}
	next := "calibrate recent bundles for per-model tier precision"
	if rollup.bundles == 0 {
		next = "no reviewed bundles yet — fetch + post/record first"
	}
	return &Response{
		Repo:     req.Repo,
		At:       now,
		Next:     next,
		Warnings: st.warnings,
		Human:    st.lines,
		Data: &Data{
			Rules: rules, Resolution: resolution, ResolutionRequested: req.Resolution,
			GeneratedAt: now, Rollup: rollup, Rows: rows, Overlap: overlap, Corpus: corpus,
		},
	}, nil
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) human(line string) {
	st.lines = append(st.lines, line)
}

type row struct {
	prov       *jq.Obj
	byStatus   map[string]int
	byTier     map[string]int
	byCategory map[string]int
	bundle     string
	at         string
	risk       string
	models     []any
	efforts    []any
	findings   []any
	hasAt      bool
}

func collectRows(st *state) []row {
	entries, err := os.ReadDir(readcommon.StateDir(st.req.RepoRootPath()))
	if err != nil {
		return nil
	}
	var rows []row
	replays := 0
	for _, e := range entries {
		bundleRow, replay, ok := st.walkEntry(e)
		switch {
		case replay:
			replays++
		case ok:
			rows = append(rows, bundleRow)
		}
	}
	if replays > 0 {
		st.warn(fmt.Sprintf(skipWarn+"%d backtest bundle(s) (replay input, not your review history)", replays))
	}
	return rows
}

// walkEntry folds one state-directory entry: a corpus is read for its
// measurement, a replay bundle is reported as skipped, and anything else is a
// history row when its findings store is readable.
func (st *state) walkEntry(e os.DirEntry) (row, bool, bool) {
	if !e.IsDir() {
		return row{}, false, false
	}
	name := e.Name()
	dir, ok := st.listedBundleDir(name)
	if !ok {
		return row{}, false, false
	}
	if corpuscore.IsDir(dir) {
		st.readCorpus(dir)
		return row{}, false, false
	}
	if isRegular(dir + "/" + readcommon.BacktestMarkerFile) {
		return row{}, true, false
	}
	bundleRow, ok := readBundleRow(st, dir, name)
	return bundleRow, false, ok
}

// listedBundleDir resolves one state-directory entry's bundle dir. A name the
// shared guard refuses is skipped with a warning: a listing carries names, not
// paths, so nothing outside the bundle home is ever read. The listed name keeps
// its own directory: the bare-name fallback ResolveBundleDir applies to an
// operator's positional would let a listed `42` read `local-42`, counting that
// bundle twice and dropping `42` from the totals with no warning.
func (st *state) listedBundleDir(name string) (string, bool) {
	dir, ref := config.BundleDir(st.req.RepoRootPath(), name)
	if ref != nil {
		st.warn(skipWarn + name + " (" + ref.Message + ")")
		return "", false
	}
	return filepath.ToSlash(dir), true
}

func readBundleRow(st *state, dir, name string) (row, bool) {
	bundle := dir + "/"
	raw, err := jq.CappedFile(bundle+"findings.json", jq.MaxFindingsBytes)
	if err != nil {
		st.warn(skipWarn + bundle + " (findings.json unreadable or not a findings array)")
		return row{}, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		st.warn(skipWarn + bundle + " (findings.json unreadable or not a findings array)")
		return row{}, false
	}
	findings, ok := doc.([]any)
	if !ok || !readcommon.ValidEntries(findings) {
		st.warn(skipWarn + bundle + " (findings.json unreadable or not a findings array)")
		return row{}, false
	}
	warnMalformedWithheld(st, findings, bundle)
	return buildRow(bundle, name, findings), true
}

func buildRow(bundle, name string, findings []any) row {
	at, hasAt := bundleTime(bundle)
	cleanDir := strings.TrimSuffix(bundle, "/")
	return row{
		bundle:     name,
		at:         at,
		hasAt:      hasAt,
		risk:       risk.BundleBand(cleanDir, readcommon.OpenBundle(cleanDir).Head(), findings),
		models:     bundleModels(bundle),
		efforts:    bundleEfforts(bundle),
		prov:       bundleProvenance(bundle),
		findings:   findings,
		byStatus:   statusCounts(findings),
		byTier:     tierCounts(findings),
		byCategory: categoryCounts(findings),
	}
}

func bundleTime(bundle string) (string, bool) {
	if isRegular(bundle + "post.json") {
		if at := readcommon.JSONRaw(bundle+"post.json", "posted_at"); at != "" {
			return at, true
		}
	}
	if isRegular(bundle + "record.json") {
		if at := readcommon.JSONRaw(bundle+"record.json", "recorded_at"); at != "" {
			return at, true
		}
	}
	return "", false
}

func bundleModels(bundle string) []any {
	costs := readcommon.CostRecords(bundle)
	if costs == nil {
		return []any{}
	}
	var names []any
	for _, rec := range costs {
		models, ok := readcommon.CostModels(rec)
		if !ok {
			return []any{}
		}
		if names, ok = appendModelNames(names, models); !ok {
			return []any{}
		}
	}
	return dedupeSorted(names)
}

func appendModelNames(names, models []any) ([]any, bool) {
	for _, mm := range models {
		var ok bool
		if names, ok = appendModelName(names, mm); !ok {
			return nil, false
		}
	}
	return names, true
}

func appendModelName(names []any, mm any) ([]any, bool) {
	m, ok := mm.(map[string]any)
	if !ok {
		if mm == nil {
			return names, true
		}
		return nil, false
	}
	name, ok := m["name"]
	if !ok || name == nil || name == false {
		return names, true
	}
	return append(names, name), true
}

func bundleEfforts(bundle string) []any {
	costs := readcommon.CostRecords(bundle)
	if costs == nil {
		return []any{}
	}
	seen := map[string]bool{}
	var efforts []any
	for _, rec := range costs {
		m, ok := rec.(map[string]any)
		if !ok {
			if rec == nil {
				efforts = appendTier(efforts, seen, "standard")
				continue
			}
			return []any{}
		}
		efforts = appendTier(efforts, seen, readcommon.EffortTier(m["effort"]))
	}
	return efforts
}

func appendTier(efforts []any, seen map[string]bool, tier string) []any {
	if !seen[tier] {
		seen[tier] = true
		efforts = append(efforts, tier)
	}
	return efforts
}

func bundleProvenance(bundle string) *jq.Obj {
	for _, rec := range []string{bundle + "post.json", bundle + "record.json"} {
		if !isRegular(rec) {
			continue
		}
		if prov := provenanceTriple(rec); prov != nil {
			return prov
		}
	}
	return unmarkedProvenance()
}

// provenanceTriple reads one bundle record under PRE-01's cap, so an
// oversized record is unmarked instead of allocated and decoded.
func provenanceTriple(path string) *jq.Obj {
	raw, err := jq.CappedFile(path, jq.MaxPRBytes)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	return jq.NewObj(
		"tool_version", m["tool_version"],
		"prompts_hash", m["prompts_hash"],
		"model", m["model"],
	)
}

func unmarkedProvenance() *jq.Obj {
	return jq.NewObj("tool_version", nil, "prompts_hash", nil, "model", nil)
}

func statusCounts(findings []any) map[string]int {
	counts := map[string]int{
		statusOpen: 0, statusAddressed: 0, statusWontFix: 0, statusFalsePositive: 0, statusDeferred: 0,
	}
	for _, e := range findings {
		if _, ok := counts[readcommon.StatusOf(e)]; ok {
			counts[readcommon.StatusOf(e)]++
		}
	}
	return counts
}

func tierCounts(findings []any) map[string]int {
	counts := map[string]int{
		tierCritical: 0, tierImportant: 0, tierInformational: 0, tierUntagged: 0,
	}
	for _, e := range findings {
		counts[core.TierOf(e)]++
	}
	return counts
}

// categoryCounts buckets one bundle's findings by published category,
// untagged for null entries.
func categoryCounts(findings []any) map[string]int {
	counts := map[string]int{}
	for _, e := range findings {
		counts[core.CategoryOf(e)]++
	}
	return counts
}

func dedupeSorted(vals []any) []any {
	if vals == nil {
		return []any{}
	}
	sorted := append([]any{}, vals...)
	slices.SortStableFunc(sorted, readcommon.Cmp)
	out := sorted[:0]
	for i, v := range sorted {
		if i == 0 || readcommon.Cmp(sorted[i-1], v) != 0 {
			out = append(out, v)
		}
	}
	if out == nil {
		return []any{}
	}
	return out
}

func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func warnMalformedWithheld(st *state, entries []any, bundle string) {
	for _, e := range entries {
		if readcommon.FieldOf(e, "withheld") != nil && readcommon.WithheldSource(e) == "" {
			st.warn("malformed withheld receipt ignored: " + bundle)
		}
	}
}
