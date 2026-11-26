// Package loop is the loop verb's CLI adapter: argv parsing, the prefs read
// (operator-defined agents and the personal spend policy), the injected
// commanders and environment, the human render, the envelope, and the exit
// table (0 the last gate passed, 1 the loop stopped without a pass, 2 usage,
// 3 canceled). Domain logic lives in internal/core/loop.
//
// `loop` is CLI-only, like `run`: it spawns the operator's agent, so it is no
// MCP tool, Go facade method, or TypeScript client method.
package loop

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	loopcore "github.com/stainless-code/verdict/internal/core/loop"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/shellquote"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	verb = "loop"
	// DefaultMaxRounds is the round ceiling a bare loop runs with.
	DefaultMaxRounds = "3"
	// DefaultTimeout is the wall-clock limit a bare loop runs with.
	DefaultTimeout = "600s"
	// DefaultMaxSeverity is the tier a bare loop's gate fails at: an
	// informational finding still shows in the round's output, but a
	// from-scratch review always finds one more, so it never blocks a pass.
	DefaultMaxSeverity = "important"

	flagAgent     = "--agent"
	flagMaxRounds = "--max-rounds"
	flagMaxCost   = "--max-cost-usd"
	flagSeverity  = "--max-severity"
	flagTimeout   = "--timeout"
	flagOutput    = "--output"
	flagFixPrompt = "--fix-prompt"
	flagPrint     = "--print"
	// flagSandbox and flagSandboxNet pass verify's own sandbox flags to the
	// fix phase's verify step.
	flagSandbox    = "--sandbox"
	flagSandboxNet = "--sandbox-network-only"
	flagAssign     = "="
	codeBadFlags   = "bad-flags"
	// floatBits renders a receipt amount without inventing digits.
	floatBits = 64

	// maxFixPromptBytes bounds the --fix-prompt file: it rides in one argv.
	maxFixPromptBytes = 64 << 10

	msgUnknownFlag = "unknown flag '%s' for loop (want --agent <name>, --max-rounds <n>, " +
		"--max-severity <critical|important|informational>, " +
		"--max-cost-usd <usd>, --timeout <duration>, --output <dir>, --fix-prompt <file>, --sandbox, " +
		"--sandbox-network-only, --print, -- <agent args>, --json)"
	msgExtraArg = "loop takes one <owner/repo> positional; unexpected '%s'"
)

// flags is the parsed loop surface.
type flags struct {
	agent     string
	maxRounds string
	maxCost   string
	severity  string
	timeout   string
	output    string
	fixPrompt string
	// pass is everything after `--`, verbatim: the agent arguments every
	// spawn carries.
	pass       []string
	print      bool
	sandbox    bool
	sandboxNet bool
}

// Run executes `loop <owner/repo> --agent <name> [--max-rounds <n>]
// [--max-severity <tier>]
// [--max-cost-usd <usd>] [--timeout <duration>] [--output <dir>]
// [--fix-prompt <file>] [--sandbox | --sandbox-network-only] [--print]
// [-- <agent args>...] [--json]`.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	r.Repo, r.Pr = req.Repo, ""
	// The summary owns stdout; every composed verb's progress goes to the
	// diagnostic stream so a pipe reads the loop's own lines only.
	r.Human = r.Diag
	f, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = rentry(req.Repo, &f)
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	lreq, ref := loopRequest(r, &req, &f)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	resp, ref := loopcore.Run(ctx, lreq)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		r.Emit(verb, req.Repo, "", resp.At, resp.Next, resp.Warnings, core.Wire(resp.Data))
	} else {
		render(r.Out, resp.Data)
	}
	return exitFor(resp.Data)
}

// exitFor maps the outcome: a pass and a --print render exit 0; an operator
// cancel — the refused stop carrying the canceled refusal — exits 3; every
// other stop exits 1, including a timeout stop whose deadline canceled a
// phase. The receipts are written either way.
func exitFor(d *loopcore.Data) int {
	switch {
	case d.Stop == loopcore.StopPlanned || d.Passed:
		return cli.ExitOK
	case d.Stop == loopcore.StopRefused && d.Refusal != nil && d.Refusal.Code == cli.CodeCanceled:
		return cli.ExitBroke
	}
	return cli.ExitRefused
}

// loopRequest resolves the parsed flags into the service request: the limits,
// the fix prompt's text, the receipts directory, and the prefs-defined agents.
func loopRequest(r *cli.Runner, req *readcommon.Request, f *flags) (*loopcore.Request, *core.RefusalError) {
	rounds, ref := parseRounds(f.maxRounds)
	if ref != nil {
		return nil, ref
	}
	timeout, ref := parseTimeout(f.timeout)
	if ref != nil {
		return nil, ref
	}
	prompt, ref := readFixPrompt(f.fixPrompt)
	if ref != nil {
		return nil, ref
	}
	output, ref := outputDir(f.output)
	if ref != nil {
		return nil, ref
	}
	agents, policy, ref := runcore.Personal(req.Env)
	if ref != nil {
		return nil, ref
	}
	return &loopcore.Request{
		Flow:       flow(r, req, agents, policy),
		RepoRoot:   req.RepoRoot,
		Repo:       req.Repo,
		Agent:      f.agent,
		MaxRounds:  rounds,
		Timeout:    timeout,
		MaxCostUSD: f.maxCost,
		Output:     output,
		FixPrompt:  prompt,
		AgentArgs:  f.pass,

		MaxSeverity: f.severity,
		Print:       f.print,

		VerifySandbox:            f.sandbox,
		VerifySandboxNetworkOnly: f.sandboxNet,
	}, nil
}

// flow wires the shared pipeline: the runner's runtime, the fetch commander,
// the agent watchdog, the process environment, and the prefs agents and
// spend policy, read the way `run` reads them.
func flow(r *cli.Runner, req *readcommon.Request, agents []config.PersonalAgent, policy *spend.Policy) *reviewflow.Flow {
	reads := coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	return &reviewflow.Flow{
		ReadRequest:    r.Read(*req, reads),
		SettingsPath:   cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		Effective:      r.Effective,
		Commander:      reads,
		AgentCommander: coreexec.Watchdog{},
		LookPath:       exec.LookPath,
		Environ:        os.Environ(),
		Human:          r.Human,
		Diag:           r.Diag,
		JSONMode:       r.JSONMode,
		Agents:         agents,
		SpendPolicy:    policy,
	}
}

// parseRounds reads --max-rounds: a positive whole number.
func parseRounds(raw string) (int, *core.RefusalError) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || strconv.Itoa(n) != raw {
		return 0, &core.RefusalError{Code: codeBadFlags, Message: core.Msgf(
			"--max-rounds must be a positive whole number, got '%s'", raw)}
	}
	return n, nil
}

// parseTimeout reads --timeout: bare seconds (600) or a Go duration
// (600s, 10m).
func parseTimeout(raw string) (time.Duration, *core.RefusalError) {
	spelled := raw
	if digitsOnly(raw) {
		spelled += "s"
	}
	d, err := time.ParseDuration(spelled)
	if err != nil || d <= 0 {
		return 0, &core.RefusalError{Code: codeBadFlags, Message: core.Msgf(
			"--timeout must be a positive duration (600, 600s, 10m), got '%s'", raw)}
	}
	return d, nil
}

func digitsOnly(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// readFixPrompt reads the --fix-prompt file: a regular, non-empty text file
// small enough to ride in one argv.
func readFixPrompt(path string) (string, *core.RefusalError) {
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", &core.RefusalError{Code: codeBadFlags, Message: core.Msgf("--fix-prompt %s"+
			" is not a readable file", path)}
	}
	if info.Size() > maxFixPromptBytes {
		return "", &core.RefusalError{Code: codeBadFlags, Message: core.Msgf(
			"--fix-prompt %s is %d bytes, over the %d-byte limit", path, info.Size(), maxFixPromptBytes)}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", &core.RefusalError{Code: codeBadFlags, Message: core.Msgf("--fix-prompt %s: %v", path, err)}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", &core.RefusalError{Code: codeBadFlags, Message: core.Msgf("--fix-prompt %s is empty", path)}
	}
	return string(raw), nil
}

// outputDir resolves --output against the working directory; "" lets the
// service pick the default under the checkout.
func outputDir(raw string) (string, *core.RefusalError) {
	if raw == "" {
		return "", nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", &core.RefusalError{Code: codeBadFlags, Message: core.Msgf("--output %s: %v", raw, err)}
	}
	return abs, nil
}

// parseFlags strips loop's flags, refusing unknown ones and any positional
// past the repo (exit 2). Every argument after a bare `--` is the agent's,
// passed through unchanged, as for run.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{maxRounds: DefaultMaxRounds, timeout: DefaultTimeout, severity: DefaultMaxSeverity}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			f.pass = append([]string{}, args[i+1:]...)
			break
		}
		next, uerr := parseToken(&f, args, i)
		if uerr != nil {
			return f, r.ReportUsage(uerr), true
		}
		i = next
	}
	if f.agent == "" {
		return f, r.ReportUsage(cli.MissingValue(flagAgent, operand(flagAgent))), true
	}
	return f, 0, false
}

// parseToken consumes one argument: --print, a value flag in either
// spelling, or a usage error for anything else. It returns the index of the
// last argument it consumed.
func parseToken(f *flags, args []string, i int) (int, *cli.UsageError) {
	a := args[i]
	if dst := boolFlag(f, a); dst != nil {
		*dst = true
		return i, nil
	}
	dst := valueFlag(f, a)
	switch {
	case dst == nil && strings.HasPrefix(a, "-"):
		return i, &cli.UsageError{Message: core.Msgf(msgUnknownFlag, a)}
	case dst == nil:
		return i, &cli.UsageError{Message: core.Msgf(msgExtraArg, a)}
	}
	name, _, _ := strings.Cut(a, flagAssign)
	value, next, uerr := flagValue(args, i, name)
	if uerr != nil {
		return i, uerr
	}
	// A following option is a lost value, not a value: no loop operand starts
	// with the flag prefix.
	if value == "" || strings.HasPrefix(value, "--") {
		return i, cli.MissingValue(name, operand(name))
	}
	*dst = value
	return next, nil
}

// flagValue reads one value flag's operand in either spelling: inline after
// `=`, or the next argument.
func flagValue(args []string, i int, name string) (string, int, *cli.UsageError) {
	if _, inline, ok := strings.Cut(args[i], flagAssign); ok {
		return inline, i, nil
	}
	return cli.FlagValue(args, i, name, operand(name))
}

// boolFlag returns the field one boolean flag sets, or nil.
func boolFlag(f *flags, arg string) *bool {
	switch arg {
	case flagPrint:
		return &f.print
	case flagSandbox:
		return &f.sandbox
	case flagSandboxNet:
		return &f.sandboxNet
	}
	return nil
}

// valueFlag returns the field one value flag fills, in either spelling, or
// nil for anything else.
func valueFlag(f *flags, arg string) *string {
	name, _, _ := strings.Cut(arg, flagAssign)
	switch name {
	case flagAgent:
		return &f.agent
	case flagMaxRounds:
		return &f.maxRounds
	case flagMaxCost:
		return &f.maxCost
	case flagSeverity:
		return &f.severity
	case flagTimeout:
		return &f.timeout
	case flagOutput:
		return &f.output
	case flagFixPrompt:
		return &f.fixPrompt
	}
	return nil
}

// operand names each value flag's operand for the usage line.
func operand(name string) string {
	switch name {
	case flagAgent:
		return "<name>"
	case flagMaxRounds:
		return "<n>"
	case flagMaxCost:
		return "<usd>"
	case flagSeverity:
		return "<critical|important|informational>"
	case flagTimeout:
		return "<duration>"
	case flagOutput:
		return "<dir>"
	}
	return "<file>"
}

// rentry is the re-entry line a refusal prints: the same loop with the agent
// the operator named.
func rentry(repo string, f *flags) string {
	return "verdict loop " + repo + " " + flagAgent + " " + shellquote.Arg(f.agent)
}

// render prints the human summary: one line per round, then the outcome; a
// --print render lists the plan instead.
func render(w io.Writer, d *loopcore.Data) {
	if d.Stop == loopcore.StopPlanned {
		renderPlan(w, d.Plan)
		return
	}
	for i := range d.Rounds {
		fmt.Fprintln(w, untrusted.Line(roundLine(&d.Rounds[i])))
	}
	fmt.Fprintln(w, untrusted.Line(outcomeLine(d)))
	if d.Refusal != nil {
		// A composed verb's message may already lead with its code (record's
		// coverage-incomplete does); the line names the code once.
		msg := strings.TrimPrefix(d.Refusal.Message, d.Refusal.Code+": ")
		fmt.Fprint(w, core.Msgf("loop: %s: %s\n", d.Refusal.Code, msg))
	}
}

// renderPlan lists every planned command under its round, with the condition
// a conditional step waits for.
func renderPlan(w io.Writer, plan []loopcore.PlanStep) {
	round := 0
	for _, step := range plan {
		if step.Round != round {
			round = step.Round
			fmt.Fprintf(w, "round %d:\n", round)
		}
		when := ""
		switch step.When {
		case "gate-failed":
			when = "(when the gate fails) "
		case "verify-cmd-declared":
			when = "(when the fix declares patch.json verifyCmd) "
		}
		fmt.Fprintf(w, "  %-7s %s%s\n", step.Phase, when, command(step.Argv))
	}
}

func command(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellquote.Arg(arg))
	}
	return strings.Join(parts, " ")
}

// roundLine is one round's summary: the bundle, the open count, the gate, the
// cost, and the fix phase's verify receipt.
func roundLine(rd *loopcore.Round) string {
	line := fmt.Sprintf("loop: round %d  %s  open %s  gate %s  cost %s",
		rd.Round, rd.Bundle, intOr(rd.Open), gateWord(rd.Gate), costWord(rd.CostUSD))
	if rd.Fix != nil {
		line += "  fix: " + verifyWord(rd.Fix.Verify)
	}
	return line
}

func outcomeLine(d *loopcore.Data) string {
	where := ""
	if d.Output != nil {
		where = " — receipts in " + *d.Output
	}
	if d.Passed {
		return fmt.Sprintf("loop: PASS after %d round(s)%s", len(d.Rounds), where)
	}
	return fmt.Sprintf("loop: STOP %s after %d round(s)%s", d.Stop, len(d.Rounds), where)
}

func intOr(v *int) string {
	if v == nil {
		return "-"
	}
	return strconv.Itoa(*v)
}

func gateWord(v *bool) string {
	switch {
	case v == nil:
		return "-"
	case *v:
		return "pass"
	}
	return "fail"
}

func costWord(v *float64) string {
	if v == nil {
		return "unknown"
	}
	return "$" + strconv.FormatFloat(*v, 'f', -1, floatBits)
}

func verifyWord(v *loopcore.Verify) string {
	switch {
	case v == nil:
		return "no verify command declared"
	case v.Refusal != nil:
		return "verify refused (" + v.Refusal.Code + ")"
	case v.TimedOut:
		return "verify timed out"
	case v.Exit != nil:
		return "verify exit " + strconv.Itoa(*v.Exit)
	}
	return "verify -"
}
