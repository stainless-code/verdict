// Package fetch is the fetch/fetch-local CLI adapter: argv parsing (same
// usage text and exit 2), one call into the fetchcore service, envelope
// rendering, and refusal-vs-cancel exit mapping. Domain logic — the bundle
// builders, budget/cap/chunk gates, and the prompt dump — lives in
// internal/core/fetch.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved fetch invocation.
type Request struct {
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// Run executes `fetch <owner/repo> <pr> [--base-sha|--since <sha>]
// [--provider <name>] [--index <ref|dir>] [--consent-over-budget]
// [--allow-head-controls] [--show-prompts] [--chunk] [--json]`, returning the
// process exit code.
// Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	f, code, done := parseFetchFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "fetch"); done {
		return code
	}
	if f.ShowPrompts && !r.JSONMode {
		// The dump owns stdout; progress moves to stderr.
		r.Human = r.Diag
	}
	resp, err := fetchcore.Run(ctx, fetchRequest(r, req, &f))
	if err != nil {
		return refuseFetch(r, err)
	}
	return renderFetch(r, resp, "fetch")
}

// RunLocal executes `fetch-local <owner/repo> <name> [--staged]
// [--range A[..|...]B] [--base R] [--stash [n]] [--worktree]
// [--index <ref|dir>] [--consent-over-budget] [--allow-head-controls]
// [--show-prompts] [--chunk] [-- paths...] [--json]`, returning the process
// exit code.
func RunLocal(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	f, code, done := parseLocalFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "fetch"); done {
		return code
	}
	if f.ShowPrompts && !r.JSONMode {
		// The dump owns stdout; progress moves to stderr, like fetch.
		r.Human = r.Diag
	}
	resp, err := fetchcore.RunLocal(ctx, fetchLocalRequest(r, req, &f))
	if err != nil {
		return refuseFetch(r, err)
	}
	return renderFetch(r, resp, "fetch-local")
}

// fetchRequest builds the core fetch request from the runner and
// invocation: the injected runtime (environment, clock, progress, and the
// production split-stream commander), the render configuration, and the
// adapter-routed streams.
func fetchRequest(r *cli.Runner, req Request, f *core.FetchFlags) *core.FetchRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
			PackFS:    skillfs.FS,
		},
		Repo: req.Repo, Pr: req.Pr, Root: &root, RepoRoot: req.RepoRoot,
	}
	return &core.FetchRequest{
		ReadRequest:  read,
		Flags:        *f,
		SettingsPath: cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		Effective:    r.Effective,
		JSONMode:     r.JSONMode,
		Warnings:     append([]string{}, r.Warnings...),
		Human:        r.Human,
		Diag:         r.Diag,
	}
}

// fetchLocalRequest builds the core fetch-local request.
func fetchLocalRequest(r *cli.Runner, req Request, f *core.FetchLocalFlags) *core.FetchLocalRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
			PackFS:    skillfs.FS,
		},
		Repo: req.Repo, Pr: req.Pr, Root: &root, RepoRoot: req.RepoRoot,
	}
	return &core.FetchLocalRequest{
		ReadRequest:  read,
		Flags:        *f,
		SettingsPath: cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		Effective:    r.Effective,
		JSONMode:     r.JSONMode,
		Warnings:     append([]string{}, r.Warnings...),
		Human:        r.Human,
		Diag:         r.Diag,
	}
}

// renderFetch renders a fetch success: warnings carry onto the runner, the
// prompt dump owns stdout in human mode, and JSON mode prints exactly one
// envelope to stdout.
func renderFetch(r *cli.Runner, resp *core.FetchResponse, verb string) int {
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope(verb, resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	if resp.Data != nil && resp.Data.PromptDump != nil {
		fmt.Fprint(r.Out, *resp.Data.PromptDump)
	}
	return cli.ExitOK
}

// refuseFetch renders a fetch failure: the missing-tool kind prints its
// frozen line with no envelope, warnings carry onto the runner, a cancel
// exits 3 (scripts treat it as broke), anything else exits 1.
func refuseFetch(r *cli.Runner, err error) int {
	if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
		fmt.Fprintf(r.Diag, "missing: %s\n", mt.Tool)
		return cli.ExitRefused
	}
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return r.RefuseTyped(ref)
	}
	return r.Refuse(&core.RefusalError{Code: fetchcore.CodeFetchFailed, Message: fmt.Sprintf("fetch failed: %v", err)})
}

// parseFetchFlags strips fetch's flags, reporting unknown ones. A value flag
// with no operand is a usage error.
func parseFetchFlags(r *cli.Runner, args []string) (core.FetchFlags, int, bool) {
	var f core.FetchFlags
	for i := 0; i < len(args); i++ {
		next, code, done := parseFetchToken(r, &f, args, i)
		if done {
			return f, code, true
		}
		i = next
	}
	return f, 0, false
}

// fetchValueWants names each fetch value flag's operand for the
// missing-value usage error.
var fetchValueWants = map[string]string{
	"--base-sha": "<sha>",
	"--since":    "<sha>",
	"--provider": "<name>",
	flagIndex:    "<ref|dir>",
	flagContext:  "<file|dir>",
	flagIgnore:   "<glob>",
}

// parseFetchToken consumes one fetch argument, returning the next index plus a
// usage refusal when the token is unknown or a value flag lost its operand.
func parseFetchToken(r *cli.Runner, f *core.FetchFlags, args []string, i int) (int, int, bool) {
	a := args[i]
	if want, ok := fetchValueWants[a]; ok {
		v, next, u := cli.FlagValue(args, i, a, want)
		if u != nil {
			return i, r.ReportUsage(u), true
		}
		setFetchValue(f, a, v)
		return next, 0, false
	}
	switch a {
	case "--consent-over-budget":
		f.BudgetConsent = true
	case "--no-default-excludes":
		f.NoDefaultExcludes = true
	case "--allow-head-controls":
		f.HeadControls = true
	case "--show-prompts":
		f.ShowPrompts = true
	case flagChunk:
		f.ChunkFlag = true
	default:
		msg := fmt.Sprintf("unknown flag '%s' for fetch (want --base-sha|--since <sha>, "+
			"--provider <name>, --index <ref|dir>, --consent-over-budget, --allow-head-controls, "+
			"--ignore <glob>, --no-default-excludes, --show-prompts, --chunk, --json)", a)
		return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
	}
	return i, 0, false
}

// parseLocalFlags strips fetch-local's flags, reporting unknown ones. A bare
// --stash keeps its documented optional value ("0"); every other value flag
// with no operand is a usage error.
func parseLocalFlags(r *cli.Runner, args []string) (core.FetchLocalFlags, int, bool) {
	var f core.FetchLocalFlags
	for i := 0; i < len(args); i++ {
		next, code, done := parseLocalToken(r, &f, args, i)
		if done {
			return f, code, true
		}
		i = next
	}
	return f, 0, false
}

// parseLocalToken consumes one fetch-local argument, returning the next
// index plus a usage refusal when the token is an unknown flag.
func parseLocalToken(r *cli.Runner, f *core.FetchLocalFlags, args []string, i int) (int, int, bool) {
	a := args[i]
	if next, u, ok := parseLocalOption(f, a, args, i); ok {
		if u != nil {
			return i, r.ReportUsage(u), true
		}
		return next, 0, false
	}
	switch a {
	case "--staged":
		f.Staged = true
	case "--worktree":
		f.Worktree = true
	case "--consent-over-budget":
		f.BudgetConsent = true
	case "--allow-head-controls":
		f.HeadControls = true
	case "--show-prompts":
		f.ShowPrompts = true
	case flagChunk:
		f.ChunkFlag = true
	case "--":
		f.Paths = append(f.Paths, args[i+1:]...)
		return len(args), 0, false
	default:
		if strings.HasPrefix(a, "-") {
			msg := fmt.Sprintf("unknown flag '%s' for fetch-local (want --staged, "+
				"--range <A..B>, --base <ref>, --stash [n], --worktree, --index <ref|dir>, "+
				"--consent-over-budget, --allow-head-controls, --ignore <glob>, --no-default-excludes, "+
				"--show-prompts, --chunk, -- <paths>, --json)", a)
			return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
		f.Paths = append(f.Paths, a)
	}
	return i, 0, false
}

// parseLocalOption consumes fetch-local's value and default-exclusion flags.
// It returns the next index, a missing-value usage error, and a match flag.
func parseLocalOption(f *core.FetchLocalFlags, a string, args []string, i int) (int, *cli.UsageError, bool) {
	switch a {
	case "--no-default-excludes":
		f.NoDefaultExcludes = true
		return i, nil, true
	case flagIgnore:
		v, next, u := cli.FlagValue(args, i, a, "<glob>")
		f.Ignore = append(f.Ignore, v)
		return next, u, true
	case flagContext:
		v, next, u := cli.FlagValue(args, i, a, "<file|dir>")
		f.Context = append(f.Context, v)
		return next, u, true
	case "--range":
		v, next, u := cli.FlagValue(args, i, a, "A[..|...]B")
		if u != nil {
			return i, u, true
		}
		f.RangeSpec = v
		return next, nil, true
	case "--base":
		v, next, u := cli.FlagValue(args, i, a, "<ref>")
		if u != nil {
			return i, u, true
		}
		f.BaseRef = v
		return next, nil, true
	case "--stash":
		f.HasStash = true
		f.StashN, i = stashValue(args, i)
		return i, nil, true
	case flagIndex:
		v, next, u := cli.FlagValue(args, i, a, "<ref|dir>")
		if u != nil {
			return i, u, true
		}
		f.Index = v
		return next, nil, true
	default:
		return i, nil, false
	}
}

// stashValue reads --stash's optional operand: a non-flag trailing word, else
// the documented "0" default (a bare --stash means stash@{0}).
func stashValue(args []string, i int) (string, int) {
	if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
		return args[i+1], i + 1
	}
	return "0", i
}

// setFetchValue applies one fetch value flag's operand.
func setFetchValue(f *core.FetchFlags, name, value string) {
	switch name {
	case flagIgnore:
		f.Ignore = append(f.Ignore, value)
	case flagContext:
		f.Context = append(f.Context, value)
	case "--provider":
		f.HasProvider, f.Provider = true, value
	case flagIndex:
		f.Index = value
	default:
		f.HasBase, f.BaseSHA = true, value
	}
}

// providerGitHub and the flag names mirror the fetchcore spellings the usage
// text names.
const (
	flagIgnore     = "--ignore"
	flagContext    = "--context"
	providerGitHub = "github"
	flagChunk      = "--chunk"
	flagIndex      = "--index"
)
