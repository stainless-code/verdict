// Package prwait adapts bounded PR waits to the CLI.
package prwait

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

// Run executes pr-wait and returns 1 when its condition remains unmet.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	q := &readycore.WaitRequest{ReadRequest: req, Warnings: append([]string{}, r.Warnings...)}
	if ref := Parse(args, q); ref != nil {
		return r.Refuse(ref)
	}
	if code, done := r.Canceled(ctx, "pr-wait"); done {
		return code
	}
	if q.Runtime == nil {
		q.Runtime = &core.Runtime{}
	}
	q.Now = r.Now
	if q.Commander == nil {
		q.Commander = coreexec.Runner{}
	}
	if !r.JSONMode {
		q.Observe = func(s *readycore.WaitSnapshot) { fmt.Fprintln(r.Out, jq.Compact(s)) }
	}
	resp, ref := readycore.Wait(ctx, q)
	if ref != nil {
		return r.Refuse(ref)
	}
	render(r, req, resp)
	if resp.Data.Outcome != "condition-met" {
		return 1
	}
	return 0
}

// Parse decodes the wait flags; validation is shared with the service.
func Parse(args []string, q *readycore.WaitRequest) *core.RefusalError {
	for len(args) > 0 {
		flag := args[0]
		if flag == "--once" {
			q.Once = true
			args = args[1:]
			continue
		}
		if len(args) < 2 {
			return badFlag(flag)
		}
		value := args[1]
		args = args[2:]
		switch flag {
		case "--until":
			q.Until = value
		case "--timeout":
			q.Timeout = value
		case "--interval":
			q.Interval = value
		case "--provider":
			q.Provider = value
		default:
			return badFlag(flag)
		}
	}

	return nil
}

func badFlag(flag string) *core.RefusalError {
	return &core.RefusalError{Code: "bad-flags", Message: "invalid or missing pr-wait flag operand: " + flag}
}

func render(r *cli.Runner, req readcommon.Request, resp *readycore.WaitResponse) {
	if r.JSONMode {
		env := core.RenderEnvelope("pr-wait", req.Repo, req.Pr, resp.At, resp.Next, resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	} else {
		fmt.Fprintln(r.Out, resp.Data.Wire().MarshalCompact())
	}
}
