package serve

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/storelock"
)

// Tier C host posting: an explicit --allow-post opt-in beyond --interactive.
// A fresh post-preview mints a single-use nonce bound to a preview digest;
// the submit route validates it, takes the bundle lock, re-checks the bundle
// fingerprint, spends the nonce under that lock, and only then delegates to
// core/post exactly like the CLI `verdict post` does.
const (
	// postSubmitPath is the POST host-submit route.
	postSubmitPath = "/v1/post/submit"

	// previewTTL is how long a minted preview nonce stays valid.
	previewTTL = 10 * time.Minute
	// maxPreviews bounds the in-memory preview store.
	maxPreviews = 64
	// previewNonceBytes is the CSPRNG nonce size in bytes.
	previewNonceBytes = 16
	// approveFalse is the post approval default the submit sends.
	approveFalse = "false"
	// reasonStalePreview is the transport reason for a spent preview.
	reasonStalePreview = "stale-preview"

	// postSubmitTimeout bounds one confirmed host submit, lock wait included.
	// The submit detaches from the browser connection (a started host write
	// is not abandoned because the tab closed), so without a bound a stalled
	// `gh` or `curl` would hold the bundle storelock, and answer every triage
	// and job on that bundle `store-busy`, until the process exited. The repo
	// has no host-call budget to reuse (`run` and `demo` budget agent runs, at
	// 300 s), so this takes five minutes: far past a healthy review POST,
	// short of a wedged host.
	postSubmitTimeout = 5 * time.Minute
	// codePostTimeout is the typed refusal a timed-out submit answers.
	codePostTimeout = "post-timeout"
	// codeCanceled is core/post's cancellation refusal code.
	codeCanceled = "canceled"
)

// PostSubmitRunner executes one real post. The production implementation
// runs postcore.Run with DryRun false; tests inject the same call wired to
// a stubbed host commander so receipt parity is proven offline.
type PostSubmitRunner func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError)

// previewEntry is one minted preview: the digest and bundle fingerprint the
// submit route re-validates, plus its expiry.
type previewEntry struct {
	expiresAt   time.Time
	root        string
	bundle      string
	digest      string
	fingerprint string
}

// previewStore is the bounded, mutex-protected, TTL-expiring single-use
// nonce store. The lock is held only to look up, validate, and consume one
// nonce atomically. The clock and the CSPRNG source are fixed once, before
// the store is shared.
type previewStore struct {
	entries map[string]*previewEntry
	now     func() time.Time
	random  func([]byte) (int, error)
	ttl     time.Duration
	mu      sync.Mutex
}

// newPreviewStore builds the store for an --allow-post server and nil for any
// other: without a submit route there is no nonce to spend, so no preview
// mints one, the Tier B job result keeps its shape, and nothing fills the
// store. The clock is fixed here, after applyServerDefaults, so no request
// ever lazily assigns it twice.
func newPreviewStore(cfg *Config) *previewStore {
	if !cfg.AllowPost {
		return nil
	}
	return &previewStore{entries: map[string]*previewEntry{}, now: cfg.Now, ttl: previewTTL}
}

// nonceReader names the CSPRNG source; nil means crypto/rand.
func (st *previewStore) nonceReader() func([]byte) (int, error) {
	if st.random == nil {
		return rand.Read
	}
	return st.random
}

// mint binds one digest and fingerprint to a fresh nonce and stores it.
func (st *previewStore) mint(root, bundle, digest, fingerprint string) (string, error) {
	nonce, err := mintPreviewNonce(st.nonceReader())
	if err != nil {
		return "", err
	}
	now := st.clock()
	ttl := st.ttl
	if ttl <= 0 {
		ttl = previewTTL
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.evictLocked(now)
	if len(st.entries) >= maxPreviews {
		st.dropOneLocked()
	}
	st.entries[nonce] = &previewEntry{
		root: root, bundle: bundle, digest: digest,
		fingerprint: fingerprint, expiresAt: now.Add(ttl),
	}
	return nonce, nil
}

// clock reads the store's injected clock. The field is written once, before
// the store is shared, so reading it here needs no lock.
func (st *previewStore) clock() time.Time {
	if st.now == nil {
		return time.Now()
	}
	return st.now()
}

// peek validates one nonce against root, bundle, and digest without spending
// it, so a retryable failure after validation (a busy store lock, a lock wait
// that timed out) leaves the nonce usable. An expired or mismatched entry is
// removed on the attempt, exactly as before: only a valid nonce survives.
func (st *previewStore) peek(nonce, root, bundle, digest string) (*previewEntry, bool) {
	return st.take(nonce, root, bundle, digest, false)
}

// consume validates one nonce against root, bundle, and digest and deletes
// it in the same critical section, so a nonce can never be spent twice: of
// two concurrent submits with one nonce, exactly one gets the entry.
func (st *previewStore) consume(nonce, root, bundle, digest string) (*previewEntry, bool) {
	return st.take(nonce, root, bundle, digest, true)
}

// take is the shared lookup. It removes the entry when spend is set or when
// the entry is expired or mismatched, all under one hold of the store mutex.
func (st *previewStore) take(nonce, root, bundle, digest string, spend bool) (*previewEntry, bool) {
	now := st.clock()
	st.mu.Lock()
	defer st.mu.Unlock()
	entry, ok := st.entries[nonce]
	if !ok {
		return nil, false
	}
	valid := entry.expiresAt.After(now) &&
		entry.root == root && entry.bundle == bundle && entry.digest == digest
	if spend || !valid {
		delete(st.entries, nonce)
	}
	if !valid {
		return nil, false
	}
	return entry, true
}

// evictLocked drops expired entries. The caller holds st.mu.
func (st *previewStore) evictLocked(now time.Time) {
	for nonce, entry := range st.entries {
		if !entry.expiresAt.After(now) {
			delete(st.entries, nonce)
		}
	}
}

// dropOneLocked drops the live entry nearest its expiry to keep the store
// bounded when it is full of live nonces: the oldest preview is the one the
// operator is least likely to still confirm. The caller holds st.mu.
func (st *previewStore) dropOneLocked() {
	var victim string
	var soonest time.Time
	for nonce, entry := range st.entries {
		if victim == "" || entry.expiresAt.Before(soonest) {
			victim, soonest = nonce, entry.expiresAt
		}
	}
	delete(st.entries, victim)
}

// mintPreviewNonce mints one CSPRNG nonce as lowercase hex. There is no
// fallback: a nonce the server cannot generate is a preview it must refuse.
func mintPreviewNonce(read func([]byte) (int, error)) (string, error) {
	var b [previewNonceBytes]byte
	if _, err := read(b[:]); err != nil {
		return "", jq.Sealed("serve: mint preview nonce", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// hashField frames one hashed field as an 8-byte big-endian length plus its
// bytes, so no two field sequences hash the same input. Bare concatenation
// lets content move between neighbors and keep the same digest.
func hashField(dst hash.Hash, field []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(field)))
	dst.Write(size[:])
	dst.Write(field)
}

// fingerprintFiles are the bundle files core/post decides from, so a change
// to any of them after the preview moves the fingerprint: the findings, the PR
// or local source, the review draft, the duplicate-post receipt (post.json),
// the controls manifest whose pinned bytes the gate re-verifies, the stored
// decision record, the patch identity, and the provenance receipt.
var fingerprintFiles = []string{
	"findings.json", "PR.json", "source.json", "review.json", "post.json",
	controls.FileName, "record.json", "identity.json", provenance.ReceiptFile,
}

// bundleFingerprint hashes the bundle state the preview read: every file in
// [fingerprintFiles] plus the diff. Any triage or re-fetch after the preview
// moves it, and the submit refuses as stale. A file that is absent is not part
// of the state; a file that exists and cannot be read is an error, never a
// silently skipped input.
func bundleFingerprint(bundleDir string) (string, error) {
	sum := sha256.New()
	for _, name := range fingerprintFiles {
		raw, err := os.ReadFile(filepath.Join(bundleDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", jq.Sealed("serve: read "+name, err)
		}
		hashField(sum, []byte(name))
		hashField(sum, raw)
	}
	if err := hashDiff(sum, filepath.Join(bundleDir, "full.diff")); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// hashDiff streams full.diff through an inner digest and frames that digest
// into the fingerprint, so a large diff never lands in memory whole and no
// content can impersonate a following field. An absent diff contributes
// nothing; an unreadable one is an error.
func hashDiff(dst hash.Hash, path string) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return jq.Sealed("serve: open full.diff", err)
	}
	defer func() { _ = file.Close() }()
	content := sha256.New()
	if _, err := io.Copy(content, file); err != nil {
		return jq.Sealed("serve: read full.diff", err)
	}
	hashField(dst, []byte("full.diff"))
	hashField(dst, content.Sum(nil))
	return nil
}

// previewDigest hashes the canonical preview wire plus the bundle identity
// and fingerprint, so the digest binds exactly what the operator confirmed.
// Every field is length-framed: root "ab" + bundle "c" must not hash like
// root "a" + bundle "bc".
func previewDigest(root, bundle, fingerprint string, wire []byte) string {
	sum := sha256.New()
	hashField(sum, []byte(root))
	hashField(sum, []byte(bundle))
	hashField(sum, []byte(fingerprint))
	hashField(sum, wire)
	return hex.EncodeToString(sum.Sum(nil))
}

// postSubmitBody is the POST /v1/post/submit wire shape. Confirm is a
// pointer so a missing field reads differently from an explicit false:
// only a boolean true can spend the nonce.
type postSubmitBody struct {
	Confirm       *bool  `json:"confirm"`
	Root          string `json:"root"`
	Bundle        string `json:"bundle"`
	PreviewDigest string `json:"preview_digest"`
	PreviewNonce  string `json:"preview_nonce"`
}

// handlePostSubmit is the Tier C confirmation-gated host submit. Without
// --allow-post it refuses 403 before any host client is touched; with it,
// the guardWrite wrapper has already enforced session, CSRF, Origin, and
// JSON, and this handler enforces confirmation, single-use preview, live
// bundle staleness, and then delegates to core/post.
func (s *Server) handlePostSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AllowPost {
		s.problem(w, http.StatusForbidden, "post-disabled",
			"host posting is disabled; restart with --interactive --allow-post to enable it")
		return
	}
	var body postSubmitBody
	if !decodeJSON(w, r, s, &body) || !s.validateSubmitBody(w, &body) {
		return
	}
	rootPath, bundleDir, ok := s.resolveWrite(w, body.Root, body.Bundle)
	if !ok {
		return
	}
	entry, ok := s.checkPreview(w, &body)
	if !ok {
		return
	}
	// A host write already under way is not abandoned because the tab closed:
	// the nonce is spent under the lock below, and the CLI post this mirrors is not
	// cancellable from a browser either. It derives from the server's
	// lifetime instead of the request's, so shutdown still cancels it (and
	// the exec layer kills the host process tree, GO-CLI-05), and it is
	// bounded, so a stalled host cannot hold the bundle lock forever.
	ctx, cancel := context.WithTimeout(s.lifeCtx, s.postTimeout())
	defer cancel()
	s.runConfirmedPost(ctx, w, &body, rootPath, bundleDir, entry) //nolint:contextcheck // bound to the server lifetime on purpose
}

// validateSubmitBody checks confirmation and preview presence; the problem
// is already written when false.
func (s *Server) validateSubmitBody(w http.ResponseWriter, body *postSubmitBody) bool {
	if body.Confirm == nil || !*body.Confirm {
		s.problem(w, http.StatusBadRequest, "needs-confirm",
			"host posting requires confirm: true after the preview")
		return false
	}
	if body.PreviewDigest == "" || body.PreviewNonce == "" {
		s.problem(w, http.StatusBadRequest, reasonStalePreview,
			"host posting requires a fresh preview_digest and preview_nonce")
		return false
	}
	return true
}

// checkPreview validates the nonce without spending it; the problem is
// already written when false. The nonce is spent later, under the bundle lock
// ([Server.spendPreview]), so a busy store or a lock timeout stays retryable.
func (s *Server) checkPreview(w http.ResponseWriter, body *postSubmitBody) (*previewEntry, bool) {
	if s.previews == nil {
		s.problem(w, http.StatusConflict, reasonStalePreview, "no preview is stored for this session")
		return nil, false
	}
	entry, ok := s.previews.peek(body.PreviewNonce, body.Root, body.Bundle, body.PreviewDigest)
	if !ok {
		s.problem(w, http.StatusConflict, reasonStalePreview,
			"preview is unknown, mismatched, expired, or already used; re-run the preview")
		return nil, false
	}
	return entry, true
}

// spendPreview atomically consumes the validated nonce; the problem is
// already written when false (a concurrent submit already spent it).
func (s *Server) spendPreview(w http.ResponseWriter, body *postSubmitBody) bool {
	if _, ok := s.previews.consume(body.PreviewNonce, body.Root, body.Bundle, body.PreviewDigest); !ok {
		s.problem(w, http.StatusConflict, reasonStalePreview,
			"preview is unknown, mismatched, expired, or already used; re-run the preview")
		return false
	}
	return true
}

// runConfirmedPost locks the bundle, re-checks staleness, spends the nonce,
// and delegates to core/post under the lock so a concurrent write cannot
// interleave. A failure before the spend (a busy or timed-out lock wait)
// leaves the nonce usable; a stale bundle spends it, because that preview is
// invalid anyway.
func (s *Server) runConfirmedPost(
	ctx context.Context, w http.ResponseWriter, body *postSubmitBody,
	rootPath, bundleDir string, entry *previewEntry,
) {
	storePath := bundleDir + "/findings.json"
	unlock, refusal := s.acquireBundleLock(ctx, storePath)
	if refusal != nil {
		if refusal.typed != nil {
			s.writeRefusal(w, verbServe, body.Bundle, refusal.typed)
			return
		}
		s.problem(w, refusal.status, refusal.reason, refusal.message)
		return
	}
	defer unlock()
	current, err := bundleFingerprint(bundleDir)
	if err != nil {
		s.problem(w, http.StatusInternalServerError, "internal",
			"could not re-read the bundle before posting: "+err.Error())
		return
	}
	if current != entry.fingerprint {
		_, _ = s.previews.consume(body.PreviewNonce, body.Root, body.Bundle, body.PreviewDigest)
		s.problem(w, http.StatusConflict, reasonStalePreview,
			"the bundle changed since the preview; re-run the preview before submitting")
		return
	}
	if !s.spendPreview(w, body) {
		return
	}
	resp, ref := s.invokePostSubmit(ctx, body, rootPath, bundleDir)
	if ref != nil {
		s.writeRefusal(w, verbServe, body.Bundle, ref)
		return
	}
	s.layer.Invalidate()
	s.writeEnvelope(w, core.RenderEnvelope(verbServe, "", body.Bundle, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil))
}

// postTimeout is the configured submit bound, [postSubmitTimeout] by default.
func (s *Server) postTimeout() time.Duration {
	if s.cfg.PostTimeout > 0 {
		return s.cfg.PostTimeout
	}
	return postSubmitTimeout
}

// postReadRequest builds the post read request a preview and a confirmed
// submit share, with the install root `verdict post` passes, so the tool
// version in the footer and payload resolves from the install and never from
// the server's working directory.
func (s *Server) postReadRequest(bundleDir, bundle, repoRoot string) core.ReadRequest {
	installRoot := s.cfg.InstallRoot
	return core.ReadRequest{
		Runtime:  s.runtime(),
		Repo:     readcommon.BundleProject(bundleDir),
		Pr:       bundle,
		Root:     &installRoot,
		RepoRoot: repoRoot,
	}
}

// lockRefusal is the transport problem a failed bundle-lock acquire answers.
type lockRefusal struct {
	// typed is set when the submit's own deadline or a shutdown ended the
	// wait: the same typed refusal the host-call phase answers, at 200.
	typed   *core.RefusalError
	reason  string
	message string
	status  int
}

// acquireBundleLock takes the bundle findings-store lock for one submit. It
// classifies the failure where the error is still inspectable: the busy
// sentinel ([storelock.ErrBusy], held by a verify job or a triage) must reach
// [errors.Is] unsealed, so a collision answers 503 store-busy, not 500.
func (s *Server) acquireBundleLock(ctx context.Context, storePath string) (func(), *lockRefusal) {
	lockWait := s.cfg.LockWait
	if lockWait <= 0 {
		lockWait = storelock.DefaultWait
	}
	unlock, err := storelock.AcquireContext(ctx, storePath, lockWait)
	if err == nil {
		return unlock, nil
	}
	if typed := s.lockWaitRefusal(ctx); typed != nil {
		return nil, &lockRefusal{typed: typed}
	}
	if errors.Is(err, storelock.ErrBusy) {
		return nil, &lockRefusal{
			status: http.StatusServiceUnavailable, reason: "store-busy",
			message: "another write holds the store lock on " + storePath + " (retry in a moment)",
		}
	}
	return nil, &lockRefusal{
		status: http.StatusInternalServerError, reason: "internal",
		message: fmt.Sprintf("could not take the store lock on %s: %v", storePath, err),
	}
}

// lockWaitRefusal maps a submit context that ended while waiting for the
// bundle lock to the typed refusals the host-call phase uses. Nothing was
// posted and the nonce is unspent, so the message says the retry is safe.
func (s *Server) lockWaitRefusal(ctx context.Context) *core.RefusalError {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &core.RefusalError{Code: codePostTimeout, Message: fmt.Sprintf(
			"the submit did not finish within %s while waiting for the bundle lock; nothing was posted and "+
				"the preview is still valid, so retry", s.postTimeout())}
	case errors.Is(ctx.Err(), context.Canceled):
		return &core.RefusalError{Code: codeCanceled, Message: "the server shut down while the submit waited " +
			"for the bundle lock; nothing was posted"}
	}
	return nil
}

// invokePostSubmit builds the confirmed post request and runs it. The request
// carries the configured environment and render configuration, so the host
// gate authenticates and the body renders exactly as the CLI's would.
func (s *Server) invokePostSubmit(
	ctx context.Context, body *postSubmitBody, rootPath, bundleDir string,
) (*postcore.Response, *core.RefusalError) {
	runner := s.runnerPostSubmit()
	read := s.postReadRequest(bundleDir, body.Bundle, rootPath)
	req := &postcore.Request{
		ReadRequest: read,
		Flags:       postcore.Flags{Approve: approveFalse, ReviewFile: bundleDir + "/review.json", DryRun: false},
		Effective:   s.cfg.Effective,
	}
	resp, ref := runner(ctx, req)
	if ref != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && contextOwned(ref) {
		// The bound, not the host's own failure, ended the call.
		ref = &core.RefusalError{Code: codePostTimeout, Message: fmt.Sprintf(
			"the host post did not finish within %s and was stopped; the host may or may not have recorded the review",
			s.postTimeout()), Warnings: ref.Warnings}
	}
	if ref != nil && errors.Is(ctx.Err(), context.Canceled) && contextOwned(ref) {
		// Shutdown, not the host, ended the call.
		ref = &core.RefusalError{Code: codeCanceled, Message: "the server shut down while the host post was in flight " +
			"and the host call was stopped; the host may or may not have recorded the review", Warnings: ref.Warnings}
	}
	return resp, ref
}

// contextOwned reports whether a refusal is the shape a call killed by its own
// deadline takes: the cancellation code, or the transport failure a stopped
// host command surfaces. A definitive host or gate refusal (already-posted,
// a stale record, an auth refusal) that lands at the boundary keeps its code.
//
// Known limit: core/post gives a post-failed refusal no typed cause. Its
// message is the head of the host's stderr, or the exit text, so a command
// the context killed cannot be told from a genuine host failure that
// happened to land after the context ended. Matching message text would
// guess, so the code only treats post-failed as context-caused once the
// context is already done; a genuine failure in that window reads as
// post-timeout or canceled, whose text already says the host may or may not
// have recorded the review.
func contextOwned(ref *core.RefusalError) bool {
	return ref.Code == codeCanceled || ref.Code == postcore.CodePostFailed
}

// runnerPostSubmit returns the injected submit runner.
func (s *Server) runnerPostSubmit() PostSubmitRunner {
	if s.postSubmitRunner != nil {
		return s.postSubmitRunner
	}
	return prodPostSubmitRunner
}

// prodPostSubmitRunner runs the real post service as a confirmed host submit.
// It is the only path that can carry DryRun false, and it is reachable only
// through the Tier C route behind --allow-post.
func prodPostSubmitRunner(
	ctx context.Context, req *postcore.Request,
) (*postcore.Response, *core.RefusalError) {
	req.Flags.DryRun = false
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
