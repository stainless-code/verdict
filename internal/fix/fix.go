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
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
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
	if r.JSONMode {
		r.Rentry += " --json"
	}
	if code, done := r.Canceled(ctx, "fix"); done {
		return code
	}
	resp, ref := fixcore.Run(ctx, fixRequest(r, req, flags))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("fix", r.Repo, r.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
	return cli.ExitOK
}

// fixRequest injects the runner's clock, progress observer, and the
// production commander into the typed service request.
func fixRequest(r *cli.Runner, req Request, f flags) *fixcore.Request {
	out := &fixcore.Request{Sig: f.sig, Human: r.Human}
	out.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr, RepoRoot: req.RepoRoot,
	}
	return out
}

// flags carries the parsed fix options.
type flags struct {
	sig string
}

// parseFlags strips fix's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--sig":
			v, next, u := cli.FlagValue(args, i, a, "<sig>")
			if u != nil {
				return f, r.ReportUsage(u), true
			}
			f.sig = v
			i = next
		default:
			msg := fmt.Sprintf(
				"unknown flag '%s' for fix (want --sig <sig>, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}
