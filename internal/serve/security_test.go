package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wantCSP is the literal policy the header must carry; it is deliberately not
// the implementation's constant, so changing one without the other fails.
const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; " +
	"form-action 'none'; frame-ancestors 'none'"

// securityCase is one literal cell of the security matrix.
type securityCase struct { //nolint:govet // table row; field order reads as the case
	name    string
	method  string
	path    string
	host    string
	headers map[string]string
	status  int
	reason  string
	allow   bool
}

// TestSecurityMatrix pins every cell the spec names: token spellings, the
// exchange, Host and Origin gates, the method table, route bounds, and the
// response headers.
func TestSecurityMatrix(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	rootID := rootIDOf(t, ts, bearer())
	session := exchangeSession(t, ts)
	for _, tc := range securityCases(rootID, session) {
		t.Run(tc.name, func(t *testing.T) {
			assertSecurityCase(t, ts, &tc)
		})
	}
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/1234", "", session)
	if got.status != http.StatusOK {
		t.Fatalf("detail with cookie = %d (%s)", got.status, got.body)
	}
}

// exchangeSession performs the token exchange and returns the cookie header
// the rest of the matrix reuses.
func exchangeSession(t *testing.T, ts *httptest.Server) (session map[string]string) {
	t.Helper()
	exchange := call(t, ts, http.MethodGet, "/?token="+testToken, "", nil)
	if exchange.status != http.StatusSeeOther {
		t.Fatalf("exchange status = %d, want 303", exchange.status)
	}
	if location := exchange.header.Get("Location"); location != "/" {
		t.Errorf("exchange Location = %q, want /", location)
	}
	cookies := exchange.header.Values("Set-Cookie")
	if len(cookies) != 1 {
		t.Fatalf("exchange Set-Cookie = %v, want one", cookies)
	}
	assertSessionCookie(t, cookies[0])
	badExchange := call(t, ts, http.MethodGet, "/?token=wrong-token", "", nil)
	if badExchange.status != http.StatusUnauthorized {
		t.Errorf("bad exchange status = %d, want 401", badExchange.status)
	}
	if len(badExchange.header.Values("Set-Cookie")) != 0 {
		t.Errorf("bad exchange wrote a cookie: %v", badExchange.header.Values("Set-Cookie"))
	}
	return map[string]string{"Cookie": cookieName + "=" + testToken}
}

// TestBadPathIsTheEarlyRouteRefusal proves the pre-routing gate is the one
// that refuses a traversal spelling, not a downstream pattern: only badPath
// produces the "no such route" transport message.
func TestBadPathIsTheEarlyRouteRefusal(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	headers := bearer()
	rootID := rootIDOf(t, ts, headers)
	for _, tc := range []struct{ name, path string }{
		{name: "encoded traversal", path: "/v1/roots/" + rootID + "/bundles/%2e%2e%2f"},
		{name: "control byte", path: "/v1/roots/" + rootID + "/bundles/local%00"},
		{name: "backslash", path: "/v1/roots/" + rootID + "/bundles/..%5C..%5Cetc"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBadPath(t, ts, headers, tc.path) })
	}
}

// assertBadPath pins the early gate's transport body.
func assertBadPath(t *testing.T, ts *httptest.Server, headers map[string]string, path string) {
	t.Helper()
	got := call(t, ts, http.MethodGet, path, "", headers)
	if got.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", got.status, got.body)
	}
	for _, want := range []string{`"reason":"not-found"`, `"message":"no such route"`} {
		if !strings.Contains(string(got.body), want) {
			t.Errorf("body = %s, want %s", got.body, want)
		}
	}
}

// securityCases is the literal matrix.
func securityCases(rootID string, session map[string]string) []securityCase {
	return []securityCase{
		{name: "no token", method: http.MethodGet, path: "/v1/health", status: http.StatusUnauthorized},
		{
			name: "same-length wrong token", method: http.MethodGet, path: "/v1/health",
			headers: map[string]string{"Authorization": "Bearer 0123456789abcdef0123456780"},
			status:  http.StatusUnauthorized,
		},
		{
			name: "short token", method: http.MethodGet, path: "/v1/health",
			headers: map[string]string{"Authorization": "Bearer short"}, status: http.StatusUnauthorized,
		},
		{name: "bearer token", method: http.MethodGet, path: "/v1/health", headers: bearer(), status: http.StatusOK},
		{name: "session cookie", method: http.MethodGet, path: "/v1/health", headers: session, status: http.StatusOK},
		{
			name: "foreign cookie", method: http.MethodGet, path: "/v1/health",
			headers: map[string]string{"Cookie": cookieName + "=0123456789abcdef0123456780"},
			status:  http.StatusUnauthorized,
		},
		{
			name: "stale empty cookie", method: http.MethodGet, path: "/v1/health",
			headers: map[string]string{"Cookie": cookieName + "="}, status: http.StatusUnauthorized,
		},
		{
			name: "cookie from another port", method: http.MethodGet, path: "/v1/health",
			headers: map[string]string{"Cookie": "verdict_session_9999=" + testToken},
			status:  http.StatusUnauthorized,
		},
		{
			name: "token in query on v1", method: http.MethodGet,
			path: "/v1/health?token=" + testToken, headers: bearer(),
			status: http.StatusBadRequest, reason: "token-in-query",
		},
		{
			name: "bad host", method: http.MethodGet, path: "/v1/health", host: "attacker.test:47123",
			headers: bearer(), status: http.StatusForbidden, reason: "bad-host",
		},
		{
			name: "bad origin", method: http.MethodGet, path: "/v1/health",
			headers: map[string]string{"Authorization": "Bearer " + testToken, "Origin": "https://evil.test"},
			status:  http.StatusForbidden, reason: "bad-origin",
		},
		{
			name: "post", method: http.MethodPost, path: "/v1/health", headers: bearer(),
			status: http.StatusMethodNotAllowed, allow: true,
		},
		{
			name: "options", method: http.MethodOptions, path: "/v1/health",
			status: http.StatusMethodNotAllowed, allow: true,
		},
		{name: "unknown route", method: http.MethodGet, path: "/v1/nope", headers: bearer(), status: http.StatusNotFound},
		{
			name: "encoded traversal", method: http.MethodGet,
			path: "/v1/roots/" + rootID + "/bundles/%2e%2e%2f", headers: bearer(),
			status: http.StatusNotFound,
		},
		{
			name: "escaping path glob", method: http.MethodGet, path: "/v1/bundles?path=../..",
			headers: bearer(), status: http.StatusBadRequest, reason: "bad-glob",
		},
		{
			name: "loopback ipv4 host", method: http.MethodGet, path: "/v1/health",
			host: "127.0.0.1:47123", headers: bearer(), status: http.StatusOK,
		},
		{
			name: "loopback name host", method: http.MethodGet, path: "/v1/health",
			host: "localhost:47123", headers: bearer(), status: http.StatusOK,
		},
		{
			name: "loopback ipv6 host", method: http.MethodGet, path: "/v1/health",
			host: "[::1]:47123", headers: bearer(), status: http.StatusOK,
		},
	}
}

// assertSecurityCase checks one matrix cell.
func assertSecurityCase(t *testing.T, ts *httptest.Server, tc *securityCase) {
	t.Helper()
	got := call(t, ts, tc.method, tc.path, tc.host, tc.headers)
	if got.status != tc.status {
		t.Fatalf("%s %s = %d, want %d (%s)", tc.method, tc.path, got.status, tc.status, got.body)
	}
	assertReason(t, got, tc.reason)
	assertAllow(t, got, tc.allow)
	assertResponseHeaders(t, got)
}

// assertReason pins the transport reason when the case names one.
func assertReason(t *testing.T, got result, reason string) {
	t.Helper()
	if reason != "" && !strings.Contains(string(got.body), `"reason":"`+reason+`"`) {
		t.Errorf("body = %s, want reason %q", got.body, reason)
	}
}

// assertAllow pins the method table's Allow header.
func assertAllow(t *testing.T, got result, want bool) {
	t.Helper()
	if !want {
		return
	}
	allow := got.header.Get("Allow")
	if !strings.Contains(allow, http.MethodGet) || !strings.Contains(allow, http.MethodHead) {
		t.Errorf("Allow = %q, want GET and HEAD", allow)
	}
}

// assertResponseHeaders pins no-CORS and the exact CSP on a 200.
func assertResponseHeaders(t *testing.T, got result) {
	t.Helper()
	if got.header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("a CORS header was emitted")
	}
	if got.status != http.StatusOK {
		return
	}
	if policy := got.header.Get("Content-Security-Policy"); policy != wantCSP {
		t.Errorf("CSP = %q, want the exact policy", policy)
	}
	if referrer := got.header.Get("Referrer-Policy"); referrer != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", referrer)
	}
	if nosniff := got.header.Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}
	if cache := got.header.Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cache)
	}
}

// TestCSP pins the browser policy on the UI and data routes.
func TestCSP(t *testing.T) {
	t.Parallel()
	ts := testServer(t, testLayer(writeFixture(t, fixtureFiles()), nil))
	for _, test := range []struct{ path, contentType, body string }{
		{path: "/", contentType: "text/html; charset=utf-8", body: `id="main"`},
		{path: "/assets/app.js", contentType: "text/javascript; charset=utf-8", body: "function render("},
		{path: "/assets/app.css", contentType: "text/css; charset=utf-8", body: "prefers-color-scheme: dark"},
		{path: "/v1/health", contentType: "application/json; charset=utf-8", body: `"watch"`},
	} {
		assertCSPRoute(t, ts, test.path, test.contentType, test.body)
	}
	unknown := call(t, ts, http.MethodGet, "/assets/not-a-file", "", bearer())
	if unknown.status != http.StatusNotFound {
		t.Errorf("unknown asset status = %d, want 404", unknown.status)
	}
}

func assertCSPRoute(t *testing.T, ts *httptest.Server, path, contentType, body string) {
	t.Helper()
	got := call(t, ts, http.MethodGet, path, "", bearer())
	if got.status != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", path, got.status)
	}
	if got.header.Get("Content-Security-Policy") != wantCSP ||
		strings.Contains(got.header.Get("Content-Security-Policy"), "unsafe-inline") {
		t.Errorf("GET %s CSP = %q, want strict policy %q", path, got.header.Get("Content-Security-Policy"), wantCSP)
	}
	if got.header.Get("Content-Type") != contentType || !strings.Contains(string(got.body), body) {
		t.Errorf("GET %s content type/body = %q/%q", path, got.header.Get("Content-Type"), got.body)
	}
}

// assertSessionCookie pins the exchange cookie's attributes.
func assertSessionCookie(t *testing.T, cookie string) {
	t.Helper()
	for _, want := range []string{
		cookieName + "=" + testToken, "Path=/v1", "HttpOnly", "SameSite=Strict",
	} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie = %q, want %q", cookie, want)
		}
	}
	for _, forbidden := range []string{"Max-Age", "Secure", "Domain="} {
		if strings.Contains(cookie, forbidden) {
			t.Errorf("Set-Cookie = %q, must not carry %q", cookie, forbidden)
		}
	}
}
