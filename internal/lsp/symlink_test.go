package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
}

// TestCheckoutProblemResolvesSymlinks pins that the checkout guard reads the
// physical directory: a link inside a checkout that points outside every
// checkout is refused (naming where it resolves), while a link that lands
// inside a checkout is served.
func TestCheckoutProblemResolvesSymlinks(t *testing.T) {
	t.Parallel()
	repo, outside := checkout(t), t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, outside, filepath.Join(repo, "escape"))
	symlink(t, filepath.Join(repo, "sub"), filepath.Join(repo, "inner"))
	symlink(t, repo, filepath.Join(outside, "back"))
	physicalOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dir, want string
	}{
		{filepath.Join(repo, "escape"), "it resolves to " + physicalOutside + ", which is not inside a git checkout"},
		{filepath.Join(repo, "inner"), ""},
		{filepath.Join(outside, "back"), ""},
		{filepath.Join(repo, "missing"), "it is not an existing directory"},
	} {
		if got := CheckoutProblem(tc.dir); got != tc.want {
			t.Errorf("CheckoutProblem(%s) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// TestSymlinkedClientRootIsDropped pins the same guard for a client
// workspace folder: a folder that links out of the checkout is dropped with
// a warning and nothing under it is served.
func TestSymlinkedClientRootIsDropped(t *testing.T) {
	t.Parallel()
	repo, outside := checkout(t), t.TempDir()
	writeBundle(t, outside, "local-1", findingJSON(sigCrit, "open", "critical", "x.go", 1))
	link := filepath.Join(repo, "escape")
	symlink(t, outside, link)
	s, _ := workspaceSession(t, map[string]any{"workspaceFolders": []any{folder(link)}})
	s.notify(methodInitialized, map[string]any{})
	frames := s.barrier(2, 0)
	texts := strings.Join(logs(t, frames), "\n")
	if countMethod(frames, methodPublish) != 0 || !strings.Contains(texts, "dropped: it resolves to") {
		t.Fatalf("a symlinked-out folder: %d publishes, logs %q", countMethod(frames, methodPublish), texts)
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}
