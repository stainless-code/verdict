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

const (
	sigFlag         = "--sig"
	minSeverityFlag = "--min-severity"
	verifyCmdFlag   = "--verify-cmd"
	pathsFlag       = "--paths"
	testFlag        = "--test"
	commitFlag      = "--commit"
	flagSep         = " "
)

// Run executes the fix verb: argv parsing, one fixcore call, and exit mapping.
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
	r.Emit("fix", r.Repo, r.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

func fixRequest(r *cli.Runner, req Request, f *flags) *fixcore.Request {
	out := &fixcore.Request{
		Sig: f.sig, All: f.all, Selected: f.selected, MinSeverity: f.minSeverity,
		VerifyCmd: f.verifyCmd, Paths: f.paths, Test: f.test, Commit: f.commit,
		Human: r.Human,
	}
	out.ReadRequest = r.Read(core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env,
		},
		Repo: req.Repo, Pr: req.Pr, RepoRoot: req.RepoRoot,
	}, coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true})
	return out
}

type flags struct {
	sig         string
	minSeverity string
	verifyCmd   string
	test        string
	paths       []string
	commit      bool
	all         bool
	selected    bool
}

func (f *flags) rentry() string {
	var sb strings.Builder
	for _, row := range []struct {
		flag string
		set  bool
	}{
		{flagSep + sigFlag + flagSep + f.sig, f.sig != ""},
		{flagSep + "--all", f.all},
		{flagSep + "--selected", f.selected},
		{flagSep + testFlag + flagSep + f.test, f.test != ""},
		{flagSep + commitFlag, f.commit},
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

func parseFlags(r *cli.Runner, args []string) (*flags, int, bool) {
	f := &flags{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case sigFlag, minSeverityFlag, verifyCmdFlag, pathsFlag, testFlag:
			v, next, u := cli.FlagValue(args, i, a, wantOf(a))
			if u != nil {
				return f, r.ReportUsage(u), true
			}
			setFixValue(f, a, v)
			i = next
		case "--all":
			f.all = true
		case "--selected":
			f.selected = true
		case commitFlag:
			f.commit = true
		default:
			msg := fmt.Sprintf("unknown flag '%s' for fix (want %s <sig>, --all|--selected "+
				"[%s <sig>] [%s] [%s <s>] [%s <cmd>] [%s <glob>], --json)",
				a, sigFlag, testFlag, commitFlag, minSeverityFlag, verifyCmdFlag, pathsFlag)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

func pathEntry(paths []string) string {
	var sb strings.Builder
	for _, p := range paths {
		sb.WriteString(flagSep + pathsFlag + flagSep + shellquote.Arg(p))
	}
	return sb.String()
}

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

func setFixValue(f *flags, flag, val string) {
	switch flag {
	case sigFlag:
		f.sig = val
	case minSeverityFlag:
		f.minSeverity = val
	case pathsFlag:
		f.paths = append(f.paths, val)
	case testFlag:
		f.test = val
	default:
		f.verifyCmd = val
	}
}
