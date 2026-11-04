package report

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestWriteArtifactKeepsTheLinkAndTheMode checks `report --output` writes
// through the operator's path: a symlink survives and only its target gains
// the new bytes, and the target's own mode is kept.
func TestWriteArtifactKeepsTheLinkAndTheMode(t *testing.T) {
	skipWithoutPOSIXModes(t)
	target, link := seedLink(t)
	if ref := writeArtifact(&core.FileArtifact{Path: link, Content: []byte("# new\n")}); ref != nil {
		t.Fatalf("writeArtifact(%s) = %q %q; want success", link, ref.Code, ref.Message)
	}
	checkLink(t, link, target)
	checkFile(t, target, "# new\n", 0o640)
}

// TestWriteArtifactRefusalKeepsTheOldFile checks a failed artifact write is a
// typed report-failed refusal that leaves the previous report intact, so a
// caller never reads a half-written file.
func TestWriteArtifactRefusalKeepsTheOldFile(t *testing.T) {
	skipWithoutPOSIXModes(t)
	path := seedUnwritable(t)
	ref := writeArtifact(&core.FileArtifact{Path: path, Content: []byte("new report\n")})
	if ref == nil {
		t.Fatal("want a report-failed refusal, got success")
	}
	if ref.Code != "report-failed" {
		t.Errorf("code = %q; want report-failed", ref.Code)
	}
	if !strings.Contains(ref.Message, path) {
		t.Errorf("message = %q; want it to name %s", ref.Message, path)
	}
	checkFile(t, path, "old\n", 0o644)
}

// skipWithoutPOSIXModes skips a test that reads or denies POSIX permission
// bits, which Windows does not model (a directory mode cannot deny creation
// and every file reports 0666).
func skipWithoutPOSIXModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not model POSIX permission bits")
	}
}

// seedLink writes a 0640 target and a symlink to it, as an operator who keeps
// reports under a stable link would have.
func seedLink(t *testing.T) (target, link string) {
	t.Helper()
	dir := t.TempDir()
	target, link = filepath.Join(dir, "review.md"), filepath.Join(dir, "out.md")
	if err := os.WriteFile(target, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	return target, link
}

// seedUnwritable writes a known report into a directory that denies creation,
// so no staged temp can be made. The mode is restored for TempDir cleanup.
func seedUnwritable(t *testing.T) (path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore directory mode: %v", err)
		}
	})
	return path
}

// checkLink fails unless link is still a symlink naming target.
func checkLink(t *testing.T, link, target string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output mode = %v; want the symlink preserved", fi.Mode())
	}
	if dest, err := os.Readlink(link); err != nil {
		t.Fatal(err)
	} else if dest != target {
		t.Errorf("link destination = %q; want %q", dest, target)
	}
}

// checkFile fails unless path holds exactly want bytes under want mode.
func checkFile(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Errorf("%s content = %q; want %q", filepath.Base(path), raw, want)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != mode {
		t.Errorf("%s mode = %o; want %o", filepath.Base(path), fi.Mode().Perm(), mode)
	}
}
