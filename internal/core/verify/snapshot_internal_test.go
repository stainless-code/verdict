package verifycore

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
)

// pruneCommander records git calls and answers them all with success.
type pruneCommander struct{ calls [][]string }

func (c *pruneCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.calls = append(c.calls, cmd.Argv)
	return core.Result{}, nil
}

// snapshotDir is the snapshot directory name a run with this deadline makes.
func snapshotDir(deadline time.Time, suffix string) string {
	return filepath.Join(os.TempDir(), snapshotPrefix+strconv.FormatInt(deadline.Unix(), decimal)+"-"+suffix)
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// lockedTree makes dir holding one file, then takes write access to dir
// away, as a command that chmods its tree read-only does.
func lockedTree(t *testing.T, dir string) {
	t.Helper()
	mustMkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
}

// TestSweepRemovesOnlySnapshotsPastTheirDeadline pins the stranded-snapshot
// sweep: a snapshot (and its staging directory) whose run's deadline has
// passed is removed even when the command left it read-only, and the
// registrations are pruned, while a snapshot of a run still inside its
// deadline — a concurrent verify — is left alone.
func TestSweepRemovesOnlySnapshotsPastTheirDeadline(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	now := time.Unix(2_000_000_000, 0)
	stale := snapshotDir(now.Add(-time.Second), "1")
	staging := stale + ".staging-2"
	live := snapshotDir(now.Add(time.Minute), "3")
	mustMkdir(t, staging)
	mustMkdir(t, live)
	lockedTree(t, filepath.Join(stale, "locked"))
	cmdr := &pruneCommander{}
	sweepSnapshots(context.Background(), contenttree.Git{Cmdr: cmdr, Root: t.TempDir()}, now)
	for _, gone := range []string{stale, staging} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep (%v)", gone, err)
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("the sweep removed a live snapshot: %v", err)
	}
	if len(cmdr.calls) != 1 || cmdr.calls[0][3] != "worktree" || cmdr.calls[0][4] != "prune" {
		t.Errorf("git calls = %q, want one worktree prune", cmdr.calls)
	}
}
