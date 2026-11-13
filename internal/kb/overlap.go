package kb

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	statusFalsePositive = "false-positive"
	statusOpen          = "open"
	statusDeferred      = "deferred"
	statusWontFix       = "wont-fix"
)

// BundleFindings pairs one reviewed bundle slug with its decoded findings
// array so overlap joins share the scan without re-reading stores when
// findings are already loaded.
type BundleFindings struct {
	Bundle   string
	Findings []any
}

// OverlapRow holds one bundle's overlap audit counts and calibration readiness.
type OverlapRow struct {
	Coverage       any
	Precision      any
	Bundle         string
	Cited          []any
	Overlap        []any
	Recalled       []any
	CitedFindings  int
	Addressed      int
	FalsePositive  int
	Pending        int
	WontFix        int
	JoinsRows      int
	Measured       bool
	CalibrateReady bool
}

// RuleExposure holds one KB rule's recall, citation, and addressed counts
// across the repository's reviewed bundles.
type RuleExposure struct {
	RecalledIn int
	CitedIn    int
	Cited      int
	Addressed  int
}

// OverlapRollup holds the repository-wide overlap rollup, per-bundle rows, and
// per-rule exposure counts from one recall x findings x joins pass.
type OverlapRollup struct {
	Precision         any
	Coverage          any
	Rules             map[string]RuleExposure
	RecalledRules     []any
	Unmeasured        []any
	PerBundle         []OverlapRow
	CitedRules        []any
	OverlapRules      []any
	FalsePositive     int
	UnmeasuredBundles int
	CitedFindings     int
	Addressed         int
	Bundles           int
	Pending           int
	WontFix           int
	JoinsRows         int
	JoinsAddressed    int
	JoinsFalse        int
	CalibrateReady    int
	MeasuredBundles   int
}

// ScanBundleFindings reads every non-corpus, non-backtest reviewed bundle under
// .verdict/state, warning on hostile names or unreadable stores.
func ScanBundleFindings(root, skipPrefix string, warn func(string)) []BundleFindings {
	entries, err := os.ReadDir(readcommon.StateDir(root))
	if err != nil {
		return nil
	}
	var out []BundleFindings
	for _, e := range entries {
		if b, ok := scanBundleEntry(root, skipPrefix, e, warn); ok {
			out = append(out, b)
		}
	}
	return out
}

func scanBundleEntry(root, skipPrefix string, e os.DirEntry, warn func(string)) (BundleFindings, bool) {
	if !e.IsDir() {
		return BundleFindings{}, false
	}
	name := e.Name()
	dir, ok := listedBundleDir(root, name, skipPrefix, warn)
	if !ok {
		return BundleFindings{}, false
	}
	if readcommon.IsCorpusBundleDir(dir) || isFile(dir+"/"+readcommon.BacktestMarkerFile) {
		return BundleFindings{}, false
	}
	findings, ok := ReadBundleFindings(dir, skipPrefix, warn)
	if !ok {
		return BundleFindings{}, false
	}
	return BundleFindings{Bundle: name, Findings: findings}, true
}

// ReadBundleFindings reads and validates dir/findings.json under the cap.
func ReadBundleFindings(dir, skipPrefix string, warn func(string)) ([]any, bool) {
	bundle := dir + "/"
	raw, err := jq.CappedFile(bundle+"findings.json", jq.MaxFindingsBytes)
	if err != nil {
		emitWarn(warn, skipPrefix+bundle+" (findings.json unreadable or not a findings array)")
		return nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		emitWarn(warn, skipPrefix+bundle+" (findings.json unreadable or not a findings array)")
		return nil, false
	}
	findings, ok := doc.([]any)
	if !ok || !readcommon.ValidEntries(findings) {
		emitWarn(warn, skipPrefix+bundle+" (findings.json unreadable or not a findings array)")
		return nil, false
	}
	return findings, true
}

// ComputeOverlap joins bundle recall manifests, finding cites, and joins rows.
func ComputeOverlap(root, skipPrefix string, bundles []BundleFindings, warn func(string)) *OverlapRollup {
	joins := ReadJoins(root, warn)
	out := &OverlapRollup{Rules: map[string]RuleExposure{}}
	for i := range bundles {
		b := &bundles[i]
		row := overlapRowFor(root, skipPrefix, b, joins, warn)
		out.PerBundle = append(out.PerBundle, row)
		recordRuleExposure(out.Rules, &row, b.Findings)
	}
	rollOverlapTotals(out, joins)
	return out
}

// ReadJoins reads every object line from .verdict/kb-joins.jsonl, warning once
// and returning an empty slice when the file is corrupt.
func ReadJoins(root string, warn func(string)) []any {
	raw, err := jq.CappedFile(filepath.Join(root, filepath.FromSlash(Joins)), jq.MaxKBBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return []any{}
	}
	if err != nil {
		emitWarn(warn, "kb joins "+Joins+" unreadable — joins counts off for this rollup (fix or remove the file)")
		return []any{}
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var joins []any
	for dec.More() {
		var v any
		if err := dec.Decode(&v); err != nil {
			emitWarn(warn, "kb joins "+Joins+" unreadable — joins counts off for this rollup (fix or remove the file)")
			return []any{}
		}
		if _, ok := v.(map[string]any); ok {
			joins = append(joins, v)
		}
	}
	return joins
}

func emitWarn(warn func(string), msg string) {
	if warn != nil {
		warn(msg)
	}
}

func listedBundleDir(root, name, skipPrefix string, warn func(string)) (string, bool) {
	dir, ref := config.BundleDir(root, name)
	if ref != nil {
		emitWarn(warn, skipPrefix+name+" ("+ref.Message+")")
		return "", false
	}
	return filepath.ToSlash(dir), true
}

func overlapRowFor(root, skipPrefix string, b *BundleFindings, joins []any, warn func(string)) OverlapRow {
	recalled, measured := recallManifest(root, skipPrefix, b.Bundle, warn)
	cited := CitedIDs(b.Findings)
	inter := ArrayOverlap(recalled, cited)
	cf := citedFindingSet(b.Findings)
	out := OverlapRow{
		Bundle:        b.Bundle,
		Measured:      measured,
		Recalled:      recalled,
		Cited:         cited,
		Overlap:       inter,
		CitedFindings: len(cf),
		JoinsRows:     countJoins(joins, b.Bundle),
	}
	if measured {
		if len(recalled) > 0 {
			out.Precision = Ratio(len(inter), len(recalled))
		}
		if len(cited) > 0 {
			out.Coverage = Ratio(len(inter), len(cited))
		}
	}
	for _, e := range cf {
		tallyOutcome(&out, readcommon.StatusOf(e))
	}
	out.CalibrateReady = calibrateEligible(root, skipPrefix, b.Bundle, warn)
	return out
}

func recordRuleExposure(rules map[string]RuleExposure, row *OverlapRow, findings []any) {
	if row.Measured {
		recordRecalledRules(rules, row.Recalled)
	}
	recordCitedRules(rules, row.Cited)
	recordFindingOutcomes(rules, findings)
}

func recordRecalledRules(rules map[string]RuleExposure, recalled []any) {
	seen := map[string]bool{}
	for _, v := range recalled {
		id, ok := v.(string)
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		r := rules[id]
		r.RecalledIn++
		rules[id] = r
	}
}

func recordCitedRules(rules map[string]RuleExposure, cited []any) {
	for _, v := range cited {
		id, ok := v.(string)
		if !ok || id == "" {
			continue
		}
		r := rules[id]
		r.CitedIn++
		rules[id] = r
	}
}

func recordFindingOutcomes(rules map[string]RuleExposure, findings []any) {
	for _, f := range findings {
		addressed := 0
		if readcommon.StatusOf(f) == outcomeAddressed {
			addressed = 1
		}
		for _, id := range FindingRuleIDs(f) {
			r := rules[id]
			r.Cited++
			r.Addressed += addressed
			rules[id] = r
		}
	}
}

func tallyOutcome(out *OverlapRow, status string) {
	switch status {
	case outcomeAddressed:
		out.Addressed++
	case statusFalsePositive:
		out.FalsePositive++
	case statusOpen, statusDeferred:
		out.Pending++
	case statusWontFix:
		out.WontFix++
	}
}

func recallManifest(root, skipPrefix, bundle string, warn func(string)) ([]any, bool) {
	dir, ok := listedBundleDir(root, bundle, skipPrefix, warn)
	if !ok {
		return []any{}, false
	}
	path := dir + "/kb-recall.json"
	if !isFile(path) {
		return []any{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return []any{}, false
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return warnManifest(dir, skipPrefix, warn)
	}
	list, ok := doc.([]any)
	if !ok {
		return warnManifest(dir, skipPrefix, warn)
	}
	return manifestIDs(list), true
}

func warnManifest(dir, skipPrefix string, warn func(string)) ([]any, bool) {
	emitWarn(warn, skipPrefix+dir+"/kb-recall.json (unreadable or not a manifest array)")
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

// CitedIDs returns the sorted unique rule IDs cited across findings.
func CitedIDs(findings []any) []any {
	var ids []any
	for _, e := range findings {
		for _, s := range FindingRuleIDs(e) {
			ids = append(ids, s)
		}
	}
	return DedupeSorted(ids)
}

// FindingRuleIDs returns unique rule IDs listed in one finding's kb array.
func FindingRuleIDs(e any) []string {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	cited, _ := m.Get("kb")
	list, ok := cited.([]any)
	if !ok {
		return nil
	}
	seen := make(map[string]bool, len(list))
	var out []string
	for _, id := range list {
		s, ok := id.(string)
		if !ok || s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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

// ArrayOverlap returns elements present in both a and b using Cmp equality.
func ArrayOverlap(recalled, cited []any) []any {
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

// Ratio returns float64(n)/float64(d), or nil when the denominator is 0.
func Ratio(n, d int) any {
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

func calibrateEligible(root, skipPrefix, bundle string, warn func(string)) bool {
	if len(bundle) >= len("local-") && bundle[:len("local-")] == "local-" {
		return false
	}
	dir, ok := listedBundleDir(root, bundle, skipPrefix, warn)
	if !ok {
		return false
	}
	if !isFile(dir + "/post.json") {
		return false
	}
	if readcommon.JSONRaw(dir+"/post.json", "head") == "" {
		return false
	}
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

func rollOverlapTotals(out *OverlapRollup, joins []any) {
	var recAll, citAll []any
	for i := range out.PerBundle {
		row := &out.PerBundle[i]
		out.Bundles++
		if row.Measured {
			out.MeasuredBundles++
			recAll = append(recAll, row.Recalled...)
			citAll = append(citAll, row.Cited...)
		} else {
			out.UnmeasuredBundles++
			out.Unmeasured = append(out.Unmeasured, row.Bundle)
		}
		accumulateRowCounts(out, row)
	}
	out.RecalledRules = DedupeSorted(recAll)
	out.CitedRules = DedupeSorted(citAll)
	inter := ArrayOverlap(out.RecalledRules, out.CitedRules)
	out.OverlapRules = inter
	if len(out.RecalledRules) > 0 {
		out.Precision = Ratio(len(inter), len(out.RecalledRules))
	}
	if len(out.CitedRules) > 0 {
		out.Coverage = Ratio(len(inter), len(out.CitedRules))
	}
	tallyJoins(out, joins)
	if out.Unmeasured == nil {
		out.Unmeasured = []any{}
	}
}

func accumulateRowCounts(out *OverlapRollup, row *OverlapRow) {
	out.CitedFindings += row.CitedFindings
	out.Addressed += row.Addressed
	out.FalsePositive += row.FalsePositive
	out.Pending += row.Pending
	out.WontFix += row.WontFix
	if row.CalibrateReady {
		out.CalibrateReady++
	}
}

func tallyJoins(out *OverlapRollup, joins []any) {
	out.JoinsRows = len(joins)
	for _, j := range joins {
		tallyJoin(out, j)
	}
}

func tallyJoin(out *OverlapRollup, j any) {
	m, ok := j.(map[string]any)
	if !ok {
		return
	}
	outcome, ok := m["outcome"].(string)
	if !ok {
		return
	}
	switch outcome {
	case outcomeAddressed:
		out.JoinsAddressed++
	case statusFalsePositive:
		out.JoinsFalse++
	}
}

// NumOrNA renders a canonical float or jq scalar string, or "n/a" when nil.
func NumOrNA(v any) string {
	if v == nil {
		return "n/a"
	}
	if f, ok := v.(float64); ok {
		return readcommon.CanonFloat(f)
	}
	return jq.Str(v)
}

// DedupeSorted returns a sorted copy of vals with Cmp duplicates removed.
func DedupeSorted(vals []any) []any {
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
