package verifycore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
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
	tmp, err := contenttree.TempRoot(g.Root)
	if err == nil {
		contenttree.SweepSnapshots(ctx, g, tmp, time.Now())
		var snap *snapshot
		if snap, err = st.newSharedSnapshot(ctx, g, tmp, timeout, shared); err == nil {
			st.warnLeftOut(&snap.content)
			return snap, nil
		}
	}
	if ctx.Err() != nil {
		return nil, canceledRefusal()
	}
	return nil, &core.RefusalError{Code: codeVerifyFailed, Message: core.Msgf(
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

// snapshotSlack is how long past its command's timeout a run may still be
// setting up, recording, or cleaning up its snapshot.
const snapshotSlack = time.Hour

// newSnapshot copies the checkout's content into a fresh private directory
// under the host temp root, like the sandbox's private TMPDIR, and names
// exactly what it copied. It sits outside the checkout on purpose
// ([contenttree.NewSnapshotDir]): a snapshot inside it would nest a second
// root in the first, which the sandbox's root and git-directory rules cannot
// both hold.
func newSnapshot(ctx context.Context, g contenttree.Git, tmp string, deadline time.Time) (*snapshot, error) {
	dir, content, err := g.SnapshotUnder(ctx, tmp, deadline)
	if err != nil {
		//nolint:wrapcheck // the caller names the root around git's reasons
		return nil, err
	}
	return &snapshot{git: g, content: content, dir: dir}, nil
}

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
	produce := "what the configured filter would produce"
	if f.Driver == lfsDriver {
		produce += " (Git LFS commits a pointer)"
	}
	return fmt.Sprintf("%d path(s) use the `%s` filter, which verify never runs (%s): the snapshot holds their "+
		"working-tree content, which may differ from %s, so its tree matches only a commit that stores those "+
		"same bytes", len(f.Paths), f.Driver, shownList(f.Paths), produce)
}

func shownList(items []string) string {
	if len(items) <= leftOutShown {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:leftOutShown], ", ") + fmt.Sprintf(", and %d more", len(items)-leftOutShown)
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
