// Package fix is the fix verb's CLI adapter: argv parsing, one call into the
// fixcore service, warning routing, envelope rendering, and exit mapping.
// Domain logic — sig selection, the ledger, and the scope audit — lives in
// internal/core/fix.
package fix

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	"github.com/stainless-code/verdict/internal/shellquote"
)

// Request carries the resolved fix invocation.
type Request struct {
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// The fix flags, spelled once: the usage text, the re-entry line, and the
// combination guard all name them.
const (
	sigFlag         = "--sig"
	minSeverityFlag = "--min-severity"
	verifyCmdFlag   = "--verify-cmd"
	pathsFlag       = "--paths"
	flagSep         = " "
)

// Run executes `fix <owner/repo> <local-slug>` with a selection mode — a
// named `--sig`, or `--all` with its batch flags — plus `--json`, returning
// the process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = "verdict fix " + req.Repo + " " + req.Pr
	f, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry += f.rentry()
	if r.JSONMode {
		r.Rentry += " --json"
	}
	if code, done := r.Canceled(ctx, "fix"); done {
		return code
	}
	resp, ref := fixcore.Run(ctx, fixRequest(r, req, f))
	if ref != nil {
		if ref.Code == core.CodeBadFlags {
			return r.ReportUsage(&cli.UsageError{Message: ref.Message})
		}
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("fix", r.Repo, r.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
	return cli.ExitOK
}

// fixRequest injects the runner's clock, progress observer, and the
// production commander into the typed service request.
func fixRequest(r *cli.Runner, req Request, f *flags) *fixcore.Request {
	out := &fixcore.Request{
		Sig: f.sig, All: f.all, MinSeverity: f.minSeverity,
		VerifyCmd: f.verifyCmd, Paths: f.paths, Human: r.Human,
	}
	out.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr, RepoRoot: req.RepoRoot,
	}
	return out
}

// flags carries the parsed fix options.
type flags struct {
	sig         string
	minSeverity string
	verifyCmd   string
	paths       []string
	all         bool
}

// rentry renders the flags a retry repeats, in flag order.
func (f *flags) rentry() string {
	var sb strings.Builder
	for _, row := range []struct {
		flag string
		set  bool
	}{
		{flagSep + sigFlag + flagSep + f.sig, f.sig != ""},
		{flagSep + "--all", f.all},
		{flagSep + minSeverityFlag + flagSep + f.minSeverity, f.minSeverity != ""},
		{flagSep + verifyCmdFlag + flagSep + shellquote.Arg(f.verifyCmd), f.verifyCmd != ""},
		{pathEntry(f.paths), len(f.paths) > 0},
	} {
		if row.set {
			sb.WriteString(row.flag)
		}
	}
	return sb.String()
}

// parseFlags strips fix's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (*flags, int, bool) {
	f := &flags{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case sigFlag, minSeverityFlag, verifyCmdFlag, pathsFlag:
			v, next, u := cli.FlagValue(args, i, a, wantOf(a))
			if u != nil {
				return f, r.ReportUsage(u), true
			}
			setFixValue(f, a, v)
			i = next
		case "--all":
			f.all = true
		default:
			msg := fmt.Sprintf("unknown flag '%s' for fix (want %s <sig>, --all "+
				"[%s <s>] [%s <cmd>] [%s <glob>], --json)",
				a, sigFlag, minSeverityFlag, verifyCmdFlag, pathsFlag)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// pathEntry renders every --paths glob a retry repeats, in the order given.
func pathEntry(paths []string) string {
	var sb strings.Builder
	for _, p := range paths {
		sb.WriteString(flagSep + pathsFlag + flagSep + shellquote.Arg(p))
	}
	return sb.String()
}

// wantOf renders one value flag's accepted operand for the usage error.
func wantOf(flag string) string {
	switch flag {
	case minSeverityFlag:
		return "critical|important|informational|off"
	case verifyCmdFlag:
		return "<cmd>"
	case pathsFlag:
		return "<glob>"
	default:
		return "<sig>"
	}
}

// setFixValue stores one value flag's operand.
func setFixValue(f *flags, flag, val string) {
	switch flag {
	case sigFlag:
		f.sig = val
	case minSeverityFlag:
		f.minSeverity = val
	case pathsFlag:
		f.paths = append(f.paths, val)
	default:
		f.verifyCmd = val
	}
}
