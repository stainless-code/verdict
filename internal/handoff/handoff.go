// Package handoff is the handoff verb's CLI adapter: argv parsing, one call
// into the handoff service, warning routing, and envelope rendering. Domain
// logic lives in internal/core/handoff.
package handoff

import (
	"context"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	handoffcore "github.com/stainless-code/verdict/internal/core/handoff"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a handoff run.
type Request = readcommon.Request

const usageHandoff = "usage: verdict handoff <owner/repo> <pr> [--min-severity <s>] " +
	"[--paths <glob>]... [--format md] [--json]"

const (
	minSeverityFlag = "--min-severity"
	pathsFlag       = "--paths"
	formatFlag      = "--format"
	formatMD        = "md"
)

// Run executes `handoff <owner/repo> <pr>`, returning the process exit code.
// The projection is read-only, so it exits 0 whenever it renders.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	opts, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "handoff"); done {
		return code
	}
	if req.Repo == "" || req.Pr == "" {
		return r.ReportUsage(&cli.UsageError{Message: usageHandoff})
	}
	resp, ref := handoffcore.Run(ctx, handoffRequest(r, req, opts))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		r.Emit("handoff", resp.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire())
		return cli.ExitOK
	}
	r.HumanLines(resp.Human)
	return cli.ExitOK
}

func handoffRequest(r *cli.Runner, req Request, opts options) *handoffcore.Request {
	var cmdr core.Commander
	if req.Runtime != nil {
		cmdr = req.Commander
	}
	read := r.Read(req, cmdr)
	return &handoffcore.Request{
		ReadRequest: read,
		MinSeverity: opts.minSeverity,
		Paths:       opts.paths,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

type options struct {
	minSeverity string
	paths       []string
	formatSet   bool
}

func parseFlags(r *cli.Runner, args []string) (options, int, bool) {
	opts := options{}
	for i := 0; i < len(args); i++ {
		next, code, done := applyFlag(r, args, i, &opts)
		if done {
			return opts, code, true
		}
		i = next
	}
	code, done := formatPrecedence(r, opts)
	return opts, code, done
}

func applyFlag(r *cli.Runner, args []string, i int, opts *options) (int, int, bool) {
	switch args[i] {
	case minSeverityFlag:
		return parseValue(r, args, i, "critical|important|informational|off",
			func(v string) { opts.minSeverity = v })
	case pathsFlag:
		return parseValue(r, args, i, "<glob>", func(v string) { opts.paths = append(opts.paths, v) })
	case formatFlag:
		next, code, done := parseFormat(r, args, i)
		opts.formatSet = !done
		return next, code, done
	default:
		return i, r.ReportUsage(&cli.UsageError{Message: usageHandoff}), true
	}
}

// formatPrecedence settles an explicit --format md against the JSON mode the
// prefs phase derived: the flag beats REVIEW_OUTPUT_FORMAT and the compact
// prefs file, the repo-wide order of flags over env over prefs. --json and
// --format md on one command line are a contradiction, so the pair refuses
// rather than letting one silently win.
func formatPrecedence(r *cli.Runner, opts options) (int, bool) {
	if !opts.formatSet {
		return 0, false
	}
	if r.JSONPeeked {
		return r.ReportUsage(&cli.UsageError{
			Message: "--format md and --json ask for different output: pass one",
		}), true
	}
	r.JSONMode = false
	r.Output = core.FormatText
	return 0, false
}

func parseValue(
	r *cli.Runner, args []string, i int, want string, set func(string),
) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, args[i], want)
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	set(v)
	return next, 0, false
}

func parseFormat(r *cli.Runner, args []string, i int) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, args[i], "md")
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	if v != formatMD {
		return i, r.ReportUsage(&cli.UsageError{
			Message: core.Msgf("--format wants md, not %q", v),
		}), true
	}
	return next, 0, false
}
