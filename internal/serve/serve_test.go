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
func prDoc(head string) string {
	return fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,`+
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/1234"}`,
		head, headB)
}

// postDoc renders a completed post receipt.
func postDoc(head string, id int) string {
	return fmt.Sprintf(`{"schema_version":"1","review":{"id":%d},"head":%q,"model":"m1",`+
		`"posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`, id, head)
}

// fixtureFiles is the two-bundle root every view test reads.
func fixtureFiles() map[string]string {
	return map[string]string{
		".verdict/state/1234/PR.json":   prDoc(headA),
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
	root := writeFixture(t, fixtureFiles())
	out := &syncBuffer{}
	cancel, done := startRun(t, root, out)
	match := waitForURL(t, out)
	assertLoopbackHealth(t, match)
	cancel()
	assertCanceled(t, done)
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
