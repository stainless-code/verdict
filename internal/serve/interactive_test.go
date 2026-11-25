package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	"github.com/stainless-code/verdict/internal/core/query"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
)

const interactiveRationale = "This finding is a false alarm, verified against the diff."

// interactiveServer binds an interactive test server over layer.
func interactiveServer(t *testing.T, layer Layer) (*httptest.Server, *Server) {
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

// TestInteractiveCSRFRefusals pins every CSRF failure mode failing closed with
// zero writes.
func TestInteractiveCSRFRefusals(t *testing.T) {
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
		{name: "opaque null", path: "/v1/triage", body: payload, headers: headers("null"), status: http.StatusForbidden, reason: "bad-origin"},
		{name: "wrong port", path: "/v1/triage", body: payload, headers: headers("http://127.0.0.1:9999"), status: http.StatusForbidden, reason: "bad-origin"},
		{name: "wrong scheme", path: "/v1/triage", body: payload, headers: headers("https://127.0.0.1:47123"), status: http.StatusForbidden, reason: "bad-origin"},
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

// TestInteractiveBadBodyRefusals pins unknown fields, trailing values,
// oversized bodies, and non-object top-level values refusing before any write.
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
		{name: "null body", path: "/v1/kb/promote", body: `null`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
		{name: "array body", path: "/v1/triage", body: `[]`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
		{name: "string body", path: "/v1/triage", body: `"root"`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
		{name: "number body", path: "/v1/triage", body: `42`, headers: hdrs, status: http.StatusBadRequest, reason: "bad-body"},
	})
}

// kbSeedRow is one active learn rule the triage flip credits through the
// finding's kb citation.
const kbSeedRow = `{"id":"kb-rule-1","rule":"Always quote shell arguments","scope":"src/*.go",` +
	`"provenance":"manual","rigor":"pointed","hits":0,"false_hits":0,"judged":0,"status":"active"}`

// kbCitedFinding is one open finding citing kb-rule-1, so a flip stages KB
// credit and rewrites kb.jsonl on both sides of the parity proof.
func kbCitedFinding(sig string) string {
	return strings.TrimSuffix(finding(sig, "open", "critical", "walked"), "}") + `,"kb":["kb-rule-1"]}`
}

// triageParityFiles is the parity fixture: the standard two-bundle root, one
// KB-cited finding, and the cited rule's store row.
func triageParityFiles() map[string]string {
	files := fixtureFiles()
	files[".verdict/state/1234/findings.json"] = "[" + strings.Join([]string{
		kbCitedFinding(sigCrit),
		finding(sigImp, "open", "important", "pointed"),
		finding(sigAddr, "addressed", "important", "walked"),
	}, ",") + "]"
	files[".verdict/kb.jsonl"] = kbSeedRow + "\n"
	return files
}

// TestInteractiveTriageSuccessParity proves the POST flips the store byte for
// byte like the CLI service on an identical fixture, including the KB credit
// the cited finding stages into a non-empty kb.jsonl.
func TestInteractiveTriageSuccessParity(t *testing.T) {
	t.Parallel()
	files := triageParityFiles()
	httpDir := writeFixture(t, files)
	serviceDir := writeFixture(t, files)
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
	assertKBChargedParity(t, httpDir, serviceDir)
}

// assertKBChargedParity proves the KB-cited leg moved both stores: a
// non-empty kb.jsonl with a credited counter and a false-positive join row,
// byte-identical on both sides.
func assertKBChargedParity(t *testing.T, httpDir, serviceDir string) {
	t.Helper()
	kbBytes, err := os.ReadFile(filepath.Join(httpDir, ".verdict/kb.jsonl"))
	if err != nil {
		t.Fatalf("kb.jsonl: %v", err)
	}
	if len(kbBytes) == 0 {
		t.Error("kb.jsonl is empty: the parity leg compared two absent stores")
	}
	if !strings.Contains(string(kbBytes), `"id":"kb-rule-1"`) {
		t.Errorf("kb.jsonl = %s, want the cited rule's row", kbBytes)
	}
	if string(kbBytes) == kbSeedRow+"\n" {
		t.Errorf("kb.jsonl = %s, want a credited counter after the flip", kbBytes)
	}
	joins, err := os.ReadFile(filepath.Join(httpDir, ".verdict/kb-joins.jsonl"))
	if err != nil {
		t.Fatalf("kb-joins.jsonl: %v", err)
	}
	if !strings.Contains(string(joins), `"outcome":"false-positive"`) {
		t.Errorf("kb-joins.jsonl = %s, want the false-positive credit row", joins)
	}
	assertFileParity(t, filepath.Join(httpDir, ".verdict/kb-joins.jsonl"),
		filepath.Join(serviceDir, ".verdict/kb-joins.jsonl"))
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

// writeMethods are the methods the mux must refuse on the interactive write
// paths.
var writeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPut, http.MethodOptions,
}

// assertMethod405 proves one method on one write path is a 405 with Allow:
// POST, never the read-only placeholder.
func assertMethod405(t *testing.T, ts *httptest.Server, method, path string) {
	t.Helper()
	got := call(t, ts, method, path, "", bearer())
	if got.status != http.StatusMethodNotAllowed {
		t.Errorf("%s %s = %d, want 405 (%s)", method, path, got.status, got.body)
		return
	}
	if allow := got.header.Get("Allow"); !strings.Contains(allow, http.MethodPost) {
		t.Errorf("%s %s Allow = %q, want POST", method, path, allow)
	}
	if strings.Contains(string(got.body), "read-only") {
		t.Errorf("%s %s body answers read-only: %s", method, path, got.body)
	}
}

// TestInteractiveMethodNotAllowed pins the interactive method matrix: only
// POST is registered on the write paths, so GET, HEAD, PUT, and OPTIONS get
// the mux's 405 with Allow: POST — never the read-only placeholder.
func TestInteractiveMethodNotAllowed(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, _ := interactiveServer(t, testLayer(root, nil))
	for _, path := range []string{"/v1/triage", "/v1/kb/promote"} {
		for _, method := range writeMethods {
			assertMethod405(t, ts, method, path)
		}
	}
}

// TestHealthReadOnlyPosture pins the health posture: read_only is true by
// default and false with --interactive.
func TestHealthReadOnlyPosture(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	layer := testLayer(root, nil)
	interactive, _ := interactiveServer(t, layer)
	got := call(t, interactive, http.MethodGet, "/v1/health", "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("interactive health = %d, want 200 (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"read_only": false`) {
		t.Errorf("interactive health = %s, want read_only false", got.body)
	}
	plain := testServer(t, layer)
	plainBody := call(t, plain, http.MethodGet, "/v1/health", "", bearer()).body
	if !strings.Contains(string(plainBody), `"read_only": true`) {
		t.Errorf("default health = %s, want read_only true", plainBody)
	}
}

// TestInteractiveCharsetContentTypeAccepted pins a JSON content type with a
// charset parameter writing successfully.
func TestInteractiveCharsetContentTypeAccepted(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	hdrs["Content-Type"] = "application/json; charset=utf-8"
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	got := post(t, ts, "/v1/triage", payload, hdrs)
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", got.status, got.body)
	}
	if env := decode(t, got.body); env["error"] != nil {
		t.Fatalf("error = %v, want null (%s)", env["error"], got.body)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".verdict/state/1234/findings.json"))
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	if !strings.Contains(string(raw), `"false-positive"`) {
		t.Errorf("findings = %s, want the false-positive flip", raw)
	}
}

// invalidateCountingLayer wraps one layer and counts cache invalidations.
type invalidateCountingLayer struct {
	Layer
	count *int
}

// Invalidate records one invalidation and clears the wrapped cache.
func (l invalidateCountingLayer) Invalidate() {
	*l.count++
	l.Layer.Invalidate()
}

// Changes forwards the wrapped layer's change stream the watch validation
// requires.
func (l invalidateCountingLayer) Changes(ctx context.Context) <-chan query.Change {
	if source, ok := l.Layer.(changeSource); ok {
		return source.Changes(ctx)
	}
	return nil
}

// TestInteractiveTriageInvalidatesCache proves a successful triage drops the
// composed views the write moved.
func TestInteractiveTriageInvalidatesCache(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	count := 0
	layer := invalidateCountingLayer{Layer: testLayer(root, nil), count: &count}
	ts, server := interactiveServer(t, layer)
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	got := post(t, ts, "/v1/triage", payload, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", got.status, got.body)
	}
	if env := decode(t, got.body); env["error"] != nil {
		t.Fatalf("error = %v, want null (%s)", env["error"], got.body)
	}
	if count != 1 {
		t.Errorf("Invalidate calls = %d, want 1 after one triage", count)
	}
}

// errorLayer wraps one layer and fails bundle resolution with an unexpected
// error, proving the write fails closed instead of running with an empty root.
type errorLayer struct {
	Layer
}

// Changes forwards the wrapped layer's change stream the watch validation
// requires.
func (l errorLayer) Changes(ctx context.Context) <-chan query.Change {
	if source, ok := l.Layer.(changeSource); ok {
		return source.Changes(ctx)
	}
	return nil
}

// ResolveBundle fails with a non-typed, non-not-found error.
func (errorLayer) ResolveBundle(string, string) (string, string, error) {
	return "", "", errors.New("boom")
}

// TestInteractiveResolveFailureFailsClosed proves an unexpected resolution
// failure is a 500 with zero writes, never a success with an empty root.
func TestInteractiveResolveFailureFailsClosed(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	layer := errorLayer{Layer: testLayer(root, nil)}
	ts, server := interactiveServer(t, layer)
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/triage", payload, writeHeaders(server))
	if got.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"reason":"internal"`) {
		t.Errorf("body = %s, want reason internal", got.body)
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a failed resolution changed the tree")
	}
}

// triagePayload renders one triage POST body over a root id and sig.
func triagePayload(rootID, sig string) string {
	return `{"root":"` + rootID + `","bundle":"1234","sig":"` + sig +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
}

// TestInteractiveTriageBadSig pins an empty or invalid sig refusing before the
// service runs: an empty sig would match the bundle's one finding.
func TestInteractiveTriageBadSig(t *testing.T) {
	t.Parallel()
	files := fixtureFiles()
	files[".verdict/state/1234/findings.json"] = "[" + finding(sigCrit, "open", "critical", "walked") + "]"
	root := writeFixture(t, files)
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	runRefusalCases(t, ts, root, []refusalCase{
		{name: "empty", path: "/v1/triage", body: triagePayload(rootID, ""), headers: hdrs, status: http.StatusBadRequest, reason: "bad-sig"},
		{name: "uppercase", path: "/v1/triage", body: triagePayload(rootID, "C111111111111111111111111111111111111111"), headers: hdrs, status: http.StatusBadRequest, reason: "bad-sig"},
		{name: "too short", path: "/v1/triage", body: triagePayload(rootID, "abc"), headers: hdrs, status: http.StatusBadRequest, reason: "bad-sig"},
		{name: "not hex", path: "/v1/triage", body: triagePayload(rootID, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"), headers: hdrs, status: http.StatusBadRequest, reason: "bad-sig"},
	})
}

// withheldTriageFinding is one open finding carrying a valid policy
// withholding receipt.
func withheldTriageFinding(sig string) string {
	receipt := `,"withheld":{"source":"agent","reason":"operator suppressed",` +
		`"at":"2026-01-01T00:00:00Z"}`
	return strings.TrimSuffix(finding(sig, "open", "critical", "walked"), "}") + receipt + "}"
}

// TestInteractiveTriageWithheldRefused proves a policy-withheld finding is a
// 404 with zero writes to findings.json or kb.jsonl, matching the copy route.
func TestInteractiveTriageWithheldRefused(t *testing.T) {
	t.Parallel()
	files := fixtureFiles()
	files[".verdict/state/1234/findings.json"] = "[" + withheldTriageFinding(sigCrit) + "]"
	root := writeFixture(t, files)
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	before := snapshotState(t, root)
	got := post(t, ts, "/v1/triage", payload, writeHeaders(server))
	if got.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"reason":"not-found"`) {
		t.Errorf("body = %s, want reason not-found", got.body)
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a withheld refusal changed the tree")
	}
	raw, err := os.ReadFile(filepath.Join(root, ".verdict/state/1234/findings.json"))
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	if !strings.Contains(string(raw), `"status":"open"`) || strings.Contains(string(raw), "false-positive") {
		t.Errorf("findings = %s, want the withheld finding still open", raw)
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict/kb.jsonl")); !os.IsNotExist(err) {
		t.Errorf("kb.jsonl = %v, want no store written", err)
	}
}

// postConcurrent performs one POST without touching `[testing.T]`, so driver
// goroutines can fire it. The result is collected and asserted by the test
// goroutine instead.
func postConcurrent(ts *httptest.Server, path, body string, headers map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+path, strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Host = testHost
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	return resp.StatusCode, nil
}

// statusesBySig flattens one decoded store to its sig-to-status view.
func statusesBySig(entries []map[string]any) map[string]string {
	got := map[string]string{}
	for _, entry := range entries {
		sig, ok := entry["sig"].(string)
		if !ok {
			continue
		}
		status, ok := entry["status"].(string)
		if !ok {
			continue
		}
		got[sig] = status
	}
	return got
}

// assertFlipped proves every wanted sig carries its flipped status in the
// bundle's findings store, with the entry count intact.
func assertFlipped(t *testing.T, root string, want map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".verdict/state/1234/findings.json"))
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("findings decode: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("findings entries = %d, want 3 (%s)", len(entries), raw)
	}
	got := statusesBySig(entries)
	for sig, status := range want {
		if got[sig] != status {
			t.Errorf("findings: sig %s status = %q, want %q (all: %v)", sig, got[sig], status, got)
		}
	}
}

// TestInteractiveTriageConcurrentFlipsPreserved proves two concurrent triage
// flips of different findings in one bundle both survive: the service
// serializes the findings read-modify-write, so neither write overwrites the
// other. Four rounds re-flip both findings on the same bundle, so a lost
// update has four chances to land.
func TestInteractiveTriageConcurrentFlipsPreserved(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	hdrs := writeHeaders(server)
	payload := func(sig, status string) string {
		return `{"root":"` + rootID + `","bundle":"1234","sig":"` + sig +
			`","status":"` + status + `","rationale":"` + interactiveRationale + `"}`
	}
	for round := range 4 {
		awaitTriagePair(t, round, ts, hdrs, payload)
		assertFlipped(t, root, map[string]string{
			sigCrit: "false-positive",
			sigImp:  "addressed",
		})
	}
}

// awaitTriagePair fires one concurrent pair of flips — one for each finding —
// and proves both answered 200.
func awaitTriagePair(
	t *testing.T,
	round int,
	ts *httptest.Server,
	hdrs map[string]string,
	payload func(sig, status string) string,
) {
	t.Helper()
	type outcome struct {
		err    error
		status int
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, req := range []struct{ sig, status string }{
		{sigCrit, "false-positive"},
		{sigImp, "addressed"},
	} {
		wg.Go(func() {
			<-start
			status, err := postConcurrent(ts, "/v1/triage",
				payload(req.sig, req.status), hdrs)
			results <- outcome{err: err, status: status}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for out := range results {
		if out.err != nil {
			t.Fatalf("round %d POST: %v", round, out.err)
		}
		if out.status != http.StatusOK {
			t.Fatalf("round %d POST = %d, want 200", round, out.status)
		}
	}
}

// assertOutsideUnchanged proves the escape target gained no flip: the
// snapshot matches and the findings file holds no false-positive.
func assertOutsideUnchanged(t *testing.T, outside string, before map[string]string) {
	t.Helper()
	if after := snapshotState(t, outside); !maps.Equal(before, after) {
		t.Fatalf("the escape target changed:\nbefore: %v\nafter:  %v", before, after)
	}
	raw, err := os.ReadFile(filepath.Join(outside, ".verdict/state/1234/findings.json"))
	if err != nil {
		t.Fatalf("outside findings: %v", err)
	}
	if strings.Contains(string(raw), "false-positive") {
		t.Errorf("outside findings = %s, want no flip outside the root", raw)
	}
}

// TestInteractiveTriageRefusesSymlinkedStateEscape proves a symlinked
// .verdict/state that leaves the served root refuses typed with zero writes
// in the escape target.
func TestInteractiveTriageRefusesSymlinkedStateEscape(t *testing.T) {
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
	ts, server := interactiveServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	payload := `{"root":"` + rootID + `","bundle":"1234","sig":"` + sigCrit +
		`","status":"false-positive","rationale":"` + interactiveRationale + `"}`
	before := snapshotState(t, outside)
	got := post(t, ts, "/v1/triage", payload, writeHeaders(server))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 typed refusal (%s)", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"code": "bad-config"`) {
		t.Errorf("body = %s, want code bad-config", got.body)
	}
	assertOutsideUnchanged(t, outside, before)
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
