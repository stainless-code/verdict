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
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/argvshape"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/budget"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
)

// DefaultTimeoutS is the watchdog budget when --timeout is absent: the same
// default verify gives the commands it spawns.
const DefaultTimeoutS = runcore.DefaultTimeoutS

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
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = rentry(&req, &flags, r.JSONMode)
	if code, done := r.Canceled(ctx, verbRun); done {
		return code
	}
	agents, policy, ref := personalAgents(req.Env)
	if ref != nil {
		return refuse(r, nil, &core.RefusalError{Code: ref.Code, Message: core.Msgf("%s", core.Trusted(ref.Message))})
	}
	request := runRequest(r, &req, &flags)
	request.SpendPolicy = policy
	resp, ref := runcore.WithAgents(ctx, request, agents)
	if ref != nil {
		return refuseRun(r, &req, &flags, request, resp, ref)
	}
	r.Warnings = resp.Warnings
	if flags.print && !r.JSONMode {
		fmt.Fprintln(r.Out, RenderCommand(resp.Data.Argv))
		return cli.ExitOK
	}
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope(verbRun, resp.Repo, resp.Pr, resp.At,
			resp.Next, resp.Warnings, resp.Data.Wire(), nil)))
	}
	return cli.ExitOK
}

// personalAgents reads operator-defined agents from the prefs file only. A
// repository config can never define one: the typed repo parser refuses the
// key, and this read addresses only the XDG/HOME settings path.
func personalAgents(env map[string]string) ([]config.PersonalAgent, *spend.Policy, *core.RefusalError) {
	return runcore.Personal(env)
}

// codeAgentFailed is the only refusal that carries the agent's exit code out
// as the process exit; every other refusal exits with verdict's own code.
const codeAgentFailed = "agent-failed"

// verbRun is the verb's name in its envelope and its re-run lines.
const verbRun = "run"

// refuseRun renders the service's refusal with the re-entry line that answers
// it ([refusalReentry]).
func refuseRun(r *cli.Runner, req *readcommon.Request, f *flags, request *core.RunRequest,
	resp *core.RunResponse, ref *core.RefusalError,
) int {
	r.Rentry = refusalReentry(req, f, r.JSONMode, request.Environ, resp, ref)
	addPermissionHint(ref, req, f)
	return refuse(r, resp, ref)
}

// codesRefusingResume are the refusals that say --resume cannot continue the
// execution, so the line that answers them starts a fresh one.
var codesRefusingResume = []string{"resume-drift", "cannot-resume"}

// refusalReentry is the re-entry line a refusal prints: with --resume among
// verdict's own flags for an execution the refusal says to continue, without
// it for a --resume the refusal says cannot continue, and as typed otherwise.
func refusalReentry(req *readcommon.Request, f *flags, jsonMode bool, environ []string,
	resp *core.RunResponse, ref *core.RefusalError,
) string {
	resume := f.resume
	switch {
	case resp != nil && resp.Resumable:
		resume = true
	case slices.Contains(codesRefusingResume, ref.Code):
		resume = false
	}
	return reentry(req, f, jsonMode, resume, environ)
}

// refuse renders a run refusal, preserving the agent's own exit code when the
// service reports one (only the agent-failed refusal carries it).

func refuse(r *cli.Runner, resp *core.RunResponse, ref *core.RefusalError) int {
	code := cli.ExitRefused
	if resp != nil && resp.Exit != 0 && ref.Code == codeAgentFailed {
		code = resp.Exit
	}
	return r.RefuseExit(ref, code)
}

// runRequest injects the production commander (the verify watchdog), the PATH
// probe, the ordered process environment, and the runner's clock and progress
// observer into the typed service request.
func runRequest(r *cli.Runner, req *readcommon.Request, flags *flags) *core.RunRequest {
	return &core.RunRequest{
		Runtime: r.Read(*req, coreexec.Watchdog{}).Runtime,
		Repo:    req.Repo, Pr: req.Pr, Root: req.Root, RepoRoot: req.RepoRoot,
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
		return i, r.ReportUsage(&cli.UsageError{Message: core.Msgf(
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

// rentry rebuilds the exact re-run command after flag parsing.
func rentry(req *readcommon.Request, f *flags, jsonMode bool) string {
	return reentry(req, f, jsonMode, f.resume, os.Environ())
}

// redactedNote tells the operator what a redacted agent argument needs: the
// line stays pasteable, since a shell reads the note as a comment.
const redactedNote = "  # replace each '" + argvshape.RedactedValue + "' with the agent argument you passed"

// reentry renders the re-run command as argv through [RenderCommand], so no
// operand the operator typed reaches the line unquoted. resume puts --resume
// before the `--` separator for an execution the refusal says to continue. The
// agent arguments after `--` keep their shape — flag names stay, since
// --resume refuses a different list — but every value is redacted, model and
// budget values included: the line is a recovery hint that lands in terminal
// and CI logs, and any value can be a credential. It is also scrubbed of the
// environment's credentials and token shapes argument by argument, before
// quoting, and a line that redacts anything carries the note asking for the
// values back.
func reentry(req *readcommon.Request, f *flags, jsonMode, resume bool, environ []string) string {
	argv := append([]string{"verdict", verbRun, req.Repo, req.Pr}, flagArgv(f, resume)...)
	if f.consent {
		argv = append(argv, "--consent-over-budget")
	}
	if jsonMode {
		argv = append(argv, "--json")
	}
	secrets := argvshape.SecretValues(environ)
	line := RenderCommand(scrubArgs(argv, secrets))
	if len(f.pass) > 0 {
		line += " -- " + RenderCommand(scrubArgs(passShape(f.pass), secrets))
	}
	if strings.Contains(line, argvshape.RedactedValue) {
		line += redactedNote
	}
	return line
}

// scrubArgs scrubs each argument of the environment's credentials and token
// shapes before it is rendered: shell quoting rewrites a value that holds a
// quote or a space, so a scrub of the rendered line would miss it.
func scrubArgs(args, secrets []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = envsecret.ScrubShapes(envsecret.ScrubValues(arg, secrets))
	}
	return out
}

// passShape keeps each agent argument's flag name and redacts every value: a
// bare long flag (`--name`) or short flag (`-x`) stays, `--name=value` keeps
// its name, and anything else — a flag's operand, a positional, a short flag
// with its value attached — is redacted whole.
func passShape(pass []string) []string {
	shape := make([]string, len(pass))
	for i, arg := range pass {
		name, _, inline := strings.Cut(arg, "=")
		long := strings.HasPrefix(arg, "--") && len(name) > 2
		switch {
		case long && inline:
			shape[i] = name + "=" + argvshape.RedactedValue
		case long, len(arg) == 2 && arg[0] == '-' && arg[1] != '-':
			shape[i] = arg
		default:
			shape[i] = argvshape.RedactedValue
		}
	}
	return shape
}

// flagArgv rebuilds the flags the operator typed, each one only when the
// invocation actually asked for it, in the order the line has always used.
func flagArgv(f *flags, resume bool) []string {
	var argv []string
	add := func(on bool, args ...string) {
		if on {
			argv = append(argv, args...)
		}
	}
	add(f.finders != "", findersFlag, f.finders)
	add(f.budget != "", budgetFlag, f.budget)
	add(resume, "--resume")
	add(f.allowShell, allowShellFlag)
	add(f.agent != "", "--agent", f.agent)
	add(f.recipe != "", recipeFlag, f.recipe)
	add(f.timeout != DefaultTimeoutS, "--timeout", f.timeout)
	add(f.rounds != DefaultRounds && f.rounds != "", roundsFlag, f.rounds)
	add(f.print, "--print")
	add(f.stage != "", stageFlag, f.stage)
	return argv
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

func addPermissionHint(ref *core.RefusalError, req *readcommon.Request, f *flags) {
	if ref.Code != "no-review-artifact" || len(f.pass) != 0 {
		return
	}
	row, ok := agentcatalog.Lookup(f.agent)
	if !ok || row.Launch == nil || row.Launch.PermissionHint == "" {
		return
	}
	command := RenderCommand([]string{"verdict", verbRun, req.Repo, req.Pr, "--agent", f.agent})
	command += findersTail(f.finders)
	ref.Message += core.Msgf("\nre-run with: %s -- %s", command, row.Launch.PermissionHint)
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
		return i, r.ReportUsage(&cli.UsageError{Message: core.Msgf(budgetFlag+" must be %s", budget.Vocabulary())}), true
	}
	f.budget = value
	return next, 0, false
}
