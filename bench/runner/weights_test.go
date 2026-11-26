package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRootWeightsRefusalNamesTheConfig proves a bad repository config names
// the file it read, like every other config parse refusal.
func TestRootWeightsRefusalNamesTheConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".verdict", "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := rootWeights(root)
	const want = `weights: bad-config: .verdict/config.json: schema_version: missing format marker, want the string "1"`
	if err == nil || err.Error() != want {
		t.Fatalf("rootWeights = %v, want %q", err, want)
	}
}
