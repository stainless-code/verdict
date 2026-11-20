package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/storelock"
)

const (
	maxSpendReceiptBytes = 1 << 20
	spendReceiptMode     = 0o600
)

func (st *state) authorizeSpend(dir string, rounds int) *core.RefusalError {
	if st.drainDigest != "" {
		return nil
	}
	captured, _, err := config.ReadRunState(dir)
	if err != nil || captured == nil || captured.Execution == nil {
		if st.req.SpendPlan != nil {
			return st.refuse(codeBadFlags, "spend plan requires a captured S5 execution")
		}
		return nil
	}
	phases, code := spend.CompletePlan(captured.Execution.ID, rounds, st.req.SpendPlan)
	if code != "" {
		return st.refuse(codeBadFlags, "spend plan has invalid or repeated launch attribution")
	}
	projection, code := spend.Project(captured.Execution.ID, phases)
	if code != "" {
		return st.refuse(codeBadFlags, "spend plan cannot be projected")
	}
	return st.authorizeProjection(dir, captured, rounds, phases, projection)
}

func (st *state) authorizeProjection(dir string, captured *config.RunState, rounds int,
	phases []spend.Phase, projection spend.Projection,
) *core.RefusalError {
	policy := st.spendPolicy(captured)
	authorization := spend.Authorize(projection, policy, st.req.ConsentOverBudget)
	if authorization.Code == "unsupported-cap" {
		return st.refuse(authorization.Code, "hard billing caps are unsupported — remove spend.enforce_cap")
	}
	if !spend.ValidLimit(policy.Budget) || !spend.ValidLimit(policy.Personal) {
		return st.refuse(codeBadFlags, "spend guards must be finite nonnegative USD amounts")
	}
	if authorization.Code != "" {
		return st.refuse(authorization.Code, "S5 spend authorization required — pass run --consent-over-budget for this invocation")
	}
	return st.spendReceipt(dir, captured.Execution.ID, rounds, phases, projection, policy)
}

func (st *state) spendPolicy(captured *config.RunState) spend.Policy {
	policy := capturedSpendPolicy(captured)
	if st.req.SpendPolicy != nil {
		current := *st.req.SpendPolicy
		current.Budget = policy.Budget
		policy = current
	}
	if value := st.req.Env["REVIEW_BUDGET_USD"]; value != "" {
		policy.Budget = value
	}
	return policy
}

func capturedSpendPolicy(captured *config.RunState) spend.Policy {
	policy := spend.Policy{}
	if captured.Resolved != nil {
		settings := captured.Resolved
		policy.RequireKnown = settings.Spend.RequireKnownEstimate
		policy.EnforceCap = settings.Spend.EnforceCap
		if settings.Spend.PersonalLimit != nil {
			policy.Personal = formatUSD(*settings.Spend.PersonalLimit)
		}
		policy.Budget = resolvedBudget(settings)
	}
	return policy
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

func (st *state) spendReceipt(dir, execution string, rounds int, phases []spend.Phase,
	projection spend.Projection, policy spend.Policy,
) *core.RefusalError {
	topology := jq.NewObj("execution_id", execution, "finders", 2, "rounds", rounds, "recipe", st.req.Recipe, "phases", phases)
	record := jq.NewObj("at", core.NowUTC(st.now()), "execution_id", execution, "scope", "invocation",
		"projection_sha256", shaHex([]byte(topology.MarshalCompact())), "topology", topology,
		"projected_usd", projection.Total, "projection", projection, "budget_usd", budgetNumber(policy.Budget),
		"personal_limit_usd", budgetNumber(policy.Personal), "require_known_estimate", policy.RequireKnown,
		"budget_src", st.budgetSource())
	if st.req.ConsentOverBudget {
		record.Set("consent", "--consent-over-budget")
	}
	path := filepath.Join(dir, "budget.json")
	unlock, err := storelock.Acquire(path, storelock.DefaultWait)
	if err != nil {
		return st.refuse(codeRecordFailed, fmt.Sprintf("cannot lock spend receipts: %v", err))
	}
	defer unlock()
	ledger, message := spendLedger(dir)
	if message != "" {
		return st.refuse(codeRecordFailed, message)
	}

	ledger = append(ledger, record)
	if err := atomic.WriteFileMode(path, []byte(jq.PrettyOrdered(ledger)+"\n"), spendReceiptMode); err != nil {
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
