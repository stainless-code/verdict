package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestServerWaitBoundRefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","timeout":"51s"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ready","arguments":{"repo":"acme/app","pr":"42","wait_for":"build","timeout":"51s"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","timeout":"0s"}}}`,
	)
	for _, id := range []string{"1", "2", "3"} {
		var res callToolResult
		if err := json.Unmarshal(frames[id].Result, &res); err != nil {
			t.Fatalf("id %s unmarshal: %v", id, err)
		}
		var got map[string]any
		if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
			t.Fatalf("id %s structuredContent: %v", id, err)
		}
		if !res.IsError || got["code"] != "bad-flags" {
			t.Fatalf("id %s got isError=%v code=%v body=%s; want bad-flags", id, res.IsError, got["code"], res.StructuredContent)
		}
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(broken)
	rootFrames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pr_wait","arguments":{"repo":"acme/app","pr":"42","until":"merged","timeout":"51s"}}}`,
	)
	var rootRes callToolResult
	if err := json.Unmarshal(rootFrames["4"].Result, &rootRes); err != nil {
		t.Fatal(err)
	}
	var rootGot map[string]any
	if err := json.Unmarshal(rootRes.StructuredContent, &rootGot); err != nil {
		t.Fatal(err)
	}
	if !rootRes.IsError || rootGot["code"] != "not-a-repo" {
		t.Fatalf("broken checkout got isError=%v code=%v; want not-a-repo", rootRes.IsError, rootGot["code"])
	}
}
