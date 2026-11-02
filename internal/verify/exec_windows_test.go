//go:build windows

package verify

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExecuteTimeoutKillsTree proves the Windows watchdog: a command that
// ignores TERM and leaves a sleeping child behind is killed as a tree,
// records the shell's SIGKILL code, and writes the timeout marker.
func TestExecuteTimeoutKillsTree(t *testing.T) {
	dir := t.TempDir()
	const vcmd = `trap '' TERM; (trap '' TERM; sleep 30) & sleep 30`
	out, err := execute(t.Context(), dir, vcmd, 1, func(string) {}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if out.exit != sigkillExit || !out.timedOut {
		t.Errorf("outcome = exit %d timedOut %v; want exit %d, timedOut true",
			out.exit, out.timedOut, sigkillExit)
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".verify.timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != timeoutLine(1) {
		t.Errorf("marker = %q; want %q", marker, timeoutLine(1))
	}
}

// TestExecuteOrphanSweepKillsTree proves the post-exit sweep: the parent
// exits 0 while a child outlives it, so the recorded exit stays 0 with
// timedOut true and the orphan marker.
func TestExecuteOrphanSweepKillsTree(t *testing.T) {
	dir := t.TempDir()
	out, err := execute(t.Context(), dir, `(sleep 30 &)`, 10, func(string) {}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if out.exit != 0 || !out.timedOut {
		t.Errorf("outcome = exit %d timedOut %v; want exit 0, timedOut true",
			out.exit, out.timedOut)
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".verify.timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != orphanMarker {
		t.Errorf("marker = %q; want %q", marker, orphanMarker)
	}
}
