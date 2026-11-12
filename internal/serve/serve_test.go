package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/testenv"
)

const (
	headA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testPort   = 47123
	testToken  = "0123456789abcdef0123456789"
	testHost   = "127.0.0.1:47123"
	cookieName = "verdict_session_47123"

	sigCrit = "c111111111111111111111111111111111111111"
	sigImp  = "b222222222222222222222222222222222222222"
	sigAddr = "d444444444444444444444444444444444444444"
	sigOpen = "e555555555555555555555555555555555555555"
)

// fixedNow pins every composed timestamp.
func fixedNow() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }

// finding renders one store entry.
func finding(sig, status, severity, rigor string) string {
	return fmt.Sprintf(`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		rigor, severity, sig, status)
}

// prDoc renders a GitHub-shaped PR.json.
func prDoc() string {
	return fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,`+
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/1234"}`,
		headA, headB)
}

// postDoc renders a completed post receipt.
func postDoc(head string, id int) string {
	return fmt.Sprintf(`{"schema_version":"1","review":{"id":%d},"head":%q,"model":"m1",`+
		`"posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`, id, head)
}

// guardRefusedBundleNames are the directories the shared guard refuses: the
// 18 names the old route regex admitted while the CLI refused them.
var guardRefusedBundleNames = []string{
	"NUL", "nul", "NuL", "nul.txt", "CON", "con.json", "PRN", "AUX",
	"COM1", "com9", "LPT1", "lpt9", "123.", "1234.", "a.", "1234..",
	"com1.txt", "1.",
}

// fixtureFiles is the two-bundle root every view test reads.
func fixtureFiles() map[string]string {
	return map[string]string{
		".verdict/state/1234/PR.json":   prDoc(),
		".verdict/state/1234/post.json": postDoc(headA, 7),
		".verdict/state/1234/findings.json": "[" + strings.Join([]string{
			finding(sigCrit, "open", "critical", "walked"),
			finding(sigImp, "open", "important", "pointed"),
			finding(sigAddr, "addressed", "important", "walked"),
		}, ",") + "]",
		".verdict/state/local-1/source.json": `{"schema_version":"1","kind":"range",` +
			`"repo":"acme/app","slug":"work","head_ref":"v2","base_sha":"` + headB + `",` +
			`"head_sha":"` + headA + `","dirty":false}`,
		".verdict/state/local-1/post.json": postDoc(headA, 8),
		".verdict/state/local-1/findings.json": "[" +
			finding(sigOpen, "open", "critical", "said") + "]",
	}
}

// writeFixture writes one root's bundle files and returns the root.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureAt(t, dir, files)
	return dir
}

// writeFixtureAt writes bundle files under dir.
func writeFixtureAt(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// testLayer builds the real query layer over dir with a fixed clock and the
// injected commander.
func testLayer(dir string, commander core.Commander) *query.Layer {
	return query.New(query.Snapshot{
		Version: "1.2.3",
		Roots:   []query.Root{{Path: dir}},
		Runtime: &core.Runtime{Now: fixedNow, Commander: commander},
	}, query.Options{Check: "critical", MaxBundles: 10})
}

// mustAbs makes dir absolute the way the layer's constructor does.
func mustAbs(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// testServer builds the handler over a layer.
func testServer(t *testing.T, layer Layer) *httptest.Server {
	t.Helper()
	server, err := New(&Config{Port: testPort, Token: testToken, Now: fixedNow, Layer: layer})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	t.Cleanup(ts.Close)
	return ts
}

// TestAggregateRoutes checks aggregate responses and query refusals.
func TestAggregateRoutes(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	assertAggregateRoutesSucceed(t, ts)
	assertAggregateRoutesRefuseUnknownDimensions(t, ts)
}

func assertAggregateRoutesSucceed(t *testing.T, ts *httptest.Server) {
	t.Helper()
	for _, path := range []string{"/v1/cost?by=repo", "/v1/precision?by=category"} {
		got := call(t, ts, http.MethodGet, path, "", bearer())
		if got.status != http.StatusOK || !strings.Contains(string(got.body), `"error": null`) {
			t.Errorf("GET %s = %d %s; want 200 success envelope", path, got.status, got.body)
		}
	}
}

func assertAggregateRoutesRefuseUnknownDimensions(t *testing.T, ts *httptest.Server) {
	t.Helper()
	for _, path := range []string{
		"/v1/cost?by=unexpected", "/v1/precision?by=unexpected",
		"/v1/cost?by=repo&unexpected=1", "/v1/precision?by=category&by=tier",
	} {
		got := call(t, ts, http.MethodGet, path, "", bearer())
		if got.status != http.StatusOK || !strings.Contains(string(got.body), `"code": "bad-by"`) {
			t.Errorf("GET %s = %d %s; want 200 typed bad-by refusal", path, got.status, got.body)
		}
	}
}

// result is one closed response: status, headers, and body.
type result struct { //nolint:govet // test result grouping
	status int
	header http.Header
	body   []byte
}

// call performs one request with the test Host and headers, closing the body
// before returning.
func call(
	t *testing.T, ts *httptest.Server, method, path, host string, headers map[string]string,
) result {
	t.Helper()
	if host == "" {
		host = testHost
	}
	req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, nil)
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

// asMap asserts one decoded object.
func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value %v is not an object", value)
	}
	return out
}

// asList asserts one decoded array.
func asList(t *testing.T, value any) []any {
	t.Helper()
	out, ok := value.([]any)
	if !ok {
		t.Fatalf("value %v is not an array", value)
	}
	return out
}

// asString asserts one decoded string.
func asString(t *testing.T, value any) string {
	t.Helper()
	out, ok := value.(string)
	if !ok {
		t.Fatalf("value %v is not a string", value)
	}
	return out
}

// bearer is the header one authorized API call carries.
func bearer() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testToken}
}

// decode decodes one JSON body with number fidelity.
func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// wire decodes one ordered object with number fidelity.
func wire(t *testing.T, obj *jq.Obj) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(obj.MarshalCompact()))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	return out
}

// TestListenBindsLoopbackOnAKernelPort proves the default bind, the resolved
// port, the tokenised URL, and the per-port cookie name.
func TestListenBindsLoopbackOnAKernelPort(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{Root: root, Port: 0, Now: fixedNow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := server.Listen(t.Context()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	assertListen(t, server)
}

// assertListen pins the resolved address, token, URL, and cookie name.
func assertListen(t *testing.T, server *Server) {
	t.Helper()
	host, port, err := net.SplitHostPort(server.Addr())
	if err != nil {
		t.Fatalf("Addr %q: %v", server.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Errorf("bound host = %q, want 127.0.0.1", host)
	}
	if port == "0" || port == "" {
		t.Errorf("bound port = %q, want a kernel-assigned port", port)
	}
	if len(server.token) < 20 {
		t.Errorf("token = %q, want a 128-bit capability", server.token)
	}
	wantURL := "http://" + net.JoinHostPort(host, port) + "/?token=" + server.token
	if server.URL() != wantURL {
		t.Errorf("URL = %q, want %q", server.URL(), wantURL)
	}
	if got := server.cookieName(); got != "verdict_session_"+port {
		t.Errorf("cookie name = %q, want verdict_session_%s", got, port)
	}
}

// TestStartURLEscapesAndExchangesTheToken proves a token with query
// metacharacters survives the printed URL and the session cookie.
func TestStartURLEscapesAndExchangesTheToken(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{
		Port: testPort, Token: "a&b=c", Now: fixedNow, Layer: testLayer(root, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := server.URL(), "http://127.0.0.1:47123/?token=a%26b%3Dc"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	t.Cleanup(ts.Close)
	exchange := call(t, ts, http.MethodGet, "/?token="+url.QueryEscape("a&b=c"), "", nil)
	if exchange.status != http.StatusSeeOther {
		t.Fatalf("exchange = %d, want 303 (%s)", exchange.status, exchange.body)
	}
	health := call(t, ts, http.MethodGet, "/v1/health", "",
		map[string]string{"Cookie": cookieName + "=a&b=c"})
	if health.status != http.StatusOK {
		t.Fatalf("health with the cookie = %d, want 200 (%s)", health.status, health.body)
	}
}

// TestTokenValueMustSurviveTheCookie proves a configured token net/http would
// rewrite is refused before the listener starts.
func TestTokenValueMustSurviveTheCookie(t *testing.T) {
	t.Parallel()
	for _, token := range []string{`a;b`, `a"b`, `a\b`} {
		t.Run(token, func(t *testing.T) {
			out := &syncBuffer{}
			runner := &cli.Runner{
				Cmd: "serve", Out: out, Human: out, Diag: out,
				Now: fixedNow, Effective: cli.Defaults(),
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if code := Run(ctx, runner, []string{"--token", token}, Request{}); code != cli.ExitUsage {
				t.Fatalf("exit = %d, want %d for token %q", code, cli.ExitUsage, token)
			}
		})
	}
}

// TestHTTPServerTimeoutsAreBounded pins finite read and idle deadlines.
func TestHTTPServerTimeoutsAreBounded(t *testing.T) {
	t.Parallel()
	server := newHTTPServer(http.NotFoundHandler())
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", server.ReadTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", server.IdleTimeout)
	}
}

// TestCanceledProbeExitsBroke proves a cancel during the repository probe
// keeps the documented exit 3 instead of turning into not-a-repo.
func TestCanceledProbeExitsBroke(t *testing.T) {
	t.Chdir(t.TempDir())
	out := &syncBuffer{}
	runner := &cli.Runner{
		Cmd: "serve", Out: out, Human: out, Diag: out, JSONMode: true,
		Now: fixedNow, Effective: cli.Defaults(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := Run(ctx, runner, nil, Request{}); code != cli.ExitBroke {
		t.Fatalf("exit = %d, want %d", code, cli.ExitBroke)
	}
	if got := asMap(t, decode(t, []byte(out.String()))["error"])["code"]; got != "canceled" {
		t.Errorf("error.code = %v, want canceled", got)
	}
}

// TestNonLoopbackWarningNamesPlainHTTP pins the exposure warning, including
// the unencrypted token and cookie.
func TestNonLoopbackWarningNamesPlainHTTP(t *testing.T) {
	t.Parallel()
	out := &syncBuffer{}
	runner := &cli.Runner{Out: out, Human: out, Diag: out, Effective: cli.Defaults()}
	warnExposure(runner, &serveOptions{host: "0.0.0.0"})
	const want = "serve: binding 0.0.0.0 exposes the read-only surface beyond loopback " +
		"over plain HTTP; the token and session cookie are not encrypted"
	if !slices.Contains(runner.Warnings, want) {
		t.Errorf("warnings = %v, want %q", runner.Warnings, want)
	}
}

// TestRouteTableIsGetOnly walks the one registration point: a non-GET row
// must fail the table's contract.
func TestRouteTableIsGetOnly(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{Root: root, Port: testPort, Token: testToken, Now: fixedNow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, item := range server.routes() {
		if item.Method != http.MethodGet {
			t.Errorf("route %s %s is not GET", item.Method, item.Pattern)
		}
	}
}

// syncBuffer is a goroutine-safe writer for the in-process Run test.
type syncBuffer struct { //nolint:govet // mutex guards the builder beside it
	mu sync.Mutex
	sb strings.Builder
}

// Write appends one chunk.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

// String reads the accumulated text.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

var urlPattern = regexp.MustCompile(`http://[^\s]+`)

// TestRunAnnouncesTokenAndExitsBroke proves the CLI adapter binds a kernel
// port, prints the tokenised URL, serves the API, and exits 3 on cancel.
func TestRunAnnouncesTokenAndExitsBroke(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := writeFixture(t, fixtureFiles())
	out := &syncBuffer{}
	cancel, done := startRun(t, root, out)
	match := waitForURL(t, out)
	assertLoopbackHealth(t, match)
	cancel()
	assertCanceled(t, done)
}

// TestServeRootDiscoversEveryRepository serves repositories found below --root.
func TestServeRootDiscoversEveryRepository(t *testing.T) {
	assertServeRootInbox(t, serveRootInbox(t))
}

func serveRootInbox(t *testing.T) map[string]any {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	for _, name := range []string{"a", "b"} {
		writeFixtureAt(t, filepath.Join(workspace, name), fixtureFiles())
	}
	out := &syncBuffer{}
	cancel, done := startRun(t, workspace, out)
	startURL := waitForURL(t, out)
	data := fetchServeInbox(t, startURL)
	cancel()
	assertCanceled(t, done)
	return data
}

func fetchServeInbox(t *testing.T, startURL string) map[string]any {
	t.Helper()
	parsed, err := url.Parse(startURL)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+parsed.Host+"/v1/bundles", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = parsed.Host
	request.Header.Set("Authorization", "Bearer tok")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/bundles = %d (%s)", response.StatusCode, body)
	}
	return asMap(t, decode(t, body)["data"])
}

func assertServeRootInbox(t *testing.T, data map[string]any) {
	t.Helper()
	rows := asList(t, data["rows"])
	if len(rows) != 4 {
		t.Fatalf("inbox rows = %d, want four bundles across two roots", len(rows))
	}
	counts := asMap(t, data["counts"])
	wantCounts := map[string]json.Number{
		"bundles": json.Number("4"), "open": json.Number("6"), "deferred": json.Number("0"),
		"addressed": json.Number("2"), "terminal": json.Number("0"),
	}
	if !maps.Equal(counts, map[string]any{
		"bundles": wantCounts["bundles"], "open": wantCounts["open"], "deferred": wantCounts["deferred"],
		"addressed": wantCounts["addressed"], "terminal": wantCounts["terminal"],
	}) {
		t.Errorf("inbox counts = %v, want literal %v", counts, wantCounts)
	}
	if asMap(t, data["discovery"])["cached"] != false {
		t.Errorf("discovery = %v, want uncached initial scan", data["discovery"])
	}
}

// startRun launches the adapter in the background.
func startRun(t *testing.T, root string, out *syncBuffer) (context.CancelFunc, <-chan int) {
	t.Helper()
	runner := &cli.Runner{
		Cmd: "serve", Out: out, Human: out, Diag: out,
		Now: fixedNow, Effective: cli.Defaults(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, runner, []string{"--root", root, "--port", "0", "--token", "tok"}, Request{})
	}()
	return cancel, done
}

// waitForURL reads the printed start URL once the listener announces it.
func waitForURL(t *testing.T, out *syncBuffer) string {
	t.Helper()
	testenv.Until(t, func() bool { return strings.Contains(out.String(), "?token=tok") })
	match := urlPattern.FindString(out.String())
	if match == "" {
		t.Fatalf("start line carries no URL:\n%s", out.String())
	}
	return match
}

// assertLoopbackHealth calls the printed URL with the fixed bearer token.
func assertLoopbackHealth(t *testing.T, match string) {
	t.Helper()
	parsed, err := url.Parse(match)
	if err != nil {
		t.Fatalf("parse start URL %q: %v", match, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, match, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = parsed.Host
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
}

// assertCanceled pins the exit-3 contract after cancel.
func assertCanceled(t *testing.T, done <-chan int) {
	t.Helper()
	select {
	case code := <-done:
		if code != cli.ExitBroke {
			t.Errorf("exit = %d, want %d after cancel", code, cli.ExitBroke)
		}
	case <-t.Context().Done():
		t.Fatal("serve did not stop after cancel")
	}
}

// snapshotState captures one tree's path -> size/mtime/mode map.
func snapshotState(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return fmt.Errorf("rel %s: %w", path, rerr)
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%d/%d/%s", info.Size(), info.ModTime().UnixNano(), info.Mode())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestReadsWriteNothing snapshots the fixture tree before and after a full
// route sweep. Removing the offline posture would stage .recheck.err and move
// a directory stamp.
func TestReadsWriteNothing(t *testing.T) {
	t.Parallel()
	files := fixtureFiles()
	files[".verdict/state/1234/threads.json"] = `{"schema_version":"1","provider":"github",` +
		`"pr":1234,"head_sha":"` + headA + `","fetched_at":"2026-01-01T00:00:00Z",` +
		`"truncated":false,"threads":[]}`
	root := writeFixture(t, files)
	before := snapshotState(t, root)
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	call(t, ts, http.MethodGet, "/", "", nil)
	call(t, ts, http.MethodGet, "/?token="+testToken, "", nil)
	rootID := rootIDOf(t, ts, headers)
	call(t, ts, http.MethodGet, "/v1/health", "", headers)
	call(t, ts, http.MethodGet, "/v1/roots", "", headers)
	call(t, ts, http.MethodGet, "/v1/bundles", "", headers)
	call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/1234", "", headers)
	call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/local-1", "", headers)
	call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/1234/findings/"+sigCrit[:4], "", headers)
	after := snapshotState(t, root)
	if !maps.Equal(before, after) {
		t.Fatalf("a read changed the fixture tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// rootIDOf reads the one served root id from /v1/roots.
func rootIDOf(t *testing.T, ts *httptest.Server, headers map[string]string) string {
	t.Helper()
	resp := call(t, ts, http.MethodGet, "/v1/roots", "", headers)
	if resp.status != http.StatusOK {
		t.Fatalf("GET roots = %d (%s)", resp.status, resp.body)
	}
	data := asMap(t, decode(t, resp.body)["data"])
	roots := asList(t, data["roots"])
	if len(roots) != 1 {
		t.Fatalf("roots = %v, want one", roots)
	}
	return asString(t, asMap(t, roots[0])["id"])
}

// failingCommander fails the test the moment a host read is attempted.
type failingCommander struct{ t *testing.T }

// Run records the forbidden call and returns a synthetic failure.
func (c failingCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.t.Errorf("commander called with %v; serve must never shell a host", cmd.Argv)
	return core.Result{ExitCode: 1}, errors.New("no host calls") //nolint:err113 // test-only commander
}

// TestNoCommanderCalls sweeps every view with a commander that fails the test
// on any invocation.
func TestNoCommanderCalls(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, failingCommander{t: t}))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	for _, path := range []string{
		"/v1/health",
		"/v1/roots",
		"/v1/bundles",
		"/v1/roots/" + rootID + "/bundles/1234",
		"/v1/roots/" + rootID + "/bundles/local-1",
		"/v1/roots/" + rootID + "/bundles/1234/findings/" + sigCrit[:4],
	} {
		if got := call(t, ts, http.MethodGet, path, "", headers); got.status != http.StatusOK {
			t.Errorf("GET %s = %d (%s)", path, got.status, got.body)
		}
	}
}

// TestCorruptStoreIsNullNotZero proves INV-1 on the wire: the listing row
// keeps null counts, the portfolio is null and partial, and the detail is a
// 200 refusal envelope.
func TestCorruptStoreIsNullNotZero(t *testing.T) {
	t.Parallel()
	files := fixtureFiles()
	files[".verdict/state/local-1/findings.json"] = "{"
	root := writeFixture(t, files)
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	got := call(t, ts, http.MethodGet, "/v1/bundles", "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("GET bundles = %d", got.status)
	}
	assertCorruptListing(t, asMap(t, decode(t, got.body)["data"]))
	assertCorruptDetail(t, ts, rootID, headers)
}

// assertCorruptListing pins null counts and the one error row.
func assertCorruptListing(t *testing.T, data map[string]any) {
	t.Helper()
	if data["counts"] != nil {
		t.Errorf("portfolio counts = %v, want null", data["counts"])
	}
	if data["partial"] != true {
		t.Errorf("partial = %v, want true", data["partial"])
	}
	assertNullCounts(t, asMap(t, asList(t, data["rows"])[1]))
	assertCorruptError(t, asList(t, data["errors"]))
}

// assertNullCounts proves every numeric count is null, never zero.
func assertNullCounts(t *testing.T, row map[string]any) {
	t.Helper()
	for _, key := range []string{"open", "deferred", "addressed", "terminal"} {
		if row[key] != nil {
			t.Errorf("corrupt row %s = %v, want null", key, row[key])
		}
	}
}

// assertCorruptError pins the one tolerated failure row.
func assertCorruptError(t *testing.T, errs []any) {
	t.Helper()
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	row := asMap(t, errs[0])
	if row["code"] != "corrupt-store" || row["bundle"] != "local-1" {
		t.Errorf("error row = %v, want corrupt-store naming local-1", row)
	}
}

// assertCorruptDetail pins the 200 refusal envelope.
func assertCorruptDetail(t *testing.T, ts *httptest.Server, rootID string, headers map[string]string) {
	t.Helper()
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/local-1", "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("corrupt detail status = %d, want 200", got.status)
	}
	envelope := decode(t, got.body)
	if envelope["data"] != nil {
		t.Errorf("corrupt detail data = %v, want null", envelope["data"])
	}
	if asMap(t, envelope["error"])["code"] != "corrupt-store" {
		t.Errorf("corrupt detail error = %v, want corrupt-store", envelope["error"])
	}
}

// TestBundleAmbiguousIsA200Refusal proves a bare name that resolves to a
// local sibling refuses instead of reading the sibling.
func TestBundleAmbiguousIsA200Refusal(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, map[string]string{
		".verdict/state/1234/findings.json": "[" +
			finding(sigCrit, "open", "critical", "walked") + "]",
		".verdict/state/local-1234/findings.json": "[" +
			finding(sigOpen, "open", "critical", "said") + "]",
	})
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/1234", "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("ambiguous detail status = %d, want 200", got.status)
	}
	envelope := decode(t, got.body)
	if envelope["data"] != nil {
		t.Errorf("ambiguous data = %v, want null", envelope["data"])
	}
	if asMap(t, envelope["error"])["code"] != "bundle-ambiguous" {
		t.Errorf("ambiguous error = %v, want bundle-ambiguous", envelope["error"])
	}
}

// TestGuardRefusedBundlesAreRefusals proves the route and the CLI answer the
// shared bundle-name guard: a directory it refuses never appears in the inbox,
// and addressing it returns the same bad-flags refusal the CLI renders.
func TestGuardRefusedBundlesAreRefusals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot create a DOS-device or trailing-dot directory; the shared guard is tested on every OS by internal/config")
	}
	t.Parallel()
	files := map[string]string{}
	for _, name := range guardRefusedBundleNames {
		base := ".verdict/state/" + name + "/"
		files[base+"PR.json"] = prDoc()
		files[base+"findings.json"] = "[" + finding(sigCrit, "open", "critical", "walked") + "]"
	}
	root := writeFixture(t, files)
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	assertGuardRefusedListing(t, ts, headers)
	for _, name := range guardRefusedBundleNames {
		t.Run(name, func(t *testing.T) {
			assertGuardRefusedDetail(t, ts, rootID, headers, name)
		})
	}
}

// assertGuardRefusedListing proves the refused directories never become rows.
func assertGuardRefusedListing(t *testing.T, ts *httptest.Server, headers map[string]string) {
	t.Helper()
	inbox := call(t, ts, http.MethodGet, "/v1/bundles", "", headers)
	if inbox.status != http.StatusOK {
		t.Fatalf("GET bundles = %d (%s)", inbox.status, inbox.body)
	}
	data := asMap(t, decode(t, inbox.body)["data"])
	if rows := asList(t, data["rows"]); len(rows) != 0 {
		t.Errorf("rows = %v, want no row for a name no verb can address", rows)
	}
	if data["partial"] != false {
		t.Errorf("partial = %v, want false", data["partial"])
	}
}

// assertGuardRefusedDetail proves the route returns the CLI's own refusal.
func assertGuardRefusedDetail(t *testing.T, ts *httptest.Server, rootID string, headers map[string]string, name string) {
	t.Helper()
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/"+url.PathEscape(name), "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("detail status = %d, want 200 (%s)", got.status, got.body)
	}
	envelope := decode(t, got.body)
	if envelope["data"] != nil {
		t.Errorf("data = %v, want null", envelope["data"])
	}
	if code := asMap(t, envelope["error"])["code"]; code != "bad-flags" {
		t.Errorf("error.code = %v, want bad-flags (the CLI's own refusal)", code)
	}
}

// TestLongBundleNameIsAddressable proves the route's old 128-byte bound did
// not hide a directory the CLI can address.
func TestLongBundleNameIsAddressable(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("a", 140)
	files := fixtureFiles()
	base := ".verdict/state/" + longName + "/"
	files[base+"PR.json"] = prDoc()
	files[base+"findings.json"] = "[" + finding(sigCrit, "open", "critical", "walked") + "]"
	root := writeFixture(t, files)
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/"+longName, "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("detail status = %d, want 200 (%s)", got.status, got.body)
	}
	if envelope := decode(t, got.body); envelope["data"] == nil {
		t.Errorf("long-name detail data = null, want the report")
	}
}

// TestDetailCarriesTheLedgerAndThreads proves the composed detail carries the
// documented keys and a missing thread feed is absence, not an error.
func TestDetailCarriesTheLedgerAndThreads(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	if inbox := call(t, ts, http.MethodGet, "/v1/bundles", "", headers); inbox.status != http.StatusOK {
		t.Fatalf("inbox = %d", inbox.status)
	}
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/1234", "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("detail = %d", got.status)
	}
	data := asMap(t, decode(t, got.body)["data"])
	assertDetailKeys(t, data)
	assertDetailReport(t, data)
}

// TestDiscoveryMetadataIsServedForRootsAndInbox pins metadata on both views.
func TestDiscoveryMetadataIsServedForRootsAndInbox(t *testing.T) {
	root := writeFixture(t, fixtureFiles())
	discovery := query.Discovery{Cached: true, ScannedAt: fixedNow()}
	layer := query.New(query.Snapshot{
		Version: "1.2.3", Roots: []query.Root{{Path: root}},
		Runtime: &core.Runtime{Now: fixedNow}, Discovery: discovery,
	}, query.Options{Check: "critical", MaxBundles: 10})
	ts := testServer(t, layer)
	headers := bearer()
	for _, route := range []string{"/v1/roots", "/v1/bundles"} {
		response := call(t, ts, http.MethodGet, route, "", headers)
		if response.status != http.StatusOK {
			t.Fatalf("GET %s = %d", route, response.status)
		}
		data := asMap(t, decode(t, response.body)["data"])
		metadata := asMap(t, data["discovery"])
		if metadata["cached"] != true || metadata["scanned_at"] != "2026-11-12T09:00:00Z" {
			t.Errorf("%s discovery = %v, want cached at the literal scan time", route, metadata)
		}
	}
}

// TestDiscoveryFlagsParseAndValidate pins the discovery flag bounds.
func TestDiscoveryFlagsParseAndValidate(t *testing.T) {
	out := &syncBuffer{}
	runner := &cli.Runner{Cmd: "serve", Out: out, Human: out, Diag: out, Effective: cli.Defaults()}
	opts, code, done := parseFlags(runner, []string{
		"--root", "~/work", "--depth", "0", "--max-visits", "17", "--max-repos", "4",
		"--rediscover", "--no-registry",
	})
	if done || code != 0 || opts.root != "~/work" || opts.depth != 0 || opts.maxVisits != 17 || opts.maxRepos != 4 || !opts.rediscover || !opts.noRegistry {
		t.Fatalf("parsed discovery flags = %+v, code=%d, done=%v", opts, code, done)
	}
	_, code, done = parseFlags(runner, []string{"--depth", "17"})
	if !done || code != cli.ExitUsage {
		t.Errorf("--depth 17 = code %d, done %v, want usage refusal", code, done)
	}
}

// assertDetailKeys pins the documented data object's key set.
func assertDetailKeys(t *testing.T, data map[string]any) {
	t.Helper()
	for _, key := range []string{"report", "gate", "threads", "artifacts", "sources", "errors", "partial"} {
		if _, ok := data[key]; !ok {
			t.Errorf("detail data lacks %q", key)
		}
	}
	if data["threads"] != nil {
		t.Errorf("threads = %v, want null (no feed)", data["threads"])
	}
	if data["partial"] != false {
		t.Errorf("partial = %v, want false", data["partial"])
	}
	if data["gate"] == nil {
		t.Error("data.gate = null after an inbox read; the detail needs its own composition")
	}
}

// assertDetailReport pins the report's portable path and the inventory.
func assertDetailReport(t *testing.T, data map[string]any) {
	t.Helper()
	report := asMap(t, data["report"])
	if report["dir"] != ".verdict/state/1234" {
		t.Errorf("data.report.dir = %v, want the root-relative bundle path", report["dir"])
	}
	if rows := asList(t, data["sources"]); len(rows) == 0 {
		t.Errorf("sources = %v, want the bundle inventory", data["sources"])
	}
}

// TestShowDataIsTheShowShape proves the show route publishes item and fix.
func TestShowDataIsTheShowShape(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	got := call(t, ts, http.MethodGet,
		"/v1/roots/"+rootID+"/bundles/1234/findings/"+sigCrit[:4], "", headers)
	if got.status != http.StatusOK {
		t.Fatalf("show = %d (%s)", got.status, got.body)
	}
	envelope := decode(t, got.body)
	if envelope["verb"] != "show" {
		t.Errorf("verb = %v, want show", envelope["verb"])
	}
	data := asMap(t, envelope["data"])
	item := asMap(t, data["item"])
	if item["sig"] != sigCrit {
		t.Errorf("item.sig = %v, want %s", item["sig"], sigCrit)
	}
	if _, ok := data["fix"]; !ok {
		t.Error("show data lacks fix")
	}
}

// TestRootSkipsTheRepoProbe proves --root is resolved before the repository
// probe: a failing git in a checkout-shaped cwd would refuse not-a-repo, but
// the explicit root reaches the listener instead.
func TestRootSkipsTheRepoProbe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stub := t.TempDir()
	gitStub := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(stub, "git"), []byte(gitStub), 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := writeFixture(t, fixtureFiles())
	out := &syncBuffer{}
	runner := &cli.Runner{
		Cmd: "serve", Out: out, Human: out, Diag: out, JSONMode: true,
		Now: fixedNow, Effective: cli.Defaults(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code := Run(ctx, runner, []string{"--root", root}, Request{})
	if code != cli.ExitBroke {
		t.Fatalf("exit = %d, want %d (a not-a-repo refusal means the root lost the ordering)", code, cli.ExitBroke)
	}
	envelope := decode(t, []byte(out.String()))
	if envelope["error"] != nil {
		t.Fatalf("error = %v, want the start envelope", envelope["error"])
	}
	data := asMap(t, envelope["data"])
	if !strings.Contains(asString(t, data["url"]), "?token=") {
		t.Errorf("url = %v, want the tokenised start URL", data["url"])
	}
}

// TestBadRootIsRefused proves a missing --root is the pre-flight bad-root
// refusal, never a listener over a directory that is not there.
func TestBadRootIsRefused(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	out := &syncBuffer{}
	runner := &cli.Runner{
		Cmd: "serve", Out: out, Human: out, Diag: out, JSONMode: true,
		Now: fixedNow, Effective: cli.Defaults(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code := Run(ctx, runner, []string{"--root", missing}, Request{})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if got := asMap(t, decode(t, []byte(out.String()))["error"])["code"]; got != "bad-root" {
		t.Errorf("error.code = %v, want bad-root", got)
	}
}

// TestListenFailureIsRefused proves an unusable port is serve-listen-failed.
func TestListenFailureIsRefused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var listen net.ListenConfig
	held, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	_, port, err := net.SplitHostPort(held.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	root := writeFixture(t, fixtureFiles())
	out := &syncBuffer{}
	runner := &cli.Runner{
		Cmd: "serve", Out: out, Human: out, Diag: out, JSONMode: true,
		Now: fixedNow, Effective: cli.Defaults(),
	}
	code := Run(context.Background(), runner, []string{"--root", root, "--port", port}, Request{})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if got := asMap(t, decode(t, []byte(out.String()))["error"])["code"]; got != "serve-listen-failed" {
		t.Errorf("error.code = %v, want serve-listen-failed", got)
	}
}

// TestServeExitKeepsTheCancelCode proves the exit mapping: a canceled root
// context leaves 3 even when the drain itself failed.
func TestServeExitKeepsTheCancelCode(t *testing.T) {
	t.Parallel()
	shutdownErr := errors.New("serve: shutdown: context deadline exceeded") //nolint:err113 // test-only error value
	for _, tc := range []struct {
		err      error
		name     string
		want     int
		canceled bool
	}{
		{name: "clean", want: cli.ExitOK},
		{name: "canceled", want: cli.ExitBroke, canceled: true},
		{name: "failed drain", err: shutdownErr, want: cli.ExitRefused},
		{name: "canceled failed drain", err: shutdownErr, want: cli.ExitBroke, canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := serveExit(contextOf(tc.canceled), tc.err); got != tc.want {
				t.Errorf("serveExit(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// contextOf is a live context, or one already canceled.
func contextOf(canceled bool) context.Context {
	if !canceled {
		return context.Background()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// blockingLayer holds one health request open until its context ends.
type blockingLayer struct {
	entered chan struct{}
	ended   chan struct{}
}

// newBlockingLayer builds the layer with both of its signals.
func newBlockingLayer() *blockingLayer {
	return &blockingLayer{entered: make(chan struct{}, 1), ended: make(chan struct{})}
}

// Health blocks until the request's own context ends.
func (l *blockingLayer) Health(ctx context.Context) *query.View {
	l.entered <- struct{}{}
	<-ctx.Done()
	close(l.ended)
	return &query.View{Data: jq.NewObj("ok", true)}
}

// Roots renders an empty view.
func (l *blockingLayer) Roots(context.Context) *query.View {
	return &query.View{Data: jq.NewObj()}
}

// Bundles renders an empty view.
func (l *blockingLayer) Bundles(context.Context, string) (*query.View, error) {
	return &query.View{Data: jq.NewObj()}, nil
}

// Detail reports the bundle missing.
func (l *blockingLayer) Detail(context.Context, string, string) (*query.View, error) {
	return nil, query.ErrNotFound
}

// Show reports the finding missing.
func (l *blockingLayer) Show(context.Context, string, string, string) (*query.View, error) {
	return nil, query.ErrNotFound
}

func (l *blockingLayer) Cost(context.Context, string) (*query.View, error) {
	return nil, query.ErrNotFound
}

func (l *blockingLayer) Precision(context.Context, string) (*query.View, error) {
	return nil, query.ErrNotFound
}

// TestShutdownExpiryClosesActiveConnections proves a drain that outlasts its
// bound terminates the in-flight request, so Serve returns instead of leaving
// the handler running behind a closed listener.
func TestShutdownExpiryClosesActiveConnections(t *testing.T) {
	t.Parallel()
	layer := newBlockingLayer()
	server, err := New(&Config{Layer: layer, Token: testToken, Now: fixedNow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server.drainTimeout = 20 * time.Millisecond
	if err := server.Listen(t.Context()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()
	getHealth(t, server.Addr(), testToken)
	<-layer.entered
	cancel()
	serveErr := <-served
	if serveErr == nil || !strings.Contains(serveErr.Error(), "serve: shutdown") {
		t.Fatalf("Serve = %v, want the drained shutdown error", serveErr)
	}
	<-layer.ended
	if got := serveExit(ctx, serveErr); got != cli.ExitBroke {
		t.Errorf("exit after the failed drain = %d, want %d", got, cli.ExitBroke)
	}
}

// getHealth fires one authenticated health request and drops the outcome: the
// server closes it when the drain expires.
func getHealth(t *testing.T, addr, token string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	go func() {
		resp, derr := (&http.Client{}).Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		_ = derr
	}()
}
