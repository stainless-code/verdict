package serve

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/forgejo"
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
