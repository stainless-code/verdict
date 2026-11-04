package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestArtifactKeepsTheLinkAndTheMode checks the MCP report tool writes its
// artifact through the operator's path: a symlink survives and only its target
// gains the bytes, with the target's own mode kept.
func TestArtifactKeepsTheLinkAndTheMode(t *testing.T) {
	target, link := seedArtifactLink(t)
	if ref := writeArtifact(&core.FileArtifact{Path: link, Content: []byte("# new\n")}); ref != nil {
		t.Fatalf("writeArtifact(%s) = %q %q; want success", link, ref.Code, ref.Message)
	}
	checkArtifactLink(t, link, target)
	checkArtifactFile(t, target, "# new\n", 0o640)
}

// TestArtifactRefusalKeepsTheOldFile checks a failed artifact write refuses
// report-failed and leaves the previous file intact.
func TestArtifactRefusalKeepsTheOldFile(t *testing.T) {
	path := seedUnwritableArtifact(t)
	ref := writeArtifact(&core.FileArtifact{Path: path, Content: []byte("{}\n")})
	if ref == nil {
		t.Fatal("want a report-failed refusal, got success")
	}
	if ref.Code != "report-failed" {
		t.Errorf("code = %q; want report-failed", ref.Code)
	}
	if !strings.Contains(ref.Message, path) {
		t.Errorf("message = %q; want it to name %s", ref.Message, path)
	}
	checkArtifactFile(t, path, "old\n", 0o644)
}

// seedArtifactLink writes a 0640 target and a symlink to it, as an operator
// who keeps reports under a stable link would have.
func seedArtifactLink(t *testing.T) (target, link string) {
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

// seedUnwritableArtifact writes a known artifact into a directory that denies
// creation. The mode is restored for TempDir cleanup.
func seedUnwritableArtifact(t *testing.T) (path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "out.sarif")
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

// checkArtifactLink fails unless link is still a symlink naming target.
func checkArtifactLink(t *testing.T, link, target string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("artifact mode = %v; want the symlink preserved", fi.Mode())
	}
	if dest, err := os.Readlink(link); err != nil {
		t.Fatal(err)
	} else if dest != target {
		t.Errorf("link destination = %q; want %q", dest, target)
	}
}

// checkArtifactFile fails unless path holds exactly want under want mode.
func checkArtifactFile(t *testing.T, path, want string, mode os.FileMode) {
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
