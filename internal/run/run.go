// Package run is the run verb's CLI adapter: argv parsing (same usage text and
// exit 2), one call into the run core service, command rendering for --print,
// envelope rendering, and refusal-vs-cancel exit mapping. Domain logic — the
// agent table, the watchdog spawn, the artifact assertion, and the provenance
// record — lives in internal/core/run.
//
// `run` is CLI-only on purpose: it is not an MCP tool, a Go facade method,
// or a TypeScript client method. A tool that spawns another agent turns
// every MCP client into a recursive launcher of an unpriced local process,
// which is the opposite of the boundary the guardrail sentence draws (no
// daemon, no queue, no model selection); the code path that starts a process
// belongs to the operator's own terminal and CI, not to a tool call from
// inside a model's turn.
package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/budget"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/spend"
)

// Request carries the resolved run invocation.
type Request struct {
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// DefaultTimeoutS is the watchdog budget when --timeout is absent: the same
// default verify gives the commands it spawns.
const DefaultTimeoutS = "300"

// DefaultRounds is the launch-to-check round count when --rounds is absent:
// two, so a wrong quote or a missing hunk receipt always gets the one repair
// round the evidence contract is built around. An explicit "1" opts out.
const DefaultRounds = "2"

// roundsFlag, recipeFlag, and stageFlag are named once for the parser and the
// usage line.
const (
	roundsFlag     = "--rounds"
	recipeFlag     = "--recipe"
	stageFlag      = "--stage"
	findersFlag    = "--finders"
	budgetFlag     = "--budget"
	allowShellFlag = "--allow-shell"
)

// verbsWanted is the flag list the unknown-flag usage error names.
const verbsWanted = "--agent <name>, --recipe <slug>, --stage <n>, --timeout <secs>, --rounds <n>, " +
	"--finders <1|2>, --budget <" + budget.RequestAuto + "|" + budget.TierLight + "|" + budget.TierStandard + "|" +
	budget.TierDeep + ">, --consent-over-budget, --resume, --allow-shell, --print, -- <agent args>, --json"

// Run executes `run <owner/repo> <pr|local-name> --agent <name> [--recipe
// <slug>] [--stage <n>] [--timeout <secs>] [--rounds <n>] [--allow-shell]
// [--print] [-- <agent args>...] [--json]`, returning the process exit code. A
// non-zero agent exit is preserved as this process's exit code after the typed
// refusal is rendered, so CI surfaces the real failure.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = rentry(req, &flags, r.JSONMode)
	if code, done := r.Canceled(ctx, "run"); done {
		return code
	}
	agents, policy, ref := personalAgents(req.Env)
	if ref != nil {
		return refuse(r, nil, &core.RefusalError{Code: ref.Code, Message: ref.Message})
	}
	request := runRequest(r, req, &flags)
	request.SpendPolicy = policy
	resp, ref := runcore.WithAgents(ctx, request, agents)
	if ref != nil {
		addPermissionHint(ref, req, &flags)
		return refuse(r, resp, ref)
	}
	r.Warnings = resp.Warnings
	if flags.print && !r.JSONMode {
		fmt.Fprintln(r.Out, RenderCommand(resp.Data.Argv))
		return cli.ExitOK
	}
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope("run", resp.Repo, resp.Pr, resp.At,
			resp.Next, resp.Warnings, resp.Data.Wire(), nil)))
	}
	return cli.ExitOK
}

// personalAgents reads operator-defined agents from the prefs file only. A
// repository config can never define one: the typed repo parser refuses the
// key, and this read addresses only the XDG/HOME settings path.
func personalAgents(env map[string]string) ([]config.PersonalAgent, *spend.Policy, *core.RefusalError) {
	prefs, ref := config.LoadPersonal(config.SettingsPath(env["HOME"], env["XDG_CONFIG_HOME"]))
	if ref != nil {
		return nil, nil, ref
	}
	return prefs.Agents, personalPolicy(prefs.Spend), nil
}

// refuse renders a run refusal, preserving the agent's own exit code when the
// service reports one (only the agent-failed refusal carries it).
func refuse(r *cli.Runner, resp *core.RunResponse, ref *core.RefusalError) int {
	code := cli.ExitRefused
	if resp != nil && resp.Exit != 0 {
		code = resp.Exit
	}
	return r.RefuseExit(ref, code)
}

// runRequest injects the production commander (the verify watchdog), the PATH
// probe, the ordered process environment, and the runner's clock and progress
// observer into the typed service request.
func runRequest(r *cli.Runner, req Request, flags *flags) *core.RunRequest {
	root := req.Root
	return &core.RunRequest{
		Runtime: r.Read(core.ReadRequest{Runtime: &core.Runtime{Env: req.Env}}, coreexec.Watchdog{}).Runtime,
		Repo:    req.Repo, Pr: req.Pr, Root: &root, RepoRoot: req.RepoRoot,
		LookPath:          exec.LookPath,
		PassThrough:       flags.pass,
		Environ:           os.Environ(),
		Agent:             flags.agent,
		Recipe:            flags.recipe,
		StageRaw:          flags.stage,
		TimeoutRaw:        flags.timeout,
		RoundsRaw:         requestedRounds(flags),
		FindersRaw:        flags.finders,
		BudgetRaw:         flags.budget,
		Print:             flags.print,
		Resume:            flags.resume,
		AllowShell:        flags.allowShell,
		ConsentOverBudget: flags.consent,
		JSONMode:          r.JSONMode,
		Warnings:          append([]string{}, r.Warnings...),
	}
}

// requestedRounds is the round count the service should act on: the operator's
// value when they named one, and empty otherwise, which leaves the service's
// own default in charge. The parsed count is never empty — it carries the
// default for the re-run line — so this is the only place the two differ.
func requestedRounds(f *flags) string {
	if !f.roundsSet {
		return ""
	}
	return f.rounds
}

// flags is the parsed run surface. pass is everything after `--`, verbatim.
// roundsSet records whether the operator named a count: the parsed value keeps
// the default so the re-run line can omit it, while the service has to know a
// render was not asked for a loop it cannot run.
type flags struct {
	finders    string
	budget     string
	agent      string
	recipe     string
	stage      string
	timeout    string
	rounds     string
	pass       []string
	consent    bool
	print      bool
	resume     bool
	allowShell bool
	roundsSet  bool
}

// parseFlags strips run's flags, reporting unknown ones. Every argument after a
// bare `--` is the operator's business and is passed through unchanged.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{timeout: DefaultTimeoutS, rounds: DefaultRounds}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			f.pass = append([]string{}, args[i+1:]...)
			return f, 0, false
		}
		next, code, done := parseToken(r, &f, args, i)
		if done {
			return f, code, true
		}
		i = next
	}
	return f, 0, false
}

// parseToken consumes one run argument, returning the next index plus a usage
// refusal when the token is unknown or a value flag lost its operand.
func parseToken(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	switch a {
	case "--agent", recipeFlag:
		return parseNameFlag(r, f, args, i)
	case findersFlag:
		return parseFindersFlag(r, f, args, i)
	case budgetFlag:
		return parseBudgetFlag(r, f, args, i)
	case stageFlag:
		return parseStageFlag(r, f, args, i)
	case "--timeout", roundsFlag:
		return parseCountedFlag(r, f, args, i)
	case "--consent-over-budget":
		f.consent = true
		return i, 0, false
	case "--resume":
		f.resume = true
		return i, 0, false
	case allowShellFlag:
		f.allowShell = true
		return i, 0, false
	case "--print":
		f.print = true
		return i, 0, false
	default:
		return i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for run (want %s)", a, verbsWanted)}), true
	}
}

// parseNameFlag consumes one --agent or --recipe value flag, which name the
// agent process and the pipeline it follows.
func parseNameFlag(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	operand, dst := "<name>", &f.agent
	if a == recipeFlag {
		operand, dst = "<slug>", &f.recipe
	}
	v, next, u := cli.FlagValue(args, i, a, operand)
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	*dst = v
	return next, 0, false
}

// parseStageFlag consumes the --stage operand, left raw here so the service can
// refuse a bad index typed, beside the plan it resolves against. An empty
// operand is the missing-value usage error, not an absent flag: the service
// reads "" as "render the whole pipeline", which is the one command --stage
// exists to avoid.
func parseStageFlag(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, stageFlag, "<n>")
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	if v == "" {
		return i, r.ReportUsage(cli.MissingValue(stageFlag, "<n>")), true
	}
	f.stage = v
	return next, 0, false
}

// parseCountedFlag consumes one --timeout or --rounds value flag, leaving the
// other count at its default when the operand is empty.
func parseCountedFlag(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	operand := "<secs>"
	if a == "--rounds" {
		operand = "<n>"
	}
	v, next, u := cli.FlagValue(args, i, a, operand)
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	if v != "" {
		if a == roundsFlag {
			f.rounds = v
			f.roundsSet = true
		} else {
			f.timeout = v
		}
	}
	return next, 0, false
}

// rentry rebuilds the exact re-run command after flag parsing. The
// pass-through tail is not reproduced: its quoting belongs to whatever shell
// the operator used.
func rentry(req Request, f *flags, jsonMode bool) string {
	s := "verdict run " + req.Repo + argSep + req.Pr + flagTail(f)
	if f.consent {
		s += " --consent-over-budget"
	}
	if jsonMode {
		s += " --json"
	}
	return s
}

// flagTail rebuilds the flags the operator typed, each one only when the
// invocation actually asked for it. The stage operand is quoted: it reaches
// this line exactly as typed — a refusal names the value it could not read —
// and the line exists to be pasted back into a shell.
func flagTail(f *flags) string {
	s := findersTail(f.finders) + budgetTail(f.budget) + resumeTail(f) + allowShellTail(f)
	if f.agent != "" {
		s += " --agent " + f.agent
	}
	if f.recipe != "" {
		s += " --recipe " + f.recipe
	}
	if f.timeout != DefaultTimeoutS {
		s += " --timeout " + f.timeout
	}
	if f.rounds != DefaultRounds && f.rounds != "" {
		s += " --rounds " + f.rounds
	}
	if f.print {
		s += " --print"
	}
	if f.stage != "" {
		s += " --stage " + quoteArg(f.stage)
	}
	return s
}

// shellSafeChars are the argument bytes a POSIX shell passes through unquoted:
// the characters no shell metacharacter uses.
const shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@%+,"

// argSep is the single space every rendered command joins its parts with.
const argSep = " "

// RenderCommand renders one argv as a POSIX shell command line, quoting every
// argument that is not already safe. It is what --print writes: the exact
// command the launcher would spawn, copy-pasteable into sh/bash/zsh, which is
// also what the tests compare byte-for-byte with the argv the child received.
func RenderCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, argSep)
}

// quoteArg single-quotes one argument when it carries anything a shell would
// interpret, closing the quote around an embedded single quote.
func quoteArg(arg string) string {
	if arg != "" && strings.Trim(arg, shellSafeChars) == "" {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func addPermissionHint(ref *core.RefusalError, req Request, f *flags) {
	if ref.Code != "no-review-artifact" || len(f.pass) != 0 {
		return
	}
	row, ok := agentcatalog.Lookup(f.agent)
	if !ok || row.Launch == nil || row.Launch.PermissionHint == "" {
		return
	}
	command := RenderCommand([]string{"verdict", "run", req.Repo, req.Pr, "--agent", f.agent})
	command += findersTail(f.finders)
	ref.Message += "\nre-run with: " + command + " -- " + row.Launch.PermissionHint
}

const personalFloatBits = 64

func personalPolicy(settings config.PersonalSpend) *spend.Policy {
	if (settings == config.PersonalSpend{}) {
		return nil
	}
	policy := &spend.Policy{RequireKnown: settings.RequireKnownEstimate, EnforceCap: settings.EnforceCap}
	if settings.BudgetUSD != nil {
		policy.Personal = strconv.FormatFloat(*settings.BudgetUSD, 'f', -1, personalFloatBits)
	}
	return policy
}

func parseFindersFlag(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	value, next, usage := cli.FlagValue(args, i, findersFlag, "<1|2>")
	if usage != nil {
		return i, r.ReportUsage(usage), true
	}
	if value == "" {
		return i, r.ReportUsage(cli.MissingValue(findersFlag, "<1|2>")), true
	}
	f.finders = value
	return next, 0, false
}

// resumeTail renders --resume for the re-entry line when the invocation asked
// to continue an execution.
func resumeTail(f *flags) string {
	if !f.resume {
		return ""
	}
	return " --resume"
}

// allowShellTail renders --allow-shell for the re-entry line when the
// invocation opted out of the discovery restriction.
func allowShellTail(f *flags) string {
	if !f.allowShell {
		return ""
	}
	return " " + allowShellFlag
}

func findersTail(raw string) string {
	if raw == "" {
		return ""
	}
	return " --finders " + quoteArg(raw)
}

// parseBudgetFlag consumes --budget, refusing an unknown depth here rather than
// in the service: the vocabulary is fixed, so the refusal is a usage error that
// lands before any bundle is read and before any process could start.
func parseBudgetFlag(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	value, next, usage := cli.FlagValue(args, i, budgetFlag, "<"+budget.Vocabulary()+">")
	if usage != nil {
		return i, r.ReportUsage(usage), true
	}
	if !budget.ValidRequest(value) {
		return i, r.ReportUsage(&cli.UsageError{Message: budgetFlag + " must be " + budget.Vocabulary()}), true
	}
	f.budget = value
	return next, 0, false
}

func budgetTail(raw string) string {
	if raw == "" {
		return ""
	}
	return argSep + budgetFlag + argSep + quoteArg(raw)
}
