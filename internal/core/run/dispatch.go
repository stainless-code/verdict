package run

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/spend"
)

// Dispatch launches the request's agent once with directive as its whole
// prompt: the same agent resolution (shipped rows plus the operator's prefs
// definitions), PATH probe, environment, working directory, and watchdog a
// review launch uses, but no review directive, no read-only restriction, and
// no artifact contract. The self-review loop's fix phase is the caller: the
// agent edits the working tree, so nothing in the bundle is asserted.
//
// A refusal is the one a review launch would report for the same process
// outcome (canceled, agent-spawn-failed, agent-timeout, agent-failed), so a
// caller surfaces the run verb's wording unchanged. With req.Print set the
// argv is resolved and returned without spawning anything.
func Dispatch(
	ctx context.Context, req *core.RunRequest, agents []config.PersonalAgent, directive string,
) ([]string, *core.RefusalError) {
	if ref := checkAgents(agents); ref != nil {
		return nil, ref
	}
	st := &state{req: req, warnings: append([]string{}, req.Warnings...), defs: agents}
	args, ref := st.dispatchArgs(ctx, directive)
	if ref != nil {
		return nil, ref
	}
	if req.Print {
		return args.argv, nil
	}
	return args.argv, st.dispatchSpawn(ctx, args)
}

// dispatchArgs resolves the agent, the watchdog budget, and the argv. The
// PATH probe is skipped for a render, which spawns nothing.
func (st *state) dispatchArgs(ctx context.Context, directive string) (*launchArgs, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, st.refuse(codeCanceled, msgCanceled)
	}
	def, ref := st.agent()
	if ref != nil {
		return nil, ref
	}
	timeout, ref := st.timeout()
	if ref != nil {
		return nil, ref
	}
	args := &launchArgs{def: def, timeout: timeout}
	if !st.req.Print {
		if _, ref := st.resolve(&args.def); ref != nil {
			return nil, ref
		}
	}
	args.argv = argvFor(&args.def, "", nil, st.req.PassThrough, directive)
	return args, nil
}

// dispatchSpawn runs the resolved argv under the watchdog. No artifact is
// asserted: a present, regular, fresh state makes the shared verdict answer
// for the process outcome alone.
func (st *state) dispatchSpawn(ctx context.Context, args *launchArgs) *core.RefusalError {
	res, err := st.spawn(ctx, args.argv, args.timeout)
	return st.verdict(ctx, args, res, err, artifactState{exists: true, regular: true, fresh: true})
}

// AuthorizeSpawn admits one launch run does not price — the self-review
// loop's review and fix spawns — and writes its receipt. Run authorizes a
// priced S5 execution itself; these launches carry no projection, so the
// repository's monthly ceiling is enforced against month-to-date known spend
// alone, and require_known_estimate refuses them exactly as it refuses an
// unpriced run.
//
// Admission appends one budget.json row for the launch (execution_id is
// launchID, projected_usd null) under the same monthly lock and ledger writer
// run's own spend receipt uses, so the month-to-date scan counts the spawn —
// as unknown spend until [PriceSpawn] records a reported amount. The row is
// dated after every recorded cost receipt already in the bundle, so the
// review's own cost.json can never read as covering a later spawn.
//
// dir is the fetched bundle: its run.json carries the effective ceiling after
// repository and personal precedence. A bundle without one falls back to the
// operator's personal policy. The returned warnings are the ones the check
// raised (an unreadable month-to-date scan, unknown-cost receipts).
func AuthorizeSpawn(ctx context.Context, req *core.RunRequest, dir, launchID string) ([]string, *core.RefusalError) {
	st := &state{req: req}
	policy, ref := st.spawnPolicy(dir)
	if ref != nil {
		return nil, ref
	}
	if !spend.ValidLimit(policy.MonthlyCap) {
		return nil, st.refuse(codeBadFlags, "monthly spend ceiling must be a finite nonnegative USD amount")
	}
	// The lock spans the scan and the append, as it does for run: two loops
	// must not both admit against the same month-to-date.
	unlock, ref := st.acquireMonthlyLock(ctx)
	if ref != nil {
		return st.warnings, ref
	}
	defer unlock()
	if ref := st.authorizeMonthlyCap(spend.Projection{}, policy); ref != nil {
		return st.warnings, ref
	}
	// Receipts carry whole seconds, so the comparison does too: a row a
	// fraction past the recorded second would print as that second.
	at := st.now().UTC().Truncate(time.Second)
	if through, ok := spend.RecordedThrough(dir); ok && !at.After(through) {
		at = through.Add(time.Second)
	}
	record := jq.NewObj("at", core.NowUTC(at), "execution_id", launchID, "scope", "spawn",
		"projected_usd", nil, "budget_usd", budgetNumber(policy.Budget),
		"personal_limit_usd", budgetNumber(policy.Personal), "require_known_estimate", policy.RequireKnown,
		"budget_src", st.budgetSource())
	return st.warnings, st.updateLedger(ctx, dir, func(ledger []any) []any { return append(ledger, record) })
}

// PriceSpawn records the agent-reported spend of one admitted spawn on the
// ledger row [AuthorizeSpawn] wrote for it, so the month-to-date scan counts
// the amount instead of an unknown. A row that is missing is left alone: the
// spawn stays unknown, never free.
func PriceSpawn(ctx context.Context, req *core.RunRequest, dir, launchID, usd string) *core.RefusalError {
	st := &state{req: req}
	if !spend.ValidLimit(usd) || usd == "" {
		return st.refuse(codeBadFlags, "reported spend must be a finite nonnegative USD amount")
	}
	return st.updateLedger(ctx, dir, func(ledger []any) []any {
		for _, row := range ledger {
			priceRow(row, launchID, usd)
		}
		return ledger
	})
}

// priceRow sets the reported amount on the ledger row the launch owns.
func priceRow(row any, launchID, usd string) {
	obj, ok := row.(*jq.Obj)
	if !ok {
		return
	}
	if id, _ := obj.Get("execution_id"); id == launchID {
		obj.Set("projected_usd", json.Number(usd))
		obj.Set("reported", "agent")
	}
}

// spawnPolicy is the policy one unpriced spawn is admitted under.
func (st *state) spawnPolicy(dir string) (spend.Policy, *core.RefusalError) {
	captured, _, err := config.ReadRunState(dir)
	if err != nil {
		return spend.Policy{}, st.refuse(codeRecordFailed, err.Error())
	}
	switch {
	case captured != nil && captured.Resolved != nil:
		return st.spendPolicy(captured), nil
	case st.req.SpendPolicy != nil:
		return *st.req.SpendPolicy, nil
	}
	return spend.Policy{}, nil
}

// personalFloatBits renders a personal spend amount without inventing digits.
const personalFloatBits = 64

// Personal reads the operator-defined agents and the personal spend policy
// from the prefs file only. A repository config can never define an agent:
// the typed repo parser refuses the key, and this read addresses only the
// XDG/HOME settings path. Every verb that launches an agent reads them here,
// so a settings.json agent resolves the same way everywhere.
func Personal(env map[string]string) ([]config.PersonalAgent, *spend.Policy, *core.RefusalError) {
	prefs, ref := config.LoadPersonal(config.SettingsPath(env["HOME"], env["XDG_CONFIG_HOME"]))
	if ref != nil {
		return nil, nil, ref
	}
	return prefs.Agents, personalPolicy(prefs.Spend), nil
}

func personalPolicy(settings config.PersonalSpend) *spend.Policy {
	if (settings == config.PersonalSpend{}) {
		return nil
	}
	policy := &spend.Policy{RequireKnown: settings.RequireKnownEstimate, EnforceCap: settings.EnforceCap}
	if settings.BudgetUSD != nil {
		policy.Personal = strconv.FormatFloat(*settings.BudgetUSD, 'f', -1, personalFloatBits)
	}
	if settings.MonthlyBudgetUSD != nil {
		policy.MonthlyCap = strconv.FormatFloat(*settings.MonthlyBudgetUSD, 'f', -1, personalFloatBits)
	}
	return policy
}
