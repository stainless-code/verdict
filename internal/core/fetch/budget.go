package fetchcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/statedir"
)

type budgetState struct {
	estimateJSON  any
	budgetJSON    any
	budgetUSD     string
	budgetSrc     string
	estTier       string
	estUSD        string
	estBasis      string
	estHarness    []string
	estRuns       int
	measuredRuns  int
	tierRuns      int
	hasBudget     bool
	receiptNeeded bool
}

// resolveBudget reads the budget: REVIEW_BUDGET_USD wins, else the
// first usable line of .review/budget (a leading $ is stripped). Unset
// disables the whole plane.
func resolveBudget(env map[string]string, r *session) (budgetState, error) {
	var st budgetState
	raw, src := "", ""
	if v, ok := env["REVIEW_BUDGET_USD"]; ok && v != "" {
		raw, src = strings.TrimSpace(v), "REVIEW_BUDGET_USD"
	} else if v, ok := readKnobFile(r, ".review/budget"); ok {
		raw, src = strings.TrimSpace(strings.TrimPrefix(v, "$")), statedir.ControlsLabel("budget")
	}
	if raw == "" {
		return st, nil
	}
	if !isBudgetNumber(raw) {
		return st, &core.RefusalError{Code: "bad-budget", Message: fmt.Sprintf(
			"budget '%s' from %s is not a plain USD number (want e.g. REVIEW_BUDGET_USD=0.50 "+
				"or a .review/budget file holding 0.50)", raw, src)}
	}
	st.budgetUSD, st.budgetSrc, st.hasBudget = raw, src, true
	return st, nil
}

func isBudgetNumber(s string) bool {
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if hasDot && strings.Contains(fracPart, ".") {
		return false
	}
	if !readcommon.IsDigits(intPart) {
		return false
	}
	if hasDot && !readcommon.IsDigits(fracPart) {
		return false
	}
	return true
}

// budgetGate gates a run on the budget: with one set, print the estimate line,
// arm the envelope summaries, and refuse over-budget runs without consent.
func budgetGate(r *session, st *budgetState, consent bool) error {
	if !st.hasBudget {
		return nil
	}
	if err := costEstimate(r, st); err != nil {
		return err
	}
	st.budgetJSON = jq.NewObj("usd", jsonNumber(st.budgetUSD), keySource, st.budgetSrc, "consented", consent)
	tierVal := any(nil)
	if st.estTier != "" {
		tierVal = st.estTier
	}
	st.estimateJSON = estimateObject(st, tierVal)
	if st.estUSD == "" {
		return nil
	}
	if overBudget(st.estUSD, st.budgetUSD) {
		if consent {
			st.receiptNeeded = true
			return nil
		}
		return &core.RefusalError{Code: "budget-exceeded", Message: fmt.Sprintf(
			"projected %s run ~$%s%s exceeds budget $%s (%s) from %d comparable run(s) — consent required before this run starts",
			st.estTier, st.estUSD, st.basisNote(), st.budgetUSD, st.budgetSrc, st.estRuns)}
	}
	return nil
}

func estimateObject(st *budgetState, tierVal any) *jq.Obj {
	o := jq.NewObj("tier", tierVal, "projected_usd", jsonFloat(st.estUSD), "comparable_runs", st.estRuns)
	if st.measuredRuns == 0 {
		return o
	}
	o.Set("basis", st.estBasis)
	if len(st.estHarness) > 0 {
		o.Set("harness", st.estHarness)
	}
	o.Set("measured_runs", st.measuredRuns)
	return o
}

// basisNote renders the measured-basis clause appended to human estimates
// and refusals; empty when the projection is purely self-reported.
func (st *budgetState) basisNote() string {
	switch {
	case st.measuredRuns == 0:
		return ""
	case st.estBasis == "measured":
		return fmt.Sprintf(" [measured from %d harness session(s)]", st.measuredRuns)
	default:
		return fmt.Sprintf(" [partly measured: %d harness session(s)]", st.measuredRuns)
	}
}

// costEstimate averages est_usd over the most-run effort
// tier across every .review/*/cost.json record (ties break toward the
// deeper tier). Unknown history reads unknown, never blocking. Handed usage
// records price the runs they bracket; a broken handoff refuses typed.
func costEstimate(r *session, st *budgetState) error {
	st.estUSD, st.estRuns, st.estTier, st.tierRuns = "", 0, "", 0
	usage, err := measuredUsage(r)
	if err != nil {
		return err
	}
	recs := estimateRecs(r, usage)
	if len(recs) == 0 {
		fmt.Fprintln(r.Human, "» estimate: no comparable past runs — cost unknown (no usable .review/*/cost.json records yet)")
		return nil
	}
	tier, tierRuns, stat, ok := pickEstimateTier(recs)
	st.estRuns = len(recs)
	if !ok {
		fmt.Fprintln(r.Human, "» estimate: no comparable past runs — cost unknown (no usable .review/*/cost.json records yet)")
		st.estRuns = 0
		return nil
	}
	st.estTier, st.tierRuns = tier, tierRuns
	st.recordBasis(stat)
	if stat.mean == "" {
		fmt.Fprintf(r.Human, "» estimate: tier %s from %d comparable run(s) — projected cost unknown "+
			"(none reported est_usd); budget $%s (%s) unenforced on unknown\n",
			tier, tierRuns, st.budgetUSD, st.budgetSrc)
		return nil
	}
	st.estUSD = stat.mean
	line := fmt.Sprintf("» estimate: tier %s ~$%s/run from %d comparable run(s) — ", tier, stat.mean, st.estRuns)
	if overBudget(stat.mean, st.budgetUSD) {
		line += fmt.Sprintf("OVER budget $%s (%s)", st.budgetUSD, st.budgetSrc)
	} else {
		line += fmt.Sprintf("within budget $%s (%s)", st.budgetUSD, st.budgetSrc)
	}
	fmt.Fprintln(r.Human, line+st.basisNote())
	return nil
}

// recordBasis carries the measured split of the picked tier into the gate's
// estimate provenance. A picked tier with no measured sample leaves the
// legacy output untouched.
func (st *budgetState) recordBasis(stat tierStat) {
	if stat.measured == 0 {
		return
	}
	st.measuredRuns = stat.measured
	st.estHarness = stat.harness
	if stat.priced > 0 && stat.measured == stat.priced {
		st.estBasis = "measured"
		return
	}
	st.estBasis = "mixed"
}

type costRec struct {
	measured    *float64
	tier        string
	usageSource string
	usd         string
	has         bool
}

func readCostRecs(path string) ([]costRec, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	var items []any
	if arr, ok := doc.([]any); ok {
		items = arr
	} else {
		items = []any{doc}
	}
	var out []costRec
	for _, it := range items {
		out = append(out, costRecord(it))
	}
	return out, true
}

func costRecord(it any) costRec {
	m, _ := jq.AsMap(it)
	tier := tierStandard
	var effort any
	if m != nil {
		effort = m["effort"]
	}
	if s, ok := effort.(string); ok {
		switch strings.ToLower(s) {
		case "cheap", tierStandard, "deep":
			tier = strings.ToLower(s)
		}
	}
	rec := costRec{tier: tier}
	if m != nil {
		if num, ok := estNumber(m["est_usd"]); ok {
			rec.usd, rec.has = num, true
		}
	}
	return rec
}

// estNumber renders est_usd as a jq number literal when it is a number,
// and null otherwise.
func estNumber(v any) (string, bool) {
	switch t := v.(type) {
	case jq.Number:
		return t.String(), true
	case json.Number:
		return t.String(), true
	case float64:
		return jq.Compact(t), true
	case int:
		return strconv.Itoa(t), true
	default:
		return "", false
	}
}

// tierStat accumulates one tier's run count and priced mean plus the
// measured-sample split behind it.
type tierStat struct {
	mean     string
	harness  []string
	meanVal  float64
	runs     int
	priced   int
	measured int
}

// no-pick, like max_by over an empty array.
func pickEstimateTier(recs []costRec) (tier string, tierRuns int, stat tierStat, ok bool) {
	if len(recs) == 0 {
		return "", 0, tierStat{}, false
	}
	best := [2]float64{-1, -1}
	for _, t := range []string{"deep", tierStandard, "cheap"} {
		s := tierStats(recs, t)
		if float64(s.runs) > best[0] || (float64(s.runs) == best[0] && s.meanVal > best[1]) {
			best = [2]float64{float64(s.runs), s.meanVal}
			tier, tierRuns, stat, ok = t, s.runs, s, true
		}
	}
	return tier, tierRuns, stat, ok
}

// tierStats handles one tier's accumulation: the run count plus the priced
// mean (-1 with no priced runs, so unpriced tiers still compete on runs).
// A handed usage record prices that run and names its source.
func tierStats(recs []costRec, t string) tierStat {
	var st tierStat
	var sum float64
	for _, r := range recs {
		if r.tier == t {
			sum, st = addTierSample(r, sum, st)
		}
	}
	st.meanVal = -1.0
	if st.priced > 0 {
		st.meanVal = sum / float64(st.priced)
		st.mean = jq.Compact(readcommon.Round4(st.meanVal))
	}
	slices.Sort(st.harness)
	st.harness = slices.Compact(st.harness)
	return st
}

// estimateRecs owns the cost-history sweep: every readable record
// accumulates, while unreadable files warn once naming every one.
func estimateRecs(r *session, usage []readcommon.ContractUsage) []costRec {
	var recs []costRec
	var bad []string
	files, gerr := filepath.Glob(filepath.Join(statedir.Resolve().Root, "*", readcommon.CostFile))
	if gerr != nil {
		files = nil
	}
	slices.Sort(files)
	for _, f := range files {
		fileRecs, ok := readCostRecs(f)
		if !ok {
			// The warning is user-visible: glob returns OS separators,
			// so Windows would print `.review\2\cost.json` where every
			// bundle path uses slashes.
			bad = append(bad, filepath.ToSlash(f))
			continue
		}
		joinMeasured(fileRecs, usage, f)
		recs = append(recs, fileRecs...)
	}
	if len(bad) > 0 {
		r.Warn(fmt.Sprintf("cost history unreadable — skipped from estimate: %s ", strings.Join(bad, " ")))
	}
	return recs
}

// measuredUsage reads the usage records the operator handed over via
// VERDICT_USAGE. A broken handoff refuses typed at the gate; no handoff
// simply leaves the self-reported record in charge.
func measuredUsage(r *session) ([]readcommon.ContractUsage, error) {
	//nolint:wrapcheck // typed refusal passes through untouched so the edge renders its code/message
	return readcommon.ReadContractUsage(r.env)
}

// joinMeasured attaches the handed usage record whose window brackets the
// cost file's mtime to the run that wrote it (the last record of a
// re-review array). No matching record leaves every record untouched.
func joinMeasured(fileRecs []costRec, usage []readcommon.ContractUsage, path string) {
	info, err := os.Stat(path)
	if err != nil || len(fileRecs) == 0 {
		return
	}
	match := readcommon.MatchContractUsage(usage, info.ModTime())
	if match == nil || match.USD == nil {
		return
	}
	last := len(fileRecs) - 1
	fileRecs[last].measured = match.USD
	fileRecs[last].usageSource = match.Source
}

// addTierSample folds one same-tier record into the accumulator.
func addTierSample(r costRec, sum float64, st tierStat) (float64, tierStat) {
	st.runs++
	sum, st.priced = tierPriced(r, sum, st.priced)
	if r.measured == nil {
		return sum, st
	}
	st.measured++
	if r.usageSource != "" {
		st.harness = append(st.harness, r.usageSource)
	}
	return sum, st
}

// tierPriced handles one priced record: the handed usage cost wins,
// else the self-reported usd parses into the sum.
func tierPriced(r costRec, sum float64, priced int) (float64, int) {
	if r.measured != nil {
		return sum + *r.measured, priced + 1
	}
	if r.has {
		if f, err := strconv.ParseFloat(r.usd, numBitSize); err == nil {
			return sum + f, priced + 1
		}
	}
	return sum, priced
}

func overBudget(projected, budget string) bool {
	p, err1 := strconv.ParseFloat(projected, numBitSize)
	b, err2 := strconv.ParseFloat(budget, numBitSize)
	if err1 != nil || err2 != nil {
		return false
	}
	return p > b
}

func jsonNumber(lit string) any {
	return jq.Number(lit)
}

// jsonFloat renders a computed mean/est literal ("" reads null). Computed
// values print in canonical jq form.
func jsonFloat(lit string) any {
	if lit == "" {
		return nil
	}
	return jq.Number(lit)
}

// writeBudgetReceipt records a consented over-budget
// run appends one receipt to the bundle's budget.json ledger.
func writeBudgetReceipt(r *session, st *budgetState, dir, now string) {
	if !st.receiptNeeded {
		return
	}
	rc := jq.NewObj(
		"at", now, "budget_usd", jsonNumber(st.budgetUSD), "budget_src", st.budgetSrc,
		"projected_tier", st.estTier, "projected_usd", jsonFloat(st.estUSD),
		"comparable_runs", st.estRuns,
	)
	if st.measuredRuns > 0 {
		rc.Set("basis", st.estBasis)
		rc.Set("measured_runs", st.measuredRuns)
	}
	rc.Set("consent", "--consent-over-budget")
	merged := budgetLedger(r, dir, rc)
	if err := atomic.WriteFile(dir+"/budget.json", []byte(merged+newline)); err != nil {
		r.Warn(fmt.Sprintf("could not write %s/budget.json: %v", dir, err))
		return
	}
	fmt.Fprintf(r.Human, "» receipt: consented over-budget run recorded in %s/budget.json (projected $%s %s vs budget $%s)\n",
		dir, st.estUSD, st.estTier, st.budgetUSD)
}

func budgetLedger(r *session, dir string, rc *jq.Obj) string {
	merged := "[" + rc.MarshalPretty() + "]"
	raw, err := os.ReadFile(dir + "/budget.json")
	if err != nil {
		return merged
	}
	// An appended ledger renders jq-array-pretty (elements indented).
	doc, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		r.Warn("budget.json unreadable — starting a fresh receipt ledger")
		return merged
	}
	arr, ok := doc.([]any)
	if !ok {
		return merged
	}
	parts := make([]string, 0, len(arr)+1)
	for _, e := range arr {
		if o, ok := e.(*jq.Obj); ok {
			parts = append(parts, indentPretty(o.MarshalPretty()))
		} else {
			parts = append(parts, "  "+jq.Compact(e))
		}
	}
	return "[\n" + strings.Join(append(parts, indentPretty(rc.MarshalPretty())), ",\n") + "\n]"
}

// indentPretty indents every line of a pretty object by two spaces,
// matching jq's array-element layout.
func indentPretty(pretty string) string {
	lines := strings.Split(pretty, newline)
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, newline)
}
