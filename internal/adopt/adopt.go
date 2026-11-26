// Package adopt is the adopt verb's CLI adapter: argv parsing, one call
// into the adoptcore service, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/adopt.
package adopt

import (
	"context"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run executes `adopt <owner/repo> <pr> --from <local-slug> [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	from, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "adopt"); done {
		return code
	}
	resp, ref := adoptcore.Run(ctx, adoptRequest(r, &req, from))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	r.HumanLine(resp.Human)
	r.Emit("adopt", req.Repo, req.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

// adoptRequest injects the runner's clock and progress observer into the
// typed service request.
func adoptRequest(r *cli.Runner, req *readcommon.Request, from string) *adoptcore.Request {
	return &adoptcore.Request{ReadRequest: r.Read(*req, coreexec.Runner{}), From: from}
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
			code := r.ReportUsage(&cli.UsageError{Message: core.Msgf(msgUnknownFlag, a)})
			return "", code, true
		}
	}
	return from, 0, false
}

// msgUnknownFlag is the frozen unknown-flag usage text.
const msgUnknownFlag = "unknown flag '%s' for adopt (want --from <local-slug>|auto, --json)"
