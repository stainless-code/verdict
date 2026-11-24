package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/storelock"
)

// Tier B interactive jobs: operator-clicked verify --sandbox and post
// --dry-run over the existing SSE channel. Jobs run on the server with
// bounded concurrency and bounded retained history, each holding a
// bundle-scoped storelock while running so a concurrent write and a job
// never interleave on one bundle.
const (
	jobKindVerify      = "verify"
	jobKindPostPreview = "post-preview"

	jobStatusQueued    = "queued"
	jobStatusRunning   = "running"
	jobStatusSucceeded = "succeeded"
	jobStatusFailed    = "failed"
	jobStatusCancelled = "cancelled" //nolint:misspell // ADR-0004 spells the cancelled state "cancelled".

	// maxConcurrentJobs bounds how many jobs run at once; the rest wait
	// queued and start as slots free.
	maxConcurrentJobs = 4
	// maxRetainedJobs bounds the in-memory history, including active jobs.
	maxRetainedJobs = 64
	// verifyTimeoutS is the watchdog budget every sandboxed verify runs
	// under: the CLI default, so a job verifies what `verdict verify
	// --sandbox` would.
	verifyTimeoutS = "300"
	// jobIDFallbackLen is the prefix of the fallback token used as a job id.
	jobIDFallbackLen = 16
	// reasonNotFound is the transport reason for an absent job or finding.
	reasonNotFound = "not-found"
	// codeBundleUnreadable refuses a preview whose bundle cannot be read
	// back for the staleness fingerprint.
	codeBundleUnreadable = "bundle-unreadable"
	// codePreviewUnpinnable refuses a preview whose rendered review carries no
	// content digest, so no nonce is minted and no post could be pinned.
	codePreviewUnpinnable = "preview-unpinnable"
	// codeNonceFailed refuses a preview when the CSPRNG cannot mint a nonce.
	codeNonceFailed = "nonce-failed"
	// jobPathVerify is the POST verify job route.
	jobPathVerify = "/v1/jobs/verify"
	// jobPathPostPreview is the POST post-preview job route.
	jobPathPostPreview = "/v1/jobs/post-preview"
)

// VerifyRunner executes one sandboxed verify. The production
// implementation runs verifycore.Run with Sandbox hardcoded true; tests
// inject a scripted in-memory executor.
type VerifyRunner func(ctx context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError)

// PostPreviewRunner executes one post preview. The production
// implementation runs postcore.Run with DryRun hardcoded true so no host
// write can ever ship; tests inject a scripted in-memory executor.
type PostPreviewRunner func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError)

// job is one queued or finished Tier B action.
type job struct {
	result    *jq.Obj
	done      chan struct{}
	cancel    context.CancelFunc
	err       *jobError
	bundleDir string
	status    string
	createdAt string
	updatedAt string
	rootPath  string
	id        string
	bundle    string
	rootID    string
	kind      string
	warnings  []string
}

// jobError is one job failure wire shape: the service refusal code and
// message, or store-busy when the bundle lock was held.
type jobError struct {
	code    string
	message string
}

// jobManager is the in-memory job store the interactive routes share.
type jobManager struct {
	server        *Server
	jobs          map[string]*job
	sem           chan struct{}
	order         []string
	wg            sync.WaitGroup
	lockWait      time.Duration
	maxConcurrent int
	maxRetained   int
	stopOne       sync.Once
	mu            sync.Mutex
	stopped       bool
}

// newJobManager builds the bounded in-memory manager. Shutdown cancels
// every active job through its own context and joins the workers.
func newJobManager(server *Server) *jobManager {
	lockWait := server.cfg.LockWait
	if lockWait <= 0 {
		lockWait = storelock.DefaultWait
	}
	return &jobManager{
		server:        server,
		jobs:          map[string]*job{},
		sem:           make(chan struct{}, maxConcurrentJobs),
		lockWait:      lockWait,
		maxConcurrent: maxConcurrentJobs,
		maxRetained:   maxRetainedJobs,
	}
}

// newJobID mints a path-safe job id from the OS CSPRNG.
func newJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return newToken()[:jobIDFallbackLen]
}

// create records one queued job and starts its worker. It evicts the
// oldest finished job when the retained history is full, and refuses
// only when the history is full of active jobs. The parent roots every
// job context; the handler passes a background context so a job outlives
// its POST.
func (m *jobManager) create(
	parent context.Context, kind, rootID, bundle, rootPath, bundleDir string,
) (*job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, false
	}
	if len(m.jobs) >= m.maxRetained {
		if !m.evictLocked() {
			return nil, false
		}
	}
	now := m.server.at()
	ctx, cancel := context.WithCancel(parent)
	current := &job{
		id:        newJobID(),
		kind:      kind,
		rootID:    rootID,
		bundle:    bundle,
		status:    jobStatusQueued,
		createdAt: now,
		updatedAt: now,
		rootPath:  rootPath,
		bundleDir: bundleDir,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	m.jobs[current.id] = current
	m.order = append(m.order, current.id)
	snapshot := m.snapshotLocked(current)
	m.server.events.publishJobs(snapshot)
	m.wg.Add(1)
	go m.run(ctx, current)
	return current, true
}

// evictLocked drops the oldest finished job from the retained history.
func (m *jobManager) evictLocked() bool {
	for i, id := range m.order {
		candidate := m.jobs[id]
		if candidate != nil && isTerminal(candidate.status) {
			delete(m.jobs, id)
			m.order = append(m.order[:i], m.order[i+1:]...)
			return true
		}
	}
	return false
}

// isTerminal reports whether a job status ends the lifecycle.
func isTerminal(status string) bool {
	return status == jobStatusSucceeded || status == jobStatusFailed || status == jobStatusCancelled
}

// get returns one job snapshot and whether it exists.
func (m *jobManager) get(id string) (*job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.jobs[id]
	return current, ok
}

// cancel asks one non-terminal job to stop through its context.
func (m *jobManager) cancel(id string) (*job, bool) {
	m.mu.Lock()
	current, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	terminal := isTerminal(current.status)
	cancel := current.cancel
	m.mu.Unlock()
	if !terminal {
		cancel()
	}
	m.mu.Lock()
	latest := m.jobs[id]
	m.mu.Unlock()
	// The worker may have evicted the id while cancel raced a new
	// create; a missing latest reads as the current handle.
	if latest == nil {
		return current, true
	}
	return latest, true
}

// snapshotLocked renders one job in the SSE and GET wire shape. The
// caller holds m.mu.
func (m *jobManager) snapshotLocked(current *job) *jq.Obj {
	snap := jq.NewObj(
		"id", current.id,
		"kind", current.kind,
		"root", current.rootID,
		"bundle", current.bundle,
	)
	snap.Set("status", current.status)
	snap.Set("created_at", current.createdAt)
	snap.Set("updated_at", current.updatedAt)
	if current.result != nil {
		snap.Set("result", current.result)
	}
	if current.err != nil {
		snap.Set("error", jq.NewObj("code", current.err.code, "message", current.err.message))
	}
	if len(current.warnings) > 0 {
		wire := make([]any, 0, len(current.warnings))
		for _, warning := range current.warnings {
			wire = append(wire, warning)
		}
		snap.Set("warnings", wire)
	}
	return snap
}

// snapshot renders one job snapshot for the transport without holding the
// lock across the publish.
func (m *jobManager) snapshot(current *job) *jq.Obj {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(current)
}

// setStatus records one transition and publishes it on the jobs SSE
// stream. A terminal job never leaves its terminal status.
func (m *jobManager) setStatus(
	current *job, status string, result *jq.Obj, failure *jobError, warnings []string,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[current.id]; !ok {
		return
	}
	if isTerminal(current.status) && !isTerminal(status) {
		return
	}
	current.status = status
	current.updatedAt = m.server.at()
	if result != nil {
		current.result = result
	}
	if failure != nil {
		current.err = failure
	}
	if warnings != nil {
		current.warnings = warnings
	}
	snapshot := m.snapshotLocked(current)
	m.server.events.publishJobs(snapshot)
}

// stop cancels every active job and joins their workers.
func (m *jobManager) stop() {
	m.stopOne.Do(func() {
		m.mu.Lock()
		m.stopped = true
		for _, current := range m.jobs {
			if !isTerminal(current.status) {
				current.cancel()
			}
		}
		m.mu.Unlock()
		m.wg.Wait()
	})
}

// run is one job worker: semaphore admission, bundle lock, the injected
// runner, and the terminal transition with cache invalidation for verify.
func (m *jobManager) run(ctx context.Context, current *job) {
	defer m.wg.Done()
	defer close(current.done)
	if !m.acquireSlot(ctx, current) {
		return
	}
	defer func() { <-m.sem }()
	if ctx.Err() != nil {
		m.setStatus(current, jobStatusCancelled, nil, nil, nil)
		return
	}
	m.setStatus(current, jobStatusRunning, nil, nil, nil)
	unlock, failure := m.takeBundleLock(ctx, current)
	if failure != nil {
		if ctx.Err() != nil {
			m.setStatus(current, jobStatusCancelled, nil, nil, nil)
			return
		}
		m.setStatus(current, jobStatusFailed, nil, failure, nil)
		return
	}
	defer unlock()
	m.execute(ctx, current)
}

// acquireSlot waits for a concurrency slot or cancellation.
func (m *jobManager) acquireSlot(ctx context.Context, current *job) bool {
	select {
	case m.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		m.setStatus(current, jobStatusCancelled, nil, nil, nil)
		return false
	}
}

// takeBundleLock takes the bundle findings-store lock for one job run.
// The path shares triage's findings.json lock — distinct from the
// knowledge-base lock — so a job and a write on one bundle serialize
// without lock nesting.
func (m *jobManager) takeBundleLock(ctx context.Context, current *job) (func(), *jobError) {
	store := current.bundleDir + "/findings.json"
	unlock, err := storelock.AcquireContext(ctx, store, m.lockWait)
	if err == nil {
		return unlock, nil
	}
	if ctx.Err() != nil {
		return nil, &jobError{code: "canceled", message: "canceled before the job started"}
	}
	if errors.Is(err, storelock.ErrBusy) {
		return nil, &jobError{
			code:    "store-busy",
			message: "another write holds the store lock on " + store + " (retry in a moment)",
		}
	}
	return nil, &jobError{code: "internal", message: fmt.Sprintf("could not take the store lock on %s: %v", store, err)}
}

// execute runs the kind's runner and records the terminal state.
func (m *jobManager) execute(ctx context.Context, current *job) {
	if current.kind == jobKindVerify {
		m.runVerify(ctx, current)
		return
	}
	m.runPostPreview(ctx, current)
}

// runVerify runs the sandboxed verify through the injected runner.
func (m *jobManager) runVerify(ctx context.Context, current *job) {
	runner := m.runnerVerify()
	read := core.ReadRequest{
		Runtime:  m.server.runtime(),
		Repo:     readcommon.BundleProject(current.bundleDir),
		Pr:       current.bundle,
		RepoRoot: current.rootPath,
	}
	req := &verifycore.Request{
		ReadRequest: read,
		TimeoutRaw:  verifyTimeoutS,
		Environ:     os.Environ(),
		Sandbox:     true,
	}
	resp, ref := runner(ctx, req)
	if ref == nil {
		result := resp.Data.Wire()
		m.server.layer.Invalidate()
		m.setStatus(current, jobStatusSucceeded, result, nil, resp.Warnings)
		return
	}
	m.server.layer.Invalidate()
	if ctx.Err() != nil {
		m.setStatus(current, jobStatusCancelled, nil, nil, nil)
		return
	}
	m.setStatus(current, jobStatusFailed, nil,
		&jobError{code: ref.Code, message: ref.Message}, ref.Warnings)
}

// runPostPreview runs the dry-run post preview through the injected
// runner. DryRun is hardcoded true here and again in the production
// runner, so no host write can ship through this path. On an --allow-post
// server a succeeded preview mints the Tier C single-use nonce bound to its
// digest and the bundle fingerprint, published beside the preview wire; a
// plain --interactive server mints nothing and its result is the bare wire.
func (m *jobManager) runPostPreview(ctx context.Context, current *job) {
	runner := m.runnerPostPreview()
	// The fingerprint is taken before the dry-run reads the bundle, under the
	// findings-store lock this job already holds, so a write that lands during
	// the preview moves the live fingerprint off this one and the submit
	// refuses 409 stale-preview instead of baking the write into a "fresh" one.
	fingerprint, failure := m.previewFingerprint(current)
	if failure != nil {
		m.setStatus(current, jobStatusFailed, nil, failure, nil)
		return
	}
	// The target repository is resolved once, after the fingerprint has read
	// source.json without blocking, and the dry-run and the minted entry both
	// carry that value.
	repo := bundleRepo(current.bundleDir)
	resp, ref := runner(ctx, m.server.previewRequest(current.bundleDir, current.bundle, current.rootPath, repo))
	if ctx.Err() != nil {
		m.setStatus(current, jobStatusCancelled, nil, nil, nil)
		return
	}
	if ref != nil {
		m.setStatus(current, jobStatusFailed, nil,
			&jobError{code: ref.Code, message: ref.Message}, ref.Warnings)
		return
	}
	result := resp.Data.Wire()
	if m.server.previews != nil {
		if failure := m.pinPreview(current, fingerprint, repo, resp, result); failure != nil {
			m.setStatus(current, jobStatusFailed, nil, failure, nil)
			return
		}
	}
	m.setStatus(current, jobStatusSucceeded, result, nil, resp.Warnings)
}

// pinPreview publishes the review text on result and mints the confirmation
// pin for it. The text goes onto the wire before the digest is taken, so the
// digest binds exactly what the confirm dialog shows. A preview that omitted
// any posted text mints no nonce or digest: the operator cannot confirm what
// was not shown, so a submit can never spend a pin for it, whatever the client.
func (m *jobManager) pinPreview(
	current *job, fingerprint, repo string, resp *postcore.Response, result *jq.Obj,
) *jobError {
	result.Set("target_repo", repo)
	if setReviewComments(result, resp.Data.RenderedComments, repo, current.bundle) {
		return nil
	}
	return m.bindPreview(current, fingerprint, repo, resp.Data.RenderedHash, result)
}

// previewRequest is the dry-run request a preview job and a submit's re-render
// share, so both render the review identically. DryRun is hardcoded true.
func (s *Server) previewRequest(bundleDir, bundle, rootPath, repo string) *postcore.Request {
	return &postcore.Request{
		ReadRequest: s.postReadRequest(bundle, rootPath, repo),
		Flags:       postcore.Flags{Approve: approveFalse, ReviewFile: bundleDir + "/review.json", DryRun: true},
		Effective:   s.cfg.Effective,
	}
}

// maxReviewPreviewBytes bounds the review text a preview publishes for the
// confirm dialog, summed over every comment the post sends. A split review is
// over the hosts' 64 KiB comment limit by definition, so the bound is four
// such comments: a normal review, split or not, shows whole, and a larger one
// is cut with the cut named on the wire.
const maxReviewPreviewBytes = 256 << 10

// setReviewComments publishes every comment the post will send beside the
// preview wire, in posting order, so the dialog shows all of what the digest
// pins. Each entry carries its label, its text (cut at a rune boundary when
// the running total reaches [maxReviewPreviewBytes]), the bytes of it shown,
// and its full byte size, so a cut is explicit per comment. Comments the
// bound leaves out entirely are named in review_omitted;
// review_comment_count is the full count. It reports whether any posted text
// was left out; when it was, review_truncated carries the bytes cut, the
// comments omitted, and the CLI command that prints the whole payload.
func setReviewComments(result *jq.Obj, comments []core.RenderedComment, repo, bundle string) bool {
	shown := make([]any, 0, len(comments))
	omitted := make([]any, 0)
	budget := maxReviewPreviewBytes
	bytesCut := 0
	for _, c := range comments {
		if budget <= 0 {
			omitted = append(omitted, c.Label)
			bytesCut += len(c.Text)
			continue
		}
		text := cutAtRune(c.Text, budget)
		budget -= len(text)
		bytesCut += len(c.Text) - len(text)
		shown = append(shown, jq.NewObj("label", c.Label, "text", text, "shown_bytes", len(text), "bytes", len(c.Text)))
	}
	result.Set("review_comments", shown)
	result.Set("review_comment_count", len(comments))
	result.Set("review_omitted", omitted)
	if bytesCut == 0 && len(omitted) == 0 {
		return false
	}
	target := repo
	if target == "" {
		target = "<repo>"
	}
	result.Set("review_truncated", jq.NewObj(
		"bytes_cut", bytesCut,
		"comments_omitted", len(omitted),
		"message", fmt.Sprintf("The preview did not show the whole review (%d bytes cut, %d comments omitted), "+
			"so it cannot be confirmed here. Inspect the full payload with `verdict post %s %s --dry-run` "+
			"and post it from the CLI.", bytesCut, len(omitted), target, bundle),
	))
	return true
}

// cutAtRune returns text cut to at most limit bytes, backed up to a rune start
// so the cut never lands inside a character.
func cutAtRune(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// previewFingerprint fingerprints the bundle before the dry-run when the
// server can confirm a post; otherwise there is nothing to bind and it
// returns the empty fingerprint. A bundle it cannot fingerprint refuses the
// preview: there is nothing confirmable to hand the operator.
func (m *jobManager) previewFingerprint(current *job) (string, *jobError) {
	if m.server.previews == nil {
		return "", nil
	}
	fingerprint, err := bundleFingerprint(current.bundleDir)
	if err != nil {
		return "", &jobError{
			code:    codeBundleUnreadable,
			message: "could not fingerprint the bundle for a preview: " + err.Error(),
		}
	}
	return fingerprint, nil
}

// bindPreview mints the confirmation nonce for one succeeded preview and
// publishes the digest and nonce on its wire. A nonce the CSPRNG cannot mint
// refuses the preview: there is nothing confirmable to hand the operator.
func (m *jobManager) bindPreview(current *job, fingerprint, repo, rendered string, result *jq.Obj) *jobError {
	digest := previewDigest(current.rootID, current.bundle, fingerprint, []byte(result.MarshalCompact()))
	nonce, err := m.server.previews.mint(current.rootID, current.bundle, digest, fingerprint, rendered, repo)
	if errors.Is(err, errUnpinnable) {
		return &jobError{code: codePreviewUnpinnable, message: err.Error()}
	}
	if err != nil {
		return &jobError{code: codeNonceFailed, message: err.Error()}
	}
	result.Set("preview_digest", digest)
	result.Set("preview_nonce", nonce)
	return nil
}

// runnerVerify returns the injected verify runner, defaulting to production.
func (m *jobManager) runnerVerify() VerifyRunner {
	if m.server.verifyRunner != nil {
		return m.server.verifyRunner
	}
	return prodVerifyRunner
}

// runnerPostPreview returns the injected post runner, defaulting to production.
func (m *jobManager) runnerPostPreview() PostPreviewRunner {
	if m.server.postPreviewRunner != nil {
		return m.server.postPreviewRunner
	}
	return prodPostPreviewRunner
}

// prodVerifyRunner runs the real verify service strictly sandboxed.
func prodVerifyRunner(
	ctx context.Context, req *verifycore.Request,
) (*verifycore.Response, *core.RefusalError) {
	req.Sandbox = true
	if req.TimeoutRaw == "" {
		req.TimeoutRaw = verifyTimeoutS
	}
	if req.Environ == nil {
		req.Environ = os.Environ()
	}
	if req.Runtime == nil {
		req.Runtime = &core.Runtime{}
	}
	if req.Env == nil {
		// The server supplies the configured map; a hand-built request still
		// gets a writable one.
		req.Env = map[string]string{}
	}
	if req.Commander == nil {
		req.Commander = coreexec.Watchdog{}
	}
	return verifycore.Run(ctx, req)
}

// prodPostPreviewRunner runs the real post service strictly as a dry run.
func prodPostPreviewRunner(
	ctx context.Context, req *postcore.Request,
) (*postcore.Response, *core.RefusalError) {
	req.Flags.DryRun = true
	if req.Flags.Approve == "" {
		req.Flags.Approve = "false"
	}
	if req.Runtime == nil {
		req.Runtime = &core.Runtime{}
	}
	if req.Env == nil {
		// The server supplies the configured map; a hand-built request still
		// gets a writable one.
		req.Env = map[string]string{}
	}
	if req.Commander == nil {
		req.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	}
	return postcore.Run(ctx, req)
}

// verifyJobBody is the POST /v1/jobs/verify wire shape. Verify is
// bundle-scoped: it runs verify --sandbox over the whole bundle, so the
// shape carries only root and bundle.
type verifyJobBody struct {
	Root   string `json:"root"`
	Bundle string `json:"bundle"`
}

// postPreviewBody is the POST /v1/jobs/post-preview wire shape.
type postPreviewBody struct {
	Root   string `json:"root"`
	Bundle string `json:"bundle"`
}

// handleVerifyJob queues a bundle-scoped sandboxed verify: strictly
// Sandbox true, over the whole bundle. Verify has no single-finding
// filter, so the wire shape names only root and bundle.
func (s *Server) handleVerifyJob(w http.ResponseWriter, r *http.Request) {
	var body verifyJobBody
	if !decodeJSON(w, r, s, &body) {
		return
	}
	rootPath, bundleDir, ok := s.resolveWrite(w, body.Root, body.Bundle)
	if !ok {
		return
	}
	s.queueJob(context.WithoutCancel(r.Context()), w, jobKindVerify, body.Root, body.Bundle, rootPath, bundleDir)
}

// handlePostPreviewJob queues a post preview: strictly DryRun true, so
// no host write can ever ship.
func (s *Server) handlePostPreviewJob(w http.ResponseWriter, r *http.Request) {
	var body postPreviewBody
	if !decodeJSON(w, r, s, &body) {
		return
	}
	rootPath, bundleDir, ok := s.resolveWrite(w, body.Root, body.Bundle)
	if !ok {
		return
	}
	s.queueJob(context.WithoutCancel(r.Context()), w, jobKindPostPreview, body.Root, body.Bundle, rootPath, bundleDir)
}

// handleCancelJob cancels one running job through its context; a settled
// job answers with its current snapshot.
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	current, ok := s.jobs.cancel(r.PathValue("id"))
	if !ok {
		s.problem(w, http.StatusNotFound, reasonNotFound, "no such job")
		return
	}
	s.writeJob(w, current, http.StatusOK)
}

// handleGetJob looks up one job in interactive mode, scoped to the
// authenticated session the guard already required.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	current, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		s.problem(w, http.StatusNotFound, reasonNotFound, "no such job")
		return
	}
	s.writeJob(w, current, http.StatusOK)
}

// queueJob creates one queued job and answers 202 with its snapshot.
func (s *Server) queueJob(
	parent context.Context, w http.ResponseWriter, kind, rootID, bundle, rootPath, bundleDir string,
) {
	current, ok := s.jobs.create(parent, kind, rootID, bundle, rootPath, bundleDir)
	if !ok {
		s.problem(w, http.StatusServiceUnavailable, "job-capacity", "job history is full of active jobs; retry later")
		return
	}
	s.writeJob(w, current, http.StatusAccepted)
}

// writeJob writes one job snapshot as the envelope data at status.
func (s *Server) writeJob(w http.ResponseWriter, current *job, status int) {
	snapshot := s.jobs.snapshot(current)
	s.writeEnvelopeStatus(w, status, core.RenderEnvelope(verbServe, "", "", s.at(), "", nil, snapshot, nil))
}

// writeEnvelopeStatus writes one envelope body at status.
func (s *Server) writeEnvelopeStatus(w http.ResponseWriter, status int, env *jq.Obj) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	fmt.Fprint(w, jq.PrettyOrdered(env))
}
