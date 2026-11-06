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
// returning the process exit code. Only .verdict/state/<pr>/threads.json is
// rewritten; the diff and the rest of the bundle are untouched.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	provider, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.RouteHuman()
	if code, done := r.Canceled(ctx, "threads"); done {
		return code
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
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	return &fetchcore.ThreadsRequest{
		ReadRequest: read,
		Provider:    provider,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parseFlags parses the threads flags. A value flag with no operand is a
// usage error, like every other verb.
func parseFlags(r *cli.Runner, args []string) (provider string, code int, done bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--provider":
			v, next, u := cli.FlagValue(args, i, a, "<name>")
			if u != nil {
				return "", r.ReportUsage(u), true
			}
			provider = v
			i = next
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for threads (want --provider <name>, --json)", a)}), true
		}
	}
	return provider, 0, false
}
