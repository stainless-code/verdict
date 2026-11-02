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
)

// budgetState carries the cost-control plane: the operator spend budget,
// the pre-run estimate, and the consent gate.
type budgetState struct {
	estimateJSON  any
	budgetJSON    any
	budgetUSD     string
	budgetSrc     string
	estTier       string
	estUSD        string
	estRuns       int
	tierRuns      int
	hasBudget     bool
	receiptNeeded bool
}

// resolveBudget ports resolve_budget: REVIEW_BUDGET_USD wins, else the
// first usable line of .review/budget (a leading $ is stripped). Unset
// disables the whole plane.
func resolveBudget(env map[string]string) (budgetState, error) {
	var st budgetState
	raw, src := "", ""
	if v, ok := env["REVIEW_BUDGET_USD"]; ok && v != "" {
		raw, src = strings.TrimSpace(v), "REVIEW_BUDGET_USD"
	} else if v, ok := readKnobFile(".review/budget"); ok {
		raw, src = strings.TrimSpace(strings.TrimPrefix(v, "$")), ".review/budget"
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

// isBudgetNumber ports `^[0-9]+(\.[0-9]+)?$`.
// isBudgetNumber ports the knob regex `^[0-9]+(\.[0-9]+)?$`: digits
// with an optional decimal fraction (a bare "1." or "." never matches).
func isBudgetNumber(s string) bool {
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if hasDot && strings.Contains(fracPart, ".") {
		return false
	}
	if !allDigits(intPart) {
		return false
	}
	if hasDot && !allDigits(fracPart) {
		return false
	}
	return true
}

// allDigits reports whether s is a non-empty ASCII digit run.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// budgetGate ports budget_gate: with a budget set, print the estimate line,
// arm the envelope summaries, and refuse over-budget runs without consent.
func budgetGate(r *session, st *budgetState, consent bool) error {
	if !st.hasBudget {
		return nil
	}
	costEstimate(r, st)
	st.budgetJSON = jq.NewObj("usd", jsonNumber(st.budgetUSD), keySource, st.budgetSrc, "consented", consent)
	tierVal := any(nil)
	if st.estTier != "" {
		tierVal = st.estTier
	}
	st.estimateJSON = jq.NewObj("tier", tierVal, "projected_usd", jsonFloat(st.estUSD), "comparable_runs", st.estRuns)
	if st.estUSD == "" {
		return nil
	}
	if overBudget(st.estUSD, st.budgetUSD) {
		if consent {
			st.receiptNeeded = true
			return nil
		}
		return &core.RefusalError{Code: "budget-exceeded", Message: fmt.Sprintf(
			"projected %s run ~$%s exceeds budget $%s (%s) from %d comparable run(s) — consent required before this run starts",
			st.estTier, st.estUSD, st.budgetUSD, st.budgetSrc, st.estRuns)}
	}
	return nil
}

// costEstimate ports cost_estimate: the mean est_usd of the most-run effort
// tier across every .review/*/cost.json record (ties break toward the
// deeper tier). Unknown history reads unknown, never blocking.
func costEstimate(r *session, st *budgetState) {
	st.estUSD, st.estRuns, st.estTier, st.tierRuns = "", 0, "", 0
	recs := estimateRecs(r)
	if len(recs) == 0 {
		fmt.Fprintln(r.Human, "» estimate: no comparable past runs — cost unknown (no usable .review/*/cost.json records yet)")
		return
	}
	tier, tierRuns, mean, ok := pickEstimateTier(recs)
	st.estRuns = len(recs)
	if !ok {
		fmt.Fprintln(r.Human, "» estimate: no comparable past runs — cost unknown (no usable .review/*/cost.json records yet)")
		st.estRuns = 0
		return
	}
	st.estTier, st.tierRuns = tier, tierRuns
	if mean == "" {
		fmt.Fprintf(r.Human, "» estimate: tier %s from %d comparable run(s) — projected cost unknown "+
			"(none reported est_usd); budget $%s (%s) unenforced on unknown\n",
			tier, tierRuns, st.budgetUSD, st.budgetSrc)
		return
	}
	st.estUSD = mean
	if overBudget(mean, st.budgetUSD) {
		fmt.Fprintf(r.Human, "» estimate: tier %s ~$%s/run from %d comparable run(s) — OVER budget $%s (%s)\n",
			tier, mean, st.estRuns, st.budgetUSD, st.budgetSrc)
	} else {
		fmt.Fprintf(r.Human, "» estimate: tier %s ~$%s/run from %d comparable run(s) — within budget $%s (%s)\n",
			tier, mean, st.estRuns, st.budgetUSD, st.budgetSrc)
	}
}

// costRec is one normalized cost record: effort tier plus optional est_usd.
type costRec struct {
	tier string
	usd  string
	has  bool
}

// readCostRecs ports the per-file jq record reader: arrays iterate,
// single docs read as one; malformed files report unreadable.
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

// costRecord ports one cost record: the tier from effort (standard when
// absent or unknown) and the priced usd when est_usd is numeric.
// Non-object records still count (the shell's filter reads .effort as
// null → standard with no priced usd).
func costRecord(it any) costRec {
	m, _ := jq.AsMap(it)
	tier := tierStandard
	var effort any
	if m != nil {
		effort = m["effort"]
	}
	if s, ok := effort.(string); ok {
		// jq ascii_downcase is Unicode-aware; tiers are
		// ASCII in practice.
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

// estNumber ports `(if (.est_usd | type) == keyNumber then .est_usd else
// null end)`, rendering the jq number literal.
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

// pickEstimateTier ports the jq summary: most runs wins, ties break toward
// the deeper (costlier) tier — first strictly-greater over
// [deep standard cheap] keeps the deeper tier on ties. Empty input reads
// no-pick, like max_by over an empty array.
func pickEstimateTier(recs []costRec) (tier string, tierRuns int, mean string, ok bool) {
	if len(recs) == 0 {
		return "", 0, "", false
	}
	best := [2]float64{-1, -1}
	for _, t := range []string{"deep", tierStandard, "cheap"} {
		runs, meanVal, tierMean := tierStats(recs, t)
		if float64(runs) > best[0] || (float64(runs) == best[0] && meanVal > best[1]) {
			best = [2]float64{float64(runs), meanVal}
			tier, tierRuns, mean, ok = t, runs, tierMean, true
		}
	}
	return tier, tierRuns, mean, ok
}

// tierStats ports one tier's accumulation: the run count plus the priced
// mean (-1 with no priced runs, so unpriced tiers still compete on runs).
func tierStats(recs []costRec, t string) (runs int, meanVal float64, mean string) {
	var sum float64
	priced := 0
	for _, r := range recs {
		if r.tier != t {
			continue
		}
		runs++
		sum, priced = tierPriced(r, sum, priced)
	}
	meanVal = -1.0
	if priced > 0 {
		meanVal = sum / float64(priced)
		mean = jq.Compact(round4(meanVal))
	}
	return runs, meanVal, mean
}

// estimateRecs ports the cost-history sweep: every readable record
// accumulates, while unreadable files warn once (the shell accumulates
// "$bad$f " per file, trailing space included).
func estimateRecs(r *session) []costRec {
	var recs []costRec
	var bad []string
	files, gerr := filepath.Glob(".review/*/cost.json")
	if gerr != nil {
		files = nil
	}
	slices.Sort(files)
	for _, f := range files {
		rec, ok := readCostRecs(f)
		if !ok {
			bad = append(bad, f)
			continue
		}
		recs = append(recs, rec...)
	}
	if len(bad) > 0 {
		r.Warn(fmt.Sprintf("cost history unreadable — skipped from estimate: %s ", strings.Join(bad, " ")))
	}
	return recs
}

// tierPriced ports one priced record: the usd parses into the sum.
func tierPriced(r costRec, sum float64, priced int) (float64, int) {
	if r.has {
		if f, err := strconv.ParseFloat(r.usd, numBitSize); err == nil {
			return sum + f, priced + 1
		}
	}
	return sum, priced
}

// overBudget ports the awk `p > b` float comparison.
func overBudget(projected, budget string) bool {
	p, err1 := strconv.ParseFloat(projected, numBitSize)
	b, err2 := strconv.ParseFloat(budget, numBitSize)
	if err1 != nil || err2 != nil {
		return false
	}
	return p > b
}

// jsonNumber renders a validated budget literal for --argjson parity: jq
// preserves plain-decimal literals verbatim (0.50 stays 0.50), so the
// literal rides through untouched (budget strings never carry exponents
// — the knob regex forbids them).
func jsonNumber(lit string) any {
	return jq.Number(lit)
}

// jsonFloat renders a computed mean/est literal ("" reads null). Computed
// values print canonically on both sides.
func jsonFloat(lit string) any {
	if lit == "" {
		return nil
	}
	return jq.Number(lit)
}

// writeBudgetReceipt ports write_budget_receipt: a consented over-budget
// run appends one receipt to the bundle's budget.json ledger.
func writeBudgetReceipt(r *session, st *budgetState, dir, now string) {
	if !st.receiptNeeded {
		return
	}
	rc := jq.NewObj(
		"at", st2at(now), "budget_usd", jsonNumber(st.budgetUSD), "budget_src", st.budgetSrc,
		"projected_tier", st.estTier, "projected_usd", jsonFloat(st.estUSD),
		"comparable_runs", st.estRuns, "consent", "--consent-over-budget",
	)
	merged := budgetLedger(r, dir, rc)
	if err := atomic.WriteFile(dir+"/budget.json", []byte(merged+newline)); err != nil {
		r.Warn(fmt.Sprintf("could not write %s/budget.json: %v", dir, err))
		return
	}
	fmt.Fprintf(r.Human, "» receipt: consented over-budget run recorded in %s/budget.json (projected $%s %s vs budget $%s)\n",
		dir, st.estUSD, st.estTier, st.budgetUSD)
}

// budgetLedger ports the receipt ledger merge: the new receipt appends
// to the stored array (old receipts keep their stored key order, like
// the shell's jq pass-through), or starts a fresh `[$rc]` ledger,
// warning when the stored ledger is unreadable.
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

// st2at formats the run timestamp (callers pass cli.NowUTC already).
func st2at(now string) string { return now }

// asciiLower lowercases ASCII letters only (C-locale tr semantics).
func asciiLower(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		sb.WriteByte(c)
	}
	return sb.String()
}
