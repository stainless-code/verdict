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
	statusF, code, done := parseNextFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "next"); done {
		return code
	}
	resp, ref := reportcore.RunNext(ctx, &core.NextRequest{
		ReadRequest: readRequest(r, req),
		Status:      statusF,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	})
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
// validates it), --json flips machine mode.
func parseNextFlags(r *cli.Runner, args []string) (string, int, bool) {
	statusF := statusOpen
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case flagStatus:
			v, next, u := cli.FlagValue(args, i, a, "<status>")
			if u != nil {
				return "", r.ReportUsage(u), true
			}
			statusF = v
			i = next + 1
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for next (want --status, --json)", a)}), true
		}
	}
	return statusF, 0, false
}

// nextStoreError owns the unreadable-store path: in machine mode the
// report envelope renders from the classified refusal (nil means the
// invalid-status path produced no report), else the fixed human line.
func nextStoreError(r *cli.Runner, resp *core.NextResponse) int {
	if !r.JSONMode {
		fmt.Fprintln(r.Diag, "next: cannot read store (run post or record first)")
		return cli.ExitRefused
	}
	ref := resp.ReportRefusal
	if ref == nil {
		return r.Refuse(&cli.RefusalError{Code: "report-failed", Message: "next: report produced no output"})
	}
	env := core.RenderEnvelope("report", r.Repo, r.Pr, resp.At, "",
		ref.Warnings, nil, &core.SchemaError{
			Code:    ref.Code,
			Message: ref.Message + "\n" + cli.HintFor(ref.Code),
		})
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return cli.ExitRefused
}
