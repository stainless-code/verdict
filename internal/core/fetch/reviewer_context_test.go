package fetchcore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReviewerContextSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink replacement requires Unix symlink semantics")
	}
	dir := contextTempDir(t)
	handed := filepath.Join(dir, "handed.txt")
	writeContextFixture(t, handed, "public")
	writeContextFixture(t, filepath.Join(dir, "private.txt"), "NOT_HANDED")
	stale := contextDirEntry(t, dir, "handed.txt")
	if err := os.Remove(handed); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private.txt", handed); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	r := contextReader{root: root, input: handed}
	if err := r.readFile("handed.txt", stale); err == nil {
		t.Fatalf("copied unhanded sibling: %q", r.files[0].body)
	}
}

func TestReviewerContextParentSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires Unix symlink semantics")
	}
	dir := contextTempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	writeContextFixture(t, filepath.Join(target, "evidence.txt"), "NOT_HANDED")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := collectContext([]string{filepath.Join(link, "evidence.txt")}); err == nil {
		t.Fatal("accepted symlink parent")
	}
}

func contextDirEntry(t *testing.T, dir, name string) os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return entry
		}
	}
	t.Fatalf("missing entry %s", name)
	return nil
}

func TestContextRelativeArgumentFromLogicalCWD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("logical PWD symlink fixture requires Unix semantics")
	}
	dir := contextTempDir(t)
	writeContextFixture(t, filepath.Join(dir, "evidence.txt"), "HANDED")
	link := filepath.Join(contextTempDir(t), "logical")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PWD", link)
	files, err := collectContext([]string{"evidence.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || string(files[0].body) != "HANDED" {
		t.Fatalf("relative hand-off = %+v", files)
	}
}
