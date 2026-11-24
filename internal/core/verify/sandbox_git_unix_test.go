//go:build unix

package verifycore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSandboxArgvRefusesASymlinkedGitDir keeps the Linux shape from binding
// only a symlinked `.git`'s target while the link itself stays replaceable in
// the writable root.
func TestSandboxArgvRefusesASymlinkedGitDir(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.Symlink(target, filepath.Join(root, ".git")))
	argv, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("argv=%v ref=%v, want sandbox-unavailable", argv, ref)
	}
	want := "sandbox unavailable: the reviewed root's " + filepath.Join(root, ".git") + " is a symlink, and bwrap " +
		"can protect only its target, so the link could be replaced with a git directory that runs later — " +
		"replace the link with the directory or a `gitdir:` file, or verify a fresh clone"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
}
