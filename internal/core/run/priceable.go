package run

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/agentusage"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/mcpconfig"
)

// codeBudgetUnpriceable refuses a cost-capped launch verdict could not price,
// before it spends anything.
const codeBudgetUnpriceable = "budget-unpriceable"

// msgPricePlanned names the planned way to price an agent verdict cannot,
// by its tracking issues: no flag or key exists for it yet.
const msgPricePlanned = "declaring a price or \"free\" per model is planned (STA-527/STA-543)"

// Unpriceable says why a launch of req's agent cannot be priced, "" when it
// can: its CLI reports its own cost (claude, opencode), or it reports token
// usage and runs a model the built-in price table carries (codex with its
// model named by -m/--model or its config.toml). A cost cap cannot be
// enforced over a launch it cannot price, so a capped run or loop refuses it
// before the first spawn. An unknown agent is the launch's own refusal, "".
func Unpriceable(req *core.RunRequest, agents []config.PersonalAgent) string {
	st := &state{req: req, defs: agents}
	def, ok := st.lookupAgent(req.Agent)
	if !ok {
		return ""
	}
	return unpriceable(req.Env, &def, req.PassThrough)
}

// unpriceable is Unpriceable for one resolved row and its `--` tail.
func unpriceable(env map[string]string, def *agentcatalog.Launch, passthrough []string) string {
	switch {
	case def.Usage == nil:
		return def.Name + " reports neither its cost nor token usage verdict can price"
	case spellsUsageFlag(def.Usage.Args, passthrough):
		return "your `--` arguments choose " + def.Name + "'s output format, so its cost is not measured"
	case def.Usage.Format != agentcatalog.UsageCodexJSONL:
		return ""
	}
	model, why := agentusage.CodexModelWhy(mcpconfig.CodexHome(env), slices.Concat(def.Args, passthrough))
	switch {
	case why != "":
		return def.Name + " reports only token usage and verdict cannot price it: " + why
	case !priced(model):
		return def.Name + "'s model " + model + " is not in verdict's built-in price table"
	}
	return ""
}

// priced reports whether the built-in price table carries model.
func priced(model string) bool {
	_, ok := agentcatalog.PriceFor(model)
	return ok
}

// spellsUsageFlag reports whether the operator's `--` tail names the row's
// structured-output flag, which drops verdict's request for it.
func spellsUsageFlag(usage, passthrough []string) bool {
	if len(usage) == 0 {
		return false
	}
	flag := usageFlag(usage)
	for _, arg := range passthrough {
		if name, _, _ := strings.Cut(arg, "="); name == flag {
			return true
		}
	}
	return false
}

// UnpriceableFix is the refusal's remedy for an agent it cannot price under
// a cap, bound names how the run stays bounded without the cap.
func UnpriceableFix(agent, bound string) string {
	fix := "review with an agent whose CLI reports its cost (claude, opencode) or codex on a priced model"
	if agent == nameCodex {
		fix = "name codex's model — `-- -m <model>`, or `model` in its config.toml — one the price table carries"
	}
	return fix + "; or drop the cap and " + bound + "; " + msgPricePlanned
}

// refuseUnpriceable refuses a run whose monthly ceiling requires a known
// estimate but whose agent launch cannot be priced: its spend would land in
// the month as unknown, so nothing could enforce the ceiling. Consent for the
// invocation accepts unknown spend, as it does for an unknown estimate.
func (st *state) refuseUnpriceable(monthlyCap string, requireKnown bool) *core.RefusalError {
	if monthlyCap == "" || !requireKnown || st.req.ConsentOverBudget {
		return nil
	}
	why := Unpriceable(st.req, st.defs)
	if why == "" {
		return nil
	}
	return st.refuse(codeBudgetUnpriceable, why+" — the monthly ceiling requires a known estimate, so "+
		"run refuses before launching anything: "+UnpriceableFix(st.req.Agent, "bound the run with --timeout, "+
		"or pass --consent-over-budget for this invocation"))
}
