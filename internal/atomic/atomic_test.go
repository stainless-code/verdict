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
// umask-022 default mode and no temp leftovers.
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
	checkStoreMode(t, fi)
	leftovers, err := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp leftovers = %v; want none", leftovers)
	}
}

// TestWriteErrSidecar checks stderr sidecars land exact bytes with the
// owner-only mode and no temp leftovers.
func TestWriteErrSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".post.err")
	data := []byte("HTTP 401: bad credentials\n")
	if err := atomic.WriteErrSidecar(path, data); err != nil {
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
	checkSidecarMode(t, fi)
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
	path := seedWriteFailure(t)
	if err := atomic.WriteFile(path, []byte("replacement\n")); err == nil {
		t.Fatal("want a failed write, got nil")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "original\n" {
		t.Errorf("after failed write = %q; want original bytes intact", raw)
	}
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
	assertNoTempFiles(t, dir)
}

// TestWriteFileRenameFailureLeavesNoTemp covers a failure after the temp
// file exists: the target is a non-empty directory, so the rename fails and
// the deferred cleanup must remove the staged temp (the earlier failure leg
// dies in CreateTemp, before any temp exists).
func TestWriteFileRenameFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomic.WriteFile(target, []byte("replacement\n")); err == nil {
		t.Fatal("want a failed rename, got nil")
	}
	assertNoTempFiles(t, dir)
	raw, err := os.ReadFile(filepath.Join(target, "keep"))
	if err != nil || string(raw) != "keep\n" {
		t.Errorf("failed rename changed the target: %q, %v", raw, err)
	}
}

// assertNoTempFiles fails on any staged atomic temp left in dir.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp leftover %q after failed write", e.Name())
		}
	}
}
