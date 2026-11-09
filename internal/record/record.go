// Package record is the record/post family's CLI adapter: argv parsing
// (same usage text and exit 2), one call into the recordcore or postcore
// service, envelope/human rendering, and refusal-vs-cancel exit mapping.
// Domain logic lives in internal/core/record and internal/core/post.
package record

import (
	"context"
	"fmt"
	"strings"

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
// [--cost-names[=<names>]] [--json]`, returning the
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
	rc.Flags.CostNames = f.costNames
	rc.Flags.RequireCoverage = f.requireCoverage
	rc.Flags.RequireCoverage = f.requireCoverage
	rc.Flags.CostPanel = f.costPanel
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
	costPanel       []string
	dryRun          bool
	strict          bool
	costNames       bool
	requireCoverage bool
}

// parseRecordFlags strips record's verb-local flags, reporting unknown ones.
func parseRecordFlags(r *cli.Runner, args []string) (recordFlags, int, bool) {
	var f recordFlags
	for _, arg := range args {
		switch {
		case arg == "--dry-run":
			f.dryRun = true
		case arg == "--strict":
			f.strict = true
		case arg == "--cost-names":
			f.costNames = true
		case arg == "--require-coverage":
			f.requireCoverage = true
		case strings.HasPrefix(arg, "--cost-names="):
			f.costNames = true
			panel, ok := splitPanel(strings.TrimPrefix(arg, "--cost-names="))
			if !ok {
				return f, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
					"empty name in '%s': --cost-names takes one name per seat, in order "+
						"(want --cost-names=lead,lens-1)", arg)}), true
			}
			f.costPanel = panel
		default:
			return f, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for record (want --dry-run, --strict, --cost-names, --require-coverage, --json)", arg)}), true
		}
	}
	return f, 0, false
}

// splitPanel splits one --cost-names value into panel slugs. An empty value
// means no explicit panel, which leaves the pinned panel as the source; an
// empty element inside the list refuses, because dropping it would shift
// every later seat onto the wrong model.
func splitPanel(value string) ([]string, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, true
	}
	parts := strings.Split(value, ",")
	panel := make([]string, 0, len(parts))
	for _, part := range parts {
		slug := strings.TrimSpace(part)
		if slug == "" {
			return nil, false
		}
		panel = append(panel, slug)
	}
	return panel, true
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
	if f.costNames {
		if len(f.costPanel) == 0 {
			s += " --cost-names"
		} else {
			s += " --cost-names=" + strings.Join(f.costPanel, ",")
		}
	}
	if jsonMode {
		s += " --json"
	}
	return s
}
