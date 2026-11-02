package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// RunNext executes `next <owner/repo> <pr> [--status, --json]`: the
// top-ranked actionable finding plus the command to act on it. The service
// is called directly; the small CLI-only replay below reproduces the shell's
// duplicated prefs warnings on the internal report's refusal envelope.
func RunNext(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	statusF, code, done := parseNextFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: "canceled before next completed"}, cli.ExitBroke)
	}
	sub, setupRef := replaySetup(r, req)
	if setupRef != nil {
		return nextStoreError(r, sub, setupRef)
	}
	resp, ref := reportcore.RunNext(ctx, &core.NextRequest{
		ReadRequest:    readRequest(r, req),
		Status:         statusF,
		Effective:      r.Effective,
		Warnings:       append([]string{}, r.Warnings...),
		ReportWarnings: sub.Warnings,
	})
	if ref != nil {
		r.Warnings = ref.Warnings
		return r.Refuse(ref)
	}
	if resp.StoreUnreadable {
		return nextStoreError(r, sub, resp.ReportRefusal)
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
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a {
		case "--status":
			statusF = next
			i += 2
		case flagJSON:
			r.JSONMode = true
			routeHuman(r)
			i++
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for next (want --status, --json)", a)}), true
		}
	}
	return statusF, 0, false
}

// replaySetup reproduces the shell's internal report re-invocation just far
// enough to reproduce its warnings: prefs are re-resolved from the same
// environment (duplicating prefs warnings exactly like the subshell) and the
// empty global forces are applied. Any setup refusal is the sub-report's
// envelope.
func replaySetup(r *cli.Runner, req Request) (*cli.Runner, *cli.RefusalError) {
	sub := &cli.Runner{
		Cmd:          "report",
		Repo:         req.Repo,
		Pr:           req.Pr,
		Out:          io.Discard,
		Diag:         io.Discard,
		Human:        io.Discard,
		Now:          r.Now,
		EnvNoColor:   req.Env["NO_COLOR"] != "",
		JSONMode:     true,
		JSONPeeked:   true,
		PrefColorSet: r.PrefColorSet,
		Warnings:     append([]string{}, r.Warnings...),
	}
	prefs, ref := cli.ResolvePrefs(sub, req.Env, os.ReadFile)
	if ref != nil {
		sub.Effective = cli.Defaults()
		return sub, asRefusal(ref)
	}
	sub.Effective = prefs.Effective
	if ref := cli.ApplyGlobalForces(sub, &cli.GlobalForces{}, subNoColor(sub, req.Env)); ref != nil {
		return sub, asRefusal(ref)
	}
	return sub, nil
}

// asRefusal recovers the typed refusal carried by a prefs error (usage
// errors cannot arise on this path; the fallback keeps the message).
func asRefusal(err error) *cli.RefusalError {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
		return ref
	}
	return &cli.RefusalError{Code: "internal", Message: err.Error()}
}

// subNoColor ports the NO_COLOR fallback for the sub-run: an explicitly
// set (even empty) NO_COLOR is respected, otherwise JSON mode defaults on.
func subNoColor(sub *cli.Runner, env map[string]string) bool {
	if sub.EnvNoColor {
		return true
	}
	if _, set := env["NO_COLOR"]; !set && sub.JSONMode {
		return true
	}
	return false
}

// nextStoreError ports the unreadable-store path: in machine mode the
// report envelope replays verbatim (nil ref means the invalid-status replay
// produced no output), else the fixed human line.
func nextStoreError(r *cli.Runner, sub *cli.Runner, ref *cli.RefusalError) int {
	if !r.JSONMode {
		fmt.Fprintln(r.Diag, "next: cannot read store (run post or record first)")
		return cli.ExitRefused
	}
	if ref == nil {
		return r.Refuse(&cli.RefusalError{Code: "report-failed", Message: "next: report produced no output"})
	}
	env := core.RenderEnvelope("report", r.Repo, r.Pr, core.NowUTC(sub.Now()), "",
		sub.Warnings, nil, &core.SchemaError{
			Code:    ref.Code,
			Message: ref.Message + "\n" + cli.HintFor(ref.Code),
		})
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return cli.ExitRefused
}
