package record

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/jq"
)

// RunPost executes
// `post <owner/repo> <pr> [--approve[=bool]] [--request-changes] [--bind]
// [--force] [--review-file <path>] [--dry-run] [--json]`, returning the
// process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func RunPost(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	dir := ".review/" + req.Pr
	f, code, done := parsePostFlags(r, args, dir)
	if done {
		return code
	}
	r.Rentry = postRentry(req.Repo, req.Pr, f, r.JSONMode)
	if code, done := r.Canceled(ctx, "post"); done {
		return code
	}
	resp, ref := postcore.Run(ctx, postRequest(r, req, f))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("post", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// postRequest builds the core post request from the runner and invocation:
// the injected runtime (environment, clock, progress, and the production
// split-stream commander), the render configuration, and the adapter-routed
// flags.
func postRequest(r *cli.Runner, req Request, f core.PostFlags) *core.PostRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr, Forces: req.Forces, Root: &root,
	}
	return &core.PostRequest{
		ReadRequest: read,
		Flags:       f,
		Effective:   r.Effective,
		JSONMode:    r.JSONMode,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parsePostFlags strips post's flags, reporting unknown ones. A value flag
// with no operand is a usage error.
func parsePostFlags(r *cli.Runner, args []string, dir string) (core.PostFlags, int, bool) {
	f := core.PostFlags{Approve: "false", ReviewFile: dir + "/review.json"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == postApproveFlag, a == "--review-file":
			v, next, u := cli.FlagValue(args, i, a, postWants[a])
			if u != nil {
				return f, r.ReportUsage(u), true
			}
			setPostValue(&f, a, v)
			i = next
		case strings.HasPrefix(a, postApproveFlag+"="):
			f.Approve = strings.TrimPrefix(a, postApproveFlag+"=")
		case a == "--request-changes":
			f.RequestChanges = true
		case a == "--bind":
			f.Bind = true
		case a == "--force":
			f.Force = true
		case a == "--dry-run":
			f.DryRun = true
		default:
			msg := fmt.Sprintf("unknown flag '%s' for post (want --approve[=bool], --request-changes, "+
				"--bind, --force, --review-file <path>, --dry-run, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// postApproveFlag names the --approve flag (which also takes the documented
// --approve=bool form).
const postApproveFlag = "--approve"

// postWants names each value flag's accepted operand for the missing-value
// usage error.
var postWants = map[string]string{
	postApproveFlag: "true|false",
	"--review-file": "<path>",
}

// setPostValue stores one value-taking post flag's value.
func setPostValue(f *core.PostFlags, name, val string) {
	if name == postApproveFlag {
		f.Approve = val
	} else {
		f.ReviewFile = val
	}
}

// postRentry rebuilds the exact re-run command recorded after flag parsing
// (flags included, in flag order, so a retry reproduces this invocation).
func postRentry(repo, pr string, f core.PostFlags, jsonMode bool) string {
	var sb strings.Builder
	sb.WriteString("verdict post " + repo + " " + pr)
	if f.Approve != "false" {
		sb.WriteString(" --approve=" + f.Approve)
	}
	if f.RequestChanges {
		sb.WriteString(" --request-changes")
	}
	if f.Bind {
		sb.WriteString(" --bind")
	}
	if f.Force {
		sb.WriteString(" --force")
	}
	if f.ReviewFile != ".review/"+pr+"/review.json" {
		sb.WriteString(" --review-file " + f.ReviewFile)
	}
	if f.DryRun {
		sb.WriteString(" --dry-run")
	}
	if jsonMode {
		sb.WriteString(" --json")
	}
	return sb.String()
}
