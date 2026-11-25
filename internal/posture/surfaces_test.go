package posture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/lsp"
	"github.com/stainless-code/verdict/internal/mcp"
	"github.com/stainless-code/verdict/internal/posture"
	"github.com/stainless-code/verdict/internal/serve"
)

const (
	probePort  = 47123
	probeHost  = "127.0.0.1:47123"
	probeToken = "0123456789abcdef0123456789"
)

// answer is the one question each surface is asked: may it write local state,
// and may it post to a code host.
type answer struct{ local, host bool }

// postureCase is one input row. The wants are literals: local writes follow
// --read-only alone on every surface; a host post additionally needs the
// surface's own gate (serve --allow-post; mcp exposes the post tool unless
// read-only; lsp has no post path).
type postureCase struct {
	name      string
	readOnly  bool
	allowPost bool
	mcpWant   answer
	serveWant answer
	lspWant   answer
}

var postureCases = []postureCase{
	{
		name:      "default",
		mcpWant:   answer{local: true, host: true},
		serveWant: answer{local: true, host: false},
		lspWant:   answer{},
	},
	{
		name:      "allow-post",
		allowPost: true,
		mcpWant:   answer{local: true, host: true},
		serveWant: answer{local: true, host: true},
		lspWant:   answer{},
	},
	{
		name:     "read-only",
		readOnly: true,
		// --read-only blocks every local write and every host post, everywhere.
		mcpWant:   answer{},
		serveWant: answer{},
		lspWant:   answer{},
	},
}

// TestSurfacesAnswerTheSameWritePosture runs the three surfaces through the
// same inputs and checks each answer against the literal table, and the shared
// rule's own answer where the surface has the matching gate.
//
//nolint:revive // the per-surface comparison reads as one table assertion
func TestSurfacesAnswerTheSameWritePosture(t *testing.T) {
	t.Parallel()
	for _, tc := range postureCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mcpAnswer(tc.readOnly); got != tc.mcpWant {
				t.Errorf("mcp answer = %+v, want %+v", got, tc.mcpWant)
			}
			if got := serveAnswer(t, tc.readOnly, tc.allowPost); got != tc.serveWant {
				t.Errorf("serve answer = %+v, want %+v", got, tc.serveWant)
			}
			if got := lspAnswer(t); got != tc.lspWant {
				t.Errorf("lsp answer = %+v, want %+v", got, tc.lspWant)
			}
			rule := posture.Rule{ReadOnly: tc.readOnly, HostPostOptIn: tc.allowPost}
			if rule.LocalWrites() != tc.serveWant.local || rule.HostPost() != tc.serveWant.host {
				t.Errorf("shared rule = {local %v host %v}, want serve's %+v",
					rule.LocalWrites(), rule.HostPost(), tc.serveWant)
			}
		})
	}
}

// mcpAnswer reads the tool list the surface publishes: triage and adopt are
// local writers, post is the host writer.
func mcpAnswer(readOnly bool) answer {
	names := mcp.ToolNames(readOnly)
	return answer{
		local: slices.Contains(names, "triage") && slices.Contains(names, "adopt"),
		host:  slices.Contains(names, "post"),
	}
}

// serveAnswer drives the write routes over HTTP with full credentials, so the
// only thing that can stop a request is the posture. A local write answers yes
// when POST /v1/triage reaches its handler (anything but 405); a host post
// answers yes when POST /v1/post/submit gets past both 405 and post-disabled.
func serveAnswer(t *testing.T, readOnly, allowPost bool) answer {
	t.Helper()
	server, err := serve.New(&serve.Config{
		Root: t.TempDir(), Port: probePort, Token: probeToken,
		ReadOnly: readOnly, AllowPost: allowPost,
	})
	if err != nil {
		t.Fatalf("serve.New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	csrf := serveCSRF(t, ts)
	triage := servePost(t, ts, "/v1/triage", csrf)
	submit := servePost(t, ts, "/v1/post/submit", csrf)
	return answer{
		local: triage.status != http.StatusMethodNotAllowed,
		host: submit.status != http.StatusMethodNotAllowed &&
			!strings.Contains(submit.body, `"reason":"post-disabled"`),
	}
}

type probeResult struct {
	body   string
	status int
}

func serveCSRF(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = probeHost
	req.Header.Set("Authorization", "Bearer "+probeToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			CSRF string `json:"csrf"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("health body %s: %v", raw, err)
	}
	return env.Data.CSRF
}

func servePost(t *testing.T, ts *httptest.Server, path, csrf string) probeResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = probeHost
	req.Header.Set("Authorization", "Bearer "+probeToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+probeHost)
	req.Header.Set("X-Verdict-CSRF", csrf)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return probeResult{status: resp.StatusCode, body: string(raw)}
}

// lspAnswer initializes a session and reads what it advertises. lsp has no
// write path to gate: its only command recomputes diagnostics, and it offers
// no code action, rename, or edit provider, whatever flags it started with.
//
//nolint:revive // the capability walk reads as one probe
func lspAnswer(t *testing.T) answer {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":null,"capabilities":{}}}`
	shutdown := `{"jsonrpc":"2.0","id":2,"method":"shutdown"}`
	exit := `{"jsonrpc":"2.0","method":"exit"}`
	var in bytes.Buffer
	for _, frame := range []string{body, shutdown, exit} {
		fmt.Fprintf(&in, "Content-Length: %d\r\n\r\n%s", len(frame), frame)
	}
	var out bytes.Buffer
	server := lsp.New(&lsp.Config{Roots: []string{t.TempDir()}})
	if err := server.Serve(context.Background(), &in, &out); err != nil {
		t.Logf("lsp session ended with %v; the initialize reply is already written", err)
	}
	_, rest, ok := strings.Cut(out.String(), "\r\n\r\n")
	if !ok {
		t.Fatalf("no initialize reply in %q", out.String())
	}
	var reply struct {
		Result struct {
			Capabilities map[string]json.RawMessage `json:"capabilities"`
		} `json:"result"`
	}
	dec := json.NewDecoder(strings.NewReader(rest))
	if err := dec.Decode(&reply); err != nil {
		t.Fatalf("initialize reply %q: %v", rest, err)
	}
	readerOnly := []string{"positionEncoding", "textDocumentSync", "executeCommandProvider"}
	var extra []string
	for key := range reply.Result.Capabilities {
		if !slices.Contains(readerOnly, key) {
			extra = append(extra, key)
		}
	}
	var commands struct {
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal(reply.Result.Capabilities["executeCommandProvider"], &commands); err != nil {
		t.Fatal(err)
	}
	writes := len(extra) > 0 || !slices.Equal(commands.Commands, []string{lsp.CommandRefresh})
	return answer{local: writes, host: writes}
}
