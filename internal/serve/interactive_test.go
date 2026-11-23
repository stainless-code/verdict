package serve

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
)

const interactiveRationale = "This finding is a false alarm, verified against the diff."

// interactiveServer binds an interactive test server over layer.
func interactiveServer(t *testing.T, layer Layer) (*httptest.Server, *Server) {
	t.Helper()
	server, err := New(&Config{Port: testPort, Token: testToken, Now: fixedNow, Layer: layer, Interactive: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	t.Cleanup(ts.Close)
	return ts, server
}

// post performs one POST through the test Host and headers.
func post(
	t *testing.T, ts *httptest.Server, path, body string, headers map[string]string,
) result {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testHost
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{status: resp.StatusCode, header: resp.Header, body: raw}
}

// writeHeaders is one write request's full credential set: session auth,
// the interactive CSRF token, and an allowlisted Origin.
func writeHeaders(server *Server) map[string]string {
	hdrs := bearer()
	hdrs["Content-Type"] = "application/json"
	hdrs["Origin"] = "http://127.0.0.1:47123"
	hdrs["X-Verdict-CSRF"] = server.csrf
	return hdrs
}

// TestPostsRefusedWithoutInteractive pins the default matrix: a POST is a
// 405 with an Allow header and zero writes.
func TestPostsRefusedWithoutInteractive(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	before := snapshotState(t, root)
	for _, path := range []string{"/v1/triage", "/v1/kb/promote"} {
		got := post(t, ts, path, `{}`, bearer())
		if got.status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405 (%s)", path, got.status, got.body)
		}
		if !strings.Contains(got.header.Get("Allow"), http.MethodGet) {
			t.Errorf("POST %s Allow = %q, want GET", path, got.header.Get("Allow"))
		}
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused POST changed the fixture tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// TestHealthAdvertisesInteractive pins the capability advertisement: and only
// on an interactive server.
func TestHealthAdvertisesInteractive(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	layer := testLayer(root, nil)
	ts, server := interactiveServer(t, layer)
	if server.csrf == "" {
		t.Fatal("an interactive server must mint a CSRF token")
	}
	got := call(t, ts, http.MethodGet, "/v1/health", "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("health = %d, want 200 (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"interactive": true`) {
		t.Errorf("health = %s, want interactive true", got.body)
	}
	if !strings.Contains(string(got.body), `"csrf": "`+server.csrf+`"`) {
		t.Errorf("health = %s, want the session csrf", got.body)
	}
	if server.csrf == testToken {
		t.Error("the CSRF token must not be the session token")
	}
	plain := testServer(t, layer)
	plainBody := call(t, plain, http.MethodGet, "/v1/health", "", bearer()).body
	if strings.Contains(string(plainBody), "csrf") || strings.Contains(string(plainBody), "interactive") {
		t.Errorf("plain health = %s, must not advertise interactive or csrf", plainBody)
	}
}

// refusalCase is one must-refuse write: its headers, status, and transport
// reason. The runner also proves the fixture tree is unchanged.
type refusalCase struct { //nolint:govet // table row; field order reads as the case
	name    string
	path    string
	body    string
	headers map[string]string
	status  int
	reason  string
}

// runRefusalCases runs one table of must-refuse POSTs: status, transport
// reason, and tree unchanged per case.
func runRefusalCases(t *testing.T, ts *httptest.Server, root string, cases []refusalCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRefusalCase(t, ts, root, &tc)
		})
	}
}

// assertRefusalCase proves one must-refuse POST fails closed without writes.
func assertRefusalCase(t *testing.T, ts *httptest.Server, root string, tc *refusalCase) {
	t.Helper()
	before := snapshotState(t, root)
	got := post(t, ts, tc.path, tc.body, tc.headers)
	if got.status != tc.status {
		t.Fatalf("status = %d, want %d (%s)", got.status, tc.status, got.body)
	}
	if !strings.Contains(string(got.body), `"reason":"`+tc.reason+`"`) {
		t.Errorf("body = %s, want reason %s", got.body, tc.reason)
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused write changed the tree")
	}
}

// TestInteractiveCSSRRefusals pins every CSRF failure mode failing closed with
// zero writes.
func TestInteractiveCSSRRefusals(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	headers := func(token string) map[string]string {
		hdrs := writeHeaders(server)
		if token == "" {
			delete(hdrs, "X-Verdict-CSRF")
		} else {
			hdrs["X-Verdict-CSRF"] = token
		}
		return hdrs
	}
	runRefusalCases(t, ts, root, []refusalCase{
		{name: "absent", path: "/v1/triage", body: payload, headers: headers(""), status: http.StatusForbidden, reason: "bad-csrf"},
		{name: "short", path: "/v1/triage", body: payload, headers: headers("z"), status: http.StatusForbidden, reason: "bad-csrf"},
		{name: "same length wrong", path: "/v1/triage", body: payload, headers: headers(strings.Repeat("z", len(server.csrf))), status: http.StatusForbidden, reason: "bad-csrf"},
	})
}

// TestInteractiveOriginRefusals pins a missing or foreign Origin on a POST
// failing closed before any write.
func TestInteractiveOriginRefusals(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	headers := func(origin string) map[string]string {
		hdrs := writeHeaders(server)
		if origin == "" {
			delete(hdrs, "Origin")
		} else {
			hdrs["Origin"] = origin
		}
		return hdrs
	}
	runRefusalCases(t, ts, root, []refusalCase{
		{name: "absent", path: "/v1/triage", body: payload, headers: headers(""), status: http.StatusForbidden, reason: "bad-origin"},
		{name: "foreign", path: "/v1/triage", body: payload, headers: headers("https://evil.test"), status: http.StatusForbidden, reason: "bad-origin"},
	})
}

// TestInteractiveContentTypeRefusal pins a non-JSON write refusing before any
// write.
func TestInteractiveContentTypeRefusal(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	hdrs := writeHeaders(server)
	hdrs["Content-Type"] = "text/plain"
	runRefusalCases(t, ts, root, []refusalCase{
		{name: "text plain", path: "/v1/kb/promote", body: `{"root":"x"}`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-content-type"},
	})
}

// TestInteractiveBadBodyRefusals pins unknown fields, trailing values, and
// oversized bodies refusing before any write.
func TestInteractiveBadBodyRefusals(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	runRefusalCases(t, ts, root, []refusalCase{
		{name: "unknown field", path: "/v1/kb/promote", body: `{"root":"` + rootID + `","surprise":1}`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
		{name: "trailing value", path: "/v1/kb/promote", body: `{"root":"` + rootID + `"} {}`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
		{name: "oversized", path: "/v1/kb/promote", body: `{"root":"` + strings.Repeat("x", 70*1024) + `"}`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
	})
}

// TestInteractiveTriageSuccessParity proves the POST flips the store byte for
// byte like the CLI service on an identical fixture.
func TestInteractiveTriageSuccessParity(t *testing.T) {
	t.Parallel()
	httpDir := writeFixture(t, fixtureFiles())
	serviceDir := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(httpDir, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	got := post(t, ts, "/v1/triage", payload, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", got.status, got.body)
	}
	env := decode(t, got.body)
	if env["error"] != nil {
		t.Fatalf("error = %v, want null (%s)", env["error"], got.body)
	}
	data := asMap(t, env["data"])
	if asString(t, data["sig"]) != sigCrit || asString(t, data["status"]) != "false-positive" {
		t.Errorf("data = %v, want the flipped sig and status", data)
	}
	_, serviceRef := triagecore.Run(t.Context(), &triagecore.Request{
		Runtime: &core.Runtime{Now: fixedNow}, Pr: "1234", RepoRoot: serviceDir,
		Sig: sigCrit, Status: "false-positive", Rationale: interactiveRationale,
	})
	if serviceRef != nil {
		t.Fatalf("service triage refused: %v", serviceRef)
	}
	assertFileParity(t, filepath.Join(httpDir, ".verdict/state/1234/findings.json"),
		filepath.Join(serviceDir, ".verdict/state/1234/findings.json"))
	assertFileParity(t, filepath.Join(httpDir, ".verdict/kb.jsonl"),
		filepath.Join(serviceDir, ".verdict/kb.jsonl"))
	raw, err := os.ReadFile(filepath.Join(httpDir, ".verdict/state/1234/findings.json"))
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	if !strings.Contains(string(raw), `"false-positive"`) {
		t.Errorf("findings = %s, want the false-positive flip", raw)
	}
}

// TestInteractiveKBPromoteParity proves the POST appends the kb.jsonl row the
// CLI service appends, byte for byte.
func TestInteractiveKBPromoteParity(t *testing.T) {
	t.Parallel()
	httpDir := writeFixture(t, fixtureFiles())
	serviceDir := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(httpDir, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","rule":"Never log secrets in error paths",` +
		`"scope":"src/*.go","rigor":"pointed"}`
	got := post(t, ts, "/v1/kb/promote", payload, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", got.status, got.body)
	}
	env := decode(t, got.body)
	if env["error"] != nil {
		t.Fatalf("error = %v, want null (%s)", env["error"], got.body)
	}
	if _, ref := learncore.Run(t.Context(), &learncore.Request{
		Runtime: &core.Runtime{Now: fixedNow}, RepoRoot: serviceDir,
		Rule: "Never log secrets in error paths", Scope: "src/*.go", Rigor: "pointed",
	}); ref != nil {
		t.Fatalf("service learn refused: %v", ref)
	}
	assertFileParity(t, filepath.Join(httpDir, ".verdict/kb.jsonl"),
		filepath.Join(serviceDir, ".verdict/kb.jsonl"))
	kbBytes, err := os.ReadFile(filepath.Join(httpDir, ".verdict/kb.jsonl"))
	if err != nil {
		t.Fatalf("kb.jsonl: %v", err)
	}
	if !strings.Contains(string(kbBytes), `"rule":"Never log secrets in error paths"`) {
		t.Errorf("kb.jsonl = %s, want the appended rule", kbBytes)
	}
}

// TestInteractiveTriageRefusalEnvelope pins a business refusal (a short
// rationale) surfacing as the typed 200 envelope.
func TestInteractiveTriageRefusalEnvelope(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"too short"}`
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/triage", payload, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"code": "rationale-required"`) {
		t.Errorf("body = %s, want rationale-required", got.body)
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused triage changed the tree")
	}
}

// TestInteractiveRootBundleConfinement pins a root outside the served set
// 404ing and a traversal bundle refusing, both without writes.
func TestInteractiveRootBundleConfinement(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	runRefusalCases(t, ts, root, []refusalCase{
		{name: "unknown root", path: "/v1/triage", body: `{"root":"000000000000","bundle":"1234","sig":"` + sigCrit + `","status":"false-positive","rationale":"` + interactiveRationale + `"}`, headers: hdrs, status: http.StatusNotFound, reason: "not-found"},
		{name: "unknown root for kb", path: "/v1/kb/promote", body: `{"root":"000000000000","rule":"r","scope":"s"}`, headers: hdrs, status: http.StatusNotFound, reason: "not-found"},
	})
	t.Run("traversal bundle", func(t *testing.T) {
		before := snapshotState(t, root)
		payload := `{"root":"` + rootID + `","bundle":"../x","sig":"` + sigCrit +
			`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
		got := post(t, ts, "/v1/triage", payload, hdrs)
		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200 refusal (%s)", got.status, got.body)
		}
		if !strings.Contains(string(got.body), `"code": "bad-flags"`) {
			t.Errorf("body = %s, want the bundle-name refusal", got.body)
		}
		if after := snapshotState(t, root); !maps.Equal(before, after) {
			t.Fatalf("a traversal refusal changed the tree")
		}
	})
}

// TestInteractiveStaticExportCarriesNoToken pins the static export carrying
// no CSRF value: data.json is written from layer views, never the health
// advertisement.
func TestInteractiveStaticExportCarriesNoToken(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	layer := testLayer(root, nil)
	_, server := interactiveServer(t, layer)
	dest := filepath.Join(exportTempDir(t), "export")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatalf("exportHTML: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dest, "data.json"))
	if err != nil {
		t.Fatalf("data.json: %v", err)
	}
	if strings.Contains(string(raw), server.csrf) {
		t.Errorf("data.json carries the CSRF value")
	}
	if strings.Contains(string(raw), `"csrf"`) {
		t.Errorf("data.json carries a csrf field: %s", raw)
	}
}

// assertFileParity compares one file on both sides of a parity proof; a
// file absent on both sides is parity.
func assertFileParity(t *testing.T, aPath, bPath string) {
	t.Helper()
	a, aErr := os.ReadFile(aPath)
	b, bErr := os.ReadFile(bPath)
	if os.IsNotExist(aErr) && os.IsNotExist(bErr) {
		return
	}
	if aErr != nil || bErr != nil {
		t.Fatalf("parity files: %v / %v", aErr, bErr)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("files differ:\nhttp:\n%s\nservice:\n%s", a, b)
	}
}
