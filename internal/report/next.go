package report

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// next envelope one-liners and the complete-coverage percentage, named so the
// two render paths cannot drift from the prose a caller reads.
const (
	nextNone        = "none — nothing to act on"
	nextAddress     = "address via address-reviews mode, then post"
	percentComplete = 100
)

// RunNext executes `next <owner/repo> <pr> [--status, --json]`: the
// top-ranked actionable finding plus the command to act on it. The service
// is called directly: the adapter resolved prefs once, so the report run
// behind next carries the same warnings instead of re-resolving them.
func RunNext(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	statusF, covMode, code, done := parseNextFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "next"); done {
		return code
	}
	resp, ref := callNext(ctx, r, req, statusF, covMode)
	if ref != nil {
		r.Warnings = ref.Warnings
		return r.Refuse(ref)
	}
	if resp.StoreUnreadable {
		return nextStoreError(r, resp)
	}
	fmt.Fprintln(r.Human, resp.Human)
	if !r.JSONMode {
		return cli.ExitOK
	}
	return renderNextJSON(r, req, resp, covMode)
}

// callNext dispatches to the finding or the coverage mode. Coverage has its own
// entry point, so the two modes never share a path that could drift.
func callNext(ctx context.Context, r *cli.Runner, req Request, statusF string, covMode bool) (*core.NextResponse, *core.RefusalError) {
	call := &core.NextRequest{
		ReadRequest: readRequest(r, req),
		Status:      statusF,
		Coverage:    covMode,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
	if covMode {
		return reportcore.RunNextCoverage(call)
	}
	return reportcore.RunNext(ctx, call)
}

// renderNextJSON writes the machine envelope for either mode.
func renderNextJSON(r *cli.Runner, req Request, resp *core.NextResponse, covMode bool) int {
	if covMode {
		env := core.RenderEnvelope("next", req.Repo, req.Pr, resp.At, coverageNextLine(resp.Coverage),
			resp.Warnings, jq.NewObj("coverage", resp.Coverage), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	next := nextNone
	if resp.Receipt != nil {
		next = "partial results — a miss is not proof of absence"
	}
	var item any
	if resp.Item != nil {
		next = nextAddress
		item = resp.Item.Wire()
	}
	env := core.RenderEnvelope("next", req.Repo, req.Pr, resp.At, next,
		resp.Warnings, resp.Receipt.Apply(jq.NewObj("item", item)), nil)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return cli.ExitOK
}

// nextFlags is one parse of the next arguments: the status filter, whether the
// caller set it, and whether coverage mode was selected.
type nextFlags struct {
	status    string
	statusSet bool
	coverage  bool
}

// parseNextFlags parses the next flags: --status takes any value (report
// validates it), --coverage selects the read mode, --json flips machine mode.
// --coverage and --status are mutually exclusive: one picks a finding mode, the
// other a coverage mode, so combining them is a usage error rather than a
// silent precedence rule.
func parseNextFlags(r *cli.Runner, args []string) (string, bool, int, bool) {
	flags, code, done := parseNextArgs(r, args)
	if done {
		return "", false, code, true
	}
	if flags.coverage && flags.statusSet {
		return "", false, r.ReportUsage(&cli.UsageError{Message: "--coverage and --status are mutually exclusive"}), true
	}
	return flags.status, flags.coverage, 0, false
}

// parseNextArgs walks the arguments once and reports the flags it understood.
func parseNextArgs(r *cli.Runner, args []string) (nextFlags, int, bool) {
	flags := nextFlags{status: statusOpen}
	for i := 0; i < len(args); {
		a := args[i]
		switch a {
		case flagStatus:
			v, next, u := cli.FlagValue(args, i, a, "<status>")
			if u != nil {
				return nextFlags{}, r.ReportUsage(u), true
			}
			flags.status, flags.statusSet, i = v, true, next+1
		case flagCoverage:
			flags.coverage, i = true, i+1
		default:
			return nextFlags{}, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for next (want --status, --coverage, --json)", a)}), true
		}
	}
	return flags, 0, false
}

// coverageNextLine is the envelope's one-line next action for the coverage
// mode: a nudge, never a gate.
func coverageNextLine(rep *core.CoverageReport) string {
	switch {
	case rep == nil:
		return "none — nothing to act on"
	case rep.Chunked:
		return "chunks[] is the attestation for this bundle"
	case rep.Percent != nil && *rep.Percent == percentComplete:
		return "coverage complete"
	default:
		return "stamp review.json coverage[] for every changed file, then re-run"
	}
}

// nextStoreError owns the unreadable-store path: in machine mode the
// report envelope renders from the classified refusal (nil means the
// invalid-status path produced no report), else the fixed human line.
func nextStoreError(r *cli.Runner, resp *core.NextResponse) int {
	ref := resp.ReportRefusal
	code := cli.ExitRefused
	if ref != nil {
		code = cli.ExitFor(ref, code)
	}
	if !r.JSONMode {
		fmt.Fprintln(r.Diag, "next: cannot read store (run post or record first)")
		return code
	}
	if ref == nil {
		return r.Refuse(&cli.RefusalError{Code: "report-failed", Message: "next: report produced no output"})
	}
	env := core.RenderEnvelope("report", r.Repo, r.Pr, resp.At, "",
		ref.Warnings, nil, &core.SchemaError{
			Code:    ref.Code,
			Message: ref.Message + "\n" + cli.HintFor(ref.Code),
		})
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return code
}
