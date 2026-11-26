// Package journal is the journal verb's CLI adapter: argv parsing, one call
// into internal/core/journal, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/journal.
package journal

import (
	"context"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	journalcore "github.com/stainless-code/verdict/internal/core/journal"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a journal run.
type Request = readcommon.Request

// Run executes `journal <owner/repo> <pr> [--json]`, returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	for _, a := range args {
		return r.ReportUsage(&cli.UsageError{
			Message: core.Msgf("unknown flag '%s' for journal (want --json)", a),
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
		r.Emit("journal", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire())
		return cli.ExitOK
	}
	r.HumanLines(resp.Human)
	return cli.ExitOK
}

func buildRequest(r *cli.Runner, req Request) *journalcore.Request {
	var cmdr core.Commander
	if req.Runtime != nil {
		cmdr = req.Commander
	}
	read := r.Read(req, cmdr)
	return &journalcore.Request{
		ReadRequest: read,
		Warnings:    append([]string{}, r.Warnings...),
	}
}
