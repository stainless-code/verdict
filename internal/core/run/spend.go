package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/storelock"
)

const (
	maxSpendReceiptBytes = 1 << 20
	spendReceiptMode     = 0o600
	stateDirMode         = 0o755
)

func (st *state) authorizeSpend(ctx context.Context, dir string, rounds int) *core.RefusalError {
	if st.drainDigest != "" {
		return nil
	}
	captured, _, err := config.ReadRunState(dir)
	if err != nil {
		return st.unreadableSpendCapture(dir, err.Error())
	}
	if captured == nil || captured.Execution == nil {
		if st.req.SpendPlan != nil {
			return st.refuse(codeBadFlags, "spend plan requires a captured S5 execution")
		}
		return nil
	}
	return st.authorizeSpendPlan(ctx, dir, captured, rounds)
}

func (st *state) unreadableSpendCapture(dir, message string) *core.RefusalError {
	if st.req.SpendPlan != nil || panelcore.CheckExecution(dir, nil).Active {
		return st.refuse(codeRecordFailed, message)
	}
	return nil
}

func (st *state) authorizeSpendPlan(ctx context.Context, dir string, captured *config.RunState, rounds int) *core.RefusalError {
	phases, code := spend.CompletePlan(captured.Execution.ID, rounds, st.req.SpendPlan)
	if code != "" {
		return st.refuse(codeBadFlags, "spend plan has invalid or repeated launch attribution")
	}
	projection, code := spend.Project(captured.Execution.ID, phases)
	if code != "" {
		return st.refuse(codeBadFlags, "spend plan cannot be projected")
	}
	return st.authorizeProjection(ctx, dir, captured, rounds, phases, projection)
}

func (st *state) authorizeProjection(ctx context.Context, dir string, captured *config.RunState, rounds int,
	phases []spend.Phase, projection spend.Projection,
) *core.RefusalError {
	policy := st.spendPolicy(captured)
	if !spend.ValidLimit(policy.Budget) || !spend.ValidLimit(policy.Personal) || !spend.ValidLimit(policy.MonthlyCap) {
		return st.refuse(codeBadFlags, "spend guards must be finite nonnegative USD amounts")
	}
	// Serialize the MTD scan and the receipt append under the repo-wide
	// monthly lock: two concurrent runs in different bundles would
	// otherwise both see the same month-to-date and both authorize. The
	// lock is held through the receipt append for every return path.
	unlock, ref := st.holdMonthlyLock(ctx, policy)
	if ref != nil {
		return ref
	}
	defer unlock()
	if ref := st.authorizeMonthlyCap(projection, policy); ref != nil {
		return ref
	}
	return st.checkAndRecord(ctx, dir, captured, rounds, phases, projection, policy)
}

func (st *state) checkAndRecord(ctx context.Context, dir string, captured *config.RunState, rounds int,
	phases []spend.Phase, projection spend.Projection, policy spend.Policy,
) *core.RefusalError {
	authorization := spend.Authorize(projection, policy, st.req.ConsentOverBudget)
	if authorization.Code == "unsupported-cap" {
		return st.refuse(authorization.Code, "hard billing caps are unsupported — remove spend.enforce_cap")
	}
	if authorization.Code != "" {
		return st.refuse(authorization.Code, "S5 spend authorization required — pass run --consent-over-budget for this invocation")
	}
	return st.spendReceipt(ctx, dir, captured.Execution.ID, rounds, phases, projection, policy)
}

// acquireMonthlyLock takes the repo-wide monthly-spend lock guarding the
// month-to-date scan plus the receipt append. The lock is the file
// monthly-spend.lock inside the per-repo state directory; the directory is
// created when absent so a fresh repository still serializes, and holding it
// across the whole authorizeProjection path keeps the lock order strict:
// the repo-wide lock is always acquired before any bundle lock.
func (st *state) acquireMonthlyLock(ctx context.Context) (func(), *core.RefusalError) {
	stateDir := readcommon.StateDir(st.req.RepoRootPath())
	if err := os.MkdirAll(stateDir, stateDirMode); err != nil {
		return nil, st.refuse(codeRecordFailed, fmt.Sprintf("cannot lock monthly spend: %v", err))
	}
	unlock, err := storelock.AcquireContext(ctx, filepath.Join(stateDir, "monthly-spend"), storelock.DefaultWait)
	if err != nil {
		return nil, st.refuse(codeRecordFailed, fmt.Sprintf("cannot lock monthly spend: %v", err))
	}
	return unlock, nil
}

// holdMonthlyLock acquires the repo-wide lock when a monthly cap is active
// and returns its unlock; otherwise it returns a no-op so every return path
// releases the same way. Acquiring before any bundle lock keeps the order
// strict.
func (st *state) holdMonthlyLock(ctx context.Context, policy spend.Policy) (func(), *core.RefusalError) {
	if policy.MonthlyCap == "" {
		return func() {}, nil
	}
	return st.acquireMonthlyLock(ctx)
}

// authorizeMonthlyCap enforces the per-repo monthly spend ceiling against
// month-to-date recorded spend plus this run's projection. When the
// pending projection is unknown, month-to-date known spend alone is
// enforced: if it already exceeds the ceiling the run refuses rather
// than launching unpriced.
func (st *state) authorizeMonthlyCap(projection spend.Projection, policy spend.Policy) *core.RefusalError {
	if policy.MonthlyCap == "" {
		return nil
	}
	if !spend.ValidLimit(policy.MonthlyCap) {
		return st.refuse(codeBadFlags, "monthly spend ceiling must be a finite nonnegative USD amount")
	}
	now := st.now()
	repoRoot := st.req.RepoRootPath()
	mtd := spend.AggregateMTD(readcommon.StateDir(repoRoot), now)
	if mtd.Warning != "" {
		st.warn(mtd.Warning)
	}
	projected := projectedString(projection)
	projUnknown := projectionUnknown(projection, projected)
	if refusal := st.checkMonthlyUnknown(policy, mtd.UnknownCount > 0, projUnknown); refusal != nil {
		return refusal
	}
	combined := sumExact(mtd.KnownUSD, projected)
	return st.checkMonthlyExceeds(policy, mtd, projected, combined, projUnknown)
}

// projectionUnknown reports whether the pending run's price is not fully
// known: unknown phases, or no known projection amount at all (the
// production no-pricing path carries no known amount).
func projectionUnknown(projection spend.Projection, projected string) bool {
	return len(projection.Unknown) > 0 || projected == ""
}

func (st *state) checkMonthlyUnknown(policy spend.Policy, mtdUnknown, projUnknown bool) *core.RefusalError {
	if !policy.RequireKnown {
		return nil
	}
	if !mtdUnknown && !projUnknown {
		return nil
	}
	if st.req.ConsentOverBudget {
		return nil
	}
	culprit := "the pending projection has unknown phases"
	if mtdUnknown && projUnknown {
		culprit = "month-to-date has unknown-cost receipts and the pending projection has unknown phases"
	} else if mtdUnknown {
		culprit = "month-to-date has unknown-cost receipts"
	}
	msg := "monthly spend unknown — " + culprit +
		" — the operator settings require a known estimate — consent before this run starts or price the receipts"
	return st.refuse("budget-unknown", msg)
}

func (st *state) checkMonthlyExceeds(policy spend.Policy, mtd spend.MTD, projected, combined string, projUnknown bool) *core.RefusalError {
	if combined != "" && spend.Exceeds(combined, policy.MonthlyCap) && !st.req.ConsentOverBudget {
		return st.refuse("spend-capped", monthlyExceedsMessage(policy, mtd, projected, combined))
	}
	if mtd.UnknownCount > 0 || projUnknown {
		st.warnMonthlyUnknown(mtd, projUnknown)
	}
	return nil
}

// monthlyExceedsMessage names the known month-to-date spend, the
// effective ceiling, and — when the pending projection is unknown —
// that known spend alone already exceeded it.
func monthlyExceedsMessage(policy spend.Policy, mtd spend.MTD, projected, combined string) string {
	if projected == "" {
		return "monthly spend ceiling $" + policy.MonthlyCap + " exceeded — month-to-date " +
			"$" + displayOrZero(mtd.KnownUSD) + " exceeds it alone, with the pending projection unknown — " +
			"pass run --consent-over-budget for this invocation or raise monthly_budget_usd"
	}
	return "monthly spend ceiling $" + policy.MonthlyCap + " exceeded — month-to-date " +
		"$" + displayOrZero(mtd.KnownUSD) + " plus projected $" + displayOrZero(projected) +
		" = $" + combined + " — pass run --consent-over-budget for this invocation " +
		"or raise monthly_budget_usd"
}

func projectedString(p spend.Projection) string {
	if p.Total != nil {
		return strconv.FormatFloat(*p.Total, 'f', -1, floatBitSize)
	}
	if p.KnownSubtotal != nil {
		return strconv.FormatFloat(*p.KnownSubtotal, 'f', -1, floatBitSize)
	}
	return ""
}

// sumExact adds two exact decimals. An empty leg reads as zero when the
// other leg is known — the lower bound for just the known side — while
// two empty legs stay "" (nothing known at all): an unknown projection
// never hides known month-to-date spend.
func sumExact(a, b string) string {
	if a == "" && b == "" {
		return ""
	}
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	sumRat, okA := new(big.Rat).SetString(a)
	sumRatB, okB := new(big.Rat).SetString(b)
	if !okA || !okB {
		return ""
	}
	sumRat.Add(sumRat, sumRatB)
	return spend.FormatRat(sumRat)
}

func displayOrZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// warnMonthlyUnknown names the unknown legs it enforced against: the
// unknown-cost month-to-date receipts, the unknown pending projection,
// or both. The caller guarantees at least one is unknown.
func (st *state) warnMonthlyUnknown(mtd spend.MTD, projectionUnknown bool) {
	tail := " — enforced against known spend only ($" + displayOrZero(mtd.KnownUSD) + ")"
	if mtd.UnknownCount > 0 {
		msg := "monthly spend has " + strconv.Itoa(mtd.UnknownCount) + " unknown-cost receipt(s) in " + mtd.Month
		if projectionUnknown {
			msg += " and the pending projection is unknown"
		}
		st.warn(msg + tail)
		return
	}
	st.warn("monthly spend's pending projection is unknown" + tail)
}

func (st *state) spendPolicy(captured *config.RunState) spend.Policy {
	policy := capturedSpendPolicy(captured)
	if st.req.SpendPolicy != nil {
		current := *st.req.SpendPolicy
		current.Budget = policy.Budget
		current.MonthlyCap = policy.MonthlyCap
		policy = current
	}
	if value := st.req.Env["REVIEW_BUDGET_USD"]; value != "" {
		policy.Budget = value
	}
	return policy
}

func capturedSpendPolicy(captured *config.RunState) spend.Policy {
	policy := spend.Policy{}
	if captured.Resolved == nil {
		return policy
	}
	settings := captured.Resolved
	policy.RequireKnown = settings.Spend.RequireKnownEstimate
	policy.EnforceCap = settings.Spend.EnforceCap
	if settings.Spend.PersonalLimit != nil {
		policy.Personal = formatUSD(*settings.Spend.PersonalLimit)
	}
	policy.Budget = resolvedBudget(settings)
	policy.MonthlyCap = monthlyCapString(settings)
	return policy
}

// monthlyCapString is the enforced monthly literal: the effective
// (post-precedence) ceiling. The repository layer's display literal is
// used only when that cap is the effective value — a stricter personal
// cap must not be enforced at the looser displayed one.
func monthlyCapString(settings *config.Resolved) string {
	if settings.Spend.MonthlyBudgetUSD == nil {
		return ""
	}
	if repo := settings.Spend.MonthlyRepoCap; repo != nil && *repo == *settings.Spend.MonthlyBudgetUSD {
		if display := settings.Defaults.Values.MonthlyDisplay; display != "" {
			return display
		}
	}
	return formatUSD(*settings.Spend.MonthlyBudgetUSD)
}

func resolvedBudget(settings *config.Resolved) string {
	if settings.Defaults.Values.BudgetUSD == nil {
		return ""
	}
	if settings.Defaults.Values.BudgetDisplay != "" {
		return settings.Defaults.Values.BudgetDisplay
	}
	return formatUSD(*settings.Defaults.Values.BudgetUSD)
}

func (st *state) spendReceipt(ctx context.Context, dir, execution string, rounds int, phases []spend.Phase,
	projection spend.Projection, policy spend.Policy,
) *core.RefusalError {
	topology := jq.NewObj("execution_id", execution, "finders", 2, "rounds", rounds, "recipe", st.req.Recipe, "phases", phases)
	st.receiptID = execution + "@" + st.now().UTC().Format(time.RFC3339Nano)
	record := jq.NewObj("at", core.NowUTC(st.now()), "execution_id", execution, "scope", "invocation",
		"invocation_id", st.receiptID,
		"projection_sha256", shaHex([]byte(topology.MarshalCompact())), "topology", topology,
		"projected_usd", projection.Total, "projection", projection, "budget_usd", budgetNumber(policy.Budget),
		"personal_limit_usd", budgetNumber(policy.Personal), "require_known_estimate", policy.RequireKnown,
		"budget_src", st.budgetSource())
	if st.req.ConsentOverBudget {
		record.Set("consent", "--consent-over-budget")
	}
	return st.updateLedger(ctx, dir, func(ledger []any) []any { return append(ledger, record) })
}

// updateLedger rewrites the bundle's budget.json receipt ledger under its
// lock: the one writer run's spend receipt and the loop's spawn receipts
// share, so both obey the same cap, lock, and atomic write.
func (st *state) updateLedger(ctx context.Context, dir string, change func([]any) []any) *core.RefusalError {
	path := filepath.Join(dir, "budget.json")
	unlock, err := storelock.AcquireContext(ctx, path, storelock.DefaultWait)
	if err != nil {
		return st.refuse(codeRecordFailed, fmt.Sprintf("cannot lock spend receipts: %v", err))
	}
	defer unlock()
	ledger, message := spendLedger(dir)
	if message != "" {
		return st.refuse(codeRecordFailed, message)
	}
	ledger = change(ledger)
	raw := []byte(jq.PrettyOrdered(ledger) + "\n")
	if len(raw) > maxSpendReceiptBytes {
		return st.refuse(codeRecordFailed, "budget.json receipt ledger is full; re-fetch into a fresh bundle")
	}
	if err := atomic.WriteFileMode(path, raw, spendReceiptMode); err != nil {
		return st.refuse(codeRecordFailed, fmt.Sprintf("cannot record spend authorization: %v", err))
	}
	return nil
}

func budgetNumber(value string) any {
	if _, err := strconv.ParseFloat(value, floatBitSize); err != nil {
		return nil
	}
	return json.Number(value)
}

func spendLedger(dir string) ([]any, string) {
	raw, message := spendReceiptBytes(dir)
	if message != "" {
		return nil, message
	}
	if raw == nil {
		return []any{}, ""
	}
	value, err := jq.DecodeOrdered(raw)
	ledger, ok := value.([]any)
	if err != nil || !ok {
		return nil, "budget.json must remain a readable receipt ledger"
	}
	return ledger, ""
}

func spendReceiptBytes(dir string) ([]byte, string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "cannot open spend receipt directory"
	}
	defer func() { _ = root.Close() }()
	file, err := readcommon.OpenNoBlockIn(root, "budget.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, "cannot read spend receipts"
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "spend receipts must be a regular file"
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSpendReceiptBytes+1))
	if err != nil || len(raw) > maxSpendReceiptBytes {
		return nil, "spend receipts exceed read limit"
	}
	return raw, ""
}

func (st *state) budgetSource() string {
	if st.req.Env["REVIEW_BUDGET_USD"] != "" {
		return "REVIEW_BUDGET_USD"
	}
	return "captured run policy"
}
