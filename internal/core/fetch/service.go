// Package fetchcore is the fetch/fetch-local domain service: the remote
// bundle build across every provider plus the local worktree builder,
// behind (ctx, Req) → (Resp, error) with the exec seam. The CLI adapter
// parses, calls, renders, and maps exits; nothing here touches argv,
// stdio, or exit codes.
package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// CodeFetchFailed is the typed refusal for every fetch-internal failure:
// the moved bodies return it wherever the shell's `fail` paths carried no
// narrower code. The adapter maps unknown errors to it as well.
const CodeFetchFailed = "fetch-failed"

// codeCanceled names the cancel refusal the adapter exits 3 on.
const codeCanceled = "canceled"

// Commander is the exec seam every fetch spawn goes through: gh, glab,
// git, curl, and the az/cafe probes. The production implementation is
// [exec.SplitCommander] (separate stdout/stderr capture, missing binaries
// read [core.MissingToolError]); tests inject a scripted commander and
// assert the exact argv. No service body spawns directly.
type Commander interface {
	Run(ctx context.Context, cmd core.Command) (core.Result, error)
	LookPath(tool string) error
}

// Run executes `fetch <owner/repo> <pr>`: the remote bundle build across
// every provider, sharing the budget/cap/chunk/prompt planes. It never
// writes stdout/stderr or exits; human lines stream to the request's Human
// writer, warnings accumulate for the envelope and stream through the
// runtime progress observer, and the envelope inputs come back in the
// response for the adapter to render.
func Run(ctx context.Context, req *core.FetchRequest) (*core.FetchResponse, error) {
	st := newState(req)
	if err := ctx.Err(); err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: "canceled before fetch completed"})
	}
	s, ref := st.session()
	if ref != nil {
		return nil, ref
	}
	return st.dispatch(ctx, s, req)
}

// dispatch validates the provider, runs the bundle preamble, and drives
// the GitHub or provider backend.
func (st *state) dispatch(ctx context.Context, s *session, req *core.FetchRequest) (*core.FetchResponse, error) {
	f, ref := st.provider(req.Flags)
	if ref != nil {
		return nil, ref
	}
	ctx2 := &fetchRunCtx{runner: s, env: s.env, repo: req.Repo, pr: req.Pr, root: req.RootPath()}
	budget, _, err := fetchPrepare(ctx2, req, f)
	if err != nil {
		return nil, st.finalizeErr(err)
	}
	if f.Provider != providerGitHub && f.HasBase && f.BaseSHA != "" {
		return nil, st.withWarnings(&core.RefusalError{Code: "bad-flags", Message: "--base-sha/--since is GitHub-only for now " +
			"(providers have no incremental re-review yet)"})
	}
	data, next, err := st.backend(ctx, ctx2, req, f, budget)
	if err != nil {
		return nil, st.finalizeErr(err)
	}
	return st.response(req, data, next), nil
}

// provider validates the provider name, defaulting to GitHub like the
// flag parser.
func (st *state) provider(f core.FetchFlags) (core.FetchFlags, *core.RefusalError) {
	if f.Provider == "" {
		f.Provider = providerGitHub
	}
	switch f.Provider {
	case providerGitHub, "gitlab", "gitcafe", "forgejo", "bitbucket", "azure":
		return f, nil
	default:
		return f, st.withWarnings(&core.RefusalError{Code: "bad-provider", Message: fmt.Sprintf(
			"unknown --provider '%s' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)", f.Provider)})
	}
}

// backend drives the GitHub or provider backend for a validated request.
func (st *state) backend(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags, budget *budgetState,
) (*core.FetchData, string, error) {
	if f.Provider != providerGitHub {
		return runProvider(ctx, ctx2, req, f, budget)
	}
	return runGitHub(ctx, ctx2, req, f, budget)
}

// RunLocal executes `fetch-local <owner/repo> <name>`: the local worktree
// bundle build. Same transport contract as Run.
func RunLocal(ctx context.Context, req *core.FetchLocalRequest) (*core.FetchResponse, error) {
	st := newLocalState(req)
	if err := ctx.Err(); err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: "canceled before fetch completed"})
	}
	s, ref := st.session()
	if ref != nil {
		return nil, ref
	}
	ctx2 := &fetchRunCtx{runner: s, env: s.env, repo: req.Repo, pr: req.Pr, root: req.RootPath()}
	data, next, err := runLocalTail(ctx, ctx2, req)
	if err != nil {
		return nil, st.finalizeErr(err)
	}
	resp := &core.FetchResponse{
		Repo:     req.Repo,
		Pr:       req.Pr,
		At:       req.TimeUTC(),
		Data:     data,
		Warnings: st.warnings,
		Next:     next,
	}
	return resp, nil
}

// state carries one fetch run: the request plus the warnings accumulated
// before the response or refusal.
type state struct {
	req      *core.FetchRequest
	warnings []string
}

// newState seeds a fetch run with the adapter's warnings.
func newState(req *core.FetchRequest) *state {
	return &state{req: req, warnings: append([]string{}, req.Warnings...)}
}

// newLocalState seeds a fetch-local run with the adapter's warnings.
func newLocalState(req *core.FetchLocalRequest) *state {
	return &state{req: &core.FetchRequest{
		ReadRequest: req.ReadRequest,
		Effective:   req.Effective,
		JSONMode:    req.JSONMode,
		Warnings:    req.Warnings,
		Human:       req.Human,
		Diag:        req.Diag,
	}, warnings: append([]string{}, req.Warnings...)}
}

// withWarnings attaches the accumulated warnings to a refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

// finalizeErr maps a run error: typed refusals carry the warnings, the
// missing-tool kind passes through untouched (the adapter renders the
// frozen line with no envelope), and anything else becomes a fetch-failed
// refusal at the boundary.
func (st *state) finalizeErr(err error) error {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return st.withWarnings(ref)
	}
	if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
		return mt
	}
	return st.withWarnings(&core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("fetch failed: %v", err)})
}

// response builds the typed success response.
func (st *state) response(req *core.FetchRequest, data *core.FetchData, next string) *core.FetchResponse {
	return &core.FetchResponse{
		Repo:     req.Repo,
		Pr:       req.Pr,
		At:       req.TimeUTC(),
		Data:     data,
		Warnings: st.warnings,
		Next:     next,
	}
}

// session builds the injected session: the adapter's streams, the render
// configuration, the clock, and a warning-collecting progress observer.
func (st *state) session() (*session, *core.RefusalError) {
	cmdr, ref := st.commander()
	if ref != nil {
		return nil, ref
	}
	return &session{
		Human:     discardNil(st.req.Human),
		Diag:      discardNil(st.req.Diag),
		JSONMode:  st.req.JSONMode,
		Effective: st.req.Effective,
		Now:       st.clock(),
		env:       st.environ(),
		progress:  st.progress(),
		warnings:  &st.warnings,
		commander: cmdr,
	}, nil
}

// commander resolves the injected commander, refusing when the caller set
// none or a transport without the look-path seam.
func (st *state) commander() (Commander, *core.RefusalError) {
	raw := st.runtimeCommander()
	cmdr, ok := raw.(Commander)
	if !ok || cmdr == nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code: CodeFetchFailed, Message: "fetch failed: no commander in runtime",
		})
	}
	return cmdr, nil
}

// runtimeCommander returns the injected command runner (nil when unset).
func (st *state) runtimeCommander() core.Commander {
	rt := st.req.Runtime
	if rt == nil {
		return nil
	}
	return rt.Commander
}

// clock returns the injected clock, defaulting to the wall clock.
func (st *state) clock() func() time.Time {
	if rt := st.req.Runtime; rt != nil && rt.Now != nil {
		return rt.Now
	}
	return time.Now
}

// environ returns the injected environment snapshot.
func (st *state) environ() map[string]string {
	if rt := st.req.Runtime; rt != nil && rt.Env != nil {
		return rt.Env
	}
	return map[string]string{}
}

// progress returns the injected progress observer (nil when unset).
func (st *state) progress() core.Progress {
	if rt := st.req.Runtime; rt != nil {
		return rt.Progress
	}
	return nil
}

// discardNil guards adapter streams that tests leave unset.
func discardNil(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// session is the service's runner: the adapter's streams plus the warning
// accumulation and exec seam the moved bodies thread through. The iso
// field carries the fetch-local --worktree checkout (nil on every other
// path); the local bundle readers check it instead of branching on flags.
// Method names mirror the old *cli.Runner surface so the moved bodies stay
// byte-identical.
type session struct {
	Human     io.Writer
	Diag      io.Writer
	progress  core.Progress
	commander Commander
	Now       func() time.Time
	env       map[string]string
	warnings  *[]string
	iso       *isolation
	Effective core.Effective
	JSONMode  bool
}

// Warn records a warning and streams it through the progress observer,
// exactly like the CLI runner (the observer owns the `warn: ` prefix).
func (s *session) Warn(msg string) {
	*s.warnings = append(*s.warnings, msg)
	if s.progress != nil {
		s.progress.Warn(msg)
	}
}

// needTool ports `need`: a missing tool refuses with the missing-tool kind
// (the adapter prints the frozen `missing: <tool>` line with no envelope).
func (s *session) needTool(tool string) error {
	//nolint:wrapcheck // the seam error passes through by design; the adapter maps its kinds
	return s.commander.LookPath(tool)
}

// run spawns argv with an empty stdin, capturing both streams. A missing
// binary or a failing call returns the sealed error with streams attached;
// callers map failures to the fetch warn-or-refuse posture.
func (s *session) run(ctx context.Context, bin string, args ...string) (core.Result, error) {
	//nolint:wrapcheck // the seam error passes through by design; callers map it per site
	return s.commander.Run(ctx, core.Command{Argv: append([]string{bin}, args...)})
}

// ghFetch runs gh with exactly the given argv and a bounded secondary
// rate-limit retry (audit 21): unhinted failures run up to 3 attempts with
// the attempt-based sleep; only rate-limit signals retry, everything else
// fails fast like the shell. The caller owns the error sidecars and the
// warn-or-refuse posture.
func (s *session) ghFetch(ctx context.Context, args ...string) (core.Result, error) {
	return s.fetchWithRetry(ctx, "transient GitHub error — retry %d/2, bundle untouched", "gh", args)
}

// curlFetch runs curl with exactly the given argv and the same bounded
// secondary rate-limit retry as gh (providers share the backoff contract).
func (s *session) curlFetch(ctx context.Context, args ...string) (core.Result, error) {
	return s.fetchWithRetry(ctx, "transient GitHub error — retry %d/2, bundle untouched", "curl", args)
}

// fetchWithRetry runs one tool with the bounded secondary rate-limit retry:
// unhinted failures run up to 3 attempts with the attempt-based sleep. A
// missing binary never retries. Only failed calls retry, so a retry can
// never double-write (the bundle lands only after a success).
func (s *session) fetchWithRetry(ctx context.Context, retryMsg, bin string, args []string) (core.Result, error) {
	var res core.Result
	var err error
	for attempt := range maxFetchRetries {
		res, err = s.run(ctx, bin, args...)
		if err == nil || s.missing(err) || !s.scheduleRetry(res.Stderr, attempt, retryMsg) {
			break
		}
	}
	return res, err
}

// missing reports whether err is the missing-tool kind the adapter renders
// with no envelope.
func (s *session) missing(err error) bool {
	_, ok := errors.AsType[*core.MissingToolError](err)
	return ok
}

// scheduleRetry waits out one bounded retry, reporting whether another
// attempt may run.
func (s *session) scheduleRetry(stderr string, attempt int, retryMsg string) bool {
	wait, retry := retryDelay(stderr, attempt)
	if !retry || attempt >= maxFetchRetries-1 {
		return false
	}
	s.Warn(fmt.Sprintf(retryMsg, attempt+1))
	time.Sleep(wait)
	return true
}
