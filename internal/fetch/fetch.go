// Package fetch ports the `fetch` and `fetch-local` bundle builders: the
// remote PR fetch across every supported host (GitHub plus the --provider
// backends) and the local worktree builder. Both write the same bundle
// layout the shell writes (PR.json/source.json, full.diff, toc.txt,
// roles.txt, hunks.tsv, stores, advisory manifests), enforce the same diff
// cap and budget gates, slice the same opt-in chunks, and render the same
// nonce-fenced prompt dump and machine envelopes.
//
// The Go fetch owns the large-PR edge set as structure (audit 18/20/21,
// folded into the port): paged file listing past the hosting CLI cap with
// the provider ceiling noted, the distinct upstream over-file-count refusal
// with its recovery hint, and bounded secondary-rate-limit backoff. Those
// paths engage only past the shell's edges, so default bundles stay
// byte-identical.
//
// R5 exceptions: bundle paths come from the operator's own invocation
// (fixed filenames under the cwd), not from agents — so this package reads
// them without [os.Root], matching the record port (the G304 exclusion in
// .golangci.yml covers those reads by design).
package fetch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
)

// Request carries the resolved fetch invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// codeFetchFailed is the typed refusal for every fetch-internal failure:
// the shell's `fail` paths that carry no narrower code.
const codeFetchFailed = "fetch-failed"

// Run executes `fetch <owner/repo> <pr> [--base-sha|--since <sha>]
// [--provider <name>] [--consent-over-budget] [--show-prompts] [--chunk]
// [--json]`, returning the process exit code. Human progress goes to the
// runner's human stream; --json mode prints exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	f, code, done := parseFetchFlags(r, args)
	if done {
		return code
	}
	if code, done := checkCancelled(ctx, r); done {
		return code
	}
	if f.showPrompts && !r.JSONMode {
		// The dump owns stdout; progress moves to stderr (exec 8>&1;
		// exec 1>&2 in the shell).
		r.Human = r.Diag
	}
	code, err := runFetch(ctx, &fetchRunCtx{runner: r, env: req.Env, repo: req.Repo, pr: req.Pr, root: req.Root}, req, f)
	return finishRun(r, code, err)
}

// RunLocal executes `fetch-local <owner/repo> <name> [--staged]
// [--range A[..|...]B] [--base R] [--stash [n]] [--consent-over-budget]
// [--show-prompts] [--chunk] [-- paths...] [--json]`, returning the
// process exit code.
func RunLocal(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	f, code, done := parseLocalFlags(r, args)
	if done {
		return code
	}
	if code, done := checkCancelled(ctx, r); done {
		return code
	}
	if f.showPrompts && !r.JSONMode {
		// The dump owns stdout; progress moves to stderr, like fetch.
		r.Human = r.Diag
	}
	code, err := runLocal(ctx, &fetchRunCtx{runner: r, env: req.Env, repo: req.Repo, pr: req.Pr, root: req.Root}, req, &f)
	return finishRun(r, code, err)
}

// fetchFlags carries the parsed fetch options.
type fetchFlags struct {
	baseSHA       string
	provider      string
	budgetConsent bool
	showPrompts   bool
	chunkFlag     bool
	hasBase       bool
	hasProvider   bool
}

// localFlags carries the parsed fetch-local options.
type localFlags struct {
	rangeSpec     string
	baseRef       string
	stashN        string
	paths         []string
	staged        bool
	hasStash      bool
	budgetConsent bool
	showPrompts   bool
	chunkFlag     bool
}

// parseFetchFlags strips fetch's flags, reporting unknown ones. A trailing
// value flag reads "" (the shell dies on the shift instead — a documented
// divergence shared with the record port).
func parseFetchFlags(r *cli.Runner, args []string) (fetchFlags, int, bool) {
	f := fetchFlags{provider: providerGitHub}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--base-sha", "--since":
			f.hasBase = true
			f.baseSHA, i = flagValue(args, i)
		case "--provider":
			f.hasProvider = true
			f.provider, i = flagValue(args, i)
		case "--consent-over-budget":
			f.budgetConsent = true
		case "--show-prompts":
			f.showPrompts = true
		case flagChunk:
			f.chunkFlag = true
		case flagJSON:
		default:
			msg := fmt.Sprintf("unknown flag '%s' for fetch (want --base-sha|--since <sha>, "+
				"--provider <name>, --consent-over-budget, --show-prompts, --chunk, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// parseLocalFlags strips fetch-local's flags, reporting unknown ones. A
// trailing value flag reads "" and a bare --stash reads "0", mirroring the
// shell's defaults.
func parseLocalFlags(r *cli.Runner, args []string) (localFlags, int, bool) {
	var f localFlags
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
func parseLocalToken(r *cli.Runner, f *localFlags, args []string, i int) (int, int, bool) {
	a := args[i]
	if next, ok := parseLocalValueFlag(f, a, args, i); ok {
		return next, 0, false
	}
	switch a {
	case "--staged":
		f.staged = true
	case "--consent-over-budget":
		f.budgetConsent = true
	case "--show-prompts":
		f.showPrompts = true
	case flagChunk:
		f.chunkFlag = true
	case flagJSON:
	case "--":
		f.paths = append(f.paths, args[i+1:]...)
		return len(args), 0, false
	default:
		if strings.HasPrefix(a, "-") {
			msg := fmt.Sprintf("unknown flag '%s' for fetch-local (want --staged, "+
				"--range <A..B>, --base <ref>, --stash [n], --consent-over-budget, "+
				"--show-prompts, --chunk, -- <paths>, --json)", a)
			return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
		f.paths = append(f.paths, a)
	}
	return i, 0, false
}

// parseLocalValueFlag consumes fetch-local's value flags, returning the next
// index and whether the flag matched.
func parseLocalValueFlag(f *localFlags, a string, args []string, i int) (int, bool) {
	switch a {
	case "--range":
		f.rangeSpec, i = flagValue(args, i)
	case "--base":
		f.baseRef, i = flagValue(args, i)
	case "--stash":
		f.hasStash = true
		f.stashN, i = stashValue(args, i)
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

// checkCancelled refuses when the context is already done.
func checkCancelled(ctx context.Context, r *cli.Runner) (int, bool) {
	if err := ctx.Err(); err != nil {
		// GO-CLI-05: cancel kills the run with exit 3 and a JSON envelope.
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before fetch completed"}, cli.ExitBroke), true
	}
	return 0, false
}

// finishRun maps a run result to the process exit code.
func finishRun(r *cli.Runner, code int, err error) int {
	if err == nil {
		return code
	}
	ne, _ := errors.AsType[*needError](err)
	if ne != nil {
		// `need`: the plain line is already on stderr; exit 1, no
		// envelope, no re-enter line.
		return cli.ExitRefused
	}
	if ref, _ := errors.AsType[*cli.RefusalError](err); ref != nil {
		return r.Refuse(ref)
	}
	return r.Refuse(&cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("fetch failed: %v", err)})
}
