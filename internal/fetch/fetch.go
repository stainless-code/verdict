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

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved fetch invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `fetch <owner/repo> <pr> [--base-sha|--since <sha>]
// [--provider <name>] [--consent-over-budget] [--allow-head-controls]
// [--show-prompts] [--chunk] [--json]`, returning the process exit code.
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
		// The dump owns stdout; progress moves to stderr (exec 8>&1;
		// exec 1>&2 in the shell).
		r.Human = r.Diag
	}
	resp, err := fetchcore.Run(ctx, fetchRequest(r, req, f))
	if err != nil {
		return refuseFetch(r, err)
	}
	return renderFetch(r, resp, "fetch")
}

// RunLocal executes `fetch-local <owner/repo> <name> [--staged]
// [--range A[..|...]B] [--base R] [--stash [n]] [--worktree]
// [--consent-over-budget] [--allow-head-controls] [--show-prompts] [--chunk]
// [-- paths...] [--json]`, returning the process exit code.
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
func fetchRequest(r *cli.Runner, req Request, f core.FetchFlags) *core.FetchRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr, Root: &root,
	}
	return &core.FetchRequest{
		ReadRequest: read,
		Flags:       f,
		Effective:   r.Effective,
		JSONMode:    r.JSONMode,
		Warnings:    append([]string{}, r.Warnings...),
		Human:       r.Human,
		Diag:        r.Diag,
	}
}

// fetchLocalRequest builds the core fetch-local request.
func fetchLocalRequest(r *cli.Runner, req Request, f *core.FetchLocalFlags) *core.FetchLocalRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
		},
		Repo: req.Repo, Pr: req.Pr, Root: &root,
	}
	return &core.FetchLocalRequest{
		ReadRequest: read,
		Flags:       *f,
		Effective:   r.Effective,
		JSONMode:    r.JSONMode,
		Warnings:    append([]string{}, r.Warnings...),
		Human:       r.Human,
		Diag:        r.Diag,
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

// parseFetchFlags strips fetch's flags, reporting unknown ones. A trailing
// value flag reads "" (the shell dies on the shift instead — a documented
// divergence shared with the record port).
func parseFetchFlags(r *cli.Runner, args []string) (core.FetchFlags, int, bool) {
	f := core.FetchFlags{Provider: providerGitHub}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--base-sha", "--since":
			f.HasBase = true
			f.BaseSHA, i = flagValue(args, i)
		case "--provider":
			f.HasProvider = true
			f.Provider, i = flagValue(args, i)
		case "--consent-over-budget":
			f.BudgetConsent = true
		case "--allow-head-controls":
			f.HeadControls = true
		case "--show-prompts":
			f.ShowPrompts = true
		case flagChunk:
			f.ChunkFlag = true
		default:
			msg := fmt.Sprintf("unknown flag '%s' for fetch (want --base-sha|--since <sha>, "+
				"--provider <name>, --consent-over-budget, --allow-head-controls, --show-prompts, --chunk, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// parseLocalFlags strips fetch-local's flags, reporting unknown ones. A
// trailing value flag reads "" and a bare --stash reads "0", mirroring the
// shell's defaults.
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
	if next, ok := parseLocalValueFlag(f, a, args, i); ok {
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
				"--range <A..B>, --base <ref>, --stash [n], --worktree, --consent-over-budget, "+
				"--allow-head-controls, --show-prompts, --chunk, -- <paths>, --json)", a)
			return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
		f.Paths = append(f.Paths, a)
	}
	return i, 0, false
}

// parseLocalValueFlag consumes fetch-local's value flags, returning the next
// index and whether the flag matched.
func parseLocalValueFlag(f *core.FetchLocalFlags, a string, args []string, i int) (int, bool) {
	switch a {
	case "--range":
		f.RangeSpec, i = flagValue(args, i)
	case "--base":
		f.BaseRef, i = flagValue(args, i)
	case "--stash":
		f.HasStash = true
		f.StashN, i = stashValue(args, i)
	default:
		return i, false
	}
	return i, true
}

// flagValue reads a trailing flag value, returning the next index
// ("", like the shell's unset default, when absent).
func flagValue(args []string, i int) (string, int) {
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", i
}

// stashValue reads the optional stash number: a non-flag trailing word,
// else the "0" default, like the shell. Returns the next index.
func stashValue(args []string, i int) (string, int) {
	if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
		return args[i+1], i + 1
	}
	return "0", i
}

// providerGitHub and flagChunk mirror the fetchcore spellings the usage
// text names.
const (
	providerGitHub = "github"
	flagChunk      = "--chunk"
)
