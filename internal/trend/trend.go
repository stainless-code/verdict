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
	resolution, filtered := resolutionFlag(args)
	doOverlap, code, done := parseFlags(r, filtered)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "trend"); done {
		return code
	}
	if req.Repo == "" {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict trend <owner/repo> [--overlap] [--json]"})
	}
	request := trendRequest(r, req, doOverlap)
	request.Resolution = resolution
	resp, ref := trendcore.Run(ctx, request)
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
	read.Progress = cli.Progress{R: r}
	return &trendcore.Request{
		ReadRequest: read,
		Overlap:     doOverlap,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

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

func resolutionFlag(args []string) (bool, []string) {
	resolution := false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--resolution" {
			resolution = true
		} else {
			filtered = append(filtered, a)
		}
	}
	return resolution, filtered
}
