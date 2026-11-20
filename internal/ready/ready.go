// Package ready is the ready verb's CLI adapter: argv parsing, one call
// into the readycore service, warning routing, envelope rendering, and exit
// mapping. Domain logic lives in internal/core/ready.
package ready

import (
	"context"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a ready run.
type Request = readcommon.Request

// Run executes `ready <owner/repo> <pr> [--json, --check, --filter]`,
// returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	q := readyRequest(r, req)
	if err := parseFlags(args, q); err != nil {
		return r.ReportUsage(err)
	}
	if code, done := r.Canceled(ctx, "ready"); done {
		return code
	}
	resp, ref := readycore.Run(ctx, q)
	if ref != nil {
		return refuseReady(r, q, ref)
	}
	r.Warnings = resp.Warnings
	r.Emit("ready", req.Repo, req.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire())
	if resp.GateFailed {
		return 1
	}
	return 0
}

// readyRequest injects the runner's clock, progress observer, and command
// runner into the typed service request.
func readyRequest(r *cli.Runner, req Request) *readycore.Request {
	var cmdr core.Commander
	if req.Runtime != nil {
		cmdr = req.Commander
	}
	read := r.Read(req, cmdr)
	if read.Commander == nil {
		read.Commander = coreexec.Runner{}
	}
	return &readycore.Request{
		ReadRequest: read,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

func refuseReady(r *cli.Runner, q *readycore.Request, ref *core.RefusalError) int {
	if q.Check && ref.Code != "filtered-out" {
		return r.RefuseExit(ref, cli.ExitBroke)
	}
	return r.Refuse(ref)
}

func parseFlags(args []string, q *readycore.Request) *cli.UsageError {
	var usage *cli.UsageError
	for len(args) > 0 {
		flag := args[0]
		switch flag {
		case "--check":
			q.Check = true
			args = args[1:]
		case "--filter":
			q.Filter = true
			args = args[1:]
		case "--wait-for", "--timeout":
			args, usage = valueFlag(args, flag, q)
			if usage != nil {
				return usage
			}
		default:
			return &cli.UsageError{Message: "unknown ready flag: " + flag}
		}
	}
	return nil
}

func valueFlag(args []string, flag string, q *readycore.Request) ([]string, *cli.UsageError) {
	if len(args) < 2 {
		return nil, &cli.UsageError{Message: "missing ready flag operand: " + flag}
	}
	if flag == "--wait-for" {
		q.WaitFor = args[1]
	} else {
		q.Timeout = args[1]
	}
	return args[2:], nil
}
