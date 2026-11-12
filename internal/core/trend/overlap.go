package trendcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type overlapRow struct {
	coverage       any
	precision      any
	bundle         string
	cited          []any
	overlap        []any
	recalled       []any
	citedFindings  int
	addressed      int
	falsePositive  int
	pending        int
	wontFix        int
	joinsRows      int
	measured       bool
	calibrateReady bool
}

type overlapRollup struct {
	precision         any
	coverage          any
	recalledRules     []any
	unmeasured        []any
	perBundle         []overlapRow
	citedRules        []any
	overlapRules      []any
	falsePositive     int
	unmeasuredBundles int
	citedFindings     int
	addressed         int
	bundles           int
	pending           int
	wontFix           int
	joinsRows         int
	joinsAddressed    int
	joinsFalse        int
	calibrateReady    int
	measuredBundles   int
}

func overlapAudit(st *state, rows []row) *overlapRollup {
	joins := readJoins(st)
	out := &overlapRollup{}
	for i := range rows {
		out.perBundle = append(out.perBundle, overlapRowFor(st, &rows[i], joins))
	}
	rollOverlapTotals(out, joins)
	return out
}

func readJoins(st *state) []any {
	raw, err := os.ReadFile(filepath.Join(st.req.RepoRootPath(), filepath.FromSlash(kb.Joins)))
	if err != nil {
		return []any{}
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var joins []any
	for dec.More() {
		var v any
		if err := dec.Decode(&v); err != nil {
			st.warn("kb joins " + kb.Joins + " unreadable — joins counts off for this rollup (fix or remove the file)")
			return []any{}
		}
		if _, ok := v.(map[string]any); ok {
			joins = append(joins, v)
		}
	}
	return joins
}

func overlapRowFor(st *state, row *row, joins []any) overlapRow {
	recalled, measured := recallManifest(st, row.bundle)
	cited := citedIDs(row.findings)
	inter := arrayOverlap(recalled, cited)
	cf := citedFindingSet(row.findings)
	out := overlapRow{
		bundle:        row.bundle,
		measured:      measured,
		recalled:      recalled,
		cited:         cited,
		overlap:       inter,
		citedFindings: len(cf),
		joinsRows:     countJoins(joins, row.bundle),
	}
	if measured {
		if len(recalled) > 0 {
			out.precision = ratio(len(inter), len(recalled))
		}
		if len(cited) > 0 {
			out.coverage = ratio(len(inter), len(cited))
		}
	}
	for _, e := range cf {
		tallyOutcome(&out, readcommon.StatusOf(e))
	}
	out.calibrateReady = calibrateEligible(st, row.bundle)
	return out
}

func tallyOutcome(out *overlapRow, status string) {
	switch status {
	case statusAddressed:
		out.addressed++
	case statusFalsePositive:
		out.falsePositive++
	case statusOpen, statusDeferred:
		out.pending++
	case statusWontFix:
		out.wontFix++
	}
}

func recallManifest(st *state, bundle string) ([]any, bool) {
	dir, ok := st.listedBundleDir(bundle)
	if !ok {
		return []any{}, false
	}
	path := dir + "/kb-recall.json"
	if !isRegular(path) {
		return []any{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return []any{}, false
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return warnManifest(st, dir)
	}
	list, ok := doc.([]any)
	if !ok {
		return warnManifest(st, dir)
	}
	return manifestIDs(list), true
}

func warnManifest(st *state, dir string) ([]any, bool) {
	st.warn(skipWarn + dir + "/kb-recall.json (unreadable or not a manifest array)")
	return []any{}, false
}

func manifestIDs(list []any) []any {
	var ids []any
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	if ids == nil {
		ids = []any{}
	}
	return ids
}

func citedIDs(findings []any) []any {
	var ids []any
	for _, e := range findings {
		ids = appendCitedIDs(ids, e)
	}
	return dedupeSorted(ids)
}

func appendCitedIDs(ids []any, e any) []any {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return ids
	}
	cited, _ := m.Get("kb")
	list, ok := cited.([]any)
	if !ok {
		return ids
	}
	for _, id := range list {
		if s, ok := id.(string); ok {
			ids = append(ids, s)
		}
	}
	return ids
}

func citedFindingSet(findings []any) []any {
	var out []any
	for _, e := range findings {
		if kbLength(kbOf(e)) > 0 {
			out = append(out, e)
		}
	}
	return out
}

func kbOf(entry any) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	cited, _ := m.Get("kb")
	if cited == nil {
		return []any{}
	}
	return cited
}

func kbLength(cited any) float64 {
	switch t := cited.(type) {
	case nil:
		return 0
	case bool:
		return 0
	case string:
		return float64(len([]rune(t)))
	case []any:
		return float64(len(t))
	case map[string]any:
		return float64(len(t))
	case *jq.Obj:
		return float64(len(t.Keys))
	default:
		if f, ok := readcommon.NumVal(t); ok {
			if f < 0 {
				return -f
			}
			return f
		}
		return 0
	}
}

func arrayOverlap(recalled, cited []any) []any {
	return arrayMinus(recalled, arrayMinus(recalled, cited))
}

func arrayMinus(a, b []any) []any {
	var out []any
	for _, x := range a {
		if !containsValue(b, x) {
			out = append(out, x)
		}
	}
	if out == nil {
		return []any{}
	}
	return out
}

func containsValue(list []any, v any) bool {
	for _, e := range list {
		if readcommon.Cmp(e, v) == 0 {
			return true
		}
	}
	return false
}

func ratio(n, d int) any {
	if d == 0 {
		return nil
	}
	return float64(n) / float64(d)
}

func countJoins(joins []any, bundle string) int {
	n := 0
	for _, j := range joins {
		m, ok := j.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := m["bundle"].(string); ok && name == bundle {
			n++
		}
	}
	return n
}

func calibrateEligible(st *state, bundle string) bool {
	if len(bundle) >= len("local-") && bundle[:len("local-")] == "local-" {
		return false
	}
	dir, ok := st.listedBundleDir(bundle)
	if !ok {
		return false
	}
	if !isRegular(dir + "/post.json") {
		return false
	}
	if readcommon.JSONRaw(dir+"/post.json", "head") == "" {
		return false
	}
	return joinIDPresent(dir)
}

// joinIDPresent owns the review.id presence check.
func joinIDPresent(dir string) bool {
	return reviewIDPresent(dir)
}

func reviewIDPresent(dir string) bool {
	m := readcommon.PostRecord(dir)
	if m == nil {
		return false
	}
	rev, ok := m["review"].(map[string]any)
	if !ok {
		return false
	}
	id, ok := rev["id"]
	if !ok || id == nil || id == false {
		return false
	}
	return jq.R(id) != ""
}

func rollOverlapTotals(out *overlapRollup, joins []any) {
	var recAll, citAll []any
	for i := range out.perBundle {
		row := &out.perBundle[i]
		out.bundles++
		if row.measured {
			out.measuredBundles++
			recAll = append(recAll, row.recalled...)
			citAll = append(citAll, row.cited...)
		} else {
			out.unmeasuredBundles++
			out.unmeasured = append(out.unmeasured, row.bundle)
		}
		accumulateRowCounts(out, row)
	}
	out.recalledRules = dedupeSorted(recAll)
	out.citedRules = dedupeSorted(citAll)
	inter := arrayOverlap(out.recalledRules, out.citedRules)
	out.overlapRules = inter
	if len(out.recalledRules) > 0 {
		out.precision = ratio(len(inter), len(out.recalledRules))
	}
	if len(out.citedRules) > 0 {
		out.coverage = ratio(len(inter), len(out.citedRules))
	}
	tallyJoins(out, joins)
	if out.unmeasured == nil {
		out.unmeasured = []any{}
	}
}

func accumulateRowCounts(out *overlapRollup, row *overlapRow) {
	out.citedFindings += row.citedFindings
	out.addressed += row.addressed
	out.falsePositive += row.falsePositive
	out.pending += row.pending
	out.wontFix += row.wontFix
	if row.calibrateReady {
		out.calibrateReady++
	}
}

func tallyJoins(out *overlapRollup, joins []any) {
	out.joinsRows = len(joins)
	for _, j := range joins {
		tallyJoin(out, j)
	}
}

func tallyJoin(out *overlapRollup, j any) {
	m, ok := j.(map[string]any)
	if !ok {
		return
	}
	outcome, ok := m["outcome"].(string)
	if !ok {
		return
	}
	switch outcome {
	case statusAddressed:
		out.joinsAddressed++
	case statusFalsePositive:
		out.joinsFalse++
	}
}

func (st *state) emitOverlapHuman(out *overlapRollup) {
	st.human(fmt.Sprintf("overlap: %d measured, %d unmeasured — precision=%s coverage=%s (recalled=%d cited=%d overlap=%d)",
		out.measuredBundles, out.unmeasuredBundles, numOrNA(out.precision), numOrNA(out.coverage),
		len(out.recalledRules), len(out.citedRules), len(out.overlapRules)))
	st.human(fmt.Sprintf("cited outcomes: %d cited findings — addressed=%d false-positive=%d pending=%d wont-fix=%d; "+
		"joins rows=%d (addressed=%d false-positive=%d)",
		out.citedFindings, out.addressed, out.falsePositive, out.pending, out.wontFix,
		out.joinsRows, out.joinsAddressed, out.joinsFalse))
	for i := range out.perBundle {
		row := &out.perBundle[i]
		st.human(fmt.Sprintf("  %s: measured=%s precision=%s coverage=%s (recalled=%d cited=%d overlap=%d) "+
			"outcomes addressed=%d false-positive=%d pending=%d",
			row.bundle, strconv.FormatBool(row.measured), numOrNA(row.precision), numOrNA(row.coverage),
			len(row.recalled), len(row.cited), len(row.overlap), row.addressed, row.falsePositive, row.pending))
	}
}

func numOrNA(v any) string {
	if v == nil {
		return "n/a"
	}
	if f, ok := v.(float64); ok {
		return readcommon.CanonFloat(f)
	}
	return jq.Str(v)
}

// overlapDoc renders the overlap envelope fragment in shell key order.
func overlapDoc(out *overlapRollup) *jq.Obj {
	perBundle := make([]any, 0, len(out.perBundle))
	for i := range out.perBundle {
		perBundle = append(perBundle, overlapRowDoc(&out.perBundle[i]))
	}
	return jq.NewObj(
		"measured_bundles", out.measuredBundles,
		"unmeasured_bundles", out.unmeasuredBundles,
		"unmeasured", out.unmeasured,
		"bundles", out.bundles,
		"recalled_rules", anyList(out.recalledRules),
		"cited_rules", anyList(out.citedRules),
		"overlap_rules", anyList(out.overlapRules),
		"precision", out.precision,
		"coverage", out.coverage,
		"cited_findings", out.citedFindings,
		"addressed", out.addressed,
		"false_positive", out.falsePositive,
		"pending", out.pending,
		"wont_fix", out.wontFix,
		"joins", jq.NewObj(
			"rows", out.joinsRows,
			"addressed", out.joinsAddressed,
			"false_positive", out.joinsFalse,
		),
		"calibrate_eligible_bundles", out.calibrateReady,
		"per_bundle", perBundle,
	)
}

func overlapRowDoc(row *overlapRow) *jq.Obj {
	return jq.NewObj(
		"bundle", row.bundle,
		"measured", row.measured,
		"recalled", anyList(row.recalled),
		"cited", anyList(row.cited),
		"overlap", anyList(row.overlap),
		"precision", row.precision,
		"coverage", row.coverage,
		"cited_findings", row.citedFindings,
		"addressed", row.addressed,
		"false_positive", row.falsePositive,
		"pending", row.pending,
		"wont_fix", row.wontFix,
		"joins_rows", row.joinsRows,
		"calibrate_eligible", row.calibrateReady,
	)
}
