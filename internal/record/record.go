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
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved record invocation.
type Request struct {
	Env      map[string]string
	Forces   *cli.GlobalForces
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// Run executes `record <owner/repo> <local-slug> [--dry-run] [--strict]
// [--json]`, returning the
// process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req *Request) int {
	setupRunner(r, req)
	f, code, done := parseRecordFlags(r, args)
	if done {
		return code
	}
	r.Rentry = recordRentry(req, f, r.JSONMode)
	if code, done := r.Canceled(ctx, "record"); done {
		return code
	}
	rc := recordRequest(r, req)
	rc.DryRun = f.dryRun
	rc.Strict = f.strict
	resp, ref := recordcore.Run(ctx, rc)
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
func recordRequest(r *cli.Runner, req *Request) *core.RecordRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime:  &core.Runtime{Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}, Commander: coreexec.Runner{}},
		Repo:     req.Repo,
		Pr:       req.Pr,
		Forces:   req.Forces,
		Root:     &root,
		RepoRoot: req.RepoRoot,
	}
	return &core.RecordRequest{
		ReadRequest: read,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// setupRunner records the invocation for envelopes and usage lines.
func setupRunner(r *cli.Runner, req *Request) {
	r.Repo, r.Pr = req.Repo, req.Pr
}

type recordFlags struct {
	dryRun bool
	strict bool
}

// parseRecordFlags strips record's verb-local flags, reporting unknown ones.
func parseRecordFlags(r *cli.Runner, args []string) (recordFlags, int, bool) {
	var f recordFlags
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			f.dryRun = true
		case "--strict":
			f.strict = true
		default:
			return f, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for record (want --dry-run, --strict, --json)", arg)}), true
		}
	}
	return f, 0, false
}

// recordRentry rebuilds the exact re-run command after flag parsing.
func recordRentry(req *Request, f recordFlags, jsonMode bool) string {
	s := "verdict record " + req.Repo + " " + req.Pr
	if f.dryRun {
		s += " --dry-run"
	}
	if f.strict {
		s += " --strict"
	}
	if jsonMode {
		s += " --json"
	}
	return s
}
