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
	scaled := new(big.Rat).Mul(rat, big.NewRat(decimalPrecision, 1))
	num, den := scaled.Num(), scaled.Denom()
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if new(big.Int).Mul(rem, big.NewInt(mtdBigTwo)).Cmp(den) >= 0 {
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
	acc := runAccumulator{seen: map[string]bool{}, index: map[string]int{}}
	for _, row := range readLedger(bundleDir) {
		if obj, ok := row.(*jq.Obj); ok {
			acc.add(obj)
		}
	}
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
	seen   map[string]bool
	index  map[string]int
	runs   []InvocationRun
	priced []bool
}

func (a *runAccumulator) add(obj *jq.Obj) {
	if stringFieldOf(obj, "scope") != ScopeInvocation || isDuplicate(obj, a.seen) {
		return
	}
	i := a.slot(stringFieldOf(obj, fieldExecutionID))
	usd, settled := settledAmount(obj)
	switch {
	case !settled:
		a.runs[i].Complete = false
	case stringFieldOf(obj, "priced_by") != PricedNotStarted:
		a.runs[i].USD.Add(a.runs[i].USD, usd)
		a.priced[i] = true
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

// InvocationOwner reports, for one bundle, whether a measured invocation
// receipt already counts a self-reported cost record of an execution written
// at a given time. It is the month-to-date scan's ownership rule: the latest
// invocation row at or before the record, of its execution (any, when it
// names none), was priced from the agent CLI's own output. A record with no
// readable time is owned when any measured invocation row of its execution
// (any, when unnamed) exists: a self-report that cannot be ordered never
// outranks measured spend.
func InvocationOwner(bundleDir string) func(exec string, at any) bool {
	owners := invocationOwners(bundleDir)
	meta := &recordedMeta{maxAt: map[string]time.Time{}, owners: owners}
	return func(exec string, at any) bool {
		if when, ok := atTimeOf(at); ok {
			return meta.measuredOwner(exec, when)
		}
		return anyMeasuredOwner(owners, exec)
	}
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

// anyMeasuredOwner reports a measured owner of exec (any, when unnamed).
func anyMeasuredOwner(owners []ledgerOwner, exec string) bool {
	for i := range owners {
		if owners[i].measured && (exec == unidentifiedExecID || owners[i].exec == exec) {
			return true
		}
	}
	return false
}

// DisplayBreach renders an exact amount compared against limit for a person:
// DisplayUSD, except that an amount over the limit whose rounding would read
// at or under it rounds up instead, so a printed breach is always visible.
func DisplayBreach(amount, limit string) string {
	shown := DisplayUSD(amount)
	if !Exceeds(amount, limit) || Exceeds(shown, limit) {
		return shown
	}
	rat, ok := new(big.Rat).SetString(amount)
	if !ok {
		return amount
	}
	scaled := new(big.Rat).Mul(rat, big.NewRat(decimalPrecision, 1))
	quo, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	return FormatRat(new(big.Rat).SetFrac(quo, big.NewInt(decimalPrecision)))
}
