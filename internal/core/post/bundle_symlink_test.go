package postcore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestPostRefusesBundleSymlink verifies the published refusal or read contract.
func TestPostRefusesBundleSymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict/state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".verdict/state/42")); err != nil {
		t.Fatal(err)
	}
	_, ref := Run(t.Context(), &core.PostRequest{RepoRoot: root, Pr: "42", Flags: core.PostFlags{Approve: "false", DryRun: true}})
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("refusal = %v; want bad-config", ref)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("target entries = %v, error = %v", entries, err)
	}
}
