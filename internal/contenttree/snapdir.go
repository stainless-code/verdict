package contenttree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// SnapshotPrefix names a snapshot directory, and with it the worktree's
// admin directory under the repository's .git/worktrees. The directory name
// carries its run's deadline in Unix seconds — `verdict-snapshot-<deadline>-…`
// — so a later run sweeps only snapshots whose run is certainly over. verify
// and run share it, so either verb sweeps what the other stranded.
const SnapshotPrefix = "verdict-snapshot-"

// decimal and int64Bits are how a snapshot deadline is written and read.
const (
	decimal   = 10
	int64Bits = 64
)

// TempRoot is the host temp root a snapshot lives under, spelled physically,
// refused when it sits inside the checkout at root (TMPDIR pointed into the
// repository, its .git included).
func TempRoot(root string) (string, error) {
	tmp := Resolved(os.TempDir())
	if Inside(root, tmp) {
		return "", jq.NewErrf("the temp directory %s is inside the checkout — set TMPDIR outside it", tmp)
	}
	return tmp, nil
}

// NewSnapshotDir creates a fresh, empty snapshot directory under the
// validated temp root tmp, named for deadline and spelled physically. It
// sits outside the checkout at root on purpose: a snapshot inside it would
// nest a second root in the first, and would leave a full copy of the tree
// in .verdict. A directory that still lands inside the checkout is removed
// and refused.
func NewSnapshotDir(root, tmp string, deadline time.Time) (string, error) {
	dir, err := os.MkdirTemp(tmp, SnapshotPrefix+strconv.FormatInt(deadline.Unix(), decimal)+"-")
	if err != nil {
		return "", jq.Sealed("create the snapshot directory", err)
	}
	// A command and the sandbox name the snapshot by its physical path.
	if physical, err := filepath.EvalSymlinks(dir); err == nil {
		dir = physical
	}
	if Inside(root, dir) {
		if err := os.Remove(dir); err != nil {
			return "", jq.Sealed("remove "+dir, err)
		}
		return "", jq.NewErrf("the temp directory %s is inside the checkout — set TMPDIR outside it", dir)
	}
	return dir, nil
}

// SweepSnapshots removes the snapshot and staging directories under the
// validated temp root tmp whose run's deadline has passed — a run killed
// before its cleanup strands them — and drops their worktree registrations.
// A live run's snapshot is never past its deadline, so concurrent runs are
// safe. Only a real directory directly under tmp, named like a snapshot and
// outside the checkout, is ever a candidate. Best effort: a sweep failure
// costs disk, never this run.
func SweepSnapshots(ctx context.Context, g Git, tmp string, now time.Time) {
	// A glob error is a malformed pattern, and this one is constant.
	candidates, _ := filepath.Glob(filepath.Join(tmp, SnapshotPrefix+"*")) //nolint:errcheck // see above
	swept := false
	for _, dir := range candidates {
		if strandedSnapshot(g.Root, dir, now) && RemoveTree(dir) == nil {
			swept = true
		}
	}
	if swept {
		// Best effort: a failed prune is retried by the next sweep.
		_ = g.Prune(context.WithoutCancel(ctx)) //nolint:errcheck // see above
	}
}

// strandedSnapshot reports whether dir may be swept: a real directory (not
// a link to one), outside the checkout at root, whose name carries a
// deadline that has passed.
func strandedSnapshot(root, dir string, now time.Time) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.IsDir() && !Inside(root, dir) && pastDeadline(dir, now)
}

// pastDeadline reports whether a snapshot directory's name carries a
// deadline that has passed.
func pastDeadline(dir string, now time.Time) bool {
	stamp, _, _ := strings.Cut(strings.TrimPrefix(filepath.Base(dir), SnapshotPrefix), "-")
	deadline, err := strconv.ParseInt(stamp, decimal, int64Bits)
	return err == nil && now.Unix() > deadline
}

// Inside reports whether path is root or below it, both made absolute and
// resolved first — root is often "." (a verb run from the toplevel), which
// [filepath.Rel] cannot compare with an absolute path.
func Inside(root, path string) bool {
	rel, err := filepath.Rel(Resolved(root), Resolved(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolved is path made absolute with every link it holds followed, or as
// absolute as it can be made when it does not exist.
func Resolved(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if target, err := filepath.EvalSymlinks(abs); err == nil {
		return target
	}
	return abs
}

// SnapshotUnder makes a fresh snapshot directory under the validated temp
// root tmp, named for deadline ([NewSnapshotDir]), and snapshots the
// checkout's content into it ([Git.Snapshot]). A failed snapshot leaves
// neither its directory nor its registration behind.
func (g Git) SnapshotUnder(ctx context.Context, tmp string, deadline time.Time) (string, Content, error) {
	dir, err := NewSnapshotDir(g.Root, tmp, deadline)
	if err != nil {
		return "", Content{}, err
	}
	content, err := g.Snapshot(ctx, dir)
	if err != nil {
		//nolint:wrapcheck // the caller names the root around git's reasons
		return "", Content{}, errors.Join(err, g.RemoveCheckout(context.WithoutCancel(ctx), dir))
	}
	return dir, content, nil
}
