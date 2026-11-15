// Package handoff is the handoff verb's CLI adapter: argv parsing, one call
// into the handoff service, warning routing, and envelope rendering. Domain
// logic lives in internal/core/handoff.
package handoff

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	handoffcore "github.com/stainless-code/verdict/internal/core/handoff"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a handoff run.
type Request = readcommon.Request

// usageHandoff is the verb's own synopsis, repeated in every usage error.
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
		env := core.RenderEnvelope("handoff", resp.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return cli.ExitOK
}

// handoffRequest injects the runner's clock and progress observer into the
// typed service request.
func handoffRequest(r *cli.Runner, req Request, opts options) *handoffcore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	return &handoffcore.Request{
		ReadRequest: read,
		MinSeverity: opts.minSeverity,
		Paths:       opts.paths,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// options carries the parsed severity floor and path globs.
type options struct {
	minSeverity string
	paths       []string
}

// parseFlags parses the verb's flags: the two value flags (one repeatable),
// the format, and the global --json the front door already stripped.
func parseFlags(r *cli.Runner, args []string) (options, int, bool) {
	opts := options{}
	for i := 0; i < len(args); i++ {
		next, code, done := applyFlag(r, args, i, &opts)
		if done {
			return opts, code, true
		}
		i = next
	}
	return opts, 0, false
}

// applyFlag parses the flag at args[i] into opts and returns the index of its
// last consumed token.
func applyFlag(r *cli.Runner, args []string, i int, opts *options) (int, int, bool) {
	switch args[i] {
	case minSeverityFlag:
		return parseValue(r, args, i, "critical|important|informational|off",
			func(v string) { opts.minSeverity = v })
	case pathsFlag:
		return parseValue(r, args, i, "<glob>", func(v string) { opts.paths = append(opts.paths, v) })
	case formatFlag:
		return parseFormat(r, args, i)
	default:
		return i, r.ReportUsage(&cli.UsageError{Message: usageHandoff}), true
	}
}

// parseValue reads one value flag and hands it to set.
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

// parseFormat reads --format and holds it to the one published value: md is
// the projection's only rendering, and the default, so naming it is explicit.
func parseFormat(r *cli.Runner, args []string, i int) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, args[i], "md")
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	if v != formatMD {
		return i, r.ReportUsage(&cli.UsageError{
			Message: fmt.Sprintf("--format wants md, not %q", v),
		}), true
	}
	return next, 0, false
}
