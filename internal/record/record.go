// Package record is the record/post family's CLI adapter: argv parsing
// (same usage text and exit 2), one call into the recordcore or postcore
// service, envelope/human rendering, and refusal-vs-cancel exit mapping.
// Domain logic lives in internal/core/record and internal/core/post.
package record

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved record invocation.
type Request struct {
	Env    map[string]string
	Forces *cli.GlobalForces
	Repo   string
	Pr     string
	Root   string
}

// Run executes `record <owner/repo> <local-slug> [--json]`, returning the
// process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	setupRunner(r, req)
	if code, done := parseRecordFlags(r, args); done {
		return code
	}
	if code, done := r.Canceled(ctx, "record"); done {
		return code
	}
	resp, ref := recordcore.Run(ctx, recordRequest(r, req))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("record", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// recordRequest builds the core request from the runner and invocation.
func recordRequest(r *cli.Runner, req Request) *core.RecordRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}},
		Repo:    req.Repo,
		Pr:      req.Pr,
		Forces:  req.Forces,
		Root:    &root,
	}
	return &core.RecordRequest{
		ReadRequest: read,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// setupRunner records the invocation for envelopes and usage lines.
func setupRunner(r *cli.Runner, req Request) {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = "verdict record " + req.Repo + " " + req.Pr
	if r.JSONMode {
		r.Rentry += " --json"
	}
}

// parseRecordFlags rejects leftovers: record takes no verb-local flags.
func parseRecordFlags(r *cli.Runner, args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	return r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for record (want --json)", args[0])}), true
}
