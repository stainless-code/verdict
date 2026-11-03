package mcp

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateToolsList rewrites the tools/list wire snapshots instead of comparing
// them. Refresh them only for a deliberate contract change.
var updateToolsList = flag.Bool("update-tools-list", false, "regenerate the tools/list wire snapshots")

// TestToolsListWireSnapshot pins the exact bytes of the tools/list result on
// every surface. The frame is the agent-facing contract: names, descriptions,
// generated schemas, required keys, enums, and annotations all ride in it.
//
//nolint:revive,gocognit // the surface matrix reads as one setup/compare block
func TestToolsListWireSnapshot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		readOnly bool
		operator bool
	}{
		{name: "default"},
		{name: "read-only", readOnly: true},
		{name: "operator", operator: true},
	}
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			cfg.ReadOnly = tc.readOnly
			cfg.Operator = tc.operator
			var out bytes.Buffer
			if err := New(&cfg).Serve(context.Background(), strings.NewReader(line+"\n"), &out); err != nil {
				t.Fatalf("Serve: %v", err)
			}
			got := out.Bytes()
			path := filepath.Join("testdata", "tools_list_"+strings.ReplaceAll(tc.name, " ", "_")+".jsonl")
			if *updateToolsList {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update-tools-list to create it)", path, err)
			}
			if bytes.Equal(got, want) {
				return
			}
			t.Errorf("tools/list bytes differ from %s: run with -update-tools-list, then review the diff\n%s",
				path, firstByteDiff(got, want))
		})
	}
}

// firstByteDiff locates the first byte where got and want differ and renders a
// short window around it, so a protocol drift is readable in test output.
func firstByteDiff(got, want []byte) string {
	limit := min(len(got), len(want))
	at := limit
	for i := range limit {
		if got[i] != want[i] {
			at = i
			break
		}
	}
	start := max(0, at-60)
	end := min(max(len(got), len(want)), at+60)
	return fmt.Sprintf("first difference at byte %d:\n got: %q\nwant: %q",
		at, window(got, start, end), window(want, start, end))
}

// window clamps bytes to the requested span.
func window(raw []byte, start, end int) []byte {
	if start >= len(raw) {
		return nil
	}
	return raw[start:min(end, len(raw))]
}
