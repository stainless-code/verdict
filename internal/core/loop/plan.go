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
// watchdog is the time left before the loop's deadline when it starts — split
// across the review's attempts — so a rendered --timeout is the most a spawn
// can get.
// The last round has no fix: nothing would review it.
func (lp *loop) plan(ctx context.Context) ([]PlanStep, *core.RefusalError) {
	// The agent resolves before any step is planned, so a one-round loop —
	// which plans no fix — still refuses an unknown agent before work starts.
	if _, ref := lp.fixArgv(ctx, 1, strconv.Itoa(lp.data.TimeoutS)); ref != nil {
		return nil, ref
	}
	steps := []PlanStep{}
	sandbox, ref := lp.reviewSandbox(ctx)
	if ref != nil {
		return nil, ref
	}
	timeout := strconv.Itoa(lp.data.TimeoutS)
	reviewTimeout := strconv.Itoa(max(1, (lp.data.TimeoutS+reviewAttempts-1)/reviewAttempts))
	for n := 1; n <= lp.req.MaxRounds; n++ {
		name := lp.name(n)
		local := localPrefix + name
		repo := lp.req.Repo
		steps = append(steps,
			PlanStep{Round: n, Phase: PhaseFetch, When: whenAlways, Argv: []string{verbName, "fetch-local", repo, name}},
			PlanStep{
				Round: n, Phase: PhaseReview, When: whenAlways, Argv: lp.reviewArgv(repo, local, reviewTimeout),
				Sandbox: sandbox,
			},
			PlanStep{Round: n, Phase: PhaseRecord, When: whenAlways, Argv: []string{
				verbName, "record", repo, local, "--require-coverage",
			}},
			PlanStep{Round: n, Phase: PhaseGate, When: whenAlways, Argv: []string{
				verbName, "gate", repo, local, "--max-severity", lp.req.MaxSeverity,
				"--coverage", gateCoverage, "--live", gateLive,
			}},
		)
		if n == lp.req.MaxRounds {
			continue
		}
		argv, ref := lp.fixArgv(ctx, n, timeout)
		if ref != nil {
			return nil, ref
		}
		steps = append(steps,
			PlanStep{Round: n, Phase: PhaseFix, When: whenGateFail, Argv: argv},
			PlanStep{Round: n, Phase: PhaseVerify, When: whenVerify, Argv: lp.verifyArgv(repo, local)},
		)
	}
	return steps, nil
}

// fixArgv resolves the fix agent's argv without spawning: the same agent
// resolution the spawn uses, so an unknown agent refuses before any work.
func (lp *loop) fixArgv(ctx context.Context, n int, timeout string) ([]string, *core.RefusalError) {
	b := lp.bundle(n)
	req := lp.req.Flow.RunRequest(b, reviewflow.Launch{Agent: lp.req.Agent, TimeoutRaw: timeout}, nil)
	req.PassThrough = lp.req.fixArgs()
	req.Print = true
	dir := filepath.Join(readcommon.StateDir(lp.req.RepoRoot), b.Pr)
	// The plan is the exact argv: the fix spawn requests the CLI's
	// structured cost output, as the real launch does.
	req.MeasureSpend = true
	out, ref := runcore.Dispatch(ctx, req, lp.req.Flow.Agents, lp.fixDirective(n, dir))
	return out.Argv, ref
}

// reviewArgv is the review step's CLI equivalent: one finder, the repair
// rounds, coverage required, and the operator's `--` tail when they passed one.
func (lp *loop) reviewArgv(repo, local, timeout string) []string {
	argv := []string{
		verbName, "run", repo, local, "--agent", lp.req.Agent, "--finders", findersOne,
		"--rounds", strconv.Itoa(reviewAttempts), "--timeout", timeout, "--require-coverage",
	}
	if lp.req.AllowUnsandboxedReview {
		argv = append(argv, "--allow-unsandboxed-review")
	}
	if len(lp.req.AgentArgs) > 0 {
		argv = append(append(argv, "--"), lp.req.AgentArgs...)
	}
	return argv
}

// reviewSandbox is the review step's OS-sandbox posture, as run's --print
// renders it: the same agent resolution and decision, nothing probed.
func (lp *loop) reviewSandbox(ctx context.Context) (*core.RunSandbox, *core.RefusalError) {
	b := lp.bundle(1)
	req := lp.req.Flow.RunRequest(b, reviewflow.Launch{Agent: lp.req.Agent}, nil)
	req.PassThrough = lp.req.AgentArgs
	req.AllowUnsandboxedReview, req.UnsandboxedOptOut = lp.req.AllowUnsandboxedReview, true
	return runcore.ReviewSandbox(ctx, req, lp.req.Flow.Agents)
}

// verifyArgv is the verify step's CLI equivalent, with its sandbox: the full
// one unless the operator opted out.
func (lp *loop) verifyArgv(repo, local string) []string {
	argv := []string{verbName, "verify", repo, local, "--timeout", strconv.Itoa(min(verifyCapS, lp.data.TimeoutS))}
	switch {
	case lp.req.VerifyNoSandbox:
	case lp.req.VerifySandboxNetworkOnly:
		argv = append(argv, "--sandbox-network-only")
	default:
		argv = append(argv, "--sandbox")
	}
	return argv
}
