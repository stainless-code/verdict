package fetchcore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestWriteIdentityFileRawDanglingSymlink refuses dangling raw entries.
func TestWriteIdentityFileRawDanglingSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte("filtered bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-raw-input", filepath.Join(dir, "full.raw.diff")); err != nil {
		t.Fatal(err)
	}
	_, err := writeIdentityFile(context.Background(), nil, dir, "head", "base")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != CodeFetchFailed {
		t.Fatalf("writeIdentityFile with a dangling raw entry = %v, want fetch-failed", err)
	}
}
