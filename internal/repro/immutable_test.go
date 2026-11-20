package repro

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecapturePreservesOldEvidence(t *testing.T) {
	dir := t.TempDir()
	first, err := Capture(dir, testSig, "", "false", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Capture(dir, testSig, "", "true", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Path != "tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-a17fcf0a2f50.sh" || first.Path == second.Path {
		t.Fatalf("paths = %q, %q", first.Path, second.Path)
	}
	assertRetainedBytes(t, dir, first.Path, "false\n")
	assertRetainedBytes(t, dir, second.Path, "true\n")
}

func TestIdenticalRecaptureIsNoOp(t *testing.T) {
	dir := t.TempDir()
	receipt, err := Capture(dir, testSig, "", "false", false)
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{filepath.Join(dir, filepath.FromSlash(receipt.Path)), filepath.Join(dir, "tests", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.command.json")}
	stamp := time.Unix(1000000000, 0)
	stampRetainedFiles(t, paths, stamp)
	if _, err := Capture(dir, testSig, "", "false", false); err != nil {
		t.Fatal(err)
	}
	checkRetainedTimes(t, paths, stamp)
}

func TestCaptureRejectsConflictingBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tests", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.sh")
	if err := os.WriteFile(path, []byte("old evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(dir, testSig, "", "false", false); err == nil {
		t.Fatal("accepted different bytes at an immutable name")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "old evidence\n" {
		t.Fatalf("previous bytes = %q, %v", raw, err)
	}
}

func TestReadRejectsContentHashMismatch(t *testing.T) {
	dir := t.TempDir()
	receipt, err := Capture(dir, testSig, "", "false", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(receipt.Path)), []byte("true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir, testSig, receipt.Path); err == nil {
		t.Fatal("accepted modified evidence")
	}
}

func assertRetainedBytes(t *testing.T, dir, path, want string) {
	t.Helper()
	raw, err := Read(dir, testSig, path)
	if err != nil || string(raw) != want {
		t.Fatalf("retained %s = %q, %v", path, raw, err)
	}
}

func TestFailedSidecarWritePreservesOldEvidence(t *testing.T) {
	dir := t.TempDir()
	first, err := Capture(dir, testSig, "", "false", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = capture(dir, testSig, "", "true", false, func(root *os.Root, name string, data []byte) error {
		if strings.HasSuffix(name, ".command.json") {
			return errors.New("injected sidecar write failure")
		}
		return writeImmutable(root, name, data)
	})
	if err == nil || !strings.Contains(err.Error(), "injected sidecar write failure") {
		t.Fatalf("sidecar write did not fail: %v", err)
	}
	assertRetainedBytes(t, dir, first.Path, "false\n")
}

func stampRetainedFiles(t *testing.T, paths []string, stamp time.Time) {
	t.Helper()
	for _, path := range paths {
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

func checkRetainedTimes(t *testing.T, paths []string, stamp time.Time) {
	t.Helper()
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(stamp) {
			t.Fatalf("identical recapture replaced %s: %v", path, info.ModTime())
		}
	}
}
