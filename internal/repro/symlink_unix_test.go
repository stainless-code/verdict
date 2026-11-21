//go:build unix

package repro

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureRejectsSymlinks(t *testing.T) {
	for _, name := range []string{"source", "destination"} {
		t.Run(name, func(t *testing.T) {
			checkSymlinkRejection(t, name)
		})
	}
}

func checkSymlinkRejection(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	source := "probe.py"
	link := filepath.Join(dir, source)
	if name == "destination" {
		source = ""
		link = filepath.Join(dir, "tests")
	}
	if name == "source" {
		target = symlinkSource(t, target)
	}
	relativeTarget, err := filepath.Rel(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relativeTarget, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(dir, testSig, source, "python3 {artifact}", false); err == nil {
		t.Fatal("accepted symlink")
	}
	checkSymlinkDestination(t, name, target)
}

func checkSymlinkDestination(t *testing.T, name, target string) {
	t.Helper()
	if name == "source" {
		return
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote through symlink: %v %v", entries, err)
	}
}

func symlinkSource(t *testing.T, dir string) string {
	t.Helper()
	target := filepath.Join(dir, "real.py")
	if err := os.WriteFile(target, []byte("assert False"), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}
