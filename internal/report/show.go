package report

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// RunShow executes `show <owner/repo> <pr> --sig <sig> [--json]`: one full
// finding record.
func RunShow(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	sig, code, done := parseShowFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: "canceled before show completed"}, cli.ExitBroke)
	}
	resp, ref := reportcore.RunShow(ctx, &core.ShowRequest{
		ReadRequest: readRequest(r, req),
		Sig:         sig,
		Warnings:    append([]string{}, r.Warnings...),
	})
	if ref != nil {
		return r.Refuse(ref)
	}
	var rec any
	if resp.Item != nil {
		rec = resp.Item.Raw
	}
	if r.JSONMode {
		env := core.RenderEnvelope("show", req.Repo, req.Pr, resp.At,
			"address via address-reviews mode, then post", resp.Warnings,
			jq.NewObj("item", rec), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	fmt.Fprint(r.Human, jq.PrettyOrdered(rec))
	return cli.ExitOK
}

// parseShowFlags parses the show flags.
func parseShowFlags(r *cli.Runner, args []string) (string, int, bool) {
	sig := ""
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a {
		case "--sig":
			sig = next
			i += 2
		case flagJSON:
			r.JSONMode = true
			routeHuman(r)
			i++
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for show (want --sig, --json)", a)}), true
		}
	}
	return sig, 0, false
}
