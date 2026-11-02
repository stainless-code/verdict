package atomic_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/atomic"
)

// TestWriteFileRoundTrip checks the atomic path writes exact bytes with the
// shell's umask-022 default mode and no temp leftovers.
func TestWriteFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	data := []byte("{\n  \"schema_version\": \"1\"\n}\n")
	if err := atomic.WriteFile(path, data); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, data) {
		t.Errorf("round trip = %q; want %q", raw, data)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o; want 644", fi.Mode().Perm())
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp leftovers = %v; want none", leftovers)
	}
}

// TestWriteFileOverwrite checks an overwrite replaces bytes (rename, never a
// torn middle: readers see old or new, never partial).
func TestWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	if err := atomic.WriteFile(path, []byte("old\n")); err != nil {
		t.Fatal(err)
	}
	if err := atomic.WriteFile(path, []byte("new content here\n")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new content here\n" {
		t.Errorf("overwrite = %q; want new content", raw)
	}
}

// TestWriteFileFailureKeepsOriginal checks a failed write leaves the original
// intact and reports an error.
func TestWriteFileFailureKeepsOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission gate does not fail for root")
	}
	_, path := seedReadOnly(t)
	if err := atomic.WriteFile(path, []byte("replacement\n")); err == nil {
		t.Fatal("want error writing into a read-only dir, got nil")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "original\n" {
		t.Errorf("after failed write = %q; want original bytes intact", raw)
	}
}

// seedReadOnly creates a file inside a read-only dir, restoring perms after.
func seedReadOnly(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "store.json")
	if err := atomic.WriteFile(path, []byte("original\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restore perms: %v", err)
		}
	})
	return dir, path
}

// TestWriteFileMissingDir checks a write below a missing directory errors.
func TestWriteFileMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "store.json")
	if err := atomic.WriteFile(path, []byte("x\n")); err == nil {
		t.Fatal("want error for a missing parent dir, got nil")
	}
}

// TestWriteFileNoTempOnFailure checks a failed write leaves no temp files.
func TestWriteFileNoTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	if err := atomic.WriteFile(filepath.Join(dir, "no-such-dir", "f"), []byte("x\n")); err == nil {
		t.Fatal("want error, got nil")
	}
	var names []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, n := range names {
		if strings.HasPrefix(n, ".tmp-") {
			t.Errorf("temp leftover %q after failed write", n)
		}
	}
}
