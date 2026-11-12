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
	for _, fixture := range []struct{ component, tail string }{
		{".verdict", "state/42"},
		{".verdict/state", "42"},
		{".verdict/state/42", ""},
	} {
		t.Run(fixture.component, func(t *testing.T) { checkBundleSymlink(t, fixture.component, fixture.tail) })
	}
}

func checkBundleSymlink(t *testing.T, component, tail string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "other")
	bundle := filepath.Join(target, filepath.FromSlash(tail))
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, filepath.FromSlash(component))
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, ref := recordcore.Run(t.Context(), &core.RecordRequest{RepoRoot: root, Pr: "42"})
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("refusal = %v; want bad-config", ref)
	}
	entries, err := os.ReadDir(bundle)
	if err != nil || len(entries) != 0 {
		t.Fatalf("target entries = %v, error = %v", entries, err)
	}
}
