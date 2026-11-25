package serve

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
)

// Writes are a loopback-only default. A bind beyond loopback carries the
// session token, the cookie, and the CSRF token over plain HTTP, where an
// on-path sniffer could replay them to mutate .verdict state or start a
// sandboxed verify job, so such a server runs read-only and refuses
// --allow-post. There is no flag to turn the writes back on.

// TestNonLoopbackBindServesReadOnly drives every write route of servers bound
// to each kind of address: loopback spellings keep their writes, anything
// else answers 405 on every write route and mints no CSRF token.
func TestNonLoopbackBindServesReadOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		host     string
		reach    string
		readOnly bool
	}{
		{"", testHost, false},
		{"127.0.0.1", testHost, false},
		{"localhost", "localhost:47123", false},
		{"::1", "[::1]:47123", false},
		{"0.0.0.0", testHost, true},
		{"::", "[::1]:47123", true},
		{"::ffff:0.0.0.0", testHost, true},
		{"192.168.1.10", "192.168.1.10:47123", true},
		{"example.test", "example.test:47123", true},
		{"127.0.0.2", "127.0.0.2:47123", true},
	} {
		t.Run("host="+tc.host, func(t *testing.T) {
			t.Parallel()
			assertBindPosture(t, tc.host, tc.reach, tc.readOnly)
		})
	}
}

// assertBindPosture binds a server to host and checks its CSRF token, its
// health posture, and every write route against the read-only expectation.
func assertBindPosture(t *testing.T, host, reach string, readOnly bool) {
	t.Helper()
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{
		Layer: testLayer(root, nil), Host: host, Port: testPort, Token: testToken, Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if minted := server.csrf != ""; minted == readOnly {
		t.Errorf("csrf minted = %v on --host %q, want %v", minted, host, !readOnly)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	health := call(t, ts, http.MethodGet, "/v1/health", reach, bearer())
	if want := fmt.Sprintf(`"read_only": %t`, readOnly); !strings.Contains(string(health.body), want) {
		t.Errorf("health on --host %q = %s, want %s", host, health.body, want)
	}
	for _, pattern := range writeRoutes {
		path := strings.ReplaceAll(pattern, "{id}", "abc")
		got := guardedPost(t, ts, path, reach, maps.Clone(writeHeaders(server)))
		if refused := got.status == http.StatusMethodNotAllowed; refused != readOnly {
			t.Errorf("POST %s on --host %q = %d (%s), want 405 = %v", path, host, got.status, got.body, readOnly)
		}
	}
}

// TestAllowPostRefusesNonLoopback pins the start refusal: host posting needs
// the loopback write default, so --allow-post on any other bind is bad-flags,
// at New and at Run, and says why.
//
//nolint:revive // the New and Run legs read as one assertion block
func TestAllowPostRefusesNonLoopback(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"0.0.0.0", "::", "192.168.1.10"} {
		if _, err := New(&Config{Layer: testLayer(t.TempDir(), nil), Host: host, Port: testPort, Token: testToken, Now: fixedNow, AllowPost: true}); err == nil ||
			!strings.Contains(err.Error(), "--allow-post needs a loopback --host") {
			t.Errorf("New(--host %s --allow-post) = %v, want the loopback refusal", host, err)
		}
	}
	out := &syncBuffer{}
	runner := &cli.Runner{Cmd: "serve", Out: out, Human: out, Diag: out, JSONMode: true, Now: fixedNow, Effective: cli.Defaults()}
	// A bounded context turns a Run that wrongly starts serving into a failure,
	// not a hang.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if code := Run(ctx, runner, []string{"--root", t.TempDir(), "--host", "0.0.0.0", "--allow-post"}, Request{}); code != cli.ExitRefused {
		t.Fatalf("Run exit = %d, want %d", code, cli.ExitRefused)
	}
	body := out.String()
	if !strings.Contains(body, `"code": "bad-flags"`) && !strings.Contains(body, `"code":"bad-flags"`) {
		t.Errorf("Run = %s, want bad-flags", body)
	}
	if !strings.Contains(body, "plain HTTP") {
		t.Errorf("Run = %s, want the reason (plain HTTP)", body)
	}
}

// runServe starts Run on a free port and returns its start envelope and the
// runner's warnings once the server announced itself.
func runServe(t *testing.T, args ...string) (envelope map[string]any, warnings []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	out := &syncBuffer{}
	runner := &cli.Runner{Cmd: "serve", Out: out, Human: out, Diag: &syncBuffer{}, JSONMode: true, Now: fixedNow, Effective: cli.Defaults()}
	var wg sync.WaitGroup
	wg.Go(func() {
		Run(ctx, runner, append([]string{"--root", t.TempDir(), "--port", "0"}, args...), Request{})
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), `"read_only"`) {
		if time.Now().After(deadline) {
			t.Fatalf("serve %v never announced; output %q", args, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return decode(t, []byte(out.String())), runner.Warnings
}

// startRecordCase is one bind's expected --json start record.
type startRecordCase struct {
	name         string
	wantURLHost  string
	args         []string
	wantWarnings []string
	readOnly     bool
}

// TestStartRecordPostureByBind pins the --json start record: a loopback bind
// reports writes on, every other bind reports read_only true, says why in a
// warning, and a wildcard bind prints a start URL the Host gate accepts.
func TestStartRecordPostureByBind(t *testing.T) {
	for _, tc := range []startRecordCase{
		{name: "loopback default", wantURLHost: "127.0.0.1:"},
		{name: "loopback read-only flag", args: []string{"--read-only"}, readOnly: true, wantURLHost: "127.0.0.1:"},
		{
			name: "wildcard bind", args: []string{"--host", "0.0.0.0"}, readOnly: true, wantURLHost: "127.0.0.1:",
			wantWarnings: []string{"exposes the read-only surface beyond loopback", "is not loopback, so this server runs read-only"},
		},
		{
			name: "wildcard bind with --read-only", args: []string{"--host", "0.0.0.0", "--read-only"}, readOnly: true,
			wantURLHost: "127.0.0.1:", wantWarnings: []string{"exposes the read-only surface beyond loopback"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertStartRecord(t, &tc)
		})
	}
}

//nolint:revive // the start-record fields and warnings read as one assertion block
func assertStartRecord(t *testing.T, tc *startRecordCase) {
	t.Helper()
	envelope, warnings := runServe(t, tc.args...)
	data := asMap(t, envelope["data"])
	if data["read_only"] != tc.readOnly {
		t.Errorf("data.read_only = %v, want %v", data["read_only"], tc.readOnly)
	}
	if data["allow_post"] != false {
		t.Errorf("data.allow_post = %v, want false", data["allow_post"])
	}
	if !strings.HasPrefix(asString(t, data["url"]), "http://"+tc.wantURLHost) {
		t.Errorf("data.url = %v, want a URL on %s", data["url"], tc.wantURLHost)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range tc.wantWarnings {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings = %q, want %q", joined, want)
		}
	}
	if len(tc.wantWarnings) == 0 && strings.Contains(joined, "read-only") {
		t.Errorf("loopback warnings = %q, want none about read-only", joined)
	}
}

// TestWildcardStartURLIsAccepted proves the printed URL for a wildcard bind
// passes the Host gate it is meant to open, and names the loopback of its
// family instead of the wildcard spelling the gate refuses.
func TestWildcardStartURLIsAccepted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ bind, wantHost string }{
		{"0.0.0.0", "127.0.0.1"},
		{"::", "::1"},
		{"::ffff:0.0.0.0", "127.0.0.1"},
		{"192.168.1.10", "192.168.1.10"},
		{"127.0.0.1", "127.0.0.1"},
	} {
		if got := startHost(tc.bind); got != tc.wantHost {
			t.Errorf("startHost(%q) = %q, want %q", tc.bind, got, tc.wantHost)
		}
	}
	server, err := New(&Config{Layer: testLayer(t.TempDir(), nil), Host: "0.0.0.0", Port: testPort, Token: testToken, Now: fixedNow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const want = "http://127.0.0.1:47123/?token=" + testToken
	if got := server.URL(); got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
	if !server.hostAllowed("127.0.0.1:47123") {
		t.Error("the start URL's Host is not on the allowlist")
	}
}

// TestIsWildcardUnmapsV4InV6 pins the spelling a plain IsUnspecified misses:
// ::ffff:0.0.0.0 is the IPv4 wildcard inside an IPv6 address.
func TestIsWildcardUnmapsV4InV6(t *testing.T) {
	t.Parallel()
	for host, want := range map[string]bool{
		"0.0.0.0": true, "::": true, "[::]": true, "::ffff:0.0.0.0": true, "[::ffff:0.0.0.0]": true,
		"127.0.0.1": false, "::1": false, "::ffff:127.0.0.1": false, "192.168.1.10": false, "example.test": false, "": false,
	} {
		if got := isWildcard(host); got != want {
			t.Errorf("isWildcard(%q) = %v, want %v", host, got, want)
		}
	}
}
