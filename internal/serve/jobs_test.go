package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/storelock"
	"github.com/stainless-code/verdict/internal/testenv"
)

// interactiveJobServer binds an interactive job server with scripted runners.
func interactiveJobServer(
	t *testing.T, layer Layer, verify VerifyRunner, postPreview PostPreviewRunner,
) (*httptest.Server, *Server) {
	t.Helper()
	server, err := New(&Config{
		Port: testPort, Token: testToken, Now: fixedNow, Layer: layer,
		VerifyRunner: verify, PostPreviewRunner: postPreview,
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
	return ts, server
}

// okVerifyRunner is a scripted sandboxed verify that never spawns.
func okVerifyRunner() VerifyRunner {
	return func(ctx context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		if !req.Sandbox {
			return nil, &core.RefusalError{Code: "test-assert", Message: "Sandbox must be true"}
		}
		if err := ctx.Err(); err != nil {
			return nil, &core.RefusalError{Code: "canceled", Message: "canceled before verify completed"}
		}
		data := &core.VerifyData{Exit: 0, TimedOut: false, OutputTail: "ok", Patch: "patch", Sandboxed: true}
		return &verifycore.Response{
			Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z", Data: data,
			Warnings: []string{}, Next: "ok",
		}, nil
	}
}

// okPostPreviewRunner is a scripted dry-run preview that never spawns.
func okPostPreviewRunner() PostPreviewRunner {
	return func(ctx context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if !req.Flags.DryRun {
			return nil, &core.RefusalError{Code: "test-assert", Message: "DryRun must be true"}
		}
		if err := ctx.Err(); err != nil {
			return nil, &core.RefusalError{Code: "canceled", Message: "canceled before post completed"}
		}
		data := &core.PostData{DryRun: true, Event: "COMMENT"}
		return &postcore.Response{
			Repo: req.Repo, Pr: req.Pr, At: "2026-11-12T09:00:00Z", Data: data,
			Warnings: []string{}, Next: "payload NOT posted (dry-run)",
		}, nil
	}
}

// createVerifyJob posts one verify job on bundle 1234 and returns its id.
func createVerifyJob(t *testing.T, ts *httptest.Server, server *Server, rootID string) string {
	t.Helper()
	return createVerifyJobOn(t, ts, server, rootID, "1234")
}

// createVerifyJobOn posts one verify job on the named bundle and
// returns its id.
func createVerifyJobOn(t *testing.T, ts *httptest.Server, server *Server, rootID, bundle string) string {
	t.Helper()
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"`+bundle+`"}`, writeHeaders(server))
	if got.status != http.StatusAccepted {
		t.Fatalf("POST verify = %d, want 202 (%s)", got.status, got.body)
	}
	env := decode(t, got.body)
	if env["error"] != nil {
		t.Fatalf("error = %v, want null (%s)", env["error"], got.body)
	}
	return asString(t, asMap(t, env["data"])["id"])
}

// distinctBundles creates n empty bundle directories for parallel job
// submission: each job needs its own bundle lock to reach its runner.
func distinctBundles(t *testing.T, root string, n int) []string {
	t.Helper()
	names := make([]string, 0, n)
	for i := range n {
		name := "j" + strconv.Itoa(i)
		if err := os.MkdirAll(filepath.Join(root, ".verdict", "state", name), 0o755); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// createPostPreviewJob posts one post-preview job and returns its id.
func createPostPreviewJob(t *testing.T, ts *httptest.Server, server *Server, rootID string) string {
	t.Helper()
	got := post(t, ts, "/v1/jobs/post-preview", `{"root":"`+rootID+`","bundle":"1234"}`, writeHeaders(server))
	if got.status != http.StatusAccepted {
		t.Fatalf("POST post-preview = %d, want 202 (%s)", got.status, got.body)
	}
	return asString(t, asMap(t, decode(t, got.body)["data"])["id"])
}

// jobStatus polls GET until one job reaches terminal and returns the snapshot.
func awaitJobTerminal(t *testing.T, ts *httptest.Server, id string) map[string]any {
	t.Helper()
	var snapshot map[string]any
	testenv.Until(t, func() bool {
		got := call(t, ts, http.MethodGet, "/v1/jobs/"+id, "", bearer())
		if got.status != http.StatusOK {
			return false
		}
		snapshot = asMap(t, decode(t, got.body)["data"])
		status := asString(t, snapshot["status"])
		return status == jobStatusSucceeded || status == jobStatusFailed || status == jobStatusCancelled
	})
	return snapshot
}

// jobCount returns the retained job count under the manager lock.
func jobCount(server *Server) int {
	server.jobs.mu.Lock()
	defer server.jobs.mu.Unlock()
	return len(server.jobs.jobs)
}

// TestJobsRefusedWithoutInteractive pins the read-only fail-closed matrix:
// every job path answers 405 with Allow GET and writes nothing.
func TestJobsRefusedWithoutInteractive(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	before := snapshotState(t, root)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/jobs/verify"},
		{http.MethodPost, "/v1/jobs/post-preview"},
		{http.MethodPost, "/v1/jobs/abc/cancel"},
		{http.MethodGet, "/v1/jobs/verify"},
		{http.MethodGet, "/v1/jobs/post-preview"},
		{http.MethodGet, "/v1/jobs/abc"},
	} {
		got := call(t, ts, tc.method, tc.path, "", bearer())
		if got.status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405 (%s)", tc.method, tc.path, got.status, got.body)
		}
		if !strings.Contains(got.header.Get("Allow"), http.MethodGet) {
			t.Errorf("%s %s Allow = %q, want GET", tc.method, tc.path, got.header.Get("Allow"))
		}
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused job changed the fixture tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// assertJobGuardRefusal posts once and pins the transport refusal.
func assertJobGuardRefusal(
	t *testing.T, ts *httptest.Server, server *Server, root, path, payload string,
	headers map[string]string, wantStatus int, wantReason, name string,
) {
	t.Helper()
	before := snapshotState(t, root)
	got := post(t, ts, path, payload, headers)
	if got.status != wantStatus || !strings.Contains(string(got.body), `"reason":"`+wantReason+`"`) {
		t.Errorf("%s = %d %s, want %d %s", name, got.status, got.body, wantStatus, wantReason)
	}
	assertNoJobs(t, server, root, before)
}

// TestJobsGuardsRefuseBeforeQueue pins CSRF, Origin, and Content-Type
// failing closed with zero jobs created.
func TestJobsGuardsRefuseBeforeQueue(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	payload := `{"root":"` + rootID + `","bundle":"1234"}`
	noCSRF := maps.Clone(hdrs)
	delete(noCSRF, "X-Verdict-CSRF")
	badOrigin := maps.Clone(hdrs)
	badOrigin["Origin"] = "https://evil.test"
	badContentType := maps.Clone(hdrs)
	badContentType["Content-Type"] = "text/plain"
	for _, tc := range []struct {
		headers map[string]string
		name    string
		path    string
		reason  string
		status  int
	}{
		{name: "csrf absent", path: "/v1/jobs/verify", headers: noCSRF, status: http.StatusForbidden, reason: "bad-csrf"},
		{name: "post-preview csrf absent", path: "/v1/jobs/post-preview", headers: noCSRF, status: http.StatusForbidden, reason: "bad-csrf"},
		{name: "bad origin", path: "/v1/jobs/verify", headers: badOrigin, status: http.StatusForbidden, reason: "bad-origin"},
		{name: "bad content-type", path: "/v1/jobs/post-preview", headers: badContentType, status: http.StatusBadRequest, reason: "bad-content-type"},
	} {
		assertJobGuardRefusal(t, ts, server, root, tc.path, payload, tc.headers, tc.status, tc.reason, tc.name)
	}
}

// assertNoJobs proves a refused job created nothing and wrote nothing.
func assertNoJobs(t *testing.T, server *Server, root string, before map[string]string) {
	t.Helper()
	if count := jobCount(server); count != 0 {
		t.Errorf("retained jobs = %d, want 0", count)
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused job changed the tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// TestJobsBadBodyRefusals pins malformed bodies refusing before queueing.
func TestJobsBadBodyRefusals(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	for _, tc := range []struct {
		name, path, body string
	}{
		{"unknown field", "/v1/jobs/verify", `{"root":"` + rootID + `","bundle":"1234","surprise":1}`},
		{"trailing", "/v1/jobs/verify", `{"root":"` + rootID + `","bundle":"1234"} {}`},
		{"null", "/v1/jobs/verify", `null`},
		{"array", "/v1/jobs/post-preview", `[]`},
		{"oversized", "/v1/jobs/post-preview", `{"root":"` + strings.Repeat("x", 70*1024) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshotState(t, root)
			got := post(t, ts, tc.path, tc.body, hdrs)
			if got.status != http.StatusBadRequest || !strings.Contains(string(got.body), `"reason":"bad-body"`) {
				t.Errorf("status = %d %s, want 400 bad-body", got.status, got.body)
			}
			assertNoJobs(t, server, root, before)
		})
	}
}

// TestJobsMethodNotAllowed pins POST-only on the interactive job posts.
func TestJobsMethodNotAllowed(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, _ := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/jobs/verify"},
		{http.MethodHead, "/v1/jobs/verify"},
		{http.MethodPut, "/v1/jobs/verify"},
		{http.MethodGet, "/v1/jobs/post-preview"},
		{http.MethodPut, "/v1/jobs/post-preview"},
		{http.MethodGet, "/v1/jobs/abc/cancel"},
	} {
		got := call(t, ts, tc.method, tc.path, "", bearer())
		if got.status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405 (%s)", tc.method, tc.path, got.status, got.body)
		}
		if !strings.Contains(got.header.Get("Allow"), http.MethodPost) {
			t.Errorf("%s %s Allow = %q, want POST", tc.method, tc.path, got.header.Get("Allow"))
		}
	}
}

// TestVerifyJobRejectsSig pins the honest bundle-scoped shape: verify runs
// over the whole bundle, so a sig field is an unknown field and refuses
// as bad-body before any job exists.
func TestVerifyJobRejectsSig(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	for _, sig := range []string{"ABCDEF12", strings.Repeat("f", 40), sigCrit} {
		before := snapshotState(t, root)
		got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234","sig":"`+sig+`"}`, writeHeaders(server))
		if got.status != http.StatusBadRequest || !strings.Contains(string(got.body), `"reason":"bad-body"`) {
			t.Errorf("sig %q = %d %s, want 400 bad-body", sig, got.status, got.body)
		}
		assertNoJobs(t, server, root, before)
	}
}

// TestVerifyJobSymlinkEscapeRefused proves the write confinement fails
// closed with a typed refusal and zero writes outside the root.
func TestVerifyJobSymlinkEscapeRefused(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are unavailable in this Windows test environment")
	}
	outside := t.TempDir()
	writeFixtureAt(t, outside, map[string]string{
		".verdict/state/1234/findings.json": "[" + finding(sigCrit, "open", "critical", "walked") + "]",
	})
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, ".verdict", "state"), filepath.Join(root, ".verdict", "state")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	before := snapshotState(t, outside)
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234"}`, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 typed refusal (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"code": "bad-config"`) {
		t.Errorf("body = %s, want code bad-config", got.body)
	}
	if count := jobCount(server); count != 0 {
		t.Errorf("retained jobs = %d, want 0 after a confinement refusal", count)
	}
	assertOutsideUnchanged(t, outside, before)
}

// recordingVerifyRunner builds a scripted sandboxed verify that records
// it ran and asserts the hardcoded sandbox posture.
func recordingVerifyRunner(called *bool) VerifyRunner {
	return func(_ context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		if !req.Sandbox {
			return nil, &core.RefusalError{Code: "test-assert", Message: "Sandbox must be true"}
		}
		if req.TimeoutRaw == "" {
			return nil, &core.RefusalError{Code: "test-assert", Message: "timeout must be set"}
		}
		*called = true
		data := &core.VerifyData{Exit: 0, OutputTail: "proof holds", Sandboxed: true}
		return &verifycore.Response{
			Repo: req.Repo, Pr: req.Pr, At: fixedNow().UTC().Format("2006-01-02T15:04:05Z"),
			Data: data, Next: "ok",
		}, nil
	}
}

// TestVerifySandboxHappyPath proves the sandboxed runner executes with
// Sandbox true, the job succeeds, and the query cache invalidates.
func TestVerifySandboxHappyPath(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	count := 0
	layer := invalidateCountingLayer{Layer: testLayer(root, nil), count: &count}
	called := false
	runner := recordingVerifyRunner(&called)
	ts, server := interactiveJobServer(t, layer, runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	assertVerifySuccess(t, ts, id, &called, &count)
}

// assertVerifySuccess pins one sandboxed verify reaching succeeded.
func assertVerifySuccess(t *testing.T, ts *httptest.Server, id string, called *bool, count *int) {
	t.Helper()
	snapshot := awaitJobTerminal(t, ts, id)
	if asString(t, snapshot["status"]) != jobStatusSucceeded {
		t.Fatalf("status = %v, want succeeded (%v)", snapshot["status"], snapshot)
	}
	if !*called {
		t.Fatal("the scripted runner never ran")
	}
	result := asMap(t, snapshot["result"])
	if result["sandboxed"] != true || result["exit"] == nil {
		t.Errorf("result = %v, want sandboxed true with an exit", result)
	}
	if *count != 1 {
		t.Errorf("Invalidate calls = %d, want 1 after a sandboxed verify", *count)
	}
}

// recordingPostRunner builds a scripted dry-run preview that records it
// ran and asserts the hardcoded dry-run posture.
func recordingPostRunner(called *bool) PostPreviewRunner {
	return func(_ context.Context, req *postcore.Request) (*postcore.Response, *core.RefusalError) {
		if !req.Flags.DryRun {
			return nil, &core.RefusalError{Code: "test-assert", Message: "DryRun must be true"}
		}
		*called = true
		data := &core.PostData{DryRun: true, Event: "COMMENT"}
		return &postcore.Response{
			Repo: req.Repo, Pr: req.Pr, At: fixedNow().UTC().Format("2006-01-02T15:04:05Z"),
			Data: data, Next: "payload NOT posted (dry-run)",
		}, nil
	}
}

// TestPostPreviewDryRunHappyPath proves the preview runner executes with
// DryRun true and the job succeeds with a dry-run result.
func TestPostPreviewDryRunHappyPath(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	called := false
	runner := recordingPostRunner(&called)
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), runner)
	rootID := rootIDOf(t, ts, bearer())
	id := createPostPreviewJob(t, ts, server, rootID)
	assertPostSuccess(t, ts, id, &called)
}

// assertPostSuccess pins one dry-run preview reaching succeeded.
func assertPostSuccess(t *testing.T, ts *httptest.Server, id string, called *bool) {
	t.Helper()
	snapshot := awaitJobTerminal(t, ts, id)
	if asString(t, snapshot["status"]) != jobStatusSucceeded {
		t.Fatalf("status = %v, want succeeded (%v)", snapshot["status"], snapshot)
	}
	if !*called {
		t.Fatal("the scripted runner never ran")
	}
	result := asMap(t, snapshot["result"])
	if result["dry_run"] != true {
		t.Errorf("result = %v, want dry_run true", result)
	}
}

// TestJobRefusalSurfacesHonestly pins a service refusal becoming a failed
// job with its code and message, never a success.
func TestJobRefusalSurfacesHonestly(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	runner := VerifyRunner(func(_ context.Context, _ *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		return nil, &core.RefusalError{Code: "no-verify-cmd", Message: "patch.json carries no verifyCmd to execute"}
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	snapshot := awaitJobTerminal(t, ts, id)
	if asString(t, snapshot["status"]) != jobStatusFailed {
		t.Fatalf("status = %v, want failed (%v)", snapshot["status"], snapshot)
	}
	failure := asMap(t, snapshot["error"])
	if asString(t, failure["code"]) != "no-verify-cmd" {
		t.Errorf("error = %v, want code no-verify-cmd", failure)
	}
	if asString(t, failure["message"]) == "" {
		t.Errorf("error = %v, want a message", failure)
	}
}

// TestJobSSEPublishesLifecycle proves queued, running, and terminal
// states arrive on the existing SSE stream as jobs events.
func TestJobSSEPublishesLifecycle(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	started := make(chan struct{})
	release := make(chan struct{})
	runner := VerifyRunner(func(_ context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		close(started)
		<-release
		data := &core.VerifyData{Exit: 0, OutputTail: "ok", Sandboxed: true}
		return &verifycore.Response{
			Repo: req.Repo, Pr: req.Pr, At: fixedNow().UTC().Format("2006-01-02T15:04:05Z"),
			Data: data, Next: "ok",
		}, nil
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	stream := openJobSSE(t, ts)
	defer func() { _ = stream.Body.Close() }()
	// A stalled read has no deadline of its own; closing the body turns a
	// missing frame into a failed read instead of the suite's global timeout.
	watchdog := time.AfterFunc(15*time.Second, func() { _ = stream.Body.Close() })
	defer watchdog.Stop()
	reader := bufio.NewReader(stream.Body)
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	<-started
	// The stream keeps only the newest frame per client, so a job released
	// before its running frame is read can settle first and replace it.
	// setStatus publishes running before the runner starts and nothing else
	// publishes until release, so running is the frame waiting in the stream.
	statuses := readJobEventsUntil(t, reader, id, nil, func(status string) bool { return status == jobStatusRunning })
	close(release)
	statuses = readJobEventsUntil(t, reader, id, statuses, isTerminal)
	// queued is best effort: a client that reads after running replaced it
	// legitimately never sees it, so only the observed order is pinned.
	if last := statuses[len(statuses)-1]; last != jobStatusSucceeded {
		t.Errorf("SSE statuses = %v, want terminal %s", statuses, jobStatusSucceeded)
	}
	if !slices.Contains(statuses, jobStatusRunning) {
		t.Errorf("SSE statuses = %v, missing %s", statuses, jobStatusRunning)
	}
	if i := slices.Index(statuses, jobStatusQueued); i > 0 {
		t.Errorf("SSE statuses = %v, queued must come first", statuses)
	}
}

// openJobSSE opens one authenticated job event stream.
func openJobSSE(t *testing.T, ts *httptest.Server) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testHost
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("GET /v1/events = %d, want 200", resp.StatusCode)
	}
	return resp
}

// readJobEventsUntil appends the job's statuses to statuses until one
// satisfies stop, failing when the stream ends or yields too many frames.
func readJobEventsUntil(
	t *testing.T, reader *bufio.Reader, id string, statuses []string, stop func(string) bool,
) []string {
	t.Helper()
	for range 40 {
		status := readOneJobStatus(t, reader, id, statuses)
		if status == "" {
			continue
		}
		statuses = append(statuses, status)
		if stop(status) {
			return statuses
		}
	}
	t.Fatalf("job %s never reached the awaited SSE event (statuses %v)", id, statuses)
	return statuses
}

// readOneJobStatus reads one line and decodes it when it is a jobs frame.
func readOneJobStatus(t *testing.T, reader *bufio.Reader, id string, statuses []string) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event: %v (statuses so far %v)", err, statuses)
	}
	if !strings.HasPrefix(line, "event: jobs") {
		return ""
	}
	return jobEventStatus(t, readJobDataLine(t, reader), id)
}

// readJobDataLine reads the id, retry, and data lines after one frame.
func readJobDataLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	for range 2 {
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatalf("read event header: %v", err)
		}
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event data: %v", err)
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data: "))
}

// jobEventStatus decodes one SSE payload and returns the job status.
func jobEventStatus(t *testing.T, data, wantID string) string {
	t.Helper()
	if data == "" {
		return ""
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		t.Fatalf("decode job event %q: %v", data, err)
	}
	if envelope.Data == nil || envelope.Data["id"] != wantID {
		return ""
	}
	status, ok := envelope.Data["status"].(string)
	if !ok {
		return ""
	}
	return status
}

// TestJobCancel proves explicit cancellation stops a running job through
// its context and leaves no runner running.
func TestJobCancel(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	started := make(chan struct{})
	var once sync.Once
	runner := VerifyRunner(func(ctx context.Context, _ *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before verify completed"}
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	<-started
	hdrs := writeHeaders(server)
	hdrs["Content-Type"] = "application/json"
	got := post(t, ts, "/v1/jobs/"+id+"/cancel", `{}`, hdrs)
	if got.status != http.StatusOK {
		t.Fatalf("POST cancel = %d, want 200 (%s)", got.status, got.body)
	}
	snapshot := awaitJobTerminal(t, ts, id)
	if asString(t, snapshot["status"]) != jobStatusCancelled {
		t.Fatalf("status = %v, want canceled (%v)", snapshot["status"], snapshot)
	}
	got = call(t, ts, http.MethodGet, "/v1/jobs/"+id, "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("GET canceled job = %d, want 200 (%s)", got.status, got.body)
	}
	again := post(t, ts, "/v1/jobs/"+id+"/cancel", `{}`, hdrs)
	if again.status != http.StatusOK {
		t.Fatalf("POST cancel again = %d, want 200 idempotent (%s)", again.status, again.body)
	}
}

// TestJobCancelUnknown proves an unknown id answers 404.
func TestJobCancelUnknown(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	got := post(t, ts, "/v1/jobs/missing/cancel", `{}`, writeHeaders(server))
	if got.status != http.StatusNotFound {
		t.Fatalf("POST unknown cancel = %d, want 404 (%s)", got.status, got.body)
	}
	got = call(t, ts, http.MethodGet, "/v1/jobs/missing", "", bearer())
	if got.status != http.StatusNotFound {
		t.Fatalf("GET unknown job = %d, want 404 (%s)", got.status, got.body)
	}
}

// TestJobStoreBusy proves a held bundle lock refuses the job as
// store-busy without running the runner. A short lock wait keeps the
// busy path under 100ms instead of the production multi-second wait.
func TestJobStoreBusy(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	store := filepath.ToSlash(filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "findings.json"))
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	called := false
	runner := VerifyRunner(func(_ context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		called = true
		data := &core.VerifyData{Exit: 0, Sandboxed: true}
		return &verifycore.Response{Pr: req.Pr, Data: data}, nil
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	server.jobs.lockWait = 20 * time.Millisecond
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	snapshot := awaitJobTerminal(t, ts, id)
	unlock()
	if called {
		t.Fatal("the runner ran while the bundle lock was held")
	}
	if asString(t, snapshot["status"]) != jobStatusFailed {
		t.Fatalf("status = %v, want failed (%v)", snapshot["status"], snapshot)
	}
	failure := asMap(t, snapshot["error"])
	if asString(t, failure["code"]) != "store-busy" {
		t.Errorf("error = %v, want code store-busy", failure)
	}
}

// TestJobHealthCapability pins the Tier B advertisement only on an
// interactive server.
func TestJobHealthCapability(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	layer := testLayer(root, nil)
	ts, _ := interactiveJobServer(t, layer, okVerifyRunner(), okPostPreviewRunner())
	got := call(t, ts, http.MethodGet, "/v1/health", "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("health = %d, want 200 (%s)", got.status, got.body)
	}
	data := asMap(t, decode(t, got.body)["data"])
	if data["read_only"] != false || data["interactive"] != true || data["jobs"] != true {
		t.Errorf("health = %s, want read_only false with interactive and jobs true", got.body)
	}
	actions := asList(t, data["actions"])
	if len(actions) != 2 || actions[0] != "verify" || actions[1] != "post-preview" {
		t.Errorf("actions = %v, want [verify post-preview]", actions)
	}
	plain := testServer(t, layer)
	plainBody := call(t, plain, http.MethodGet, "/v1/health", "", bearer()).body
	if strings.Contains(string(plainBody), `"jobs"`) || strings.Contains(string(plainBody), `"actions"`) {
		t.Errorf("plain health = %s, must not advertise jobs or actions", plainBody)
	}
}

// TestJobsConcurrentAndShutdown proves concurrent jobs finish under -race
// and the manager joins them cleanly on stop.
func TestJobsConcurrentAndShutdown(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	var mu sync.Mutex
	entered := 0
	runner := VerifyRunner(func(_ context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		mu.Lock()
		entered++
		mu.Unlock()
		data := &core.VerifyData{Exit: 0, Sandboxed: true}
		return &verifycore.Response{Pr: req.Pr, Data: data}, nil
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	const jobs = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	var idsMu sync.Mutex
	ids := make([]string, 0, jobs)
	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			<-start
			id := createVerifyJob(t, ts, server, rootID)
			idsMu.Lock()
			ids = append(ids, id)
			idsMu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	awaitAllSucceeded(t, ts, ids)
	mu.Lock()
	defer mu.Unlock()
	if entered != jobs {
		t.Errorf("runner entered = %d, want %d", entered, jobs)
	}
	done := make(chan struct{})
	go func() { server.jobs.stop(); close(done) }()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("job shutdown did not join")
	}
	// A second stop is idempotent: the WaitGroup stays joined.
	server.jobs.stop()
}

// jobStatusOf polls one job until its status equals want.
func jobStatusOf(t *testing.T, ts *httptest.Server, id, want string) {
	t.Helper()
	testenv.Until(t, func() bool {
		got := call(t, ts, http.MethodGet, "/v1/jobs/"+id, "", bearer())
		if got.status != http.StatusOK {
			return false
		}
		snapshot := asMap(t, decode(t, got.body)["data"])
		return asString(t, snapshot["status"]) == want
	})
}

// blockedVerifyRunner builds a verify runner that entries, then waits
// for release. Entered counts invocations guarded by mu; maxActive and
// active track the number of concurrently executing runners.
func blockedVerifyRunner(release chan struct{}, mu *sync.Mutex, entered, active, maxActive *int) VerifyRunner {
	return func(ctx context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		mu.Lock()
		*entered++
		*active++
		if *active > *maxActive {
			*maxActive = *active
		}
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		*active--
		mu.Unlock()
		data := &core.VerifyData{Exit: 0, Sandboxed: true}
		return &verifycore.Response{Pr: req.Pr, Data: data}, nil
	}
}

// countLocked reads the shared counters under mu for a polling predicate.
func countLocked(mu *sync.Mutex, value *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *value
}

// awaitAllSucceeded awaits each id to succeeded.
func awaitAllSucceeded(t *testing.T, ts *httptest.Server, ids []string) {
	t.Helper()
	for _, id := range ids {
		snapshot := awaitJobTerminal(t, ts, id)
		if asString(t, snapshot["status"]) != jobStatusSucceeded {
			t.Fatalf("job %s status = %v, want succeeded (%v)", id, snapshot["status"], snapshot)
		}
	}
}

// cancelJob posts the cancel for one id and asserts the 200.
func cancelJob(t *testing.T, ts *httptest.Server, hdrs map[string]string, id string) {
	t.Helper()
	got := post(t, ts, "/v1/jobs/"+id+"/cancel", `{}`, hdrs)
	if got.status != http.StatusOK {
		t.Fatalf("POST cancel = %d, want 200 (%s)", got.status, got.body)
	}
}

// assertTerminalCanceled awaits terminal and asserts canceled.
func assertTerminalCanceled(t *testing.T, ts *httptest.Server, id string) {
	t.Helper()
	snapshot := awaitJobTerminal(t, ts, id)
	if asString(t, snapshot["status"]) != jobStatusCancelled {
		t.Fatalf("status = %v, want canceled (%v)", snapshot["status"], snapshot)
	}
}

// assertBoundHeld asserts the concurrency bound while all slots block.
func assertBoundHeld(t *testing.T, mu *sync.Mutex, entered, maxActive int) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if entered != maxConcurrentJobs {
		t.Fatalf("entered = %d, want %d while all slots blocked", entered, maxConcurrentJobs)
	}
	if maxActive != maxConcurrentJobs {
		t.Fatalf("maxActive = %d, want %d; concurrency exceeds the bound", maxActive, maxConcurrentJobs)
	}
}

// assertBoundFinal asserts the bound after release.
func assertBoundFinal(t *testing.T, mu *sync.Mutex, entered, maxActive int) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if entered != 6 {
		t.Errorf("entered = %d, want 6 after release", entered)
	}
	if maxActive != maxConcurrentJobs {
		t.Errorf("maxActive = %d, want %d; concurrency exceeds the bound", maxActive, maxConcurrentJobs)
	}
}

// assertFastDrained asserts one frame arrives for one publish round.
func assertFastDrained(t *testing.T, fast <-chan jobFrame, i int) {
	t.Helper()
	select {
	case frame := <-fast:
		if frame.id == 0 {
			t.Fatalf("publish %d delivered an unnumbered frame", i)
		}
	default:
		t.Fatalf("healthy subscriber missed publish %d", i)
	}
}

// assertSlowBounded asserts the slow channel never overflows its slot.
func assertSlowBounded(t *testing.T, slow <-chan jobFrame) {
	t.Helper()
	if count := len(slow); count > eventBufferSize {
		t.Fatalf("slow subscriber buffered %d frames, want at most %d", count, eventBufferSize)
	}
}

// assertSlowHeld asserts the slow channel still holds one frame.
func assertSlowHeld(t *testing.T, slow <-chan jobFrame) {
	t.Helper()
	select {
	case frame := <-slow:
		if frame.id == 0 {
			t.Fatal("slow subscriber delivered an unnumbered frame")
		}
	default:
		t.Fatal("slow subscriber held no frame after 10 publishes")
	}
}

// publishRound publishes one frame and checks both subscribers.
func publishRound(t *testing.T, broker *eventBroker, slow, fast <-chan jobFrame, i int) {
	t.Helper()
	broker.publishJobs(jq.NewObj("id", "job-slow", "seq", i))
	assertFastDrained(t, fast, i)
	assertSlowBounded(t, slow)
}

// TestJobCancelWhileQueued proves cancellation while waiting for a
// concurrency slot transitions to canceled and the runner never runs.
// Mutation: dropping the ctx.Done case in acquireSlot leaves the fifth
// job queued instead of canceled, so the terminal wait fails.
func TestJobCancelWhileQueued(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	release := make(chan struct{})
	var mu sync.Mutex
	entered, active, maxActive := 0, 0, 0
	runner := blockedVerifyRunner(release, &mu, &entered, &active, &maxActive)
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	bundles := distinctBundles(t, root, 5)
	hdrs := writeHeaders(server)
	ids := make([]string, 0, 4)
	for _, bundle := range bundles[:4] {
		ids = append(ids, createVerifyJobOn(t, ts, server, rootID, bundle))
	}
	testenv.Until(t, func() bool { return countLocked(&mu, &entered) == 4 })
	queuedID := createVerifyJobOn(t, ts, server, rootID, bundles[4])
	jobStatusOf(t, ts, queuedID, jobStatusQueued)
	cancelJob(t, ts, hdrs, queuedID)
	assertTerminalCanceled(t, ts, queuedID)
	if got := countLocked(&mu, &entered); got != 4 {
		t.Fatalf("runner entered = %d, want 4; the queued runner must never execute", got)
	}
	close(release)
	awaitAllSucceeded(t, ts, ids)
	if got := countLocked(&mu, &entered); got != 4 {
		t.Fatalf("runner entered after release = %d, want 4; the canceled job never ran", got)
	}
}

// TestJobCancelWhileWaitingOnLock proves cancellation while waiting on
// the bundle lock cancels promptly instead of waiting out the lock
// timeout, and the runner never runs.
func TestJobCancelWhileWaitingOnLock(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	store := filepath.ToSlash(filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "findings.json"))
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	called := false
	var calledMu sync.Mutex
	runner := VerifyRunner(func(_ context.Context, req *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		calledMu.Lock()
		called = true
		calledMu.Unlock()
		data := &core.VerifyData{Exit: 0, Sandboxed: true}
		return &verifycore.Response{Pr: req.Pr, Data: data}, nil
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	jobStatusOf(t, ts, id, jobStatusRunning)
	start := time.Now()
	got := post(t, ts, "/v1/jobs/"+id+"/cancel", `{}`, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("POST cancel = %d, want 200 (%s)", got.status, got.body)
	}
	snapshot := awaitJobTerminal(t, ts, id)
	elapsed := time.Since(start)
	unlock()
	if asString(t, snapshot["status"]) != jobStatusCancelled {
		t.Fatalf("status = %v, want canceled (%v)", snapshot["status"], snapshot)
	}
	if elapsed >= storelock.DefaultWait {
		t.Fatalf("cancel waited %s, want prompt cancellation inside the lock wait", elapsed)
	}
	calledMu.Lock()
	defer calledMu.Unlock()
	if called {
		t.Fatal("the runner ran while the job waited on the store lock")
	}
}

// TestJobMaxConcurrentBound proves at most four runners execute at
// once: six blocked jobs hold four active and two queued, then all six
// finish after release. Mutation: enlarging the semaphore lets all six
// enter, so the maxActive assertion fires; shrinking it leaves entered
// below four, so the bound wait fails.
func TestJobMaxConcurrentBound(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	release := make(chan struct{})
	var mu sync.Mutex
	entered, active, maxActive := 0, 0, 0
	runner := blockedVerifyRunner(release, &mu, &entered, &active, &maxActive)
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	bundles := distinctBundles(t, root, 6)
	ids := make([]string, 0, 6)
	for _, bundle := range bundles {
		ids = append(ids, createVerifyJobOn(t, ts, server, rootID, bundle))
	}
	testenv.Until(t, func() bool { return countLocked(&mu, &entered) >= maxConcurrentJobs })
	for range 100 {
		runtime.Gosched()
	}
	assertBoundHeld(t, &mu, entered, maxActive)
	close(release)
	awaitAllSucceeded(t, ts, ids)
	assertBoundFinal(t, &mu, entered, maxActive)
}

// TestJobEvictionCapsHistory proves retained history stays capped at
// the retained limit: the oldest terminal jobs evict first.
func TestJobEvictionCapsHistory(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	ids := make([]string, 0, 70)
	for range 70 {
		id := createVerifyJob(t, ts, server, rootID)
		awaitJobTerminal(t, ts, id)
		ids = append(ids, id)
	}
	if count := jobCount(server); count != maxRetainedJobs {
		t.Fatalf("retained jobs = %d, want %d", count, maxRetainedJobs)
	}
	for _, id := range ids[:6] {
		got := call(t, ts, http.MethodGet, "/v1/jobs/"+id, "", bearer())
		if got.status != http.StatusNotFound {
			t.Fatalf("GET evicted job %s = %d, want 404 (%s)", id, got.status, got.body)
		}
	}
	got := call(t, ts, http.MethodGet, "/v1/jobs/"+ids[len(ids)-1], "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("GET newest job = %d, want 200 (%s)", got.status, got.body)
	}
}

// TestJobCapacityRefusesFullHistory proves a create that cannot evict
// refuses 503 job-capacity. A small retained cap on the manager keeps
// the full-history state reachable without 64 blocked workers.
func TestJobCapacityRefusesFullHistory(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	release := make(chan struct{})
	var mu sync.Mutex
	entered, active, maxActive := 0, 0, 0
	runner := blockedVerifyRunner(release, &mu, &entered, &active, &maxActive)
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	server.jobs.maxRetained = 3
	rootID := rootIDOf(t, ts, bearer())
	bundles := distinctBundles(t, root, 4)
	ids := make([]string, 0, 3)
	for _, bundle := range bundles[:3] {
		ids = append(ids, createVerifyJobOn(t, ts, server, rootID, bundle))
	}
	testenv.Until(t, func() bool { return countLocked(&mu, &entered) == 3 })
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"`+bundles[3]+`"}`, writeHeaders(server))
	if got.status != http.StatusServiceUnavailable || !strings.Contains(string(got.body), `"reason":"job-capacity"`) {
		t.Fatalf("POST full history = %d %s, want 503 job-capacity", got.status, got.body)
	}
	close(release)
	for _, id := range ids {
		terminal := awaitJobTerminal(t, ts, id)
		if asString(t, terminal["status"]) != jobStatusSucceeded {
			t.Fatalf("job %s status = %v, want succeeded (%v)", id, terminal["status"], terminal)
		}
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a capacity refusal changed the tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// TestStopCancelsRunningJob proves stop cancels an actively running
// job and joins it: the runner observes cancellation and exits before
// stop returns. Mutation: dropping the cancel or Wait in stop leaves
// the runner blocked, so the exited check after stop returns fails.
func TestStopCancelsRunningJob(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	started := make(chan struct{})
	exited := make(chan struct{})
	var once sync.Once
	runner := VerifyRunner(func(ctx context.Context, _ *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
		once.Do(func() { close(started) })
		defer close(exited)
		<-ctx.Done()
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before verify completed"}
	})
	ts, server := interactiveJobServer(t, testLayer(root, nil), runner, okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	<-started
	server.jobs.stop()
	select {
	case <-exited:
	default:
		t.Fatal("the runner had not exited when stop returned; stop did not join")
	}
	current, ok := server.jobs.get(id)
	if !ok {
		t.Fatalf("job %s missing after stop", id)
	}
	if current.status != jobStatusCancelled {
		t.Fatalf("status after stop = %v, want canceled", current.status)
	}
	select {
	case <-current.done:
	default:
		t.Fatal("the job worker had not finished when stop returned")
	}
}

// TestJobCreateAfterStopRefused proves a late create after stop cannot
// race wg.Wait: it refuses cleanly with 503 and queues nothing.
func TestJobCreateAfterStopRefused(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	server.jobs.stop()
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234"}`, writeHeaders(server))
	if got.status != http.StatusServiceUnavailable || !strings.Contains(string(got.body), `"reason":"job-capacity"`) {
		t.Fatalf("POST after stop = %d %s, want 503 job-capacity", got.status, got.body)
	}
	if count := jobCount(server); count != 0 {
		t.Fatalf("retained jobs = %d, want 0 after a refused create", count)
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused create changed the tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// TestSlowSSEDoesNotBlock proves a subscriber that never reads does
// not stall publishJobs: publications return immediately while a
// healthy subscriber still receives each frame.
func TestSlowSSEDoesNotBlock(t *testing.T) {
	t.Parallel()
	broker := newEventBroker()
	slowID, slow := broker.subscribeJobs()
	defer broker.unsubscribeJobs(slowID)
	fastID, fast := broker.subscribeJobs()
	defer broker.unsubscribeJobs(fastID)
	start := time.Now()
	for i := range 10 {
		publishRound(t, broker, slow, fast, i)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("publishing with a slow subscriber took %s, want immediate returns", elapsed)
	}
	// The slow channel never drained, so publishes kept replacing its
	// single slot instead of blocking: it still holds a frame.
	assertSlowHeld(t, slow)
}
