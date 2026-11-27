//go:build !windows

package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// committedLinks are the symlinks TestReviewSnapshotHoldsNoSymlinks commits:
// absolute, climbing out, chained through another link, dangling, and a
// directory link that stays inside the tree. None reaches a view.
func committedLinks(resolved string) map[string]string {
	return map[string]string{
		"abs":      filepath.Join(resolved, "src"),
		"esc":      strings.Repeat("../", 24),
		"hop":      "abs",
		"dangling": "missing/x",
		"dir":      "src",
	}
}

// TestReviewSnapshotHoldsNoSymlinks proves a view holds none of the tree's
// symlinks — whatever they point at — names them in one warning, and a write
// by a dropped name lands in the view as a plain file, changing nothing of
// the operator's.
func TestReviewSnapshotHoldsNoSymlinks(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	writeUnder(t, root, filepath.Join("src", "main.go"), originalMain)
	links := commitLinks(t, root)
	var kept []string
	act := func(cmd *core.Command) {
		kept = writeThroughLinks(t, cmd, links)
		inView(t, cmd, viewReview, reviewDoc)
	}
	resp, ref, _ := confinedRun(t, root, "pi", act)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	if len(kept) > 0 {
		t.Errorf("the view kept the symlinks %q", kept)
	}
	assertCheckoutUntouched(t, root)
	assertWarned(t, resp.Warnings, "the review snapshot leaves out the tree's 5 symlink(s): abs, dangling, dir, esc, hop")
	assertWarned(t, resp.Warnings, "pi wrote outside its review artifacts: abs, dangling, dir, esc, hop")
}

// commitLinks commits committedLinks in root's checkout and returns them.
func commitLinks(t *testing.T, root string) map[string]string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	links := committedLinks(resolved)
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	testenv.Checkout(t, root)
	return links
}

// writeThroughLinks writes by every link's name in the view and returns the
// names that were still links there.
func writeThroughLinks(t *testing.T, cmd *core.Command, links map[string]string) []string {
	t.Helper()
	var kept []string
	for name := range links {
		if info, err := os.Lstat(filepath.Join(cmd.Dir, name)); err == nil && info.Mode()&os.ModeSymlink != 0 {
			kept = append(kept, name)
		}
		inView(t, cmd, name, "planted\n")
	}
	return kept
}

// TestReadPinnedNeverWaitsOnAFIFO proves an artifact swapped for a FIFO after
// the walk saw a file is refused at once: the read never waits for a writer.
func TestReadPinnedNeverWaitsOnAFIFO(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUnder(t, dir, reviewFile, reviewDoc)
	walked, err := os.Lstat(filepath.Join(dir, reviewFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, reviewFile)); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, reviewFile), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	done := make(chan string, 1)
	go func() {
		_, _, why := readPinned(root, reviewFile, walked, maxArtifactBytes)
		done <- why
	}()
	var why string
	testenv.Until(t, func() bool {
		select {
		case why = <-done:
			return true
		default:
			return false
		}
	})
	if !slices.Contains([]string{whyNotRegular, whyUnreadable}, why) {
		t.Errorf("readPinned = %q, want the FIFO refused", why)
	}
}
