// Package journal is the journal verb's CLI adapter: argv parsing, one call
// into internal/core/journal, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/journal.
package journal

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	journalcore "github.com/stainless-code/verdict/internal/core/journal"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a journal run.
type Request = readcommon.Request

// Run executes `journal <owner/repo> <pr> [--json]`, returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	for _, a := range args {
		return r.ReportUsage(&cli.UsageError{
			Message: fmt.Sprintf("unknown flag '%s' for journal (want --json)", a),
		})
	}
	if code, done := r.Canceled(ctx, "journal"); done {
		return code
	}
	if req.Repo == "" || req.Pr == "" {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict journal <owner/repo> <pr> [--json]"})
	}
	resp, ref := journalcore.Run(ctx, buildRequest(r, req))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("journal", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return cli.ExitOK
}

func buildRequest(r *cli.Runner, req Request) *journalcore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	return &journalcore.Request{
		ReadRequest: read,
		Warnings:    append([]string{}, r.Warnings...),
	}
}
