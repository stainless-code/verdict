package verifycore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// snapshot is the content a command runs against: a private copy of the
// checkout's content and its tree id. The command runs there, never in the
// user's checkout, so editing, checking out, or restoring files there while
// it runs cannot change what the receipt says was tested.
type snapshot struct {
	dir     string
	content contenttree.Content
	git     contenttree.Git
}

// takeSnapshot copies root's content into a private directory outside the
// checkout and names it, after sweeping snapshots earlier runs stranded. It
// refuses when the content cannot be snapshotted: verify runs a command only
// in a snapshot of the content its receipt binds — running in the checkout
// instead would let the command change what it is credited with testing.
func (st *state) takeSnapshot(ctx context.Context, commander core.Commander, root string, timeout int) (*snapshot, *core.RefusalError) {
	g := contenttree.Git{Cmdr: commander, Root: root, Env: st.req.Environ}
	shared, ref := st.resolveShares(ctx, g)
	if ref != nil {
		return nil, ref
	}
	if st.req.afterShares != nil {
		st.req.afterShares()
	}
	snap, ref := st.snapshotWith(ctx, g, timeout, shared)
	if ref != nil {
		closeShares(shared)
		return nil, ref
	}
	return snap, nil
}

// snapshotWith makes the snapshot the resolved shares join.
func (st *state) snapshotWith(ctx context.Context, g contenttree.Git, timeout int, shared []sharedDir) (*snapshot, *core.RefusalError) {
	// The temp root is validated before anything under it is swept or made:
	// one inside the checkout would make checkout content (a committed
	// directory with a snapshot's name) look like a stranded snapshot.
	tmp, err := tempRoot(g.Root)
	if err == nil {
		sweepSnapshots(ctx, g, tmp, time.Now())
		var snap *snapshot
		if snap, err = st.newSharedSnapshot(ctx, g, tmp, timeout, shared); err == nil {
			st.warnLeftOut(&snap.content)
			return snap, nil
		}
	}
	if ctx.Err() != nil {
		return nil, canceledRefusal()
	}
	return nil, &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
		"verify failed: could not snapshot the content at %s (%v) — verify runs a command only in a "+
			"snapshot of the content its receipt binds, never in your checkout; nothing ran", g.Root, err)}
}

// newSharedSnapshot makes the snapshot, then places the shares in it. They
// join after the snapshot is named and re-hashed, and the tree names index
// paths alone, so no share is ever counted as content.
func (st *state) newSharedSnapshot(ctx context.Context, g contenttree.Git, tmp string, timeout int,
	shared []sharedDir,
) (*snapshot, error) {
	snap, err := newSnapshot(ctx, g, tmp, time.Now().Add(time.Duration(timeout)*time.Second+snapshotSlack))
	if err != nil {
		return nil, err
	}
	if err := exposeShares(snap.dir, shared, st.bindsShares()); err != nil {
		st.removeSnapshot(ctx, snap)
		return nil, err
	}
	st.shared = shared
	return snap, nil
}

// tempRoot is the host temp root a snapshot lives under, spelled physically,
// refused when it sits inside the checkout at root (TMPDIR pointed into the
// repository, its .git included).
func tempRoot(root string) (string, error) {
	tmp := resolved(os.TempDir())
	if inside(root, tmp) {
		return "", jq.NewErrf("the temp directory %s is inside the checkout — set TMPDIR outside it", tmp)
	}
	return tmp, nil
}

// snapshotPrefix names a snapshot directory, and with it the worktree's
// admin directory under the repository's .git/worktrees. The directory name
// carries the run's deadline in Unix seconds — `verdict-snapshot-<deadline>-…`
// — so a later run sweeps only snapshots whose run is certainly over.
const snapshotPrefix = "verdict-snapshot-"

// snapshotSlack is how long past its command's timeout a run may still be
// setting up, recording, or cleaning up its snapshot.
const snapshotSlack = time.Hour

// newSnapshot copies the checkout's content into a fresh private directory
// under the host temp root, like the sandbox's private TMPDIR, and names
// exactly what it copied. It sits outside the checkout on purpose: a
// snapshot inside it would nest a second root in the first, which the
// sandbox's root and git-directory rules cannot both hold, and would leave a
// full copy of the tree in .verdict.
func newSnapshot(ctx context.Context, g contenttree.Git, tmp string, deadline time.Time) (*snapshot, error) {
	dir, err := os.MkdirTemp(tmp, snapshotPrefix+strconv.FormatInt(deadline.Unix(), decimal)+"-")
	if err != nil {
		return nil, jq.Sealed("create the snapshot directory", err)
	}
	// The command and the sandbox name the snapshot by its physical path.
	if physical, ref := canonicalRoot(dir); ref == nil {
		dir = physical
	}
	if inside(g.Root, dir) {
		if err := os.Remove(dir); err != nil {
			return nil, jq.Sealed("remove "+dir, err)
		}
		return nil, jq.NewErrf("the temp directory %s is inside the checkout — set TMPDIR outside it", dir)
	}
	content, err := g.Snapshot(ctx, dir)
	if err != nil {
		// A failed snapshot can leave its directory or registration behind.
		//nolint:wrapcheck // the caller names the root around git's reasons
		return nil, errors.Join(err, g.RemoveCheckout(context.WithoutCancel(ctx), dir))
	}
	return &snapshot{git: g, content: content, dir: dir}, nil
}

// sweepSnapshots removes the snapshot and staging directories under the
// validated temp root tmp whose run's deadline has passed — a run killed
// before its cleanup strands them — and drops their worktree registrations.
// A live run's snapshot is never past its deadline, so concurrent runs are
// safe. Only a real directory directly under tmp, named like a snapshot and
// outside the checkout, is ever a candidate. Best effort: a sweep failure
// costs disk, never this run.
func sweepSnapshots(ctx context.Context, g contenttree.Git, tmp string, now time.Time) {
	// A glob error is a malformed pattern, and this one is constant.
	candidates, _ := filepath.Glob(filepath.Join(tmp, snapshotPrefix+"*")) //nolint:errcheck // see above
	swept := false
	for _, dir := range candidates {
		if strandedSnapshot(g.Root, dir, now) && contenttree.RemoveTree(dir) == nil {
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
	return err == nil && info.IsDir() && !inside(root, dir) && pastDeadline(dir, now)
}

// pastDeadline reports whether a snapshot directory's name carries a
// deadline that has passed.
func pastDeadline(dir string, now time.Time) bool {
	stamp, _, _ := strings.Cut(strings.TrimPrefix(filepath.Base(dir), snapshotPrefix), "-")
	deadline, err := strconv.ParseInt(stamp, decimal, int64Bits)
	return err == nil && now.Unix() > deadline
}

// decimal and int64Bits are how a snapshot deadline is written and read.
const (
	decimal   = 10
	int64Bits = 64
)

// leftOutShown caps each list warnLeftOut names.
const leftOutShown = 10

// warnLeftOut names what the snapshot does not hold, so a command that needs
// it fails loudly instead of mysteriously.
func (st *state) warnLeftOut(c *contenttree.Content) {
	if len(c.Untracked) > 0 {
		st.warn(fmt.Sprintf("the snapshot leaves out %d untracked file(s) the bundle does not cover: %s — "+
			"`git add -N <path>` brings one into the content", len(c.Untracked), shownList(c.Untracked)))
	}
	ignored := slices.DeleteFunc(slices.Clone(c.Ignored), func(top string) bool {
		return slices.ContainsFunc(st.shared, func(s sharedDir) bool { return s.rel == top })
	})
	if len(ignored) > 0 {
		st.warn(fmt.Sprintf("the command runs in a clean checkout of the content, like CI: %d ignored "+
			"entr(ies) here are not in it (%s) — install dependencies in the command itself "+
			"(e.g. npm ci && npm test), or share them read-only with verify.share in %s",
			len(ignored), shownList(ignored), config.ConfigPath))
	}
	if len(c.Gitlinks) > 0 {
		st.warn(fmt.Sprintf("%d submodule(s) are empty in the snapshot: %s", len(c.Gitlinks), shownList(c.Gitlinks)))
	}
	for _, f := range c.Filtered {
		st.warn(filteredNote(f) + " — a PR's VERIFIED claim binds to this run only if the PR's head stores those bytes; " +
			"a fetch-local bundle of this checkout still binds")
	}
}

// lfsDriver is the filter driver git-lfs installs.
const lfsDriver = "lfs"

// filteredNote says what a filter driver verify never ran leaves in the
// snapshot, naming git-lfs for its driver: a commit stores what the driver
// would have made, which only running it could re-derive.
func filteredNote(f contenttree.Filtered) string {
	stored := "the `" + f.Driver + "` filter driver's output"
	if f.Driver == lfsDriver {
		stored = "the Git LFS pointers"
	}
	return fmt.Sprintf("%d path(s) use the `%s` filter, which verify never runs (%s): the snapshot holds their "+
		"working-tree content, not %s a commit stores, so its tree matches only a commit that stores those same bytes",
		len(f.Paths), f.Driver,
		shownList(f.Paths), stored)
}

func shownList(items []string) string {
	if len(items) <= leftOutShown {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:leftOutShown], ", ") + fmt.Sprintf(", and %d more", len(items)-leftOutShown)
}

// inside reports whether path is root or below it, both made absolute and
// resolved first — root is often "." (verify run from the toplevel), which
// [filepath.Rel] cannot compare with an absolute path.
func inside(root, path string) bool {
	rel, err := filepath.Rel(resolved(root), resolved(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolved is path made absolute with every link it holds followed, or as
// absolute as it can be made when it does not exist.
func resolved(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if target, err := filepath.EvalSymlinks(abs); err == nil {
		return target
	}
	return abs
}

// finishSnapshot is a run's deferred cleanup: it removes the snapshot (a
// no-op once the run removed it), releases the held shares, and re-reads the
// warnings into a refusal built before the cleanup ran, so a cleanup warning
// reaches it too.
func (st *state) finishSnapshot(ctx context.Context, snap *snapshot, ref *core.RefusalError) {
	st.removeSnapshot(ctx, snap)
	closeShares(st.shared)
	if ref != nil {
		ref.Warnings = st.warnings
	}
}

// removeSnapshot deletes the snapshot and its registration, warning when any
// of it survives so a stranded directory is visible.
func (st *state) removeSnapshot(ctx context.Context, snap *snapshot) {
	if snap == nil {
		return
	}
	if err := snap.git.RemoveCheckout(context.WithoutCancel(ctx), snap.dir); err != nil {
		st.warn(fmt.Sprintf("snapshot cleanup: %s was not fully removed (%v); remove it by hand", snap.dir, err))
	}
}
