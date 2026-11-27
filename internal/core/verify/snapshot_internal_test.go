package verifycore

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
)

// pruneCommander records git calls and answers them all with success.
type pruneCommander struct{ calls [][]string }

func (c *pruneCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.calls = append(c.calls, cmd.Argv)
	return core.Result{}, nil
}

// isPrune reports a hook-free `git worktree prune`.
func isPrune(argv []string) bool {
	_, args, ok := gitcmd.Split(argv)
	return ok && slices.Equal(args, []string{"worktree", "prune"})
}

// snapshotDir is the snapshot directory name a run with this deadline makes.
func snapshotDir(deadline time.Time, suffix string) string {
	return filepath.Join(os.TempDir(), contenttree.SnapshotPrefix+strconv.FormatInt(deadline.Unix(), 10)+"-"+suffix)
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
	contenttree.SweepSnapshots(context.Background(), contenttree.Git{Cmdr: cmdr, Root: t.TempDir()}, contenttree.Resolved(tmp), now)
	for _, gone := range []string{stale, staging} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep (%v)", gone, err)
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("the sweep removed a live snapshot: %v", err)
	}
	if len(cmdr.calls) != 1 || !isPrune(cmdr.calls[0]) {
		t.Errorf("git calls = %q, want one worktree prune", cmdr.calls)
	}
}

// TestSweepNeverTouchesTheCheckout pins the sweep's candidate rule: a
// directory named like a long-stranded snapshot is left alone when it sits
// inside the checkout (TMPDIR pointing into the repository) or is a link,
// so checkout content a PR names that way is never deleted.
func TestSweepNeverTouchesTheCheckout(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(root, "tmpdir")
	victim := filepath.Join(tmp, contenttree.SnapshotPrefix+"1-victim")
	mustMkdir(t, victim)
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(outside, contenttree.SnapshotPrefix+"1-link")); err != nil {
		t.Fatal(err)
	}
	cmdr := &pruneCommander{}
	now := time.Unix(2_000_000_000, 0)
	contenttree.SweepSnapshots(context.Background(), contenttree.Git{Cmdr: cmdr, Root: root}, contenttree.Resolved(tmp), now)
	contenttree.SweepSnapshots(context.Background(), contenttree.Git{Cmdr: cmdr, Root: root}, contenttree.Resolved(outside), now)
	if _, err := os.Stat(filepath.Join(victim, "keep.txt")); err != nil {
		t.Errorf("the sweep deleted checkout content: %v", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("git calls = %q, want none: nothing was swept", cmdr.calls)
	}
}

// TestTakeSnapshotRefusesATempRootInsideTheCheckout pins the guard order:
// verify run from the toplevel (root ".") with TMPDIR inside the checkout —
// a directory of it, or its .git — refuses before sweeping or making
// anything there, and leaves a committed snapshot-named directory alone.
func TestTakeSnapshotRefusesATempRootInsideTheCheckout(t *testing.T) {
	for _, sub := range []string{"tmpdir", ".git"} {
		t.Run(sub, func(t *testing.T) { requireTempRootRefused(t, sub) })
	}
}

// requireTempRootRefused runs takeSnapshot from a checkout's toplevel with
// TMPDIR at its sub directory holding a snapshot-named directory, and checks
// it refuses, runs no git, and deletes nothing.
func requireTempRootRefused(t *testing.T, sub string) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	tmp := filepath.Join(root, sub)
	keep := filepath.Join(tmp, contenttree.SnapshotPrefix+"1-victim", "keep.txt")
	mustMkdir(t, filepath.Dir(keep))
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	cmdr := &pruneCommander{}
	st := &state{req: &Request{}}
	snap, ref := st.takeSnapshot(context.Background(), cmdr, ".", 10)
	if snap != nil || ref == nil || !strings.Contains(ref.Message, "inside the checkout") {
		t.Fatalf("snapshot = %v, refusal = %v; want the inside-the-checkout refusal", snap, ref)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("checkout content under TMPDIR was deleted: %v", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("git calls = %q, want none before the refusal", cmdr.calls)
	}
}
