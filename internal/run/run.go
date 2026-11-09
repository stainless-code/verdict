// Package run is the run verb's CLI adapter: argv parsing (same usage text and
// exit 2), one call into the run core service, command rendering for --print,
// envelope rendering, and refusal-vs-cancel exit mapping. Domain logic — the
// agent table, the watchdog spawn, the artifact assertion, and the provenance
// record — lives in internal/core/run.
//
// `run` is CLI-only on purpose: it is not an MCP tool, a Go facade method, or a
// TypeScript client method. A tool that spawns another agent turns every MCP
// client into a recursive launcher of an unpriced local process, which is the
// opposite of the boundary the guardrail sentence draws (no daemon, no queue, no
// model selection); the code path that starts a process belongs to the operator's
// own terminal and CI, not to a tool call from inside a model's turn.
package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved run invocation.
type Request struct {
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// defaultTimeoutS is the watchdog budget when --timeout is absent: the same
// default verify gives the commands it spawns.
const defaultTimeoutS = "300"

// verbsWanted is the flag list the unknown-flag usage error names.
const verbsWanted = "--agent <name>, --timeout <secs>, --print, -- <agent args>, --json"

// Run executes
// `run <owner/repo> <pr|local-name> --agent <name> [--timeout <secs>] [--print]
// [-- <agent args>...] [--json]`, returning the process exit code. A non-zero
// agent exit is preserved as this process's exit code after the typed refusal is
// rendered, so CI surfaces the real failure.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = rentry(req, flags, r.JSONMode)
	if code, done := r.Canceled(ctx, "run"); done {
		return code
	}
	resp, ref := runcore.Run(ctx, runRequest(r, req, flags))
	if ref != nil {
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
func runRequest(r *cli.Runner, req Request, flags flags) *core.RunRequest {
	root := req.Root
	return &core.RunRequest{
		ReadRequest: core.ReadRequest{
			Runtime: &core.Runtime{
				Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
				Commander: coreexec.Watchdog{},
			},
			Repo: req.Repo, Pr: req.Pr, Root: &root, RepoRoot: req.RepoRoot,
		},
		LookPath:    exec.LookPath,
		PassThrough: flags.pass,
		Environ:     os.Environ(),
		Agent:       flags.agent,
		TimeoutRaw:  flags.timeout,
		Print:       flags.print,
		JSONMode:    r.JSONMode,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// flags is the parsed run surface. pass is everything after `--`, verbatim.
type flags struct {
	agent   string
	timeout string
	pass    []string
	print   bool
}

// parseFlags strips run's flags, reporting unknown ones. Every argument after a
// bare `--` is the operator's business and is passed through unchanged.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{timeout: defaultTimeoutS}
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
	case "--agent":
		v, next, u := cli.FlagValue(args, i, a, "<name>")
		if u != nil {
			return i, r.ReportUsage(u), true
		}
		f.agent = v
		return next, 0, false
	case "--timeout":
		v, next, u := cli.FlagValue(args, i, a, "<secs>")
		if u != nil {
			return i, r.ReportUsage(u), true
		}
		if v != "" {
			f.timeout = v
		}
		return next, 0, false
	case "--print":
		f.print = true
		return i, 0, false
	default:
		return i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for run (want %s)", a, verbsWanted)}), true
	}
}

// rentry rebuilds the exact re-run command after flag parsing. The pass-through
// tail is not reproduced: its quoting belongs to whatever shell the operator used.
func rentry(req Request, f flags, jsonMode bool) string {
	s := "verdict run " + req.Repo + " " + req.Pr
	if f.agent != "" {
		s += " --agent " + f.agent
	}
	if f.timeout != defaultTimeoutS {
		s += " --timeout " + f.timeout
	}
	if f.print {
		s += " --print"
	}
	if jsonMode {
		s += " --json"
	}
	return s
}

// shellSafeChars are the argument bytes a POSIX shell passes through unquoted:
// the characters no shell metacharacter uses.
const shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@%+,"

// RenderCommand renders one argv as a POSIX shell command line, quoting every
// argument that is not already safe. It is what --print writes: the exact
// command the launcher would spawn, copy-pasteable into sh/bash/zsh, which is
// also what the tests compare byte-for-byte with the argv the child received.
func RenderCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

// quoteArg single-quotes one argument when it carries anything a shell would
// interpret, closing the quote around an embedded single quote.
func quoteArg(arg string) string {
	if arg != "" && strings.Trim(arg, shellSafeChars) == "" {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
