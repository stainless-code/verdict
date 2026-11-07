package fetchcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
)

// TestReceiptSummaryNamesOnlyRealAmounts pins the human receipt against a
// consented blind run: with no projection and no ceiling it printed
// `projected $  vs budget $` right after the sibling line that says a blind run
// has no ceiling, implying a budget the operator never set.
func TestReceiptSummaryNamesOnlyRealAmounts(t *testing.T) {
	cases := []struct{ name, est, tier, budget, want string }{
		{"armed", "3", "standard", "0.50", "projected $3 standard vs budget $0.50"},
		{"blind consented", "", "", "", "projection unknown with no budget ceiling"},
		{"budget only", "", "", "10", "projection unknown vs budget $10"},
		{"projection only", "9.5", "standard", "", "projected $9.5 standard with no budget ceiling"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &budgetState{estUSD: tc.est, estTier: tc.tier, budgetUSD: tc.budget}
			if got := receiptSummary(st); got != tc.want {
				t.Errorf("receiptSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGroupReceiptOmitsAnAbsentPersonalCeiling pins the grouped line: consent
// over a group guard without a personal ceiling rendered `vs personal $`.
func TestGroupReceiptOmitsAnAbsentPersonalCeiling(t *testing.T) {
	report := &config.GroupSpendReport{
		Total:      "5",
		TotalKnown: true,
		Groups:     []config.GroupSpend{{GroupID: "api", Effort: "standard", GuardUSD: "4", Projection: "5", Known: true, OverGuard: true}},
	}
	st := &budgetState{groupSpend: newGroupSpendState(report)}
	got := groupReceiptSummary(st)
	if strings.Contains(got, "vs personal") {
		t.Errorf("groupReceiptSummary = %q, want no personal comparison without a ceiling", got)
	}
	if !strings.HasSuffix(got, "; total $5") {
		t.Errorf("groupReceiptSummary = %q, want the exact total last", got)
	}

	report.PersonalLimit = "10"
	withCeiling := &budgetState{groupSpend: newGroupSpendState(report)}
	if got := groupReceiptSummary(withCeiling); !strings.HasSuffix(got, "; total $5 vs personal $10") {
		t.Errorf("groupReceiptSummary = %q, want the ceiling named when it exists", got)
	}
}
