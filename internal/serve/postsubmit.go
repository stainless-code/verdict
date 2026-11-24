package serve

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/storelock"
)

// Tier C host posting: an explicit --allow-post opt-in beyond --interactive.
// A fresh post-preview mints a single-use nonce bound to a preview digest;
// the submit route consumes it under the bundle lock before delegating to
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
	// previewNonceLen is the hex nonce length the fallback token slices.
	previewNonceLen = 32
	// approveFalse is the post approval default the submit sends.
	approveFalse = "false"
	// reasonStalePreview is the transport reason for a spent preview.
	reasonStalePreview = "stale-preview"
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
// nonce atomically.
type previewStore struct {
	entries map[string]*previewEntry
	now     func() time.Time
	ttl     time.Duration
	mu      sync.Mutex
}

// newPreviewStore builds an empty store.
func newPreviewStore() *previewStore {
	return &previewStore{entries: map[string]*previewEntry{}}
}

// mint binds one digest and fingerprint to a fresh nonce and stores it.
func (st *previewStore) mint(root, bundle, digest, fingerprint string) string {
	nonce := mintPreviewNonce()
	now := time.Now()
	if st.now != nil {
		now = st.now()
	}
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
	return nonce
}

// consume validates one nonce against root, bundle, and digest and deletes
// it in the same critical section, so a nonce can never be spent twice even
// when the follow-up staleness check refuses.
func (st *previewStore) consume(nonce, root, bundle, digest string) (*previewEntry, bool) {
	now := time.Now()
	if st.now != nil {
		now = st.now()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	entry, ok := st.entries[nonce]
	if !ok {
		return nil, false
	}
	delete(st.entries, nonce)
	if !entry.expiresAt.After(now) {
		return nil, false
	}
	if entry.root != root || entry.bundle != bundle || entry.digest != digest {
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

// dropOneLocked drops one entry to keep the store bounded when it is full
// of live nonces. The caller holds st.mu.
func (st *previewStore) dropOneLocked() {
	for nonce := range st.entries {
		delete(st.entries, nonce)
		return
	}
}

// mintPreviewNonce mints one CSPRNG nonce as lowercase hex.
func mintPreviewNonce() string {
	var b [previewNonceBytes]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	// Fallback to the capability token shape when the CSPRNG fails.
	return newToken()[:previewNonceLen]
}

// bundleFingerprint hashes the bundle state the preview read: findings,
// the PR or local source, and the review draft. Any triage or re-fetch
// after the preview moves it, and the submit refuses as stale.
func bundleFingerprint(bundleDir string) string {
	sum := sha256.New()
	for _, name := range []string{"findings.json", "PR.json", "source.json", "review.json", "full.diff"} {
		raw, err := os.ReadFile(bundleDir + "/" + name)
		if err != nil {
			continue
		}
		_, _ = sum.Write([]byte(name))
		_, _ = sum.Write(raw)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// previewDigest hashes the canonical preview wire plus the bundle identity
// and fingerprint, so the digest binds exactly what the operator confirmed.
func previewDigest(root, bundle, fingerprint string, wire []byte) string {
	sum := sha256.New()
	_, _ = sum.Write([]byte(root))
	_, _ = sum.Write([]byte(bundle))
	_, _ = sum.Write([]byte(fingerprint))
	_, _ = sum.Write(wire)
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
	entry, ok := s.consumePreview(w, &body)
	if !ok {
		return
	}
	s.runConfirmedPost(w, r, &body, rootPath, bundleDir, entry)
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

// consumePreview atomically validates and consumes the nonce; the problem
// is already written when false.
func (s *Server) consumePreview(w http.ResponseWriter, body *postSubmitBody) (*previewEntry, bool) {
	if s.previews == nil {
		s.problem(w, http.StatusConflict, reasonStalePreview, "no preview is stored for this session")
		return nil, false
	}
	// Consume the nonce first so a replay, a mismatched digest, or a stale
	// bundle can never be retried with the same nonce.
	if s.previews.now == nil {
		s.previews.now = s.now
	}
	entry, ok := s.previews.consume(body.PreviewNonce, body.Root, body.Bundle, body.PreviewDigest)
	if !ok {
		s.problem(w, http.StatusConflict, reasonStalePreview,
			"preview is unknown, mismatched, expired, or already used; re-run the preview")
		return nil, false
	}
	return entry, true
}

// runConfirmedPost locks the bundle, re-checks staleness, and delegates to
// core/post under the lock so a concurrent write cannot interleave.
func (s *Server) runConfirmedPost(
	w http.ResponseWriter, r *http.Request, body *postSubmitBody,
	rootPath, bundleDir string, entry *previewEntry,
) {
	storePath := bundleDir + "/findings.json"
	unlock, lockErr := s.acquireBundleLock(r, storePath)
	if lockErr != nil {
		if errors.Is(lockErr, storelock.ErrBusy) {
			s.problem(w, http.StatusServiceUnavailable, "store-busy",
				"another write holds the store lock on "+storePath+" (retry in a moment)")
			return
		}
		s.problem(w, http.StatusInternalServerError, "internal",
			fmt.Sprintf("could not take the store lock on %s: %v", storePath, lockErr))
		return
	}
	defer unlock()
	if current := bundleFingerprint(bundleDir); current != entry.fingerprint {
		s.problem(w, http.StatusConflict, reasonStalePreview,
			"the bundle changed since the preview; re-run the preview before submitting")
		return
	}
	resp, ref := s.invokePostSubmit(r, body, rootPath, bundleDir)
	if ref != nil {
		s.writeRefusal(w, verbServe, body.Bundle, ref)
		return
	}
	s.layer.Invalidate()
	s.writeEnvelope(w, core.RenderEnvelope(verbServe, "", body.Bundle, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil))
}

// acquireBundleLock takes the bundle findings-store lock for one submit.
func (s *Server) acquireBundleLock(r *http.Request, storePath string) (func(), error) {
	lockWait := s.cfg.LockWait
	if lockWait <= 0 {
		lockWait = storelock.DefaultWait
	}
	unlock, err := storelock.AcquireContext(r.Context(), storePath, lockWait)
	if err != nil {
		return nil, jq.Sealed("serve: acquire bundle lock on "+storePath, err)
	}
	return unlock, nil
}

// invokePostSubmit builds the confirmed post request and runs it.
func (s *Server) invokePostSubmit(
	r *http.Request, body *postSubmitBody, rootPath, bundleDir string,
) (*postcore.Response, *core.RefusalError) {
	runner := s.runnerPostSubmit()
	read := core.ReadRequest{
		Runtime:  &core.Runtime{Now: s.now, Env: map[string]string{}},
		Repo:     readcommon.BundleProject(bundleDir),
		Pr:       body.Bundle,
		RepoRoot: rootPath,
	}
	req := &postcore.Request{
		ReadRequest: read,
		Flags:       postcore.Flags{Approve: approveFalse, ReviewFile: bundleDir + "/review.json", DryRun: false},
	}
	return runner(r.Context(), req)
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
		req.Runtime = &core.Runtime{Env: map[string]string{}}
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	if req.Commander == nil {
		req.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	}
	return postcore.Run(ctx, req)
}
