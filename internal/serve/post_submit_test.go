package serve

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/testenv"
)

// postSubmitServer binds an allow-post server with counting runners.
func postSubmitServer(t *testing.T, layer Layer, allowPost bool) (*httptest.Server, *Server, *submitCounts) { //nolint:revive // counting runners branch inline with the server setup.
	t.Helper()
	counts := &submitCounts{}
	preview := PostPreviewRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		counts.mu.Lock()
		counts.preview++
		counts.mu.Unlock()
		if !req.Flags.DryRun {
			return nil, &core.RefusalError{Code: "test-assert", Message: "DryRun must be true"}
		}
		if err := ctx.Err(); err != nil {
			return nil, &core.RefusalError{Code: "canceled", Message: "canceled before post completed"}
		}
		data := &core.PostData{DryRun: true, Event: "COMMENT", CommitID: headA}
		return &postcore.Response{
			Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z", Data: data,
			Warnings: []string{}, Next: "payload NOT posted (dry-run)",
		}, nil
	})
	submit := PostSubmitRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		counts.mu.Lock()
		counts.submit++
		counts.mu.Unlock()
		if req.Flags.DryRun {
			return nil, &core.RefusalError{Code: "test-assert", Message: "submit must not be dry-run"}
		}
		if err := ctx.Err(); err != nil {
			return nil, &core.RefusalError{Code: "canceled", Message: "canceled before post completed"}
		}
		data := &core.PostData{DryRun: false, Event: "COMMENT", CommitID: headA}
		return &postcore.Response{
			Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z", Data: data,
			Warnings: []string{}, Next: "submitted",
		}, nil
	})
	server, err := New(&Config{
		Port: testPort, Token: testToken, Now: fixedNow, Layer: layer,
		Interactive: true, AllowPost: allowPost,
		VerifyRunner: okVerifyRunner(), PostPreviewRunner: preview, PostSubmitRunner: submit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	t.Cleanup(ts.Close)
	t.Cleanup(func() { server.jobs.stop() })
	return ts, server, counts
}

// submitCounts tallies runner invocations under a mutex.
type submitCounts struct {
	mu      sync.Mutex
	preview int
	submit  int
}

func (c *submitCounts) submits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.submit
}

// mintPreview drives one post-preview job to completion and returns the
// published digest and nonce.
func mintPreview(t *testing.T, ts *httptest.Server, server *Server, rootID, bundle string) (string, string) {
	t.Helper()
	digest, nonce, _ := mintPreviewSnapshot(t, ts, server, rootID, bundle)
	return digest, nonce
}

// mintPreviewSnapshot drives one preview and returns its terminal snapshot
// beside the digest and nonce it published.
func mintPreviewSnapshot(
	t *testing.T, ts *httptest.Server, server *Server, rootID, bundle string,
) (string, string, map[string]any) {
	t.Helper()
	id := createPostPreviewJobOn(t, ts, server, rootID, bundle)
	snap := awaitJobTerminal(t, ts, id)
	if asString(t, snap["status"]) != jobStatusSucceeded {
		t.Fatalf("preview status = %v, want succeeded (%v)", snap["status"], snap)
	}
	result := asMap(t, snap["result"])
	digest, ok := result["preview_digest"].(string)
	if !ok || digest == "" {
		t.Fatalf("result lacks preview_digest: %v", result)
	}
	if len(digest) != 64 {
		t.Errorf("preview_digest len = %d, want 64 hex chars (%s)", len(digest), digest)
	}
	nonce, ok := result["preview_nonce"].(string)
	if !ok || nonce == "" {
		t.Fatalf("result lacks preview_nonce: %v", result)
	}
	return digest, nonce, snap
}

// createPostPreviewJobOn posts one preview on the named bundle.
func createPostPreviewJobOn(t *testing.T, ts *httptest.Server, server *Server, rootID, bundle string) string {
	t.Helper()
	got := post(t, ts, "/v1/jobs/post-preview", `{"root":"`+rootID+`","bundle":"`+bundle+`"}`, writeHeaders(server))
	if got.status != http.StatusAccepted {
		t.Fatalf("POST post-preview = %d, want 202 (%s)", got.status, got.body)
	}
	return asString(t, asMap(t, decode(t, got.body)["data"])["id"])
}

// submitBody renders one submit payload.
func submitBody(root, bundle, digest, nonce string, confirm *bool) string {
	base := `{"root":"` + root + `","bundle":"` + bundle + `","preview_digest":"` + digest + `","preview_nonce":"` + nonce + `"`
	if confirm == nil {
		return base + `}`
	}
	if *confirm {
		return base + `,"confirm":true}`
	}
	return base + `,"confirm":false}`
}

// TestPostSubmitRefusedWithoutInteractive pins 405 with Allow GET when the
// server is read-only, mirroring the Tier A/B placeholder matrix.
func TestPostSubmitRefusedWithoutInteractive(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	got := post(t, ts, postSubmitPath, `{}`, bearer())
	if got.status != http.StatusMethodNotAllowed {
		t.Fatalf("POST submit = %d, want 405 (%s)", got.status, got.body)
	}
	if !strings.Contains(got.header.Get("Allow"), http.MethodGet) {
		t.Errorf("Allow = %q, want GET", got.header.Get("Allow"))
	}
}

// TestPostSubmitDisabledWithoutAllowPost pins 403 post-disabled with zero
// postcore calls when only --interactive is set, and that such a server
// publishes the bare Tier B preview: no digest, no nonce, and no store to fill.
func TestPostSubmitDisabledWithoutAllowPost(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), false)
	rootID := rootIDOf(t, ts, bearer())
	snap := awaitJobTerminal(t, ts, createPostPreviewJobOn(t, ts, server, rootID, "1234"))
	if asString(t, snap["status"]) != jobStatusSucceeded {
		t.Fatalf("preview status = %v, want succeeded (%v)", snap["status"], snap)
	}
	assertBareTierBPreview(t, server, asMap(t, snap["result"]))
	digest, nonce := strings.Repeat("a", 64), strings.Repeat("b", 32)
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusForbidden {
		t.Fatalf("POST submit = %d, want 403 (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"reason":"post-disabled"`) {
		t.Errorf("body = %s, want post-disabled", got.body)
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submit calls = %d, want 0", submits)
	}
}

// assertBareTierBPreview pins the Tier B preview result shape: the dry-run wire
// alone, with no confirmation fields and no store behind a server that cannot
// accept a submit.
func assertBareTierBPreview(t *testing.T, server *Server, result map[string]any) {
	t.Helper()
	got := slices.Sorted(maps.Keys(result))
	want := []string{
		"anchors", "body", "commit_id", "confidence", "dry_run", "event", "findings",
		"items", "payload_hash", "provenance", "security_summary", "suggestions",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Tier B preview keys = %v, want %v", got, want)
	}
	if server.previews != nil {
		t.Error("a server without --allow-post holds a preview store")
	}
}

// TestAllowPostRequiresInteractive pins the startup ladder: --allow-post
// without --interactive is bad-flags both at New and at Run.
func TestAllowPostRequiresInteractive(t *testing.T) {
	t.Parallel()
	if _, err := New(&Config{Port: testPort, Token: testToken, Now: fixedNow, Layer: testLayer(t.TempDir(), nil), AllowPost: true}); err == nil {
		t.Fatal("New with AllowPost but no Interactive succeeded, want refusal")
	} else if !strings.Contains(err.Error(), "--allow-post requires --interactive") {
		t.Errorf("New error = %v, want --allow-post requires --interactive", err)
	}
	root := t.TempDir()
	out := &syncBuffer{}
	runner := &cli.Runner{Cmd: "serve", Out: out, Human: out, Diag: out, JSONMode: true, Now: fixedNow, Effective: cli.Defaults()}
	if code := Run(t.Context(), runner, []string{"--root", root, "--allow-post"}, Request{}); code != cli.ExitRefused {
		t.Fatalf("Run exit = %d, want %d", code, cli.ExitRefused)
	}
	if got := asMap(t, decode(t, []byte(out.String()))["error"])["code"]; got != "bad-flags" {
		t.Errorf("error.code = %v, want bad-flags (out %s)", got, out.String())
	}
}

// TestPostSubmitGuardsRefuseBeforePost pins CSRF, Origin, Host, and method
// failing closed with zero postcore calls.
func TestPostSubmitGuardsRefuseBeforePost(t *testing.T) { //nolint:revive // guard matrix asserts each refusal inline
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	payload := submitBody(rootID, "1234", digest, nonce, new(true))

	good := writeHeaders(server)
	noCSRF := map[string]string{"Authorization": "Bearer " + testToken, "Content-Type": "application/json", "Origin": "http://127.0.0.1:47123"}
	if got := post(t, ts, postSubmitPath, payload, noCSRF); got.status != http.StatusForbidden || !strings.Contains(string(got.body), `"reason":"bad-csrf"`) {
		t.Errorf("no CSRF = %d %s, want 403 bad-csrf", got.status, got.body)
	}
	badOrigin := map[string]string{"Authorization": "Bearer " + testToken, "Content-Type": "application/json", "Origin": "https://evil.test", "X-Verdict-CSRF": server.csrf}
	if got := post(t, ts, postSubmitPath, payload, badOrigin); got.status != http.StatusForbidden || !strings.Contains(string(got.body), `"reason":"bad-origin"`) {
		t.Errorf("bad Origin = %d %s, want 403 bad-origin", got.status, got.body)
	}
	noOrigin := map[string]string{"Authorization": "Bearer " + testToken, "Content-Type": "application/json", "X-Verdict-CSRF": server.csrf}
	if got := post(t, ts, postSubmitPath, payload, noOrigin); got.status != http.StatusForbidden || !strings.Contains(string(got.body), `"reason":"bad-origin"`) {
		t.Errorf("absent Origin = %d %s, want 403 bad-origin", got.status, got.body)
	}
	// Wrong Host via call: hostAllowed refuses before the handler.
	if got := call(t, ts, http.MethodPost, postSubmitPath, "evil.test:1234", good); got.status != http.StatusForbidden || !strings.Contains(string(got.body), `"reason":"bad-host"`) {
		t.Errorf("bad Host = %d %s, want 403 bad-host", got.status, got.body)
	}
	// GET to the submit route in interactive mode is 405 Allow POST.
	if got := call(t, ts, http.MethodGet, postSubmitPath, "", bearer()); got.status != http.StatusMethodNotAllowed || !strings.Contains(got.header.Get("Allow"), http.MethodPost) {
		t.Errorf("GET submit = %d Allow %q, want 405 Allow POST", got.status, got.header.Get("Allow"))
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submits = %d, want 0 after guard refusals", submits)
	}
}

// TestPostSubmitRequiresConfirm pins missing and false confirm refusing
// before the nonce is even spent.
func TestPostSubmitRequiresConfirm(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	for _, tc := range []struct {
		confirm *bool
		name    string
	}{
		{name: "omitted", confirm: nil},
		{name: "false", confirm: new(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digest, nonce := mintPreview(t, ts, server, rootID, "1234")
			got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, tc.confirm), writeHeaders(server))
			if got.status != http.StatusBadRequest || !strings.Contains(string(got.body), `"reason":"needs-confirm"`) {
				t.Errorf("%s = %d %s, want 400 needs-confirm", tc.name, got.status, got.body)
			}
		})
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submits = %d, want 0", submits)
	}
}

// TestPostSubmitStalePreviewCases pins unknown, mismatched, and replayed
// nonces refusing without postcore calls.
func TestPostSubmitStalePreviewCases(t *testing.T) { //nolint:revive // nonce matrix asserts each refusal inline
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")

	// Unknown nonce.
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, strings.Repeat("0", 32), new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("unknown nonce = %d %s, want 409 stale-preview", got.status, got.body)
	}
	// Digest mismatch burns the nonce on attempt.
	got = post(t, ts, postSubmitPath, submitBody(rootID, "1234", strings.Repeat("a", 64), nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("digest mismatch = %d %s, want 409 stale-preview", got.status, got.body)
	}
	// The mismatched attempt consumed the real nonce: replay is now unknown.
	got = post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("replay after consume = %d %s, want 409 stale-preview", got.status, got.body)
	}
	// A fresh preview spent once cannot be replayed.
	digest2, nonce2 := mintPreview(t, ts, server, rootID, "1234")
	got = post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest2, nonce2, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("fresh submit = %d %s, want 200 success", got.status, got.body)
	}
	got = post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest2, nonce2, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict {
		t.Errorf("replayed nonce = %d %s, want 409", got.status, got.body)
	}
	if submits := counts.submits(); submits != 1 {
		t.Errorf("postcore submits = %d, want exactly 1", submits)
	}
}

// TestPostSubmitExpiredNonce pins a nonce older than the TTL refusing.
func TestPostSubmitExpiredNonce(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	now := fixedNow()
	clock := func() time.Time { return now }
	counts := &submitCounts{}
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		counts.mu.Lock()
		counts.preview++
		counts.mu.Unlock()
		data := &core.PostData{DryRun: true, Event: "COMMENT", CommitID: headA}
		return &postcore.Response{Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z", Data: data}, nil
	})
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		counts.mu.Lock()
		counts.submit++
		counts.mu.Unlock()
		data := &core.PostData{DryRun: false, Event: "COMMENT", CommitID: headA}
		return &postcore.Response{Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z", Data: data}, nil
	})
	server, err := New(&Config{
		Port: testPort, Token: testToken, Now: clock, Layer: testLayer(root, nil),
		Interactive: true, AllowPost: true,
		VerifyRunner: okVerifyRunner(), PostPreviewRunner: preview, PostSubmitRunner: submit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(ts.Close)
	t.Cleanup(func() { server.jobs.stop() })
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	now = now.Add(11 * time.Minute)
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("expired nonce = %d %s, want 409 stale-preview", got.status, got.body)
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submits = %d, want 0", submits)
	}
}

// TestPostSubmitStaleBundleRefused pins a findings.json mutation between
// preview and submit refusing stale-preview before postcore.
func TestPostSubmitStaleBundleRefused(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server, counts := postSubmitServer(t, testLayer(root, nil), true)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreview(t, ts, server, rootID, "1234")
	findings := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "findings.json")
	raw, err := os.ReadFile(findings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(findings, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("stale bundle = %d %s, want 409 stale-preview", got.status, got.body)
	}
	if submits := counts.submits(); submits != 0 {
		t.Errorf("postcore submits = %d, want 0 after stale bundle", submits)
	}
}

// TestHealthAdvertisesAllowPost pins health carrying allow_post only with
// the flag on.
func TestHealthAdvertisesAllowPost(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	layer := testLayer(root, nil)
	ts, _, _ := postSubmitServer(t, layer, true)
	got := call(t, ts, http.MethodGet, "/v1/health", "", bearer())
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"allow_post": true`) {
		t.Errorf("allow-post health = %s, want allow_post true", got.body)
	}
	ts2, _, _ := postSubmitServer(t, layer, false)
	got2 := call(t, ts2, http.MethodGet, "/v1/health", "", bearer())
	if got2.status != http.StatusOK || !strings.Contains(string(got2.body), `"allow_post": false`) {
		t.Errorf("interactive health = %s, want allow_post false", got2.body)
	}
}

// postReviewFixture is the one-comment review the real postcore happy path
// posts.
const postReviewFixture = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
	`"rigor":"pointed","severity":"informational"}],` +
	`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`

// postHeadA is the fetched head the stubbed live-SHA read returns.
const postHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubPostCommander is a hermetic host commander: canned live-SHA,
// authorship, recheck, and reviews-POST answers, with every call counted.
// After the first POST it replays that review and its anchored comments as
// live, so the second submit's duplicate guard sees what the real host would.
type stubPostCommander struct { //nolint:govet // host stub grouping reads as the canned surface
	t        *testing.T
	mu       sync.Mutex
	calls    [][]string
	bodies   []string
	comments []map[string]any
	head     string
	posted   bool
	// stall, when non-nil, makes the reviews POST announce itself on the
	// channel and then block until its context ends, like a wedged gh/curl.
	stall chan struct{}
}

func (s *stubPostCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) { //nolint:gocognit,revive,cyclop // argv switch is the host seam's contract
	s.t.Helper()
	s.mu.Lock()
	s.calls = append(s.calls, append([]string{}, cmd.Argv...))
	joined := strings.Join(cmd.Argv, " ")
	s.mu.Unlock()
	switch {
	case strings.Contains(joined, ".head.sha"):
		return core.Result{Stdout: s.head + "\n"}, nil
	case strings.Contains(joined, "api user") || strings.Contains(joined, "api\x00user"):
		return core.Result{Stdout: "someone-else\n"}, nil
	case strings.Contains(joined, "reviewThreads"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	case strings.Contains(joined, "--method POST") && strings.Contains(joined, "reviews"):
		if s.stall != nil {
			close(s.stall)
			<-ctx.Done()
			return core.Result{}, ctx.Err()
		}
		var raw []byte
		if cmd.Stdin != nil {
			var err error
			if raw, err = io.ReadAll(cmd.Stdin); err != nil {
				return core.Result{}, err
			}
		}
		if len(raw) > 0 {
			var payload struct {
				Body     string           `json:"body"`
				Comments []map[string]any `json:"comments"`
			}
			if err := json.Unmarshal(raw, &payload); err == nil {
				s.mu.Lock()
				if payload.Body != "" {
					s.bodies = append(s.bodies, payload.Body)
				}
				s.comments = append(s.comments, payload.Comments...)
				s.mu.Unlock()
			}
		}
		s.mu.Lock()
		s.posted = true
		s.mu.Unlock()
		return core.Result{Stdout: `{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}`}, nil
	case strings.Contains(joined, "pulls/7/reviews"):
		s.mu.Lock()
		bodies := append([]string{}, s.bodies...)
		s.mu.Unlock()
		if len(bodies) == 0 {
			return core.Result{Stdout: "[]"}, nil
		}
		items := make([]map[string]any, 0, len(bodies))
		for _, body := range bodies {
			items = append(items, map[string]any{
				"id": 101, "state": "commented", "body": body,
				"commit_id": s.head, "submitted_at": "2026-01-01T00:00:00Z",
			})
		}
		raw, err := json.Marshal(items)
		if err != nil {
			return core.Result{}, err
		}
		return core.Result{Stdout: string(raw)}, nil
	case strings.Contains(joined, "pulls/7/comments"):
		s.mu.Lock()
		stored := append([]map[string]any{}, s.comments...)
		s.mu.Unlock()
		if len(stored) == 0 {
			return core.Result{Stdout: "[]"}, nil
		}
		items := make([]map[string]any, 0, len(stored))
		for _, comment := range stored {
			replay := map[string]any{}
			maps.Copy(replay, comment)
			replay["commit_id"] = s.head
			items = append(items, replay)
		}
		raw, err := json.Marshal(items)
		if err != nil {
			return core.Result{}, err
		}
		return core.Result{Stdout: string(raw)}, nil
	default:
		return core.Result{Stdout: "[]"}, nil
	}
}

func (s *stubPostCommander) LookPath(string) error { return nil }

// postedBodies returns the review bodies the stubbed host received.
func (s *stubPostCommander) postedBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.bodies...)
}

func (s *stubPostCommander) postedFlag() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posted
}

// realPostRunners wires the production postcore.Run against the stubbed
// commander for both preview (dry-run) and submit.
func realPostRunners(cmdr *stubPostCommander) (PostPreviewRunner, PostSubmitRunner) { //nolint:revive // runner wiring branches inline
	preview := PostPreviewRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		req.Flags.DryRun = true
		if req.Flags.Approve == "" {
			req.Flags.Approve = approveFalse
		}
		if req.Runtime == nil {
			req.Runtime = &core.Runtime{Env: map[string]string{}}
		}
		req.Commander = cmdr
		if req.Env == nil {
			req.Env = map[string]string{}
		}
		return postcore.Run(ctx, req)
	})
	submit := PostSubmitRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		req.Flags.DryRun = false
		if req.Flags.Approve == "" {
			req.Flags.Approve = approveFalse
		}
		if req.Runtime == nil {
			req.Runtime = &core.Runtime{Env: map[string]string{}}
		}
		req.Commander = cmdr
		if req.Env == nil {
			req.Env = map[string]string{}
		}
		return postcore.Run(ctx, req)
	})
	return preview, submit
}

// seedPostBundle writes a minimal GitHub bundle for bundle 7 and snapshots
// its controls so the real postcore gates pass.
func seedPostBundle(t *testing.T, root string) {
	t.Helper()
	const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := filepath.Join(root, ".verdict", "state", "7")
	files := map[string]string{
		"PR.json": `{"state":"OPEN","headRefOid":"` + postHeadA + `","baseRefOid":"` + base + `",` +
			`"author":{"login":"someone-else"},"url":"https://github.com/acme/app/pull/7"}`,
		"full.diff": "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n",
		"review.json": postReviewFixture,
	}
	for name, content := range files {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(testenv.MaybeFindingsReview(name, content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: dir, BaseSHA: base, AllowHead: true})
	if ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA: base, Trusted: true, CapturedAt: "2026-11-02T12:00:00Z",
		Evidence:  config.Evidence{SchemaVersion: "1", BaseSHA: base, Plan: config.Plan{Status: config.PendingStatus}},
		Resolved:  &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Inventory: config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("seed run state: %v", serr)
	}
}

// TestPostSubmitRealPostcoreParity proves preview → confirm → submit writes
// the exact CLI post receipts through the real postcore, and a fresh second
// submit hits postcore's duplicate-post refusal without a second host POST.
func TestPostSubmitRealPostcoreParity(t *testing.T) { //nolint:gocognit,revive,cyclop // parity asserts receipts and duplicate inline
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	seedPostBundle(t, root)
	cmdr := &stubPostCommander{t: t, head: postHeadA}
	preview, submit := realPostRunners(cmdr)
	install := installRootAt(t, installVersion)
	server, err := New(&Config{
		Port: testPort, Token: testToken, Now: fixedNow, Layer: testLayer(root, nil),
		Interactive: true, AllowPost: true, InstallRoot: install,
		VerifyRunner: okVerifyRunner(), PostPreviewRunner: preview, PostSubmitRunner: submit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(ts.Close)
	t.Cleanup(func() { server.jobs.stop() })
	rootID := rootIDOf(t, ts, bearer())

	digest, nonce, previewSnap := mintPreviewSnapshot(t, ts, server, rootID, "7")
	previewBody := asMap(t, asMap(t, previewSnap["result"])["body"])
	if previewBody["footer"] == nil {
		t.Errorf("preview body carries no footer: %v; the render configuration never reached the preview", previewBody)
	}
	got := post(t, ts, postSubmitPath, submitBody(rootID, "7", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("submit = %d %s, want 200 success envelope", got.status, got.body)
	}
	data := asMap(t, decode(t, got.body)["data"])
	if data["commit_id"] != postHeadA {
		t.Errorf("commit_id = %v, want %s", data["commit_id"], postHeadA)
	}
	if data["dry_run"] != false {
		t.Errorf("dry_run = %v, want false", data["dry_run"])
	}
	if !cmdr.postedFlag() {
		t.Error("submit did not reach the stubbed host POST")
	}
	// The body the host received is the CLI render: same bytes as a direct
	// core/post run with the CLI's resolved configuration, footer included.
	servedBodies := cmdr.postedBodies()
	if len(servedBodies) != 1 {
		t.Fatalf("served host POSTs = %d, want exactly 1", len(servedBodies))
	}
	if !strings.Contains(servedBodies[0], "<!-- verdict-footer -->") {
		t.Errorf("posted body has no verdict footer (%d bytes): the render configuration was zero",
			len(servedBodies[0]))
	}
	if !strings.Contains(servedBodies[0], "**Verdict "+installVersion+"**") {
		t.Errorf("posted footer does not carry the install version %s:\n%s", installVersion, servedBodies[0])
	}
	if cliBody := cliPostedBody(t, install); cliBody != servedBodies[0] {
		t.Errorf("served post body (%d bytes) differs from the CLI render (%d bytes)\nserved:\n%s\nCLI:\n%s",
			len(servedBodies[0]), len(cliBody), servedBodies[0], cliBody)
	}
	// Receipt parity: the same findings.json post receipt the CLI writes.
	receiptRaw, err := os.ReadFile(filepath.Join(mustAbs(t, root), ".verdict", "state", "7", "post.json"))
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	var receipt map[string]any
	if err := decodeBodyJSON(t, receiptRaw, &receipt); err != nil {
		t.Fatalf("decode post.json: %v", err)
	}
	if receipt["head"] != postHeadA {
		t.Errorf("post.json head = %v, want %s", receipt["head"], postHeadA)
	}
	if receipt["schema_version"] != "1" {
		t.Errorf("post.json schema_version = %v, want 1", receipt["schema_version"])
	}
	findingsRaw, err := os.ReadFile(filepath.Join(mustAbs(t, root), ".verdict", "state", "7", "findings.json"))
	if err != nil {
		t.Fatalf("read findings.json: %v", err)
	}
	var findings []map[string]any
	if err := decodeBodyJSON(t, findingsRaw, &findings); err != nil {
		t.Fatalf("decode findings.json: %v", err)
	}
	if len(findings) != 1 || findings[0]["path"] != "notes.txt" || findings[0]["status"] != "open" {
		t.Errorf("findings = %v, want one open notes.txt entry", findings)
	}
	// A fresh preview + submit of the same payload hits core/post's
	// duplicate-post refusal without a second host POST.
	digest2, nonce2 := mintPreview(t, ts, server, rootID, "7")
	got = post(t, ts, postSubmitPath, submitBody(rootID, "7", digest2, nonce2, new(true)), writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("second submit = %d, want 200 envelope refusal (%s)", got.status, got.body)
	}
	env := decode(t, got.body)
	if env["error"] == nil {
		t.Fatalf("second submit data = %v, want typed refusal", env["data"])
	}
	code := asMap(t, env["error"])["code"]
	if code != "already-posted" && code != "duplicate-payload" {
		t.Errorf("second submit code = %v, want already-posted or duplicate-payload", code)
	}
	// No second host POST fired: still exactly one POST argv.
	posts := 0
	cmdr.mu.Lock()
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, " "), "--method POST") {
			posts++
		}
	}
	cmdr.mu.Unlock()
	if posts != 1 {
		t.Errorf("host POST calls = %d, want exactly 1", posts)
	}
}

// decodeBodyJSON unmarshals one byte slice for receipt assertions.
func decodeBodyJSON(t *testing.T, raw []byte, out any) error {
	t.Helper()
	return json.Unmarshal(raw, out)
}

// cliPostedBody renders the same seeded bundle the way the CLI does — a
// direct core/post run carrying the resolved render configuration — and
// returns the body it posts. It is the reference the served post must match
// byte for byte.
func cliPostedBody(t *testing.T, installRoot string) string {
	t.Helper()
	root := mustAbs(t, writeFixture(t, fixtureFiles()))
	seedPostBundle(t, root)
	cmdr := &stubPostCommander{t: t, head: postHeadA}
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Now: fixedNow, Env: map[string]string{},
			Commander: cmdr,
		},
		Repo:     "acme/app",
		Pr:       "7",
		Root:     &installRoot,
		RepoRoot: root,
	}
	req := &postcore.Request{
		ReadRequest: read,
		Flags: postcore.Flags{
			Approve:    approveFalse,
			ReviewFile: filepath.Join(root, ".verdict", "state", "7", "review.json"),
			DryRun:     false,
		},
		Effective: cli.Defaults(),
	}
	if _, ref := postcore.Run(t.Context(), req); ref != nil {
		t.Fatalf("CLI post: %s: %s", ref.Code, ref.Message)
	}
	bodies := cmdr.postedBodies()
	if len(bodies) != 1 {
		t.Fatalf("CLI post sent %d bodies, want exactly 1", len(bodies))
	}
	return bodies[0]
}

// installVersion is the version the install-root fixture declares.
const installVersion = "4.5.6"

// decoyVersion is what a package.json in the process working directory
// declares; a served post must never stamp it.
const decoyVersion = "9.9.9-decoy"

// installRootAt writes a directory holding a package.json of the given
// version and returns it: the stand-in for the verdict install root.
func installRootAt(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x","version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// realPostServer binds an allow-post server over one seeded bundle 7 with the
// real postcore wired to cmdr, and returns it with its root id.
func realPostServer(t *testing.T, cmdr *stubPostCommander, cfg *Config) (ts *httptest.Server, server *Server, rootID, root string) {
	t.Helper()
	root = mustAbs(t, writeFixture(t, fixtureFiles()))
	seedPostBundle(t, root)
	preview, submit := realPostRunners(cmdr)
	cfg.Port, cfg.Token, cfg.Now, cfg.Layer = testPort, testToken, fixedNow, testLayer(root, nil)
	cfg.Interactive, cfg.AllowPost = true, true
	cfg.VerifyRunner, cfg.PostPreviewRunner, cfg.PostSubmitRunner = okVerifyRunner(), preview, submit
	server, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts = httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(ts.Close)
	t.Cleanup(func() { server.jobs.stop() })
	return ts, server, rootIDOf(t, ts, bearer()), root
}

// TestPostSubmitToolVersionIgnoresWorkingDirectory pins that a served post
// resolves the tool version from the install root exactly as the CLI does: a
// server started in a directory holding a foreign package.json must stamp the
// install's version into both the preview and the posted comment, never the
// decoy's. It changes the process working directory, so it cannot run parallel.
func TestPostSubmitToolVersionIgnoresWorkingDirectory(t *testing.T) { //nolint:revive // one flow asserts the preview and the posted body
	decoy := t.TempDir()
	if err := os.WriteFile(filepath.Join(decoy, "package.json"), []byte(`{"version":"`+decoyVersion+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(decoy)
	install := installRootAt(t, installVersion)
	cmdr := &stubPostCommander{t: t, head: postHeadA}
	ts, server, rootID, _ := realPostServer(t, cmdr, &Config{InstallRoot: install})

	digest, nonce, snap := mintPreviewSnapshot(t, ts, server, rootID, "7")
	previewBody, err := json.Marshal(asMap(t, snap["result"])["body"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(previewBody), "Verdict "+installVersion) || strings.Contains(string(previewBody), decoyVersion) {
		t.Errorf("preview body does not stamp install version %s (decoy %s): %s", installVersion, decoyVersion, previewBody)
	}
	got := post(t, ts, postSubmitPath, submitBody(rootID, "7", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("submit = %d %s, want 200 success envelope", got.status, got.body)
	}
	bodies := cmdr.postedBodies()
	if len(bodies) != 1 {
		t.Fatalf("host POSTs = %d, want exactly 1", len(bodies))
	}
	if strings.Contains(bodies[0], decoyVersion) {
		t.Errorf("posted body stamps the cwd package.json version %s:\n%s", decoyVersion, bodies[0])
	}
	if want := cliPostedBody(t, install); bodies[0] != want {
		t.Errorf("served body differs from the CLI render\nserved:\n%s\nCLI:\n%s", bodies[0], want)
	}
}

// TestPostSubmitTimeoutRefusesAndReleasesLock pins that a host call that
// outlives the submit bound is stopped and answered with the typed
// post-timeout refusal, and that the bundle storelock is released so a triage
// on the same bundle succeeds afterwards. The bound is injected: the shipped
// five minutes never decides the test.
func TestPostSubmitTimeoutRefusesAndReleasesLock(t *testing.T) { //nolint:revive // one flow asserts the refusal, the host state, and the lock
	t.Parallel()
	cmdr := &stubPostCommander{t: t, head: postHeadA, stall: make(chan struct{})}
	ts, server, rootID, root := realPostServer(t, cmdr, &Config{PostTimeout: 500 * time.Millisecond})
	digest, nonce, _ := mintPreviewSnapshot(t, ts, server, rootID, "7")

	got := post(t, ts, postSubmitPath, submitBody(rootID, "7", digest, nonce, new(true)), writeHeaders(server))
	select {
	case <-cmdr.stall:
	default:
		t.Fatal("the stalled host POST was never reached: the refusal came from somewhere else")
	}
	if got.status != http.StatusOK {
		t.Fatalf("submit = %d %s, want 200 refusal envelope", got.status, got.body)
	}
	errObj := asMap(t, decode(t, got.body)["error"])
	if errObj["code"] != codePostTimeout {
		t.Fatalf("submit error = %v, want code %s", errObj, codePostTimeout)
	}
	if !strings.Contains(asString(t, errObj["message"]), "try: check the pull request") {
		t.Errorf("post-timeout message carries no hint: %v", errObj["message"])
	}
	if cmdr.postedFlag() {
		t.Error("a timed-out submit must not report the host POST as completed")
	}
	// The lock is released: a triage on the same bundle takes it and writes.
	store := filepath.Join(root, ".verdict", "state", "7", "findings.json")
	if err := os.WriteFile(store, []byte("["+finding(sigCrit, "open", "critical", "walked")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	triage := `{"root":"` + rootID + `","bundle":"7","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	res := post(t, ts, "/v1/triage", triage, writeHeaders(server))
	if res.status != http.StatusOK || !strings.Contains(string(res.body), `"error": null`) {
		t.Fatalf("triage after the timeout = %d %s, want success (the bundle lock leaked)", res.status, res.body)
	}
}

// submitAsync fires one confirmed submit and delivers its answer; a transport
// failure delivers the zero result, which the caller's status check rejects.
func submitAsync(ts *httptest.Server, server *Server, body string) <-chan result {
	answered := make(chan result, 1)
	go func() {
		got, err := submitOnce(ts, server, body)
		if err != nil {
			got = result{}
		}
		answered <- got
	}()
	return answered
}

// submitOnce is the blocking request submitAsync runs off the test goroutine,
// so it reports an error instead of failing the test.
func submitOnce(ts *httptest.Server, server *Server, body string) (result, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+postSubmitPath, strings.NewReader(body))
	if err != nil {
		return result{}, err
	}
	req.Host = testHost
	for name, value := range writeHeaders(server) {
		req.Header.Set(name, value)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		return result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return result{status: resp.StatusCode, body: raw}, err
}

// serveForShutdown binds a real listener for [Server.Serve] on a
// kernel-assigned port, then restores the fixed port the test host header
// names so the httptest handler keeps its allowlist. It returns the Serve
// result channel and the cancel that begins shutdown.
func serveForShutdown(t *testing.T, server *Server) (served <-chan error, shutdown func()) {
	t.Helper()
	server.cfg.Port = 0
	if err := server.Listen(t.Context()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.port = testPort
	server.refreshAllowlists()
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	t.Cleanup(cancelRoot)
	done := make(chan error, 1)
	go func() { done <- server.Serve(rootCtx) }()
	return done, cancelRoot
}

// TestPostSubmitShutdownCancelsInFlightHostPost pins GO-CLI-05 for the served
// submit: canceling the server's root context reaches the host call through
// the submit context, so a post that is mid-flight is stopped promptly and
// answered with the typed canceled refusal that says the host may have
// recorded the review. The submit bound is a minute, so only the shutdown can
// end the call within the test's deadline; the submit derives from the server
// lifetime, never the request, so a closed tab still does not abort it.
func TestPostSubmitShutdownCancelsInFlightHostPost(t *testing.T) { //nolint:revive // one flow asserts the host stop, the answer, and the join
	t.Parallel()
	cmdr := &stubPostCommander{t: t, head: postHeadA, stall: make(chan struct{})}
	ts, server, rootID, _ := realPostServer(t, cmdr, &Config{PostTimeout: time.Minute})
	digest, nonce, _ := mintPreviewSnapshot(t, ts, server, rootID, "7")
	served, shutdown := serveForShutdown(t, server)
	answered := submitAsync(ts, server, submitBody(rootID, "7", digest, nonce, new(true)))

	select {
	case <-cmdr.stall:
	case <-time.After(10 * time.Second):
		t.Fatal("the host POST was never reached")
	}
	shutdown()
	var got result
	select {
	case got = <-answered:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not cancel the in-flight host post: the submit is bound to the request, not the server")
	}
	if got.status != http.StatusOK {
		t.Fatalf("submit = %d %s, want a 200 refusal envelope", got.status, got.body)
	}
	errObj := asMap(t, decode(t, got.body)["error"])
	if errObj["code"] != codeCanceled {
		t.Fatalf("submit error = %v, want code %s", errObj, codeCanceled)
	}
	if !strings.Contains(asString(t, errObj["message"]), "may or may not have recorded the review") {
		t.Errorf("cancel message lacks the may-have-recorded warning: %v", errObj["message"])
	}
	if cmdr.postedFlag() {
		t.Error("a canceled submit must not report the host POST as completed")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v, want a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}
