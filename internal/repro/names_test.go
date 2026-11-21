package repro

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInternalWindowsSeparators(t *testing.T) {
	name, err := internalName(`tests\probe.py`)
	if err != nil || name != "tests/probe.py" {
		t.Fatalf("internal name = %q, %v", name, err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "probe.py"), []byte("assert False\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkInternalWindowsRead(t, dir)
}

func checkInternalWindowsRead(t *testing.T, dir string) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	raw, err := read(root, `tests\probe.py`)
	if err != nil || string(raw) != "assert False\n" {
		t.Fatalf("internal read = %q, %v", raw, err)
	}
	if _, err := Capture(dir, testSig, `tests\probe.py`, "python3 {artifact}", false); err == nil {
		t.Fatal("accepted backslashes in agent input")
	}
}
