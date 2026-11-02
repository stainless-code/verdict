package report

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
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
	dir := readcommon.ResolveBundleDir(req.Pr)
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return r.Refuse(err)
	}
	if sig == "" {
		return r.Refuse(&cli.RefusalError{Code: "missing-sig", Message: "show needs --sig <sig> (short prefix ok)"})
	}
	full := readcommon.ResolveSig(entries, sig)
	switch full {
	case "NONE":
		return r.Refuse(&cli.RefusalError{Code: "unknown-sig", Message: "show: sig not in store"})
	case "MANY":
		return r.Refuse(&cli.RefusalError{Code: "ambiguous-sig", Message: "show: sig prefix ambiguous"})
	}
	rec := findRecord(entries, full)
	if r.JSONMode {
		env := jq.NewObj(
			"schema_version", "1",
			"verb", "show",
			"repo", r.Repo,
			"pr", cli.PrJSON(r.Pr),
			"at", cli.NowUTC(r.Now()),
			keyNext, "address via address-reviews mode, then post",
			"warnings", cliStrings(r.Warnings),
			"data", jq.NewObj("item", rec),
			"error", nil,
		)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return 0
	}
	fmt.Fprint(r.Human, jq.PrettyOrdered(rec))
	return 0
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

// findRecord selects the full-sig record (null when no entry matches, which
// only happens for unreachable sigs).
func findRecord(entries []any, full string) any {
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		if sig, _ := m.Get("sig"); sig == full {
			return e
		}
	}
	return nil
}
