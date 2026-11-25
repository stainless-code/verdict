package loopcore

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Plan conditions: a fix runs only after a failed gate, and verify only when
// the fix agent declared a command.
const (
	whenAlways   = "always"
	whenGateFail = "gate-failed"
	whenVerify   = "verify-cmd-declared"
)

// plan renders every command the loop can run, round by round, as its CLI
// equivalent: the composed verbs run in-process with exactly these options,
// and the fix phase is the agent argv itself, prompt included. A spawn's
// watchdog is the wall budget that remains when it starts, so the rendered
// --timeout is the whole budget, the most any spawn can get. The last round
// has no fix: nothing would review it.
func (lp *loop) plan(ctx context.Context) ([]PlanStep, *core.RefusalError) {
	steps := []PlanStep{}
	budget := strconv.Itoa(lp.data.BudgetS)
	for n := 1; n <= lp.req.MaxRounds; n++ {
		name := lp.name(n)
		local := localPrefix + name
		repo := lp.req.Repo
		steps = append(steps,
			PlanStep{Round: n, Phase: PhaseFetch, When: whenAlways, Argv: []string{verbName, "fetch-local", repo, name}},
			PlanStep{Round: n, Phase: PhaseReview, When: whenAlways, Argv: []string{
				verbName, "run", repo, local, "--agent", lp.req.Agent, "--finders", findersOne, "--timeout", budget,
			}},
			PlanStep{Round: n, Phase: PhaseRecord, When: whenAlways, Argv: []string{
				verbName, "record", repo, local, "--require-coverage",
			}},
			PlanStep{Round: n, Phase: PhaseGate, When: whenAlways, Argv: []string{
				verbName, "gate", repo, local, "--max-severity", gateSeverity,
				"--coverage", gateCoverage, "--live", gateLive,
			}},
		)
		if n == lp.req.MaxRounds {
			continue
		}
		argv, ref := lp.fixArgv(ctx, n, budget)
		if ref != nil {
			return nil, ref
		}
		steps = append(steps,
			PlanStep{Round: n, Phase: PhaseFix, When: whenGateFail, Argv: argv},
			PlanStep{Round: n, Phase: PhaseVerify, When: whenVerify, Argv: []string{
				verbName, "verify", repo, local, "--timeout", strconv.Itoa(min(verifyCapS, lp.data.BudgetS)),
			}},
		)
	}
	return steps, nil
}

// fixArgv resolves the fix agent's argv without spawning: the same agent
// resolution the spawn uses, so an unknown agent refuses before any work.
func (lp *loop) fixArgv(ctx context.Context, n int, budget string) ([]string, *core.RefusalError) {
	b := lp.bundle(n)
	req := lp.req.Flow.RunRequest(b, reviewflow.Launch{Agent: lp.req.Agent, TimeoutRaw: budget}, nil)
	req.Print = true
	dir := filepath.Join(readcommon.StateDir(lp.req.RepoRoot), b.Pr)
	return runcore.Dispatch(ctx, req, lp.req.Flow.Agents, lp.fixDirective(n, dir))
}
