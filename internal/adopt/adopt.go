// Package adopt is the adopt verb's CLI adapter: argv parsing, one call
// into the adoptcore service, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/adopt.
package adopt

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved adopt invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `adopt <owner/repo> <pr> --from <local-slug> [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	from, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before adopt completed"}, cli.ExitBroke)
	}
	resp, ref := adoptcore.Run(ctx, adoptRequest(r, req, from))
	if ref != nil {
		return refuse(r, ref)
	}
	r.Warnings = resp.Warnings
	fmt.Fprintln(r.Human, resp.Human)
	if r.JSONMode {
		env := core.RenderEnvelope("adopt", req.Repo, req.Pr, resp.At, resp.Next,
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

// adoptRequest injects the runner's clock and progress observer into the
// typed service request.
func adoptRequest(r *cli.Runner, req Request, from string) *adoptcore.Request {
	read := core.ReadRequest{
		Runtime: &core.Runtime{Env: req.Env, Now: r.Now, Progress: runnerProgress{r}},
		Repo:    req.Repo,
		Pr:      req.Pr,
	}
	out := &adoptcore.Request{From: from}
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

// parseFlags strips adopt's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	from := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--from":
			if i+1 < len(args) {
				i++
				from = args[i]
			} else {
				from = ""
			}
		default:
			msg := fmt.Sprintf(msgUnknownFlag, a)
			code := r.ReportUsage(&cli.UsageError{Message: msg})
			return "", code, true
		}
	}
	return from, 0, false
}

// msgUnknownFlag is the frozen unknown-flag usage text.
const msgUnknownFlag = "unknown flag '%s' for adopt (want --from <local-slug>, --json)"
