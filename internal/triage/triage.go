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
	Env  map[string]string
	Repo string
	Pr   string
	Root string
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
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before triage completed"}, cli.ExitBroke)
	}
	resp, ref := triagecore.Run(ctx, triageRequest(r, req, flags))
	if ref != nil {
		return refuse(r, ref)
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

// refuse replays the service's accumulated warnings before rendering.
func refuse(r *cli.Runner, ref *core.RefusalError) int {
	if ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	return r.Refuse(ref)
}

// triageRequest injects the runner's clock and progress observer into the
// typed service request.
func triageRequest(r *cli.Runner, req Request, flags flags) *triagecore.Request {
	read := core.ReadRequest{
		Runtime: &core.Runtime{Env: req.Env, Now: r.Now, Progress: runnerProgress{r}},
		Repo:    req.Repo,
		Pr:      req.Pr,
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

// runnerProgress routes core's progress observations onto the runner's
// frozen streams: warnings keep the `warn: ` prefix and accumulate.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

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
		a := args[i]
		switch a {
		case "--sig", "--status", "--rationale":
			if i+1 < len(args) {
				i++
				setFlag(&f, a, args[i])
			} else {
				setFlag(&f, a, "")
			}
		case "--force":
			f.force = true
		case "--dry-run":
			f.dryRun = true
		default:
			msg := fmt.Sprintf("unknown flag '%s' for triage "+
				"(want --sig, --status, --rationale, --force, --dry-run, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
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
