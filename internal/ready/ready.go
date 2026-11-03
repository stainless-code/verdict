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
	check, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before ready completed"}, cli.ExitBroke)
	}
	resp, ref := readycore.Run(ctx, readyRequest(r, req, check))
	if ref != nil {
		if check {
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
	read.Progress = runnerProgress{r}
	read.Commander = coreexec.Runner{}
	return &readycore.Request{
		ReadRequest: read,
		Check:       check,
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

// parseFlags parses the ready flags.
func parseFlags(r *cli.Runner, args []string) (bool, int, bool) {
	check := false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		default:
			return false, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for ready (want --json, --check)", a)}), true
		}
	}
	return check, 0, false
}
