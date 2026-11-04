package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/release"
	"github.com/stainless-code/verdict/internal/testenv"
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

// TestWindowsHandoffSpawnFailureRemovesHelper pins that a failed helper spawn
// leaves no <target>.new file in the install dir.
func TestWindowsHandoffSpawnFailureRemovesHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged helper is a POSIX artifact that must fail to exec")
	}
	installDir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TEMP", os.Getenv("TMPDIR"))
	staged := filepath.Join(t.TempDir(), "verdict")
	if err := os.WriteFile(staged, []byte("not an executable image\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(installDir, "verdict")
	det := &detection{
		InstallDir: installDir,
		Receipt:    filepath.Join(installDir, ".verdict-installer"),
		exe:        target,
	}
	req := &Request{GOOS: "windows", EnvSlice: []string{"PATH=/usr/bin:/bin"}}
	_, ref := windowsHandoff(context.Background(), req, det, &release.Result{Binary: staged, SHA256: "abc"}, "1.2.3")
	if ref == nil || !strings.Contains(ref.Message, "cannot start the upgrade helper") {
		t.Fatalf("windowsHandoff refusal = %v, want a spawn failure", ref)
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Errorf("failed spawn left %s.new behind (err = %v); uninstall cannot rmdir the install dir", target, err)
	}
}

func waitForArgv(t *testing.T, path string) []string {
	t.Helper()
	var argv []string
	testenv.Until(t, func() bool {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			return false
		}
		argv = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		return true
	})
	return argv
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
