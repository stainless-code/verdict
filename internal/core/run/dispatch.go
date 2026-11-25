package run

import (
	"context"
	"strconv"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
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

// MonthlyCeiling refuses a launch the repository's monthly spend ceiling
// already forbids. Run authorizes a priced S5 execution itself; a launch it
// does not price — the self-review loop's review and fix spawns — calls this
// before spawning, so the ceiling binds every spend the loop starts. The
// pending launch is unpriced, so month-to-date known spend is enforced alone,
// and require_known_estimate refuses it exactly as it refuses an unpriced run.
//
// dir is the fetched bundle: its run.json carries the effective ceiling after
// repository and personal precedence. A bundle without one falls back to the
// operator's personal policy. The returned warnings are the ones the check
// raised (an unreadable month-to-date scan, unknown-cost receipts).
func MonthlyCeiling(ctx context.Context, req *core.RunRequest, dir string) ([]string, *core.RefusalError) {
	st := &state{req: req}
	captured, _, err := config.ReadRunState(dir)
	if err != nil {
		return nil, st.refuse(codeRecordFailed, err.Error())
	}
	policy := spend.Policy{}
	switch {
	case captured != nil && captured.Resolved != nil:
		policy = st.spendPolicy(captured)
	case req.SpendPolicy != nil:
		policy = *req.SpendPolicy
	}
	unlock, ref := st.holdMonthlyLock(ctx, policy)
	if ref != nil {
		return st.warnings, ref
	}
	defer unlock()
	return st.warnings, st.authorizeMonthlyCap(spend.Projection{}, policy)
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
