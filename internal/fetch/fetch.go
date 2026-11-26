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
	"io"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run executes `fetch <owner/repo> <pr> [--base-sha|--since <sha>]
// [--provider <name>] [--index <ref|dir>] [--context <file|dir>]
// [--spec <file>] [--issue <ref>] [--consent-over-budget]
// [--allow-head-controls] [--show-prompts] [--chunk] [--json]`, returning the
// process exit code.
// Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
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
	resp, err := fetchcore.Run(ctx, fetchRequest(r, &req, &f))
	if err != nil {
		return refuseFetch(r, err)
	}
	return renderFetch(r, resp, "fetch")
}

// RunLocal executes `fetch-local <owner/repo> <name> [--staged]
// [--range A[..|...]B] [--base R] [--stash [n]] [--worktree] [--scan <path>]
// [--index <ref|dir>] [--consent-over-budget] [--allow-head-controls]
// [--show-prompts] [--chunk] [-- paths...] [--json]`, returning the process
// exit code.
func RunLocal(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
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
	resp, err := fetchcore.RunLocal(ctx, fetchLocalRequest(r, &req, &f))
	if err != nil {
		return refuseFetch(r, err)
	}
	return renderFetch(r, resp, "fetch-local")
}

// fetchRequest builds the core fetch request from the runner and
// invocation: the injected runtime (environment, clock, progress, and the
// production split-stream commander), the render configuration, and the
// adapter-routed streams.
func fetchRequest(r *cli.Runner, req *readcommon.Request, f *fetchcore.Flags) *fetchcore.Request {
	return &fetchcore.Request{
		ReadRequest:  fetchRead(r, req),
		Flags:        *f,
		SettingsPath: cli.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"]),
		Effective:    r.Effective,
		JSONMode:     r.JSONMode,
		Warnings:     append([]string{}, r.Warnings...),
		Human:        r.Human,
		Diag:         r.Diag,
	}
}

// fetchRead is the invocation both fetch verbs share: the request's runtime
// plus the embedded pack payload, over the production split-stream commander.
func fetchRead(r *cli.Runner, req *readcommon.Request) core.ReadRequest {
	rt := *req.Runtime
	rt.PackFS = skillfs.FS
	in := *req
	in.Runtime = &rt
	return r.Read(in, coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true})
}

// fetchLocalRequest builds the core fetch-local request.
func fetchLocalRequest(r *cli.Runner, req *readcommon.Request, f *fetchcore.LocalFlags) *fetchcore.LocalRequest {
	return &fetchcore.LocalRequest{
		ReadRequest:  fetchRead(r, req),
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
func renderFetch(r *cli.Runner, resp *fetchcore.Response, verb string) int {
	r.Warnings = resp.Warnings
	if r.JSONMode {
		r.Emit(verb, resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire())
		return cli.ExitOK
	}
	if resp.Data != nil && resp.Data.Preview != nil {
		writePreview(r.Out, resp.Data.Preview)
		return cli.ExitOK
	}
	if resp.Data != nil && resp.Data.PromptDump != nil {
		fmt.Fprint(r.Out, *resp.Data.PromptDump)
	}
	return cli.ExitOK
}

// writePreview renders the human preview: one header carrying the byte totals,
// one line per included file in diff order, one line per withheld section with
// its rule and reason, and the resolved review budget last so an operator reads
// the cost before the list grows.
func writePreview(out io.Writer, p *core.PreviewData) {
	fmt.Fprintf(out, "preview: %d included, %d excluded (%d bytes reviewed, %d excluded)\n",
		len(p.Included), len(p.Excluded), p.IncludedBytes, p.ExcludedBytes)
	for _, f := range p.Included {
		fmt.Fprintf(out, "  include %s (%s, %d bytes)\n", f.Path, f.Role, f.Bytes)
	}
	for _, e := range p.Excluded {
		fmt.Fprintf(out, "  exclude %s (%s, %s, %s, %d bytes)\n", e.Path, e.RuleID, e.Pattern, e.Reason, e.Bytes)
	}
	fmt.Fprintf(out, "budget: %s — %d finder(s), cross-check %s", p.Budget.Tier, p.Budget.Finders, p.Budget.CrossCheck)
	if len(p.Budget.Reasons) > 0 {
		fmt.Fprintf(out, " (%s)", strings.Join(p.Budget.Reasons, ", "))
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "no bundle written: preview stops before the bundle directory\n")
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
	return r.Refuse(&core.RefusalError{Code: fetchcore.CodeFetchFailed, Message: core.Msgf("fetch failed: %v", err)})
}

// parseFetchFlags strips fetch's flags, reporting unknown ones. A value flag
// with no operand is a usage error.
func parseFetchFlags(r *cli.Runner, args []string) (fetchcore.Flags, int, bool) {
	var f fetchcore.Flags
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
	flagSpec:     "<file>",
	flagIssue:    "<ref>",
	flagIgnore:   "<glob>",
}

// parseFetchToken consumes one fetch argument, returning the next index plus a
// usage refusal when the token is unknown or a value flag lost its operand.
func parseFetchToken(r *cli.Runner, f *fetchcore.Flags, args []string, i int) (int, int, bool) {
	a := args[i]
	if code, done, consumed := parseInlineIssue(r, f, a); consumed {
		return i, code, done
	}
	if want, ok := fetchValueWants[a]; ok {
		return parseFetchValue(r, f, args, i, a, want)
	}
	switch a {
	case "--consent-over-budget":
		f.BudgetConsent = true
	case "--no-default-excludes":
		f.NoDefaultExcludes = true
	case flagPreview:
		f.Preview = true
	case "--allow-head-controls":
		f.HeadControls = true
	case "--show-prompts":
		f.ShowPrompts = true
	case flagChunk:
		f.ChunkFlag = true
	default:
		msg := fmt.Sprintf("unknown flag '%s' for fetch (want --base-sha|--since <sha>, "+
			"--provider <name>, --index <ref|dir>, --issue <ref>, --consent-over-budget, --allow-head-controls, "+
			"--ignore <glob>, --no-default-excludes, --preview, --show-prompts, --chunk, --json)", a)
		return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
	}
	return i, 0, false
}

// parseFetchValue consumes one value flag and its operand, reporting the
// missing-value usage error a bare flag or an empty --issue earns.
func parseFetchValue(r *cli.Runner, f *fetchcore.Flags, args []string, i int, flag, want string) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, flag, want)
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	if flag == flagIssue && v == "" {
		// An empty issue reference is a missing operand, not a ref the
		// service would have to spend a host read refusing.
		return i, r.ReportUsage(cli.MissingValue(flag, want)), true
	}
	setFetchValue(f, flag, v)
	return next, 0, false
}

// parseInlineIssue consumes the `--issue=<ref>` spelling, which the global
// pass leaves alone because only fetch takes the flag. consumed reports
// whether this token was the flag, done whether the command is finished; an
// empty inline value is a missing operand.
func parseInlineIssue(r *cli.Runner, f *fetchcore.Flags, a string) (code int, done, consumed bool) {
	value, ok := strings.CutPrefix(a, flagIssue+"=")
	if !ok {
		return 0, false, false
	}
	if value == "" {
		return r.ReportUsage(cli.MissingValue(flagIssue, fetchValueWants[flagIssue])), true, true
	}
	f.Issue = append(f.Issue, value)
	return 0, false, true
}

// parseLocalFlags strips fetch-local's flags, reporting unknown ones. A bare
// --stash keeps its documented optional value ("0"); every other value flag
// with no operand is a usage error.
func parseLocalFlags(r *cli.Runner, args []string) (fetchcore.LocalFlags, int, bool) {
	var f fetchcore.LocalFlags
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
func parseLocalToken(r *cli.Runner, f *fetchcore.LocalFlags, args []string, i int) (int, int, bool) {
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
	case flagPreview:
		f.Preview = true
	case "--allow-head-controls":
		f.HeadControls = true
	case "--show-prompts":
		f.ShowPrompts = true
	case flagChunk:
		f.ChunkFlag = true
	default:
		if strings.HasPrefix(a, "-") {
			msg := fmt.Sprintf("unknown flag '%s' for fetch-local (want --staged, "+
				"--range <A..B>, --base <ref>, --stash [n], --worktree, --scan <path>, --index <ref|dir>, "+
				"--consent-over-budget, --allow-head-controls, --ignore <glob>, --no-default-excludes, "+
				"--preview, --show-prompts, --chunk, -- <paths>, --json)", a)
			return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
		// A bare word is never a path filter: an unquoted glob or variable the
		// shell expanded would narrow the review silently. Filters go after `--`.
		msg := fmt.Sprintf("unexpected argument '%s' for fetch-local: path filters go after '--' "+
			"(fetch-local <owner/repo> <name> -- <paths>)", a)
		return i, r.ReportUsage(&cli.UsageError{Message: msg}), true
	}
	return i, 0, false
}

// localValueWants maps each fetch-local single-value or repeatable flag to its
// operand placeholder for missing-value usage errors.
var localValueWants = map[string]string{
	flagIgnore:  "<glob>",
	flagContext: "<file|dir>",
	flagSpec:    "<file>",
	flagScan:    "<path>",
	"--range":   "A[..|...]B",
	"--base":    "<ref>",
	flagIndex:   "<ref|dir>",
}

// parseLocalOption consumes fetch-local's value and default-exclusion flags.
// It returns the next index, a missing-value usage error, and a match flag.
func parseLocalOption(f *fetchcore.LocalFlags, a string, args []string, i int) (int, *cli.UsageError, bool) {
	if want, ok := localValueWants[a]; ok {
		v, next, u := cli.FlagValue(args, i, a, want)
		if u != nil {
			return i, u, true
		}
		setLocalValue(f, a, v)
		return next, nil, true
	}
	if a == "--" {
		// Everything after `--` is a path filter: it selects, never excludes.
		f.Paths = append(f.Paths, args[i+1:]...)
		return len(args), nil, true
	}
	switch a {
	case "--no-default-excludes":
		f.NoDefaultExcludes = true
		return i, nil, true
	case "--stash":
		f.HasStash = true
		f.StashN, i = stashValue(args, i)
		return i, nil, true
	default:
		return i, nil, false
	}
}

// setLocalValue applies one fetch-local value flag's operand.
func setLocalValue(f *fetchcore.LocalFlags, name, value string) {
	switch name {
	case flagIgnore:
		f.Ignore = append(f.Ignore, value)
	case flagContext:
		f.Context = append(f.Context, value)
	case flagSpec:
		f.Spec = append(f.Spec, value)
	case flagScan:
		f.Scan = append(f.Scan, value)
	case "--range":
		f.RangeSpec = value
	case "--base":
		f.BaseRef = value
	case flagIndex:
		f.Index = value
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
func setFetchValue(f *fetchcore.Flags, name, value string) {
	switch name {
	case flagIgnore:
		f.Ignore = append(f.Ignore, value)
	case flagContext:
		f.Context = append(f.Context, value)
	case flagSpec:
		f.Spec = append(f.Spec, value)
	case flagIssue:
		f.Issue = append(f.Issue, value)
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
	flagSpec       = "--spec"
	flagIssue      = "--issue"
	flagScan       = "--scan"
	providerGitHub = "github"
	flagChunk      = "--chunk"
	flagIndex      = "--index"
	flagPreview    = "--preview"
)

// CheckFlags validates fetch's flags alone, reporting a usage error on r
// exactly as Run would. The front door calls it ahead of any host lookup, so a
// mistyped flag is refused before verdict spends a host call on its target.
func CheckFlags(r *cli.Runner, args []string) (code int, bad bool) {
	_, code, bad = parseFetchFlags(r, args)
	return code, bad
}
