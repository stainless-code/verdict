// Package ready is the ready verb's CLI adapter: argv parsing, one call
// into the readycore service, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/ready.
package ready

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a ready run.
type Request = readcommon.Request

// Run executes `ready <owner/repo> <pr> [--json, --check]`, returning the
// exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	q := readyRequest(r, req, false)
	if err := parseFlags(args, q); err != nil {
		return r.ReportUsage(err)
	}
	if code, done := r.Canceled(ctx, "ready"); done {
		return code
	}
	resp, ref := readycore.Run(ctx, q)
	if ref != nil {
		if q.Check {
			return r.RefuseExit(ref, cli.ExitBroke)
		}
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("ready", req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	if resp.GateFailed {
		return 1
	}
	return 0
}

// readyRequest injects the runner's clock, progress observer, and command
// runner into the typed service request.
func readyRequest(r *cli.Runner, req Request, check bool) *readycore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	if read.Commander == nil {
		read.Commander = coreexec.Runner{}
	}
	return &readycore.Request{
		ReadRequest: read,
		Check:       check,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

func parseFlags(args []string, q *readycore.Request) *cli.UsageError {
	for len(args) > 0 {
		flag := args[0]
		if flag == "--check" {
			q.Check = true
			args = args[1:]
			continue
		}
		if len(args) < 2 {
			return &cli.UsageError{Message: "missing ready flag operand: " + flag}
		}
		value := args[1]
		args = args[2:]
		switch flag {
		case "--wait-for":
			q.WaitFor = value
		case "--timeout":
			q.Timeout = value
		default:
			return &cli.UsageError{Message: "unknown ready flag: " + flag}
		}
	}
	return nil
}
