package trendcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestTrendRefusesAStoreItCannotList proves a bundle home that is not a
// directory refuses corrupt-store naming it: a discarded ReadDir error would
// let trend report an empty rollup for a store an operator can see is there.
func TestTrendRefusesAStoreItCannotList(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, ".verdict", "state")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := core.ReadRequest{Runtime: &core.Runtime{}, RepoRoot: root}
	_, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref == nil || ref.Code != "corrupt-store" {
		t.Fatalf("trend over a file store = %v; want corrupt-store", ref)
	}
	if !strings.Contains(ref.Message, ".verdict/state") {
		t.Errorf("message = %q; want it to name the store", ref.Message)
	}
}
