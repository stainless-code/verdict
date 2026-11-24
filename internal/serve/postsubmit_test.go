package serve

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/forgejo"
	"github.com/stainless-code/verdict/internal/storelock"
)

// postServer starts one allow-post server over cfg with the given post
// runners, returning the test listener and the server.
func postServer(
	t *testing.T, cfg *Config, preview PostPreviewRunner, submit PostSubmitRunner,
) (*httptest.Server, *Server) {
	t.Helper()
	if cfg.Now == nil {
		cfg.Now = fixedNow
	}
	cfg.Port, cfg.Token = testPort, testToken
	cfg.Interactive, cfg.AllowPost = true, true
	cfg.VerifyRunner = okVerifyRunner()
	cfg.PostPreviewRunner = preview
	cfg.PostSubmitRunner = submit
	server, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	t.Cleanup(ts.Close)
	t.Cleanup(func() { server.jobs.stop() })
	return ts, server
}

// dryRunResponse is the minimal succeeded preview every runner here returns.
func dryRunResponse(req *postcore.Request) *postcore.Response {
	return &postcore.Response{
		Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z",
		Data:     &core.PostData{DryRun: true, Event: "COMMENT", CommitID: headA},
		Warnings: []string{}, Next: "payload NOT posted (dry-run)",
	}
}

// submittedResponse is the minimal succeeded non-dry-run submit.
func submittedResponse(req *postcore.Request) *postcore.Response {
	return &postcore.Response{
		Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z",
		Data:     &core.PostData{DryRun: false, Event: "COMMENT", CommitID: headA},
		Warnings: []string{}, Next: "submitted",
	}
}

// TestStartForwardsTheRunnerEnvAndEffective pins the adapter edge: the CLI
// request's environment and the runner's resolved render configuration reach
// the server's start state, which is where every job reads them.
func TestStartForwardsTheRunnerEnvAndEffective(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	env := map[string]string{"FORGEJO_HOST": "https://forgejo.invalid", "FORGEJO_TOKEN": "app-token"}
	effective := cli.Defaults()
	effective.PublishFooter = "false"
	effective.PublishLogoURL = "https://example.invalid/logo.svg"
	out := &syncBuffer{}
	runner := &cli.Runner{Out: out, Human: out, Diag: out, Now: fixedNow, Effective: effective}
	server, code, ok := start(t.Context(), runner, &serveOptions{root: root, layer: testLayer(root, nil)},
		root, Request{Env: env})
	if !ok {
		t.Fatalf("start refused with code %d: %s", code, out.String())
	}
	defer func() { _ = server.Close() }()
	if !maps.Equal(env, server.cfg.Env) {
		t.Errorf("start Env = %v, want the CLI's %v", server.cfg.Env, env)
	}
	if server.cfg.Effective != effective {
		t.Errorf("start Effective = %+v, want the runner's %+v", server.cfg.Effective, effective)
	}
}

// TestServedPostRequestsCarryConfigEnvAndEffective pins the transport's two
// carried fields: a preview and its confirmed post both see the environment
// and the render configuration the server started with, never an empty map.
func TestServedPostRequestsCarryConfigEnvAndEffective(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	env := map[string]string{"FORGEJO_HOST": "https://forgejo.invalid", "FORGEJO_TOKEN": "app-token"}
	effective := core.Effective{
		PublishFooter:  "false",
		PublishLogoURL: "https://example.invalid/logo.svg",
	}
	var mu sync.Mutex
	var seen []*postcore.Request
	capture := func(req *postcore.Request) {
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
	}
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		capture(req)
		return dryRunResponse(req), nil
	})
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		capture(req)
		return submittedResponse(req), nil
	})
	ts, server := postServer(t, &Config{
		Layer: testLayer(root, nil), Env: env, Effective: effective,
	}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("submit = %d %s, want 200", got.status, got.body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("post requests = %d, want one preview and one submit", len(seen))
	}
	for i, req := range seen {
		if !maps.Equal(env, req.Env) {
			t.Errorf("request %d Env = %v, want %v", i, req.Env, env)
		}
		if req.Effective != effective {
			t.Errorf("request %d Effective = %+v, want %+v", i, req.Effective, effective)
		}
	}
}

// TestServedPostDefaultEffectiveIsTheCLIDefault pins the default: a server
// configured with no render knobs posts with the same configuration the CLI
// resolves when nothing overrides it.
func TestServedPostDefaultEffectiveIsTheCLIDefault(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var got core.Effective
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		got = req.Effective
		return dryRunResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview,
		PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return submittedResponse(req), nil
		}))
	rootID := rootIDOf(t, ts, bearer())
	mintPreview(t, ts, server, rootID, "1234")
	if got != core.Defaults() {
		t.Errorf("preview Effective = %+v, want core.Defaults() %+v", got, core.Defaults())
	}
}

// forgejoGateRunners wires a preview that refuses through the gate
// internal/core/post actually calls for Forgejo, plus a succeeding submit.
func forgejoGateRunners() (PostPreviewRunner, PostSubmitRunner) {
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if ref := forgejo.Auth(req.Env); ref != nil {
			return nil, ref
		}
		return dryRunResponse(req), nil
	})
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		return submittedResponse(req), nil
	})
	return preview, submit
}

// TestForgejoAuthGateReadsConfigEnv pins the consequence of the carried
// environment: the configured Forgejo credentials pass the gate, so the
// preview succeeds with a digest to confirm.
func TestForgejoAuthGateReadsConfigEnv(t *testing.T) {
	t.Parallel()
	preview, submit := forgejoGateRunners()
	root := writeFixture(t, fixtureFiles())
	env := map[string]string{"FORGEJO_HOST": "https://forgejo.invalid", "FORGEJO_TOKEN": "app-token"}
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil), Env: env}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	id := createPostPreviewJobOn(t, ts, server, rootID, "1234")
	snap := awaitJobTerminal(t, ts, id)
	if asString(t, snap["status"]) != jobStatusSucceeded {
		t.Fatalf("preview = %v, want succeeded", snap)
	}
	if _, ok := asMap(t, snap["result"])["preview_digest"]; !ok {
		t.Errorf("succeeded preview has no digest: %v", snap)
	}
}

// TestForgejoAuthGateRefusesAnEmptyEnv pins the other half: with no carried
// environment the same gate refuses auth-missing before any host call, and
// the preview publishes nothing confirmable.
func TestForgejoAuthGateRefusesAnEmptyEnv(t *testing.T) {
	t.Parallel()
	preview, submit := forgejoGateRunners()
	root := writeFixture(t, fixtureFiles())
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil), Env: map[string]string{}}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	id := createPostPreviewJobOn(t, ts, server, rootID, "1234")
	snap := awaitJobTerminal(t, ts, id)
	if asString(t, snap["status"]) != jobStatusFailed {
		t.Fatalf("preview = %v, want failed", snap)
	}
	if code := asString(t, asMap(t, snap["error"])["code"]); code != "auth-missing" {
		t.Errorf("preview error code = %q, want auth-missing (%v)", code, snap)
	}
	if _, ok := snap["result"]; ok {
		t.Errorf("refused preview published a result: %v", snap)
	}
}

// TestPreviewStoreConcurrentConsume pins single use under contention: two
// consumers of one nonce race, and exactly one wins.
func TestPreviewStoreConcurrentConsume(t *testing.T) {
	t.Parallel()
	store := &previewStore{entries: map[string]*previewEntry{}, now: fixedNow, ttl: previewTTL}
	nonce, err := store.mint("root", "1234", "digest", "fingerprint")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var wins atomic.Int32
	for range 2 {
		wg.Go(func() {
			<-start
			if _, ok := store.consume(nonce, "root", "1234", "digest"); ok {
				wins.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("consume wins = %d, want exactly 1", wins.Load())
	}
}

// TestConcurrentPreviewJobsMintOnDifferentBundles pins the store's clock as
// construction state: two preview jobs run at once on different bundles, both
// reach mint together, and neither assigns the clock lazily. Under -race the
// lazy assignment this replaced reports a data race here.
func TestConcurrentPreviewJobsMintOnDifferentBundles(t *testing.T) { //nolint:revive // the rendezvous and its two asserts read as one block
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	release := make(chan struct{})
	var arrived atomic.Int32
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if arrived.Add(1) == 2 {
			close(release)
		}
		select {
		case <-release:
		case <-time.After(15 * time.Second):
			return nil, &core.RefusalError{Code: "test-assert", Message: "the two preview jobs never met"}
		}
		return dryRunResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview,
		PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return submittedResponse(req), nil
		}))
	rootID := rootIDOf(t, ts, bearer())
	ids := make([]string, 0, 2)
	for _, bundle := range []string{"1234", "local-1"} {
		ids = append(ids, createPostPreviewJobOn(t, ts, server, rootID, bundle))
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("job ids = %v, want two distinct previews", ids)
	}
	for _, id := range ids {
		snap := awaitJobTerminal(t, ts, id)
		if asString(t, snap["status"]) != jobStatusSucceeded {
			t.Fatalf("preview %s = %v, want succeeded", id, snap)
		}
		result := asMap(t, snap["result"])
		if result["preview_digest"] == nil || result["preview_nonce"] == nil {
			t.Errorf("preview %s published no digest or nonce: %v", id, result)
		}
	}
}

// TestPreviewDigestFramesFields pins the length framing: fields that move a
// byte across their boundary must not keep the digest.
func TestPreviewDigestFramesFields(t *testing.T) {
	t.Parallel()
	wire := []byte("wire")
	if previewDigest("ab", "c", "fp", wire) == previewDigest("a", "bc", "fp", wire) {
		t.Error("previewDigest collides on root \"ab\"+bundle \"c\" against root \"a\"+bundle \"bc\"")
	}
	if previewDigest("root", "1234", "ab", wire) == previewDigest("root", "1234", "a", []byte("b"+string(wire))) {
		t.Error("previewDigest collides on a fingerprint moved into the wire")
	}
}

// TestBundleFingerprintFramesFields pins the same framing on the bundle
// fingerprint: without it, one file's trailing bytes can impersonate the next
// file's name and bytes.
func TestBundleFingerprintFramesFields(t *testing.T) {
	t.Parallel()
	first := writeFixture(t, map[string]string{
		".verdict/state/7/findings.json": "x",
		".verdict/state/7/review.json":   "review.jsonx",
	})
	second := writeFixture(t, map[string]string{
		".verdict/state/7/findings.json": "xreview.json",
		".verdict/state/7/review.json":   "x",
	})
	dirA := filepath.Join(first, ".verdict", "state", "7")
	dirB := filepath.Join(second, ".verdict", "state", "7")
	gotA, err := bundleFingerprint(dirA)
	if err != nil {
		t.Fatalf("fingerprint %s: %v", dirA, err)
	}
	gotB, err := bundleFingerprint(dirB)
	if err != nil {
		t.Fatalf("fingerprint %s: %v", dirB, err)
	}
	if gotA == gotB {
		t.Error("bundleFingerprint collides when content shifts across file boundaries")
	}
}

// TestBundleFingerprintRefusesUnreadableDiff pins the read as a refusal: a
// full.diff that exists but cannot be read fails the fingerprint instead of
// vanishing from the hash input, and the preview job refuses with it.
func TestBundleFingerprintRefusesUnreadableDiff(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	dir := filepath.Join(root, ".verdict", "state", "1234")
	if err := os.Mkdir(filepath.Join(dir, "full.diff"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := bundleFingerprint(dir)
	if err == nil {
		t.Fatalf("bundleFingerprint = %q, nil; want an unreadable-diff error", got)
	}
	if !strings.Contains(err.Error(), "full.diff") {
		t.Errorf("error = %v, want it to name full.diff", err)
	}
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)},
		PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return dryRunResponse(req), nil
		}),
		PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return submittedResponse(req), nil
		}))
	rootID := rootIDOf(t, ts, bearer())
	id := createPostPreviewJobOn(t, ts, server, rootID, "1234")
	snap := awaitJobTerminal(t, ts, id)
	if asString(t, snap["status"]) != jobStatusFailed {
		t.Fatalf("preview = %v, want failed", snap)
	}
	if code := asString(t, asMap(t, snap["error"])["code"]); code != codeBundleUnreadable {
		t.Errorf("preview error code = %q, want %q (%v)", code, codeBundleUnreadable, snap)
	}
	if _, ok := snap["result"]; ok {
		t.Errorf("unfingerprintable preview published a result: %v", snap)
	}
}

// TestPreviewRefusedWhenCSPRNFails pins the nonce source: a failing CSPRNG
// refuses the preview, because a weak fallback nonce cannot be a confirmation
// gate.
func TestPreviewRefusedWhenCSPRNFails(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)},
		PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return dryRunResponse(req), nil
		}),
		PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return submittedResponse(req), nil
		}))
	// The server's store is the one the job mints through; pointing its CSPRNG
	// at a failing reader exercises the shipped path without a global.
	server.previews.random = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	rootID := rootIDOf(t, ts, bearer())
	id := createPostPreviewJobOn(t, ts, server, rootID, "1234")
	snap := awaitJobTerminal(t, ts, id)
	if asString(t, snap["status"]) != jobStatusFailed {
		t.Fatalf("preview = %v, want failed", snap)
	}
	if code := asString(t, asMap(t, snap["error"])["code"]); code != codeNonceFailed {
		t.Errorf("preview error code = %q, want %q (%v)", code, codeNonceFailed, snap)
	}
}

// TestPostSubmitSurvivesACanceledRequest pins the confirmed post's context as
// detached: a closed tab cancels the HTTP request, but the host write already
// under way is not abandoned.
func TestPostSubmitSurvivesACanceledRequest(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		return dryRunResponse(req), nil
	})
	submit := PostSubmitRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if err := ctx.Err(); err != nil {
			return nil, &core.RefusalError{Code: "canceled", Message: "submit context was canceled"}
		}
		return submittedResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, postSubmitPath,
		strings.NewReader(submitBody(rootID, "1234", digest, nonce, new(true))))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handlePostSubmit(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("submit = %d %s, want 200 with the tab closed", recorder.Code, recorder.Body)
	}
}

// okPostRunners are the minimal succeeding preview and submit runners.
func okPostRunners() (PostPreviewRunner, PostSubmitRunner) {
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return dryRunResponse(req), nil
		}, func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return submittedResponse(req), nil
		}
}

// TestPostSubmitStoreBusyIs503 pins the lock collision: a submit that finds the
// bundle's findings.json lock held (a verify job holds the same lock for its
// whole run) answers the documented 503 store-busy, never 500 internal. The
// busy sentinel must survive the lock-acquire boundary for [errors.Is].
func TestPostSubmitStoreBusyIs503(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	preview, submit := okPostRunners()
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil), LockWait: 50 * time.Millisecond}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID, "1234")
	store := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "findings.json")
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("hold the bundle lock: %v", err)
	}
	t.Cleanup(unlock)

	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusServiceUnavailable || !strings.Contains(string(got.body), `"reason":"store-busy"`) {
		t.Fatalf("submit under a held lock = %d %s, want 503 store-busy", got.status, got.body)
	}
}

// mintPreviewSnapshotPair mints one preview and returns its digest and nonce.
func mintPreviewSnapshotPair(t *testing.T, ts *httptest.Server, server *Server, rootID, bundle string) (digest, nonce string) {
	t.Helper()
	digest, nonce, _ = mintPreviewSnapshot(t, ts, server, rootID, bundle)
	return digest, nonce
}

// TestPostSubmitDefinitiveRefusalSurvivesTheDeadline pins the post-timeout
// rewrite as narrow: a definitive refusal (already-posted) the runner returns
// right at the boundary keeps its code, while the transport failure and the
// cancellation a killed host call surface as still read post-timeout.
func TestPostSubmitDefinitiveRefusalSurvivesTheDeadline(t *testing.T) { //nolint:revive // one matrix of boundary refusals
	t.Parallel()
	cases := []struct{ name, code, want string }{
		{"already-posted keeps its code", "already-posted", "already-posted"},
		{"post-failed at the deadline is the timeout", postcore.CodePostFailed, codePostTimeout},
		{"canceled at the deadline is the timeout", "canceled", codePostTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := writeFixture(t, fixtureFiles())
			preview, _ := okPostRunners()
			submit := PostSubmitRunner(func(ctx context.Context, _ *postcore.Request) (*postcore.Response, *core.RefusalError) {
				<-ctx.Done()
				return nil, &core.RefusalError{Code: tc.code, Message: "refused at the boundary"}
			})
			ts, server := postServer(t, &Config{Layer: testLayer(root, nil), PostTimeout: 100 * time.Millisecond}, preview, submit)
			rootID := rootIDOf(t, ts, bearer())
			digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID, "1234")
			got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
			if got.status != http.StatusOK {
				t.Fatalf("submit = %d %s, want 200 refusal envelope", got.status, got.body)
			}
			if code := asString(t, asMap(t, decode(t, got.body)["error"])["code"]); code != tc.want {
				t.Errorf("error code = %q, want %q", code, tc.want)
			}
		})
	}
}

// TestBundleFingerprintCoversEveryPostInput pins the fingerprint to each bundle
// file core/post decides from: changing any one after the preview must move it.
func TestBundleFingerprintCoversEveryPostInput(t *testing.T) { //nolint:revive // one loop asserts each input moves the fingerprint
	t.Parallel()
	want := []string{
		"findings.json", "PR.json", "source.json", "review.json", "post.json",
		"controls.json", "record.json", "identity.json", "provenance.json",
	}
	if !slices.Equal(fingerprintFiles, want) {
		t.Fatalf("fingerprintFiles = %v, want %v", fingerprintFiles, want)
	}
	dir := t.TempDir()
	base, err := bundleFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := bundleFingerprint(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got == base {
			t.Errorf("creating %s left the fingerprint unchanged", name)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPostSubmitPostJSONAfterPreviewIsStale pins the duplicate-post receipt: a
// post.json written between preview and submit (another poster beat this one)
// refuses 409 stale-preview before core/post runs.
func TestPostSubmitPostJSONAfterPreviewIsStale(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	receipt := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "post.json")
	if err := os.WriteFile(receipt, []byte(`{"head":"`+headA+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("post.json after preview = %d %s, want 409 stale-preview", got.status, got.body)
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submits = %d, want 0", submits)
	}
}

// TestPreviewStoreEvictsTheOldestWhenFull pins the bound: a full store of live
// nonces drops the entry nearest expiry, never an arbitrary one.
func TestPreviewStoreEvictsTheOldestWhenFull(t *testing.T) { //nolint:revive // mint loop and the eviction asserts read as one flow
	t.Parallel()
	tick := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	store := &previewStore{
		entries: map[string]*previewEntry{}, ttl: previewTTL,
		now: func() time.Time { tick = tick.Add(time.Second); return tick },
	}
	nonces := make([]string, 0, maxPreviews+1)
	for range maxPreviews + 1 {
		nonce, err := store.mint("root", "1234", "digest", "fingerprint")
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		nonces = append(nonces, nonce)
	}
	if len(store.entries) != maxPreviews {
		t.Fatalf("store holds %d entries, want %d", len(store.entries), maxPreviews)
	}
	if _, ok := store.entries[nonces[0]]; ok {
		t.Error("the oldest nonce survived a full store; an arbitrary entry was evicted")
	}
	for _, nonce := range nonces[1:] {
		if _, ok := store.entries[nonce]; !ok {
			t.Fatalf("a newer nonce %s was evicted instead of the oldest", nonce)
		}
	}
}
