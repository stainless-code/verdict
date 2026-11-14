package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

type prWaitStubCommander struct{}

func (prWaitStubCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "--include"):
		return core.Result{Stdout: "HTTP/2.0 200 OK\r\n\r\n" + `{"state":"open","head":{"sha":"abc"}}`}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"headRefOid":"abc","statusCheckRollup":[],"reviews":[]}`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	}
	return core.Result{ExitCode: 1}, nil
}

func TestServerPRWaitOnceWithoutTimeout(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig()
	cfg.Commanders = map[string]core.Commander{"pr_wait": prWaitStubCommander{}}
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","once":true}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","once":true,"timeout":"0s"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","once":true,"interval":"1ms"}}}`,
	)
	var res callToolResult
	if err := json.Unmarshal(frames["1"].Result, &res); err != nil {
		t.Fatalf("id 1 unmarshal: %v", err)
	}
	var got struct {
		Outcome      string `json:"outcome"`
		Observations int    `json:"observations"`
	}
	if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
		t.Fatalf("id 1 structuredContent: %v", err)
	}
	if res.IsError || got.Outcome != "condition-unmet" || got.Observations != 1 {
		t.Fatalf("id 1 got isError=%v data=%+v body=%s", res.IsError, got, res.StructuredContent)
	}
	assertToolRefusalCode(t, "2", frames["2"], "bad-flags")
	assertToolRefusalCode(t, "3", frames["3"], "bad-flags")
}

func TestServerWaitBoundRefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","timeout":"51s"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ready","arguments":{"repo":"acme/app","pr":"42","wait_for":"build","timeout":"51s"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","timeout":"0s"}}}`,
	)
	for _, id := range []string{"1", "2", "3"} {
		assertToolRefusalCode(t, id, frames[id], "bad-flags")
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(broken)
	rootFrames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","timeout":"51s"}}}`,
	)
	assertToolRefusalCode(t, "4", rootFrames["4"], "not-a-repo")
}

func assertToolRefusalCode(t *testing.T, id string, f frame, wantCode string) {
	t.Helper()
	var res callToolResult
	if err := json.Unmarshal(f.Result, &res); err != nil {
		t.Fatalf("id %s unmarshal: %v", id, err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
		t.Fatalf("id %s structuredContent: %v", id, err)
	}
	if !res.IsError || got["code"] != wantCode {
		t.Fatalf("id %s got isError=%v code=%v body=%s; want %s", id, res.IsError, got["code"], res.StructuredContent, wantCode)
	}
}
