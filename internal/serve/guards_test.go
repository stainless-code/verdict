package serve

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// Local writes are on by default, so every write route sits behind the same
// guards a rebinding page or a cross-site form would have to beat: a Host that
// names the listen address, an allowlisted Origin, the session token, the
// per-session CSRF token, and a JSON body. These tests drive every write route
// with each guard broken in turn; the refusal must come before any handler
// runs, any store is touched, or any runner is called.

// guardedServer is a writable server (host posting on, the widest surface)
// whose runners fail the test if a refused request ever reaches one.
func guardedServer(t *testing.T) (ts *httptest.Server, server *Server, root string, runs *atomic.Int32) {
	t.Helper()
	root = writeFixture(t, fixtureFiles())
	runs = new(atomic.Int32)
	server, err := New(&Config{
		Layer: testLayer(root, nil), Port: testPort, Token: testToken, Now: fixedNow, AllowPost: true,
		VerifyRunner: func(context.Context, *verifycore.Request) (*verifycore.Response, *core.RefusalError) {
			runs.Add(1)
			return nil, &core.RefusalError{Code: "test-assert", Message: "a refused request ran verify"}
		},
		PostPreviewRunner: func(context.Context, *postcore.Request) (*postcore.Response, *core.RefusalError) {
			runs.Add(1)
			return nil, &core.RefusalError{Code: "test-assert", Message: "a refused request ran a preview"}
		},
		PostSubmitRunner: func(context.Context, *postcore.Request) (*postcore.Response, *core.RefusalError) {
			runs.Add(1)
			return nil, &core.RefusalError{Code: "test-assert", Message: "a refused request posted"}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts = httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, server, root, runs
}

// guardCase breaks one guard. mutate edits the otherwise fully valid write
// request: host is the Host header, headers the rest.
type guardCase struct {
	name   string
	mutate func(host *string, headers map[string]string)
	reason string
	status int
}

func guardCases() []guardCase {
	setOrigin := func(origin string) func(*string, map[string]string) {
		return func(_ *string, h map[string]string) { h["Origin"] = origin }
	}
	setHost := func(value string) func(*string, map[string]string) {
		return func(host *string, _ map[string]string) { *host = value }
	}
	return []guardCase{
		{"foreign host", setHost("attacker.test:47123"), "bad-host", http.StatusForbidden},
		{"rebound host on another port", setHost("127.0.0.1:47124"), "bad-host", http.StatusForbidden},
		{"host without port", setHost("localhost"), "bad-host", http.StatusForbidden},
		{"wildcard host", setHost("0.0.0.0:47123"), "bad-host", http.StatusForbidden},
		{"trailing-dot host", setHost("localhost.:47123"), "bad-host", http.StatusForbidden},
		{"loopback name as a subdomain of a foreign name", setHost("localhost.attacker.test:47123"), "bad-host", http.StatusForbidden},
		{"foreign origin", setOrigin("https://evil.test"), "bad-origin", http.StatusForbidden},
		{"null origin", setOrigin("null"), "bad-origin", http.StatusForbidden},
		{"loopback origin on another port", setOrigin("http://127.0.0.1:47124"), "bad-origin", http.StatusForbidden},
		{"loopback name inside a foreign origin", setOrigin("http://127.0.0.1:47123.evil.test"), "bad-origin", http.StatusForbidden},
		{"https loopback origin", setOrigin("https://127.0.0.1:47123"), "bad-origin", http.StatusForbidden},
		{"absent origin", func(_ *string, h map[string]string) { delete(h, "Origin") }, "bad-origin", http.StatusForbidden},
		{"no credentials", func(_ *string, h map[string]string) { delete(h, "Authorization") }, "unauthorized", http.StatusUnauthorized},
		{"wrong bearer", func(_ *string, h map[string]string) {
			h["Authorization"] = "Bearer " + strings.Repeat("0", len(testToken))
		}, "unauthorized", http.StatusUnauthorized},
		{"token in the query", func(_ *string, h map[string]string) { h["X-Query"] = "?token=" + testToken }, "token-in-query", http.StatusBadRequest},
		{"no csrf token", func(_ *string, h map[string]string) { delete(h, csrfHeader) }, "bad-csrf", http.StatusForbidden},
		{"wrong csrf token", func(_ *string, h map[string]string) { h[csrfHeader] = strings.Repeat("0", len(testToken)) }, "bad-csrf", http.StatusForbidden},
		{"session token as csrf token", func(_ *string, h map[string]string) { h[csrfHeader] = testToken }, "bad-csrf", http.StatusForbidden},
		{"urlencoded form body", func(_ *string, h map[string]string) { h["Content-Type"] = "application/x-www-form-urlencoded" }, "bad-content-type", http.StatusBadRequest},
		{"multipart form body", func(_ *string, h map[string]string) { h["Content-Type"] = "multipart/form-data; boundary=x" }, "bad-content-type", http.StatusBadRequest},
		{"text/plain body", func(_ *string, h map[string]string) { h["Content-Type"] = "text/plain" }, "bad-content-type", http.StatusBadRequest},
		{"no content type", func(_ *string, h map[string]string) { delete(h, "Content-Type") }, "bad-content-type", http.StatusBadRequest},
	}
}

// guardedPost sends one write with the given Host and headers, a `{}` body, and
// the "token in the query" case spelled onto the URL.
func guardedPost(t *testing.T, ts *httptest.Server, path, host string, headers map[string]string) result {
	t.Helper()
	if query, ok := headers["X-Query"]; ok {
		path += query
		delete(headers, "X-Query")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{status: resp.StatusCode, header: resp.Header, body: body}
}

// TestWriteGuardsRefuseEveryBrokenGuardOnEveryRoute is the matrix: each of the
// six write routes against each broken guard. A refused request changes no
// file, queues no job, and calls no runner.
//
//nolint:revive // the route-by-guard matrix reads as one assertion block
func TestWriteGuardsRefuseEveryBrokenGuardOnEveryRoute(t *testing.T) {
	t.Parallel()
	ts, server, root, runs := guardedServer(t)
	before := snapshotState(t, root)
	for _, pattern := range writeRoutes {
		path := strings.ReplaceAll(pattern, "{id}", "abc")
		for _, tc := range guardCases() {
			t.Run(pattern+" "+tc.name, func(t *testing.T) {
				host := testHost
				headers := writeHeaders(server)
				tc.mutate(&host, headers)
				got := guardedPost(t, ts, path, host, headers)
				if got.status != tc.status || !strings.Contains(string(got.body), fmt.Sprintf(`"reason":%q`, tc.reason)) {
					t.Errorf("POST %s = %d %s, want %d %s", path, got.status, got.body, tc.status, tc.reason)
				}
			})
		}
	}
	if n := runs.Load(); n != 0 {
		t.Errorf("a refused write reached a runner %d time(s)", n)
	}
	assertNoJobs(t, server, root, before)
}

// TestWriteGuardsAcceptTheFullCredentialSet is the control for the matrix: the
// same request with every guard satisfied gets past all of them (it reaches its
// handler, so it is anything but a guard refusal).
//
//nolint:revive // the route-by-reason scan reads as one assertion block
func TestWriteGuardsAcceptTheFullCredentialSet(t *testing.T) {
	t.Parallel()
	ts, server, _, _ := guardedServer(t)
	guardReasons := []string{"bad-host", "bad-origin", "unauthorized", "token-in-query", "bad-csrf", "bad-content-type"}
	for _, pattern := range writeRoutes {
		path := strings.ReplaceAll(pattern, "{id}", "abc")
		got := guardedPost(t, ts, path, testHost, writeHeaders(server))
		for _, reason := range guardReasons {
			if strings.Contains(string(got.body), `"reason":"`+reason+`"`) {
				t.Errorf("POST %s with the full credential set was refused by a guard: %d %s", path, got.status, got.body)
			}
		}
		if got.status == http.StatusMethodNotAllowed {
			t.Errorf("POST %s = 405 on a writable server", path)
		}
	}
}

// TestReadRoutesRefuseForeignHostAndOrigin pins the Host and Origin gate on
// the read side too: a rebinding page must not read a session out of a route
// either, so the gate sits in front of the whole mux.
func TestReadRoutesRefuseForeignHostAndOrigin(t *testing.T) {
	t.Parallel()
	ts, _, _, _ := guardedServer(t)
	// /v1/events streams: without the gate a request would hang, not fail.
	ts.Client().Timeout = 5 * time.Second
	for _, path := range []string{"/", "/v1/health", "/v1/bundles", "/v1/jobs/abc", "/v1/events"} {
		if got := call(t, ts, http.MethodGet, path, "attacker.test:47123", bearer()); got.status != http.StatusForbidden ||
			!strings.Contains(string(got.body), `"reason":"bad-host"`) {
			t.Errorf("GET %s with a foreign Host = %d %s, want 403 bad-host", path, got.status, got.body)
		}
		headers := bearer()
		headers["Origin"] = "https://evil.test"
		if got := call(t, ts, http.MethodGet, path, "", headers); got.status != http.StatusForbidden ||
			!strings.Contains(string(got.body), `"reason":"bad-origin"`) {
			t.Errorf("GET %s with a foreign Origin = %d %s, want 403 bad-origin", path, got.status, got.body)
		}
	}
}

// TestWildcardBindAllowlistsOnlyLoopback pins what the manual promises: a
// wildcard bind (--host 0.0.0.0 or ::) never allowlists its own wildcard
// spelling, because a browser can reach http://0.0.0.0:<port>; only the
// loopback names (plus a concrete interface address) are accepted.
//
//nolint:revive // the per-bind table reads as one assertion block
func TestWildcardBindAllowlistsOnlyLoopback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		host   string
		accept []string
		refuse []string
	}{
		{"0.0.0.0", []string{"127.0.0.1:47123", "localhost:47123"}, []string{"0.0.0.0:47123"}},
		{"::", []string{"[::1]:47123"}, []string{"[::]:47123", ":::47123"}},
		{"192.168.1.10", []string{"192.168.1.10:47123", "127.0.0.1:47123"}, []string{"192.168.1.11:47123", "0.0.0.0:47123"}},
	} {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			server, err := New(&Config{Layer: testLayer(t.TempDir(), nil), Host: tc.host, Port: testPort, Token: testToken, Now: fixedNow})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for _, host := range tc.accept {
				assertAllowlisted(t, server, tc.host, host, true)
			}
			for _, host := range tc.refuse {
				assertAllowlisted(t, server, tc.host, host, false)
			}
		})
	}
}

// assertAllowlisted checks one spelling against both the Host and the Origin
// allowlist of a server bound to bind.
func assertAllowlisted(t *testing.T, server *Server, bind, spelling string, want bool) {
	t.Helper()
	if got := server.hostAllowed(spelling); got != want {
		t.Errorf("Host %q allowed = %v on a --host %s server, want %v", spelling, got, bind, want)
	}
	if got := server.originAllowed("http://" + spelling); got != want {
		t.Errorf("Origin http://%s allowed = %v on a --host %s server, want %v", spelling, got, bind, want)
	}
}
