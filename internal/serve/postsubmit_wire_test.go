package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/testenv"
)

// manualDeadline is a submit context whose deadline the test fires at a moment
// it chooses (after a stub is entered), so no wall-clock bound races the work
// that precedes the step under test. Fired, Err reports
// [context.DeadlineExceeded] exactly as a real expired deadline does; a parent
// that ends first (shutdown) still reports its own error.
type manualDeadline struct {
	context.Context //nolint:containedctx // a test double that is itself the context
	cancel          context.CancelFunc
	fired           atomic.Bool
}

func newManualDeadline(parent context.Context) *manualDeadline {
	inner, cancel := context.WithCancel(parent)
	return &manualDeadline{Context: inner, cancel: cancel}
}

func (m *manualDeadline) Err() error {
	if m.fired.Load() {
		return context.DeadlineExceeded
	}
	return m.Context.Err()
}

// fire ends the context as a deadline: the flag is set before Done closes, so
// whoever wakes on Done reads DeadlineExceeded.
func (m *manualDeadline) fire() {
	m.fired.Store(true)
	m.cancel()
}

// installDeadline makes server's next submit run under a fireable deadline and
// returns it.
func installDeadline(t *testing.T, server *Server) *manualDeadline {
	t.Helper()
	deadline := newManualDeadline(t.Context())
	server.submitDeadline = func(context.Context) (context.Context, context.CancelFunc) {
		return deadline, deadline.cancel
	}
	return deadline
}

// awaitClosed waits for ch to close within the polling bound.
func awaitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	testenv.Until(t, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	})
}

// asNumber asserts one decoded JSON number.
func asNumber(t *testing.T, value any) int {
	t.Helper()
	num, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value %v is not a number", value)
	}
	n, err := num.Int64()
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// commentsPreview is a dry-run runner whose rendered comments are fixed.
func commentsPreview(comments []core.RenderedComment, repos *repoLog) PostPreviewRunner {
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		repos.add(req.Repo)
		resp := dryRunResponse(req)
		resp.Data.RenderedComments = comments
		resp.Data.RenderedHash = "hash-stable"
		return resp, nil
	}
}

// repoLog records the repository each runner call was handed.
type repoLog struct {
	repos []string
	mu    sync.Mutex
}

func (l *repoLog) add(repo string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.repos = append(l.repos, repo)
}

func (l *repoLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.repos...)
}

func okSubmit() PostSubmitRunner {
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		return submittedResponse(req), nil
	}
}

// TestPreviewPublishesEveryCommentThePostSends pins the wire the confirm dialog
// renders: every comment the post sends, in posting order with its label, so
// a review that splits into follow-up comments shows all of what the pinned
// digest covers, not its head alone.
func TestPreviewPublishesEveryCommentThePostSends(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	comments := []core.RenderedComment{
		{Label: "Review body, part 1 of 2", Text: "head text"},
		{Label: "Follow-up review comment, part 2 of 2", Text: "second text"},
		{Label: "Inline comment on a.go:3", Text: "inline text"},
	}
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, commentsPreview(comments, &repoLog{}), okSubmit())
	rootID := rootIDOf(t, ts, bearer())
	_, _, snap := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	result := decode(t, mustJSON(t, snap["result"]))
	shown := asList(t, result["review_comments"])
	if len(shown) != 3 || asNumber(t, result["review_comment_count"]) != 3 {
		t.Fatalf("review_comments = %v (count %v), want all 3", shown, result["review_comment_count"])
	}
	for i, want := range comments {
		entry := asMap(t, shown[i])
		if asString(t, entry["label"]) != want.Label || asString(t, entry["text"]) != want.Text ||
			asNumber(t, entry["bytes"]) != len(want.Text) || asNumber(t, entry["shown_bytes"]) != len(want.Text) {
			t.Errorf("comment %d = %v, want %+v", i+1, entry, want)
		}
	}
	if omitted := asList(t, result["review_omitted"]); len(omitted) != 0 {
		t.Errorf("review_omitted = %v, want none", omitted)
	}
}

// TestPreviewNamesWhatTheBoundLeavesOut pins the truncation disclosure: the
// bound is a total over every comment, the comment it lands in is cut with its
// full size kept, and every comment after it is named in review_omitted, so the
// dialog can say exactly what the operator was not shown.
func TestPreviewNamesWhatTheBoundLeavesOut(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	comments := []core.RenderedComment{
		{Label: "first", Text: strings.Repeat("a", 262144-10)},
		{Label: "second", Text: strings.Repeat("b", 100)},
		{Label: "third", Text: "c"},
		{Label: "fourth", Text: "d"},
	}
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, commentsPreview(comments, &repoLog{}), okSubmit())
	rootID := rootIDOf(t, ts, bearer())
	snap := awaitJobTerminal(t, ts, createPostPreviewJobOn(t, ts, server, rootID, "1234"))
	if asString(t, snap["status"]) != jobStatusSucceeded {
		t.Fatalf("preview status = %v, want succeeded (%v)", snap["status"], snap)
	}
	result := decode(t, mustJSON(t, snap["result"]))
	shown := asList(t, result["review_comments"])
	if len(shown) != 2 {
		t.Fatalf("shown %d comments, want the two the bound reaches", len(shown))
	}
	cut := asMap(t, shown[1])
	if len(asString(t, cut["text"])) != 10 || asNumber(t, cut["shown_bytes"]) != 10 || asNumber(t, cut["bytes"]) != 100 {
		t.Errorf("cut comment = %d shown of %d bytes, want 10 of 100", len(asString(t, cut["text"])), asNumber(t, cut["bytes"]))
	}
	omitted := asList(t, result["review_omitted"])
	if len(omitted) != 2 || omitted[0] != "third" || omitted[1] != "fourth" {
		t.Errorf("review_omitted = %v, want [third fourth]", omitted)
	}
	if asNumber(t, result["review_comment_count"]) != 4 {
		t.Errorf("review_comment_count = %v, want the full 4", result["review_comment_count"])
	}
	requireCutNote(t, result)
}

// requireCutNote pins the server's statement of a cut: the bytes cut, the
// comments omitted, and the CLI command that prints the whole payload.
func requireCutNote(t *testing.T, result map[string]any) {
	t.Helper()
	cutNote := asMap(t, result["review_truncated"])
	if asNumber(t, cutNote["bytes_cut"]) != 90+2 || asNumber(t, cutNote["comments_omitted"]) != 2 {
		t.Errorf("review_truncated = %v, want 92 bytes cut and 2 comments omitted", cutNote)
	}
	if want := "verdict post acme/app 1234 --dry-run"; !strings.Contains(asString(t, cutNote["message"]), want) {
		t.Errorf("review_truncated message = %q, want it to point at %q", cutNote["message"], want)
	}
}

// TestTruncatedPreviewMintsNoNonceAndCannotBeConfirmed pins the server-side
// half of "you post only what you saw": a preview that omits any posted text
// publishes no preview_digest or preview_nonce and stores no pin, so a crafted
// submit (any nonce, any digest) for that bundle refuses 409 stale-preview and
// reaches no host write, whatever client sent it.
//
// Break killed: binding the preview whatever its truncation (dropping the early
// return after setReviewComments) mints a nonce and the first assertion fails.
func TestTruncatedPreviewMintsNoNonceAndCannotBeConfirmed(t *testing.T) { //nolint:revive // one flat run of independent assertions on one wire
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var submits atomic.Int32
	submit := func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		submits.Add(1)
		return okSubmit()(ctx, req)
	}
	comments := []core.RenderedComment{
		{Label: "first", Text: strings.Repeat("a", 262144)},
		{Label: "second", Text: "left out"},
	}
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, commentsPreview(comments, &repoLog{}), submit)
	rootID := rootIDOf(t, ts, bearer())
	snap := awaitJobTerminal(t, ts, createPostPreviewJobOn(t, ts, server, rootID, "1234"))
	result := asMap(t, snap["result"])
	for _, key := range []string{"preview_digest", "preview_nonce"} {
		if _, present := result[key]; present {
			t.Errorf("a truncated preview carries %s = %v; want none", key, result[key])
		}
	}
	if _, present := result["review_truncated"]; !present {
		t.Fatalf("a truncated preview states no truncation: %v", result)
	}
	server.previews.mu.Lock()
	pins := len(server.previews.entries)
	server.previews.mu.Unlock()
	if pins != 0 {
		t.Errorf("preview store holds %d pins after a truncated preview; want none", pins)
	}
	craftedDigest, craftedNonce := strings.Repeat("ab", 32), strings.Repeat("cd", 16)
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", craftedDigest, craftedNonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Errorf("crafted submit = %d %s; want 409 stale-preview", got.status, got.body)
	}
	if n := submits.Load(); n != 0 {
		t.Errorf("host submits = %d after a crafted confirm; want 0", n)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestPostSubmitPostsToThePreviewedRepo pins the target binding on the happy
// path: the preview resolves the repository once, publishes it on the wire the
// dialog shows, and the submit's re-render and post both run against that same
// value.
func TestPostSubmitPostsToThePreviewedRepo(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var previewed, posted repoLog
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		posted.add(req.Repo)
		return submittedResponse(req), nil
	})
	preview := commentsPreview([]core.RenderedComment{{Label: "Review body", Text: "text"}}, &previewed)
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce, snap := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	if got := asString(t, asMap(t, snap["result"])["target_repo"]); got != "acme/app" {
		t.Errorf("target_repo = %q, want acme/app", got)
	}
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("submit = %d %s, want success", got.status, got.body)
	}
	if want := []string{"acme/app", "acme/app"}; strings.Join(previewed.all(), ",") != strings.Join(want, ",") {
		t.Errorf("dry-run repos = %v, want the preview then the re-render on %v", previewed.all(), want)
	}
	if want := []string{"acme/app"}; strings.Join(posted.all(), ",") != strings.Join(want, ",") {
		t.Errorf("posted repos = %v, want %v", posted.all(), want)
	}
}

// TestPostSubmitRefusesATargetRepoThatChanged pins the repository binding: the
// content digest does not cover the target, so a submit that resolves a
// different repository than the preview did refuses before the re-render and
// before any host call, and spends the nonce, because that preview is invalid.
func TestPostSubmitRefusesATargetRepoThatChanged(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var posted repoLog
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		posted.add(req.Repo)
		return submittedResponse(req), nil
	})
	var previewed repoLog
	preview := commentsPreview([]core.RenderedComment{{Label: "Review body", Text: "text"}}, &previewed)
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce, _ := mintPreviewSnapshot(t, ts, server, rootID, "1234")
	// The bundle resolves to acme/app now; the preview was bound to another
	// repository, as when source.json moves between the fingerprint and the read.
	server.previews.mu.Lock()
	server.previews.entries[nonce].repo = "someone-else/other"
	server.previews.mu.Unlock()
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) ||
		!strings.Contains(string(got.body), "target repository changed") {
		t.Fatalf("a changed target repository = %d %s, want 409 stale-preview naming it", got.status, got.body)
	}
	if n := len(posted.all()); n != 0 {
		t.Errorf("posts = %d, want 0", n)
	}
	if n := len(previewed.all()); n != 1 {
		t.Errorf("dry-runs = %d, want only the preview: the refusal comes before the re-render", n)
	}
	if _, ok := server.previews.peek(nonce, rootID, "1234", digest); ok {
		t.Error("the refused submit left the nonce spendable")
	}
}

// TestPostSubmitReRenderTimeoutNamesTheReRender pins the phase in the
// refusal: a deadline that ends the submit's own dry-run re-render is not a
// wait for the bundle lock (the submit already holds it). It answers the typed
// post-timeout naming the re-render, keeps the warnings the render gathered,
// says nothing was posted, and leaves the nonce unspent.
func TestPostSubmitReRenderTimeoutNamesTheReRender(t *testing.T) { //nolint:revive // one flow asserts the refusal, the warnings, and the nonce
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var calls atomic.Int32
	entered := make(chan struct{})
	preview := PostPreviewRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if calls.Add(1) == 1 {
			resp := dryRunResponse(req)
			resp.Data.RenderedComments = []core.RenderedComment{{Label: "Review body", Text: "text"}}
			resp.Data.RenderedHash = "hash-stable"
			return resp, nil
		}
		close(entered)
		<-ctx.Done()
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before post completed", Warnings: []string{"render warning"}}
	})
	var submits atomic.Int32
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		submits.Add(1)
		return submittedResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
	deadline := installDeadline(t, server)
	answered := submitAsync(t, ts, server, submitBody(rootID, "1234", digest, nonce, new(true)))
	awaitClosed(t, entered)
	deadline.fire()
	got := awaitResult(t, answered)
	if got.status != http.StatusOK {
		t.Fatalf("submit = %d %s, want a 200 typed refusal", got.status, got.body)
	}
	env := decode(t, got.body)
	errObj := asMap(t, env["error"])
	message := asString(t, errObj["message"])
	if asString(t, errObj["code"]) != codePostTimeout || !strings.Contains(message, "while re-rendering the review") ||
		strings.Contains(message, "bundle lock") {
		t.Errorf("error = %v, want post-timeout naming the re-render and not the lock", errObj)
	}
	requireNoValidityPromise(t, message)
	if warnings := asList(t, env["warnings"]); len(warnings) != 1 || warnings[0] != "render warning" {
		t.Errorf("warnings = %v, want the render's warning kept", warnings)
	}
	if n := submits.Load(); n != 0 {
		t.Errorf("postcore submits = %d, want 0", n)
	}
	if _, ok := server.previews.peek(nonce, rootID, "1234", digest); !ok {
		t.Error("the re-render timeout spent the nonce")
	}
}

// TestPreviewWithoutAContentDigestMintsNoNonce pins the fail-closed pin: core
// reads an empty pin as "pin nothing", so a preview whose render carries no
// digest is refused and no nonce exists to submit with.
func TestPreviewWithoutAContentDigestMintsNoNonce(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	preview := renderingPreview(func(int) (string, string) { return "text", "" })
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, okSubmit())
	rootID := rootIDOf(t, ts, bearer())
	snap := awaitJobTerminal(t, ts, createPostPreviewJobOn(t, ts, server, rootID, "1234"))
	if asString(t, snap["status"]) != jobStatusFailed ||
		asString(t, asMap(t, snap["error"])["code"]) != codePreviewUnpinnable {
		t.Fatalf("preview with no digest = %v, want a failed job with %s", snap, codePreviewUnpinnable)
	}
	if snap["result"] != nil && asMap(t, snap["result"])["preview_nonce"] != nil {
		t.Errorf("a nonce was published beside an unpinnable preview: %v", snap["result"])
	}
	if n := len(server.previews.entries); n != 0 {
		t.Errorf("preview store holds %d entries, want none", n)
	}
	if _, err := server.previews.mint(rootID, "1234", "digest", "fingerprint", "", "acme/app"); err == nil {
		t.Error("the store minted a nonce with an empty pin")
	}
}

// TestPostSubmitRefusesAReRenderThatPinsNothing pins the same rule at submit:
// a re-render that yields no digest cannot be checked against the preview, and
// the confirmed post must never go out with an empty pin.
func TestPostSubmitRefusesAReRenderThatPinsNothing(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	preview := renderingPreview(func(n int) (string, string) {
		if n == 1 {
			return "text", "hash-shown"
		}
		return "text", ""
	})
	var posts atomic.Int32
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		posts.Add(1)
		return submittedResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusConflict || !strings.Contains(string(got.body), `"reason":"stale-preview"`) {
		t.Fatalf("submit after an unpinnable re-render = %d %s, want 409 stale-preview", got.status, got.body)
	}
	if n := posts.Load(); n != 0 {
		t.Errorf("posts = %d, want 0", n)
	}
}

// TestPostSubmitReRenderStagesNothingAndPinsTheRealPost pins two request
// properties: the submit's re-render runs read-only (it must not stage
// findings.dryrun.json under the lock) while the preview does not, and the real
// post carries the preview's non-empty digest as its pin.
func TestPostSubmitReRenderStagesNothingAndPinsTheRealPost(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var mu sync.Mutex
	var noPersist []bool
	preview := PostPreviewRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		mu.Lock()
		noPersist = append(noPersist, req.NoPersist)
		mu.Unlock()
		resp := dryRunResponse(req)
		resp.Data.RenderedHash = "hash-stable"
		return resp, nil
	})
	var pin string
	var postNoPersist bool
	submit := PostSubmitRunner(func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		pin, postNoPersist = req.PinnedContentHash, req.NoPersist
		return submittedResponse(req), nil
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
	got := post(t, ts, postSubmitPath, submitBody(rootID, "1234", digest, nonce, new(true)), writeHeaders(server))
	if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
		t.Fatalf("submit = %d %s, want success", got.status, got.body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(noPersist) != 2 || noPersist[0] || !noPersist[1] {
		t.Errorf("NoPersist per dry-run = %v, want [false true]: the preview stages, the submit's re-render does not", noPersist)
	}
	if pin != "hash-stable" || postNoPersist {
		t.Errorf("real post pin = %q (no-persist %v), want the preview's digest on a persisting post", pin, postNoPersist)
	}
}

// TestPostSubmitReRenderKeepsADefinitiveRefusal pins the guard on the re-render
// relabel: a refusal that is not the ended context's own (a gate, a changed
// bundle) keeps its code even when the deadline passes around it.
func TestPostSubmitReRenderKeepsADefinitiveRefusal(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var calls atomic.Int32
	entered := make(chan struct{})
	preview := PostPreviewRunner(func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if calls.Add(1) == 1 {
			return dryRunResponse(req), nil
		}
		close(entered)
		<-ctx.Done()
		return nil, &core.RefusalError{Code: "no-pr-target", Message: "post needs a PR bundle"}
	})
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, okSubmit())
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
	deadline := installDeadline(t, server)
	answered := submitAsync(t, ts, server, submitBody(rootID, "1234", digest, nonce, new(true)))
	awaitClosed(t, entered)
	deadline.fire()
	got := awaitResult(t, answered)
	if code := asString(t, asMap(t, decode(t, got.body)["error"])["code"]); code != "no-pr-target" {
		t.Errorf("error code = %q, want the definitive refusal's own %q (%s)", code, "no-pr-target", got.body)
	}
}
