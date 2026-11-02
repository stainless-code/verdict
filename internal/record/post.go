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
	if err := ctx.Err(); err != nil {
		// GO-CLI-05: cancel kills the run with exit 3 and a JSON envelope.
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before post completed"}, cli.ExitBroke)
	}
	resp, ref := postcore.Run(ctx, postRequest(r, req, f))
	if ref != nil {
		return refusePost(r, ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("post", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// refusePost renders a post failure: warnings carry onto the runner, a
// cancel exits 3 (scripts treat it as broke), anything else exits 1.
func refusePost(r *cli.Runner, ref *core.RefusalError) int {
	if ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	if ref.Code == codeCanceled {
		return r.RefuseExit(ref, cli.ExitBroke)
	}
	return r.Refuse(ref)
}

// postRequest builds the core post request from the runner and invocation:
// the injected runtime (environment, clock, progress, and the production
// split-stream commander), the render configuration, and the adapter-routed
// flags.
func postRequest(r *cli.Runner, req Request, f core.PostFlags) *core.PostRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: runnerProgress{r},
			Commander: coreexec.SplitCommander{},
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

// parsePostFlags strips post's flags, reporting unknown ones. A trailing
// value flag reads "" (the shell dies on the shift instead — a documented
// divergence shared with the triage port — and the gate below refuses typed).
func parsePostFlags(r *cli.Runner, args []string, dir string) (core.PostFlags, int, bool) {
	f := core.PostFlags{Approve: "false", ReviewFile: dir + "/review.json"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--approve", a == "--review-file":
			v := ""
			if i+1 < len(args) {
				i++
				v = args[i]
			}
			setPostValue(&f, a, v)
		case strings.HasPrefix(a, "--approve="):
			f.Approve = strings.TrimPrefix(a, "--approve=")
		case a == "--request-changes":
			f.RequestChanges = true
		case a == "--bind":
			f.Bind = true
		case a == "--force":
			f.Force = true
		case a == "--dry-run":
			f.DryRun = true
		case a == "--json":
		default:
			msg := fmt.Sprintf("unknown flag '%s' for post (want --approve[=bool], --request-changes, "+
				"--bind, --force, --review-file <path>, --dry-run, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// setPostValue stores one value-taking post flag's value.
func setPostValue(f *core.PostFlags, name, val string) {
	if name == "--approve" {
		f.Approve = val
	} else {
		f.ReviewFile = val
	}
}

// postRentry rebuilds the exact re-run command the shell records after flag
// parsing (flags included, in shell order, so a retry reproduces this
// invocation).
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
