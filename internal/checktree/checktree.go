// Package checktree is the check-tree verb's CLI adapter: argv parsing,
// one call into the checktreecore service, warning routing, envelope
// rendering, and exit mapping. Domain logic lives in
// internal/core/checktree.
package checktree

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a check-tree run.
type Request = readcommon.Request

// Run executes `check-tree [--expect <prefix,...>] [--json]`, returning the
// exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	expect, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before check-tree completed"}, cli.ExitBroke)
	}
	resp, ref := checktreecore.Run(ctx, checktreeRequest(r, req, expect))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("check-tree", "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), resp.Error)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return resp.Exit
}

// checktreeRequest injects the runner's clock, progress observer, and
// command runner into the typed service request.
func checktreeRequest(r *cli.Runner, req Request, expect string) *checktreecore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = runnerProgress{r}
	read.Commander = coreexec.Runner{}
	return &checktreecore.Request{
		ReadRequest: read,
		Expect:      expect,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// runnerProgress routes core's progress observations onto the runner's
// frozen streams: human lines and diagnostics keep their streams,
// warnings keep the `warn: ` prefix and accumulate.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// parseFlags parses the check-tree flags.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	expect := ""
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a {
		case "--expect":
			expect = next
			i += 2
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for check-tree (want --expect <prefix,...>, --json)", a)}), true
		}
	}
	return expect, 0, false
}
