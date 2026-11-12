package recordcore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// TestRecordRefusesBundleSymlink verifies the bundle refusal.
func TestRecordRefusesBundleSymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict/state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".verdict/state/42")); err != nil {
		t.Fatal(err)
	}
	_, ref := recordcore.Run(t.Context(), &core.RecordRequest{RepoRoot: root, Pr: "42"})
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("refusal = %v; want bad-config", ref)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("target entries = %v, error = %v", entries, err)
	}
}
