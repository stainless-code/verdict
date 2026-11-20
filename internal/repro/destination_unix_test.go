//go:build unix

package repro

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDryRunRejectsDestinationSymlinks(t *testing.T) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "artifact", true: "sidecar"}[sidecar], func(t *testing.T) {
			dir := symlinkDestination(t, sidecar)
			if _, err := Capture(dir, testSig, "", "false", true); err == nil {
				t.Fatal("dry run accepted a destination symlink")
			}
		})
	}
}

func TestSidecarSymlinkLeavesNoArtifact(t *testing.T) {
	dir := symlinkDestination(t, true)
	if _, err := Capture(dir, testSig, "", "false", false); err == nil {
		t.Fatal("capture accepted a sidecar symlink")
	}
	path := filepath.Join(dir, "tests", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.sh")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("sidecar symlink left an unaccounted artifact: %v", err)
	}
}

func symlinkDestination(t *testing.T, sidecar bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	name := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.sh"
	content := "false\n"
	if sidecar {
		name = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.command.json"
		content = `{"path":"tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.sh","command":"sh tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.sh","redacted":false}`
	}
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "target"), filepath.Join(dir, "tests", name)); err != nil {
		t.Fatal(err)
	}
	return dir
}
