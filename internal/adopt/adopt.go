// Package adopt is the adopt verb's CLI adapter: argv parsing, one call
// into the adoptcore service, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/adopt.
package adopt

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved adopt invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `adopt <owner/repo> <pr> --from <local-slug> [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	from, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "adopt"); done {
		return code
	}
	resp, ref := adoptcore.Run(ctx, adoptRequest(r, req, from))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	fmt.Fprintln(r.Human, resp.Human)
	if r.JSONMode {
		env := core.RenderEnvelope("adopt", req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// adoptRequest injects the runner's clock and progress observer into the
// typed service request.
func adoptRequest(r *cli.Runner, req Request, from string) *adoptcore.Request {
	read := core.ReadRequest{
		Runtime: &core.Runtime{Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}, Commander: coreexec.Runner{}},
		Repo:    req.Repo,
		Pr:      req.Pr,
	}
	out := &adoptcore.Request{From: from}
	out.ReadRequest = read
	return out
}

// parseFlags strips adopt's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	from := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--from":
			v, next, u := cli.FlagValue(args, i, a, "<local-slug>")
			if u != nil {
				return "", r.ReportUsage(u), true
			}
			from = v
			i = next
		default:
			msg := fmt.Sprintf(msgUnknownFlag, a)
			code := r.ReportUsage(&cli.UsageError{Message: msg})
			return "", code, true
		}
	}
	return from, 0, false
}

// msgUnknownFlag is the frozen unknown-flag usage text.
const msgUnknownFlag = "unknown flag '%s' for adopt (want --from <local-slug>|auto, --json)"
