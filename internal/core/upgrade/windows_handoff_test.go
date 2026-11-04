package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/release"
)

// TestWindowsHandoffStagesHelperBesideTarget pins where the helper image
// lands. The helper renames its own image over the target with a plain
// MoveFileEx (os.Rename), which cannot cross volumes, so the image must sit in
// the target's directory — not in [os.TempDir], which may be on another drive.
// The staged "binary" is a shell script that records the helper argv it was
// started with, which carries the `--new` image path the helper will move.
func TestWindowsHandoffStagesHelperBesideTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged helper is a POSIX shell script")
	}
	installDir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TEMP", os.Getenv("TMPDIR"))
	argvFile := filepath.Join(t.TempDir(), "helper.argv")
	staged := filepath.Join(t.TempDir(), "verdict")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvFile + ".tmp' && mv '" + argvFile + ".tmp' '" + argvFile + "'\n"
	if err := os.WriteFile(staged, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(installDir, "verdict")
	det := &detection{
		InstallDir: installDir,
		Receipt:    filepath.Join(installDir, ".verdict-installer"),
		exe:        target,
	}
	req := &Request{GOOS: "windows", EnvSlice: []string{"PATH=/usr/bin:/bin"}}
	resp, ref := windowsHandoff(context.Background(), req, det, &release.Result{Binary: staged, SHA256: "abc"}, "1.2.3")
	if ref != nil {
		t.Fatalf("windowsHandoff refused: %+v", ref)
	}
	if !resp.Handoff || resp.Target != target {
		t.Errorf("response = %+v; want a handoff targeting %s", resp, target)
	}
	argv := waitForArgv(t, argvFile)
	newImage := argValue(t, argv, "--new")
	if got := filepath.Dir(newImage); got != installDir {
		t.Errorf("helper image %s is in %s; want the install dir %s (a rename across volumes fails on Windows)",
			newImage, got, installDir)
	}
	if got := argValue(t, argv, "--target"); got != target {
		t.Errorf("--target = %s; want %s", got, target)
	}
}

func waitForArgv(t *testing.T, path string) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the helper never recorded its argv at %s", path)
	return nil
}

func argValue(t *testing.T, argv []string, flag string) string {
	t.Helper()
	for i, arg := range argv {
		if arg == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("argv %q has no %s", argv, flag)
	return ""
}
