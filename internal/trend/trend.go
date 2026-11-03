// Package trend is the trend verb's CLI adapter: argv parsing, one call
// into the trendcore service, warning routing, envelope rendering, and
// exit mapping. Domain logic lives in internal/core/trend.
package trend

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a trend run.
type Request = readcommon.Request

// Run executes `trend <owner/repo> [--overlap, --json]`, returning the
// exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	doOverlap, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before trend completed"}, cli.ExitBroke)
	}
	if req.Repo == "" {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict trend <owner/repo> [--overlap] [--json]"})
	}
	resp, ref := trendcore.Run(ctx, trendRequest(r, req, doOverlap))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("trend", req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return 0
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return 0
}

// trendRequest injects the runner's clock and progress observer into the
// typed service request.
func trendRequest(r *cli.Runner, req Request, doOverlap bool) *trendcore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = runnerProgress{r}
	return &trendcore.Request{
		ReadRequest: read,
		Overlap:     doOverlap,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// runnerProgress routes core's progress observations onto the runner's
// frozen streams: human lines and diagnostics keep their streams,
// warnings keep the `warn: ` prefix and accumulate.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// parseFlags parses the trend flags.
func parseFlags(r *cli.Runner, args []string) (bool, int, bool) {
	overlap := false
	for _, a := range args {
		switch a {
		case "--overlap":
			overlap = true
		default:
			return false, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for trend (want --overlap, --json)", a)}), true
		}
	}
	return overlap, 0, false
}
