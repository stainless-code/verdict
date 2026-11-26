package spend

import (
	"math/big"

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
		if !ok || !settledMeasured(obj) || isDuplicate(obj, seen) {
			continue
		}
		projected, _ := obj.Get("projected_usd")
		usd, ok := ratOfJSONNumber(projected)
		if !ok {
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
