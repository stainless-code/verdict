// Package fix is the fix verb's CLI adapter: argv parsing, one call into the
// fixcore service, warning routing, envelope rendering, and exit mapping.
// Domain logic — sig selection, the ledger, and the scope audit — lives in
// internal/core/fix.
package fix

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
)

// Request carries the resolved fix invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `fix <owner/repo> <local-slug> [--sig <s>] [--json]`,
// returning the process exit code. Human progress goes to the runner's human
// stream; --json mode prints exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = "verdict fix " + req.Repo + " " + req.Pr
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if flags.sig != "" {
		r.Rentry += " --sig " + flags.sig
	}
	if flags.json {
		r.Rentry += " --json"
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{
			Code:    "canceled",
			Message: "canceled before fix completed",
		}, cli.ExitBroke)
	}
	resp, ref := fixcore.Run(ctx, fixRequest(r, req, flags))
	if ref != nil {
		return refuse(r, ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("fix", r.Repo, r.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprintln(r.Out, env.MarshalPretty())
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

// fixRequest injects the runner's clock, progress observer, and the
// production commander into the typed service request.
func fixRequest(r *cli.Runner, req Request, f flags) *fixcore.Request {
	out := &fixcore.Request{Sig: f.sig, Human: r.Human}
	out.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: runnerProgress{r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr,
	}
	return out
}

// runnerProgress routes core's warning observations onto the runner's frozen
// diagnostic stream.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line (fix writes its human bytes through
// the request's writer, so this is only a fallback).
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// flags carries the parsed fix options.
type flags struct {
	sig  string
	json bool
}

// parseFlags strips fix's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--sig":
			if i+1 < len(args) {
				i++
				f.sig = args[i]
			} else {
				f.sig = ""
			}
		case "--json":
			f.json = true
		default:
			msg := fmt.Sprintf(
				"unknown flag '%s' for fix (want --sig <sig>, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}
