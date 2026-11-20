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
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a trend run.
type Request = readcommon.Request

// Run executes `trend <owner/repo> [--overlap, --incidents, --json]`,
// returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	rules, ruleFiltered := rulesFlag(args)
	resolution, filtered := resolutionFlag(ruleFiltered)
	doOverlap, doIncidents, code, done := parseFlags(r, filtered)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "trend"); done {
		return code
	}
	if req.Repo == "" {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict trend <owner/repo> [--overlap] [--incidents] [--json]"})
	}
	request := trendRequest(r, req, doOverlap)
	request.Resolution = resolution
	request.Rules = rules
	request.Incidents = doIncidents
	resp, ref := trendcore.Run(ctx, request)
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		r.Emit("trend", req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire())
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
	var cmdr core.Commander
	if req.Runtime != nil {
		cmdr = req.Commander
	}
	read := r.Read(req, cmdr)
	return &trendcore.Request{
		ReadRequest: read,
		Overlap:     doOverlap,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parseFlags parses the trend flags.
func parseFlags(r *cli.Runner, args []string) (bool, bool, int, bool) {
	overlap, incidents := false, false
	for _, a := range args {
		switch a {
		case "--overlap":
			overlap = true
		case "--incidents":
			incidents = true
		default:
			return false, false, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for trend (want --overlap, --incidents, --json)", a)}), true
		}
	}
	return overlap, incidents, 0, false
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

func rulesFlag(args []string) (bool, []string) {
	rules := false
	var ruleFiltered []string
	for _, arg := range args {
		if arg == "--rules" {
			rules = true
		} else {
			ruleFiltered = append(ruleFiltered, arg)
		}
	}
	return rules, ruleFiltered
}
