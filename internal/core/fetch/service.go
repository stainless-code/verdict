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

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/retry"
)

// CodeFetchFailed is the typed refusal for every fetch-internal failure
// that carries no narrower code. The adapter maps unknown errors to it as
// well.
const CodeFetchFailed = "fetch-failed"

// msgReadFailed formats an unreadable input artifact; the read sites share
// one literal so the message never drifts.
const msgReadFailed = "could not read %s: %v"

// codeCanceled names the cancel refusal the adapter exits 3 on.
const codeCanceled = "canceled"

// msgCanceledFetch is the frozen pre/post-cancel refusal message.
const msgCanceledFetch = "canceled before fetch completed"

// Commander is the exec seam every fetch spawn goes through: gh, glab,
// git, curl, and the az/cafe probes. The production implementation is
// [exec.Runner] (separate stdout/stderr capture, missing binaries
// read [core.MissingToolError]); tests inject a scripted commander and
// assert the exact argv. No service body spawns directly.
type Commander interface {
	Run(ctx context.Context, cmd *core.Command) (core.Result, error)
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
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch})
	}
	s, ref := st.session()
	if ref != nil {
		return nil, ref
	}
	return st.dispatch(ctx, s, req)
}

func (st *state) dispatch(ctx context.Context, s *session, req *core.FetchRequest) (*core.FetchResponse, error) {
	f, ref := st.provider(req.Flags)
	if ref != nil {
		return nil, ref
	}
	ctx2 := &fetchRunCtx{runner: s, env: s.env, repo: req.Repo, pr: req.Pr, root: req.RootPath()}
	budget, _, err := fetchPrepare(ctx2, req, f)
	if err != nil {
		return nil, st.fail(ctx, err, s)
	}
	if f.Provider != providerGitHub && f.HasBase && f.BaseSHA != "" {
		return nil, st.withWarnings(&core.RefusalError{Code: "bad-flags", Message: "--base-sha/--since is GitHub-only for now " +
			"(providers have no incremental re-review yet)"})
	}
	data, next, err := st.backend(ctx, ctx2, req, f, budget)
	if err != nil {
		return nil, st.fail(ctx, err, s)
	}
	return st.response(req, data, next), nil
}

func (st *state) provider(f core.FetchFlags) (core.FetchFlags, *core.RefusalError) {
	if f.Provider == "" {
		f.Provider = providerGitHub
	}
	switch f.Provider {
	case providerGitHub, providerGitLab, providerGitCafe, providerForgejo, providerBitbucket, providerAzure:
		return f, nil
	default:
		return f, st.withWarnings(&core.RefusalError{Code: codeBadProvider, Message: fmt.Sprintf(
			"unknown --provider '%s' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)", f.Provider)})
	}
}

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
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch})
	}
	s, ref := st.session()
	if ref != nil {
		return nil, ref
	}
	ctx2 := &fetchRunCtx{runner: s, env: s.env, repo: req.Repo, pr: req.Pr, root: req.RootPath()}
	data, next, err := runLocalTail(ctx, ctx2, req)
	if err != nil {
		return nil, st.fail(ctx, err, s)
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

// fail maps a run error: a missing tool keeps its frozen line, a canceled
// context refuses as canceled at once, and everything else goes through
// finalizeErr's typed/generic mapping.
func (st *state) fail(ctx context.Context, err error, s *session) error {
	if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
		return mt
	}
	if ctx.Err() != nil {
		return st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch})
	}
	return st.finalizeErr(err, s)
}

// finalizeErr maps a run error: typed refusals carry the warnings and any
// retry hint from the last exhausted transient call, the missing-tool kind
// passes through untouched (the adapter renders the frozen line with no
// envelope), and anything else becomes a fetch-failed refusal at the
// boundary.
func (st *state) finalizeErr(err error, s *session) error {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		attachRetryAt(ref, s)
		return st.withWarnings(ref)
	}
	if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
		return mt
	}
	ref := &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("fetch failed: %v", err)}
	attachRetryAt(ref, s)
	return st.withWarnings(ref)
}

// attachRetryAt copies a retryable failure's envelope hint onto a refusal.
func attachRetryAt(ref *core.RefusalError, s *session) {
	if s != nil && ref.RetryAt == "" {
		ref.RetryAt = s.retryAt
	}
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
		repoRoot:  st.req.RepoRootPath(),
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

type session struct {
	Human     io.Writer
	Diag      io.Writer
	progress  core.Progress
	commander Commander
	warnings  *[]string
	env       map[string]string
	Now       func() time.Time
	iso       *isolation
	controls  *controls.Snapshot
	Effective core.Effective
	// repoRoot is the reviewed repository root every bundle path
	// anchors to, resolved once per run from the request.
	repoRoot string
	retryAt  string
	privacy  config.PrivacyView
	JSONMode bool
}

// display renders a bundle path for the human stream: repository-relative
// when the bundle lives under the run's root, so the operator-facing line
// never carries an absolute local path.
func (s *session) display(dir string) string {
	return readcommon.DisplayPath(s.repoRoot, dir)
}

// nowUTC renders the session clock in the feed's fetched_at format.
func (s *session) nowUTC() string {
	if s.Now == nil {
		return core.NowUTC(time.Now())
	}
	return core.NowUTC(s.Now())
}

// retryTime formats the session clock plus a wait for the refusal envelope.
func (s *session) retryTime(wait time.Duration) string {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return retry.At(now, wait)
}

func (s *session) Warn(msg string) {
	*s.warnings = append(*s.warnings, msg)
	if s.progress != nil {
		s.progress.Warn(msg)
	}
}

// needTool requires the tool on PATH: a missing tool refuses with the
// missing-tool kind (the adapter prints the frozen `missing: <tool>` line
// with no envelope).
func (s *session) needTool(tool string) error {
	//nolint:wrapcheck // the seam error passes through by design; the adapter maps its kinds
	return s.commander.LookPath(tool)
}

// run spawns argv with the runner's stdin default, capturing both streams.
// A missing binary or a spawn failure returns the seam error with streams
// attached; a completed process with a non-zero wait code returns the same
// shell-visible failure text, and callers map both to the fetch
// warn-or-refuse posture.
func (s *session) run(ctx context.Context, bin string, args ...string) (core.Result, error) {
	res, err := s.commander.Run(ctx, &core.Command{Argv: append([]string{bin}, args...)})
	if err != nil {
		return res, err //nolint:wrapcheck // the seam error passes through by design; callers map it per site
	}
	if res.ExitCode != 0 {
		return res, res.ExitFailure(bin) //nolint:wrapcheck // locally constructed exit-status error, not an external error
	}
	return res, nil
}

// ghFetch runs gh with exactly the given argv and the shared bounded
// transient retry. The caller owns the error sidecars and the
// warn-or-refuse posture.
func (s *session) ghFetch(ctx context.Context, args ...string) (core.Result, error) {
	return s.fetchWithRetry(ctx, "gh", args)
}

// curlFetch runs curl with exactly the given argv and the same bounded
// transient retry as gh (providers share one backoff contract).
func (s *session) curlFetch(ctx context.Context, args ...string) (core.Result, error) {
	return s.fetchWithRetry(ctx, "curl", args)
}

// fetchWithRetry runs one tool with the shared bounded transient retry:
// secondary rate limits and the host's internal-error retry note run up to
// retry.MaxAttempts attempts with the attempt-based sleep. A missing binary
// never retries. Only failed calls retry, so a retry can never double-write
// (the bundle lands only after a success).
//
//nolint:revive // the bounded retry loop branches are the retry contract
func (s *session) fetchWithRetry(ctx context.Context, bin string, args []string) (core.Result, error) {
	var res core.Result
	var err error
	s.retryAt = ""
	for attempt := range retry.MaxAttempts {
		res, err = s.run(ctx, bin, args...)
		if err == nil || s.missing(err) {
			return res, err
		}
		wait, ok := retry.Delay(res.Stderr, attempt)
		if !ok {
			return res, err
		}
		exhausted, waitErr := s.awaitRetry(ctx, wait, attempt)
		if waitErr != nil {
			return res, waitErr
		}
		if exhausted {
			return res, err
		}
	}
	return res, err
}

// awaitRetry waits before the next attempt. The last attempt records the
// refusal's retry_at and reports exhaustion; a canceled wait returns the
// context error.
func (s *session) awaitRetry(ctx context.Context, wait time.Duration, attempt int) (bool, error) {
	if attempt >= retry.MaxAttempts-1 {
		s.retryAt = s.retryTime(wait)
		return true, nil
	}
	s.Warn(fmt.Sprintf("transient GitHub error — retry %d/2, bundle untouched", attempt+1))
	if !retry.Sleep(ctx, wait) {
		return false, ctx.Err() //nolint:wrapcheck // the context error is the cancellation signal
	}
	return false, nil
}

// missing reports whether err is the missing-tool kind the adapter renders
// with no envelope.
func (s *session) missing(err error) bool {
	_, ok := errors.AsType[*core.MissingToolError](err)
	return ok
}
