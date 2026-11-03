// Package threads is the threads verb's CLI adapter: argv parsing, one call
// into the fetchcore refresh service, warning routing, envelope rendering,
// and exit mapping. Domain logic lives in internal/core/fetch and the
// provider-neutral contract in internal/core/threads.
package threads

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a threads run.
type Request = readcommon.Request

// Run executes `threads <owner/repo> <pr> [--provider <name>] [--json]`,
// returning the process exit code. Only .review/<pr>/threads.json is
// rewritten; the diff and the rest of the bundle are untouched.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	provider, code, done := parseFlags(r, args)
	if done {
		return code
	}
	routeHuman(r)
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before threads completed"}, cli.ExitBroke)
	}
	resp, ref := fetchcore.RunThreads(ctx, threadsRequest(r, req, provider))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("threads", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// threadsRequest injects the runner's clock, progress observer, and command
// runner into the typed refresh request.
func threadsRequest(r *cli.Runner, req Request, provider string) *fetchcore.ThreadsRequest {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = runnerProgress{r}
	read.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	return &fetchcore.ThreadsRequest{
		ReadRequest: read,
		Provider:    provider,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parseFlags parses the threads flags. A trailing value flag reads "" and
// falls back to github, like fetch's documented divergence.
func parseFlags(r *cli.Runner, args []string) (provider string, code int, done bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			r.JSONMode = true
		case "--provider":
			provider, i = flagValue(args, i)
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for threads (want --provider <name>, --json)", a)}), true
		}
	}
	return provider, 0, false
}

// flagValue reads a trailing flag value ("", like the shell's unset default,
// when absent).
func flagValue(args []string, i int) (string, int) {
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", i
}

// runnerProgress routes core's progress observations onto the runner's frozen
// streams: human lines and diagnostics keep their streams, warnings keep the
// `warn: ` prefix and accumulate.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}
