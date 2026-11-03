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
	if code, done := r.Canceled(ctx, "check-tree"); done {
		return code
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
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{}
	return &checktreecore.Request{
		ReadRequest: read,
		Expect:      expect,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parseFlags parses the check-tree flags.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	expect := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--expect":
			v, next, u := cli.FlagValue(args, i, a, "<prefix,...>")
			if u != nil {
				return "", r.ReportUsage(u), true
			}
			expect = v
			i = next + 1
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for check-tree (want --expect <prefix,...>, --json)", a)}), true
		}
	}
	return expect, 0, false
}
