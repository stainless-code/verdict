// Package demo is the demo verb's CLI adapter: argv parsing, the caller's
// repository probe, the PATH and environment injection, human rendering of the
// review record produced, envelope rendering, and exit mapping. Domain logic
// lives in internal/core/demo.
//
// The adapter owns the two process facts the service may not read itself: the
// checkout the operator stands in, and the ordered child environment the agent
// inherits. It also owns the human layout — the review body is the only thing
// on stdout, so every composed verb's progress line goes to stderr.
package demo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	democore "github.com/stainless-code/verdict/internal/core/demo"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// verb is the CLI verb name, used for the envelope and the entries.
	verb = "demo"
	// agentFlag, keepFlag, and timeoutFlag are the verb-local flags.
	agentFlag   = "--agent"
	keepFlag    = "--keep"
	timeoutFlag = "--timeout"
	// flagSeparator ends flag parsing, so a positional never reads as a flag.
	flagSeparator = "--"
	// flagAssign is the inline-value spelling `--flag=value`.
	flagAssign = "="
	// codeMissingTool is the shared refusal for a binary that is not installed;
	// the adapter renders its frozen line with no hint, like every other verb.
	codeMissingTool = "missing-tool"
	// msgUnknownFlag is the usage refusal for a flag demo does not take.
	msgUnknownFlag = "unknown flag '%s' for demo (want --agent <name>, --keep, --timeout <secs>, --json)"
	// msgEmptyAgent refuses an empty --agent value, which would otherwise read
	// as "no override" and silently fall back to auto-detection.
	msgEmptyAgent = "flag '--agent' needs a value (want a name from the agent catalog)"
	// msgEmptyTimeout refuses an empty --timeout value, which would otherwise
	// read as the default and hide the typo.
	msgEmptyTimeout = "flag '--timeout' needs a value (want a positive number of seconds)"
	// msgTooMany names the single-positional contract.
	msgTooMany = "demo takes one <public-github-pr-url> positional, not %d (want %s)"
	// urlShape is the one accepted positional spelling the usage refusal names.
	urlShape = "https://github.com/<owner>/<repo>/pull/<n>"
)

// flags is demo's parsed surface: the optional URL, the agent override, and
// whether the disposable directory is kept.
type flags struct {
	target  string
	agent   string
	timeout string
	keep    bool
}

// Run executes `demo [<public-github-pr-url>] [--agent <name>] [--keep]
// [--timeout <secs>] [--json]`, returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	// The review body owns stdout; every composed verb's progress moves to the
	// diagnostic stream so the render is the only thing a pipe reads.
	r.Human = r.Diag
	f, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = rentry(f.target, f.agent)
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	resp, ref := democore.Run(ctx, demoRequest(ctx, r, &req, f))
	if ref != nil {
		return refuse(r, ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		r.Emit(verb, resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, core.Wire(resp.Data))
		return cli.ExitOK
	}
	renderHuman(r, resp)
	return cli.ExitOK
}

// parseFlags strips demo's flags and its one optional positional, reporting
// unknown flags and a second positional as usage errors (exit 2).
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	var positionals []string
	for i := 0; i < len(args); i++ {
		next, code, done := parseToken(r, &f, args, i, &positionals)
		if done {
			return f, code, true
		}
		i = next
	}
	return finishFlags(r, &f, positionals)
}

// parseToken consumes one demo argument: a value flag, a boolean flag, the
// separator that ends flag parsing, or a positional.
func parseToken(
	r *cli.Runner, f *flags, args []string, i int, positionals *[]string,
) (next, code int, done bool) {
	a := args[i]
	switch {
	case a == agentFlag || strings.HasPrefix(a, agentFlag+flagAssign):
		v, next, code, done := parseValueToken(r, args, i, agentFlag, "<name>", msgEmptyAgent)
		if !done {
			f.agent = v
		}
		return next, code, done
	case a == timeoutFlag || strings.HasPrefix(a, timeoutFlag+flagAssign):
		v, next, code, done := parseValueToken(r, args, i, timeoutFlag, "<secs>", msgEmptyTimeout)
		if !done {
			f.timeout = v
		}
		return next, code, done
	case a == keepFlag:
		f.keep = true
	case a == flagSeparator:
		*positionals = append(*positionals, args[i+1:]...)
		return len(args), 0, false
	case strings.HasPrefix(a, "-"):
		return i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(msgUnknownFlag, a)}), true
	default:
		*positionals = append(*positionals, a)
	}
	return i, 0, false
}

// parseValueToken consumes one value flag in both spellings, `--flag value`
// and `--flag=value`, refusing a lost or empty value with emptyMsg.
func parseValueToken(
	r *cli.Runner, args []string, i int, name, operand, emptyMsg string,
) (value string, next, code int, done bool) {
	if a := args[i]; a == name {
		v, n, u := cli.FlagValue(args, i, a, operand)
		if u != nil {
			return "", i, r.ReportUsage(u), true
		}
		// A following option is a lost value, not a value: demo's operands are
		// a number and an agent name, so a token starting with the separator
		// can only be the next flag. Taking it would consume that flag and
		// report bad-timeout after a fetch instead of a usage error here.
		if v == "" || strings.HasPrefix(v, flagSeparator) {
			return "", i, r.ReportUsage(&cli.UsageError{Message: emptyMsg}), true
		}
		return v, n, 0, false
	}
	v := strings.TrimPrefix(args[i], name+flagAssign)
	if v == "" {
		return "", i, r.ReportUsage(&cli.UsageError{Message: emptyMsg}), true
	}
	return v, i, 0, false
}

// finishFlags settles the one optional positional and refuses a second.
func finishFlags(r *cli.Runner, f *flags, positionals []string) (flags, int, bool) {
	if len(positionals) > 1 {
		return *f, r.ReportUsage(&cli.UsageError{
			Message: fmt.Sprintf(msgTooMany, len(positionals), urlShape),
		}), true
	}
	if len(positionals) == 1 {
		f.target = positionals[0]
	}
	return *f, 0, false
}

// demoRequest injects the runtime, the streams, the checkout, and both
// commanders into the typed service request.
func demoRequest(ctx context.Context, r *cli.Runner, req *readcommon.Request, f flags) *democore.Request {
	// The caller's top level is probed for both forms: a local review reads it,
	// and a URL review still must never write its bundle inside it. Outside a
	// repository the probe answers "" and the run continues.
	callerRoot := ""
	if root, err := readcommon.RepoRoot(ctx); err == nil {
		callerRoot = root
	}
	checkout := ""
	if f.target == "" {
		checkout = callerRoot
	}
	out := &democore.Request{
		Target:         f.target,
		Agent:          f.agent,
		TimeoutRaw:     f.timeout,
		Checkout:       checkout,
		CallerRoot:     callerRoot,
		LookPath:       exec.LookPath,
		Commander:      coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		AgentCommander: coreexec.Watchdog{},
		Environ:        os.Environ(),
		Human:          r.Human,
		Diag:           r.Diag,
		Warnings:       append([]string{}, r.Warnings...),
		JSONMode:       r.JSONMode,
		Keep:           f.keep,
		SettingsPath:   cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		Effective:      r.Effective,
	}
	out.ReadRequest = r.Read(*req, coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true})
	return out
}

// renderHuman prints the record: the identity of the run, the facts line, the
// review body record rendered, the topology block record omits below its size
// gate, the next steps, and where the bundle went.
func renderHuman(r *cli.Runner, resp *democore.Response) {
	fmt.Fprintln(r.Out, resp.Header)
	if resp.Facts != "" {
		fmt.Fprintln(r.Out, "  "+resp.Facts)
	}
	fmt.Fprintln(r.Out)
	fmt.Fprint(r.Out, ensureNewline(resp.Body))
	if resp.TopologyAppended {
		fmt.Fprintln(r.Out)
		fmt.Fprintln(r.Out, resp.Topology)
	}
	fmt.Fprintln(r.Out)
	for i, step := range resp.Steps {
		if i == 0 {
			fmt.Fprintln(r.Out, "next:  "+step)
			continue
		}
		fmt.Fprintln(r.Out, "       "+step)
	}
	fmt.Fprintln(r.Out, bundleLine(resp))
}

// bundleLine says where the artifacts landed and what happens to them.
func bundleLine(resp *democore.Response) string {
	if resp.Data == nil {
		return ""
	}
	if resp.Data.Kept {
		return "bundle: " + resp.Data.BundleDir + " (kept: --keep)"
	}
	return "bundle: " + resp.Data.BundleDir + " (removed; pass --keep to inspect)"
}

// ensureNewline keeps the body separated from the following block whatever the
// renderer put at its end.
func ensureNewline(body string) string {
	if body == "" || strings.HasSuffix(body, "\n") {
		return body
	}
	return body + "\n"
}

// refuse renders a demo failure: a missing binary prints its frozen line with
// no hint (nothing a hint could add) in human mode, a cancel exits 3, anything
// else is a typed refusal at exit 1. Machine mode always renders the envelope,
// so a missing binary is not the one failure a script cannot parse.
func refuse(r *cli.Runner, ref *core.RefusalError) int {
	if ref.Code == codeMissingTool && !r.JSONMode {
		fmt.Fprintln(r.Diag, ref.Message)
		r.WriteRentry()
		return cli.ExitRefused
	}
	return r.RefuseTyped(ref)
}

// rentry renders the re-entry line the refusal prints: the same invocation
// with the transport and agent the operator asked for, so a failed first run
// hands back the command that retries it.
func rentry(target, agent string) string {
	return "verdict demo " + urlOrPlaceholder(target) + agentFlag + " " + agentOrPlaceholder(agent)
}

// urlOrPlaceholder renders the re-entry line's positional, with its trailing
// space so the flag follows on the same line.
func urlOrPlaceholder(target string) string {
	if target == "" {
		return ""
	}
	return target + " "
}

// agentOrPlaceholder renders the re-entry line's agent.
func agentOrPlaceholder(agent string) string {
	if agent == "" {
		return "<name>"
	}
	return agent
}
