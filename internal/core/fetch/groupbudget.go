package fetchcore

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// groupSpendState is the grouped evaluation the gate records for receipts: the
// resolved per-group budgets, the report, and the concrete groups each verdict
// named. It lives on budgetState so an existing caller needs no new field.
type groupSpendState struct {
	overGuard []string
	unknown   []string
	report    config.GroupSpendReport
}

// gateGroupedSpend is the per-group plane: it derives each resolved group's
// guard, prices the groups from the histories bound to their own captures, and
// refuses on the first concrete violation. A known group over its own guard is
// always reported, a known aggregate (or known lower bound) above the personal
// ceiling is a spend-capped hit, and an unknown group under the strict posture
// refuses until the operator consents. Nothing here invents USD.
func gateGroupedSpend(r *session, st *budgetState, res *config.Resolved, consent bool) error {
	budgets := config.GroupBudgets(res)
	if len(budgets) == 0 {
		return nil
	}
	samples := boundGroupSamples(r)
	sum := config.EvaluateGroupSpend(budgets, samples, personalLimitLiteral(res))
	if len(budgets) == 1 && !sum.Groups[0].Known {
		return gateSoleGroupWholeRun(r, st, res, consent, &budgets[0])
	}
	st.groupSpend = newGroupSpendState(&sum)
	st.estimateJSON = groupedEstimateObject(&sum)
	if st.hasBudget {
		st.budgetJSON = jq.NewObj("usd", jsonNumber(st.budgetUSD), keySource, st.budgetSrc, "consented", consent)
	}
	if ref := gateGroupedGuards(st, &sum, consent); ref != nil {
		return ref
	}
	if ref := gateGroupedPersonal(st, &sum, consent); ref != nil {
		return ref
	}
	if len(st.groupSpend.unknown) > 0 {
		return gateGroupedUnknown(st, res, consent)
	}
	return nil
}

// gateSoleGroupWholeRun prices a plan whose only group has no bound price of
// its own. That group's work IS the whole proposal, so the existing whole-run
// estimator (measured handoffs, tier selection, unknown stays unknown) prices
// it, and the sole group's own guard plus the personal ceiling are applied to
// that projection with exact decimal comparisons. No run binding is invented
// and no price is split across groups.
func gateSoleGroupWholeRun(r *session, st *budgetState, res *config.Resolved, consent bool,
	group *config.GroupBudget,
) error {
	adoptGroupGuard(st, group)
	if err := costEstimate(r, st, res); err != nil {
		return err
	}
	recordEstimate(st, consent)
	if st.estUSD == "" {
		return gateUnknownEstimate(r, st, res, consent)
	}
	if over := config.SpendExceeds(st.estUSD, group.GuardUSD); over {
		if !consent {
			return budgetExceeded(st)
		}
		st.receiptNeeded = true
	}
	return gateSoleGroupPersonal(st, res, consent)
}

// gateSoleGroupPersonal applies the personal ceiling to the whole-proposal
// projection, exactly like the group guard.
func gateSoleGroupPersonal(st *budgetState, res *config.Resolved, consent bool) error {
	limit := personalLimitLiteral(res)
	if !config.SpendExceeds(st.estUSD, limit) {
		return nil
	}
	if !consent {
		return personalCeilingError(st, limit)
	}
	st.receiptNeeded = true
	return nil
}

// adoptGroupGuard makes the sole group's resolved guard the run's budget guard
// when no ordinary budget resolved, so the refusal and receipt name the guard
// that actually applied.
func adoptGroupGuard(st *budgetState, group *config.GroupBudget) {
	if group.GuardUSD == "" || st.budgetUSD != "" {
		return
	}
	st.budgetUSD, st.budgetSrc, st.hasBudget = group.GuardUSD, group.Source, true
}

// newGroupSpendState records the report and the concrete group ids each verdict
// named, so a receipt can state what it overrides.
func newGroupSpendState(report *config.GroupSpendReport) *groupSpendState {
	st := &groupSpendState{report: *report}
	for i := range report.Groups {
		group := &report.Groups[i]
		if group.Known && group.OverGuard {
			st.overGuard = append(st.overGuard, group.GroupID)
		}
		if !group.Known {
			st.unknown = append(st.unknown, group.GroupID)
		}
	}
	return st
}

// gateGroupedGuards refuses a known group over its own guard and arms the
// receipt that records the consent.
func gateGroupedGuards(st *budgetState, sum *config.GroupSpendReport, consent bool) error {
	if len(st.groupSpend.overGuard) == 0 {
		return nil
	}
	if !consent {
		return groupOverGuardError(sum)
	}
	st.receiptNeeded = true
	return nil
}

// boundGroupSamples reads the group-cost claims of every past bundle whose
// typed capture verifies: each sample keeps its own binding, so history
// attributable to another capture is never re-priced against this run.
func boundGroupSamples(r *session) []config.CostSample {
	dirs, err := filepath.Glob(filepath.Join(r.repoRoot, ".verdict", "state", "*"))
	if err != nil {
		return nil
	}
	slices.Sort(dirs)
	var samples []config.CostSample
	var bad []string
	for _, dir := range dirs {
		found, ok := captureSamples(dir)
		if !ok {
			bad = append(bad, filepath.ToSlash(filepath.Join(dir, readcommon.CostFile)))
			continue
		}
		samples = append(samples, found...)
	}
	if len(bad) > 0 {
		r.Warn(fmt.Sprintf("group cost history unreadable — skipped from the group estimate: %s ",
			strings.Join(bad, " ")))
	}
	return samples
}

// captureSamples reads one bundle's verified capture and its bound group costs;
// ok is false only when the record exists but is unreadable.
func captureSamples(dir string) ([]config.CostSample, bool) {
	rs, ref := config.VerifyRunState(dir)
	if ref != nil || rs == nil {
		return nil, true
	}
	found, status := config.ReadGroupCosts(rs, filepath.Join(dir, readcommon.CostFile))
	return found, status != config.LensUsageMalformed
}

// personalLimitLiteral renders the personal ceiling as the exact literal the
// group math compares against; an unset ceiling leaves the plane unenforced.
func personalLimitLiteral(res *config.Resolved) string {
	if res == nil || res.Spend.PersonalLimit == nil {
		return ""
	}
	return strconv.FormatFloat(*res.Spend.PersonalLimit, 'f', -1, floatBitSize)
}

// gateGroupedPersonal applies the personal whole-run ceiling to the exact
// aggregate, and to the known subtotal when another group is unknown: a known
// lower bound above the ceiling is a real violation, never hidden by the
// unknown group.
func gateGroupedPersonal(st *budgetState, sum *config.GroupSpendReport, consent bool) error {
	if !spendOverPersonal(sum) {
		return nil
	}
	if consent {
		st.receiptNeeded = true
		return nil
	}
	return groupPersonalError(sum)
}

// groupOverGuardError refuses a known group whose own projection is above its
// resolved guard, naming the group and both figures.
func groupOverGuardError(sum *config.GroupSpendReport) error {
	worst := sum.Groups[0]
	for i := range sum.Groups {
		group := &sum.Groups[i]
		if group.Known && group.OverGuard && group.GroupID == firstGroupID(sum) {
			worst = *group
		}
	}
	return &core.RefusalError{Code: "budget-exceeded", Message: fmt.Sprintf(
		"projected %s effort for group %s ~$%s exceeds its budget $%s%s — consent required before this run starts",
		worst.Effort, worst.GroupID, worst.Projection, worst.GuardUSD, groupBasisNote(sum))}
}

// firstGroupID is the group whose guard overrun the refusal names first.
func firstGroupID(sum *config.GroupSpendReport) string {
	for i := range sum.Groups {
		if sum.Groups[i].Known && sum.Groups[i].OverGuard {
			return sum.Groups[i].GroupID
		}
	}
	return ""
}

// groupPersonalError refuses the personal ceiling hit, saying whether it rests
// on the exact aggregate or on a known subtotal with unknown groups.
func groupPersonalError(sum *config.GroupSpendReport) error {
	if sum.TotalKnown {
		return &core.RefusalError{Code: codeSpendCapped, Message: fmt.Sprintf(
			"projected groups ~$%s total exceeds the personal spend ceiling %s — consent before this run "+
				"starts, or raise the ceiling in settings and narrow the run",
			sum.Total, groupLimitNote(sum))}
	}
	return &core.RefusalError{Code: codeSpendCapped, Message: fmt.Sprintf(
		"known group projections already exceed the personal spend ceiling %s while %s unknown — the "+
			"subtotal is a lower bound, so consent covers an unbounded remainder",
		groupLimitNote(sum), strings.Join(unknownGroupIDs(sum), ", "))}
}

// gateGroupedUnknown refuses an unknown group projection under the strict
// posture: no USD is invented, and consent is the only way past it.
func gateGroupedUnknown(st *budgetState, res *config.Resolved, consent bool) error {
	if consent {
		st.receiptNeeded = true
		return nil
	}
	if res == nil || !res.Spend.RequireKnownEstimate {
		return nil
	}
	return &core.RefusalError{Code: "budget-unknown", Message: fmt.Sprintf(
		"no cost history attributable to this capture prices %s — require_known_estimate refuses the run "+
			"until the operator consents to an unknown group projection",
		strings.Join(unknownGroupIDs(&st.groupSpend.report), ", "))}
}

// unknownGroupIDs lists the groups the history could not price.
func unknownGroupIDs(sum *config.GroupSpendReport) []string {
	var out []string
	for i := range sum.Groups {
		if !sum.Groups[i].Known {
			out = append(out, sum.Groups[i].GroupID)
		}
	}
	return out
}

// groupBasisNote states the groups a verdict compared and whether the total is
// exact or a lower bound.
func groupBasisNote(sum *config.GroupSpendReport) string {
	if sum.TotalKnown {
		return fmt.Sprintf(" (groups total $%s)", sum.Total)
	}
	return fmt.Sprintf(" (known subtotal, %s unknown)", strings.Join(unknownGroupIDs(sum), ", "))
}

// groupLimitNote renders the personal ceiling for a message.
func groupLimitNote(sum *config.GroupSpendReport) string {
	return "$" + sum.PersonalLimit
}

// spendOverPersonal reports the personal ceiling hit on exact or lower-bound
// figures.
func spendOverPersonal(sum *config.GroupSpendReport) bool {
	return sum.OverPersonal || sum.KnownSubtotalOverPersonal
}

// groupedEstimateObject is the public projection of a grouped run: every
// group's guard, tier and the projection the history supports (absent when
// unknown), and the personal comparison stated as an exact total or a known
// lower bound. No whole-run budget and no zero is invented for a hidden
// (unknown) group.
func groupedEstimateObject(report *config.GroupSpendReport) *jq.Obj {
	groups := make([]any, 0, len(report.Groups))
	for i := range report.Groups {
		groups = append(groups, groupReceiptEntry(&report.Groups[i]))
	}
	o := jq.NewObj("groups", groups)
	personal := jq.NewObj("basis", personalBasis(report))
	if report.PersonalLimit != "" {
		personal.Set("limit_usd", jsonNumber(report.PersonalLimit))
	}
	if report.TotalKnown {
		personal.Set("total_usd", jsonNumber(report.Total))
	} else if report.KnownSubtotal != "" {
		personal.Set("known_subtotal_usd", jsonNumber(report.KnownSubtotal))
	}
	o.Set("personal", personal)
	return o
}

// receiptProjectionBody writes the projection a consented receipt covers: the
// grouped per-group guards and totals, or the whole-run estimate.
func receiptProjectionBody(st *budgetState, rc *jq.Obj) {
	if st.groupSpend != nil {
		groupReceiptBody(st, rc)
	} else if st.hasBudget {
		rc.Set("budget_usd", jsonNumber(st.budgetUSD))
		rc.Set("budget_src", st.budgetSrc)
	}
	if st.groupSpend == nil {
		rc.Set("projected_tier", st.estTier)
		rc.Set("projected_usd", jsonFloat(st.estUSD))
		rc.Set("comparable_runs", st.estRuns)
	}
	if st.estUSD == "" {
		rc.Set("reason", "unknown-estimate")
	}
	if st.measuredRuns > 0 {
		rc.Set("basis", st.estBasis)
		rc.Set("measured_runs", st.measuredRuns)
	}
}

// receiptKind names the consented run the receipt covers.
func receiptKind(st *budgetState) string {
	if st.groupSpend != nil {
		return "consented grouped run"
	}
	return "consented over-budget run"
}

// receiptSummary renders the human summary for an armed receipt. It names only
// amounts the run actually carries: a consented blind run has no projection and
// a run without a ceiling has no budget, and printing `$` with nothing after it
// implied a ceiling the operator never set.
func receiptSummary(st *budgetState) string {
	if st.groupSpend != nil {
		return groupReceiptSummary(st)
	}
	summary := "projection unknown"
	if st.estUSD != "" {
		summary = "projected $" + st.estUSD
		if st.estTier != "" {
			summary += " " + st.estTier
		}
	}
	if st.budgetUSD == "" {
		return summary + " with no budget ceiling"
	}
	return summary + " vs budget $" + st.budgetUSD
}

// groupReceiptBody writes the grouped receipt: every group's own guard, the
// projection the history supports (absent when unknown, never zero), the
// personal ceiling comparison with its basis, and the concrete group guards the
// operator consented to override.
func groupReceiptBody(st *budgetState, rc *jq.Obj) {
	report := &st.groupSpend.report
	groups := make([]any, 0, len(report.Groups))
	for i := range report.Groups {
		groups = append(groups, groupReceiptEntry(&report.Groups[i]))
	}
	rc.Set("groups", groups)
	rc.Set("personal_limit_usd", jsonNumber(report.PersonalLimit))
	rc.Set("personal_basis", personalBasis(report))
	if report.TotalKnown {
		rc.Set("personal_total_usd", jsonNumber(report.Total))
	} else if report.KnownSubtotal != "" {
		rc.Set("personal_known_subtotal_usd", jsonNumber(report.KnownSubtotal))
	}
	if len(st.groupSpend.overGuard) > 0 {
		rc.Set("overridden_group_guards", stringList(st.groupSpend.overGuard))
	}
	if len(st.groupSpend.unknown) > 0 {
		rc.Set("unknown_groups", stringList(st.groupSpend.unknown))
	}
}

// groupReceiptEntry renders one group's guard, tier, source and the projection
// the history supports (absent when unknown, never zero).
func groupReceiptEntry(group *config.GroupSpend) *jq.Obj {
	entry := jq.NewObj("group", group.GroupID)
	entry.Set("effort", group.Effort)
	if group.GuardUSD != "" {
		entry.Set("guard_usd", jsonNumber(group.GuardUSD))
	}
	if group.Source != "" {
		entry.Set("guard_src", group.Source)
	}
	if group.Known {
		entry.Set("projected_usd", jsonNumber(group.Projection))
	}
	entry.Set("over_guard", group.OverGuard)
	return entry
}

// listSeparator joins the human receipt's group fragments.
const listSeparator = ", "

// groupSummaryPart renders one group's human fragment.
func groupSummaryPart(group *config.GroupSpend) string {
	projected := "unknown"
	if group.Known {
		projected = "$" + group.Projection
	}
	return fmt.Sprintf("%s %s %s vs guard $%s", group.GroupID, group.Effort, projected, group.GuardUSD)
}

// stringList widens a []string to the []any shape the ordered writer renders.
func stringList(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// personalBasis names what the personal comparison rested on: an exact
// aggregate, a known lower bound while groups are unknown, or no ceiling.
func personalBasis(report *config.GroupSpendReport) string {
	switch {
	case report.PersonalLimit == "":
		return "no-personal-limit"
	case report.TotalKnown:
		return "exact-total"
	default:
		return "known-lower-bound"
	}
}

// groupReceiptSummary renders the human line for a grouped receipt.
func groupReceiptSummary(st *budgetState) string {
	report := &st.groupSpend.report
	parts := make([]string, 0, len(report.Groups))
	for i := range report.Groups {
		parts = append(parts, groupSummaryPart(&report.Groups[i]))
	}
	total := "total unknown"
	if report.TotalKnown {
		total = "total $" + report.Total
	} else if report.KnownSubtotal != "" {
		total = "known subtotal $" + report.KnownSubtotal + " (lower bound)"
	}
	tail := "; " + total
	if report.PersonalLimit != "" {
		tail += " vs personal $" + report.PersonalLimit
	}
	return strings.Join(parts, listSeparator) + tail
}
