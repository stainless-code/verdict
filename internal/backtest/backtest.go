// Package backtest is the backtest verb's CLI adapter: argv parsing, one call
// into the core mining, replay, score, or composed service, and envelope
// rendering. It is CLI-only on purpose — replay spends on model runs, so
// neither the MCP surface nor the SDK exposes it.
package backtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	corebacktest "github.com/stainless-code/verdict/internal/core/backtest"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/jq"
	runverb "github.com/stainless-code/verdict/internal/run"
)

// The shipped modes. `mine` is free and local; `replay` spawns the
// operator's own agent, one review per candidate; `score` is a pure read over
// a replay's bundles; `composed` is the bare `--since` form that runs all
// three in order.
const (
	modeMine     = "mine"
	modeReplay   = "replay"
	modeScore    = "score"
	modeComposed = "composed"
)

// Defaults the replay mode hands the core service: the same 300-second
// watchdog `run` gives one launch, one round, and a ten-review cap.
const (
	defaultTimeoutS   = "300"
	defaultRounds     = "1"
	defaultMaxReviews = "10"
	// defaultUntil is the miner's window end when --until is absent.
	defaultUntil = "HEAD"
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
	modeScore: "--run <id>, --json",
	modeComposed: "--since <90d|12w|18mo|2y|ref>, --until <ref>, --run <slug>, --agent <name>, " +
		"--max-reviews <n>, --budget-usd <usd>, --timeout <secs>, --rounds <n>, -- <agent args>, --json",
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
	switch f.mode {
	case modeReplay:
		return runReplay(ctx, r, req, f)
	case modeScore:
		return runScore(ctx, r, req, f)
	case modeComposed:
		return runComposed(ctx, r, req, f)
	default:
		return runMine(ctx, r, req, f)
	}
}

// runMine executes `backtest mine`: the SZZ candidate miner.
func runMine(ctx context.Context, r *cli.Runner, req Request, f *flags) int {
	resp, ref := corebacktest.Run(ctx, mineRequest(r, req, f))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	renderJSON(r, resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

// runReplay executes `backtest replay`: the candidate-by-candidate agent run.
func runReplay(ctx context.Context, r *cli.Runner, req Request, f *flags) int {
	resp, ref := corebacktest.RunReplay(ctx, replayRequest(r, req, f, f.run))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	renderJSON(r, resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

// runScore executes `backtest score`: the pure read over a replay's bundles.
func runScore(ctx context.Context, r *cli.Runner, req Request, f *flags) int {
	resp, ref := corebacktest.RunScore(ctx, scoreRequest(r, req, f.run))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	renderJSON(r, resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

// runComposed executes the bare `--since` form: mine, replay, then score, all
// through the same services the single-mode verbs call. The run id the miner
// resolved is the one replay and score read, so `--run` is optional here too.
func runComposed(ctx context.Context, r *cli.Runner, req Request, f *flags) int {
	if f.agent == "" {
		return r.RefuseTyped(&core.RefusalError{
			Code: "bad-flags",
			Message: "backtest --since runs mine, replay, and score, and replay needs --agent <name> (known: " +
				strings.Join(runcore.AgentNames(), ", ") + ")",
		})
	}
	mined, ref := corebacktest.Run(ctx, mineRequest(r, req, f))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = mined.Warnings
	run := mined.Data.Run
	replayed, ref := corebacktest.RunReplay(ctx, replayRequest(r, req, f, run))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = replayed.Warnings
	scored, ref := corebacktest.RunScore(ctx, scoreRequest(r, req, run))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = scored.Warnings
	renderJSON(r, scored.At, scored.Next, scored.Warnings, scored.Data.Wire())
	return cli.ExitOK
}

// mineRequest is one miner invocation.
func mineRequest(r *cli.Runner, req Request, f *flags) *corebacktest.Request {
	return &corebacktest.Request{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}, Commander: coreexec.Runner{MissingTool: true},
		},
		RepoRoot: req.RepoRoot, Mode: modeMine, Since: f.since, Until: f.until,
		Run: f.run, Warnings: append([]string{}, r.Warnings...),
	}
}

// replayRequest is one replay invocation for the run it is given. The
// commander is the same split-stream runner fetch-local uses, because the
// bundle build inside the service is a fetch-local build.
func replayRequest(r *cli.Runner, req Request, f *flags, run string) *corebacktest.ReplayRequest {
	return &corebacktest.ReplayRequest{
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
		Run:           run,
		Agent:         f.agent,
		MaxReviewsRaw: f.maxReviews,
		BudgetRaw:     f.budgetUSD,
		TimeoutRaw:    f.timeout,
		RoundsRaw:     f.rounds,
		SettingsPath:  cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		JSONMode:      r.JSONMode,
		Warnings:      append([]string{}, r.Warnings...),
	}
}

// scoreRequest is one scoring invocation for the run it is given.
func scoreRequest(r *cli.Runner, req Request, run string) *corebacktest.ScoreRequest {
	return &corebacktest.ScoreRequest{
		Runtime:  &core.Runtime{Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}},
		RepoRoot: req.RepoRoot, Run: run, Warnings: append([]string{}, r.Warnings...),
	}
}

// renderJSON writes the --json envelope a mode's data object belongs to.
func renderJSON(r *cli.Runner, at, next string, warnings []string, data *jq.Obj) {
	if !r.JSONMode {
		return
	}
	fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope("backtest", "", "", at, next, warnings, data, nil)))
}

// flags is the parsed surface of every mode. pass is everything after `--`,
// verbatim, and only the replay and composed modes accept one.
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

// parseFlags reads the positional mode and the flags that mode accepts. A
// leading token that is not a flag is the mode; omitting it is the composed
// invocation, which is resolved once --since is known.
func parseFlags(args []string) (*flags, *cli.UsageError) {
	f := &flags{until: defaultUntil, timeout: defaultTimeoutS, rounds: defaultRounds, maxReviews: defaultMaxReviews}
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
	if uerr := resolveMode(f); uerr != nil {
		return f, uerr
	}
	return f, nil
}

// checkMode accepts the shipped modes and the undecided no-mode form.
func checkMode(mode string) *cli.UsageError {
	switch mode {
	case "", modeMine, modeReplay, modeScore:
		return nil
	default:
		return usage("unknown mode '" + mode + "' for backtest (want mine, replay, or score)")
	}
}

// resolveMode names the bare `--since` invocation; without it a mode is
// required.
func resolveMode(f *flags) *cli.UsageError {
	if f.mode != "" {
		return nil
	}
	if f.since == "" {
		return usage("backtest needs a mode ('mine', 'replay', or 'score'), or --since to mine, replay, and score in one run")
	}
	f.mode = modeComposed
	return nil
}

// parseToken consumes one flag and its operand, returning the next index. A
// bare `--` ends the mode's own flags; everything after it is the operator's.
func parseToken(f *flags, args []string, i int) (int, *cli.UsageError) {
	a := args[i]
	if a == "--" {
		if !f.launches() {
			return i, f.unknown(a)
		}
		f.pass = append([]string{}, args[i+1:]...)
		return len(args), nil
	}
	if !strings.HasPrefix(a, "-") {
		return i, usage("unexpected argument '" + a + "' for " + f.label())
	}
	if next, ok, uerr := parseValueFlag(f, args, i); ok {
		return next, uerr
	}
	if f.launches() && (a == "--print") {
		return i, usage("backtest replay always launches: --print belongs to 'verdict run'")
	}
	return i, f.unknown(a)
}

// launches reports whether the invocation runs an agent: the agent arguments
// after `--` are accepted, and --print is run's escape hatch rather than a
// silently accepted no-op.
func (f *flags) launches() bool {
	return f.mode != modeMine && f.mode != modeScore
}

// label names the invocation in a usage error.
func (f *flags) label() string {
	if f.mode == "" {
		return "backtest"
	}
	return "backtest " + f.mode
}

// valueFlag is one flag that takes an operand, with the modes that accept it
// (empty for every mode).
type valueFlag struct {
	field       *string
	name        string
	placeholder string
	modes       []string
}

// valueFlags lists every mode's operand flags.
func (f *flags) valueFlags() []valueFlag {
	return []valueFlag{
		{&f.since, "--since", "<duration|ref>", []string{modeMine, modeComposed}},
		{&f.until, "--until", "<ref>", []string{modeMine, modeComposed}},
		{&f.run, "--run", "<id|slug>", nil},
		{&f.agent, "--agent", "<name>", []string{modeReplay, modeComposed}},
		{&f.maxReviews, "--max-reviews", "<n>", []string{modeReplay, modeComposed}},
		{&f.budgetUSD, "--budget-usd", "<usd>", []string{modeReplay, modeComposed}},
		{&f.timeout, "--timeout", "<secs>", []string{modeReplay, modeComposed}},
		{&f.rounds, "--rounds", "<n>", []string{modeReplay, modeComposed}},
	}
}

// parseValueFlag consumes one known operand flag for the active mode.
func parseValueFlag(f *flags, args []string, i int) (int, bool, *cli.UsageError) {
	for _, vf := range f.valueFlags() {
		if vf.name != args[i] || !f.accepts(vf) {
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

// accepts reports whether the active mode takes one operand flag. The
// undecided no-mode form accepts the union: its flags are what decide it.
func (f *flags) accepts(vf valueFlag) bool {
	if f.mode == "" || len(vf.modes) == 0 {
		return true
	}
	return slices.Contains(vf.modes, f.mode)
}

// unknown builds the per-mode unknown-flag usage error.
func (f *flags) unknown(flag string) *cli.UsageError {
	return usage("unknown flag '" + flag + "' for " + f.label() + " (want " + f.wanted() + ")")
}

// wanted names the flag list a usage error publishes for the active mode.
func (f *flags) wanted() string {
	if f.mode == "" {
		return wanted[modeComposed]
	}
	return wanted[f.mode]
}

// usage builds the exit-2 flag error.
func usage(msg string) *cli.UsageError {
	return &cli.UsageError{Message: msg}
}

// rentry rebuilds the re-run command after flag parsing: the flags the operator
// changed, json mode, and the pass-through tail after a bare `--`.
func rentry(f *flags, jsonMode bool) string {
	s := "verdict backtest"
	if f.mode != modeComposed {
		s += " " + f.mode
	}
	s += modeRentry(f)
	if jsonMode {
		s += " --json"
	}
	if f.mode != modeMine && f.mode != modeScore && len(f.pass) > 0 {
		s += " -- " + runverb.RenderCommand(f.pass)
	}
	return s
}

// modeRentry renders the flags the active mode accepted.
func modeRentry(f *flags) string {
	switch f.mode {
	case modeMine:
		return mineRentry(f)
	case modeScore:
		return scoreRentry(f)
	case modeComposed:
		return composedRentry(f)
	default:
		return replayRentry(f)
	}
}

// mineRentry renders the mining flags, skipping the ones left at their
// defaults.
func mineRentry(f *flags) string {
	s := ""
	if f.since != "" {
		s += " --since " + f.since
	}
	if f.until != "" && f.until != defaultUntil {
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

// scoreRentry renders the scoring flags: the run it grades.
func scoreRentry(f *flags) string {
	if f.run == "" {
		return ""
	}
	return " --run " + f.run
}

// composedRentry renders the window, then the replay plan the composed run
// applies to every candidate.
func composedRentry(f *flags) string {
	s := ""
	if f.since != "" {
		s += " --since " + f.since
	}
	if f.until != "" && f.until != defaultUntil {
		s += " --until " + f.until
	}
	return s + replayRentry(f)
}
