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

// TestSandboxArgvRefusesAGitDirReachedThroughAnInRootSymlink keeps a
// `gitdir:` path that crosses a symlink inside the writable root from being
// bound by its target alone: the link could be replaced, and git follows it.
func TestSandboxArgvRefusesAGitDirReachedThroughAnInRootSymlink(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "real", "gitdir"), 0o755))
	mustSandboxFS(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: link/gitdir\n"), 0o600))
	requireGitRefusal(t, root, "the git location "+filepath.Join(root, "link", "gitdir")+
		" passes through the symlink "+filepath.Join(root, "link")+" inside the reviewed root")
}

// TestSandboxArgvRefusesAGitDirLinkedOutsideTheRoot pins the escape where the
// in-root link points outside the root: the target needs no bind (it is
// read-only already), but the link itself sits in the writable root, so the
// command could swap it for a directory with hooks. It refuses.
func TestSandboxArgvRefusesAGitDirLinkedOutsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.Symlink(outside, filepath.Join(root, "link")))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: link\n"), 0o600))
	requireGitRefusal(t, root, "the git location "+filepath.Join(root, "link")+
		" passes through the symlink "+filepath.Join(root, "link")+" inside the reviewed root")
}

// TestSandboxArgvRefusesACommondirLinkedOutsideTheRoot pins the same escape
// one pointer further: a commondir that reaches outside the root through an
// in-root symlink.
func TestSandboxArgvRefusesACommondirLinkedOutsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	mustSandboxFS(t, os.Symlink(outside, filepath.Join(root, "shared")))
	mustSandboxFS(t, os.WriteFile(filepath.Join(root, ".git", "commondir"), []byte("../shared\n"), 0o600))
	requireGitRefusal(t, root, "the git location "+filepath.Join(root, "shared")+
		" passes through the symlink "+filepath.Join(root, "shared")+" inside the reviewed root")
}

// TestSandboxArgvNamesTheRealInspectError keeps a `.git` that cannot be
// inspected from being reported as missing: a root that is a file fails
// Lstat with ENOTDIR, and the refusal names that error.
func TestSandboxArgvNamesTheRealInspectError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file-root")
	mustSandboxFS(t, os.WriteFile(root, []byte("x"), 0o600))
	_, ref := bwrapArgv(root, homeInputs(t.TempDir()))
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	want := "sandbox unavailable: could not inspect " + filepath.Join(root, ".git") + " (lstat " +
		filepath.Join(root, ".git") + ": not a directory), so it cannot be protected"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
}
