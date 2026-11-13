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
	if code, done := r.Canceled(ctx, "show"); done {
		return code
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
		data := jq.NewObj("item", rec, "fix", nil)
		if resp.Fix != nil {
			data.Set("fix", resp.Fix.Wire())
		}
		next := "address via address-reviews mode, then post"
		if resp.Item == nil {
			next = "partial results — a miss is not proof of absence"
		}
		env := core.RenderEnvelope("show", req.Repo, req.Pr, resp.At,
			next, resp.Warnings,
			resp.Receipt.Apply(data), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	if resp.Item == nil {
		fmt.Fprintln(r.Human, "show: sig not in partial results — a miss is not proof of absence")
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
		switch a {
		case "--sig":
			v, next, u := cli.FlagValue(args, i, a, "<sig>")
			if u != nil {
				return "", r.ReportUsage(u), true
			}
			sig = v
			i = next + 1
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for show (want --sig, --json)", a)}), true
		}
	}
	return sig, 0, false
}
