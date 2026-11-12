package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
)

// TestPackToolCarriesWarnings proves the pack tool result repeats the
// repo-layer warnings: an MCP caller never sees the stderr warn line, so a
// dropped list hides the stray file entirely.
func TestPackToolCarriesWarnings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	packs := filepath.Join(root, ".verdict", "packs")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packs, "README.md"), []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := &packcore.Request{List: true}
	req.ReadRequest = core.ReadRequest{
		RepoRoot: root,
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
	}
	data, ref := findTool(t, "pack").invoke(context.Background(), req)
	if ref != nil {
		t.Fatalf("pack invoke refusal: %s: %s", ref.Code, ref.Message)
	}
	var result struct {
		Packs    []any    `json:"packs"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rawMarshal(data), &result); err != nil {
		t.Fatalf("decode pack result: %v", err)
	}
	want := []string{".verdict/packs/README.md is not a pack directory — ignored"}
	if !slices.Equal(result.Warnings, want) {
		t.Fatalf("warnings = %q, want %q", result.Warnings, want)
	}
	if result.Packs == nil || len(result.Packs) != 0 {
		t.Fatalf("packs = %v, want an empty array beside the warning", result.Packs)
	}
}
