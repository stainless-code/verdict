package atomic_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/atomic"
)

func TestCreateFileNeverReplacesExistingBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence")
	if err := os.WriteFile(path, []byte("old evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	created, err := atomic.CreateFileModeIn(root, "evidence", []byte("different evidence"), 0o600)
	if err != nil || created {
		t.Errorf("existing name: created=%v, error=%v", created, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "old evidence" {
		t.Fatalf("existing bytes = %q, %v", raw, err)
	}
}
