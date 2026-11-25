package serve

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// writeRoutes is every route that writes: the local triage and KB writes, the
// job routes, and the host submit. A route added to the writable table without
// a row here fails the pin below, so --read-only cannot silently miss it.
var writeRoutes = []string{
	"/v1/jobs/post-preview",
	"/v1/jobs/verify",
	"/v1/jobs/{id}/cancel",
	"/v1/kb/promote",
	"/v1/post/submit",
	"/v1/triage",
}

// TestDefaultServerIsWritable pins the shared write posture: with no flag the
// server serves local writes (it mints the CSRF token and registers every POST
// route), and --read-only is the only thing that removes them.
func TestDefaultServerIsWritable(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	ts, server := interactiveServer(t, testLayer(root, nil))
	if server.csrf == "" {
		t.Fatal("a default server minted no CSRF token, so it cannot take local writes")
	}
	health := call(t, ts, http.MethodGet, "/v1/health", "", bearer())
	if !strings.Contains(string(health.body), `"read_only": false`) {
		t.Errorf("default health = %s, want read_only false", health.body)
	}
	if got := post(t, ts, "/v1/triage", `{}`, writeHeaders(server)); got.status == http.StatusMethodNotAllowed {
		t.Errorf("default POST /v1/triage = 405, want the write to reach its handler (%s)", got.body)
	}
	writable := slices.Sorted(maps.Keys(interactivePostPatterns(server)))
	if !slices.Equal(writable, writeRoutes) {
		t.Errorf("writable POST routes = %v, want %v", writable, writeRoutes)
	}
}

// TestReadOnlyRefusesEveryWriteRoute pins --read-only against every write
// route, with full credentials so nothing but the posture can refuse: each
// answers 405, the tree is unchanged, and no CSRF token exists to spend.
func TestReadOnlyRefusesEveryWriteRoute(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	server, err := New(&Config{Layer: testLayer(root, nil), Port: testPort, Token: testToken, Now: fixedNow, ReadOnly: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if server.csrf != "" {
		t.Error("a read-only server minted a CSRF token")
	}
	if got := interactivePostPatterns(server); len(got) != 0 {
		t.Errorf("read-only POST routes = %v, want none", got)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	headers := writeHeaders(server)
	before := snapshotState(t, root)
	for _, pattern := range writeRoutes {
		path := strings.ReplaceAll(pattern, "{id}", "abc")
		if got := post(t, ts, path, `{}`, headers); got.status != http.StatusMethodNotAllowed {
			t.Errorf("read-only POST %s = %d, want 405 (%s)", path, got.status, got.body)
		}
	}
	if after := snapshotState(t, root); !maps.Equal(before, after) {
		t.Fatalf("a refused write changed the fixture tree:\nbefore: %v\nafter:  %v", before, after)
	}
}

// TestPostureFlags pins the argv: --read-only sets the one knob, and the
// retired --interactive is an unknown flag (a usage error), not an alias.
//
//nolint:revive // the flag matrix reads as one assertion block
func TestPostureFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		args         []string
		wantReadOnly bool
	}{
		{name: "default"},
		{name: "read-only", args: []string{"--read-only"}, wantReadOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := &syncBuffer{}
			runner := &cli.Runner{Out: out, Human: out, Diag: out, Effective: cli.Defaults()}
			opts, code, done := parseFlags(runner, tc.args)
			if done {
				t.Fatalf("parseFlags(%v) stopped with code %d: %s", tc.args, code, out.String())
			}
			if opts.readOnly != tc.wantReadOnly {
				t.Errorf("readOnly = %v, want %v", opts.readOnly, tc.wantReadOnly)
			}
		})
	}
	out := &syncBuffer{}
	runner := &cli.Runner{Out: out, Human: out, Diag: out, Effective: cli.Defaults()}
	if _, code, done := parseFlags(runner, []string{"--interactive"}); !done || code != cli.ExitUsage {
		t.Errorf("--interactive = done %v code %d, want a usage refusal", done, code)
	}
}
