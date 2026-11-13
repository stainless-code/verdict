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
	"github.com/stainless-code/verdict/internal/readcommon"
)

// RunPost executes
// `post <owner/repo> <pr> [--approve[=bool]] [--request-changes] [--bind]
// [--force] [--review-file <path>] [--dry-run] [--json]`, returning the
// process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func RunPost(ctx context.Context, r *cli.Runner, args []string, req *Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	dir, ref := readcommon.ResolveBundleDir(req.RepoRoot, req.Pr)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	f, code, done := parsePostFlags(r, args, dir)
	if done {
		return code
	}
	r.Rentry = postRentry(req.Repo, req.Pr, dir, f, r.JSONMode)
	if code, done := r.Canceled(ctx, "post"); done {
		return code
	}
	resp, ref := postcore.Run(ctx, postRequest(r, req, f))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(postcore.RenderPostEnvelope(resp)))
	}
	return cli.ExitOK
}

// postRequest builds the core post request from the runner and invocation:
// the injected runtime (environment, clock, progress, and the production
// split-stream commander), the render configuration, and the adapter-routed
// flags.
func postRequest(r *cli.Runner, req *Request, f core.PostFlags) *core.PostRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr, Forces: req.Forces, Root: &root, RepoRoot: req.RepoRoot,
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
		case a == postApproveFlag, a == "--review-file", a == postSummaryFlag, a == postMinSeverityFlag:
			v, next, u := cli.FlagValue(args, i, a, postWants[a])
			if u != nil {
				return f, r.ReportUsage(u), true
			}
			setPostValue(&f, a, v)
			i = next
		case parsePostEqFlag(&f, a):
		case applyPostSwitchFlag(&f, a):
		default:
			msg := fmt.Sprintf("unknown flag '%s' for post (want --approve[=bool], --request-changes, "+
				"--bind, --force, --review-file <path>, --summary comment|off, --dry-run, --require-coverage, "+
				"--allow-secret-values, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// postApproveFlag names the --approve flag (which also takes the documented
// --approve=bool form), and postDryRunFlag the dry-run switch the parser and
// the re-entry line share.
const postApproveFlag = "--approve"

func parsePostEqFlag(f *core.PostFlags, arg string) bool {
	name, val, ok := strings.Cut(arg, postEqSep)
	if !ok {
		return false
	}
	switch name {
	case postApproveFlag:
		f.Approve = val
		return true
	case postSummaryFlag:
		f.Summary = val
		return true
	default:
		return false
	}
}

func applyPostSwitchFlag(f *core.PostFlags, arg string) bool {
	switch arg {
	case "--request-changes":
		f.RequestChanges = true
	case "--bind":
		f.Bind = true
	case "--force":
		f.Force = true
	case postDryRunFlag:
		f.DryRun = true
	case "--require-coverage":
		f.RequireCoverage = true
	case postcore.AllowSecretValuesFlag:
		f.AllowSecretValues = true
	default:
		return false
	}
	return true
}

const postEqSep = "="

const postMinSeverityFlag = "--min-severity"

const postDryRunFlag = "--dry-run"

const postSummaryFlag = "--summary"

// postWants names each value flag's accepted operand for the missing-value
// usage error.
var postWants = map[string]string{
	postApproveFlag:     "true|false",
	"--review-file":     "<path>",
	postSummaryFlag:     "comment|off",
	postMinSeverityFlag: "critical|important|informational|off",
}

// setPostValue stores one value-taking post flag's value.
func setPostValue(f *core.PostFlags, name, val string) {
	switch name {
	case postApproveFlag:
		f.Approve = val
	case postSummaryFlag:
		f.Summary = val
	case postMinSeverityFlag:
		f.MinSeverity = val
	default:
		f.ReviewFile = val
	}
}

// postRentry rebuilds the exact re-run command recorded after flag parsing
// (flags included, in flag order, so a retry reproduces this invocation).
func postRentry(repo, pr, dir string, f core.PostFlags, jsonMode bool) string {
	var sb strings.Builder
	sb.WriteString("verdict post " + repo + " " + pr)
	for _, flag := range postRentryFlags(f, dir, jsonMode) {
		sb.WriteString(" " + flag)
	}
	return sb.String()
}

// postRentryFlags lists the flags a retry repeats, in flag order: one row per
// flag instead of one branch, so the list stays a table as flags are added.
func postRentryFlags(f core.PostFlags, dir string, jsonMode bool) []string {
	rows := []struct {
		flag string
		set  bool
	}{
		{"--approve=" + f.Approve, f.Approve != "false"},
		{"--request-changes", f.RequestChanges},
		{"--bind", f.Bind},
		{"--force", f.Force},
		{"--review-file " + f.ReviewFile, f.ReviewFile != dir+"/review.json"},
		{postSummaryFlag + " " + f.Summary, f.Summary != "" && f.Summary != "off"},
		{postDryRunFlag, f.DryRun},
		{"--min-severity " + f.MinSeverity, f.MinSeverity != ""},
		{postcore.AllowSecretValuesFlag, f.AllowSecretValues},
		{"--json", jsonMode},
	}
	var out []string
	for _, row := range rows {
		if row.set {
			out = append(out, row.flag)
		}
	}
	return out
}
