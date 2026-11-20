package repro

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSig = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCaptureAndRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.py"), []byte("assert False\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := Capture(dir, testSig, "probe.py", "python3 {artifact}", false)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Command != "python3 tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.py" || receipt.Redacted {
		t.Fatalf("receipt = %+v", receipt)
	}
	raw, err := Read(dir, testSig, receipt.Path)
	if err != nil || string(raw) != "assert False\n" {
		t.Fatalf("read = %q, %v", raw, err)
	}
	checkSavedCommand(t, dir)
}

func TestCommandOnlyRedaction(t *testing.T) {
	dir := t.TempDir()
	receipt, err := Capture(dir, testSig, "", "echo ghp_abcdefghijklmnopqrstuvwxyz0123456789", false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Read(dir, testSig, receipt.Path)
	if err != nil || string(raw) != "echo ***REDACTED***\n" || !receipt.Redacted {
		t.Fatalf("receipt=%+v bytes=%q error=%v", receipt, raw, err)
	}
}

func TestCaptureDryRun(t *testing.T) {
	dir := t.TempDir()
	_, err := Capture(dir, testSig, "", "false", true)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("dry run wrote files: %v %v", entries, err)
	}
}

func TestCaptureRejectsUnsafeInputs(t *testing.T) {
	dir := unsafeInputFixture(t)
	for _, tc := range []struct{ name, source, command string }{
		{"escape", filepath.Join("..", "probe.py"), "python3 {artifact}"},
		{"absolute", filepath.Join(dir, "probe.py"), "python3 {artifact}"},
		{"large file", "huge.py", "python3 {artifact}"},
		{"large command", "", strings.Repeat("x", 4097)},
		{"empty command", "", ""},
		{"missing placeholder", "probe.py", "python3 probe.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Capture(dir, testSig, tc.source, tc.command, false); err == nil {
				t.Fatal("accepted unsafe reproduction")
			}
		})
	}
}

func TestReadRejectsOtherFinding(t *testing.T) {
	dir := t.TempDir()
	receipt, err := Capture(dir, testSig, "", "false", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", receipt.Path); err == nil {
		t.Fatal("read another finding's artifact")
	}
}

func checkSavedCommand(t *testing.T, dir string) {
	t.Helper()
	command, err := os.ReadFile(filepath.Join(dir, "tests", testSig+".command.json"))
	if err != nil || !strings.Contains(string(command), `"command":"python3 tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.py"`) {
		t.Fatalf("command = %q, %v", command, err)
	}
}

func unsafeInputFixture(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "bundle")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(parent, "probe.py"), filepath.Join(dir, "probe.py")} {
		if err := os.WriteFile(target, []byte("assert False\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "huge.py"), []byte(strings.Repeat("x", 65537)), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
