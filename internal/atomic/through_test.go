package atomic_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stainless-code/verdict/internal/atomic"
)

// TestWriteThroughKeepsTheExistingMode checks an overwrite keeps the mode the
// operator's file already had instead of forcing the caller's default.
func TestWriteThroughKeepsTheExistingMode(t *testing.T) {
	skipWithoutPOSIXModes(t)
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := atomic.WriteThrough(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkContent(t, path, "new\n")
	checkMode(t, path, 0o640)
}

// TestWriteThroughReplacesTheSymlinksTarget checks a symlinked output path is
// written through: the link survives and only the file it names changes.
func TestWriteThroughReplacesTheSymlinksTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "review.md")
	link := filepath.Join(dir, "out.md")
	seedLink(t, target, link)
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	// On Windows os.Stat defers reading the file id until the first SameFile,
	// so a late read would see the replacement under the same path; comparing
	// before with itself loads and caches the original id now.
	_ = os.SameFile(before, before)
	if err := atomic.WriteThrough(link, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Compare identity after ONE write, while the original's inode is still
	// the only one freed: a second write's staged file may reuse that inode
	// number (ext4, tmpfs, NTFS file ids), which would read as "rewritten in
	// place".
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("target was rewritten in place; want a staged file renamed over it")
	}
	if err := atomic.WriteThrough(link, []byte("newer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkLink(t, link, target)
	checkContent(t, target, "newer\n")
}

// TestWriteThroughCreatesThroughADanglingLink checks a link whose target does
// not exist yet still gets its target created, with the link left alone and
// the caller's mode on the new file.
func TestWriteThroughCreatesThroughADanglingLink(t *testing.T) {
	skipWithoutPOSIXModes(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "review.md")
	link := filepath.Join(dir, "out.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if err := atomic.WriteThrough(link, []byte("# new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkLink(t, link, target)
	checkContent(t, target, "# new\n")
	checkMode(t, target, 0o600)
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

// seedLink writes target with a distinctive 0640 mode and links link to it.
func seedLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.WriteFile(target, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
}

// checkLink fails unless link is still a symlink naming target.
func checkLink(t *testing.T, link, target string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link mode = %v; want the symlink preserved, not replaced by the data", fi.Mode())
	}
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if dest != target {
		t.Errorf("link destination = %q; want %q", dest, target)
	}
}

// checkContent fails unless path holds exactly want.
func checkContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Errorf("%s content = %q; want %q", filepath.Base(path), raw, want)
	}
}

// checkMode fails unless path's permission bits are want.
func checkMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != want {
		t.Errorf("%s mode = %o; want %o", filepath.Base(path), fi.Mode().Perm(), want)
	}
}
