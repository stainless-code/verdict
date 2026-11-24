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
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/forgejo"
	"github.com/stainless-code/verdict/internal/storelock"
	"github.com/stainless-code/verdict/internal/testenv"
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
		Data:     renderedData(),
		Warnings: []string{}, Next: "payload NOT posted (dry-run)",
	}
}

// renderedData is a dry-run payload that carries the one comment and the
// digest every confirmable preview has: a preview without a digest is refused.
func renderedData() *core.PostData {
	return &core.PostData{
		DryRun: true, Event: "COMMENT", CommitID: headA,
		RenderedComments: []core.RenderedComment{{Label: "Review body", Text: "the review"}},
		RenderedHash:     "hash-default",
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
	if len(seen) != 3 {
		t.Fatalf("post requests = %d, want the preview, the submit's re-render, and the submit", len(seen))
	}
	for i, req := range seen {
		requireCarried(t, i, req, env, &effective, i < 2)
	}
}

// requireCarried checks one captured post request: its dry-run flag and the
// environment and render configuration the server started with.
func requireCarried(t *testing.T, i int, req *postcore.Request, env map[string]string, effective *core.Effective, dryRun bool) {
	t.Helper()
	if req.Flags.DryRun != dryRun {
		t.Errorf("request %d DryRun = %v, want %v", i, req.Flags.DryRun, dryRun)
	}
	if !maps.Equal(env, req.Env) {
		t.Errorf("request %d Env = %v, want %v", i, req.Env, env)
	}
	if req.Effective != *effective {
		t.Errorf("request %d Effective = %+v, want %+v", i, req.Effective, *effective)
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
	nonce, err := store.mint("root", "1234", "digest", "fingerprint", "rendered", "o/r")
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
	// A refusal envelope is 200 too: only a null error proves the post ran.
	if !strings.Contains(recorder.Body.String(), `"error": null`) {
		t.Errorf("submit = %s, want a success envelope: the closed tab aborted the post", recorder.Body)
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
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
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
func mintPreviewSnapshotPair(t *testing.T, ts *httptest.Server, server *Server, rootID string) (digest, nonce string) {
	t.Helper()
	digest, nonce, _ = mintPreviewSnapshot(t, ts, server, rootID, "1234")
	return digest, nonce
}

// TestPostSubmitDefinitiveRefusalSurvivesTheDeadline pins the post-timeout
// rewrite as narrow: a definitive refusal (already-posted) the runner returns
// right at the boundary keeps its code, while the transport failure and the
// cancellation a killed host call surface as still read post-timeout. It drives
// the host-call phase directly with a deadline the test fires after the runner
// is entered, so no wall-clock bound races the lock, the fingerprint, or the
// re-render that precede it over HTTP; the HTTP lock-wait deadline is its own
// test.
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
			deadline := newManualDeadline(t.Context())
			entered := make(chan struct{})
			preview, _ := okPostRunners()
			submit := PostSubmitRunner(func(ctx context.Context, _ *postcore.Request) (*postcore.Response, *core.RefusalError) {
				close(entered)
				<-ctx.Done()
				return nil, &core.RefusalError{Code: tc.code, Message: "refused at the boundary"}
			})
			_, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
			bundleDir := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234")
			answered := make(chan *core.RefusalError, 1)
			go func() {
				_, ref := server.invokePostSubmit(deadline, &postSubmitBody{Bundle: "1234"}, mustAbs(t, root), bundleDir,
					&previewEntry{repo: "acme/app", rendered: "digest"})
				answered <- ref
			}()
			awaitClosed(t, entered)
			deadline.fire()
			var ref *core.RefusalError
			testenv.Until(t, func() bool {
				select {
				case ref = <-answered:
					return true
				default:
					return false
				}
			})
			if ref == nil || ref.Code != tc.want {
				t.Errorf("refusal = %+v, want code %q", ref, tc.want)
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
		nonce, err := store.mint("root", "1234", "digest", "fingerprint", "rendered", "o/r")
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

// heldBundleLock holds the findings-store lock of one fixture bundle until the
// returned release runs (or the test ends), standing in for a verify job or a
// triage that has the bundle.
func heldBundleLock(t *testing.T, root, bundle string) (release func()) {
	t.Helper()
	store := filepath.Join(mustAbs(t, root), ".verdict", "state", bundle, "findings.json")
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("hold the bundle lock: %v", err)
	}
	var once sync.Once
	release = func() { once.Do(unlock) }
	t.Cleanup(release)
	return release
}

// TestPostSubmitBusyLockKeepsTheNonce pins the retry the 503 store-busy message
// invites: a submit refused for a held bundle lock has not spent its nonce, so
// the same preview submits successfully once the lock is released and posts
// once.
func TestPostSubmitBusyLockKeepsTheNonce(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	server.cfg.LockWait = 50 * time.Millisecond
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	release := heldBundleLock(t, root, "1234")

	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusServiceUnavailable || !strings.Contains(string(got.body), `"reason":"store-busy"`) {
		t.Fatalf("submit under a held lock = %d %s, want 503 store-busy", got.status, got.body)
	}
	if submits := counts.submits(); submits != 0 {
		t.Fatalf("postcore submits under a held lock = %d, want 0", submits)
	}
	release()
	got = post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("retry with the same nonce = %d %s, want 200 success", got.status, got.body)
	}
	if submits := counts.submits(); submits != 1 {
		t.Errorf("postcore submits = %d, want exactly 1", submits)
	}
	got = post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict {
		t.Errorf("replay after the post = %d %s, want 409", got.status, got.body)
	}
}

// requireNoValidityPromise fails when a lock-wait refusal promises the preview
// is still valid: it may have expired or been spent during the wait.
func requireNoValidityPromise(t *testing.T, message string) {
	t.Helper()
	if strings.Contains(message, "still valid") {
		t.Errorf("message promises the preview is still valid: %q", message)
	}
}

// TestPostSubmitLockWaitEndedByTheContext pins a submit whose deadline or
// shutdown ends the wait for the bundle lock: it answers the same typed
// refusal the host-call phase uses, not 500 internal, posts nothing, and
// leaves the nonce usable.
func TestPostSubmitLockWaitEndedByTheContext(t *testing.T) { //nolint:revive // one matrix of lock-wait endings
	t.Parallel()
	cases := []struct {
		setup func(*Server)
		name  string
		want  string
	}{
		{name: "deadline", want: codePostTimeout, setup: func(*Server) {}},
		{name: "shutdown", want: codeCanceled, setup: func(s *Server) { s.beginStop() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := writeFixture(t, fixtureFiles())
			ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
			server.cfg.PostTimeout = 100 * time.Millisecond
			rootID := rootIDOf(t, ts, bearer())
			digest, nonce := mintPreview(t, ts, server, rootID, "1234")
			release := heldBundleLock(t, root, "1234")
			tc.setup(server)

			got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
			if got.status != http.StatusOK {
				t.Fatalf("submit = %d %s, want 200 typed refusal, not 500", got.status, got.body)
			}
			errObj := asMap(t, decode(t, got.body)["error"])
			if code := asString(t, errObj["code"]); code != tc.want {
				t.Errorf("error code = %q, want %q (%s)", code, tc.want, got.body)
			}
			requireNoValidityPromise(t, asString(t, errObj["message"]))
			if submits := counts.submits(); submits != 0 {
				t.Errorf("postcore submits = %d, want 0", submits)
			}
			if _, ok := server.previews.peek(nonce, rootID, "1234", digest); !ok {
				t.Error("the lock-wait refusal spent the nonce")
			}
			release()
		})
	}
}

// TestPostSubmitConcurrentSameNonceOnePost pins single use across two submits
// racing on one nonce behind a held lock: exactly one posts, the other is
// stale.
func TestPostSubmitConcurrentSameNonceOnePost(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	release := heldBundleLock(t, root, "1234")
	body := submitBody(rootID, "1234", digest, nonce, new(true))
	first, second := submitAsync(t, ts, server, body), submitAsync(t, ts, server, body)
	release()
	var ok, stale int
	for _, got := range []result{awaitResult(t, first), awaitResult(t, second)} {
		switch {
		case got.status == http.StatusOK && strings.Contains(string(got.body), `"error": null`):
			ok++
		case got.status == http.StatusConflict:
			stale++
		default:
			t.Errorf("submit = %d %s, want 200 success or 409 stale-preview", got.status, got.body)
		}
	}
	if ok != 1 || stale != 1 {
		t.Errorf("outcomes = %d success, %d stale, want exactly 1 and 1", ok, stale)
	}
	if submits := counts.submits(); submits != 1 {
		t.Errorf("postcore submits = %d, want exactly 1", submits)
	}
}

// awaitResult waits for one async submit answer within the polling bound, so a
// request that never answers fails the test instead of hanging the suite.
func awaitResult(t *testing.T, answered <-chan result) result {
	t.Helper()
	var got result
	testenv.Until(t, func() bool {
		select {
		case got = <-answered:
			return true
		default:
			return false
		}
	})
	return got
}

// TestPostSubmitStaleBundleSpendsTheNonce pins that a stale fingerprint burns
// the nonce: that preview is invalid, so no retry with it may succeed.
func TestPostSubmitStaleBundleSpendsTheNonce(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	receipt := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "post.json")
	original, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte(`{"head":"`+headA+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	body := submitBody(rootID, "1234", digest, nonce, new(true))
	if got := post(t, ts, postSubmitPath, body, writeHeaders(server)); got.status != http.StatusConflict {
		t.Fatalf("stale submit = %d %s, want 409", got.status, got.body)
	}
	// Restore the previewed bytes: the fingerprint matches again, so only a
	// spent nonce can make the replay refuse.
	if err := os.WriteFile(receipt, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := post(t, ts, postSubmitPath, body, writeHeaders(server)); got.status != http.StatusConflict {
		t.Errorf("replay after a stale refusal = %d %s, want 409: the stale preview kept a live nonce", got.status, got.body)
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submits = %d, want 0", submits)
	}
}

// TestPostSubmitWriteDuringPreviewIsStale pins the fingerprint's moment: it is
// taken before the dry-run reads the bundle, so a write that lands while the
// preview runs is not baked into a "fresh" fingerprint and the submit refuses.
func TestPostSubmitWriteDuringPreviewIsStale(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	receipt := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "post.json")
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if err := os.WriteFile(receipt, []byte(`{"head":"`+headA+`"}`), 0o644); err != nil {
			return nil, &core.RefusalError{Code: "test-assert", Message: err.Error()}
		}
		return dryRunResponse(req), nil
	})
	var submits atomic.Int32
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		submits.Add(1)
		return submittedResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("write during the preview = %d %s, want 409 stale-preview", got.status, got.body)
	}
	if n := submits.Load(); n != 0 {
		t.Errorf("postcore submits = %d, want 0", n)
	}
}

// renderingPreview is a dry-run runner whose rendered review text and hash come
// from render, called once per dry-run (the preview, then the submit's
// re-render): n is the 1-based call number.
func renderingPreview(render func(n int) (body, hash string)) PostPreviewRunner {
	var calls atomic.Int32
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		resp := dryRunResponse(req)
		body, hash := render(int(calls.Add(1)))
		resp.Data.RenderedComments = []core.RenderedComment{{Label: "Review body", Text: body}}
		resp.Data.RenderedHash = hash
		return resp, nil
	}
}

// TestPostSubmitRefusesAReviewThatRendersDifferently pins the binding to the
// rendered review: the bundle fingerprint is a content hash of the current
// bytes, so a review.json swapped while the dry-run read it and restored before
// the submit passes the fingerprint; the submit's own re-render does not match
// the one the operator saw, so it refuses, posts nothing, and spends the nonce.
func TestPostSubmitRefusesAReviewThatRendersDifferently(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var submits atomic.Int32
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		submits.Add(1)
		return submittedResponse(req), nil
	})
	preview := renderingPreview(func(n int) (string, string) {
		if n == 1 {
			return "the text the operator saw", "hash-shown"
		}
		return "a different review", "hash-swapped"
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce, _ := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	body := submitBody(rootID, "1234", digest, nonce, new(true))
	got := post(t, ts, postSubmitPath, body, writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Fatalf("a review that renders differently = %d %s, want 409 stale-preview", got.status, got.body)
	}
	if n := submits.Load(); n != 0 {
		t.Errorf("postcore submits = %d, want 0", n)
	}
	if _, ok := server.previews.peek(nonce, rootID, "1234", digest); ok {
		t.Error("the refused submit left the nonce spendable")
	}
}

// overwrite replaces path's bytes, failing the test on a write error.
func overwrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Error(err)
	}
}

// readText returns path's content, failing the test on a read error.
func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Error(err)
	}
	return string(raw)
}

// swapAfterReRender is a dry-run runner whose rendered hash is review's text
// and which, on its second call (the submit's verified re-render), changes the
// review after reading it: a writer that ignores the advisory lock.
func swapAfterReRender(t *testing.T, review string) PostPreviewRunner {
	t.Helper()
	var calls atomic.Int32
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		resp := dryRunResponse(req)
		resp.Data.RenderedHash = readText(t, review)
		if calls.Add(1) == 2 {
			defer overwrite(t, review, `{"body":"swapped after the check"}`)
		}
		return resp, nil
	}
}

// pinCheckingSubmit models core/post's contract: it digests the review it is
// about to send and refuses a pin it does not match before counting a post.
func pinCheckingSubmit(t *testing.T, review string, posts *atomic.Int32) PostSubmitRunner {
	t.Helper()
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if req.PinnedContentHash != "" && req.PinnedContentHash != readText(t, review) {
			return nil, &core.RefusalError{Code: postcore.CodePostContentChanged, Message: "not the previewed content"}
		}
		posts.Add(1)
		return submittedResponse(req), nil
	}
}

// TestPostSubmitPinsThePostToTheVerifiedContent pins the closing of the last
// window: the re-render matched the preview, then review.json changed (a writer
// that ignores the advisory lock) before the post's own read. The submit runner
// models core/post's contract (digest the review it is about to send, refuse a
// pin it does not match before any host write), so this proves the server pins
// the previewed digest on the real request and answers the refusal as the
// 409 post-content-changed it is, not as a post and not as a stale preview.
func TestPostSubmitPinsThePostToTheVerifiedContent(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	review := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "review.json")
	overwrite(t, review, `{"body":"as previewed"}`)
	var posts atomic.Int32
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)},
		swapAfterReRender(t, review), pinCheckingSubmit(t, review, &posts))
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce, _ := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"post-content-changed"`) {
		t.Fatalf("a review changed after the verified render = %d %s, want 409 post-content-changed", got.status, got.body)
	}
	if strings.Contains(string(got.body), "stale-preview") {
		t.Errorf("the pin's refusal reads as a stale preview: %s", got.body)
	}
	if n := posts.Load(); n != 0 {
		t.Errorf("posted reviews = %d, want 0", n)
	}
	if _, ok := server.previews.peek(nonce, rootID, "1234", digest); ok {
		t.Error("the refused submit left the nonce spendable")
	}
}

// TestPostSubmitPostsTheReviewTheDialogShowed pins the matching path of the
// binding: when the re-render equals the preview's, the submit posts once, and
// the preview wire carries the exact text the confirm dialog renders.
func TestPostSubmitPostsTheReviewTheDialogShowed(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var submits atomic.Int32
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		submits.Add(1)
		return submittedResponse(req), nil
	})
	preview := renderingPreview(func(int) (string, string) { return "## Review\n\n<b>raw</b> text", "hash-stable" })
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce, snap := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	result := asMap(t, snap["result"])
	shown := asList(t, result["review_comments"])
	if len(shown) != 1 {
		t.Fatalf("review_comments = %v, want the one comment", shown)
	}
	if got := asString(t, asMap(t, shown[0])["text"]); got != "## Review\n\n<b>raw</b> text" {
		t.Errorf("review comment text = %q, want the rendered text verbatim", got)
	}
	if len(asList(t, result["review_omitted"])) != 0 {
		t.Errorf("review_omitted = %v, want none", result["review_omitted"])
	}
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("submit = %d %s, want 200 success", got.status, got.body)
	}
	if n := submits.Load(); n != 1 {
		t.Errorf("postcore submits = %d, want 1", n)
	}
}

// TestPreviewTextIsBoundedAtARuneBoundary pins the cap on the published text:
// a comment over [maxReviewPreviewBytes] (256 KiB) is cut at a rune boundary
// (never mid character, so the wire stays valid UTF-8), and its entry still
// carries the full size so the dialog can say it was cut.
func TestPreviewTextIsBoundedAtARuneBoundary(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	// "é" is two bytes: an odd prefix puts the cap in the middle of one.
	long := "x" + strings.Repeat("é", 262144)
	preview := renderingPreview(func(int) (string, string) { return long, "hash" })
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview,
		PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
			return submittedResponse(req), nil
		}))
	rootID := rootIDOf(t, ts, bearer())
	_, _, snap := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	result := asMap(t, snap["result"])
	entry := asMap(t, asList(t, result["review_comments"])[0])
	text := asString(t, entry["text"])
	if len(text) != 262143 {
		t.Errorf("published %d bytes, want 262143 (the 256 KiB bound, backed up to the rune start)", len(text))
	}
	if !utf8.ValidString(text) {
		t.Error("the truncated text is not valid UTF-8")
	}
	if got := asNumber(t, entry["bytes"]); got != len(long) {
		t.Errorf("entry bytes = %d, want the full %d", got, len(long))
	}
}

// TestNewClonesTheCallerEnv pins the snapshot: a credential the caller changes
// after New must not reach the next submit through the shared map.
func TestNewClonesTheCallerEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"GH_TOKEN": "before"}
	server, err := New(&Config{Port: testPort, Token: testToken, Now: fixedNow, Layer: testLayer(t.TempDir(), nil), Env: env})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env["GH_TOKEN"] = "after"
	if got := server.cfg.Env["GH_TOKEN"]; got != "before" {
		t.Errorf("server env GH_TOKEN = %q, want the value at New (%q)", got, "before")
	}
}
