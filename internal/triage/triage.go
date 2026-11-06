// Package triage is the triage verb's CLI adapter: argv parsing, one call
// into the triagecore service, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/triage.
package triage

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved triage invocation.
type Request struct {
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// Run executes `triage <owner/repo> <slug> --sig <s> --status <st>
// --rationale <r> [--force] [--dry-run] [--json]`, returning the process exit
// code. Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "triage"); done {
		return code
	}
	resp, ref := triagecore.Run(ctx, triageRequest(r, req, flags))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	fmt.Fprintln(r.Human, resp.Human)
	if r.JSONMode {
		env := core.RenderEnvelope("triage", r.Repo, r.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// triageRequest injects the runner's clock and progress observer into the
// typed service request.
func triageRequest(r *cli.Runner, req Request, flags flags) *triagecore.Request {
	read := core.ReadRequest{
		Runtime:  &core.Runtime{Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}},
		Repo:     req.Repo,
		Pr:       req.Pr,
		RepoRoot: req.RepoRoot,
	}
	out := &triagecore.Request{
		Sig:       flags.sig,
		Status:    flags.status,
		Rationale: flags.rationale,
		Force:     flags.force,
		DryRun:    flags.dryRun,
	}
	out.ReadRequest = read
	return out
}

// flags carries the parsed triage options.
type flags struct {
	sig       string
	status    string
	rationale string
	force     bool
	dryRun    bool
}

// parseFlags strips triage's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	for i := 0; i < len(args); i++ {
		next, code, done := parseToken(r, &f, args, i)
		if done {
			return f, code, true
		}
		i = next
	}
	return f, 0, false
}

// parseToken consumes one triage argument, returning the next index plus a
// usage refusal when the token is unknown or a value flag lost its operand.
func parseToken(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	if want, ok := triageWants[a]; ok {
		v, next, u := cli.FlagValue(args, i, a, want)
		if u != nil {
			return i, r.ReportUsage(u), true
		}
		setFlag(f, a, v)
		return next, 0, false
	}
	switch a {
	case "--force":
		f.force = true
	case "--dry-run":
		f.dryRun = true
	default:
		msg := fmt.Sprintf("unknown flag '%s' for triage "+
			"(want --sig, --status, --rationale, --force, --dry-run, --json)", a)
		return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
	}
	return i, 0, false
}

// triageWants names each value flag's accepted operand for the missing-value
// usage error.
var triageWants = map[string]string{
	"--sig":       "<sig>",
	"--status":    "<status>",
	"--rationale": "<text>",
}

// setFlag stores one value-taking flag.
func setFlag(f *flags, name, val string) {
	switch name {
	case "--sig":
		f.sig = val
	case "--status":
		f.status = val
	case "--rationale":
		f.rationale = val
	}
}
