package reportcore

import (
	"math/big"
	"strconv"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
)

const mtdFloatBits = 64

// monthToDate builds the MTD block for report --cost when a monthly
// ceiling is configured in the captured bundle. Nil when no ceiling is
// set, so the cost envelope stays byte-identical to the pre-cap shape.
func monthToDate(st *state) *core.ReportMTD {
	if !hasMonthlyCap(st) {
		return nil
	}
	now := parseNow(st.now)
	repoRoot := st.req.RepoRootPath()
	agg := spend.AggregateMTD(readcommon.StateDir(repoRoot), now)
	if agg.Warning != "" {
		st.warn(agg.Warning)
	}
	return buildMTD(st, agg)
}

func hasMonthlyCap(st *state) bool {
	if st.policy == nil || st.policy.Resolved == nil {
		return false
	}
	sv := st.policy.Resolved.Spend
	return sv.MonthlyBudgetUSD != nil || sv.MonthlyRepoCap != nil || sv.MonthlyPersonalCap != nil
}

func parseNow(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now()
	}
	return t
}

func buildMTD(st *state, agg spend.MTD) *core.ReportMTD {
	sv := st.policy.Resolved.Spend
	mtd := &core.ReportMTD{Month: agg.Month, KnownCount: agg.KnownCount, UnknownCount: agg.UnknownCount}
	if agg.KnownUSD != "" {
		if f, err := strconv.ParseFloat(agg.KnownUSD, mtdFloatBits); err == nil {
			mtd.KnownUSD = &f
		}
	}
	if sv.MonthlyBudgetUSD != nil {
		mtd.MonthlyCapUSD = sv.MonthlyBudgetUSD
	}
	if sv.MonthlyRepoCap != nil {
		mtd.MonthlyRepoCap = sv.MonthlyRepoCap
	}
	if sv.MonthlyPersonalCap != nil {
		mtd.MonthlyPersonalCap = sv.MonthlyPersonalCap
	}
	mtd.RemainingUSD = remainingUSD(mtd.MonthlyCapUSD, agg)
	return mtd
}

// remainingUSD returns ceiling minus known spend, clamped at zero. No
// receipts at all (empty known with zero unknown) spent nothing, so the
// full ceiling remains; unpriced receipts with no known spend leave the
// remainder unknown, never the full ceiling.
func remainingUSD(ceiling *float64, agg spend.MTD) *float64 {
	if ceiling == nil {
		return nil
	}
	if agg.KnownUSD == "" {
		if agg.UnknownCount > 0 {
			return nil
		}
		rem := *ceiling
		return &rem
	}
	capRat, okCap := new(big.Rat).SetString(strconv.FormatFloat(*ceiling, 'f', -1, mtdFloatBits))
	knownRat, okKnown := new(big.Rat).SetString(agg.KnownUSD)
	if !okCap || !okKnown {
		return nil
	}
	rem := new(big.Rat).Sub(capRat, knownRat)
	if rem.Sign() < 0 {
		rem.SetInt64(0)
	}
	remF, _ := rem.Float64()
	return &remF
}
