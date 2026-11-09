package report

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
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
	call := &core.NextRequest{
		ReadRequest: readRequest(r, req),
		Status:      statusF,
		Coverage:    covMode,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
	var resp *core.NextResponse
	var ref *core.RefusalError
	if covMode {
		resp, ref = reportcore.RunNextCoverage(call)
	} else {
		resp, ref = reportcore.RunNext(ctx, call)
	}
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
	if covMode {
		env := core.RenderEnvelope("next", req.Repo, req.Pr, resp.At, coverageNextLine(resp.Coverage),
			resp.Warnings, jq.NewObj("coverage", resp.Coverage), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	next := "none — nothing to act on"
	var item any
	if resp.Item != nil {
		next = "address via address-reviews mode, then post"
		item = resp.Item.Wire()
	}
	env := core.RenderEnvelope("next", req.Repo, req.Pr, resp.At, next,
		resp.Warnings, jq.NewObj("item", item), nil)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return cli.ExitOK
}

// parseNextFlags parses the next flags: --status takes any value (report
// validates it), --coverage selects the read mode, --json flips machine mode.
// --coverage and --status are mutually exclusive: one picks a finding mode, the
// other a coverage mode, so combining them is a usage error rather than a
// silent precedence rule.
func parseNextFlags(r *cli.Runner, args []string) (string, bool, int, bool) {
	statusF := statusOpen
	statusSet, covMode := false, false
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case flagStatus:
			v, next, u := cli.FlagValue(args, i, a, "<status>")
			if u != nil {
				return "", false, r.ReportUsage(u), true
			}
			statusF = v
			statusSet = true
			i = next + 1
		case flagCoverage:
			covMode = true
			i++
		default:
			return "", false, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for next (want --status, --coverage, --json)", a)}), true
		}
	}
	if covMode && statusSet {
		return "", false, r.ReportUsage(&cli.UsageError{Message: "--coverage and --status are mutually exclusive"}), true
	}
	return statusF, covMode, 0, false
}

// coverageNextLine is the envelope's one-line next action for the coverage
// mode: a nudge, never a gate.
func coverageNextLine(rep *core.CoverageReport) string {
	switch {
	case rep == nil:
		return "none — nothing to act on"
	case rep.Chunked:
		return "chunks[] is the attestation for this bundle"
	case rep.Percent != nil && *rep.Percent == 100:
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
