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
	"strings"
	"sync"
	"testing"

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
		Port: testPort, Token: testToken, Now: fixedNow, Layer: layer, Interactive: true,
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

// createVerifyJob posts one verify job and returns its id.
func createVerifyJob(t *testing.T, ts *httptest.Server, server *Server, rootID string) string {
	t.Helper()
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234"}`, writeHeaders(server))
	if got.status != http.StatusAccepted {
		t.Fatalf("POST verify = %d, want 202 (%s)", got.status, got.body)
	}
	env := decode(t, got.body)
	if env["error"] != nil {
		t.Fatalf("error = %v, want null (%s)", env["error"], got.body)
	}
	return asString(t, asMap(t, env["data"])["id"])
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

// TestVerifyJobBadSig pins a malformed sig refusing before any job exists.
func TestVerifyJobBadSig(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	for _, sig := range []string{"ABCDEF12", "abc", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		before := snapshotState(t, root)
		got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234","sig":"`+sig+`"}`, hdrs)
		if got.status != http.StatusBadRequest || !strings.Contains(string(got.body), `"reason":"bad-sig"`) {
			t.Errorf("sig %q = %d %s, want 400 bad-sig", sig, got.status, got.body)
		}
		assertNoJobs(t, server, root, before)
	}
}

// TestVerifyJobUnknownSig pins a well-formed but absent sig refusing 404.
func TestVerifyJobUnknownSig(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234","sig":"`+strings.Repeat("f", 40)+`"}`, writeHeaders(server))
	if got.status != http.StatusNotFound || !strings.Contains(string(got.body), `"reason":"not-found"`) {
		t.Fatalf("unknown sig = %d %s, want 404 not-found", got.status, got.body)
	}
	assertNoJobs(t, server, root, before)
}

// TestVerifyJobWithheldRefused proves a policy-withheld sig never queues.
func TestVerifyJobWithheldRefused(t *testing.T) {
	t.Parallel()
	files := fixtureFiles()
	files[".verdict/state/1234/findings.json"] = "[" + withheldTriageFinding(sigCrit) + "]"
	root := writeFixture(t, files)
	ts, server := interactiveJobServer(t, testLayer(root, nil), okVerifyRunner(), okPostPreviewRunner())
	rootID := rootIDOf(t, ts, bearer())
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/jobs/verify", `{"root":"`+rootID+`","bundle":"1234","sig":"`+sigCrit+`"}`, writeHeaders(server))
	if got.status != http.StatusNotFound || !strings.Contains(string(got.body), `"reason":"not-found"`) {
		t.Fatalf("status = %d, want 404 not-found (%s)", got.status, got.body)
	}
	assertNoJobs(t, server, root, before)
	raw, err := os.ReadFile(filepath.Join(root, ".verdict/state/1234/findings.json"))
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	if strings.Contains(string(raw), "false-positive") {
		t.Errorf("findings = %s, want the withheld finding still open", raw)
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
	rootID := rootIDOf(t, ts, bearer())
	id := createVerifyJob(t, ts, server, rootID)
	<-started
	close(release)
	statuses := readJobEvents(t, stream, id)
	for _, want := range []string{jobStatusQueued, jobStatusRunning, jobStatusSucceeded} {
		found := slices.Contains(statuses, want)
		if !found {
			t.Errorf("SSE statuses = %v, missing %s", statuses, want)
		}
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

// readJobEvents drains the stream until one job reaches a terminal event.
func readJobEvents(t *testing.T, resp *http.Response, id string) []string {
	t.Helper()
	reader := bufio.NewReader(resp.Body)
	var statuses []string
	for range 40 {
		status := readOneJobStatus(t, reader, id, statuses)
		if status != "" {
			statuses = append(statuses, status)
			if isTerminal(status) {
				return statuses
			}
		}
	}
	t.Fatalf("job %s never reached a terminal SSE event (statuses %v)", id, statuses)
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
// store-busy without running the runner.
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
	results := make(chan string, jobs)
	wg.Add(jobs)
	for range jobs {
		go func() {
			defer wg.Done()
			<-start
			id := createVerifyJob(t, ts, server, rootID)
			idsMu.Lock()
			ids = append(ids, id)
			idsMu.Unlock()
			results <- id
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for id := range results {
		snapshot := awaitJobTerminal(t, ts, id)
		if asString(t, snapshot["status"]) != jobStatusSucceeded {
			t.Fatalf("job %s status = %v, want succeeded (%v)", id, snapshot["status"], snapshot)
		}
	}
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
	_ = jq.NewObj("jobs", len(ids))
}
