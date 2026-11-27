//go:build !windows

package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/contenttree"
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

// TestSharedSnapshotHoldsNoSymlinkAndIsReadOnly proves the shared snapshot
// every copy is made from — which sits beside each copy, at `../..` — holds
// none of the tree's symlinks and nothing writable: a committed link to the
// operator's own source, written through from a copy, changes nothing.
func TestSharedSnapshotHoldsNoSymlinkAndIsReadOnly(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	writeUnder(t, root, filepath.Join("src", "main.go"), originalMain)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(resolved, "src", "main.go"), filepath.Join(root, "src", "escape")); err != nil {
		t.Fatal(err)
	}
	testenv.Checkout(t, root)
	var found []string
	act := func(cmd *core.Command) {
		found = probeSharedSnapshot(t, cmd)
		inView(t, cmd, viewReview, reviewDoc)
	}
	if _, ref, _ := confinedRun(t, root, "pi", act); ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	if want := []string{"snapshot"}; !slices.Equal(found, want) {
		t.Errorf("the shared snapshot showed %q, want only itself, sealed", found)
	}
	assertCheckoutUntouched(t, root)
}

// probeSharedSnapshot finds the shared snapshot beside the copy cmd runs in
// and reports it, every symlink, every writable entry, and every entry the
// group or others may read in it, after
// writing through the committed src/escape link as a hostile agent would.
func probeSharedSnapshot(t *testing.T, cmd *core.Command) []string {
	t.Helper()
	var found []string
	wrappers, _ := filepath.Glob(filepath.Join(cmd.Dir, "..", "..", "*", "src")) //nolint:errcheck // a constant pattern
	for _, src := range wrappers {
		snap := filepath.Dir(src)
		found = append(found, "snapshot")
		_ = os.WriteFile(filepath.Join(src, "escape"), []byte("planted\n"), 0o600) //nolint:errcheck // a refused write is the point
		found = append(found, unsealed(snap)...)
	}
	return found
}

// unsealed names every symlink, every writable entry, and every entry the
// group or others may read, below dir.
func unsealed(dir string) []string {
	var found []string
	_ = filepath.WalkDir(dir, func(p string, _ os.DirEntry, _ error) error { //nolint:errcheck // reports below
		if info, err := os.Lstat(p); err == nil {
			found = append(found, unsealedEntry(p, info)...)
		}
		return nil
	})
	return found
}

// unsealedEntry names one entry when it is a symlink or writable.
func unsealedEntry(p string, info os.FileInfo) []string {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return []string{"link=" + p}
	case info.Mode().Perm()&0o222 != 0:
		return []string{"writable=" + p}
	case info.Mode().Perm()&0o077 != 0:
		return []string{"shared=" + p}
	}
	return nil
}

// TestRestoredRewriteOfTheSnapshotRefusesTheNextCopy proves a rewrite of the
// shared snapshot that restores a file's size, mode, and modification time
// — chmod u+w, write, chmod u-w, touch — still refuses the next copy: the
// write moved the file's change time, which no user-space call can set.
func TestRestoredRewriteOfTheSnapshotRefusesTheNextCopy(t *testing.T) {
	t.Parallel()
	ws, root := fakeCanonical(t, map[string]string{"src/main.go": originalMain})
	expect := expectedArtifacts(here, reviewFile, nil)
	if _, err := ws.open(t.Context(), bundleDir(root), here, expect, 30); err != nil {
		t.Fatalf("a sealed snapshot refused its first copy: %v", err)
	}
	rewriteRestored(t, filepath.Join(ws.tree, "src", "main.go"), strings.Replace(originalMain, "original", "poisoned", 1))
	_, err := ws.open(t.Context(), bundleDir(root), here, expect, 30)
	if err == nil || !strings.Contains(err.Error(), "changed after it was taken") {
		t.Errorf("a restored rewrite of the snapshot gave a copy (err = %v)", err)
	}
}

// rewriteRestored rewrites the file at path in place with body, which must be
// its size, then restores its mode and modification time.
func rewriteRestored(t *testing.T, path, body string) {
	t.Helper()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(body)) != before.Size() {
		t.Fatalf("the rewrite is %d bytes, want the file's %d", len(body), before.Size())
	}
	if err := os.Chmod(path, before.Mode().Perm()|0o200); err != nil {
		t.Fatal(err)
	}
	writeInPlace(t, path, body)
	if err := os.Chmod(path, before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
}

// writeInPlace writes body over the file at path without replacing it.
func writeInPlace(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestSnapshotBaseRefusesALinkedCacheComponent proves a link inside the cache
// directory where verdict would create its snapshot directory — `verdict`
// pointing into the checkout — refuses before anything is created through it.
func TestSnapshotBaseRefusesALinkedCacheComponent(t *testing.T) {
	t.Parallel()
	tmp := contenttree.Resolved(t.TempDir())
	root := filepath.Join(tmp, "repo")
	cache := contenttree.Resolved(t.TempDir())
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(cache, "verdict")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotBase(root, tmp, map[string]string{"XDG_CACHE_HOME": cache}, true); err == nil {
		t.Error("a linked cache component was followed")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) > 0 {
		t.Errorf("the refusal left %d entries in the checkout (%v)", len(entries), err)
	}
}
