// Package trendcore is the trend verb's domain service: the portfolio
// rollup over every reviewed bundle's findings store, plus the opt-in KB
// overlap audit. Pure read, writes nothing, no network — rescan semantics,
// so hand-edited stores stay truthful. internal/trend owns argv parsing,
// progress routing, envelope rendering, and exit mapping.
package trendcore

import (
	"context"
	"maps"
	"os"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved trend invocation: the shared read runtime
// plus the --overlap audit flag.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Overlap  bool
	Warnings []string
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
// the portfolio rollup keys, the per-bundle rows, and the optional
// overlap join. Field order is the shell's envelope construction order.
type Data struct { //nolint:govet // field order is the published property order
	GeneratedAt string
	Rollup      rollup
	Rows        []row
	Overlap     *overlapRollup
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
	if d.Overlap != nil {
		data.Set("overlap", overlapDoc(d.Overlap))
	}
	return data
}

// state carries one trend run: the request, the accumulated warnings,
// and the buffered human lines (human mode prints after the run, so the
// warnings streamed during the scan keep their position).
type state struct {
	req      *Request
	warnings []string
	lines    []string
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
	var overlap *overlapRollup
	if req.Overlap {
		overlap = overlapAudit(st, rows)
	}
	st.emitHuman(&rollup, overlap)
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
		Data:     &Data{GeneratedAt: now, Rollup: rollup, Rows: rows, Overlap: overlap},
	}, nil
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// human buffers one human output line for the human-mode summary.
func (st *state) human(line string) {
	st.lines = append(st.lines, line)
}

// row is one per-bundle rollup point.
type row struct {
	prov     *jq.Obj
	byStatus map[string]int
	byTier   map[string]int
	bundle   string
	at       string
	models   []any
	efforts  []any
	findings []any
	hasAt    bool
}

// collectRows ports the per-bundle scan: every .review/*/ carrying a
// findings array is one point in time; unreadable stores warn and skip,
// never fail.
func collectRows(st *state) []row {
	entries, err := os.ReadDir(".review")
	if err != nil {
		return nil
	}
	var rows []row
	for _, e := range entries {
		if bundleRow, ok := readBundleRow(st, e); ok {
			rows = append(rows, bundleRow)
		}
	}
	return rows
}

// readBundleRow ports one bundle's read: non-dirs and unreadable or
// misshapen stores warn and skip.
func readBundleRow(st *state, e os.DirEntry) (row, bool) {
	if !e.IsDir() {
		return row{}, false
	}
	name := e.Name()
	bundle := ".review/" + name + "/"
	raw, err := os.ReadFile(bundle + "findings.json")
	if err != nil {
		st.warn("trend skips " + bundle + " (findings.json unreadable or not a findings array)")
		return row{}, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		st.warn("trend skips " + bundle + " (findings.json unreadable or not a findings array)")
		return row{}, false
	}
	findings, ok := doc.([]any)
	if !ok || !validTrendEntries(findings) {
		st.warn("trend skips " + bundle + " (findings.json unreadable or not a findings array)")
		return row{}, false
	}
	return buildRow(bundle, name, findings), true
}

// validTrendEntries shares the entry-shape guard: trend warns and skips
// the bundle instead of refusing.
func validTrendEntries(findings []any) bool {
	for _, e := range findings {
		if !validTrendEntry(e) {
			return false
		}
	}
	return true
}

// validTrendEntry gates one entry like the read-verb entry guard.
func validTrendEntry(e any) bool {
	if e == nil {
		return true
	}
	m, ok := e.(*jq.Obj)
	if !ok {
		return false
	}
	return validTrendStrings(m) && validTrendMaps(m)
}

// validTrendStrings gates the string-indexed entry fields.
func validTrendStrings(m *jq.Obj) bool {
	for _, key := range []string{"sig", "body", "heading", "kind", "suggestion"} {
		v, has := m.Get(key)
		if !has || v == nil {
			continue
		}
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

// validTrendMaps gates the indexed-map entry fields.
func validTrendMaps(m *jq.Obj) bool {
	for _, key := range []string{"severity", "rigor"} {
		v, has := m.Get(key)
		if !has || v == nil {
			continue
		}
		switch v.(type) {
		case map[string]any, *jq.Obj, []any:
			return false
		default:
		}
	}
	return true
}

// buildRow builds one bundle row.
func buildRow(bundle, name string, findings []any) row {
	at, hasAt := bundleTime(bundle)
	return row{
		bundle:   name,
		at:       at,
		hasAt:    hasAt,
		models:   bundleModels(bundle),
		efforts:  bundleEfforts(bundle),
		prov:     bundleProvenance(bundle),
		findings: findings,
		byStatus: statusCounts(findings),
		byTier:   tierCounts(findings),
	}
}

// bundleTime ports the bundle timestamp: post.json posted_at, else
// record.json recorded_at, else undated.
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

// bundleModels ports the per-bundle model list (unique, invalid reads []).
func bundleModels(bundle string) []any {
	costs := costRecords(bundle + "cost.json")
	if costs == nil {
		return []any{}
	}
	var names []any
	for _, rec := range costs {
		models, ok := recordModels(rec)
		if !ok {
			return []any{}
		}
		if names, ok = appendModelNames(names, models); !ok {
			return []any{}
		}
	}
	return dedupeSorted(names)
}

// appendModelNames folds one record's models into the name list: nameless
// models skip, non-object models invalidate the bundle read.
func appendModelNames(names, models []any) ([]any, bool) {
	for _, mm := range models {
		var ok bool
		if names, ok = appendModelName(names, mm); !ok {
			return nil, false
		}
	}
	return names, true
}

// appendModelName folds one model into the name list.
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

// bundleEfforts ports the per-bundle effort list (unique, invalid reads []).
func bundleEfforts(bundle string) []any {
	costs := costRecords(bundle + "cost.json")
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

// appendTier appends one normalized tier once.
func appendTier(efforts []any, seen map[string]bool, tier string) []any {
	if !seen[tier] {
		seen[tier] = true
		efforts = append(efforts, tier)
	}
	return efforts
}

// costRecords reads one cost file as a record list, or nil when absent or
// invalid (the shell's `|| echo "[]"`).
func costRecords(path string) []any {
	if !isRegular(path) {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	if arr, ok := doc.([]any); ok {
		return arr
	}
	return []any{doc}
}

// recordModels reads one cost record's models list.
func recordModels(rec any) ([]any, bool) {
	m, ok := rec.(map[string]any)
	if !ok {
		if rec == nil {
			return nil, true
		}
		return nil, false
	}
	raw, ok := m["models"]
	if !ok || raw == nil {
		return nil, true
	}
	return coerceModels(raw)
}

// coerceModels ports the models shape gate: arrays pass through, maps
// contribute values in key order, anything else fails the bundle read.
func coerceModels(raw any) ([]any, bool) {
	switch t := raw.(type) {
	case []any:
		return t, true
	case map[string]any:
		vals := make([]any, 0, len(t))
		for _, k := range sortedKeys(t) {
			vals = append(vals, t[k])
		}
		return vals, true
	default:
		return nil, false
	}
}

// bundleProvenance ports the per-bundle provenance triple: the post
// receipt first, then the calibration record; old receipts read unmarked.
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

// provenanceTriple reads one receipt's triple, or nil when invalid.
func provenanceTriple(path string) *jq.Obj {
	raw, err := os.ReadFile(path)
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

// unmarkedProvenance renders the all-null triple.
func unmarkedProvenance() *jq.Obj {
	return jq.NewObj("tool_version", nil, "prompts_hash", nil, "model", nil)
}

// statusCounts counts findings by status; unknown statuses count nowhere,
// mirroring the fixed-key by_status object.
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

// tierCounts counts findings by tier (untagged for anything else).
func tierCounts(findings []any) map[string]int {
	counts := map[string]int{
		tierCritical: 0, tierImportant: 0, tierInformational: 0, tierUntagged: 0,
	}
	for _, e := range findings {
		counts[tierOf(e)]++
	}
	return counts
}

// tierOf ports the trend tier: the raw severity or untagged.
func tierOf(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return tierUntagged
	}
	sev, _ := m.Get("severity")
	if s, ok := sev.(string); ok {
		switch s {
		case tierCritical, tierImportant, tierInformational:
			return s
		}
	}
	return tierUntagged
}

// dedupeSorted dedupes values in jq order (unique).
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

// isRegular reports whether path is a regular file.
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// sortedKeys returns the sorted keys of m (jq object iteration order).
func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
