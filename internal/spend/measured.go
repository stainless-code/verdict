package spend

import (
	"math/big"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// ScopeInvocation marks the ledger row one `run` invocation writes when it
// authorizes a two-finder execution; the run settles it in place with the
// spend its spawns measured.
const ScopeInvocation = "invocation"

// Measured is one bundle's spend the agent CLIs' own output priced: the
// exact sum of its measured ledger rows and how many rows contributed.
type Measured struct {
	USD  string
	Rows int
}

// MeasuredRow is one ledger row priced from the agent CLI's own output.
type MeasuredRow struct {
	USD       *big.Rat
	Execution string
	Scope     string
}

// MeasuredSpend reads a bundle's budget.json and sums its measured rows.
func MeasuredSpend(bundleDir string) Measured {
	return SumMeasured(BundleMeasuredRows(bundleDir))
}

// BundleMeasuredRows reads a bundle's budget.json measured rows; nil when the
// ledger is absent or unreadable.
func BundleMeasuredRows(bundleDir string) []MeasuredRow {
	return MeasuredRows(readLedger(bundleDir))
}

// MeasuredRows picks the ledger rows the agent CLI's own output priced
// (measured or token-priced), one per invocation the way the month-to-date
// scan dedupes them. Projections, self-reports, and launches that never
// started are not measured spend and stay out.
func MeasuredRows(ledger []any) []MeasuredRow {
	seen := map[string]bool{}
	var rows []MeasuredRow
	for _, row := range ledger {
		obj, ok := row.(*jq.Obj)
		if !ok || !settledMeasured(obj) {
			continue
		}
		// The amount is checked before the dedupe key is spent, so an
		// unreadable row cannot hide a later readable copy of itself.
		projected, _ := obj.Get("projected_usd")
		usd, ok := ratOfJSONNumber(projected)
		if !ok || isDuplicate(obj, seen) {
			continue
		}
		rows = append(rows, MeasuredRow{
			USD: usd, Execution: stringFieldOf(obj, fieldExecutionID), Scope: stringFieldOf(obj, "scope"),
		})
	}
	return rows
}

// SumMeasured totals measured rows exactly; USD is "" when none contributed.
func SumMeasured(rows []MeasuredRow) Measured {
	if len(rows) == 0 {
		return Measured{}
	}
	sum := new(big.Rat)
	for _, row := range rows {
		sum.Add(sum, row.USD)
	}
	return Measured{USD: FormatRat(sum), Rows: len(rows)}
}

// DisplayUSD renders an exact USD literal at the precision every money line
// prints: rounded half away from zero to four decimals, trailing zeros
// trimmed. An agent CLI's float noise (1.30604299999999994) stays in the
// receipt, where the month-to-date sum needs it exact, and out of the line a
// person reads. A literal that does not parse is returned unchanged.
func DisplayUSD(lit string) string {
	rat, ok := ParseAmount(lit)
	if !ok {
		return lit
	}
	return roundUSD(rat, false)
}

// roundUSD renders an exact amount at four decimals, half away from zero, or
// up to the next ten-thousandth when up is set.
func roundUSD(rat *big.Rat, up bool) string {
	scaled := new(big.Rat).Mul(rat, big.NewRat(decimalPrecision, 1))
	num, den := scaled.Num(), scaled.Denom()
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	switch {
	case up && rem.Sign() != 0:
		quo.Add(quo, big.NewInt(1))
	case !up && new(big.Int).Mul(rem, big.NewInt(mtdBigTwo)).Cmp(den) >= 0:
		quo.Add(quo, big.NewInt(1))
	}
	return FormatRat(new(big.Rat).SetFrac(quo, big.NewInt(decimalPrecision)))
}

// InvocationRun is one two-finder execution as its invocation receipts price
// it: the measured spend summed over every invocation, and whether every
// invocation settled. A resumed execution is one run, however many
// invocations it took.
type InvocationRun struct {
	USD       *big.Rat
	Execution string
	Complete  bool
}

// InvocationRuns groups a bundle's invocation receipts by execution, in
// ledger order, keeping only executions with at least one measured or
// token-priced invocation. An execution with any invocation still unsettled
// is not Complete: its USD is a known lower bound, not its price.
func InvocationRuns(bundleDir string) []InvocationRun {
	acc := runAccumulator{seen: map[string]bool{}, index: map[string]int{}, pending: map[string]int{}}
	for _, row := range readLedger(bundleDir) {
		if obj, ok := row.(*jq.Obj); ok {
			acc.add(obj)
		}
	}
	acc.settlePending()
	out := acc.runs[:0]
	for i := range acc.runs {
		if acc.priced[i] {
			out = append(out, acc.runs[i])
		}
	}
	return out
}

// runAccumulator folds invocation rows into one run per execution.
type runAccumulator struct {
	seen    map[string]bool
	index   map[string]int
	pending map[string]int
	runs    []InvocationRun
	priced  []bool
}

// add folds one invocation row. Only a readable settled copy spends the
// invocation's dedupe key, so an unreadable or unsettled copy cannot hide a
// later settled one; an invocation no copy settled is resolved at the end.
func (a *runAccumulator) add(obj *jq.Obj) {
	if stringFieldOf(obj, "scope") != ScopeInvocation {
		return
	}
	i := a.slot(stringFieldOf(obj, fieldExecutionID))
	usd, settled := settledAmount(obj)
	if !settled {
		a.pend(obj, i)
		return
	}
	if isDuplicate(obj, a.seen) {
		return
	}
	if stringFieldOf(obj, "priced_by") != PricedNotStarted {
		a.runs[i].USD.Add(a.runs[i].USD, usd)
		a.priced[i] = true
	}
}

// pend records an unsettled copy; one with no dedupe key is its own
// invocation and leaves its run incomplete at once.
func (a *runAccumulator) pend(obj *jq.Obj, i int) {
	key := budgetDedupeKey(obj)
	if key == "" {
		a.runs[i].Complete = false
		return
	}
	a.pending[key] = i
}

// settlePending marks incomplete every run holding an invocation no readable
// settled copy priced.
func (a *runAccumulator) settlePending() {
	for key, i := range a.pending {
		if !a.seen[key] {
			a.runs[i].Complete = false
		}
	}
}

// slot is the run index of exec, opening a run on its first invocation.
func (a *runAccumulator) slot(exec string) int {
	if i, ok := a.index[exec]; ok {
		return i
	}
	a.index[exec] = len(a.runs)
	a.runs = append(a.runs, InvocationRun{USD: new(big.Rat), Execution: exec, Complete: true})
	a.priced = append(a.priced, false)
	return len(a.runs) - 1
}

// settledAmount reads one invocation row's settled amount: measured,
// token-priced, or a launch that never started (zero).
func settledAmount(obj *jq.Obj) (*big.Rat, bool) {
	if !settledMeasured(obj) && stringFieldOf(obj, "priced_by") != PricedNotStarted {
		return nil, false
	}
	projected, _ := obj.Get("projected_usd")
	return ratOfJSONNumber(projected)
}

// InvocationOwner reports, for one bundle, whether a measured two-finder
// execution already counts a self-reported cost record. An execution with any
// measured invocation is counted once from its receipts (at least its
// measured sum), so the record is owned when it names such an execution, or,
// naming none, when the latest invocation row at or before it (any, when the
// record carries no readable time) belongs to one. A self-report is never an
// extra run beside measured spend of the same execution.
func InvocationOwner(bundleDir string) func(exec string, at any) bool {
	owners := invocationOwners(bundleDir)
	measured := map[string]bool{}
	for i := range owners {
		if owners[i].measured {
			measured[owners[i].exec] = true
		}
	}
	o := ownership{owners: owners, measured: measured}
	return o.owns
}

// ownership answers InvocationOwner for one bundle's invocation rows.
type ownership struct {
	measured map[string]bool
	owners   []ledgerOwner
}

func (o ownership) owns(exec string, at any) bool {
	if exec != unidentifiedExecID {
		return o.measured[exec]
	}
	when, timed := atTimeOf(at)
	if !timed {
		return len(o.measured) > 0
	}
	latest := latestOwnerAt(o.owners, when)
	return latest != nil && o.measured[latest.exec]
}

// invocationOwners is the bundle's invocation rows as ownership candidates.
func invocationOwners(bundleDir string) []ledgerOwner {
	var owners []ledgerOwner
	for _, row := range readLedger(bundleDir) {
		obj, ok := row.(*jq.Obj)
		if !ok || stringFieldOf(obj, "scope") != ScopeInvocation {
			continue
		}
		if owner, ok := ledgerOwnerOf(obj); ok {
			owners = append(owners, owner)
		}
	}
	return owners
}

// latestOwnerAt is the latest invocation row written at or before when.
func latestOwnerAt(owners []ledgerOwner, when time.Time) *ledgerOwner {
	var latest *ledgerOwner
	for i := range owners {
		row := &owners[i]
		if !row.at.After(when) && (latest == nil || row.at.After(latest.at)) {
			latest = row
		}
	}
	return latest
}

// DisplayBreach renders an exact amount literal compared against limit for a
// person; see DisplayBreachRat. A literal that does not parse is returned
// unchanged.
func DisplayBreach(amount, limit string) string {
	rat, ok := ParseAmount(amount)
	if !ok {
		return amount
	}
	return DisplayBreachRat(rat, limit)
}

// DisplayBreachRat renders an exact amount compared against limit for a
// person: four decimals half away from zero, except that an amount over the
// limit whose rounding would read at or under it rounds up instead, so a
// printed breach is always visible. It formats from the rational itself, so a
// mean whose fraction is too long to print as a literal still reads as money.
func DisplayBreachRat(amount *big.Rat, limit string) string {
	shown := roundUSD(amount, false)
	if !ExceedsRat(amount, limit) || Exceeds(shown, limit) {
		return shown
	}
	return roundUSD(amount, true)
}

// ExceedsRat compares an exact amount with the decimal literal the operator
// provided, as Exceeds does.
func ExceedsRat(amount *big.Rat, limit string) bool {
	l, ok := new(big.Rat).SetString(limit)
	return amount != nil && ok && amount.Sign() >= 0 && l.Sign() >= 0 && amount.Cmp(l) > 0
}
