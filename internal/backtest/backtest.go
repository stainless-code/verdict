// Package backtest is the backtest verb's CLI adapter: argv parsing, one call
// into the core mining or replay service, and envelope rendering. It is
// CLI-only on purpose — replay spends on model runs, so neither the MCP
// surface nor the SDK exposes it.
package backtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	corebacktest "github.com/stainless-code/verdict/internal/core/backtest"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	runverb "github.com/stainless-code/verdict/internal/run"
)

// The two shipped modes. `mine` is free and local; `replay` spawns the
// operator's own agent, one review per candidate.
const (
	modeMine   = "mine"
	modeReplay = "replay"
)

// Defaults the replay mode hands the core service: the same 300-second
// watchdog `run` gives one launch, one round, and a ten-review cap.
const (
	defaultTimeoutS   = "300"
	defaultRounds     = "1"
	defaultMaxReviews = "10"
)

// Request carries the resolved invocation.
type Request struct {
	Env      map[string]string
	Root     string
	RepoRoot string
}

// wanted names the flag list a per-mode usage error publishes.
var wanted = map[string]string{
	modeMine: "--since <90d|12w|18mo|2y|ref>, --until <ref>, --run <slug>, --json",
	modeReplay: "--run <id>, --agent <name>, --max-reviews <n>, --budget-usd <usd>, " +
		"--timeout <secs>, --rounds <n>, -- <agent args>, --json",
}

// Run executes one `backtest <mode>` invocation.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	f, uerr := parseFlags(args)
	if uerr != nil {
		return r.ReportUsage(uerr)
	}
	r.Rentry = rentry(f, r.JSONMode)
	if code, done := r.Canceled(ctx, "backtest"); done {
		return code
	}
	if f.mode == modeReplay {
		return runReplay(ctx, r, req, f)
	}
	return runMine(ctx, r, req, f)
}

// runMine executes `backtest mine`: the SZZ candidate miner.
func runMine(ctx context.Context, r *cli.Runner, req Request, f *flags) int {
	resp, ref := corebacktest.Run(ctx, &corebacktest.Request{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}, Commander: coreexec.Runner{MissingTool: true},
		},
		RepoRoot: req.RepoRoot, Mode: f.mode, Since: f.since, Until: f.until,
		Run: f.run, Warnings: append([]string{}, r.Warnings...),
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope("backtest", "", "", resp.At,
			resp.Next, resp.Warnings, resp.Data.Wire(), nil)))
	}
	return cli.ExitOK
}

// runReplay executes `backtest replay`: the candidate-by-candidate agent run.
// The commander is the same split-stream runner fetch-local uses, because the
// bundle build inside the service is a fetch-local build.
func runReplay(ctx context.Context, r *cli.Runner, req Request, f *flags) int {
	resp, ref := corebacktest.RunReplay(ctx, &corebacktest.ReplayRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
			PackFS:    skillfs.FS,
		},
		LookPath:      exec.LookPath,
		Effective:     r.Effective,
		PassThrough:   f.pass,
		Environ:       os.Environ(),
		RepoRoot:      req.RepoRoot,
		Root:          req.Root,
		Run:           f.run,
		Agent:         f.agent,
		MaxReviewsRaw: f.maxReviews,
		BudgetRaw:     f.budgetUSD,
		TimeoutRaw:    f.timeout,
		RoundsRaw:     f.rounds,
		SettingsPath:  cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		JSONMode:      r.JSONMode,
		Warnings:      append([]string{}, r.Warnings...),
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope("backtest", "", "", resp.At,
			resp.Next, resp.Warnings, resp.Data.Wire(), nil)))
	}
	return cli.ExitOK
}

// flags is the parsed surface of both modes. pass is everything after `--`,
// verbatim, and only the replay mode accepts one.
type flags struct {
	mode       string
	since      string
	until      string
	run        string
	agent      string
	timeout    string
	rounds     string
	maxReviews string
	budgetUSD  string
	pass       []string
}

// parseFlags reads the positional mode and the flags that mode accepts.
func parseFlags(args []string) (*flags, *cli.UsageError) {
	f := &flags{until: "HEAD", timeout: defaultTimeoutS, rounds: defaultRounds, maxReviews: defaultMaxReviews}
	i := 0
	if i < len(args) && !strings.HasPrefix(args[i], "-") {
		f.mode = args[i]
		i++
	}
	if uerr := checkMode(f.mode); uerr != nil {
		return f, uerr
	}
	for ; i < len(args); i++ {
		next, uerr := parseToken(f, args, i)
		if uerr != nil {
			return f, uerr
		}
		i = next
	}
	return f, nil
}

// checkMode accepts the shipped modes.
func checkMode(mode string) *cli.UsageError {
	if mode == modeMine || mode == modeReplay {
		return nil
	}
	if mode == "" {
		return usage("backtest needs a mode ('mine' or 'replay')")
	}
	return usage("unknown mode '" + mode + "' for backtest (want mine or replay)")
}

// parseToken consumes one flag and its operand, returning the next index. A
// bare `--` ends replay's own flags; everything after it is the operator's.
func parseToken(f *flags, args []string, i int) (int, *cli.UsageError) {
	a := args[i]
	if a == "--" {
		if f.mode != modeReplay {
			return i, f.unknown(a)
		}
		f.pass = append([]string{}, args[i+1:]...)
		return len(args), nil
	}
	if !strings.HasPrefix(a, "-") {
		return i, usage("unexpected argument '" + a + "' for backtest " + f.mode)
	}
	if next, ok, uerr := parseValueFlag(f, args, i); ok {
		return next, uerr
	}
	if f.mode == modeReplay && (a == "--print") {
		return i, usage("backtest replay always launches: --print belongs to 'verdict run'")
	}
	return i, f.unknown(a)
}

// valueFlag is one flag that takes an operand, with the one mode that accepts
// it ("" for both).
type valueFlag struct {
	field       *string
	name        string
	placeholder string
	mode        string
}

// valueFlags lists both modes' operand flags.
func (f *flags) valueFlags() []valueFlag {
	return []valueFlag{
		{&f.since, "--since", "<duration|ref>", modeMine},
		{&f.until, "--until", "<ref>", modeMine},
		{&f.run, "--run", "<id|slug>", ""},
		{&f.agent, "--agent", "<name>", modeReplay},
		{&f.maxReviews, "--max-reviews", "<n>", modeReplay},
		{&f.budgetUSD, "--budget-usd", "<usd>", modeReplay},
		{&f.timeout, "--timeout", "<secs>", modeReplay},
		{&f.rounds, "--rounds", "<n>", modeReplay},
	}
}

// parseValueFlag consumes one known operand flag for the active mode.
func parseValueFlag(f *flags, args []string, i int) (int, bool, *cli.UsageError) {
	for _, vf := range f.valueFlags() {
		if vf.name != args[i] || (vf.mode != "" && vf.mode != f.mode) {
			continue
		}
		v, next, uerr := cli.FlagValue(args, i, vf.name, vf.placeholder)
		if uerr != nil {
			return i, true, uerr
		}
		*vf.field = v
		return next, true, nil
	}
	return i, false, nil
}

// unknown builds the per-mode unknown-flag usage error.
func (f *flags) unknown(flag string) *cli.UsageError {
	return usage("unknown flag '" + flag + "' for backtest " + f.mode + " (want " + wanted[f.mode] + ")")
}

// usage builds the exit-2 flag error.
func usage(msg string) *cli.UsageError {
	return &cli.UsageError{Message: msg}
}

// rentry rebuilds the re-run command after flag parsing: the flags the operator
// changed, json mode, and replay's pass-through tail after the bare `--`.
func rentry(f *flags, jsonMode bool) string {
	s := "verdict backtest " + f.mode + modeRentry(f)
	if jsonMode {
		s += " --json"
	}
	if f.mode == modeReplay && len(f.pass) > 0 {
		s += " -- " + runverb.RenderCommand(f.pass)
	}
	return s
}

// modeRentry renders the flags the active mode accepted.
func modeRentry(f *flags) string {
	if f.mode == modeMine {
		return mineRentry(f)
	}
	return replayRentry(f)
}

// mineRentry renders the mining flags, skipping the ones left at their
// defaults.
func mineRentry(f *flags) string {
	s := ""
	if f.since != "" {
		s += " --since " + f.since
	}
	if f.until != "" && f.until != "HEAD" {
		s += " --until " + f.until
	}
	if f.run != "" {
		s += " --run " + f.run
	}
	return s
}

// replayRentry renders the replay flags, skipping the ones left at their
// defaults.
func replayRentry(f *flags) string {
	s := ""
	if f.run != "" {
		s += " --run " + f.run
	}
	if f.agent != "" {
		s += " --agent " + f.agent
	}
	if f.maxReviews != defaultMaxReviews {
		s += " --max-reviews " + f.maxReviews
	}
	if f.budgetUSD != "" {
		s += " --budget-usd " + f.budgetUSD
	}
	if f.timeout != defaultTimeoutS {
		s += " --timeout " + f.timeout
	}
	if f.rounds != defaultRounds {
		s += " --rounds " + f.rounds
	}
	return s
}
